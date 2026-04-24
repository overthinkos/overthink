package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Test scaffolding — fake subprocess seams
// ---------------------------------------------------------------------------

// fakeLoopState holds the per-iteration outputs the fakes emit.
type fakeLoopState struct {
	// Per-iteration scenario statuses. Index i is iteration i+1.
	// Each inner slice is the post-AI status for every scenario.
	iterStatuses [][]string
	// Build failures per iteration (1-indexed sparse).
	buildFailures map[int]bool
	// Iteration counter — how many times runOvImageTestFn fired.
	testInvocations int
	// Capture scenarios per iteration (key = iter).
	perIterScenarios map[int][]ScenarioTestResult
}

// setupFakeLoop installs fakes for buildImageFn, runOvImageTestFn,
// runRunnerFn, loadWorktreeDescriptions. Returns a cleanup func.
func setupFakeLoop(t *testing.T, state *fakeLoopState, scenarios []ScenarioTestResult) func() {
	t.Helper()
	origBuild := buildImageFn
	origTest := runOvImageTestFn
	origRunner := runRunnerFn
	origLoad := loadWorktreeDescriptions

	buildImageFn = func(ctx context.Context, worktreeDir, image, tag, logPath string) (time.Duration, error) {
		// Derive iter from tag: ovbench/<id>-iter<k>:<image>
		k := extractIterFromTag(tag)
		if state.buildFailures != nil && state.buildFailures[k] {
			return 100 * time.Millisecond, fmt.Errorf("fake build failure iter%d", k)
		}
		return 200 * time.Millisecond, nil
	}
	runOvImageTestFn = func(ctx context.Context, tag string) ([]byte, time.Duration, error) {
		state.testInvocations++
		k := extractIterFromTag(tag)
		var iterScenarios []ScenarioTestResult
		if state.perIterScenarios != nil {
			iterScenarios = state.perIterScenarios[k]
		} else if k-1 < len(state.iterStatuses) {
			// Fall back to the per-status vector with the frozen scenario set.
			statuses := state.iterStatuses[k-1]
			for i, s := range scenarios {
				if i < len(statuses) {
					s.Status = statuses[i]
					if s.Status == "pass" {
						// Passing scenarios have no pending steps —
						// this is the invariant Classify relies on.
						s.PendingSteps = 0
					}
				}
				iterScenarios = append(iterScenarios, s)
			}
		} else {
			iterScenarios = scenarios
		}
		r := TestRunResults{
			Scenarios: iterScenarios,
		}
		out, _ := yaml.Marshal(r)
		return out, 50 * time.Millisecond, nil
	}
	runRunnerFn = func(ctx context.Context, d Dispatcher, layout RunLayout, argv []string, env map[string]string, logPath string) (time.Duration, error) {
		// No-op — just record the invocation via log file presence.
		if logPath != "" {
			_ = os.WriteFile(logPath, []byte("fake-runner\n"), 0o644)
		}
		return 300 * time.Millisecond, nil
	}
	loadWorktreeDescriptions = func(opts BenchmarkOpts, layout RunLayout) *LabelDescriptionSet {
		return nil
	}

	return func() {
		buildImageFn = origBuild
		runOvImageTestFn = origTest
		runRunnerFn = origRunner
		loadWorktreeDescriptions = origLoad
	}
}

func extractIterFromTag(tag string) int {
	// Shape: ovbench/<id>-iter<k>:<image>
	idx := strings.Index(tag, "-iter")
	if idx < 0 {
		return 0
	}
	rest := tag[idx+5:]
	colon := strings.Index(rest, ":")
	if colon > 0 {
		rest = rest[:colon]
	}
	var k int
	fmt.Sscanf(rest, "%d", &k)
	return k
}

// makeTestScenarios returns a small pre-AI scenario set with pre-computed
// fingerprints for deterministic classification.
func makeTestScenarios() ([]ScenarioTestResult, map[string]string, map[string]string) {
	scenarios := []ScenarioTestResult{
		{ID: "desc:layer:sshd:0", Origin: "layer:sshd", Name: "A", Status: "fail", PendingSteps: 1},
		{ID: "desc:layer:foo:0", Origin: "layer:foo", Name: "B", Status: "fail", PendingSteps: 2},
		{ID: "desc:layer:bar:0", Origin: "layer:bar", Name: "C", Status: "fail", PendingSteps: 0},
	}
	fps := map[string]string{
		"desc:layer:sshd:0": "sha256:stable-a",
		"desc:layer:foo:0":  "sha256:stable-b",
		"desc:layer:bar:0":  "sha256:stable-c",
	}
	tagFps := map[string]string{
		"desc:layer:sshd:0": "sha256:tag-a",
		"desc:layer:foo:0":  "sha256:tag-b",
		"desc:layer:bar:0":  "sha256:tag-c",
	}
	return scenarios, fps, tagFps
}

// setupOpts builds a BenchmarkOpts ready for RunBenchmark.
func setupOpts(t *testing.T, projectDir string, plateau, maxIter int) (BenchmarkOpts, RunLayout, Dispatcher) {
	t.Helper()
	ctx := context.Background()
	layout := NewRunLayout(projectDir, "test-"+t.Name())
	if err := CreateWorktree(ctx, layout); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	scenarios, fps, tagFps := makeTestScenarios()

	opts := BenchmarkOpts{
		ProjectDir:         projectDir,
		Deployment:         "test-deploy",
		DeploymentNode:     &DeploymentNode{Target: "host"},
		Runner:             &BenchmarkRunner{Name: "stub", Command: []string{"echo"}, Timeout: "1m"},
		Prompt:             "fake prompt",
		TargetImage:        "fedora-ov",
		PlateauIterations:  plateau,
		MaxIterations:      maxIter,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		PreAIScenarios:     scenarios,
		PreFingerprints:    fps,
		PreTagFingerprints: tagFps,
	}
	d := &hostDispatcher{node: opts.DeploymentNode, name: opts.Deployment}
	return opts, layout, d
}

// ---------------------------------------------------------------------------
// Plateau trajectory tests
// ---------------------------------------------------------------------------

func TestRunBenchmark_PlateauAfterNoProgress(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, d := setupOpts(t, projectDir, 3, 50)
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	// Stub: scenarios never change — all fail every iteration.
	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"fail", "fail", "fail"},
			{"fail", "fail", "fail"},
			{"fail", "fail", "fail"},
			{"fail", "fail", "fail"},
		},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout, d)
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}
	if report.ExitReason != "plateau" {
		t.Errorf("exit_reason: got %q, want plateau", report.ExitReason)
	}
	// 1 initial iteration setting best=0 then 3 consecutive non-improving = 4 iterations total.
	if report.IterationsRun < 3 {
		t.Errorf("iterations_run: got %d, want at least 3", report.IterationsRun)
	}
	if report.BestScore != 0 {
		t.Errorf("best_score: got %d, want 0", report.BestScore)
	}
}

func TestRunBenchmark_ImprovementDefeatsPlateau(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, d := setupOpts(t, projectDir, 3, 50)
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	// iter1: scenario 0 passes (score=1)
	// iter2-4: same state (plateau counter increments 3x → stop at iter4)
	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
		},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout, d)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExitReason != "plateau" {
		t.Errorf("exit_reason: %q", report.ExitReason)
	}
	if report.BestScore != 1 {
		t.Errorf("best_score: got %d, want 1", report.BestScore)
	}
	if report.BestIteration != 1 {
		t.Errorf("best_iteration: got %d, want 1", report.BestIteration)
	}
}

func TestRunBenchmark_IncrementalProgress(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, d := setupOpts(t, projectDir, 2, 50)
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	// iter1: score=1; iter2: score=2; iter3: score=2; iter4: score=2 → plateau(2)
	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "pass", "fail"},
			{"pass", "pass", "fail"},
			{"pass", "pass", "fail"},
		},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout, d)
	if err != nil {
		t.Fatal(err)
	}
	if report.BestScore != 2 {
		t.Errorf("best_score: got %d, want 2", report.BestScore)
	}
	if report.BestIteration != 2 {
		t.Errorf("best_iteration: got %d, want 2", report.BestIteration)
	}
	if report.ExitReason != "plateau" {
		t.Errorf("exit_reason: %q", report.ExitReason)
	}
}

func TestRunBenchmark_CeilingStopsBeforePlateau(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, d := setupOpts(t, projectDir, 10, 2) // plateau large, ceiling small
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	// Always improving, but max-iterations=2 cuts it short.
	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "pass", "fail"},
		},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout, d)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExitReason != "ceiling" {
		t.Errorf("exit_reason: got %q, want ceiling", report.ExitReason)
	}
	if report.IterationsRun != 2 {
		t.Errorf("iterations_run: got %d, want 2", report.IterationsRun)
	}
}

func TestRunBenchmark_BuildFailureDoesNotCrash(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, d := setupOpts(t, projectDir, 2, 50)
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
		},
		buildFailures: map[int]bool{2: true}, // iter2 build fails
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout, d)
	if err != nil {
		t.Fatalf("RunBenchmark should not fail on build-failure: %v", err)
	}
	// iter2 should have BuildFailure=true.
	if len(report.Iterations) < 2 {
		t.Fatalf("want >= 2 iterations, got %d", len(report.Iterations))
	}
	if !report.Iterations[1].BuildFailure {
		t.Errorf("iter2 should have BuildFailure=true")
	}
	// Score should NOT regress (carried forward).
	if report.Iterations[1].Score != report.Iterations[0].Score {
		t.Errorf("iter2 score should equal iter1 on build failure: got %d, want %d",
			report.Iterations[1].Score, report.Iterations[0].Score)
	}
}

// ---------------------------------------------------------------------------
// Scope rendering + file-based outputs
// ---------------------------------------------------------------------------

func TestRenderScope_IncludesHistory(t *testing.T) {
	projectDir := initGitRepo(t)
	opts, layout, _ := setupOpts(t, projectDir, 3, 50)
	defer func() { _ = RemoveWorktree(context.Background(), layout) }()

	report := &FinalReport{
		BestScore: 2,
		Iterations: []IterationState{
			{K: 1, Score: 1, Scenarios: []ScenarioVerdict{
				{ID: "desc:layer:sshd:0", Verdict: VerdictSolved},
			}, PlateauCounterAfter: 0},
			{K: 2, Score: 2, Scenarios: []ScenarioVerdict{
				{ID: "desc:layer:sshd:0", Verdict: VerdictSolved},
				{ID: "desc:layer:foo:0", Verdict: VerdictSolved},
			}, PlateauCounterAfter: 0},
		},
	}
	unsolved := opts.PreAIScenarios[2:] // still-unsolved list for iter 3

	scope := renderScope(opts, layout, 3, report, unsolved)
	if len(scope.History) != 2 {
		t.Errorf("history should have 2 entries, got %d", len(scope.History))
	}
	if scope.BestScore != 2 {
		t.Errorf("best_score: %d", scope.BestScore)
	}
	if len(scope.Scenarios) != 1 {
		t.Errorf("scope scenarios should show unsolved (1), got %d", len(scope.Scenarios))
	}
}

func TestWriteScope_MirrorsToWorktree(t *testing.T) {
	projectDir := initGitRepo(t)
	layout := NewRunLayout(projectDir, "scope-mirror-test")
	ctx := context.Background()
	if err := CreateWorktree(ctx, layout); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = RemoveWorktree(ctx, layout) }()

	if err := os.MkdirAll(layout.IterDir(1), 0o755); err != nil {
		t.Fatal(err)
	}
	scope := &BenchmarkScope{RunID: layout.RunID, Iteration: 1}
	if err := writeScope(layout, 1, scope); err != nil {
		t.Fatal(err)
	}
	// Per-iter copy.
	if _, err := os.Stat(filepath.Join(layout.IterDir(1), "scope.yml")); err != nil {
		t.Errorf("iter1 scope.yml missing: %v", err)
	}
	// Mirrored copy in worktree.
	if _, err := os.Stat(filepath.Join(layout.WorktreeDir, ".benchmark", "scope.yml")); err != nil {
		t.Errorf("worktree scope.yml missing: %v", err)
	}
}

// ---------------------------------------------------------------------------
// computeSummary
// ---------------------------------------------------------------------------

func TestComputeSummary_Tally(t *testing.T) {
	scenarios := []ScenarioVerdict{
		{Verdict: VerdictSolved}, {Verdict: VerdictSolved},
		{Verdict: VerdictPartial},
		{Verdict: VerdictUnchanged},
		{Verdict: VerdictRegressed},
		{Verdict: VerdictTampered},
		{Verdict: VerdictAdded},
	}
	s := computeSummary(scenarios, 6)
	if s.Input != 6 || s.Solved != 2 || s.Partial != 1 || s.Unchanged != 1 ||
		s.Regressed != 1 || s.Tampered != 1 || s.Added != 1 {
		t.Errorf("counts wrong: %+v", s)
	}
	want := 2.0 / 6.0 * 100.0
	if s.PercentSolved < want-0.01 || s.PercentSolved > want+0.01 {
		t.Errorf("percent_solved: got %v, want ~%v", s.PercentSolved, want)
	}
}

// ---------------------------------------------------------------------------
// ResolveAndPrintScope + ResolveLastTestTag
// ---------------------------------------------------------------------------

func TestResolveAndPrintScope_ReadsWorktreeMirror(t *testing.T) {
	projectDir := initGitRepo(t)
	runID := "scope-read-test"
	layout := NewRunLayout(projectDir, runID)
	ctx := context.Background()
	if err := CreateWorktree(ctx, layout); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = RemoveWorktree(ctx, layout) }()

	mirror := filepath.Join(layout.WorktreeDir, ".benchmark")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	expected := []byte("run_id: scope-read-test\niteration: 4\n")
	if err := os.WriteFile(filepath.Join(mirror, "scope.yml"), expected, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("OV_BENCHMARK_RUN_ID", runID)

	tmpOut, err := os.CreateTemp("", "scope-out-*.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpOut.Name())

	if err := ResolveAndPrintScope(projectDir, tmpOut); err != nil {
		t.Fatalf("ResolveAndPrintScope: %v", err)
	}
	tmpOut.Close()
	got, _ := os.ReadFile(tmpOut.Name())
	if string(got) != string(expected) {
		t.Errorf("scope content: got %q, want %q", got, expected)
	}
}

func TestResolveLastTestTag(t *testing.T) {
	t.Setenv("OV_BENCHMARK_RUN_ID", "abc")
	t.Setenv("OV_BENCHMARK_ITERATION", "3")

	tmpOut, err := os.CreateTemp("", "tag-out-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpOut.Name())

	if err := ResolveLastTestTag("fedora-ov", tmpOut); err != nil {
		t.Fatal(err)
	}
	tmpOut.Close()
	got, _ := os.ReadFile(tmpOut.Name())
	want := "ovbench/abc-iter2:fedora-ov\n"
	if string(got) != want {
		t.Errorf("tag: got %q, want %q", string(got), want)
	}
}

func TestResolveLastTestTag_Iter1Errors(t *testing.T) {
	t.Setenv("OV_BENCHMARK_RUN_ID", "abc")
	t.Setenv("OV_BENCHMARK_ITERATION", "1")

	tmpOut, _ := os.CreateTemp("", "tag-*.txt")
	defer os.Remove(tmpOut.Name())

	if err := ResolveLastTestTag("fedora-ov", tmpOut); err == nil {
		t.Error("iter 1 should have no prior tag")
	}
}
