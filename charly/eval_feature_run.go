package main

// eval_feature_run.go — the Agent Driven Evaluation (ADE) acceptance
// runners: `charly box feature run <image>` and `charly eval feature run <deployment>`.
//
// These run an entity's OWN baked Gherkin scenarios (the `description.scenario`
// blocks, shipped in the ai.opencharly.description OCI label) as acceptance
// tests — the RUN half of the `charly feature {list,pending,validate}` family
// (see description_cmd.go). Both reuse the shared scenario engine
// (RunScenarios, description_run.go) and the same target/var resolution as
// `charly eval box` / `charly eval live` (R3); the only new behaviour is surfacing
// scenario results as a first-class pass/fail run and, for the live verb,
// wiring the agent grader so prose-only steps are agent-graded.
//
//   - `charly box feature run <image>`     — BUILD scope. Disposable container
//     (podman run --rm per check); deterministic steps only. A prose-only
//     step has no stable target to probe, so it stays advisory-skip — use the
//     live verb to agent-grade it.
//   - `charly eval feature run <name>`     — DEPLOY scope. Against a running
//     image-backed (pod) deployment; deterministic steps run their checks and
//     prose-only steps bind to the agent grader (unless --no-agent), which
//     probes the live deployment and returns a pass/fail verdict.

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ---------------------------------------------------------------------------
// Shared reporting
// ---------------------------------------------------------------------------

// scenarioFailCount returns how many scenarios ended in a fail verdict.
func scenarioFailCount(results []ScenarioResult) int {
	n := 0
	for _, r := range results {
		if r.Status == TestFail {
			n++
		}
	}
	return n
}

// reportScenarios writes results in the requested format and returns the
// fail count. Reuses the FormatScenarioResults* reporters (description_report.go).
func reportScenarios(w io.Writer, results []ScenarioResult, format string) int {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		_ = FormatScenarioResultsJSON(w, results)
	case "tap":
		FormatScenarioResultsTAP(w, results)
	case "junit":
		_ = FormatScenarioResultsJUnit(w, results)
	default:
		FormatScenarioResultsText(w, results)
	}
	return scenarioFailCount(results)
}

// resolveGraderAI loads the project's `ai:` catalog and resolves the named
// AI (or the sole entry when name is empty). Errors clearly when no AI is
// configured so the operator knows to add one or pass --no-agent.
func resolveGraderAI(dir, name string) (*AIConfig, error) {
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading project for the ai: catalog: %w", err)
	}
	if !ok || uf == nil || len(uf.AI) == 0 {
		return nil, fmt.Errorf("agent grader needs a kind:ai entry (an `ai:` map in eval.yml); add one or pass --no-agent for deterministic-only")
	}
	ai, _, err := ResolveAI(uf.AI, name)
	if err != nil {
		return nil, err
	}
	return ai, nil
}

// scenarioTagFilter parses an optional --tag expression into a TagExpr.
func scenarioTagFilter(tag string) (*TagExpr, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, nil
	}
	return ParseTagExpr(tag)
}

// ---------------------------------------------------------------------------
// charly box feature run <image>  (BUILD scope)
// ---------------------------------------------------------------------------

// BoxFeatureCmd groups `charly box feature run` (and room for future build-scope
// feature verbs). The run-verb lives here, per description_cmd.go's design
// note, so it fits the existing build-mode command hierarchy.
type BoxFeatureCmd struct {
	Run BoxFeatureRunCmd `cmd:"" help:"Run an image's baked Gherkin scenarios against a disposable container (build scope; prose-only steps need a live deployment — see charly eval feature run)"`
}

// BoxFeatureRunCmd: `charly box feature run <image>`. Build-scope acceptance —
// the image's baked scenarios run against a disposable container. Image refs
// resolve against local container storage (never charly.yml), same as
// `charly eval box`.
type BoxFeatureRunCmd struct {
	Image  string `arg:"" help:"Image reference (full ref or short name resolved against local container storage)"`
	Format string `long:"format" default:"text" help:"Output format: text, json, tap, junit"`
	Tag    string `long:"tag" help:"Only run scenarios matching this tag expression (e.g. 'smoke and not slow')"`
	Strict bool   `long:"strict" help:"Treat prose-only (unbound) steps as failures instead of skips"`
}

func (c *BoxFeatureRunCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	imageRef, err := resolveLocalImageRef(rt.RunEngine, c.Image)
	if err != nil {
		return err
	}
	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No scenarios baked into this image (author a description.scenario block).")
		return nil
	}
	filter, err := scenarioTagFilter(c.Tag)
	if err != nil {
		return fmt.Errorf("parsing --tag: %w", err)
	}

	runner := NewRunner(ImageChain(rt.RunEngine, imageRef), ResolveEvalVarsBuild(meta), RunModeImage)
	runner.Distros = meta.Distro
	// Build scope: no live target to probe, so no grader — prose-only steps
	// stay advisory (skip, or fail under --strict).
	results := RunScenarios(context.Background(), runner, meta.Description, filter, c.Strict)

	fmt.Fprintf(os.Stderr, "Feature run (image, build scope): %s\n", imageRef)
	fails := reportScenarios(os.Stdout, results, c.Format)
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}

// ---------------------------------------------------------------------------
// charly eval feature run <deployment>  (DEPLOY scope)
// ---------------------------------------------------------------------------

// EvalFeatureCmd groups `charly eval feature run` under the live-eval hierarchy.
type EvalFeatureCmd struct {
	Run EvalFeatureRunCmd `cmd:"" help:"Run a running deployment's baked Gherkin scenarios as acceptance tests; prose-only steps are agent-graded (Agent Driven Evaluation)"`
}

// EvalFeatureRunCmd: `charly eval feature run <deployment>`. Deploy-scope
// acceptance against a running image-backed (pod) deployment. Deterministic
// steps run their embedded check; prose-only steps bind to the agent grader
// (unless --no-agent), which probes the live deployment for evidence.
type EvalFeatureRunCmd struct {
	Image    string `arg:"" help:"Deployment name (an image-backed pod deployment)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Format   string `long:"format" default:"text" help:"Output format: text, json, tap, junit"`
	Tag      string `long:"tag" help:"Only run scenarios matching this tag expression"`
	AI       string `long:"ai" help:"kind:ai entry to use as the prose-step grader (default: the sole configured ai)"`
	Timeout  string `long:"timeout" help:"Per-grader-call wall-clock cap (Go duration; default 5m or the ai entry's timeout)"`
	NoAgent  bool   `long:"no-agent" help:"Deterministic-only: do not agent-grade prose-only steps (they report as unbound/skip)"`
	Strict   bool   `long:"strict" help:"Treat unbound steps as failures (only meaningful with --no-agent)"`
}

func (c *EvalFeatureRunCmd) Run() error {
	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	dir, _ := os.Getwd()
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
	}

	// Resolve the deploy key → declared image short-name via the shared
	// resolver, then to a registry ref, then read its OCI labels — the same
	// metadata path `charly eval live` uses (R3).
	imageRef := resolveDeployImageName(c.Image, c.Instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return fmt.Errorf("resolving deploy image %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No scenarios baked into this deployment's image (author a description.scenario block).")
		return nil
	}

	// Deploy overlay → runtime variable resolution (real HOST_PORT mappings,
	// container IP, env), same as `charly eval live`.
	var deployOverlay *DeploymentNode
	if dc := loadDeployConfigForRead("charly eval feature run"); dc != nil {
		if entry, ok := dc.Deploy[deployKey(c.Image, c.Instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Deploy[c.Image]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := ResolveEvalVarsRuntime(meta, deployOverlay, engine, c.Image, containerName, c.Instance)

	filter, err := scenarioTagFilter(c.Tag)
	if err != nil {
		return fmt.Errorf("parsing --tag: %w", err)
	}

	runner := NewRunner(ContainerChain(engine, containerName), resolver, RunModeLive)
	runner.Image = c.Image
	runner.Instance = c.Instance
	runner.Distros = meta.Distro

	// Wire the agent grader for prose-only steps unless deterministic-only.
	if !c.NoAgent {
		ai, aerr := resolveGraderAI(dir, c.AI)
		if aerr != nil {
			return aerr
		}
		runner.Grader = &AgentGrader{AI: ai, Target: c.Image, Instance: c.Instance, Timeout: c.Timeout}
	}

	results := RunScenarios(context.Background(), runner, meta.Description, filter, c.Strict)

	grading := "agent-graded prose"
	if c.NoAgent {
		grading = "deterministic-only"
	}
	fmt.Fprintf(os.Stderr, "Feature run (deploy scope, %s): %s (container: %s)\n", grading, meta.Image, containerName)
	fails := reportScenarios(os.Stdout, results, c.Format)
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}
