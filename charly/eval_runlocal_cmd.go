package main

// eval_runlocal_cmd.go — in-target entry point of the harness.
//
// The host-side `charly eval run <score>` is a thin forwarder. All real
// work happens here, executed *inside the chosen target* (pod via
// `podman exec`, vm via `ssh`, or host directly).
//
// Responsibilities (in order):
//   1. Acquire .harness/<score>/.lock — per-target concurrency guard
//   2. Resolve the score's `recipes:` to a merged scenario list
//   3. Clone <project> -> <project>/.eval/<score>/runs/<run-id>/repo
//   4. Create branch charlyeval/<run-id> + submodule init
//   5. Synthesize the pre-AI baseline from the merged scenarios
//   6. Drive RunHarness — the iteration state machine
//   7. Push branch back to the bind-mounted/host project repo
//   8. Release lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// pinPersistentXDGRuntimeDir relocates `XDG_RUNTIME_DIR` to a persistent
// path under `$HOME` when the current value points at a transient
// `/run/user/<uid>` tmpfs. Crun stores per-container status files at
// `$XDG_RUNTIME_DIR/crun/<id>/status`; if that location is wiped while
// containers are still running, every subsequent `podman exec` against
// those containers fails with "container does not exist" — even though
// the container processes are alive. Forensic evidence from the
// 2026-04-27 N canary: `/run/user/1000` disappeared between phases 6
// and 7, breaking the harness's per-iter `RunEvalLive`
// probes for every pre-existing pod. Pinning to `$HOME/.local/share/charly-runtime`
// (a regular directory on the harness sandbox's persistent overlay) survives
// whatever wipes the tmpfs.
//
// Only relocates when XDG_RUNTIME_DIR is empty or a `/run/user/...`
// path; explicit overrides are respected.
func pinPersistentXDGRuntimeDir() error {
	current := os.Getenv("XDG_RUNTIME_DIR")
	if current != "" && !strings.HasPrefix(current, "/run/user/") {
		return nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/user"
	}
	persistent := filepath.Join(home, ".local", "share", "charly-runtime")
	if err := os.MkdirAll(persistent, 0o700); err != nil {
		return fmt.Errorf("creating persistent runtime dir %s: %w", persistent, err)
	}
	if err := os.Setenv("XDG_RUNTIME_DIR", persistent); err != nil {
		return fmt.Errorf("setting XDG_RUNTIME_DIR=%s: %w", persistent, err)
	}
	return nil
}

// EvalRunLocalCmd drives the iteration loop in the chosen target.
type EvalRunLocalCmd struct {
	Score       string `arg:"" help:"Score name (from eval.yml)"`
	TargetImage string `name:"target-image" help:"Target image to score (default: derived from score / pod)"`
	Agent       string `name:"agent" help:"Agent to invoke (defaults to score.agent when single-element)"`
	RunID       string `name:"run-id" help:"Run identifier (set by host harness; auto if empty)"`
	PlateauIter int    `name:"plateau-iteration" help:"Override score.plateau_iteration"`
	MaxScenario int    `name:"max-scenario" help:"Cap the pending input set"`
	Tag         string `name:"tag" help:"Gherkin tag expression to narrow scenarios"`
	DryRun      bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only scenarios)"`
	Format      string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
	NoLock      bool   `name:"no-lock" hidden:"" help:"Skip flock (tests only)"`
	KeepRepo    bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run completes (debugging only — clones are ~100MB)"`
	ProjectDir  string `name:"project-dir" hidden:"" help:"Override project root (default: cwd or /workspace)"`
}

// HarnessLockPath returns the absolute path of the per-score flock
// file under the harness data root.
func HarnessLockPath(projectDir, score string) string {
	return filepath.Join(HarnessDataRoot(projectDir, score), ".lock")
}

func (c *EvalRunLocalCmd) Run() error {
	ctx := context.Background()

	// Pin XDG_RUNTIME_DIR to a persistent location BEFORE any podman
	// operation runs. Every child process the harness spawns — the AI's
	// claude subprocess, its bash subshells, the per-iter probe path's
	// `podman exec` calls — inherits this env. With it pinned, crun
	// status files live on the harness sandbox's overlay (persistent) rather
	// than `/run/user/1000` (transient).
	if err := pinPersistentXDGRuntimeDir(); err != nil {
		return err
	}

	projectDir := c.ProjectDir
	if projectDir == "" {
		if _, err := os.Stat("/workspace"); err == nil {
			projectDir = "/workspace"
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			projectDir = cwd
		}
	}

	uf, ok, err := LoadUnified(projectDir)
	if err != nil {
		return fmt.Errorf("load harness config from %s: %w", projectDir, err)
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly eval run-local: no charly.yml in %s", projectDir)
	}

	score, err := ResolveScore(uf.Score, c.Score)
	if err != nil {
		return err
	}
	tk, tn, err := ResolveScoreTarget(score)
	if err != nil {
		return err
	}

	// Resolve the score's `recipes:` list against the recipe catalog —
	// FULL scope (used in non-progressive mode and for the global
	// nonce set in progressive mode).
	fullMergedScenarios, fullResolvedRecipes, err := ResolveScoreRecipe(score, uf.Recipe)
	if err != nil {
		return fmt.Errorf("score %q: %w", c.Score, err)
	}

	// Generate per-run nonces and substitute into a SECOND scenarios
	// slice for scoring. The AI sees the un-substituted slice via
	// ${SCENARIOS}/${RECIPES} prompt tokens (placeholders only); the
	// substituted slice flows into baseline synthesis + per-iter
	// scoring so probes carry real nonce values that the AI cannot
	// have pre-set. See SubstituteScenarioNonces in eval_substitute.go.
	//
	// Nonces are generated ONCE over the FULL recipe set so they're
	// stable across phases (a scenario in tier4 sees the same nonce
	// in phase 4 regardless of which phase introduced tier4).
	nonces, err := GenerateHarnessNonces(fullMergedScenarios)
	if err != nil {
		return fmt.Errorf("generate harness nonces: %w", err)
	}
	if len(nonces) > 0 {
		names := make([]string, 0, len(nonces))
		for name := range nonces {
			names = append(names, name)
		}
		fmt.Fprintf(os.Stderr,
			"harness: generated %d per-run nonce(s): %v\n", len(nonces), names)
	}

	// AI selection — score.Agent is the eligible list; --agent picks one.
	aiName := c.Agent
	if aiName == "" {
		if len(score.Agent) == 1 {
			aiName = score.Agent[0]
		} else if len(score.Agent) == 0 {
			return fmt.Errorf("score %q has empty ai: list", c.Score)
		} else {
			return fmt.Errorf("score %q has multiple eligible agents (%v); pass --agent NAME", c.Score, score.Agent)
		}
	}
	ai, _, err := ResolveAgent(uf.Agent, aiName)
	if err != nil {
		return err
	}

	if !c.NoLock {
		unlock, lerr := acquireHarnessLock(projectDir, c.Score)
		if lerr != nil {
			return lerr
		}
		defer unlock()
	}

	layout := NewRunLayout(projectDir, c.Score, c.RunID)
	if err := os.MkdirAll(layout.RunDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", layout.RunDir, err)
	}
	fmt.Fprintf(os.Stderr, "harness: score=%s ai=%s run=%s where=%s:%s\n",
		c.Score, aiName, layout.RunID, tk, tn)

	if err := CreateRunClone(ctx, layout); err != nil {
		return fmt.Errorf("clone %s -> %s: %w", projectDir, layout.RepoDir, err)
	}

	targetImage := c.TargetImage
	if targetImage == "" && score.TargetImage != "" {
		targetImage = score.TargetImage
	}

	// No in-pod preflight: the harness only owns the harness sandbox itself
	// (rebuilt fresh per run by the host-side preflight in eval_runner_cmd.go).
	// Inside the harness sandbox, the AI is on its own — it builds whatever images
	// each scenario needs, creates each pod a scenario references via
	// `charly deploy add`, and modifies state until scenarios pass. The
	// harness scoring code probes per scenario.Pod after the AI exits.

	tagExpr := c.Tag
	if tagExpr == "" {
		tagExpr = score.Tag
	}
	plateau := c.PlateauIter
	if plateau == 0 {
		plateau = score.PlateauIteration
	}

	notesSnap := ""
	if score.NotesEnabled() {
		notesSnap, _ = ReadNote(projectDir, c.Score)
	}

	mcp := score.EffectiveMCPEndpoint()

	aiVer := LocalCaptureVersion(ctx, ai)

	// commonOpts captures everything that doesn't change across phases.
	commonOpts := HarnessOpts{
		ProjectDir:       projectDir,
		ScoreName:        c.Score,
		Score:            score,
		TargetKind:       string(tk),
		TargetName:       tn,
		AgentName:        aiName,
		Agent:            ai,
		Prompt:           score.Prompt,
		TargetImage:      targetImage,
		Tag:              tagExpr,
		PlateauIteration: plateau,
		MaxScenario:      c.MaxScenario,
		MCPEndpoint:      mcp,
		Notes:            notesSnap,
		DryRun:           c.DryRun,
		SkipRebuild:      c.SkipRebuild,
		Format:           c.Format,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
	}

	var report *FinalReport
	if score.Progressive {
		report, err = runProgressiveHarness(ctx, layout, commonOpts, score, uf.Recipe, fullMergedScenarios, fullResolvedRecipes, nonces)
	} else {
		report, err = runSinglePhaseHarness(ctx, layout, commonOpts, score, fullMergedScenarios, fullResolvedRecipes, nonces)
	}
	if err != nil {
		return err
	}
	report.AgentVersion = map[string]string{aiName: aiVer.String()}

	if err := PushBranchToHost(ctx, layout); err != nil {
		fmt.Fprintf(os.Stderr, "harness: push branch back failed (non-fatal): %v\n", err)
	}

	_ = writeReport(layout, report)

	if !c.KeepRepo {
		if err := os.RemoveAll(layout.RepoDir); err != nil {
			fmt.Fprintf(os.Stderr, "harness: cleanup of %s failed (non-fatal): %v\n", layout.RepoDir, err)
		}
	}

	printHarnessReport(os.Stdout, report, c.Format)
	return nil
}

// runSinglePhaseHarness wraps the legacy non-progressive path: one
// merged scenario set, one RunHarness call, one FinalReport.
func runSinglePhaseHarness(
	ctx context.Context,
	layout RunLayout,
	commonOpts HarnessOpts,
	score *HarnessScore,
	mergedScenarios []Scenario,
	resolvedRecipes []*HarnessRecipe,
	nonces map[string]string,
) (*FinalReport, error) {
	scoringScenarios, err := SubstituteScenarioNonces(mergedScenarios, nonces)
	if err != nil {
		return nil, fmt.Errorf("substitute harness nonces: %w", err)
	}
	preAIResults, preFingerprints, preTagFingerprints := synthesizeScoreBaseline(commonOpts.ScoreName, scoringScenarios)
	opts := commonOpts
	opts.Recipe = append([]string(nil), score.Recipe...)
	opts.ResolvedRecipes = resolvedRecipes
	opts.MergedScenarios = mergedScenarios
	opts.ScoringScenarios = scoringScenarios
	opts.PreAIScenario = preAIResults
	opts.PreFingerprints = preFingerprints
	opts.PreTagFingerprints = preTagFingerprints
	return RunHarness(ctx, opts, layout)
}

// runProgressiveHarness implements curriculum-style phase execution.
// Phases iterate over score.Recipe incrementally: phase 1 uses
// recipes[0:1], phase 2 uses recipes[0:2], ... phase N uses all
// recipes. Each phase runs its own iteration loop (RunHarness) with a
// fresh per-phase baseline, exits on solved-all OR plateau, and the
// next phase begins with state still in place (no preflight reset).
//
// State across phases:
//   - The harness sandbox, nested-podman containers, and bind-mounted
//     /workspace are NOT touched between phases — the AI's deployed
//     pods stay running, fingerprints persist, NOTES.md persists.
//   - Nonces are run-scoped (generated once over the full recipe set)
//     so a tier4 nonce in phase 4 is the same value across iters
//     within phase 4.
//   - Per-phase Iterations are concatenated into the master report
//     in run order; each iter carries its Phase number.
func runProgressiveHarness(
	ctx context.Context,
	layout RunLayout,
	commonOpts HarnessOpts,
	score *HarnessScore,
	recipeCatalog map[string]*HarnessRecipe,
	fullMergedScenarios []Scenario,
	fullResolvedRecipes []*HarnessRecipe,
	nonces map[string]string,
) (*FinalReport, error) {
	totalPhases := len(score.Recipe)
	if totalPhases == 0 {
		return nil, fmt.Errorf("score %q: progressive: true requires non-empty recipes:", commonOpts.ScoreName)
	}

	master := &FinalReport{
		Schema:              1,
		Score:               commonOpts.ScoreName,
		Recipe:              append([]string(nil), score.Recipe...),
		Calver:              ComputeCalVer(),
		RunID:               layout.RunID,
		Agent:               commonOpts.AgentName,
		Where:               ReportWhere{Kind: commonOpts.TargetKind, Name: commonOpts.TargetName},
		TargetImage:         commonOpts.TargetImage,
		Tag:                 commonOpts.Tag,
		PlateauIteration:    commonOpts.PlateauIteration,
		MCPEndpoint:         commonOpts.MCPEndpoint,
		CharlyharnessBranch: layout.Branch,
		StartedUTC:          time.Now().UTC().Format(time.RFC3339),
	}

	phasesCompleted := 0
	overallExitReason := ""

	for n := 1; n <= totalPhases; n++ {
		phaseRecipes := append([]string(nil), score.Recipe[:n]...)
		phaseMerged, phaseResolved, err := resolvePhaseScenarios(score, recipeCatalog, n)
		if err != nil {
			return master, fmt.Errorf("phase %d: %w", n, err)
		}
		phaseScoring, err := SubstituteScenarioNonces(phaseMerged, nonces)
		if err != nil {
			return master, fmt.Errorf("phase %d: substitute nonces: %w", n, err)
		}
		preAIResults, preFingerprints, preTagFingerprints := synthesizeScoreBaseline(commonOpts.ScoreName, phaseScoring)

		fmt.Fprintf(os.Stderr, "harness: phase %d/%d — recipes %v (%d scenarios)\n",
			n, totalPhases, phaseRecipes, len(phaseMerged))

		phaseLayout := layout
		phaseLayout.Phase = n

		phaseOpts := commonOpts
		phaseOpts.Recipe = phaseRecipes
		phaseOpts.ResolvedRecipes = phaseResolved
		phaseOpts.MergedScenarios = phaseMerged
		phaseOpts.ScoringScenarios = phaseScoring
		phaseOpts.PreAIScenario = preAIResults
		phaseOpts.PreFingerprints = preFingerprints
		phaseOpts.PreTagFingerprints = preTagFingerprints
		phaseOpts.Phase = n
		phaseOpts.PhaseTotal = totalPhases

		phaseReport, err := RunHarness(ctx, phaseOpts, phaseLayout)
		if err != nil {
			// Surface the partial master state with whatever has been
			// completed so far.
			finalizeMasterReport(master, phasesCompleted, "interrupted")
			return master, fmt.Errorf("phase %d: %w", n, err)
		}

		// Merge phase results into the master report.
		for i := range phaseReport.Iterations {
			phaseReport.Iterations[i].Phase = n
		}
		master.Iterations = append(master.Iterations, phaseReport.Iterations...)
		master.Phases = append(master.Phases, PhaseReport{
			N:             n,
			Recipe:        phaseRecipes,
			IterationsRun: phaseReport.IterationsRun,
			ExitReason:    phaseReport.ExitReason,
			Score:         phaseReport.BestScore,
			Total:         len(phaseMerged),
		})
		master.BestScore = phaseReport.BestScore
		master.BestIteration = len(master.Iterations)
		master.IterationsRun = len(master.Iterations)
		master.FinalScenario = phaseReport.FinalScenario

		if phaseReport.ExitReason == "solved-all" {
			phasesCompleted++
		}

		// Decide whether the run continues to the next phase, or ends here.
		// Logic extracted into decideOverallExit for unit-testability.
		if reason, shouldBreak := decideOverallExit(ctx.Err(), phaseReport.ExitReason); shouldBreak {
			overallExitReason = reason
			break
		}
	}

	if overallExitReason == "" {
		// All phases attempted. Last phase's exit reason becomes the
		// run-level exit reason (matches the user's natural reading:
		// "did the FINAL phase end clean?").
		if len(master.Phases) > 0 {
			overallExitReason = master.Phases[len(master.Phases)-1].ExitReason
		} else {
			overallExitReason = "interrupted"
		}
	}
	finalizeMasterReport(master, phasesCompleted, overallExitReason)
	return master, nil
}

// decideOverallExit determines whether the progressive phase loop in
// runProgressiveHarness should END now or CONTINUE to the next phase,
// based on the phase that just completed. Returns the overall exit
// reason to record on the master report (when ending) and whether to
// break the phase loop.
//
// The contract:
//
//   - ctx-cancelled phases yield "interrupted" + break (operator killed
//     the run; no further phases should be attempted).
//   - plateau-exited phases yield "plateau" + break (the AI exhausted
//     its per-phase recovery budget — plateau_iteration consecutive
//     zero-delta iters, each up to progress_no_improvement_timeout. End
//     the run here so the score reflects what the AI ACTUALLY
//     accomplished before stalling, not what it could have stumbled
//     into on later phases that it never had to actually engage with).
//   - solved-all phases yield "" + continue (the AI completed the
//     phase's scenarios; the curriculum keeps unlocking).
//
// Pre-2026-04-27 plateau also continued to the next phase. That
// silently let the AI "skip past" any phase it stalled on and rack up
// easier wins later. The /charly-internals:cutover-policy hard-cutover that
// landed today changed plateau to end-the-run; this helper exists so
// that decision is unit-testable without a full RunHarness fixture.
func decideOverallExit(ctxErr error, phaseExitReason string) (overallExitReason string, shouldBreak bool) {
	if ctxErr != nil {
		return "interrupted", true
	}
	if phaseExitReason == "plateau" {
		return "plateau", true
	}
	return "", false
}

// resolvePhaseScenarios returns the merged scenario list for the first
// `phaseN` recipes of `score.Recipe` (1-indexed phaseN). Each appended
// scenario is stamped with its source recipe name (matching
// ResolveScoreRecipe behavior). Returns the resolved recipe pointers
// in the same order, for the ${RECIPES} renderer.
func resolvePhaseScenarios(score *HarnessScore, recipeCatalog map[string]*HarnessRecipe, phaseN int) ([]Scenario, []*HarnessRecipe, error) {
	if phaseN <= 0 || phaseN > len(score.Recipe) {
		return nil, nil, fmt.Errorf("invalid phase %d (have %d recipes)", phaseN, len(score.Recipe))
	}
	subscore := *score
	subscore.Recipe = append([]string(nil), score.Recipe[:phaseN]...)
	return ResolveScoreRecipe(&subscore, recipeCatalog)
}

// finalizeMasterReport stamps the closing fields on a progressive
// master report after all phases have run.
func finalizeMasterReport(master *FinalReport, phasesCompleted int, exitReason string) {
	master.PhasesCompleted = phasesCompleted
	master.ExitReason = exitReason
	master.FinishedUTC = time.Now().UTC().Format(time.RFC3339)
	if master.Calver == "" {
		master.Calver = ComputeCalVer()
	}
	master.Summary = computeSummary(master.FinalScenario, len(master.FinalScenario))
}

// acquireHarnessLock takes an exclusive flock on the per-score lock file.
func acquireHarnessLock(projectDir, score string) (func(), error) {
	path := HarnessLockPath(projectDir, score)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("harness: another run is in progress for score %q (lock: %s)", score, path)
	}
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

// loadDescriptionsFromDir is retained for the (deprecated) image-baked
// path; the score-based flow uses synthesizeScoreBaseline instead.
func loadDescriptionsFromDir(dir, image string) *LabelDescriptionSet {
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		return nil
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg, err := LoadConfig(dir)
	if err != nil || cfg == nil {
		return nil
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return nil
	}
	return CollectDescriptions(cfg, layers, image)
}

func scopeMirrorPath(layout RunLayout) string {
	return filepath.Join(layout.RepoDir, ".eval")
}
