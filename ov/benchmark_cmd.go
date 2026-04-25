package main

// benchmark_cmd.go — Kong command structs for the `ov benchmark` verb.
//
// Command tree:
//   ov benchmark run <deployment>     — the main iterative benchmark
//   ov benchmark list                 — list past runs under .benchmark/
//   ov benchmark list-runners         — show configured runners
//   ov benchmark report [<run-id>]    — re-render a past report.yml
//   ov benchmark scope                — AI-facing: print current scope.yml
//   ov benchmark last-test-tag        — AI-facing: print prior iter's tag
//   ov benchmark self-evaluate        — AI-facing: rebuild+test, print score

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// BenchmarkCmd is the Kong group struct for `ov benchmark …`.
type BenchmarkCmd struct {
	Run           BenchmarkRunCmd           `cmd:"" help:"Iterate an AI agent against BDD scenarios until plateau"`
	List          BenchmarkListCmd          `cmd:"" help:"List past benchmark runs"`
	ListRunners   BenchmarkListRunnersCmd   `cmd:"" name:"list-runners" help:"Show configured runners from overthink.yml benchmark: section"`
	Report        BenchmarkReportCmd        `cmd:"" help:"Render a past run's report.yml"`
	Scope         BenchmarkScopeCmd         `cmd:"" help:"AI-facing: print the current iteration's scope YAML"`
	LastTestTag   BenchmarkLastTestTagCmd   `cmd:"" name:"last-test-tag" help:"AI-facing: print the prior iteration's image tag"`
	SelfEvaluate  BenchmarkSelfEvaluateCmd  `cmd:"" name:"self-evaluate" help:"AI-facing: rebuild worktree into a throwaway tag and run ov image test"`
}

// ---------------------------------------------------------------------------
// ov benchmark run
// ---------------------------------------------------------------------------

// BenchmarkRunCmd is the main driver.
type BenchmarkRunCmd struct {
	Deployment        string `arg:"" help:"Deployment name from deploy.yml"`
	Runner            string `help:"Runner name (required if >1 configured)"`
	Tags              string `help:"Gherkin tag expression to narrow scenarios"`
	PlateauIterations int    `name:"plateau-iterations" default:"3" help:"Consecutive non-improving iterations that trigger stop"`
	MaxIterations     int    `name:"max-iterations" default:"50" help:"Hard ceiling; 0 = unbounded (plateau still drives exit)"`
	MaxScenarios      int    `name:"max-scenarios" help:"Cap the pending input set"`
	NoMCP             bool   `name:"no-mcp" help:"Bypass MCP endpoint preflight"`
	NoIsolate         bool   `name:"no-isolate" help:"Edit workspace in-place (dangerous; requires TTY confirmation)"`
	DryRun            bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild       bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only scenarios only)"`
	RebuildBaseline   bool   `name:"rebuild-baseline" help:"Rebuild the image before baseline run too"`
	Format            string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
}

// Run executes the benchmark. The full orchestration flow:
//   A. Preflight
//   B. Collect + filter scenarios
//   C. Snapshot (create worktree)
//   D. Baseline run
//   E. Iteration loop (delegates to RunBenchmark)
//   F. Report
func (c *BenchmarkRunCmd) Run() error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getcwd: %w", err)
	}

	// A. Load benchmark config.
	cfg, err := LoadBenchmarkConfig(projectDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("benchmark: overthink.yml has no benchmark: section — see /ov:benchmark")
	}
	runner, err := ResolveRunner(cfg, c.Runner)
	if err != nil {
		return err
	}

	// Locate the deployment node.
	uf, _, err := LoadUnified(projectDir)
	if err != nil {
		return fmt.Errorf("load overthink.yml: %w", err)
	}
	node, err := resolveDeploymentNode(uf, c.Deployment)
	if err != nil {
		return err
	}

	// Pick dispatcher (rejects k8s).
	dispatcher, err := ResolveDispatcher(node, c.Deployment)
	if err != nil {
		return err
	}

	// Preflight.
	if err := dispatcher.Preflight(ctx, runner.Command[0]); err != nil {
		return err
	}

	// C. Create worktree.
	layout := NewRunLayout(projectDir, "")
	if err := CreateWorktree(ctx, layout); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}
	fmt.Fprintf(os.Stderr, "benchmark: run-id %s, worktree %s, branch %s\n",
		layout.RunID, layout.WorktreeDir, layout.Branch)

	// Sync credentials once (preflight phase).
	if err := dispatcher.SyncCredentials(ctx, runner.Credentials); err != nil {
		return fmt.Errorf("sync credentials: %w", err)
	}

	// B. Collect + filter scenarios from the worktree (so we see the
	// frozen pre-AI state).
	// In the real flow we'd use LoadConfig + CollectDescriptions here;
	// for the current code-level implementation this is wired through
	// the existing loaders at the call site. We construct the
	// PreAIScenarios set from the worktree's description collection.
	preAIResults, preFingerprints, preTagFingerprints, err := collectPreAIBaseline(layout, c.Deployment, node)
	if err != nil {
		return fmt.Errorf("collect baseline: %w", err)
	}

	// Wire the loop's loadWorktreeDescriptions seam to the real loader
	// for this run. (Test code overrides this to inject fakes.)
	origLoader := loadWorktreeDescriptions
	loadWorktreeDescriptions = func(opts BenchmarkOpts, layout RunLayout) *LabelDescriptionSet {
		return collectFromWorktree(layout, opts.TargetImage)
	}
	defer func() { loadWorktreeDescriptions = origLoader }()

	// E. Dispatch the iteration loop.
	opts := BenchmarkOpts{
		ProjectDir:         projectDir,
		Deployment:         c.Deployment,
		DeploymentNode:     node,
		Runner:             runner,
		Prompt:             cfg.Prompt,
		TargetImage:        resolveTargetImage(node, c.Deployment),
		Tags:               c.Tags,
		PlateauIterations:  c.PlateauIterations,
		MaxIterations:      c.MaxIterations,
		MaxScenarios:       c.MaxScenarios,
		NoMCP:              c.NoMCP,
		NoIsolate:          c.NoIsolate,
		DryRun:             c.DryRun,
		SkipRebuild:        c.SkipRebuild,
		RebuildBaseline:    c.RebuildBaseline,
		Format:             c.Format,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		PreAIScenarios:     preAIResults,
		PreFingerprints:    preFingerprints,
		PreTagFingerprints: preTagFingerprints,
	}

	report, err := RunBenchmark(ctx, opts, layout, dispatcher)
	if err != nil {
		return err
	}

	// F. Print report.
	printReport(os.Stdout, report, c.Format)
	return nil
}

// resolveDeploymentNode walks the unified file's deployments map + the
// user-level deploy.yml overlay for the named deployment. Accepts the
// plural `deployments:`, singular `deployment:` (unified normalization),
// and the user-level `~/.config/ov/deploy.yml` (via LoadDeployConfig —
// which applies the same deployment-merge rules as every other ov verb).
func resolveDeploymentNode(uf *UnifiedFile, name string) (*DeploymentNode, error) {
	// First: plural form after unified normalization.
	if uf != nil && uf.Deployments != nil && uf.Deployments.Images != nil {
		if node, ok := uf.Deployments.Images[name]; ok {
			return &node, nil
		}
	}
	// Second: singular form if normalization hasn't run yet for this key.
	if uf != nil && uf.DeploymentSingular != nil {
		if node, ok := uf.DeploymentSingular[name]; ok {
			return &node, nil
		}
	}
	// Third: user-level ~/.config/ov/deploy.yml, which is NOT folded into
	// uf. Every deploy-mode ov verb reads it separately; so must we.
	dc, err := LoadDeployConfig()
	if err == nil && dc != nil {
		if node, ok := dc.Deployment[name]; ok {
			return &node, nil
		}
	}
	return nil, fmt.Errorf("benchmark: deployment %q not found in overthink.yml or ~/.config/ov/deploy.yml", name)
}

// resolveTargetImage picks the image name this deployment builds
// against. Schema-v4: node.Image is the direct ref for target: pod.
// Falls back to the deploy NAME when Image is unset, matching the
// conventional `ov-<image>` container naming — a user-level deploy.yml
// overlay often sets only the flags (disposable, etc.) without an
// explicit image, relying on the deploy name and the image name being
// identical (the common case for `ov deploy add <name> <ref>`).
func resolveTargetImage(node *DeploymentNode, name string) string {
	if node != nil && node.Image != "" {
		return node.Image
	}
	return name
}

// collectPreAIBaseline runs `ov image test <current-image> --format
// yaml` against the current image tag (whatever the deploy is running)
// to establish a per-scenario baseline verdict, then pairs it with
// the worktree's description-collected fingerprints.
//
// This is v1's pragmatic implementation — it trusts the deploy's
// current tag as the baseline. `--rebuild-baseline` (when set)
// builds a fresh baseline tag first; that wiring is deferred to a
// follow-up pass because it's orthogonal to the state-machine
// correctness being proven by the rest of the cutover.
func collectPreAIBaseline(layout RunLayout, deployName string, node *DeploymentNode) ([]ScenarioTestResult, map[string]string, map[string]string, error) {
	// Collect fingerprints from the worktree (which equals the pre-AI
	// source since CreateWorktree snapshots HEAD).
	set := collectFromWorktree(layout, resolveTargetImage(node, deployName))
	fingerprints := FingerprintSet(set)
	tagFingerprints := make(map[string]string)
	if set != nil {
		for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
			for _, ld := range sec {
				for sIdx, scenario := range ld.Description.Scenarios {
					expanded := ExpandScenario(scenario)
					for _, es := range expanded {
						id := ScenarioID(ld.Origin, sIdx, es.RowIndex)
						tagFingerprints[id] = FingerprintTags(es.Tags)
					}
				}
			}
		}
	}

	// Synthesize a ScenarioTestResult per scenario, initially as "fail"
	// (pre-AI baseline). A later refinement could probe the live deploy
	// via `ov test <deploy>` to get real status, but for v1 the ID set
	// + fingerprints are what matter for classification.
	var scenarios []ScenarioTestResult
	if set != nil {
		for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
			for _, ld := range sec {
				for sIdx, scenario := range ld.Description.Scenarios {
					expanded := ExpandScenario(scenario)
					for _, es := range expanded {
						id := ScenarioID(ld.Origin, sIdx, es.RowIndex)
						pending := 0
						for _, step := range es.Steps {
							if step.IsPending() {
								pending++
							}
						}
						scenarios = append(scenarios, ScenarioTestResult{
							ID:           id,
							Origin:       ld.Origin,
							Name:         es.Name,
							Tags:         es.Tags,
							Status:       "fail",
							PendingSteps: pending,
						})
					}
				}
			}
		}
	}
	return scenarios, fingerprints, tagFingerprints, nil
}

// collectFromWorktree loads the project config from the worktree dir
// and returns its LabelDescriptionSet for imageName.
func collectFromWorktree(layout RunLayout, imageName string) *LabelDescriptionSet {
	// Load config in the worktree context.
	origWd, _ := os.Getwd()
	if err := os.Chdir(layout.WorktreeDir); err != nil {
		return nil
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg, err := LoadConfig(layout.WorktreeDir)
	if err != nil || cfg == nil {
		return nil
	}
	// CollectDescriptions takes the full layer map; load it. ScanLayers
	// takes the PROJECT dir (it runs LoadUnified internally to pick up
	// layers: blocks inside overthink.yml), not the layers/ subdir —
	// passing the subdir makes LoadUnified fail to find overthink.yml
	// and the legacy fallback looks for <dir>/layers which doesn't
	// exist, yielding an empty map.
	layers, err := ScanLayers(layout.WorktreeDir)
	if err != nil {
		return nil
	}
	return CollectDescriptions(cfg, layers, imageName)
}

// printReport emits the final report in the requested format.
func printReport(w *os.File, r *FinalReport, format string) {
	if r == nil {
		return
	}
	switch format {
	case "yaml":
		// Re-dump the persisted report.yml to stdout.
		data, err := os.ReadFile(filepath.Join(r.RunID, "report.yml"))
		if err == nil {
			_, _ = w.Write(data)
			return
		}
		// Fall through to text if we can't find the file.
		fallthrough
	default:
		fmt.Fprintf(w, "\nBenchmark complete: run %s\n", r.RunID)
		fmt.Fprintf(w, "  target:     %s (%s)\n", r.TargetDeployment, r.TargetImage)
		fmt.Fprintf(w, "  runner:     %s\n", r.Runner)
		fmt.Fprintf(w, "  exit:       %s after %d iteration(s)\n", r.ExitReason, r.IterationsRun)
		fmt.Fprintf(w, "  best score: %d (iter%d)\n", r.BestScore, r.BestIteration)
		fmt.Fprintf(w, "  summary:    solved=%d partial=%d unchanged=%d regressed=%d tampered=%d added=%d (%.1f%% solved)\n",
			r.Summary.Solved, r.Summary.Partial, r.Summary.Unchanged,
			r.Summary.Regressed, r.Summary.Tampered, r.Summary.Added,
			r.Summary.PercentSolved)
	}
}

// ---------------------------------------------------------------------------
// ov benchmark list
// ---------------------------------------------------------------------------

type BenchmarkListCmd struct {
	Format string `enum:"text,yaml" default:"text"`
}

func (c *BenchmarkListCmd) Run() error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	runs, err := ListRuns(context.Background(), projectDir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No benchmark runs found under .benchmark/")
		return nil
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedUTC.After(runs[j].StartedUTC)
	})
	fmt.Printf("%-30s  %-12s  %-20s  %s\n", "RUN_ID", "STATUS", "STARTED (UTC)", "BRANCH")
	for _, r := range runs {
		started := r.StartedUTC.Format(time.RFC3339)
		if r.StartedUTC.IsZero() {
			started = "-"
		}
		branch := fmt.Sprintf("ovbench/%s", r.RunID)
		if !r.BranchExists {
			branch += " (gone)"
		}
		fmt.Printf("%-30s  %-12s  %-20s  %s\n", r.RunID, r.Status, started, branch)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ov benchmark list-runners
// ---------------------------------------------------------------------------

type BenchmarkListRunnersCmd struct{}

func (c *BenchmarkListRunnersCmd) Run() error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadBenchmarkConfig(projectDir)
	if err != nil {
		return err
	}
	PrintRunners(os.Stdout, cfg)
	return nil
}

// ---------------------------------------------------------------------------
// ov benchmark report
// ---------------------------------------------------------------------------

type BenchmarkReportCmd struct {
	RunID  string `arg:"" optional:"" help:"Run ID to render (default: latest)"`
	Format string `enum:"text,yaml" default:"text"`
}

func (c *BenchmarkReportCmd) Run() error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := c.RunID
	if runID == "" {
		runs, err := ListRuns(context.Background(), projectDir)
		if err != nil || len(runs) == 0 {
			return fmt.Errorf("benchmark report: no runs found under .benchmark/")
		}
		runID = runs[0].RunID
	}
	reportPath := filepath.Join(projectDir, ".benchmark", runID, "report.yml")
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", reportPath, err)
	}
	if c.Format == "yaml" {
		_, _ = os.Stdout.Write(data)
		return nil
	}
	// text format: re-print a condensed summary.
	// For v1 we just dump the YAML in text mode too — the human
	// reader can scan it, and the fancy table can be added later.
	_, _ = os.Stdout.Write(data)
	return nil
}

// ---------------------------------------------------------------------------
// ov benchmark scope (AI-facing)
// ---------------------------------------------------------------------------

type BenchmarkScopeCmd struct{}

func (c *BenchmarkScopeCmd) Run() error {
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	return ResolveAndPrintScope(projectDir, os.Stdout)
}

// ---------------------------------------------------------------------------
// ov benchmark last-test-tag (AI-facing)
// ---------------------------------------------------------------------------

type BenchmarkLastTestTagCmd struct {
	TargetImage string `arg:"" optional:"" help:"Target image name (default: read from the run's report.yml)"`
}

func (c *BenchmarkLastTestTagCmd) Run() error {
	img := c.TargetImage
	if img == "" {
		// Try to read target_image from the active run's report.
		projectDir, _ := os.Getwd()
		runID := os.Getenv("OV_BENCHMARK_RUN_ID")
		if runID != "" {
			reportPath := filepath.Join(projectDir, ".benchmark", runID, "report.yml")
			if data, err := os.ReadFile(reportPath); err == nil {
				img = parseTargetImageFromReport(data)
			}
		}
	}
	if img == "" {
		return fmt.Errorf("benchmark: target image not specified and cannot be resolved from report.yml")
	}
	return ResolveLastTestTag(img, os.Stdout)
}

// parseTargetImageFromReport extracts target_image from a report.yml.
// Returns "" if unparseable — the caller falls back to the positional arg.
func parseTargetImageFromReport(data []byte) string {
	var r FinalReport
	// yaml.Unmarshal is tolerant; ignore errors.
	_ = safeUnmarshalReport(data, &r)
	return r.TargetImage
}

// safeUnmarshalReport is a tiny wrapper that isolates the yaml import
// to one function (we already import yaml.v3 elsewhere in the package).
var safeUnmarshalReport = func(data []byte, r *FinalReport) error {
	// Delegate to the benchmark_loop's yaml import via ParseOvTestOutput's
	// sibling — we just need a plain Unmarshal.
	return unmarshalYAML(data, r)
}

// unmarshalYAML is a package-local shim so tests can swap the impl.
// Default uses yaml.v3 directly.
func unmarshalYAML(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

// ---------------------------------------------------------------------------
// ov benchmark self-evaluate (AI-facing; expensive)
// ---------------------------------------------------------------------------

type BenchmarkSelfEvaluateCmd struct {
	TargetImage string `help:"Target image name; defaults to active run's image"`
}

func (c *BenchmarkSelfEvaluateCmd) Run() error {
	ctx := context.Background()
	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := os.Getenv("OV_BENCHMARK_RUN_ID")
	if runID == "" {
		return fmt.Errorf("benchmark self-evaluate: OV_BENCHMARK_RUN_ID not set (run under `ov benchmark run` context)")
	}
	layout := NewRunLayout(projectDir, runID)
	img := c.TargetImage
	if img == "" {
		// Attempt to resolve from report.yml.
		reportPath := filepath.Join(layout.RunDir, "report.yml")
		if data, err := os.ReadFile(reportPath); err == nil {
			img = parseTargetImageFromReport(data)
		}
	}
	if img == "" {
		return fmt.Errorf("benchmark self-evaluate: target image unknown")
	}

	// Throwaway tag — does NOT collide with iter<k> tags used by the loop.
	tag := fmt.Sprintf("ovbench/%s-selfeval-%d:%s", runID, time.Now().Unix(), img)
	fmt.Fprintf(os.Stderr, "benchmark self-evaluate: rebuilding into %s\n", tag)

	buildDur, buildErr := buildImageFn(ctx, layout.WorktreeDir, img, tag, "")
	if buildErr != nil {
		return fmt.Errorf("rebuild: %w", buildErr)
	}
	fmt.Fprintf(os.Stderr, "benchmark self-evaluate: rebuild took %s\n", buildDur)

	// Run ov image test (non-fake path).
	testOut, testDur, testErr := runOvImageTestFn(ctx, tag)
	if testErr != nil {
		return fmt.Errorf("test: %w", testErr)
	}
	fmt.Fprintf(os.Stderr, "benchmark self-evaluate: test took %s\n", testDur)

	parsed, err := ParseOvTestOutput(testOut)
	if err != nil {
		return fmt.Errorf("parse test output: %w", err)
	}
	fmt.Printf("\nSelf-evaluation against %s:\n", tag)
	fmt.Printf("  scenarios: %d total, %d pass, %d fail, %d skip\n",
		parsed.Summary.Total, parsed.Summary.Pass, parsed.Summary.Fail, parsed.Summary.Skip)

	// NOTE: self-evaluate is PURE — it does NOT mutate the active
	// iteration's scope/score. The authoritative score comes from
	// the harness-driven rebuild at the end of each iteration.
	// Clean up the throwaway tag to avoid polluting storage.
	_ = exec.CommandContext(ctx, pickEngineForRun(), "rmi", tag).Run()
	return nil
}

// pickEngineForRun returns the container engine to use — podman by default.
func pickEngineForRun() string {
	return "podman"
}
