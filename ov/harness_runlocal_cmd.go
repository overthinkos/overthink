package main

// harness_runlocal_cmd.go — in-target entry point of the harness.
//
// The host-side `ov harness run <recipe>` is a thin forwarder. All real
// work happens here, executed *inside the chosen target* (pod via
// `podman exec`, vm via `ssh`, or host directly).
//
// Responsibilities (in order):
//   1. Acquire .harness/<recipe>/.lock — per-target concurrency guard
//   2. Clone <project> -> <project>/.harness/<recipe>/runs/<run-id>/repo
//   3. Create branch ovharness/<run-id> + submodule init
//   4. Collect pre-AI baseline (descriptions + fingerprints from the clone)
//   5. Drive RunHarness — the iteration state machine
//   6. Push branch back to the bind-mounted/host project repo
//   7. Release lock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// HarnessRunLocalCmd drives the iteration loop in the chosen target.
type HarnessRunLocalCmd struct {
	Recipe       string `arg:"" help:"Recipe name (from harness.yml)"`
	TargetImage  string `name:"target-image" help:"Target image to score (default: derived from recipe / pod)"`
	AI           string `name:"ai" help:"AI to invoke (defaults to recipe.ai when single-element)"`
	RunID        string `name:"run-id" help:"Run identifier (set by host harness; auto if empty)"`
	PlateauIter  int    `name:"plateau-iteration" help:"Override recipe.plateau_iteration"`
	MaxIter      int    `name:"max-iteration" help:"Override recipe.max_iteration"`
	MaxScenario  int    `name:"max-scenario" help:"Cap the pending input set"`
	Tag          string `name:"tag" help:"Gherkin tag expression to narrow scenarios"`
	DryRun       bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild  bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only scenarios)"`
	Format       string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
	NoLock       bool   `name:"no-lock" hidden:"" help:"Skip flock (tests only)"`
	ProjectDir   string `name:"project-dir" hidden:"" help:"Override project root (default: cwd or /workspace)"`
}

// HarnessLockPath returns the absolute path of the per-recipe flock
// file under the harness data root (outside the project tree).
func HarnessLockPath(projectDir, recipe string) string {
	return filepath.Join(HarnessDataRoot(projectDir, recipe), ".lock")
}

func (c *HarnessRunLocalCmd) Run() error {
	ctx := context.Background()

	projectDir := c.ProjectDir
	if projectDir == "" {
		// Prefer /workspace if mounted (pod target); else cwd.
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

	recipe, err := ResolveRecipe(uf.Recipe, c.Recipe)
	if err != nil {
		return err
	}
	tk, tn, err := ResolveRecipeTarget(recipe)
	if err != nil {
		return err
	}

	// AI selection — recipe.AI is the eligible list; --ai picks one.
	aiName := c.AI
	if aiName == "" {
		if len(recipe.AI) == 1 {
			aiName = recipe.AI[0]
		} else if len(recipe.AI) == 0 {
			return fmt.Errorf("recipe %q has empty ai: list", c.Recipe)
		} else {
			return fmt.Errorf("recipe %q has multiple eligible AIs (%v); pass --ai NAME", c.Recipe, recipe.AI)
		}
	}
	ai, _, err := ResolveAI(uf.AI, aiName)
	if err != nil {
		return err
	}

	if !c.NoLock {
		unlock, lerr := acquireHarnessLock(projectDir, c.Recipe)
		if lerr != nil {
			return lerr
		}
		defer unlock()
	}

	layout := NewRunLayout(projectDir, c.Recipe, c.RunID)
	if err := os.MkdirAll(layout.RunDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", layout.RunDir, err)
	}
	fmt.Fprintf(os.Stderr, "harness: recipe=%s ai=%s run=%s where=%s:%s\n",
		c.Recipe, aiName, layout.RunID, tk, tn)

	if err := CreateRunClone(ctx, layout); err != nil {
		return fmt.Errorf("clone %s -> %s: %w", projectDir, layout.RepoDir, err)
	}

	targetImage := c.TargetImage
	if targetImage == "" && recipe.TargetImage != "" {
		targetImage = recipe.TargetImage
	}

	var preAIResults []ScenarioTestResult
	var preFingerprints, preTagFingerprints map[string]string
	if len(recipe.Scenario) > 0 {
		// Recipe-scenario mode: synthesize the baseline from recipe
		// scenarios directly (all marked fail, fingerprints computed
		// from the Scenario object). The harness scores these against
		// the live deployment ov-<recipe.deployment> after the AI
		// builds + deploys + tests.
		preAIResults, preFingerprints, preTagFingerprints = synthesizeRecipeBaseline(c.Recipe, recipe.Scenario)
	} else {
		preAIResults, preFingerprints, preTagFingerprints, err = collectPreAIBaselineFromDir(layout.RepoDir, targetImage)
		if err != nil {
			return fmt.Errorf("collect baseline: %w", err)
		}
	}

	tagExpr := c.Tag
	if tagExpr == "" {
		tagExpr = recipe.Tag
	}
	plateau := c.PlateauIter
	if plateau == 0 {
		plateau = recipe.PlateauIteration
	}
	maxIter := c.MaxIter
	if maxIter == 0 {
		maxIter = recipe.MaxIteration
	}

	notesSnap := ""
	if recipe.NotesEnabled() {
		notesSnap, _ = ReadNote(projectDir, c.Recipe)
	}

	mcp := recipe.EffectiveMCPEndpoint()

	// Build ai_version map by running version_command via local exec
	// (we're already inside the target, so direct exec is correct).
	aiVer := LocalCaptureVersion(ctx, ai)

	opts := HarnessOpts{
		ProjectDir:         projectDir,
		RecipeName:         c.Recipe,
		Recipe:             recipe,
		TargetKind:         string(tk),
		TargetName:         tn,
		AIName:             aiName,
		AI:                 ai,
		Prompt:             recipe.Prompt,
		TargetImage:        targetImage,
		Tag:                tagExpr,
		PlateauIteration:   plateau,
		MaxIteration:       maxIter,
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

	// Result file is written by writeReport (called inside RunHarness);
	// re-write here with the AIVersion now populated.
	_ = writeReport(layout, report)

	printHarnessReport(os.Stdout, report, c.Format)
	return nil
}

// acquireHarnessLock takes an exclusive flock on the per-recipe
// lock file under the harness data root.
func acquireHarnessLock(projectDir, recipe string) (func(), error) {
	path := HarnessLockPath(projectDir, recipe)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("harness: another run is in progress for recipe %q (lock: %s)", recipe, path)
	}
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

func collectPreAIBaselineFromDir(dir, image string) ([]ScenarioTestResult, map[string]string, map[string]string, error) {
	set := loadDescriptionsFromDir(dir, image)
	fingerprints := FingerprintSet(set)
	tagFingerprints := make(map[string]string)
	if set != nil {
		for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
			for _, ld := range sec {
				for sIdx, scenario := range ld.Description.Scenario {
					expanded := ExpandScenario(scenario)
					for _, es := range expanded {
						id := ScenarioID(ld.Origin, sIdx, es.RowIndex)
						tagFingerprints[id] = FingerprintTags(es.Tag)
					}
				}
			}
		}
	}
	var scenarios []ScenarioTestResult
	if set != nil {
		for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
			for _, ld := range sec {
				for sIdx, scenario := range ld.Description.Scenario {
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
							Tag:          es.Tag,
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
