package main

// benchmark_loop.go — the iteration state machine for `ov benchmark run`.
//
// Responsibilities:
//   - Drive the plateau-bounded iteration loop
//   - Write scope.yml + prompt.md per iteration
//   - Dispatch the runner into the target deploy
//   - Shell out to `ov image build` and `ov image test --format yaml`
//     for per-iteration scoring
//   - Aggregate per-iteration state into report.yml
//
// Every subprocess invocation goes through exported helper vars
// (buildImageFn, runOvImageTestFn) so unit tests can substitute fakes
// without touching the real podman/git/ov machinery.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Opts + state types
// ---------------------------------------------------------------------------

// HarnessOpts are the resolved inputs to a run, populated by
// BenchmarkRunCmd.Run before calling RunHarness.
type HarnessOpts struct {
	ProjectDir       string
	RecipeName       string
	Recipe           *HarnessRecipe
	TargetKind       string // "pod" | "vm" | "host"
	TargetName       string // pod or vm name (empty when host)
	AIName           string
	AI               *AIConfig
	Prompt           string // template text; per-iter substitution at render time
	TargetImage      string
	Tag              string
	PlateauIteration int
	MaxIteration     int // 0 = unbounded (except by plateau)
	MaxScenario      int
	MCPEndpoint      string
	Notes            string // ${NOTES} snapshot at run start
	NoMCP            bool
	NoIsolate        bool
	DryRun           bool
	SkipRebuild      bool
	RebuildBaseline  bool
	Format           string // "text" | "yaml"
	Stdout           *os.File
	Stderr           *os.File

	// PreAIScenario is the frozen set of scenarios the benchmark is
	// scored against. Populated from CollectDescriptions at Phase B.
	// Changes to descriptions post-iteration don't alter this set.
	PreAIScenario []ScenarioTestResult

	// PreFingerprints maps ScenarioID -> body fingerprint at baseline.
	PreFingerprints map[string]string

	// PreTagFingerprints maps ScenarioID -> tag fingerprint at baseline.
	PreTagFingerprints map[string]string
}

// IterationState captures one iteration's outputs.
type IterationState struct {
	K                  int                        `yaml:"k"`
	Score              int                        `yaml:"score"`
	PlateauCounterAfter int                       `yaml:"plateau_counter_after"`
	BuildFailure       bool                       `yaml:"build_failure,omitempty"`
	BuildDuration      string                     `yaml:"build_duration,omitempty"`
	TestDuration       string                     `yaml:"test_duration,omitempty"`
	RunnerDuration     string                     `yaml:"runner_duration,omitempty"`
	CommitSHA          string                     `yaml:"commit_sha,omitempty"`
	Scenario          []ScenarioVerdict          `yaml:"scenario,omitempty"`
	AddedScenario     []string                   `yaml:"added_scenario,omitempty"`
}

// ScenarioVerdict is one scenario's post-iteration outcome.
type ScenarioVerdict struct {
	ID              string  `yaml:"id"`
	Origin          string  `yaml:"origin,omitempty"`
	Verdict         Verdict `yaml:"verdict"`
	Baseline        string  `yaml:"baseline,omitempty"`
	Final           string  `yaml:"final,omitempty"`
	FingerprintPre  string  `yaml:"fingerprint_pre,omitempty"`
	FingerprintPost string  `yaml:"fingerprint_post,omitempty"`
	PendingPre      int     `yaml:"pending_pre,omitempty"`
	PendingPost     int     `yaml:"pending_post,omitempty"`
}

// FinalReport is the aggregate persisted to result.{calver}.yml.
type FinalReport struct {
	Schema           int               `yaml:"schema"`
	Recipe           string            `yaml:"recipe"`
	Calver           string            `yaml:"calver"`
	RunID            string            `yaml:"run_id"`
	AI               string            `yaml:"ai"`
	AIVersion        map[string]string `yaml:"ai_version,omitempty"`
	Where            ReportWhere       `yaml:"where"`
	TargetImage      string            `yaml:"target_image,omitempty"`
	Tag              string            `yaml:"tag,omitempty"`
	PlateauIteration int               `yaml:"plateau_iteration"`
	MaxIteration     int               `yaml:"max_iteration"`
	MCPEndpoint      string            `yaml:"mcp_endpoint,omitempty"`
	StartedUTC       string            `yaml:"started_utc"`
	FinishedUTC      string            `yaml:"finished_utc"`
	ExitReason       string            `yaml:"exit_reason"` // plateau | ceiling | solved-all | interrupted | dry-run
	IterationsRun    int               `yaml:"iterations_run"`
	BestScore        int               `yaml:"best_score"`
	BestIteration    int               `yaml:"best_iteration"`
	OvharnessBranch  string            `yaml:"ovharness_branch,omitempty"`
	Summary          ReportSummary     `yaml:"summary"`
	Iterations       []IterationState  `yaml:"iteration,omitempty"`
	FinalScenario    []ScenarioVerdict `yaml:"final_scenario,omitempty"`
}

// ReportWhere identifies the target a run executed against.
type ReportWhere struct {
	Kind string `yaml:"kind"`           // pod | vm | host
	Name string `yaml:"name,omitempty"` // pod or vm name; absent for host
}

// ReportSummary is the aggregate metrics panel.
type ReportSummary struct {
	Input          int     `yaml:"input"`
	Solved         int     `yaml:"solved"`
	Partial        int     `yaml:"partial"`
	Unchanged      int     `yaml:"unchanged"`
	Regressed      int     `yaml:"regressed"`
	Tampered       int     `yaml:"tampered"`
	Added          int     `yaml:"added"`
	PercentSolved  float64 `yaml:"percent_solved"`
}

// ---------------------------------------------------------------------------
// Subprocess seams (test hooks)
// ---------------------------------------------------------------------------

// findOvForBenchmark returns the path to the ov binary the benchmark
// should re-invoke for sub-commands. Prefers os.Executable() so the
// harness's own build is used — the host's `/usr/bin/ov` may be older
// and lack flags this binary depends on (e.g., `ov image test
// --format yaml`). Falls back to "ov" on PATH if os.Executable fails.
func findOvForBenchmark() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "ov"
}

// buildImageFn builds the target image from the per-run repo into tag.
// Returns the build's wall-clock duration and any error. Swappable for tests.
var buildImageFn = func(ctx context.Context, repoDir, image, tag, logPath string) (time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findOvForBenchmark(), "-C", repoDir,
		"image", "build", image, "--tag", tag)
	if logPath != "" {
		f, err := os.Create(logPath)
		if err == nil {
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close()
		}
	}
	err := cmd.Run()
	return time.Since(start), err
}

// runOvImageTestFn shells out to `ov image test <tag> --format yaml`
// and returns the raw YAML bytes + duration. Swappable for tests.
var runOvImageTestFn = func(ctx context.Context, tag string) ([]byte, time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findOvForBenchmark(), "image", "test", tag, "--format", "yaml")
	out, err := cmd.Output()
	return out, time.Since(start), err
}

// runRunnerFn invokes the runner directly inside the pod. cwd is set to
// the per-run repo clone (layout.RepoDir). env is merged into os.Environ
// (overrides win). Swappable for tests.
//
// No more Dispatcher abstraction: this command always runs *inside* the
// pod (under `ov benchmark run-local`), so the runner is just a local
// exec — no podman exec, no ssh, no host/pod marshaling.
var runRunnerFn = func(ctx context.Context, layout RunLayout, argv []string, env map[string]string, logPath string) (time.Duration, error) {
	start := time.Now()
	if len(argv) == 0 {
		return 0, fmt.Errorf("benchmark: runner has empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = layout.RepoDir
	cmd.Env = mergeOsEnv(env)
	if logPath != "" {
		f, ferr := os.Create(logPath)
		if ferr == nil {
			cmd.Stdout = f
			cmd.Stderr = f
			defer f.Close()
		}
	}
	err := cmd.Run()
	return time.Since(start), err
}

// mergeOsEnv returns os.Environ() merged with overrides from env.
// Overrides win on key collision.
func mergeOsEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	out := append([]string(nil), os.Environ()...)
	for k, v := range env {
		prefix := k + "="
		replaced := false
		for i, e := range out {
			if strings.HasPrefix(e, prefix) {
				out[i] = prefix + v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, prefix+v)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// RunHarness — the main entry point
// ---------------------------------------------------------------------------

// RunHarness executes the iteration loop against opts and returns
// the final report. Caller is responsible for creating the per-run
// clone and for collecting the pre-AI baseline (those happen in
// BenchmarkRunLocalCmd.Run, inside the pod).
func RunHarness(ctx context.Context, opts HarnessOpts, layout RunLayout) (*FinalReport, error) {
	started := time.Now().UTC()
	report := &FinalReport{
		Schema:           1,
		Recipe:           opts.RecipeName,
		Calver:           ComputeCalVer(),
		RunID:            layout.RunID,
		AI:               opts.AIName,
		Where:            ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		TargetImage:      opts.TargetImage,
		Tag:              opts.Tag,
		PlateauIteration: opts.PlateauIteration,
		MaxIteration:     opts.MaxIteration,
		MCPEndpoint:      opts.MCPEndpoint,
		OvharnessBranch:  layout.Branch,
		StartedUTC:       started.Format(time.RFC3339),
	}

	plateauCounter := 0
	bestScore := 0
	bestIteration := 0
	preIDs := scenarioIDSet(opts.PreAIScenario)

	// Iteration loop.
	for k := 1; opts.MaxIteration == 0 || k <= opts.MaxIteration; k++ {
		// Compute still-unsolved.
		unsolved := stillUnsolved(opts.PreAIScenario, report.Iterations)
		if len(unsolved) == 0 && k > 1 {
			report.ExitReason = "solved-all"
			break
		}

		iterState, err := runOneIteration(ctx, opts, layout, k, unsolved, report)
		if err != nil {
			// A genuine infrastructure error (not a build/test failure) aborts.
			return report, fmt.Errorf("iter%d: %w", k, err)
		}
		report.Iterations = append(report.Iterations, iterState)

		// Dry-run exits after iter 1 writes its scope+prompt.
		if opts.DryRun {
			report.ExitReason = "dry-run"
			break
		}

		// Plateau bookkeeping.
		if iterState.Score > bestScore {
			bestScore = iterState.Score
			bestIteration = k
			plateauCounter = 0
		} else {
			plateauCounter++
		}
		iterState.PlateauCounterAfter = plateauCounter
		// Re-persist with updated plateau counter (writeIterScore is
		// called inside runOneIteration with counter=0 placeholder;
		// fix in place by rewriting).
		report.Iterations[len(report.Iterations)-1].PlateauCounterAfter = plateauCounter
		_ = writeIterScore(layout, k, report.Iterations[len(report.Iterations)-1])

		// Plateau exit.
		if plateauCounter >= opts.PlateauIteration {
			report.ExitReason = "plateau"
			break
		}

		// Ceiling exit.
		if opts.MaxIteration != 0 && k >= opts.MaxIteration {
			report.ExitReason = "ceiling"
			break
		}
	}

	finished := time.Now().UTC()
	report.FinishedUTC = finished.Format(time.RFC3339)
	report.BestScore = bestScore
	report.BestIteration = bestIteration
	report.IterationsRun = len(report.Iterations)
	if report.ExitReason == "" {
		// Loop exited without hitting a condition above (e.g. unbounded
		// ceiling + no plateau yet), only possible when ctx cancelled.
		report.ExitReason = "interrupted"
	}

	// Aggregate per-scenario final verdicts (from the LAST iteration's
	// scenario verdicts, plus `added` scenarios from the final reload).
	if n := len(report.Iterations); n > 0 {
		report.FinalScenario = report.Iterations[n-1].Scenario
	}
	report.Summary = computeSummary(report.FinalScenario, len(preIDs))

	// Persist the report.
	if err := writeReport(layout, report); err != nil {
		return report, fmt.Errorf("write report: %w", err)
	}
	return report, nil
}

// scenarioIDSet returns a set of scenario IDs from a frozen list.
func scenarioIDSet(scenarios []ScenarioTestResult) map[string]bool {
	out := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		out[s.ID] = true
	}
	return out
}

// stillUnsolved returns scenario IDs still in play (not in verdict
// Solved or Tampered) across the run so far.
func stillUnsolved(pre []ScenarioTestResult, iters []IterationState) []ScenarioTestResult {
	// Build a map of latest verdict per scenario ID.
	latest := make(map[string]Verdict)
	for _, it := range iters {
		for _, v := range it.Scenario {
			latest[v.ID] = v.Verdict
		}
	}
	var out []ScenarioTestResult
	for _, s := range pre {
		v, seen := latest[s.ID]
		if !seen {
			out = append(out, s)
			continue
		}
		if v == VerdictSolved || v == VerdictTampered {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ---------------------------------------------------------------------------
// runOneIteration
// ---------------------------------------------------------------------------

// runOneIteration drives a single pass through Phase E of the flow.
func runOneIteration(
	ctx context.Context,
	opts HarnessOpts,
	layout RunLayout,
	k int,
	unsolved []ScenarioTestResult,
	reportSoFar *FinalReport,
) (IterationState, error) {
	iter := IterationState{K: k}
	iterDir := layout.IterDir(k)
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return iter, fmt.Errorf("mkdir iter%d: %w", k, err)
	}

	// 1. Write scope.yml
	scope := renderScope(opts, layout, k, reportSoFar, unsolved)
	if err := writeScope(layout, k, scope); err != nil {
		return iter, fmt.Errorf("write scope: %w", err)
	}

	// 2. Render + write prompt.md
	notesSnap := opts.Notes
	if recipeName := opts.RecipeName; recipeName != "" && opts.Recipe != nil && opts.Recipe.NotesEnabled() {
		// Refresh per iteration so the AI sees notes the previous iter may have appended.
		if body, _ := ReadNote(opts.ProjectDir, recipeName); body != "" {
			notesSnap = body
		}
	}
	mcp := opts.MCPEndpoint
	if mcp == "" {
		mcp = DefaultMCPEndpoint
	}
	substCtx := &SubstContext{
		RunID:            layout.RunID,
		RecipeName:       opts.RecipeName,
		AIName:           opts.AIName,
		WorkspacePath:    layout.RepoDir,
		TargetImage:      opts.TargetImage,
		TargetKind:       opts.TargetKind,
		TargetName:       opts.TargetName,
		Iteration:        k,
		MaxIteration:     opts.MaxIteration,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   computePlateauSoFar(reportSoFar),
		BestScore:        reportSoFar.BestScore,
		MCPEndpoint:      mcp,
		Notes:            notesSnap,
		Tag:              opts.Tag,
		Timeout:          opts.AI.Timeout,
	}
	if opts.Recipe != nil {
		substCtx.AppendEnv(opts.Recipe.Env)
	}
	if opts.AI != nil {
		substCtx.AppendEnv(opts.AI.Env)
	}
	promptText := Substitute(opts.Prompt, substCtx)
	if err := writePrompt(layout, k, promptText); err != nil {
		return iter, fmt.Errorf("write prompt: %w", err)
	}

	// Dry-run exits here without invoking the runner.
	if opts.DryRun {
		return iter, nil
	}

	// 3. Dispatch the runner.
	runnerArgv, runnerEnv := renderRunnerInvocation(opts, substCtx, promptText, iterDir)
	runnerLog := filepath.Join(iterDir, "runner.log")

	// Apply per-runner timeout.
	timeout, _ := ParseAITimeout(opts.AI.Timeout)
	runnerCtx, cancelRunner := context.WithTimeout(ctx, timeout)
	runnerDur, runnerErr := runRunnerFn(runnerCtx, layout, runnerArgv, runnerEnv, runnerLog)
	cancelRunner()
	iter.RunnerDuration = runnerDur.String()
	if runnerErr != nil {
		// Log-only; runner failures don't abort the loop (the plateau
		// counter will catch a persistent bad runner).
		fmt.Fprintf(opts.Stderr, "iter%d: runner exited with error: %v (continuing)\n", k, runnerErr)
	}

	// 4. Rebuild (unless --skip-rebuild). The build's --tag flag takes
	// just the TAG portion (no `/`, no `:`); the full image ref is
	// reconstructed for the test step. Using a "/" or ":" inside --tag
	// makes podman build fail with "invalid reference format".
	iterTagSuffix := fmt.Sprintf("ovharness-%s-iter%d", layout.RunID, k)
	iterRef := fmt.Sprintf("ghcr.io/overthinkos/%s:%s", opts.TargetImage, iterTagSuffix)
	var testOut []byte
	if !opts.SkipRebuild {
		buildLog := filepath.Join(iterDir, "build.log")
		buildDur, buildErr := buildImageFn(ctx, layout.RepoDir, opts.TargetImage, iterTagSuffix, buildLog)
		iter.BuildDuration = buildDur.String()
		if buildErr != nil {
			iter.BuildFailure = true
			// Score is unchanged (== prior iteration's score); no test run.
			iter.Score = priorScore(reportSoFar)
			iter.Scenario = priorScenarios(reportSoFar)
			if err := commitIterationBestEffort(ctx, layout, k, iter); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}

		// 5. Evaluate via ov image test (full ref, not just the tag).
		testStart := time.Now()
		out, _, testErr := runOvImageTestFn(ctx, iterRef)
		iter.TestDuration = time.Since(testStart).String()
		if testErr != nil {
			// Test runner failure; treat as build failure (score unchanged).
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Scenario = priorScenarios(reportSoFar)
			fmt.Fprintf(opts.Stderr, "iter%d: ov image test: %v\n", k, testErr)
			if err := commitIterationBestEffort(ctx, layout, k, iter); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}
		testOut = out
		// Persist raw test output.
		_ = os.WriteFile(filepath.Join(iterDir, "test-output.yaml"), out, 0o644)
	}

	// 6. Parse + classify.
	parsed, err := ParseOvTestOutput(testOut)
	if err != nil {
		return iter, fmt.Errorf("parse test output: %w", err)
	}

	// Reload fingerprints from the per-run clone's current state (the
	// AI may have edited scenario bodies; we re-scan to compute deltas).
	postSet := loadDescriptionsFromDir(layout.RepoDir, opts.TargetImage)
	postFingerprints := FingerprintSet(postSet)
	postTagFingerprints := collectTagFingerprints(postSet)

	// Classify each pre-AI scenario.
	postByID := parsed.ScenarioByID()
	for _, pre := range opts.PreAIScenario {
		preState := ScenarioState{
			Present:        true,
			Fingerprint:    opts.PreFingerprints[pre.ID],
			Status:         pre.Status,
			PendingSteps:   pre.PendingSteps,
			TagFingerprint: opts.PreTagFingerprints[pre.ID],
		}
		var postState ScenarioState
		if post, ok := postByID[pre.ID]; ok {
			postState = ScenarioState{
				Present:        true,
				Fingerprint:    postFingerprints[pre.ID],
				Status:         post.Status,
				PendingSteps:   post.PendingSteps,
				TagFingerprint: postTagFingerprints[pre.ID],
			}
		}
		v := Classify(preState, postState)
		iter.Scenario = append(iter.Scenario, ScenarioVerdict{
			ID:              pre.ID,
			Origin:          pre.Origin,
			Verdict:         v,
			Baseline:        pre.Status,
			Final:           postState.Status,
			FingerprintPre:  preState.Fingerprint,
			FingerprintPost: postState.Fingerprint,
			PendingPre:      preState.PendingSteps,
			PendingPost:     postState.PendingSteps,
		})
		if v == VerdictSolved {
			iter.Score++
		}
	}

	// Identify `added` scenarios (present post but not in pre).
	preIDs := scenarioIDSet(opts.PreAIScenario)
	for id := range postByID {
		if !preIDs[id] {
			iter.AddedScenario = append(iter.AddedScenario, id)
		}
	}

	// 7. Commit iteration on the worktree branch.
	solvedIDs := collectSolvedIDs(iter.Scenario)
	if err := commitIterationBestEffort(ctx, layout, k, iter); err != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
	}
	_ = solvedIDs // consumed inside commitIterationBestEffort

	// Persist per-iter score.yml.
	if err := writeIterScore(layout, k, iter); err != nil {
		return iter, fmt.Errorf("write iter score: %w", err)
	}
	return iter, nil
}

// commitIterationBestEffort commits the iteration in the per-run clone
// and stashes the SHA. Errors are non-fatal — the commit step is
// audit-trail; a hook abort doesn't invalidate the iteration's scoring.
func commitIterationBestEffort(ctx context.Context, layout RunLayout, k int, iter IterationState) error {
	solved := collectSolvedIDs(iter.Scenario)
	sha, err := CommitIterationInRepo(ctx, layout, k, iter.Score, solved)
	if err != nil {
		return err
	}
	iter.CommitSHA = sha
	return nil
}

// collectSolvedIDs returns the IDs with Verdict == Solved.
func collectSolvedIDs(v []ScenarioVerdict) []string {
	var out []string
	for _, s := range v {
		if s.Verdict == VerdictSolved {
			out = append(out, s.ID)
		}
	}
	return out
}

// priorScore returns the last iteration's score or 0 for the first iteration.
func priorScore(r *FinalReport) int {
	if r == nil || len(r.Iterations) == 0 {
		return 0
	}
	return r.Iterations[len(r.Iterations)-1].Score
}

// priorScenarios returns the last iteration's scenario slice.
func priorScenarios(r *FinalReport) []ScenarioVerdict {
	if r == nil || len(r.Iterations) == 0 {
		return nil
	}
	return r.Iterations[len(r.Iterations)-1].Scenario
}

// computePlateauSoFar returns the plateau counter value going into iter k+1.
func computePlateauSoFar(r *FinalReport) int {
	if r == nil || len(r.Iterations) == 0 {
		return 0
	}
	return r.Iterations[len(r.Iterations)-1].PlateauCounterAfter
}

// ---------------------------------------------------------------------------
// Scope rendering
// ---------------------------------------------------------------------------

// Scope is the YAML-serializable form of /workspace/.harness/scope.yml.
type HarnessScope struct {
	RunID            string              `yaml:"run_id"`
	Recipe           string              `yaml:"recipe,omitempty"`
	AI               string              `yaml:"ai,omitempty"`
	Iteration        int                 `yaml:"iteration"`
	MaxIteration     int                 `yaml:"max_iteration"`
	PlateauIteration int                 `yaml:"plateau_iteration"`
	PlateauCounter   int                 `yaml:"plateau_counter"`
	BestScore        int                 `yaml:"best_score"`
	TargetImage      string              `yaml:"target_image"`
	Where            ReportWhere         `yaml:"where"`
	Tag              string              `yaml:"tag,omitempty"`
	History          []ScopeHistoryEntry `yaml:"history,omitempty"`
	Scenario         []ScopeScenario     `yaml:"scenario,omitempty"`
}

// ScopeHistoryEntry summarizes one past iteration for the AI.
type ScopeHistoryEntry struct {
	K                  int      `yaml:"k"`
	Score              int      `yaml:"score"`
	SolvedIDs          []string `yaml:"solved_ids,omitempty"`
	NewlySolvedIDs     []string `yaml:"newly_solved_ids,omitempty"`
	Runtime            string   `yaml:"runtime,omitempty"`
	PlateauCounterAfter int     `yaml:"plateau_counter_after,omitempty"`
}

// ScopeScenario is one still-unsolved scenario as the AI sees it.
type ScopeScenario struct {
	ID              string                     `yaml:"id"`
	Origin          string                     `yaml:"origin,omitempty"`
	BaselineVerdict string                     `yaml:"baseline_verdict,omitempty"`
	Trajectory      []ScopeScenarioTrajectory  `yaml:"trajectory,omitempty"`
	PendingCurrent  int                        `yaml:"pending_steps_current,omitempty"`
}

// ScopeScenarioTrajectory records one iteration's verdict + pending delta.
type ScopeScenarioTrajectory struct {
	K                 int     `yaml:"k"`
	Verdict           Verdict `yaml:"verdict"`
	PendingStepsDelta int     `yaml:"pending_steps_delta,omitempty"`
}

// renderScope builds the Scope that iteration k will see.
func renderScope(opts HarnessOpts, layout RunLayout, k int, reportSoFar *FinalReport, unsolved []ScenarioTestResult) *HarnessScope {
	s := &HarnessScope{
		RunID:            layout.RunID,
		Recipe:           opts.RecipeName,
		AI:               opts.AIName,
		Iteration:        k,
		MaxIteration:     opts.MaxIteration,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   computePlateauSoFar(reportSoFar),
		BestScore:        reportSoFar.BestScore,
		TargetImage:      opts.TargetImage,
		Where:            ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		Tag:              opts.Tag,
	}
	for _, h := range reportSoFar.Iterations {
		s.History = append(s.History, ScopeHistoryEntry{
			K:                   h.K,
			Score:               h.Score,
			SolvedIDs:           collectSolvedIDs(h.Scenario),
			Runtime:             h.RunnerDuration,
			PlateauCounterAfter: h.PlateauCounterAfter,
		})
	}
	for _, u := range unsolved {
		s.Scenario = append(s.Scenario, ScopeScenario{
			ID:              u.ID,
			Origin:          u.Origin,
			BaselineVerdict: u.Status,
			PendingCurrent:  u.PendingSteps,
		})
	}
	return s
}

// writeScope writes scope.yml to iter<k>/ AND mirrors to the per-run
// clone at <RepoDir>/.harness/scope.yml (the path `ov benchmark scope`
// reads from inside the pod when the AI's cwd is the clone).
func writeScope(layout RunLayout, k int, s *HarnessScope) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	iterPath := filepath.Join(layout.IterDir(k), "scope.yml")
	if err := os.WriteFile(iterPath, data, 0o644); err != nil {
		return err
	}
	mirrorDir := filepath.Join(layout.RepoDir, ".benchmark")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mirrorDir, "scope.yml"), data, 0o644)
}

// writePrompt mirrors prompt.md alongside scope.yml.
func writePrompt(layout RunLayout, k int, text string) error {
	iterPath := filepath.Join(layout.IterDir(k), "prompt.md")
	if err := os.WriteFile(iterPath, []byte(text), 0o644); err != nil {
		return err
	}
	mirrorDir := filepath.Join(layout.RepoDir, ".benchmark")
	if err := os.MkdirAll(mirrorDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(mirrorDir, "prompt.md"), []byte(text), 0o644)
}

// writeIterScore writes iter<k>/score.yml.
func writeIterScore(layout RunLayout, k int, state IterationState) error {
	data, err := yaml.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(layout.IterDir(k), "score.yml"), data, 0o644)
}

// writeReport writes the aggregated result.{calver}.yml under
// .harness/<recipe>/results/. The Calver is set on the FinalReport at
// RunHarness entry; if missing for any reason, generate one now.
func writeReport(layout RunLayout, r *FinalReport) error {
	if r.Calver == "" {
		r.Calver = ComputeCalVer()
	}
	if r.Recipe == "" {
		r.Recipe = layout.Recipe
	}
	if r.Schema == 0 {
		r.Schema = 1
	}
	data, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(layout.ResultsDir(), 0o755); err != nil {
		return err
	}
	resultPath := filepath.Join(layout.ResultsDir(), "result."+r.Calver+".yml")
	return os.WriteFile(resultPath, data, 0o644)
}

// printHarnessReport renders a summary of the run to stdout for the
// host-side caller. Mirrors the legacy printReport behavior.
func printHarnessReport(w *os.File, r *FinalReport, format string) {
	if format == "yaml" {
		data, _ := yaml.Marshal(r)
		_, _ = w.Write(data)
		return
	}
	fmt.Fprintf(w, "harness: recipe=%s ai=%s exit=%s iterations=%d best=%d/%d\n",
		r.Recipe, r.AI, r.ExitReason, r.IterationsRun, r.BestScore, r.Summary.Input)
	fmt.Fprintf(w, "  result: .harness/%s/results/result.%s.yml\n", r.Recipe, r.Calver)
	fmt.Fprintf(w, "  branch: %s\n", r.OvharnessBranch)
}

// ---------------------------------------------------------------------------
// Runner argv + env rendering
// ---------------------------------------------------------------------------

// renderRunnerInvocation prepares the argv + env the dispatcher will
// execute, per the runner's prompt_via mode.
func renderRunnerInvocation(opts HarnessOpts, substCtx *SubstContext, promptText, iterDir string) ([]string, map[string]string) {
	// Write prompt-file if the runner uses that delivery mode.
	if opts.AI.PromptVia == "file" {
		path := filepath.Join(iterDir, "prompt-arg.md")
		_ = os.WriteFile(path, []byte(promptText), 0o644)
		substCtx.PromptFile = path
	}
	if opts.AI.PromptVia == "argv" || opts.AI.PromptVia == "" {
		substCtx.Prompt = promptText
	}

	argv := SubstituteArgv(opts.AI.Command, substCtx)
	env := SubstituteEnv(opts.AI.Env, substCtx)
	if env == nil {
		env = make(map[string]string)
	}
	env["OV_HARNESS_RUN_ID"] = substCtx.RunID
	env["OV_HARNESS_ITERATION"] = fmt.Sprintf("%d", substCtx.Iteration)
	env["OV_HARNESS_RECIPE"] = substCtx.RecipeName
	env["OV_HARNESS_AI"] = substCtx.AIName
	env["OV_HARNESS_TARGET_KIND"] = substCtx.TargetKind
	env["OV_HARNESS_TARGET_NAME"] = substCtx.TargetName
	return argv, env
}

// ---------------------------------------------------------------------------
// Post-AI description reload + tag-fingerprint collection
// ---------------------------------------------------------------------------

// collectTagFingerprints mirrors FingerprintSet but for tags only.
func collectTagFingerprints(set *LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
		for _, ld := range sec {
			for sIdx, scenario := range ld.Description.Scenario {
				expanded := ExpandScenario(scenario)
				for _, es := range expanded {
					id := ScenarioID(ld.Origin, sIdx, es.RowIndex)
					out[id] = FingerprintTags(es.Tag)
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Summary aggregation
// ---------------------------------------------------------------------------

// computeSummary tallies per-verdict counts from the final scenario set.
func computeSummary(scenarios []ScenarioVerdict, total int) ReportSummary {
	s := ReportSummary{Input: total}
	for _, v := range scenarios {
		switch v.Verdict {
		case VerdictSolved:
			s.Solved++
		case VerdictPartial:
			s.Partial++
		case VerdictUnchanged:
			s.Unchanged++
		case VerdictRegressed:
			s.Regressed++
		case VerdictTampered:
			s.Tampered++
		case VerdictAdded:
			s.Added++
		}
	}
	if total > 0 {
		s.PercentSolved = float64(s.Solved) / float64(total) * 100.0
	}
	return s
}

// ---------------------------------------------------------------------------
// Scope-from-env — `ov benchmark scope` handler
// ---------------------------------------------------------------------------

// ResolveAndPrintScope reads OV_BENCHMARK_RUN_ID from the environment,
// locates the active scope.yml inside the per-run clone, and writes
// its contents to out. The AI-facing `ov benchmark scope` command
// dispatches here.
//
// Path resolution: prefers the per-run clone's mirror at
// /workspace/.harness/<run-id>/repo/.harness/scope.yml, with
// fallbacks for the AI's possibly-different cwd and a host-side run.
func ResolveAndPrintScope(projectDir string, stdout *os.File) error {
	var candidates []string
	runID := os.Getenv("OV_BENCHMARK_RUN_ID")
	if runID != "" {
		candidates = append(candidates,
			filepath.Join("/workspace", ".benchmark", runID, "repo", ".benchmark", "scope.yml"),
			filepath.Join(projectDir, ".benchmark", runID, "repo", ".benchmark", "scope.yml"),
		)
	}
	// Fallback: if cwd IS the per-run repo (common when AI is invoked from there).
	candidates = append(candidates, filepath.Join(projectDir, ".benchmark", "scope.yml"))

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			_, _ = stdout.Write(data)
			return nil
		}
	}
	return fmt.Errorf("benchmark scope: no scope.yml found (tried %s)", strings.Join(candidates, ", "))
}

// ResolveLastTestTag reads OV_BENCHMARK_RUN_ID + OV_BENCHMARK_ITERATION
// from the environment and prints the prior iteration's image tag.
// Used by `ov benchmark last-test-tag` so the AI can re-run
// `ov image test <tag> --format yaml` without triggering a rebuild.
func ResolveLastTestTag(targetImage string, stdout *os.File) error {
	runID := os.Getenv("OV_BENCHMARK_RUN_ID")
	if runID == "" {
		return fmt.Errorf("benchmark: OV_BENCHMARK_RUN_ID not set")
	}
	iterStr := os.Getenv("OV_BENCHMARK_ITERATION")
	var iter int
	fmt.Sscanf(iterStr, "%d", &iter)
	if iter <= 1 {
		return fmt.Errorf("benchmark: no prior iteration on iter %d", iter)
	}
	tag := fmt.Sprintf("ghcr.io/overthinkos/%s:ovharness-%s-iter%d", targetImage, runID, iter-1)
	fmt.Fprintln(stdout, tag)
	return nil
}
