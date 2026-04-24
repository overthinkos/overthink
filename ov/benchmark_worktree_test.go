package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initGitRepo creates a tempdir and runs `git init` in it, returning the
// path. The returned repo has one initial commit so `git worktree add`
// can reference HEAD.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()

	mustGit := func(args ...string) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
		}
	}
	mustGit("init", "--initial-branch=main")
	// Set a test identity so commits don't fail on hosts without a
	// global git config.
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test User")
	// Seed an initial commit.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mustGit("add", "README.md")
	mustGit("commit", "-m", "init")
	return dir
}

// ---------------------------------------------------------------------------
// RunLayout
// ---------------------------------------------------------------------------

func TestGenerateRunID_Shape(t *testing.T) {
	id := GenerateRunID()
	// Expected: 20060102-150405-xxxxxx (15 + 1 + 6 = 22 chars)
	if len(id) != 22 {
		t.Errorf("run-id length: got %d, want 22 (got %q)", len(id), id)
	}
	if !strings.Contains(id, "-") {
		t.Errorf("run-id should contain dashes: %q", id)
	}
}

func TestGenerateRunID_Unique(t *testing.T) {
	a := GenerateRunID()
	b := GenerateRunID()
	if a == b {
		t.Errorf("consecutive run-ids must differ: %s == %s", a, b)
	}
}

func TestNewRunLayout(t *testing.T) {
	l := NewRunLayout("/proj", "test-id")
	if l.RunID != "test-id" {
		t.Errorf("RunID: %q", l.RunID)
	}
	if l.Branch != "ovbench/test-id" {
		t.Errorf("Branch: %q", l.Branch)
	}
	if l.RunDir != "/proj/.benchmark/test-id" {
		t.Errorf("RunDir: %q", l.RunDir)
	}
	if l.WorktreeDir != "/proj/.benchmark/test-id/worktree" {
		t.Errorf("WorktreeDir: %q", l.WorktreeDir)
	}
}

func TestNewRunLayout_AutoRunID(t *testing.T) {
	l := NewRunLayout("/proj", "")
	if l.RunID == "" {
		t.Error("empty runID should be auto-generated")
	}
}

func TestRunLayout_IterDir(t *testing.T) {
	l := NewRunLayout("/proj", "test-id")
	if got := l.IterDir(3); got != "/proj/.benchmark/test-id/iter3" {
		t.Errorf("IterDir(3): %q", got)
	}
}

// ---------------------------------------------------------------------------
// CreateWorktree / RemoveWorktree
// ---------------------------------------------------------------------------

func TestCreateWorktree_HappyPath(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-happy")

	ctx := context.Background()
	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	// Worktree dir exists + is a git working tree.
	if _, err := os.Stat(filepath.Join(l.WorktreeDir, ".git")); err != nil {
		t.Errorf(".git missing from worktree: %v", err)
	}
	// The seeded README.md is present in the worktree.
	if _, err := os.Stat(filepath.Join(l.WorktreeDir, "README.md")); err != nil {
		t.Errorf("README.md missing from worktree: %v", err)
	}
	// Branch exists.
	if !branchExists(ctx, projectDir, l.Branch) {
		t.Errorf("branch %s should exist", l.Branch)
	}
}

func TestCreateWorktree_BranchExistsErrors(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-exists")
	ctx := context.Background()

	// Pre-create the branch.
	cmd := exec.CommandContext(ctx, "git", "-C", projectDir, "branch", l.Branch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed branch: %v\n%s", err, out)
	}

	err := CreateWorktree(ctx, l)
	if err == nil {
		t.Fatal("want error when branch already exists; got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention branch exists: %v", err)
	}
}

func TestCreateWorktree_WorktreePathExistsErrors(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-path")
	ctx := context.Background()

	// Pre-create the run dir AND the worktree dir — worktree path clash.
	if err := os.MkdirAll(l.WorktreeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := CreateWorktree(ctx, l)
	if err == nil {
		t.Fatal("want error when worktree path exists; got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention path exists: %v", err)
	}
}

func TestRemoveWorktree_Idempotent(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-rm")
	ctx := context.Background()

	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(ctx, l); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	// Second remove must not error — this is the idempotency guarantee.
	if err := RemoveWorktree(ctx, l); err != nil {
		t.Errorf("second remove should be idempotent: %v", err)
	}
	// Branch is gone.
	if branchExists(ctx, projectDir, l.Branch) {
		t.Errorf("branch %s should be deleted", l.Branch)
	}
}

// ---------------------------------------------------------------------------
// CommitIteration
// ---------------------------------------------------------------------------

func TestCommitIteration_AllowEmpty(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-commit-empty")
	ctx := context.Background()

	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatal(err)
	}

	// No-op iteration: nothing changed in the worktree.
	sha, err := CommitIteration(ctx, l, 1, 0, nil)
	if err != nil {
		t.Fatalf("CommitIteration: %v", err)
	}
	if sha == "" {
		t.Error("SHA should be non-empty on successful commit")
	}

	// Verify commit landed on the branch by inspecting git log.
	cmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir, "log", "--format=%s", "-n1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "iter1: score=0") {
		t.Errorf("commit subject mismatch: %q", string(out))
	}
}

func TestCommitIteration_WithChanges(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-commit-change")
	ctx := context.Background()

	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatal(err)
	}
	// Make a change in the worktree.
	if err := os.WriteFile(filepath.Join(l.WorktreeDir, "new-file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sha, err := CommitIteration(ctx, l, 2, 3, []string{"id1", "id2", "id3"})
	if err != nil {
		t.Fatalf("CommitIteration: %v", err)
	}
	if sha == "" {
		t.Error("SHA should be non-empty")
	}

	cmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir, "log", "--format=%s", "-n1")
	out, _ := cmd.Output()
	want := "iter2: score=3, solved=[id1,id2,id3]"
	if !strings.Contains(string(out), want) {
		t.Errorf("commit subject: want substring %q, got %q", want, string(out))
	}
}

func TestCommitIteration_TruncatesManyIDs(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-commit-many")
	ctx := context.Background()

	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatal(err)
	}
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	if _, err := CommitIteration(ctx, l, 1, 8, ids); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir, "log", "--format=%s", "-n1")
	out, _ := cmd.Output()
	// Expect first 6 IDs + "...+2"
	if !strings.Contains(string(out), "...+2") {
		t.Errorf("truncation marker missing: %q", string(out))
	}
}

// ---------------------------------------------------------------------------
// ListRuns
// ---------------------------------------------------------------------------

func TestListRuns_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got, err := ListRuns(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0, got %d", len(got))
	}
}

func TestListRuns_MixedStatus(t *testing.T) {
	projectDir := initGitRepo(t)
	// Create two runs: one with report.yml (complete), one without (incomplete).
	completeRun := "20260424-100000-aaaaaa"
	incompleteRun := "20260424-110000-bbbbbb"
	completeDir := filepath.Join(projectDir, ".benchmark", completeRun)
	incompleteDir := filepath.Join(projectDir, ".benchmark", incompleteRun)
	if err := os.MkdirAll(completeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(completeDir, "report.yml"), []byte("run_id: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListRuns(context.Background(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 runs, got %d: %+v", len(got), got)
	}

	// Sort is descending by time; incomplete (110000) should come first.
	if got[0].RunID != incompleteRun {
		t.Errorf("order: got[0]=%s, want %s first (newer)", got[0].RunID, incompleteRun)
	}

	for _, s := range got {
		switch s.RunID {
		case completeRun:
			if s.Status != "complete" {
				t.Errorf("%s should be complete, got %q", s.RunID, s.Status)
			}
		case incompleteRun:
			if s.Status != "incomplete" {
				t.Errorf("%s should be incomplete, got %q", s.RunID, s.Status)
			}
		}
	}
}

func TestListRuns_MalformedRunIDStillIncluded(t *testing.T) {
	projectDir := initGitRepo(t)
	weirdRun := "not-a-timestamp"
	dir := filepath.Join(projectDir, ".benchmark", weirdRun)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListRuns(context.Background(), projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 run, got %d", len(got))
	}
	if got[0].RunID != weirdRun {
		t.Errorf("RunID: %q", got[0].RunID)
	}
	// StartedUTC should be zero (unparseable) but not cause an error.
	if !got[0].StartedUTC.IsZero() {
		t.Errorf("StartedUTC should be zero for malformed id, got %v", got[0].StartedUTC)
	}
}

func TestParseRunIDTimestamp(t *testing.T) {
	cases := []struct {
		id   string
		want time.Time
	}{
		{"20260424-221500-abcdef", time.Date(2026, 4, 24, 22, 15, 0, 0, time.UTC)},
		{"short", time.Time{}},
		{"20260424-ZZZZZZ-xxxxxx", time.Time{}},
	}
	for _, c := range cases {
		got := parseRunIDTimestamp(c.id)
		if !got.Equal(c.want) {
			t.Errorf("parse(%q) = %v; want %v", c.id, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// PruneRun
// ---------------------------------------------------------------------------

func TestPruneRun(t *testing.T) {
	projectDir := initGitRepo(t)
	l := NewRunLayout(projectDir, "test-prune")
	ctx := context.Background()

	if err := CreateWorktree(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := PruneRun(ctx, l); err != nil {
		t.Fatalf("PruneRun: %v", err)
	}
	if _, err := os.Stat(l.RunDir); !os.IsNotExist(err) {
		t.Errorf("RunDir should be gone after prune: err=%v", err)
	}
	if branchExists(ctx, projectDir, l.Branch) {
		t.Errorf("branch should be gone after prune")
	}
}
