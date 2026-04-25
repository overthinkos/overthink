package main

// benchmark_cmd.go — host-side Kong commands for `ov benchmark`.
//
// Command tree:
//   ov benchmark run <pod>            — host forwarder; dispatches into the pod
//   ov benchmark run-local <image>    — pod-side iteration driver (hidden)
//   ov benchmark sync-credentials <pod> — one-shot credential sync
//   ov benchmark list                 — past runs under .benchmark/
//   ov benchmark list-runners         — show configured runners
//   ov benchmark report [<run-id>]    — re-render a past report.yml
//   ov benchmark scope                — AI-facing: print current scope.yml
//   ov benchmark last-test-tag        — AI-facing: print prior iter's tag
//   ov benchmark self-evaluate        — AI-facing: rebuild+test, print score
//
// The thin-host / fat-pod split: the host's `ov benchmark run` is a
// forwarder. It validates the pod, generates a run-id, then execs:
//
//   ov cmd <pod> -- ov benchmark run-local <image> --run-id <id> ...
//
// streaming stdout/stderr through. After the pod completes, the host
// `podman cp`s /workspace/.benchmark/<run-id> back to ./benchmark/<run-id>
// so host-side `ov benchmark list` and `ov benchmark report` work.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// BenchmarkCmd is the Kong group struct for `ov benchmark …`.
type BenchmarkCmd struct {
	Run              BenchmarkRunCmd              `cmd:"" help:"Iterate an AI agent against BDD scenarios until plateau (forwards into the bench-pod)"`
	RunLocal         BenchmarkRunLocalCmd         `cmd:"" name:"run-local" hidden:"" help:"Pod-side iteration driver (host-internal; do not invoke directly)"`
	SyncCredentials  BenchmarkSyncCredentialsCmd  `cmd:"" name:"sync-credentials" help:"One-shot copy of AI-CLI credentials into the bench-pod"`
	List             BenchmarkListCmd             `cmd:"" help:"List past benchmark runs"`
	ListRunners      BenchmarkListRunnersCmd      `cmd:"" name:"list-runners" help:"Show configured runners from overthink.yml benchmark: section"`
	Report           BenchmarkReportCmd           `cmd:"" help:"Render a past run's report.yml"`
	Scope            BenchmarkScopeCmd            `cmd:"" help:"AI-facing: print the current iteration's scope YAML"`
	LastTestTag      BenchmarkLastTestTagCmd      `cmd:"" name:"last-test-tag" help:"AI-facing: print the prior iteration's image tag"`
	SelfEvaluate     BenchmarkSelfEvaluateCmd     `cmd:"" name:"self-evaluate" help:"AI-facing: rebuild current clone into a throwaway tag and run ov image test"`
}

// ---------------------------------------------------------------------------
// ov benchmark run — HOST FORWARDER
// ---------------------------------------------------------------------------

// BenchmarkRunCmd is the host-side forwarder. It validates the pod is
// suitable and dispatches `ov benchmark run-local` inside it.
type BenchmarkRunCmd struct {
	Pod               string `arg:"" help:"Bench-pod deployment name (e.g., bench-pod)"`
	TargetImage       string `name:"target-image" help:"Target image to benchmark (default: the pod's deployment image)"`
	Runner            string `help:"Runner name (required if >1 configured)"`
	Tags              string `help:"Gherkin tag expression to narrow scenarios"`
	PlateauIterations int    `name:"plateau-iterations" default:"3" help:"Consecutive non-improving iterations that trigger stop"`
	MaxIterations     int    `name:"max-iterations" default:"50" help:"Hard ceiling; 0 = unbounded"`
	MaxScenarios      int    `name:"max-scenarios" help:"Cap the pending input set"`
	DryRun            bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild       bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only scenarios only)"`
	Format            string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
}

// Run forwards the benchmark into the pod. Steps:
//   1. Resolve the deployment node + validate it's a target=pod that's running
//   2. Resolve target image (CLI flag wins; else node.Image; else error)
//   3. Generate run-id host-side so we know where to copy back from
//   4. Exec `ov cmd <pod> -- ov benchmark run-local …` with stdout passthrough
//   5. After pod exits: podman cp /workspace/.benchmark/<run-id> -> ./.benchmark/<run-id>
//   6. Propagate pod's exit code
func (c *BenchmarkRunCmd) Run() error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}

	uf, _, err := LoadUnified(projectDir)
	if err != nil {
		return fmt.Errorf("load overthink.yml: %w", err)
	}
	node, err := resolveDeploymentNode(uf, c.Pod)
	if err != nil {
		return err
	}
	if node.Target != "" && node.Target != "pod" && node.Target != "container" {
		return fmt.Errorf("benchmark run: %q has target=%q; only pod-target deployments are supported in the thin-host model", c.Pod, node.Target)
	}

	containerName := "ov-" + c.Pod
	if err := podRunning(ctx, containerName); err != nil {
		return err
	}

	target := c.TargetImage
	if target == "" {
		target = resolveTargetImage(node, c.Pod)
	}
	if target == "" {
		return fmt.Errorf("benchmark run: target image unknown — pass --target-image or set node.image in deploy.yml")
	}

	runID := GenerateRunID()
	fmt.Fprintf(os.Stderr, "benchmark: forwarding to pod %s, run-id %s, target %s\n", c.Pod, runID, target)

	args := []string{"exec", "-i", "-e", "OV_BENCHMARK_RUN_ID=" + runID, containerName,
		"ov", "benchmark", "run-local", target,
		"--run-id", runID,
		"--plateau-iterations", fmt.Sprintf("%d", c.PlateauIterations),
		"--max-iterations", fmt.Sprintf("%d", c.MaxIterations),
		"--format", c.Format,
	}
	if c.Runner != "" {
		args = append(args, "--runner", c.Runner)
	}
	if c.Tags != "" {
		args = append(args, "--tags", c.Tags)
	}
	if c.MaxScenarios > 0 {
		args = append(args, "--max-scenarios", fmt.Sprintf("%d", c.MaxScenarios))
	}
	if c.DryRun {
		args = append(args, "--dry-run")
	}
	if c.SkipRebuild {
		args = append(args, "--skip-rebuild")
	}

	cmd := exec.CommandContext(ctx, "podman", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()

	// Always attempt to mirror the pod's run dir back to the host —
	// even on failure the partial artifacts (build logs, scope.yml,
	// runner.log) are useful for debugging.
	if cpErr := mirrorRunDirFromPod(ctx, containerName, projectDir, runID); cpErr != nil {
		fmt.Fprintf(os.Stderr, "benchmark: mirror run dir back from pod failed (non-fatal): %v\n", cpErr)
	}

	return runErr
}

// mirrorRunDirFromPod runs `podman cp <pod>:/workspace/.benchmark/<run-id>
// <projectDir>/.benchmark/`. Idempotent against re-runs (the dir contents
// land under <projectDir>/.benchmark/<run-id>/).
func mirrorRunDirFromPod(ctx context.Context, containerName, projectDir, runID string) error {
	src := containerName + ":" + filepath.Join(BenchRootDir, runID)
	dst := filepath.Join(projectDir, ".benchmark") + string(filepath.Separator)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "podman", "cp", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("podman cp %s %s: %w\n%s", src, dst, err, string(out))
	}
	return nil
}

// resolveDeploymentNode walks unified deployments + the user-level
// deploy.yml overlay for the named deployment.
func resolveDeploymentNode(uf *UnifiedFile, name string) (*DeploymentNode, error) {
	if uf != nil && uf.Deployments != nil && uf.Deployments.Images != nil {
		if node, ok := uf.Deployments.Images[name]; ok {
			return &node, nil
		}
	}
	if uf != nil && uf.DeploymentSingular != nil {
		if node, ok := uf.DeploymentSingular[name]; ok {
			return &node, nil
		}
	}
	dc, err := LoadDeployConfig()
	if err == nil && dc != nil {
		if node, ok := dc.Deployment[name]; ok {
			return &node, nil
		}
	}
	return nil, fmt.Errorf("benchmark: deployment %q not found in overthink.yml or ~/.config/ov/deploy.yml", name)
}

// resolveTargetImage picks the image name this deployment targets.
func resolveTargetImage(node *DeploymentNode, name string) string {
	if node != nil && node.Image != "" {
		return node.Image
	}
	return name
}

// printReport emits the final report in the requested format.
func printReport(w *os.File, r *FinalReport, format string) {
	if r == nil {
		return
	}
	switch format {
	case "yaml":
		data, err := os.ReadFile(filepath.Join(r.RunID, "report.yml"))
		if err == nil {
			_, _ = w.Write(data)
			return
		}
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
	_, _ = os.Stdout.Write(data)
	return nil
}

// ---------------------------------------------------------------------------
// ov benchmark scope (AI-facing — runs INSIDE the pod)
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
// ov benchmark last-test-tag (AI-facing — runs INSIDE the pod)
// ---------------------------------------------------------------------------

type BenchmarkLastTestTagCmd struct {
	TargetImage string `arg:"" optional:"" help:"Target image name (default: read from the run's report.yml)"`
}

func (c *BenchmarkLastTestTagCmd) Run() error {
	img := c.TargetImage
	if img == "" {
		runID := os.Getenv("OV_BENCHMARK_RUN_ID")
		if runID != "" {
			// Inside the pod, the report.yml lives under /workspace/.benchmark/<run-id>/.
			candidates := []string{
				filepath.Join("/workspace", ".benchmark", runID, "report.yml"),
			}
			projectDir, _ := os.Getwd()
			candidates = append(candidates, filepath.Join(projectDir, ".benchmark", runID, "report.yml"))
			for _, p := range candidates {
				if data, err := os.ReadFile(p); err == nil {
					if v := parseTargetImageFromReport(data); v != "" {
						img = v
						break
					}
				}
			}
		}
	}
	if img == "" {
		return fmt.Errorf("benchmark: target image not specified and cannot be resolved from report.yml")
	}
	return ResolveLastTestTag(img, os.Stdout)
}

// parseTargetImageFromReport extracts target_image from a report.yml.
func parseTargetImageFromReport(data []byte) string {
	var r FinalReport
	_ = safeUnmarshalReport(data, &r)
	return r.TargetImage
}

var safeUnmarshalReport = func(data []byte, r *FinalReport) error {
	return unmarshalYAML(data, r)
}

func unmarshalYAML(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

// ---------------------------------------------------------------------------
// ov benchmark self-evaluate (AI-facing — runs INSIDE the pod)
// ---------------------------------------------------------------------------

// BenchmarkSelfEvaluateCmd rebuilds the current per-run clone into a
// throwaway tag and runs ov image test against it. Pure: does NOT
// mutate the active iteration's score. Authoritative score still
// comes from the harness at end-of-iteration.
type BenchmarkSelfEvaluateCmd struct {
	TargetImage string `help:"Target image name; defaults to active run's image"`
}

func (c *BenchmarkSelfEvaluateCmd) Run() error {
	ctx := context.Background()
	runID := os.Getenv("OV_BENCHMARK_RUN_ID")
	if runID == "" {
		return fmt.Errorf("benchmark self-evaluate: OV_BENCHMARK_RUN_ID not set (run under `ov benchmark run` context)")
	}

	// Layout rooted at /workspace (the pod's bind-mount).
	layout := NewRunLayout(HostRepoMount, runID)
	img := c.TargetImage
	if img == "" {
		reportPath := filepath.Join(layout.RunDir, "report.yml")
		if data, err := os.ReadFile(reportPath); err == nil {
			img = parseTargetImageFromReport(data)
		}
	}
	if img == "" {
		return fmt.Errorf("benchmark self-evaluate: target image unknown")
	}

	tag := fmt.Sprintf("ovbench/%s-selfeval-%d:%s", runID, time.Now().Unix(), img)
	fmt.Fprintf(os.Stderr, "benchmark self-evaluate: rebuilding into %s\n", tag)

	buildDur, buildErr := buildImageFn(ctx, layout.RepoDir, img, tag, "")
	if buildErr != nil {
		return fmt.Errorf("rebuild: %w", buildErr)
	}
	fmt.Fprintf(os.Stderr, "benchmark self-evaluate: rebuild took %s\n", buildDur)

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

	_ = exec.CommandContext(ctx, pickEngineForRun(), "rmi", tag).Run()
	return nil
}

func pickEngineForRun() string {
	return "podman"
}

// avoid import-strings-only error when this file's only direct strings.X
// caller (none currently) is removed; satisfies the linter on incremental edits.
var _ = strings.TrimSpace
