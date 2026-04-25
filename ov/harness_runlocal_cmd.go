package main

// harness_runlocal_cmd.go — in-target entry point of the harness.
//
// The host-side `ov harness run <score>` is a thin forwarder. All real
// work happens here, executed *inside the chosen target* (pod via
// `podman exec`, vm via `ssh`, or host directly).
//
// Responsibilities (in order):
//   1. Acquire .harness/<score>/.lock — per-target concurrency guard
//   2. Resolve the score's `recipes:` to a merged scenario list
//   3. Clone <project> -> <project>/.harness/<score>/runs/<run-id>/repo
//   4. Create branch ovharness/<run-id> + submodule init
//   5. Synthesize the pre-AI baseline from the merged scenarios
//   6. Drive RunHarness — the iteration state machine
//   7. Push branch back to the bind-mounted/host project repo
//   8. Release lock

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// HarnessRunLocalCmd drives the iteration loop in the chosen target.
type HarnessRunLocalCmd struct {
	Score       string `arg:"" help:"Score name (from harness.yml)"`
	TargetImage string `name:"target-image" help:"Target image to score (default: derived from score / pod)"`
	AI          string `name:"ai" help:"AI to invoke (defaults to score.ai when single-element)"`
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

func (c *HarnessRunLocalCmd) Run() error {
	ctx := context.Background()

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
		return fmt.Errorf("ov harness run-local: no overthink.yml in %s", projectDir)
	}

	score, err := ResolveScore(uf.Score, c.Score)
	if err != nil {
		return err
	}
	tk, tn, err := ResolveScoreTarget(score)
	if err != nil {
		return err
	}

	// Resolve the score's `recipes:` list against the recipe catalog.
	mergedScenarios, resolvedRecipes, err := ResolveScoreRecipes(score, uf.Recipe)
	if err != nil {
		return fmt.Errorf("score %q: %w", c.Score, err)
	}

	// AI selection — score.AI is the eligible list; --ai picks one.
	aiName := c.AI
	if aiName == "" {
		if len(score.AI) == 1 {
			aiName = score.AI[0]
		} else if len(score.AI) == 0 {
			return fmt.Errorf("score %q has empty ai: list", c.Score)
		} else {
			return fmt.Errorf("score %q has multiple eligible AIs (%v); pass --ai NAME", c.Score, score.AI)
		}
	}
	ai, _, err := ResolveAI(uf.AI, aiName)
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

	// Pre-flight: ensure the score's deployment is FRESH and DISPOSABLE.
	// The deployment must exist in deploy.yml with `disposable: true`
	// (per /ov-dev:disposable, no auto-derivation — explicit opt-in).
	// The harness explicitly builds the target image fresh from host
	// source, then uses `ov rebuild --reuse-image` to recreate the
	// container without re-triggering the (intentionally-failing) layer
	// scenario tests. The fresh container persists for the duration of
	// the run; the AI's per-iteration `ov update` swaps the image
	// without destroying the container. Next run rebuilds fresh again.
	if score.Deployment != "" && !c.DryRun {
		if err := ensureFreshDisposableDeployment(ctx, projectDir, score.Deployment, targetImage); err != nil {
			return fmt.Errorf("preflight deploy %q: %w", score.Deployment, err)
		}
	}

	// Synthesize the pre-AI baseline from the merged scenario set.
	// All scenarios start fail; fingerprints come from the scenario
	// YAML so post-iteration fingerprint comparison works.
	preAIResults, preFingerprints, preTagFingerprints := synthesizeScoreBaseline(c.Score, mergedScenarios)

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

	opts := HarnessOpts{
		ProjectDir:         projectDir,
		ScoreName:          c.Score,
		Score:              score,
		Recipes:            append([]string(nil), score.Recipes...),
		ResolvedRecipes:    resolvedRecipes,
		MergedScenarios:    mergedScenarios,
		TargetKind:         string(tk),
		TargetName:         tn,
		AIName:             aiName,
		AI:                 ai,
		Prompt:             score.Prompt,
		TargetImage:        targetImage,
		Tag:                tagExpr,
		PlateauIteration:   plateau,
		MaxScenario:        c.MaxScenario,
		MCPEndpoint:        mcp,
		Notes:              notesSnap,
		DryRun:             c.DryRun,
		SkipRebuild:        c.SkipRebuild,
		Format:             c.Format,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		PreAIScenario:      preAIResults,
		PreFingerprints:    preFingerprints,
		PreTagFingerprints: preTagFingerprints,
	}

	report, err := RunHarness(ctx, opts, layout)
	if err != nil {
		return err
	}
	report.AIVersion = map[string]string{aiName: aiVer.String()}

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

// ensureFreshDisposableDeployment guarantees that the score's
// deployment is registered in deploy.yml with `disposable: true` and
// that the container is freshly rebuilt from a freshly-built image
// before iter1.
//
// Implementation note: we don't use `ov rebuild <name>` directly because
// rebuild does `ov image build` + `ov image test` internally, and bench
// targets ship intentionally-unsolved layer-baked scenarios (the whole
// point of a benchmark target is that scenarios fail until the AI
// solves them). So we build the image ourselves, then `ov rebuild
// --reuse-image` to skip the test-of-unsolved-baseline.
//
// Contract:
//   - Hard error if the deployment is not registered (hint: ov deploy add)
//   - Hard error if the deployment is not marked disposable (per the
//     /ov-dev:disposable explicit-opt-in rule — never auto-flipped)
//   - On success, the container is fresh from a clean image and ready
//     to be scored against. It persists for the rest of the run; the
//     AI's per-iteration `ov update <name>` swaps the image without
//     destroying the container. Next harness run rebuilds again.
func ensureFreshDisposableDeployment(ctx context.Context, projectDir, name, targetImage string) error {
	cfg, err := LoadDeployConfig()
	if err != nil {
		return fmt.Errorf("load deploy.yml: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no deploy.yml found — run `ov deploy add %s <ref> --disposable` first", name)
	}
	entry, ok := cfg.Deployment[name]
	if !ok {
		return fmt.Errorf(
			"deployment %q is not registered in deploy.yml. The harness needs a pre-existing deployment to rebuild fresh per run. Run: ov deploy add %s <image-or-layer-ref> --disposable",
			name, name,
		)
	}
	if !entry.IsDisposable() {
		return fmt.Errorf(
			"deployment %q is registered but not marked `disposable: true`. The harness performs an `ov rebuild` per run, which is autonomous-destroy semantics — explicit opt-in is required (see /ov-dev:disposable). Edit ~/.config/ov/deploy.yml: under `deployment.%s`, add `disposable: true`",
			name, name,
		)
	}
	if targetImage == "" {
		return fmt.Errorf(
			"deployment %q: harness preflight needs score.target_image to know which image to build. Set `target_image:` on the score in harness.yml",
			name,
		)
	}

	ov := findOvForBenchmark()

	// 1. Build the target image fresh from host source.
	fmt.Fprintf(os.Stderr, "harness: preflight build of image %q (fresh from host source)\n", targetImage)
	build := exec.CommandContext(ctx, ov, "-C", projectDir, "image", "build", targetImage)
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("ov image build %s: %w", targetImage, err)
	}

	// 2. Rebuild the deployment with --reuse-image (skip the redundant
	//    build-then-test step that ov rebuild does internally; the test
	//    against the unsolved baseline scenarios would always fail).
	fmt.Fprintf(os.Stderr, "harness: preflight rebuild of disposable deployment %q (fresh-per-run)\n", name)
	rebuild := exec.CommandContext(ctx, ov, "-C", projectDir, "rebuild", "--reuse-image", name)
	rebuild.Stdout = os.Stderr
	rebuild.Stderr = os.Stderr
	if err := rebuild.Run(); err != nil {
		return fmt.Errorf("ov rebuild --reuse-image %s: %w", name, err)
	}
	fmt.Fprintf(os.Stderr, "harness: deployment %q is fresh and running; persists for the duration of this run\n", name)
	return nil
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
	layers, err := ScanLayers(dir)
	if err != nil {
		return nil
	}
	return CollectDescriptions(cfg, layers, image)
}

func scopeMirrorPath(layout RunLayout) string {
	return filepath.Join(layout.RepoDir, ".harness")
}
