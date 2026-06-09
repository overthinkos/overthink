package main

// eval_runner_cmd.go — `charly eval` command tree (host-side dispatcher).
//
// Post the 2026-04 kind split, the runner is keyed on `kind: score`.
// `recipe:` entries are pure spec; the user invokes a score, not a recipe.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The HarnessCmd top-level type was deleted in the eval-cutover. Its
// fields are absorbed into EvalCmd in eval_cmd.go; the per-subcommand
// types (EvalListAICmd, EvalRunCmd, EvalSyncCredCmd, EvalScopeCmd,
// EvalLastTagCmd, EvalSelfEvalCmd, EvalListRunsCmd, EvalReportCmd,
// EvalNoteCmd, EvalRunLocalCmd) live in this file (and the
// eval_runlocal_cmd.go / eval_synccreds_cmd.go siblings).

// ---------------------------------------------------------------------------
// list-ai / list-recipe / list-score — functional inspection
// ---------------------------------------------------------------------------

// EvalListAICmd implements `charly eval list-ai`.
type EvalListAICmd struct{}

func (c *EvalListAICmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No charly.yml found in current directory.")
		return nil
	}
	PrintAIs(os.Stdout, uf.AI)
	return nil
}

// EvalListRecipeCmd implements `charly eval list-recipe` — lists pure
// spec recipes (description + scenarios).
type EvalListRecipeCmd struct{}

func (c *EvalListRecipeCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No charly.yml found in current directory.")
		return nil
	}
	PrintRecipe(os.Stdout, uf.Recipe)
	return nil
}

// EvalListScoreCmd implements `charly eval list-score` — lists runner
// configs (target, AI, plateau, recipes).
type EvalListScoreCmd struct{}

func (c *EvalListScoreCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No charly.yml found in current directory.")
		return nil
	}
	PrintScores(os.Stdout, uf.Score)
	return nil
}

// ---------------------------------------------------------------------------
// run — host-side dispatcher
// ---------------------------------------------------------------------------

// EvalRunCmd is `charly eval run <name>` — overloaded by the kind the name
// resolves to: a kind:eval bed runs the full R10 sequence (build → eval
// image → deploy → eval live → fresh update → tear down); a kind:score
// drives the AI iteration loop. The two namespaces are disjoint (a name
// cannot be both — foldEvalBeds enforces it at load time).
type EvalRunCmd struct {
	Name string `arg:"" optional:"" help:"kind:eval bed (full R10 sequence) or kind:score (AI iteration loop). Omit with --all-beds."`
	AI   string `name:"ai" help:"Pick which AI to run (required if score.ai has more than one entry)"`

	// kind:eval bed-path flags (ignored on the kind:score path).
	AllBeds   bool `name:"all-beds" help:"Run every kind:eval bed (name-sorted) through the full R10 sequence"`
	Keep      bool `name:"keep" help:"kind:eval beds: don't tear the bed down after the run"`
	NoRebuild bool `name:"no-rebuild" help:"kind:eval beds: skip the fresh-update R10 re-verify step (R10 acceptance gate)"`

	// Mutually-exclusive target overrides (kind:score path).
	Pod  string `name:"on-pod" xor:"target" help:"Override score target with this pod deployment"`
	VM   string `name:"on-vm" xor:"target" help:"Override score target with this VM"`
	Host bool   `name:"on-host" xor:"target" help:"Override score target to run on the host directly"`

	PlateauIteration int    `name:"plateau-iteration" help:"Override score.plateau_iteration"`
	MaxScenario      int    `name:"max-scenario" help:"Cap the pending input set"`
	Tag              string `name:"tag" help:"Override score.tag (Gherkin tag expression)"`
	DryRun           bool   `name:"dry-run" help:"Render scope+prompt without rebuild"`
	SkipRebuild      bool   `name:"skip-rebuild" help:"Source-only scenarios"`
	KeepRepo         bool   `name:"keep-repo" help:"Don't delete the per-run repo clone after the run (~100MB; debugging only)"`
	Format           string `name:"format" enum:"text,yaml" default:"text" help:"Output format"`
}

func (c *EvalRunCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return err
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly eval run: no charly.yml in %s", cwd)
	}

	// Reusable-artifact retention: after the run completes (any path — bed,
	// --all-beds, or score), trim .eval to defaults.keep_eval_runs. Runs after
	// the new run's output is written, so the newest run is kept; NOTES.md is
	// always preserved. keep_eval_runs: 0 / absent disables. See `charly clean`.
	if keep := resolveIntPtr(uf.Defaults.KeepEvalRuns, nil, keepEvalRunsFallback); keep > 0 {
		defer func() {
			if removed, e := pruneEvalRuns(filepath.Join(cwd, ".eval"), keep, false); e == nil && len(removed) > 0 {
				fmt.Fprintf(os.Stderr, "Pruned %d old eval run artifact(s) (keep_eval_runs=%d)\n", len(removed), keep)
			}
		}()
	}

	// kind:eval bed dispatch — beds and scores share the `charly eval run`
	// verb. --all-beds runs every bed; a bare name that resolves to a bed
	// runs that one; otherwise fall through to the kind:score AI loop.
	beds := uf.EvalBeds()
	if c.AllBeds {
		return runAllEvalBeds(beds, bedRunOpts{Keep: c.Keep, NoRebuild: c.NoRebuild})
	}
	if c.Name == "" {
		return fmt.Errorf("charly eval run: provide a kind:eval bed or kind:score name, or pass --all-beds")
	}
	if node, isBed := beds[c.Name]; isBed {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			exe = os.Args[0]
		}
		res, runErr := runEvalBed(exe, c.Name, node, bedRunOpts{Keep: c.Keep, NoRebuild: c.NoRebuild})
		if res != nil {
			fmt.Fprintf(os.Stderr, "charly eval run %s: %s (steps=%d)\n",
				c.Name, summaryStatus(res.OK), len(res.Step))
		}
		// Propagate the eval-check exit code (2) when the bed failed at an
		// eval step (eval-image/eval-live reporting failing checks), so
		// `charly eval run <bed>` distinguishes "the thing under test is broken"
		// from an infra failure (build/deploy/vm-create) which stays exit 1.
		if runErr != nil && res != nil && res.FailExitCode == EvalCheckFailExitCode {
			return &EvalFailedError{Msg: fmt.Sprintf("charly eval run %s: eval checks failed", c.Name)}
		}
		return runErr
	}

	// kind:score path (AI iteration loop).
	score, err := ResolveScore(uf.Score, c.Name)
	if err != nil {
		return err
	}
	if c.Pod != "" {
		score.Pod = c.Pod
		score.VM = ""
		score.Host = false
	} else if c.VM != "" {
		score.VM = c.VM
		score.Pod = ""
		score.Host = false
	} else if c.Host {
		score.Host = true
		score.Pod = ""
		score.VM = ""
		score.Disposable = true
	}
	tk, tn, err := ResolveScoreTarget(score)
	if err != nil {
		return err
	}

	runID := GenerateRunID()
	args := []string{"eval", "run-local", c.Name, "--run-id", runID}
	if c.AI != "" {
		args = append(args, "--ai", c.AI)
	}
	if c.PlateauIteration > 0 {
		args = append(args, "--plateau-iteration", fmt.Sprintf("%d", c.PlateauIteration))
	}
	if c.MaxScenario > 0 {
		args = append(args, "--max-scenario", fmt.Sprintf("%d", c.MaxScenario))
	}
	if c.Tag != "" {
		args = append(args, "--tag", c.Tag)
	}
	if c.DryRun {
		args = append(args, "--dry-run")
	}
	if c.KeepRepo {
		args = append(args, "--keep-repo")
	}
	if c.SkipRebuild {
		args = append(args, "--skip-rebuild")
	}
	if c.Format != "" {
		args = append(args, "--format", c.Format)
	}

	// Per-run freshness for pod targets: if the harness sandbox is marked
	// disposable, restart its systemd quadlet unit so the container is
	// destroyed (`--rm` flag in the quadlet) and recreated from scratch
	// before dispatching. The sandbox pod IS the harness's sole disposable
	// resource — everything inside (deployments, images, AI work) is
	// the AI's job and lives inside the pod's nested podman, all
	// destroyed when the pod's container layer is wiped on restart.
	//
	// We deliberately do NOT go through a full redeploy here: that regenerates
	// the systemd quadlet via `charly config`, and the current generator
	// emits the image's default named volume AND the deploy.yml bind
	// override at the same mount path (a pre-existing dedup bug). Going
	// through systemctl restart uses the existing on-disk quadlet
	// unchanged.
	if tk == TargetKindPod {
		if cfg, _ := LoadDeployConfig(); cfg != nil {
			if entry, ok := cfg.Deploy[tn]; ok && entry.IsDisposable() {
				unit := "charly-" + tn + ".service"
				container := "charly-" + tn
				fmt.Fprintf(os.Stderr,
					"harness: preflight restart of disposable harness sandbox %q (fresh-per-run)\n", tn)
				cmd := exec.Command("systemctl", "--user", "restart", unit)
				cmd.Stdout = os.Stderr
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("preflight restart of %s: %w", unit, err)
				}
				// The restart wipes the container's writable layer (the
				// `--rm` flag in the quadlet means restart = destroy +
				// recreate). Re-sync the host's fresh charly binary AND
				// claude credentials into the pod so the in-pod harness
				// has the latest code AND can authenticate. Without
				// these, the AI invocation fails with auth errors and
				// the in-pod charly falls back to the (older) image-baked
				// version which lacks post-cutover commands.
				ready := exec.Command("podman", "inspect", "--format", "{{.State.Running}}", container)
				_ = ready.Run()
				if exe, err := os.Executable(); err == nil && exe != "" {
					sync := exec.Command("podman", "cp", exe, container+":/usr/local/bin/charly")
					sync.Stdout = os.Stderr
					sync.Stderr = os.Stderr
					if err := sync.Run(); err != nil {
						return fmt.Errorf("preflight sync of charly binary into %s: %w", container, err)
					}
				}
				// Re-sync AI credentials (claude creds, etc.) into the
				// freshly-restarted pod. Use the host's just-built charly
				// binary so the sync logic itself is post-cutover.
				credSync := exec.Command(findCharlyForEval(), "eval", "sync-credential", c.Name)
				credSync.Stdout = os.Stderr
				credSync.Stderr = os.Stderr
				if err := credSync.Run(); err != nil {
					return fmt.Errorf("preflight sync of credentials for score %q: %w", c.Name, err)
				}
			}
		}
	}

	switch tk {
	case TargetKindHost:
		// Test-bed image preflight. The deploy that prepared the host
		// installs layers (host packages + configs) only; container
		// images that scenarios spawn need to be pulled or built
		// before the score's runner walks them.
		if !c.DryRun {
			if err := ensureScoreImages(context.Background(), score, uf, cwd); err != nil {
				return err
			}
		}
		return runLocalInProcess(args, c.Name, runID, score, uf, cwd)
	case TargetKindPod:
		return dispatchToPod(tn, c.Name, args)
	case TargetKindVM:
		return dispatchToVM(tn, c.Name, args)
	}
	return fmt.Errorf("unsupported target kind: %s", tk)
}

// runAllEvalBeds runs every kind:eval bed (name-sorted for determinism)
// through the full R10 sequence — the `--all-beds` replacement for the
// retired `charly eval kind all`. Aggregates failures so one broken bed
// doesn't mask the rest.
func runAllEvalBeds(beds map[string]DeploymentNode, opts bedRunOpts) error {
	if len(beds) == 0 {
		return fmt.Errorf("charly eval run --all-beds: no kind:eval beds defined in eval.yml")
	}
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	names := make([]string, 0, len(beds))
	for n := range beds {
		names = append(names, n)
	}
	sort.Strings(names)
	var failures []string
	allEvalFails := true // every failure so far is a failing-checks (exit 2) failure
	for _, n := range names {
		res, runErr := runEvalBed(exe, n, beds[n], opts)
		if res != nil {
			fmt.Fprintf(os.Stderr, "charly eval run %s: %s (steps=%d)\n",
				n, summaryStatus(res.OK), len(res.Step))
		}
		if runErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", n, runErr))
			if res == nil || res.FailExitCode != EvalCheckFailExitCode {
				allEvalFails = false
			}
		}
	}
	if len(failures) > 0 {
		msg := fmt.Sprintf("charly eval run --all-beds: %d failure(s):\n  - %s",
			len(failures), strings.Join(failures, "\n  - "))
		// All failures were failing checks → eval-fail exit code (2). Any
		// infra failure (build/deploy/vm-create) keeps the generic exit 1.
		if allEvalFails {
			return &EvalFailedError{Msg: msg}
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// runLocalInProcess invokes EvalRunLocalCmd in-process for host targets.
func runLocalInProcess(args []string, scoreName, runID string, _ *HarnessScore, _ *UnifiedFile, cwd string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "charly"
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func dispatchToPod(podName, scoreName string, args []string) error {
	containerName := "charly-" + podName
	full := append([]string{"exec", "-i", containerName, "charly"}, args...)
	cmd := exec.Command("podman", full...)
	if err := runWithPhaseResync(cmd, scoreName); err != nil {
		return fmt.Errorf("podman exec %s: %w", containerName, err)
	}
	return mirrorPodHarnessDir(containerName)
}

func mirrorPodHarnessDir(containerName string) error {
	cmd := exec.Command("podman", "cp", containerName+":/workspace/.eval/.", "./.eval/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "harness: mirror artifacts back: %v (non-fatal)\n", err)
	}
	return nil
}

func dispatchToVM(vmName, scoreName string, args []string) error {
	full := append([]string{"vm", "ssh", vmName, "--", "charly"}, args...)
	cmd := exec.Command("charly", full...)
	return runWithPhaseResync(cmd, scoreName)
}

// evalPhaseRe matches the orchestrator's per-phase boundary marker:
//
//	harness: phase N/M — recipes [...] (K scenarios)
//
// Captures phase number N. Progress lines have the form
// `harness: progress [phase N/M iter K] ...` and are deliberately not
// matched — only the boundary line should trigger a credential resync.
var evalPhaseRe = regexp.MustCompile(`^harness: phase (\d+)/\d+ —`)

// phaseResyncFn is the credential-resync hook invoked by
// runWithPhaseResync at every phase boundary (N >= 2). Default invokes
// `charly eval sync-credential <score>` from the host. Tests override to
// record calls without spawning subprocesses.
var phaseResyncFn = func(scoreName string, phase int) error {
	cmd := exec.Command(findCharlyForEval(), "eval", "sync-credential", scoreName)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runWithPhaseResync runs cmd, forwarding stdout to os.Stdout, while
// watching stderr for the orchestrator's phase-boundary marker. On each
// new phase number N >= 2 it calls phaseResyncFn(scoreName, N) in a
// goroutine to refresh AI credentials on the target before iter 1's
// claude subprocess spawns.
//
// Why: the harness sandbox's `~/.claude/.credentials.json` is a one-shot copy
// taken by the host preflight. Anthropic OAuth access tokens are
// short-lived (typically ~8h, often less if the copy was already aged
// at run start). A long phase 4 — which can hold the AI for ~50 min on
// the watchdog — easily lets the in-pod token expire. After that, every
// claude spawn in subsequent phases bails with HTTP 401 and the run
// plateaus without ever exercising later phases.
//
// Phase 1 is intentionally skipped: EvalRunCmd.Run's preflight has
// already run `charly eval sync-credential` immediately before dispatch, so
// phase 1's claude has the freshest possible credentials.
//
// The resync runs concurrently with the orchestrator's per-phase
// preflight (synthesize baseline, render scope, etc.) so the
// `podman cp` typically completes before iter 1's claude actually
// spawns. If the race goes the other way, only iter 1 of the affected
// phase fails authentication; the resync wins by iter 2 and plateau
// detection (plateau_iteration >= 2) keeps the phase alive.
func runWithPhaseResync(cmd *exec.Cmd, scoreName string) error {
	cmd.Stdout = os.Stdout
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	seen := map[int]bool{1: true} // preflight already covered phase 1
	scanner := bufio.NewScanner(stderrPipe)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintln(os.Stderr, line)
		m := evalPhaseRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		go func(phase int) {
			fmt.Fprintf(os.Stderr,
				"harness: phase %d boundary — resyncing AI credentials before iter 1\n",
				phase)
			if err := phaseResyncFn(scoreName, phase); err != nil {
				fmt.Fprintf(os.Stderr,
					"harness: credential resync at phase %d failed (continuing): %v\n",
					phase, err)
			}
		}(n)
	}
	return cmd.Wait()
}

// ---------------------------------------------------------------------------
// sync-credential — `charly eval sync-credential <score>`
// ---------------------------------------------------------------------------

// EvalSyncCredCmd is `charly eval sync-credential <score>`.
type EvalSyncCredCmd struct {
	Score string `arg:"" help:"Score name"`
	AI    string `name:"ai" help:"Sync credentials for this AI only (default: all configured)"`
}

func (c *EvalSyncCredCmd) Run() error { return c.RunActual() }

// ---------------------------------------------------------------------------
// AI-facing iteration helpers
// ---------------------------------------------------------------------------

// EvalScopeCmd reads the active iteration's scope.yml.
type EvalScopeCmd struct{}

func (c *EvalScopeCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly eval scope: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, _ := os.Getwd()
	path := fmt.Sprintf("%s/.eval/%s/runs/%s/iter%s/scope.yml", cwd, score, runID, iter)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = os.Stdout.Write(data)
	return err
}

// EvalLastTagCmd prints the prior iteration's image tag.
type EvalLastTagCmd struct{}

func (c *EvalLastTagCmd) Run() error {
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	if runID == "" || iter == "" {
		return fmt.Errorf("charly eval last-test-tag: must run inside an iteration")
	}
	var k int
	fmt.Sscanf(iter, "%d", &k)
	if k <= 1 {
		return fmt.Errorf("charly eval last-test-tag: no prior iteration (k=%d)", k)
	}
	fmt.Printf("charlyeval-%s-iter%d\n", runID, k-1)
	return nil
}

// EvalSelfEvalCmd implements `charly eval self-evaluate` — the AI's
// canonical self-verification path during a harness iteration.
//
// Behavior: invokes the SAME RunEvalLive function the
// end-of-iter harness scorer calls, against the SAME in-scope recipe
// scenarios for the current phase, against the AI's live deployments.
// Output is a per-scenario verdict table (pass / fail / skipped).
// Exit 0 if every in-scope scenario passes, non-zero otherwise.
//
// Anti-deception properties:
//
//   - Reads `eval.yml` from the project tree (cwd → LoadUnified),
//     NOT from the per-iter repo clone the AI works in. Edits the AI
//     might make to `.harness/<run>/repo/eval.yml` do not change
//     what self-evaluate evaluates — the same load path the harness
//     scorer uses governs both.
//
//   - Always invokes RunEvalLive with RunScoringOpts{}
//     (zero value: ValidateAiArtifacts=false, IterStartTime=zero) so
//     EVERY probe physically executes against live systems. This is
//     what produces the canonical artifacts the harness scorer then
//     validates in `validate_ai_artifacts: true` mode. If self-eval
//     honored the flag, artifacts would never exist and the freshness
//     gate would always fail.
//
//   - The `score.ValidateAiArtifacts` field is intentionally ignored
//     here: self-evaluate's job is to PRODUCE artifacts via fresh
//     execution, not to consume them.
type EvalSelfEvalCmd struct{}

func (c *EvalSelfEvalCmd) Run() error {
	score := os.Getenv("CHARLY_EVAL_SCORE")
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	phaseStr := os.Getenv("CHARLY_EVAL_PHASE")
	if score == "" || runID == "" || iter == "" {
		return fmt.Errorf("charly eval self-evaluate: must run inside an iteration (CHARLY_EVAL_SCORE/RUN_ID/ITERATION env required)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	// Load eval.yml from the project tree, NOT from the per-iter
	// repo clone the AI works in. This is the load-bearing
	// anti-deception property: AI edits to the per-iter clone don't
	// change what self-evaluate sees, because the harness scorer at
	// iter end uses the same project-tree path.
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return fmt.Errorf("charly eval self-evaluate: load eval.yml: %w", err)
	}
	if !ok || uf == nil {
		return fmt.Errorf("charly eval self-evaluate: no eval.yml at project root %s — self-evaluate must run from a directory with a project tree containing eval.yml (typically /workspace inside the harness sandbox)", cwd)
	}
	resolvedScore, err := ResolveScore(uf.Score, score)
	if err != nil {
		return fmt.Errorf("charly eval self-evaluate: resolve score %q: %w", score, err)
	}

	// Determine the in-scope recipe set: progressive scores reveal
	// recipes per phase; non-progressive scores show all at once.
	var phase int
	if phaseStr != "" {
		fmt.Sscanf(phaseStr, "%d", &phase)
	}
	var scenarios []Scenario
	if resolvedScore.Progressive && phase > 0 {
		scenarios, _, err = resolvePhaseScenarios(resolvedScore, uf.Recipe, phase)
	} else {
		scenarios, _, err = ResolveScoreRecipe(resolvedScore, uf.Recipe)
	}
	if err != nil {
		return fmt.Errorf("charly eval self-evaluate: resolve scenarios: %w", err)
	}
	if len(scenarios) == 0 {
		fmt.Fprintln(os.Stdout, "charly eval self-evaluate: no in-scope scenarios for this phase")
		return nil
	}

	// Always-execute mode (RunScoringOpts zero value): no
	// validate-ai-artifacts shortcut, no freshness gate. Every probe
	// re-runs against live systems, producing fresh artifacts at the
	// recipe-declared `artifact:` paths.
	ctx := context.Background()
	live, err := RunEvalLive(ctx, resolvedScore.Deploy, score, scenarios, RunScoringOpts{})
	if err != nil {
		return fmt.Errorf("charly eval self-evaluate: live scoring: %w", err)
	}

	// Print verdict table — same fields the orchestrator's scorer
	// records into result-<calver>.yml. The AI reads stdout to see
	// pass/fail/skipped per scenario.
	fmt.Fprintf(os.Stdout, "self-evaluate: score=%s phase=%d iter=%s run=%s\n", score, phase, iter, runID)
	fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", "SCENARIO", "STATUS", "DETAIL")
	failed := 0
	for _, sc := range live.Scenario {
		detail := ""
		if sc.SkippedReason != "" {
			detail = sc.SkippedReason
		}
		fmt.Fprintf(os.Stdout, "%-50s  %-7s  %s\n", sc.Name, sc.Status, detail)
		if sc.Status != "pass" && sc.Status != "skip" {
			failed++
		}
	}
	fmt.Fprintf(os.Stdout, "summary: %d/%d pass, %d fail, %d skip (total %d)\n",
		live.Summary.Pass, live.Summary.Total, live.Summary.Fail, live.Summary.Skip, live.Summary.Total)
	if failed > 0 {
		return fmt.Errorf("self-evaluate: %d scenario(s) failed", failed)
	}
	return nil
}

// ---------------------------------------------------------------------------
// list / report — past-run inspection
// ---------------------------------------------------------------------------

// EvalListRunsCmd lists past runs across all scores.
type EvalListRunsCmd struct{}

func (c *EvalListRunsCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	runs, err := ListRuns(nil, cwd)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs found under .eval/.")
		return nil
	}
	fmt.Printf("%-20s  %-25s  %-10s  %s\n", "SCORE", "RUN_ID", "STATUS", "STARTED")
	for _, r := range runs {
		started := r.StartedUTC.Format("2006-01-02 15:04:05Z")
		fmt.Printf("%-20s  %-25s  %-10s  %s\n", r.Score, r.RunID, r.Status, started)
	}
	return nil
}

// EvalReportCmd prints a past result-<calver>.yml.
type EvalReportCmd struct {
	Score  string `arg:"" optional:"" help:"Score name (default: latest)"`
	Calver string `arg:"" optional:"" help:"Calver of the result to display (default: latest)"`
}

func (c *EvalReportCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	resultsRoot := fmt.Sprintf("%s/.harness", cwd)
	if c.Score == "" {
		entries, err := os.ReadDir(resultsRoot)
		if err != nil {
			return fmt.Errorf("no .harness directory in %s", cwd)
		}
		var newest os.DirEntry
		var newestT int64
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Unix() > newestT {
				newestT = info.ModTime().Unix()
				newest = e
			}
		}
		if newest == nil {
			return fmt.Errorf("no scores under %s", resultsRoot)
		}
		c.Score = newest.Name()
	}
	resultsDir := fmt.Sprintf("%s/%s/results", resultsRoot, c.Score)
	if c.Calver == "" {
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			return fmt.Errorf("no results directory: %s", resultsDir)
		}
		var latest string
		for _, e := range entries {
			n := e.Name()
			if len(n) > 7 && n[:7] == "result-" && n[len(n)-4:] == ".yml" {
				if n > latest {
					latest = n
				}
			}
		}
		if latest == "" {
			return fmt.Errorf("no result files under %s", resultsDir)
		}
		c.Calver = latest[7 : len(latest)-4]
	}
	path := fmt.Sprintf("%s/result-%s.yml", resultsDir, c.Calver)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// note read / note append — persistent NOTES.md memory
// ---------------------------------------------------------------------------

// EvalNoteCmd groups the note read/append verbs.
type EvalNoteCmd struct {
	Read   EvalNoteReadCmd   `cmd:"" help:"Print the persistent NOTES.md for a score"`
	Append EvalNoteAppendCmd `cmd:"" help:"Atomically append a note to a score's NOTES.md"`
}

type EvalNoteReadCmd struct {
	Score string `arg:"" optional:"" help:"Score name (default: $CHARLY_EVAL_SCORE)"`
}

func (c *EvalNoteReadCmd) Run() error {
	score := c.Score
	if score == "" {
		score = os.Getenv("CHARLY_EVAL_SCORE")
	}
	if score == "" {
		return fmt.Errorf("charly eval note read: score name required (pass as arg or set CHARLY_EVAL_SCORE)")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	body, err := ReadNote(dir, score)
	if err != nil {
		return err
	}
	if body == "" {
		fmt.Fprintln(os.Stdout, "(empty)")
		return nil
	}
	fmt.Fprint(os.Stdout, body)
	return nil
}

type EvalNoteAppendCmd struct {
	Score string `arg:"" help:"Score name (or skip and pass --score)"`
	Text  string `arg:"" help:"Note text (one paragraph)"`
}

func (c *EvalNoteAppendCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	ai := os.Getenv("CHARLY_EVAL_AI")
	if iter == "" {
		iter = "0"
	}
	return AppendNote(dir, c.Score, runID, iter, ai, c.Text)
}
