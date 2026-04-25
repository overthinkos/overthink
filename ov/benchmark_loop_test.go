package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Test scaffolding — fake subprocess seams (post-refactor: no Dispatcher,
// no loadWorktreeDescriptions seam — runRunnerFn now takes (ctx, layout, ...).
// ---------------------------------------------------------------------------

type fakeLoopState struct {
	iterStatuses     [][]string
	buildFailures    map[int]bool
	testInvocations  int
	perIterScenarios map[int][]ScenarioTestResult
}

func setupFakeLoop(t *testing.T, state *fakeLoopState, scenarios []ScenarioTestResult) func() {
	t.Helper()
	origBuild := buildImageFn
	origTest := runOvImageTestFn
	origRunner := runRunnerFn

	buildImageFn = func(ctx context.Context, repoDir, image, tag, logPath string) (time.Duration, error) {
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
			statuses := state.iterStatuses[k-1]
			for i, s := range scenarios {
				if i < len(statuses) {
					s.Status = statuses[i]
					if s.Status == "pass" {
						s.PendingSteps = 0
					}
				}
				iterScenarios = append(iterScenarios, s)
			}
		} else {
			iterScenarios = scenarios
		}
		r := TestRunResults{Scenarios: iterScenarios}
		out, _ := yaml.Marshal(r)
		return out, 50 * time.Millisecond, nil
	}
	runRunnerFn = func(ctx context.Context, layout RunLayout, argv []string, env map[string]string, logPath string) (time.Duration, error) {
		if logPath != "" {
			_ = os.WriteFile(logPath, []byte("fake-runner\n"), 0o644)
		}
		return 300 * time.Millisecond, nil
	}

	return func() {
		buildImageFn = origBuild
		runOvImageTestFn = origTest
		runRunnerFn = origRunner
	}
}

func extractIterFromTag(tag string) int {
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

// initFakeRepo creates a project dir with a minimal git repo and clones
// it into the per-run scratch path, simulating what CreateRunClone does
// inside the pod. Returns (projectDir, layout) ready for RunBenchmark.
func initFakeRepo(t *testing.T) (string, RunLayout) {
	t.Helper()
	projectDir := t.TempDir()
	mustGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = projectDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	mustGit("init", "--quiet", "--initial-branch=main")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(projectDir, "seed"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit("add", ".")
	mustGit("commit", "--quiet", "-m", "init", "--allow-empty")

	layout := NewRunLayout(projectDir, "test-"+t.Name())
	if err := os.MkdirAll(layout.RunDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Minimal "clone": copy the .git tree + working files into RepoDir.
	// Real CreateRunClone shells out to `git clone`; for unit tests we
	// just replicate its post-state.
	if err := os.MkdirAll(layout.RepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cloneCmd := exec.Command("git", "clone", "--quiet", "--no-local", projectDir, layout.RepoDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, string(out))
	}
	branchCmd := exec.Command("git", "-C", layout.RepoDir, "checkout", "-b", layout.Branch)
	if out, err := branchCmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout -b: %v\n%s", err, string(out))
	}
	cfgEmail := exec.Command("git", "-C", layout.RepoDir, "config", "user.email", "test@example.com")
	_ = cfgEmail.Run()
	cfgName := exec.Command("git", "-C", layout.RepoDir, "config", "user.name", "test")
	_ = cfgName.Run()

	return projectDir, layout
}

// setupOpts builds a BenchmarkOpts ready for RunBenchmark.
func setupOpts(t *testing.T, plateau, maxIter int) (BenchmarkOpts, RunLayout) {
	t.Helper()
	_, layout := initFakeRepo(t)

	scenarios, fps, tagFps := makeTestScenarios()

	opts := BenchmarkOpts{
		ProjectDir:         layout.RepoDir,
		Deployment:         "test-deploy",
		Runner:             &BenchmarkRunner{Name: "stub", Command: []string{"true"}, Timeout: "1m"},
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
	return opts, layout
}

// ---------------------------------------------------------------------------
// Plateau trajectory tests
// ---------------------------------------------------------------------------

func TestRunBenchmark_PlateauAfterNoProgress(t *testing.T) {
	opts, layout := setupOpts(t, 3, 50)

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

	report, err := RunBenchmark(context.Background(), opts, layout)
	if err != nil {
		t.Fatalf("RunBenchmark: %v", err)
	}
	if report.ExitReason != "plateau" {
		t.Errorf("exit_reason: got %q, want plateau", report.ExitReason)
	}
	if report.IterationsRun < 3 {
		t.Errorf("iterations_run: got %d, want at least 3", report.IterationsRun)
	}
	if report.BestScore != 0 {
		t.Errorf("best_score: got %d, want 0", report.BestScore)
	}
}

func TestRunBenchmark_ImprovementDefeatsPlateau(t *testing.T) {
	opts, layout := setupOpts(t, 3, 50)

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

	report, err := RunBenchmark(context.Background(), opts, layout)
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
	opts, layout := setupOpts(t, 2, 50)

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

	report, err := RunBenchmark(context.Background(), opts, layout)
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
	opts, layout := setupOpts(t, 10, 2)

	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "pass", "fail"},
		},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout)
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
	opts, layout := setupOpts(t, 2, 50)

	state := &fakeLoopState{
		iterStatuses: [][]string{
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
			{"pass", "fail", "fail"},
		},
		buildFailures: map[int]bool{2: true},
	}
	cleanup := setupFakeLoop(t, state, opts.PreAIScenarios)
	defer cleanup()

	report, err := RunBenchmark(context.Background(), opts, layout)
	if err != nil {
		t.Fatalf("RunBenchmark should not fail on build-failure: %v", err)
	}
	if len(report.Iterations) < 2 {
		t.Fatalf("want >= 2 iterations, got %d", len(report.Iterations))
	}
	if !report.Iterations[1].BuildFailure {
		t.Errorf("iter2 should have BuildFailure=true")
	}
	if report.Iterations[1].Score != report.Iterations[0].Score {
		t.Errorf("iter2 score should equal iter1 on build failure: got %d, want %d",
			report.Iterations[1].Score, report.Iterations[0].Score)
	}
}

// ---------------------------------------------------------------------------
// Scope rendering + file-based outputs
// ---------------------------------------------------------------------------

func TestRenderScope_IncludesHistory(t *testing.T) {
	opts, layout := setupOpts(t, 3, 50)

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
	unsolved := opts.PreAIScenarios[2:]

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

func TestWriteScope_MirrorsToRepoDir(t *testing.T) {
	_, layout := initFakeRepo(t)

	if err := os.MkdirAll(layout.IterDir(1), 0o755); err != nil {
		t.Fatal(err)
	}
	scope := &BenchmarkScope{RunID: layout.RunID, Iteration: 1}
	if err := writeScope(layout, 1, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(layout.IterDir(1), "scope.yml")); err != nil {
		t.Errorf("iter1 scope.yml missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.RepoDir, ".benchmark", "scope.yml")); err != nil {
		t.Errorf("repo .benchmark/scope.yml missing: %v", err)
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

func TestResolveAndPrintScope_ReadsRepoMirror(t *testing.T) {
	projectDir, layout := initFakeRepo(t)
	runID := layout.RunID

	mirror := filepath.Join(layout.RepoDir, ".benchmark")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatal(err)
	}
	expected := []byte("run_id: " + runID + "\niteration: 4\n")
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
	want := "ghcr.io/overthinkos/fedora-ov:ovbench-abc-iter2\n"
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
