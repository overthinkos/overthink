package main

// harness_loop.go — the iteration state machine for `charly eval run`.
//
// Post the 2026-04 kind split, the loop is keyed on a `kind: score`
// (HarnessScore) which references one or more `kind: recipe` entries.
// Per iteration:
//   - the AI sees ALL recipes via ${RECIPES} (per-recipe-grouped block)
//     and the flat ${SCENARIOS} (concatenated in score.recipes order)
//   - the harness scores against the union of every referenced
//     recipe's scenarios — score = total solved across the whole set
//   - the prompt surfaces ${SCORE_DELTA} (improvement vs prev iter)
//     and ${ATTEMPTS_LEFT} (plateau_iteration - plateau_counter)
//
// The only loop bound is plateau detection. There is no max-iteration
// ceiling — as long as the AI keeps improving, the run continues.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Opts + state types
// ---------------------------------------------------------------------------

// HarnessOpts are the resolved inputs to a run, populated by
// EvalRunLocalCmd.Run before calling RunHarness.
type HarnessOpts struct {
	ProjectDir string
	ScoreName  string
	Score      *HarnessScore
	// Recipes is the ordered list of recipe names this score evaluates
	// against (mirror of Score.Recipes — kept for substitution wiring).
	Recipe []string
	// ResolvedRecipes is the recipe catalog projection in the same order
	// as Recipes; consumed by the ${RECIPES} renderer.
	ResolvedRecipes []*HarnessRecipe
	// MergedScenarios is the concatenated scenario list across every
	// referenced recipe (in score.Recipe order). Each scenario carries
	// its source recipe via Scenario.SourceRecipe. **Drives ${SCENARIOS}
	// and ${RECIPES} prompt rendering** — the AI sees this slice with
	// any ${EVAL_NONCE_*} placeholders un-substituted.
	MergedScenarios []Scenario

	// ScoringScenarios is MergedScenarios with all ${EVAL_NONCE_*}
	// tokens substituted to their per-run hex values. **Drives baseline
	// synthesis AND per-iter scoring**. Generated once per run via
	// GenerateHarnessNonces + SubstituteScenarioNonces, after the AI's
	// prompt is rendered. The AI never sees this slice — substituted
	// values stay inside the harness's scoring path.
	ScoringScenarios []Scenario

	TargetKind string // "pod" | "vm" | "host"
	TargetName string // pod or vm name (empty when host)
	AIName     string

	// Phase / PhaseTotal carry progressive-scoping context. When the
	// score is non-progressive both are 0 and ${PHASE_*} tokens
	// substitute to "0"/"" — the prompt template is expected to omit
	// them in that case. When progressive, Phase is 1-indexed and
	// PhaseTotal == len(score.Recipe).
	Phase            int
	PhaseTotal       int
	AI               *AIConfig
	Prompt           string // template; per-iter substitution at render time
	TargetImage      string
	Tag              string
	PlateauIteration int
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

	// PreAIScenario is the frozen set of scenarios the score is
	// evaluated against. Populated from synthesizeScoreBaseline at
	// EvalRunLocalCmd entry.
	PreAIScenario []ScenarioEvalResult

	// PreFingerprints maps ScenarioID -> body fingerprint at baseline.
	PreFingerprints map[string]string

	// PreTagFingerprints maps ScenarioID -> tag fingerprint at baseline.
	PreTagFingerprints map[string]string
}

// IterationState captures one iteration's outputs.
//
// For AIs with OutputFormat="" (plain), RunnerOutput inlines the FULL
// AI stdout/stderr from runner.log — no truncation. For AIs with
// OutputFormat="stream-json", stdout (NDJSON) is parsed line-by-line
// into RunnerEvent and stderr lands in a sibling file referenced by
// RunnerStderrPath; RunnerOutput is left empty in that mode because
// RunnerEvent is the structured equivalent. The raw NDJSON is also
// kept on disk at iter<k>/runner.ndjson for byte-exact debugging.
//
// StartedUTC / FinishedUTC / IterationDuration are absolute timestamps
// for the whole iteration body (build + runner + scoring) so a reader
// of result-{calver}.yml can reconstruct the wall-clock timeline.
// RunnerCommand captures the post-substitution argv that was actually
// exec'd (e.g. with ${PROMPT} expanded to the rendered prompt text).
//
// WatchdogSample is the score-progress timeline: one entry per
// CheckInterval tick (default 5m), each carrying (at_utc, elapsed,
// score, total, last_improved_at). This is what answers "what score
// did the AI reach when?" — cross-reference at_utc with StartedUTC.
type IterationState struct {
	K                   int               `yaml:"k"`
	Phase               int               `yaml:"phase,omitempty"`
	Score               int               `yaml:"score"`
	ScoreDelta          int               `yaml:"score_delta"`
	PlateauCounterAfter int               `yaml:"plateau_counter_after"`
	BuildFailure        bool              `yaml:"build_failure,omitempty"`
	StartedUTC          string            `yaml:"started_utc,omitempty"`
	FinishedUTC         string            `yaml:"finished_utc,omitempty"`
	IterationDuration   string            `yaml:"iteration_duration,omitempty"`
	BuildDuration       string            `yaml:"build_duration,omitempty"`
	TestDuration        string            `yaml:"test_duration,omitempty"`
	RunnerDuration      string            `yaml:"runner_duration,omitempty"`
	RunnerCommand       []string          `yaml:"runner_command,omitempty"`
	RunnerOutput        string            `yaml:"runner_output,omitempty"`
	RunnerLogPath       string            `yaml:"runner_log_path,omitempty"`
	RunnerNdjsonPath    string            `yaml:"runner_ndjson_path,omitempty"`
	RunnerStderrPath    string            `yaml:"runner_stderr_path,omitempty"`
	RunnerEvent         []RunnerEvent     `yaml:"runner_event,omitempty"`
	WatchdogSample      []WatchdogSample  `yaml:"watchdog_sample,omitempty"`
	BuildLogPath        string            `yaml:"build_log_path,omitempty"`
	CommitSHA           string            `yaml:"commit_sha,omitempty"`
	Scenario            []ScenarioVerdict `yaml:"scenario,omitempty"`
	AddedScenario       []string          `yaml:"added_scenario,omitempty"`
}

// RunnerEvent is one parsed line from a stream-json AI runner's stdout.
// AtUTC is the wall-clock moment the line was read (RFC3339); Type is
// the top-level "type" field of the JSON object when present (claude
// emits "system", "assistant", "user", "result", etc.); Raw is the
// complete parsed JSON object so callers don't lose any fields. On a
// malformed JSON line the parser stores
// `Raw: {"_parse_error": <msg>, "_line": <raw bytes>}` and leaves Type
// empty — partial output survives rather than aborting the loop.
type RunnerEvent struct {
	AtUTC string         `yaml:"at_utc"`
	Type  string         `yaml:"type,omitempty"`
	Raw   map[string]any `yaml:"raw"`
}

// WatchdogSample is one tick of the score-progress watchdog (default
// 5m cadence). The harness loop appends one of these per OnTick fired
// during a stream-json or plain iteration that runs in recipe-mode.
// LastImprovedAt is empty until the AI has scored at least once.
type WatchdogSample struct {
	AtUTC          string `yaml:"at_utc"`
	Elapsed        string `yaml:"elapsed"`
	Score          int    `yaml:"score"`
	Total          int    `yaml:"total"`
	LastImprovedAt string `yaml:"last_improved_at,omitempty"`
}

// ScenarioVerdict is one scenario's post-iteration outcome.
type ScenarioVerdict struct {
	ID              string  `yaml:"id"`
	Origin          string  `yaml:"origin,omitempty"`
	SourceRecipe    string  `yaml:"source_recipe,omitempty"`
	Verdict         Verdict `yaml:"verdict"`
	Baseline        string  `yaml:"baseline,omitempty"`
	Final           string  `yaml:"final,omitempty"`
	FingerprintPre  string  `yaml:"fingerprint_pre,omitempty"`
	FingerprintPost string  `yaml:"fingerprint_post,omitempty"`
	PendingPre      int     `yaml:"pending_pre,omitempty"`
	PendingPost     int     `yaml:"pending_post,omitempty"`
	// SkippedReason carries the dependency-cascade explanation when
	// Verdict == VerdictSkipped. Format: "dep-unmet: <upstream-name>".
	SkippedReason string `yaml:"skipped_reason,omitempty"`
}

// FinalReport is the aggregate persisted to result-{calver}.yml.
type FinalReport struct {
	Schema              int               `yaml:"schema"`
	Score               string            `yaml:"score"`
	Recipe              []string          `yaml:"recipe,omitempty"`
	Calver              string            `yaml:"calver"`
	RunID               string            `yaml:"run_id"`
	AI                  string            `yaml:"ai"`
	AIVersion           map[string]string `yaml:"ai_version,omitempty"`
	Where               ReportWhere       `yaml:"where"`
	TargetImage         string            `yaml:"target_image,omitempty"`
	Tag                 string            `yaml:"tag,omitempty"`
	PlateauIteration    int               `yaml:"plateau_iteration"`
	MCPEndpoint         string            `yaml:"mcp_endpoint,omitempty"`
	StartedUTC          string            `yaml:"started_utc"`
	FinishedUTC         string            `yaml:"finished_utc"`
	ExitReason          string            `yaml:"exit_reason"` // plateau | solved-all | interrupted | dry-run
	IterationsRun       int               `yaml:"iterations_run"`
	BestScore           int               `yaml:"best_score"`
	BestIteration       int               `yaml:"best_iteration"`
	CharlyharnessBranch string            `yaml:"ovharness_branch,omitempty"`
	Summary             ReportSummary     `yaml:"summary"`
	Phases              []PhaseReport     `yaml:"phase,omitempty"`
	PhasesCompleted     int               `yaml:"phases_completed,omitempty"`
	Iterations          []IterationState  `yaml:"iteration,omitempty"`
	FinalScenario       []ScenarioVerdict `yaml:"final_scenario,omitempty"`
}

// PhaseReport summarizes one phase of a progressive run.
type PhaseReport struct {
	N             int      `yaml:"n"`
	Recipe        []string `yaml:"recipe,omitempty"`
	IterationsRun int      `yaml:"iterations_run"`
	ExitReason    string   `yaml:"exit_reason"` // solved-all | plateau | interrupted
	Score         int      `yaml:"score"`
	Total         int      `yaml:"total"`
}

// ReportWhere identifies the target a run executed against.
type ReportWhere struct {
	Kind string `yaml:"kind"`           // pod | vm | host
	Name string `yaml:"name,omitempty"` // pod or vm name; absent for host
}

// ReportSummary is the aggregate metrics panel.
type ReportSummary struct {
	Input         int     `yaml:"input"`
	Solved        int     `yaml:"solved"`
	Partial       int     `yaml:"partial"`
	Unchanged     int     `yaml:"unchanged"`
	Regressed     int     `yaml:"regressed"`
	Tampered      int     `yaml:"tampered"`
	Added         int     `yaml:"added"`
	Skipped       int     `yaml:"skipped,omitempty"`
	PercentSolved float64 `yaml:"percent_solved"`
}

// ---------------------------------------------------------------------------
// Subprocess seams (test hooks)
// ---------------------------------------------------------------------------

// findCharlyForEval returns the path to the charly binary the harness
// should re-invoke for sub-commands. Prefers os.Executable() so the
// harness's own build is used.
func findCharlyForEval() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	return "charly"
}

// buildImageFn builds the target image from the per-run repo into tag.
var buildImageFn = func(ctx context.Context, repoDir, image, tag, logPath string) (time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findCharlyForEval(), "-C", repoDir,
		"box", "build", image, "--tag", tag)
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

// runCharlyImageTestFn shells out to `charly eval box <tag> --format yaml`.
var runCharlyImageTestFn = func(ctx context.Context, tag string) ([]byte, time.Duration, error) {
	start := time.Now()
	cmd := exec.CommandContext(ctx, findCharlyForEval(), "eval", "box", tag, "--format", "yaml")
	out, err := cmd.Output()
	return out, time.Since(start), err
}

// RunnerStreamConfig customizes runRunnerFn's stdout/stderr handling
// for AIs that emit structured output. When OutputFormat is empty, the
// runner uses the legacy merged-stream path (stdout+stderr → logPath).
// When OutputFormat is "stream-json", stdout is teed to NdjsonPath
// AND parsed line-by-line into RunnerEvents dispatched to OnEvent;
// stderr is written to StderrPath.
//
// The merged-stream path is preserved verbatim for codex / gemini and
// any AI without explicit stream-json support — switching the AI's
// output_format flips the entire stdout pipeline atomically.
type RunnerStreamConfig struct {
	OutputFormat string            // "" | "stream-json"
	NdjsonPath   string            // stream-json only — raw NDJSON tee
	StderrPath   string            // stream-json only — separate stderr file
	OnEvent      func(RunnerEvent) // stream-json only — called per parsed line
}

// runRunnerFn invokes the runner inside the active target. When
// stream is non-nil and stream.OutputFormat == "stream-json", stdout
// is streamed through a streamJSONSink (tee + parse) and stderr is
// written to stream.StderrPath. Otherwise stdout+stderr merge into
// logPath as before.
var runRunnerFn = func(ctx context.Context, layout RunLayout, argv []string, env map[string]string, logPath string, stream *RunnerStreamConfig) (time.Duration, error) {
	start := time.Now()
	if len(argv) == 0 {
		return 0, fmt.Errorf("harness: runner has empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = layout.RepoDir
	cmd.Env = mergeOsEnv(env)

	if stream != nil && stream.OutputFormat == AIOutputFormatStreamJSON {
		sink, err := newStreamJSONSink(stream.NdjsonPath, stream.OnEvent)
		if err != nil {
			return 0, fmt.Errorf("harness: open ndjson sink: %w", err)
		}
		defer sink.Close()
		stderrFile, err := os.Create(stream.StderrPath)
		if err != nil {
			return 0, fmt.Errorf("harness: open stderr file: %w", err)
		}
		defer stderrFile.Close()
		cmd.Stdout = sink
		cmd.Stderr = stderrFile
		runErr := cmd.Run()
		return time.Since(start), runErr
	}

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
// EvalRunLocalCmd.Run, inside the target).
//
// Loop bounds, post-cutover:
//   - solved-all: every scenario has been solved
//   - plateau: plateau_counter >= plateau_iteration
//   - dry-run: after iter 1 with --dry-run
//   - interrupted: ctx cancelled
func RunHarness(ctx context.Context, opts HarnessOpts, layout RunLayout) (*FinalReport, error) {
	started := time.Now().UTC()
	report := &FinalReport{
		Schema:              1,
		Score:               opts.ScoreName,
		Recipe:              append([]string(nil), opts.Recipe...),
		Calver:              ComputeCalVer(),
		RunID:               layout.RunID,
		AI:                  opts.AIName,
		Where:               ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		TargetImage:         opts.TargetImage,
		Tag:                 opts.Tag,
		PlateauIteration:    opts.PlateauIteration,
		MCPEndpoint:         opts.MCPEndpoint,
		CharlyharnessBranch: layout.Branch,
		StartedUTC:          started.Format(time.RFC3339),
	}

	plateauCounter := 0
	bestScore := 0
	bestIteration := 0
	prevScore := 0
	preIDs := scenarioIDSet(opts.PreAIScenario)

	// Iteration loop — plateau-bounded, no max-iteration ceiling.
	for k := 1; ; k++ {
		// Compute still-unsolved.
		unsolved := stillUnsolved(opts.PreAIScenario, report.Iterations)
		if len(unsolved) == 0 && k > 1 {
			report.ExitReason = "solved-all"
			break
		}

		iterState, err := runOneIteration(ctx, opts, layout, k, unsolved, report, prevScore, plateauCounter, started)
		if err != nil {
			return report, fmt.Errorf("iter%d: %w", k, err)
		}
		report.Iterations = append(report.Iterations, iterState)

		// Dry-run exits after iter 1 writes its scope+prompt.
		if opts.DryRun {
			report.ExitReason = "dry-run"
			break
		}

		// Compute delta vs previous iter (k==1 → prevScore=0 so delta=Score).
		iterState.ScoreDelta = iterState.Score - prevScore
		report.Iterations[len(report.Iterations)-1].ScoreDelta = iterState.ScoreDelta

		// Plateau bookkeeping.
		if iterState.Score > bestScore {
			bestScore = iterState.Score
			bestIteration = k
			plateauCounter = 0
		} else {
			plateauCounter++
		}
		report.Iterations[len(report.Iterations)-1].PlateauCounterAfter = plateauCounter
		_ = writeIterScore(layout, k, report.Iterations[len(report.Iterations)-1])

		prevScore = iterState.Score

		// Plateau exit (only loop bound besides solved-all/dry-run/ctx).
		if opts.PlateauIteration > 0 && plateauCounter >= opts.PlateauIteration {
			report.ExitReason = "plateau"
			break
		}

		// Ctx cancellation.
		if ctx.Err() != nil {
			report.ExitReason = "interrupted"
			break
		}
	}

	finished := time.Now().UTC()
	report.FinishedUTC = finished.Format(time.RFC3339)
	report.BestScore = bestScore
	report.BestIteration = bestIteration
	report.IterationsRun = len(report.Iterations)
	if report.ExitReason == "" {
		report.ExitReason = "interrupted"
	}

	// Aggregate per-scenario final verdicts.
	if n := len(report.Iterations); n > 0 {
		report.FinalScenario = report.Iterations[n-1].Scenario
	}
	report.Summary = computeSummary(report.FinalScenario, len(preIDs))

	if err := writeReport(layout, report); err != nil {
		return report, fmt.Errorf("write report: %w", err)
	}
	return report, nil
}

// scenarioIDSet returns a set of scenario IDs from a frozen list.
func scenarioIDSet(scenarios []ScenarioEvalResult) map[string]bool {
	out := make(map[string]bool, len(scenarios))
	for _, s := range scenarios {
		out[s.ID] = true
	}
	return out
}

// stillUnsolved returns scenario IDs still in play across the run so far.
func stillUnsolved(pre []ScenarioEvalResult, iters []IterationState) []ScenarioEvalResult {
	latest := make(map[string]Verdict)
	for _, it := range iters {
		for _, v := range it.Scenario {
			latest[v.ID] = v.Verdict
		}
	}
	var out []ScenarioEvalResult
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

// runOneIteration drives a single pass through the iteration body.
func runOneIteration(
	ctx context.Context,
	opts HarnessOpts,
	layout RunLayout,
	k int,
	unsolved []ScenarioEvalResult,
	reportSoFar *FinalReport,
	prevScore int,
	plateauCounterEntering int,
	benchmarkStart time.Time,
) (iter IterationState, err error) {
	iter = IterationState{K: k, Phase: opts.Phase}
	iterStart := time.Now().UTC()
	iter.StartedUTC = iterStart.Format(time.RFC3339)
	// Stamp FinishedUTC + IterationDuration on every return path —
	// success and failure alike. omitempty means failed-early returns
	// (mkdir / scope-write / prompt-write) still produce sensible
	// records. Named return values make this a single closure rather
	// than five copies sprinkled through the function body.
	defer func() {
		finished := time.Now().UTC()
		iter.FinishedUTC = finished.Format(time.RFC3339)
		iter.IterationDuration = finished.Sub(iterStart).String()
	}()
	// iterMu serializes appends to iter.RunnerEvent (from the parser
	// goroutine in stream-json mode) and iter.WatchdogSample (from the
	// watchdog goroutine). Both writers use this same lock — straight
	// sync.Mutex is sufficient since this is per-iteration scope.
	var iterMu sync.Mutex
	iterDir := layout.IterDir(k)
	if err := os.MkdirAll(iterDir, 0o755); err != nil {
		return iter, fmt.Errorf("mkdir iter%d: %w", k, err)
	}

	// 0. Pre-iter fixture-persistence check (iter ≥ 2 only): probe whether
	// every in-scope scenario's `pod:` is still running inside the harness sandbox.
	// Per the harness contract, fixtures from earlier phases must persist
	// for cumulative scoring; if one disappeared (R10 saw charly-desktop's
	// supervisord exit cleanly mid-run between phases 6 and 7), warn the
	// AI via stderr — its prompt context will pick up the warning. Don't
	// auto-redeploy: that's the AI's job. Skip on iter1 (no prior fixtures
	// expected yet — they're being deployed in this iter for the first time).
	if k > 1 {
		warnMissingInScopePods(opts.MergedScenarios)
	}

	// 1. Write scope.yml
	scope := renderScope(opts, layout, k, reportSoFar, unsolved)
	if err := writeScope(layout, k, scope); err != nil {
		return iter, fmt.Errorf("write scope: %w", err)
	}

	// 2. Render + write prompt.md
	notesSnap := opts.Notes
	if opts.Score != nil && opts.Score.NotesEnabled() {
		runNotesPath := NotePathForRun(layout.HarnessRoot, layout.RunID)
		if data, err := os.ReadFile(runNotesPath); err == nil {
			notesSnap = string(data)
		} else {
			notesSnap = ""
		}
	}
	mcp := opts.MCPEndpoint
	if mcp == "" {
		mcp = DefaultMCPEndpoint
	}
	scenariosYAML := RenderRecipeScenariosYAML(opts.MergedScenarios)
	recipesYAML := ""
	if len(opts.Recipe) > 0 {
		// Build a recipe-name → *HarnessRecipe map from ResolvedRecipes
		// for the renderer.
		catalog := make(map[string]*HarnessRecipe, len(opts.ResolvedRecipes))
		for i, name := range opts.Recipe {
			if i < len(opts.ResolvedRecipes) {
				catalog[name] = opts.ResolvedRecipes[i]
			}
		}
		recipesYAML = RenderScoreRecipesYAML(opts.Recipe, catalog)
	}
	deploymentName := ""
	if opts.Score != nil {
		deploymentName = opts.Score.Deploy
	}
	scoreDelta := 0
	if k > 1 {
		scoreDelta = priorScore(reportSoFar) - prevScore
		// Note: at this point reportSoFar.Iterations[-1] doesn't yet
		// exist for iter k. The "delta last-shown to AI" is
		// (priorIter.Score - prevPrevScore). For k==1 delta is 0.
		// Simpler model: pass scoreDelta as the previous iter's delta.
		if n := len(reportSoFar.Iterations); n > 0 {
			scoreDelta = reportSoFar.Iterations[n-1].ScoreDelta
		}
	}
	attemptsLeft := opts.PlateauIteration - plateauCounterEntering
	if attemptsLeft < 0 {
		attemptsLeft = 0
	}
	phaseRecipesJoined := ""
	phaseIntro := ""
	if opts.PhaseTotal > 0 {
		phaseRecipesJoined = strings.Join(opts.Recipe, ", ")
		if opts.Phase == 1 {
			phaseIntro = fmt.Sprintf(
				"Phase %d of %d — first phase, in-scope recipes: %s",
				opts.Phase, opts.PhaseTotal, phaseRecipesJoined,
			)
		} else {
			added := ""
			if n := len(opts.Recipe); n > 0 {
				added = opts.Recipe[n-1]
			}
			phaseIntro = fmt.Sprintf(
				"Phase %d of %d — added recipe: %q. Total in-scope recipes: %s",
				opts.Phase, opts.PhaseTotal, added, phaseRecipesJoined,
			)
		}
	}
	substCtx := &SubstContext{
		RunID:            layout.RunID,
		ScoreName:        opts.ScoreName,
		AIName:           opts.AIName,
		WorkspacePath:    layout.RepoDir,
		TargetImage:      opts.TargetImage,
		TargetKind:       opts.TargetKind,
		TargetName:       opts.TargetName,
		Iteration:        k,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   plateauCounterEntering,
		BestScore:        reportSoFar.BestScore,
		ScoreDelta:       scoreDelta,
		AttemptsLeft:     attemptsLeft,
		MCPEndpoint:      mcp,
		Notes:            notesSnap,
		Scenarios:        scenariosYAML,
		Recipe:           recipesYAML,
		Phase:            opts.Phase,
		PhaseTotal:       opts.PhaseTotal,
		PhaseRecipes:     phaseRecipesJoined,
		PhaseIntro:       phaseIntro,
		Deploy:           deploymentName,
		Tag:              opts.Tag,
		Timeout:          opts.AI.Timeout,
	}
	if opts.Score != nil {
		substCtx.AppendEnv(opts.Score.Env)
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

	// Per-iteration wall-clock cap is OPT-IN via `ai.<name>.timeout:`.
	// Empty (the default) → no cap; the runner inherits the parent
	// context's cancellation only and runs until the AI exits or the
	// user interrupts. The plateau counter is the loop bound, not wall
	// clock. The score's prompt promises "Take all the time you need" —
	// honoring that promise is the harness's job.
	timeout, _ := ParseAITimeout(opts.AI.Timeout)
	var runnerCtx context.Context
	var cancelRunner context.CancelFunc
	if timeout > 0 {
		runnerCtx, cancelRunner = context.WithTimeout(ctx, timeout)
	} else {
		runnerCtx, cancelRunner = context.WithCancel(ctx)
	}

	// Score-progress watchdog. Hidden from the AI by construction —
	// runs in this Go process, never appears in the AI's prompt or any
	// tool the AI invokes. Probes the live deployments via
	// RunEvalLive every CheckInterval; reports the current
	// score to host stderr; terminates the runner if the score has not
	// improved in NoImprovementTimeout. Plateau detection (across
	// iterations) and this watchdog (within an iteration) are
	// orthogonal — both bound the run, neither penalizes legitimately
	// long iterations that ARE making progress.
	//
	// Watchdog only applies in recipe-mode (when ScoringScenarios is
	// non-empty). Image-test mode runs scoring after the runner exits,
	// so there's no live-score signal to poll.
	watchdogStarted := false
	var watchdogDone chan struct{}
	if len(opts.ScoringScenarios) > 0 {
		checkInterval, _ := ParseAITimeout(opts.AI.ProgressCheckInterval)
		if checkInterval == 0 {
			checkInterval = DefaultProgressCheckInterval
		}
		noImpTimeout, _ := ParseAITimeout(opts.AI.ProgressNoImprovementTimeout)
		if noImpTimeout == 0 {
			noImpTimeout = DefaultProgressNoImprovementTimeout
		}
		if checkInterval > 0 {
			scoringScenarios := opts.ScoringScenarios
			deployment := opts.Score.Deploy
			scoreName := opts.ScoreName
			phase, phaseTotal, iterK := opts.Phase, opts.PhaseTotal, k
			stderr := opts.Stderr
			wd := &ProgressWatchdog{
				CheckInterval:        checkInterval,
				NoImprovementTimeout: noImpTimeout,
				BenchmarkStart:       benchmarkStart,
				Probe: func(probeCtx context.Context) (int, int, error) {
					// IterStartTime here uses BENCHMARK start, NOT
					// per-iter start: artifacts produced legitimately
					// in earlier phases (e.g. record/stop's cast file
					// in phase 6) must remain valid through phase 7 + 8
					// scoring even though their mtime predates each
					// later phase's per-iter start. Anti-deception is
					// preserved because files older than the benchmark
					// start are still rejected.
					live, err := RunEvalLive(probeCtx, deployment, scoreName, scoringScenarios, RunScoringOpts{
						ValidateAiArtifacts: opts.Score.ValidateAiArtifacts,
						IterStartTime:       benchmarkStart,
					})
					if err != nil {
						return 0, 0, err
					}
					return live.Summary.Pass, live.Summary.Total, nil
				},
				OnTick: func(elapsed time.Duration, score, total int, lastImprovedAt time.Time) {
					// All user-facing timestamps render as offsets from the
					// benchmark's run-start (`+Nm0s into the run`) instead
					// of wall-clock HH:MM:SS — operators read run-relative
					// times far more easily than absolute clock times when
					// reasoning about plateau windows + watchdog timeouts.
					// `elapsed` here is RUN-elapsed (since RunHarness's
					// `started`), not iter-elapsed, so the operator sees a
					// monotonic offset that grows across phases. Idle (time
					// since last improvement) is an additional duration
					// because that's what predicts the no-improvement
					// watchdog firing.
					runElapsed := time.Since(benchmarkStart).Round(time.Second)
					var deltaInfo string
					if !lastImprovedAt.IsZero() {
						idle := time.Since(lastImprovedAt).Round(time.Second)
						lastImprovedRunOffset := lastImprovedAt.Sub(benchmarkStart).Round(time.Second)
						deltaInfo = fmt.Sprintf(" (last improvement %s ago, at +%s into the run)",
							idle, lastImprovedRunOffset)
					} else {
						deltaInfo = " (no improvement observed yet)"
					}
					_ = elapsed // kept for callback signature stability; runElapsed is canonical
					fmt.Fprintf(stderr,
						"harness: progress [phase %d/%d iter %d] +%s into the run — current score %d/%d%s\n",
						phase, phaseTotal, iterK, runElapsed, score, total, deltaInfo)
					// Persist the same observation into the iteration
					// record so result-{calver}.yml carries the score
					// timeline as a structured field (not just an
					// ephemeral stderr line).
					sample := WatchdogSample{
						AtUTC:   time.Now().UTC().Format(time.RFC3339),
						Elapsed: elapsed.Round(time.Second).String(),
						Score:   score,
						Total:   total,
					}
					if !lastImprovedAt.IsZero() {
						sample.LastImprovedAt = lastImprovedAt.UTC().Format(time.RFC3339)
					}
					iterMu.Lock()
					iter.WatchdogSample = append(iter.WatchdogSample, sample)
					iterMu.Unlock()
					// Also append the sample as a JSON line to a host-
					// visible <iter-dir>/watchdog.jsonl file so operators
					// can `tail -f` mid-iteration. The result.yml carries
					// the same timeline as a structured field, but it is
					// only flushed at iter end — the JSONL stream is the
					// only live observation surface. Best-effort: write
					// failures are logged but don't disrupt the watchdog
					// or the AI runner.
					if data, err := json.Marshal(sample); err == nil {
						path := filepath.Join(layout.IterDir(iterK), "watchdog.jsonl")
						if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
							_, _ = f.Write(append(data, '\n'))
							_ = f.Close()
						}
					}
				},
				OnTickError: func(err error) {
					fmt.Fprintf(stderr,
						"harness: progress [phase %d/%d iter %d] probe error (continuing): %v\n",
						phase, phaseTotal, iterK, err)
				},
				OnTimeout: func(reason string) {
					fmt.Fprintf(stderr,
						"harness: watchdog [phase %d/%d iter %d] terminating AI runner — %s\n",
						phase, phaseTotal, iterK, reason)
					cancelRunner()
				},
			}
			watchdogDone = make(chan struct{})
			watchdogStarted = true
			go func() {
				wd.Run(runnerCtx)
				close(watchdogDone)
			}()
		}
	}

	// Capture the post-substitution argv so result-{calver}.yml shows
	// what was actually exec'd (with ${PROMPT} expanded to the rendered
	// prompt text). Useful for replaying a problem run by hand.
	iter.RunnerCommand = append([]string(nil), runnerArgv...)

	// Build the runner stream configuration. For AIs with
	// output_format: stream-json, stdout (NDJSON) is parsed into
	// RunnerEvents and stderr is split into a sibling file. For all
	// other AIs, stream is nil → legacy merged-stream path.
	var streamCfg *RunnerStreamConfig
	if opts.AI != nil && opts.AI.OutputFormat == AIOutputFormatStreamJSON {
		ndjsonPath := filepath.Join(iterDir, "runner.ndjson")
		stderrPath := filepath.Join(iterDir, "runner.stderr.log")
		streamCfg = &RunnerStreamConfig{
			OutputFormat: AIOutputFormatStreamJSON,
			NdjsonPath:   ndjsonPath,
			StderrPath:   stderrPath,
			OnEvent: func(ev RunnerEvent) {
				iterMu.Lock()
				iter.RunnerEvent = append(iter.RunnerEvent, ev)
				iterMu.Unlock()
			},
		}
		iter.RunnerNdjsonPath = ndjsonPath
		iter.RunnerStderrPath = stderrPath
	}

	runnerDur, runnerErr := runRunnerFn(runnerCtx, layout, runnerArgv, runnerEnv, runnerLog, streamCfg)
	cancelRunner()
	if watchdogStarted {
		<-watchdogDone // ensure watchdog goroutine exits before iter completes
	}
	iter.RunnerDuration = runnerDur.String()
	if streamCfg == nil {
		// Plain runners merge stdout+stderr into runner.log; inline it
		// into the result for backward compatibility.
		iter.RunnerLogPath = runnerLog
		if data, err := os.ReadFile(runnerLog); err == nil {
			iter.RunnerOutput = string(data)
		}
	}
	if runnerErr != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: runner exited with error: %v (continuing)\n", k, runnerErr)
	}

	// 4. Score against the substituted scenario set. The AI saw the
	// MergedScenarios slice (with ${EVAL_NONCE_*} placeholders);
	// scoring runs against ScoringScenarios with substituted values.
	useRecipeScenarios := len(opts.ScoringScenarios) > 0
	iterTagSuffix := fmt.Sprintf("charlyeval-%s-iter%d", layout.RunID, k)
	iterRef := fmt.Sprintf("ghcr.io/overthinkos/%s:%s", opts.TargetImage, iterTagSuffix)
	var (
		testOut             []byte
		parsed              *EvalRunResults
		postFingerprints    map[string]string
		postTagFingerprints map[string]string
	)

	if useRecipeScenarios {
		testStart := time.Now()
		live, scoreErr := RunEvalLive(ctx, opts.Score.Deploy, opts.ScoreName, opts.ScoringScenarios, RunScoringOpts{
			ValidateAiArtifacts: opts.Score.ValidateAiArtifacts,
			// Freshness floor uses benchmarkStart so artifacts
			// produced in earlier phases survive scoring across
			// phase boundaries — see the watchdog probe path
			// for the design rationale.
			IterStartTime: benchmarkStart,
		})
		iter.TestDuration = time.Since(testStart).String()
		if scoreErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Scenario = priorScenarios(reportSoFar)
			fmt.Fprintf(opts.Stderr, "iter%d: live score: %v\n", k, scoreErr)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}
		parsed = live
		if data, err := yaml.Marshal(live); err == nil {
			testOut = data
			_ = os.WriteFile(filepath.Join(iterDir, "test-output.yaml"), testOut, 0o644)
		}
		postFingerprints = opts.PreFingerprints
		postTagFingerprints = opts.PreTagFingerprints
	} else if !opts.SkipRebuild {
		buildLog := filepath.Join(iterDir, "build.log")
		buildDur, buildErr := buildImageFn(ctx, layout.RepoDir, opts.TargetImage, iterTagSuffix, buildLog)
		iter.BuildDuration = buildDur.String()
		iter.BuildLogPath = buildLog
		if buildErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Scenario = priorScenarios(reportSoFar)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}

		testStart := time.Now()
		out, _, testErr := runCharlyImageTestFn(ctx, iterRef)
		iter.TestDuration = time.Since(testStart).String()
		if testErr != nil {
			iter.BuildFailure = true
			iter.Score = priorScore(reportSoFar)
			iter.Scenario = priorScenarios(reportSoFar)
			fmt.Fprintf(opts.Stderr, "iter%d: charly eval box: %v\n", k, testErr)
			if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
				fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
			}
			_ = writeIterScore(layout, k, iter)
			return iter, nil
		}
		testOut = out
		_ = os.WriteFile(filepath.Join(iterDir, "test-output.yaml"), out, 0o644)
		postSet := loadDescriptionsFromDir(layout.RepoDir, opts.TargetImage)
		postFingerprints = FingerprintSet(postSet)
		postTagFingerprints = collectTagFingerprints(postSet)
	}

	if parsed == nil {
		p, err := ParseCharlyTestOutput(testOut)
		if err != nil {
			return iter, fmt.Errorf("parse test output: %w", err)
		}
		parsed = p
	}
	if postFingerprints == nil {
		postFingerprints = map[string]string{}
	}
	if postTagFingerprints == nil {
		postTagFingerprints = map[string]string{}
	}

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
		// Carry SourceRecipe through from baseline annotation if any.
		sr := ""
		if idx := strings.Index(pre.Origin, "recipe:"); idx >= 0 {
			sr = pre.Origin[idx+len("recipe:"):]
		}
		iter.Scenario = append(iter.Scenario, ScenarioVerdict{
			ID:              pre.ID,
			Origin:          pre.Origin,
			SourceRecipe:    sr,
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

	preIDs := scenarioIDSet(opts.PreAIScenario)
	for id := range postByID {
		if !preIDs[id] {
			iter.AddedScenario = append(iter.AddedScenario, id)
		}
	}

	solvedIDs := collectSolvedIDs(iter.Scenario)
	if err := commitIterationBestEffort(ctx, layout, k, iter, opts); err != nil {
		fmt.Fprintf(opts.Stderr, "iter%d: commit: %v\n", k, err)
	}
	_ = solvedIDs

	if err := writeIterScore(layout, k, iter); err != nil {
		return iter, fmt.Errorf("write iter score: %w", err)
	}
	return iter, nil
}

// commitIterationBestEffort commits the iteration in the per-run clone.
//
// Before committing, emits a per-iter delta summary line and kills
// orphaned `while true; do sleep N; done` / `pgrep -f` self-match
// poll-loop bash subprocesses left dangling by the AI's
// `Bash{run_in_background: true}` + `TaskOutput`-timeout pattern
// (Claude Code issue 52328 — see
// `.eval/ISSUE-claude-code-bash-pgrep-self-match-deadlock.md`). Without
// this kill, orphans accumulate across iterations, eventually wedging
// the next claude spawn (the parent claude process waits for all
// background bash subprocesses to exit before terminating itself).
//
// The kill targets two patterns observed in the 2026-04-28 R10 round:
//   - `bash -c 'while true; do sleep N; done'` (heartbeat keepalives)
//   - `bash -c '... pgrep -f "<arbitrary>" ... ; do sleep N'` (self-match polls)
//
// Best-effort: pkill failure is logged but doesn't abort the commit.
func commitIterationBestEffort(ctx context.Context, layout RunLayout, k int, iter IterationState, opts HarnessOpts) error {
	emitIterEndSummary(k, iter)
	killOrphanLoopBashes(opts.TargetKind, opts.TargetName)
	solved := collectSolvedIDs(iter.Scenario)
	sha, err := CommitIterationInRepo(ctx, layout, k, iter.Score, solved)
	if err != nil {
		return err
	}
	iter.CommitSHA = sha
	return nil
}

// emitIterEndSummary prints one stderr line at the end of every
// iteration with a per-iter delta breakdown: solved count this iter,
// list of failed scenarios (capped at 5), cascade-skipped count, and
// the new cumulative score. The watchdog's per-tick "current score"
// log shows running totals but does not delta-summarize. This is the
// "what did this iter actually change?" view operators want.
//
// Format:
//
//	harness: phase 6 iter 1 → solved 6 of 13 this iter (failed:
//	desktop-cdp-loads-web; cascade-skipped: 6 dependents); cumulative 67/74
//
// `solved this iter` counts scenarios with VerdictSolved (baseline
// fail → final pass) — NOT cumulative passing scenarios from prior
// iters. Cumulative score = total scenarios with `final: pass` across
// all in-scope recipes.
func emitIterEndSummary(k int, iter IterationState) {
	var solvedThisIter, failedFinal, skippedFinal, cumulativePass, total int
	var failedNames []string
	for _, s := range iter.Scenario {
		total++
		switch s.Verdict {
		case VerdictSolved:
			solvedThisIter++
			cumulativePass++
		case VerdictUnchanged:
			// baseline pass → final pass: counts toward cumulative but
			// not solved-this-iter.
			if s.Final == "pass" {
				cumulativePass++
			} else {
				failedFinal++
				if len(failedNames) < 5 {
					failedNames = append(failedNames, scenarioShortName(s.ID))
				}
			}
		case VerdictSkipped:
			skippedFinal++
		case VerdictTampered:
			// counted as cumulative pass because the baseline was passing.
			// Tampering means baseline-pass + final-fail.
			failedFinal++
			if len(failedNames) < 5 {
				failedNames = append(failedNames, scenarioShortName(s.ID))
			}
		default:
			if s.Final == "pass" {
				cumulativePass++
			}
		}
	}
	failPart := ""
	if failedFinal > 0 {
		more := ""
		if failedFinal > len(failedNames) {
			more = fmt.Sprintf(", +%d more", failedFinal-len(failedNames))
		}
		failPart = fmt.Sprintf(" (failed: %s%s; cascade-skipped: %d)", strings.Join(failedNames, ", "), more, skippedFinal)
	} else if skippedFinal > 0 {
		failPart = fmt.Sprintf(" (cascade-skipped: %d)", skippedFinal)
	}
	fmt.Fprintf(os.Stderr,
		"harness: iter %d end → solved %d this iter%s; cumulative %d/%d\n",
		k, solvedThisIter, failPart, cumulativePass, total)
}

// scenarioShortName returns the tail segment of a `desc:pod:<pod>:<idx>`
// or `recipe:<name>:<idx>` scenario ID, falling back to the full ID.
// Used by emitIterEndSummary to keep the failed-name list compact.
func scenarioShortName(id string) string {
	parts := strings.Split(id, ":")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + ":" + parts[len(parts)-1]
	}
	return id
}

// warnMissingInScopePods probes the running container set and warns
// once per missing fixture pod that's referenced in the in-scope
// scenarios. The harness contract requires earlier-phase fixture pods
// to persist for cumulative scoring; this is a soft signal to the AI
// (via the next iter's prompt context) that something needs redeploying.
// Best-effort: probe failures are silently ignored (the live scorer
// will surface real issues at iter end).
func warnMissingInScopePods(scenarios []Scenario) {
	// Collect unique pod names from scenarios.
	uniquePods := map[string]bool{}
	for _, sc := range scenarios {
		if sc.Pod != "" {
			// Use only the root segment for dotted paths — nested
			// pods are checked via their parent's reachability.
			rootPod := sc.Pod
			if i := strings.IndexByte(rootPod, '.'); i > 0 {
				rootPod = rootPod[:i]
			}
			uniquePods[rootPod] = true
		}
	}
	if len(uniquePods) == 0 {
		return
	}
	// Probe each: `podman ps --filter name=charly-<pod> --format {{.Names}}`.
	// One missing pod produces one warn line; multiple missing produce one
	// warn line each so the operator/AI sees them all.
	missing := 0
	for pod := range uniquePods {
		expected := "charly-" + strings.ReplaceAll(pod, ".", "_")
		out, err := exec.Command("podman", "ps", "--filter", "name="+expected,
			"--filter", "status=running", "--format", "{{.Names}}").Output()
		if err != nil {
			continue // probe error → silent (don't spam)
		}
		if !strings.Contains(string(out), expected) {
			fmt.Fprintf(os.Stderr,
				"harness: WARNING: in-scope fixture pod %q is not running — "+
					"earlier-phase scenarios that probe it will fail at iter end "+
					"unless the AI redeploys it this iteration.\n", expected)
			missing++
		}
	}
	if missing > 0 {
		fmt.Fprintf(os.Stderr,
			"harness: %d fixture pod(s) missing — see warnings above; "+
				"the AI's iteration prompt should restore them per harness contract.\n",
			missing)
	}
}

// killOrphanLoopBashes kills issue-52328 deadlock orphans inside the
// target's PID namespace. The orchestrator runs HOST-side; orphans
// accumulate INSIDE the harness sandbox (where the AI runner spawns claude,
// which forks `bash -c 'while true; do sleep N; done'` heartbeats and
// `bash -c '... pgrep -f ...; do sleep N'` self-match polls). Without
// `podman exec`, pkill on the host would scan the wrong PID namespace.
//
// Two patterns observed in the 2026-04-28 R10 round:
//   - `while true.*sleep [0-9]+`            (heartbeat keepalives)
//   - `bash -c .*pgrep -f .*sleep`          (self-match polls)
//
// Best-effort: failures are silent (no harness sandbox = nothing to kill).
// Pod-target only: vm/host targets don't have the same PID-namespace
// shape and the AI runs natively, so the issue does not apply.
func killOrphanLoopBashes(targetKind, targetName string) {
	if targetKind != "pod" || targetName == "" {
		return
	}
	container := "charly-" + targetName
	patterns := map[string]string{
		"while-true-sleep": `while true.*sleep [0-9]+`,
		"pgrep-self-match": `bash -c .*pgrep -f .*sleep`,
	}
	for label, pat := range patterns {
		// pkill -c reports kill count; -f matches full cmdline.
		// `podman exec` runs the kill inside the pod's PID namespace.
		cmd := exec.Command("podman", "exec", container, "pkill", "-c", "-f", pat)
		out, _ := cmd.Output()
		var n int
		fmt.Sscanf(string(out), "%d", &n)
		if n > 0 {
			fmt.Fprintf(os.Stderr, "harness: killed %d orphan bash poll-loop(s) [%s] inside %s before iter commit\n", n, label, container)
		}
	}
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

// computePlateauSoFar returns the plateau counter going into iter k+1.
func computePlateauSoFar(r *FinalReport) int {
	if r == nil || len(r.Iterations) == 0 {
		return 0
	}
	return r.Iterations[len(r.Iterations)-1].PlateauCounterAfter
}

// ---------------------------------------------------------------------------
// Scope rendering
// ---------------------------------------------------------------------------

// HarnessScope is the YAML-serializable form of /workspace/.eval/scope.yml.
type HarnessScope struct {
	RunID            string              `yaml:"run_id"`
	Score            string              `yaml:"score,omitempty"`
	Recipe           []string            `yaml:"recipe,omitempty"`
	AI               string              `yaml:"ai,omitempty"`
	Iteration        int                 `yaml:"iteration"`
	PlateauIteration int                 `yaml:"plateau_iteration"`
	PlateauCounter   int                 `yaml:"plateau_counter"`
	AttemptsLeft     int                 `yaml:"attempts_left"`
	BestScore        int                 `yaml:"best_score"`
	ScoreDelta       int                 `yaml:"score_delta"`
	TargetImage      string              `yaml:"target_image"`
	Where            ReportWhere         `yaml:"where"`
	Tag              string              `yaml:"tag,omitempty"`
	History          []ScopeHistoryEntry `yaml:"history,omitempty"`
	Scenario         []ScopeScenario     `yaml:"scenario,omitempty"`
}

// ScopeHistoryEntry summarizes one past iteration for the AI.
type ScopeHistoryEntry struct {
	K                   int      `yaml:"k"`
	Score               int      `yaml:"score"`
	ScoreDelta          int      `yaml:"score_delta"`
	SolvedIDs           []string `yaml:"solved_id,omitempty"`
	NewlySolvedIDs      []string `yaml:"newly_solved_id,omitempty"`
	Runtime             string   `yaml:"runtime,omitempty"`
	PlateauCounterAfter int      `yaml:"plateau_counter_after,omitempty"`
}

// ScopeScenario is one still-unsolved scenario as the AI sees it.
type ScopeScenario struct {
	ID              string                    `yaml:"id"`
	Origin          string                    `yaml:"origin,omitempty"`
	BaselineVerdict string                    `yaml:"baseline_verdict,omitempty"`
	Trajectory      []ScopeScenarioTrajectory `yaml:"trajectory,omitempty"`
	PendingCurrent  int                       `yaml:"pending_steps_current,omitempty"`
}

// ScopeScenarioTrajectory records one iteration's verdict + pending delta.
type ScopeScenarioTrajectory struct {
	K                 int     `yaml:"k"`
	Verdict           Verdict `yaml:"verdict"`
	PendingStepsDelta int     `yaml:"pending_steps_delta,omitempty"`
}

// renderScope builds the Scope that iteration k will see.
func renderScope(opts HarnessOpts, layout RunLayout, k int, reportSoFar *FinalReport, unsolved []ScenarioEvalResult) *HarnessScope {
	plateauCounter := computePlateauSoFar(reportSoFar)
	attemptsLeft := opts.PlateauIteration - plateauCounter
	if attemptsLeft < 0 {
		attemptsLeft = 0
	}
	scoreDelta := 0
	if n := len(reportSoFar.Iterations); n > 0 {
		scoreDelta = reportSoFar.Iterations[n-1].ScoreDelta
	}
	s := &HarnessScope{
		RunID:            layout.RunID,
		Score:            opts.ScoreName,
		Recipe:           append([]string(nil), opts.Recipe...),
		AI:               opts.AIName,
		Iteration:        k,
		PlateauIteration: opts.PlateauIteration,
		PlateauCounter:   plateauCounter,
		AttemptsLeft:     attemptsLeft,
		BestScore:        reportSoFar.BestScore,
		ScoreDelta:       scoreDelta,
		TargetImage:      opts.TargetImage,
		Where:            ReportWhere{Kind: opts.TargetKind, Name: opts.TargetName},
		Tag:              opts.Tag,
	}
	for _, h := range reportSoFar.Iterations {
		s.History = append(s.History, ScopeHistoryEntry{
			K:                   h.K,
			Score:               h.Score,
			ScoreDelta:          h.ScoreDelta,
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

// writeScope writes scope.yml to iter<k>/ AND mirrors to the per-run clone.
func writeScope(layout RunLayout, k int, s *HarnessScope) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	iterPath := filepath.Join(layout.IterDir(k), "scope.yml")
	if err := os.WriteFile(iterPath, data, 0o644); err != nil {
		return err
	}
	mirrorDir := filepath.Join(layout.RepoDir, ".eval")
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
	mirrorDir := filepath.Join(layout.RepoDir, ".eval")
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

// writeReport writes the aggregated result-{calver}.yml.
func writeReport(layout RunLayout, r *FinalReport) error {
	if r.Calver == "" {
		r.Calver = ComputeCalVer()
	}
	if r.Score == "" {
		r.Score = layout.Score
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
	resultPath := filepath.Join(layout.ResultsDir(), "result-"+r.Calver+".yml")
	return os.WriteFile(resultPath, data, 0o644)
}

// printHarnessReport renders a summary of the run to stdout.
func printHarnessReport(w *os.File, r *FinalReport, format string) {
	if format == "yaml" {
		data, _ := yaml.Marshal(r)
		_, _ = w.Write(data)
		return
	}
	fmt.Fprintf(w, "harness: score=%s ai=%s exit=%s iterations=%d best=%d/%d\n",
		r.Score, r.AI, r.ExitReason, r.IterationsRun, r.BestScore, r.Summary.Input)
	fmt.Fprintf(w, "  result: .eval/%s/results/result-%s.yml\n", r.Score, r.Calver)
	fmt.Fprintf(w, "  branch: %s\n", r.CharlyharnessBranch)
}

// ---------------------------------------------------------------------------
// Runner argv + env rendering
// ---------------------------------------------------------------------------

// renderRunnerInvocation prepares the argv + env the dispatcher executes.
func renderRunnerInvocation(opts HarnessOpts, substCtx *SubstContext, promptText, iterDir string) ([]string, map[string]string) {
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
	env["CHARLY_EVAL_RUN_ID"] = substCtx.RunID
	env["CHARLY_EVAL_ITERATION"] = fmt.Sprintf("%d", substCtx.Iteration)
	env["CHARLY_EVAL_SCORE"] = substCtx.ScoreName
	env["CHARLY_EVAL_AI"] = substCtx.AIName
	env["CHARLY_EVAL_TARGET_KIND"] = substCtx.TargetKind
	env["CHARLY_EVAL_TARGET_NAME"] = substCtx.TargetName
	// CHARLY_EVAL_PHASE is the 1-indexed phase number (0 when the score
	// is single-pass / non-progressive). `charly eval self-evaluate`
	// uses this to resolve the in-scope recipes for the current phase
	// the same way the orchestrator's scorer does.
	env["CHARLY_EVAL_PHASE"] = fmt.Sprintf("%d", substCtx.Phase)
	if opts.Score != nil && opts.Score.NotesEnabled() {
		harnessRoot := HarnessDataRoot(opts.ProjectDir, opts.ScoreName)
		env["CHARLY_EVAL_NOTES_FILE"] = NotePathForRun(harnessRoot, substCtx.RunID)
	}
	return argv, env
}

// ---------------------------------------------------------------------------
// Post-AI description reload + tag-fingerprint collection
// ---------------------------------------------------------------------------

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
		case VerdictSkipped:
			s.Skipped++
		}
	}
	if total > 0 {
		s.PercentSolved = float64(s.Solved) / float64(total) * 100.0
	}
	return s
}

// ---------------------------------------------------------------------------
// Scope-from-env — `charly eval scope` handler
// ---------------------------------------------------------------------------

// ResolveAndPrintScope reads CHARLY_EVAL_RUN_ID from the environment,
// locates the active scope.yml inside the per-run clone, and writes
// its contents to out.
func ResolveAndPrintScope(projectDir string, stdout *os.File) error {
	var candidates []string
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	if runID != "" {
		candidates = append(candidates,
			filepath.Join("/workspace", ".harness", runID, "repo", ".harness", "scope.yml"),
			filepath.Join(projectDir, ".eval", runID, "repo", ".harness", "scope.yml"),
		)
	}
	candidates = append(candidates, filepath.Join(projectDir, ".eval", "scope.yml"))

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			_, _ = stdout.Write(data)
			return nil
		}
	}
	return fmt.Errorf("harness scope: no scope.yml found (tried %s)", strings.Join(candidates, ", "))
}

// ResolveLastTestTag reads CHARLY_EVAL_RUN_ID + CHARLY_EVAL_ITERATION
// from the environment and prints the prior iteration's image tag.
func ResolveLastTestTag(targetImage string, stdout *os.File) error {
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	if runID == "" {
		return fmt.Errorf("harness: CHARLY_EVAL_RUN_ID not set")
	}
	iterStr := os.Getenv("CHARLY_EVAL_ITERATION")
	var iter int
	fmt.Sscanf(iterStr, "%d", &iter)
	if iter <= 1 {
		return fmt.Errorf("harness: no prior iteration on iter %d", iter)
	}
	tag := fmt.Sprintf("ghcr.io/overthinkos/%s:charlyeval-%s-iter%d", targetImage, runID, iter-1)
	fmt.Fprintln(stdout, tag)
	return nil
}
