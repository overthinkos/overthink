package main

// benchmark_runlocal_cmd.go — pod-side entry point of the benchmark.
//
// The host's `ov benchmark run <pod>` is a thin forwarder. All real
// work happens here, executed *inside the pod* via:
//
//   ov cmd <pod> -- ov benchmark run-local <target-image> --run-id <id> ...
//
// Responsibilities (in order):
//   1. Acquire /workspace/.benchmark/.lock — per-pod concurrency guard
//   2. Clone /workspace -> /workspace/.benchmark/<run-id>/repo
//   3. Create branch ovbench/<run-id> + submodule init
//   4. Collect pre-AI baseline (descriptions + fingerprints from the clone)
//   5. Drive RunBenchmark — the iteration state machine
//   6. Push branch back to the bind-mounted host repo at /workspace
//   7. Release lock
//
// The pod has its own `ov` binary (via the `ov-full` layer) and nested
// podman/buildah (via `container-nesting`), so RunBenchmark's calls to
// `ov image build` and `ov image test --format yaml` work unchanged
// inside the pod's nested storage.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// In-pod path constants.
const (
	HostRepoMount    = "/workspace"
	BenchRootDir     = "/workspace/.benchmark"
	BenchLockPath    = "/workspace/.benchmark/.lock"
	InPodMCPEndpoint = "http://localhost:18765/mcp"
)

// BenchmarkRunLocalCmd is the hidden pod-side iteration loop driver.
type BenchmarkRunLocalCmd struct {
	TargetImage       string `arg:"" help:"Target image to benchmark"`
	Runner            string `help:"Runner name (required if >1 configured)"`
	RunID             string `name:"run-id" required:"" help:"Run identifier (set by host harness)"`
	PlateauIterations int    `name:"plateau-iterations" default:"3" help:"Consecutive non-improving iterations that trigger stop"`
	MaxIterations     int    `name:"max-iterations" default:"50" help:"Hard ceiling; 0 = unbounded"`
	MaxScenarios      int    `name:"max-scenarios" help:"Cap the pending input set"`
	Tags              string `help:"Gherkin tag expression to narrow scenarios"`
	DryRun            bool   `name:"dry-run" help:"Render scope+prompt then exit; no AI invocation, no rebuild"`
	SkipRebuild       bool   `name:"skip-rebuild" help:"Skip per-iteration rebuild (source-only scenarios only)"`
	Format            string `enum:"text,yaml" default:"text" help:"Report format on stdout"`
	NoLock            bool   `name:"no-lock" hidden:"" help:"Skip flock (tests only)"`
}

// Run executes the pod-side iteration loop.
func (c *BenchmarkRunLocalCmd) Run() error {
	ctx := context.Background()

	if _, err := os.Stat(HostRepoMount); err != nil {
		return fmt.Errorf("benchmark run-local: %s not present — `ov benchmark run-local` must execute inside a pod with the ov-mcp /workspace bind-mount: %w", HostRepoMount, err)
	}

	if !c.NoLock {
		unlock, err := acquireBenchLock()
		if err != nil {
			return err
		}
		defer unlock()
	}

	cfg, err := LoadBenchmarkConfig(HostRepoMount)
	if err != nil {
		return fmt.Errorf("load benchmark config from %s: %w", HostRepoMount, err)
	}
	if cfg == nil {
		return errors.New("benchmark run-local: /workspace/overthink.yml has no benchmark: section — see /ov:benchmark")
	}
	runner, err := ResolveRunner(cfg, c.Runner)
	if err != nil {
		return err
	}

	layout := NewRunLayout(HostRepoMount, c.RunID)
	if err := os.MkdirAll(layout.RunDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", layout.RunDir, err)
	}
	fmt.Fprintf(os.Stderr, "benchmark: run-id %s, repo %s, branch %s\n", layout.RunID, layout.RepoDir, layout.Branch)

	if err := CreateRunClone(ctx, layout); err != nil {
		return fmt.Errorf("clone %s -> %s: %w", HostRepoMount, layout.RepoDir, err)
	}

	preAIResults, preFingerprints, preTagFingerprints, err := collectPreAIBaselineFromDir(layout.RepoDir, c.TargetImage)
	if err != nil {
		return fmt.Errorf("collect baseline: %w", err)
	}

	opts := BenchmarkOpts{
		ProjectDir:         layout.RepoDir,
		Deployment:         "(pod-self)",
		Runner:             runner,
		Prompt:             cfg.Prompt,
		TargetImage:        c.TargetImage,
		Tags:               c.Tags,
		PlateauIterations:  c.PlateauIterations,
		MaxIterations:      c.MaxIterations,
		MaxScenarios:       c.MaxScenarios,
		DryRun:             c.DryRun,
		SkipRebuild:        c.SkipRebuild,
		Format:             c.Format,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		PreAIScenarios:     preAIResults,
		PreFingerprints:    preFingerprints,
		PreTagFingerprints: preTagFingerprints,
	}

	report, err := RunBenchmark(ctx, opts, layout)
	if err != nil {
		return err
	}

	if err := PushBranchToHost(ctx, layout); err != nil {
		fmt.Fprintf(os.Stderr, "benchmark: push branch back to /workspace failed (non-fatal): %v\n", err)
	}

	printReport(os.Stdout, report, c.Format)
	return nil
}

// acquireBenchLock takes an exclusive flock on /workspace/.benchmark/.lock.
// Refuses concurrent runs in the same pod with a clear error.
func acquireBenchLock() (func(), error) {
	if err := os.MkdirAll(BenchRootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", BenchRootDir, err)
	}
	f, err := os.OpenFile(BenchLockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", BenchLockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("benchmark: another run is in progress in this pod (lock: %s)", BenchLockPath)
	}
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		_ = os.Remove(BenchLockPath)
	}, nil
}

// collectPreAIBaselineFromDir reads the per-run clone's descriptions
// and synthesizes the pre-AI baseline ScenarioTestResult set + fingerprints.
// Replaces the old host-side worktree-coupled collectPreAIBaseline.
func collectPreAIBaselineFromDir(dir, image string) ([]ScenarioTestResult, map[string]string, map[string]string, error) {
	set := loadDescriptionsFromDir(dir, image)
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

// loadDescriptionsFromDir loads project config from dir and returns
// its LabelDescriptionSet. Used by baseline collection and per-iteration
// post-state reload — replaces the old `loadWorktreeDescriptions` seam.
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

// scopeMirrorPath returns the path inside the per-run clone where
// scope.yml + prompt.md are mirrored for the AI to read via
// `ov benchmark scope`.
func scopeMirrorPath(layout RunLayout) string {
	return filepath.Join(layout.RepoDir, ".benchmark")
}
