package main

// benchmark_worktree.go — git worktree lifecycle + per-iteration commits.
//
// Each benchmark run gets its own git worktree on a dedicated branch
// `ovbench/<run-id>`. The worktree is the AI's workspace: the AI edits
// in it, the harness rebuilds from it, every iteration is captured as
// a real git commit on that branch.
//
// Design rules (plan §2.3):
//   - Hooks RUN on per-iteration commits (no --no-verify). Per the
//     project's cutover policy, hook bypasses require explicit approval.
//   - CommitIteration uses --allow-empty so no-op iterations still
//     leave an audit marker on the branch.
//   - RemoveWorktree is idempotent; ListRuns classifies complete vs.
//     incomplete without hiding either.
//
// All operations shell out to git; no external git library.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// RunLayout is the canonical set of paths for one benchmark run,
// rooted at ProjectDir.
type RunLayout struct {
	ProjectDir  string // the project root where .benchmark/ lives
	RunID       string // "<UTC-timestamp>-<shorthash>"
	RunDir      string // <ProjectDir>/.benchmark/<run-id>
	WorktreeDir string // <ProjectDir>/.benchmark/<run-id>/worktree
	Branch      string // "ovbench/<run-id>"
}

// NewRunLayout constructs a RunLayout from projectDir. A run-id is
// generated if runID == "".
func NewRunLayout(projectDir, runID string) RunLayout {
	if runID == "" {
		runID = GenerateRunID()
	}
	runDir := filepath.Join(projectDir, ".benchmark", runID)
	return RunLayout{
		ProjectDir:  projectDir,
		RunID:       runID,
		RunDir:      runDir,
		WorktreeDir: filepath.Join(runDir, "worktree"),
		Branch:      "ovbench/" + runID,
	}
}

// GenerateRunID returns a fresh run identifier of the shape
// <UTC-timestamp>-<shorthash>, deterministic per-process and
// readable in sort-friendly form.
func GenerateRunID() string {
	ts := time.Now().UTC().Format("20060102-150405")
	buf := make([]byte, 3) // 6 hex chars
	if _, err := rand.Read(buf); err != nil {
		// Should never fail; fall back to a fixed suffix rather than panic.
		return ts + "-000000"
	}
	return ts + "-" + hex.EncodeToString(buf)
}

// IterDir returns the path for iteration k under this run.
func (l RunLayout) IterDir(k int) string {
	return filepath.Join(l.RunDir, fmt.Sprintf("iter%d", k))
}

// ---------------------------------------------------------------------------
// Worktree lifecycle
// ---------------------------------------------------------------------------

// CreateWorktree runs `git -C <projectDir> worktree add <worktreeDir>
// HEAD -b <branch>`. Requires that <projectDir> is a git checkout.
// Fails cleanly if the branch already exists (without clobbering).
//
// The parent of WorktreeDir (RunDir) is created on demand.
func CreateWorktree(ctx context.Context, l RunLayout) error {
	if err := os.MkdirAll(l.RunDir, 0o755); err != nil {
		return fmt.Errorf("create run dir %s: %w", l.RunDir, err)
	}

	// Fail fast if the branch already exists — do not silently reuse,
	// because a stale branch would carry prior benchmark commits the
	// caller doesn't want mixed with this run.
	if branchExists(ctx, l.ProjectDir, l.Branch) {
		return fmt.Errorf("branch %s already exists; clean up prior run with `git branch -D %s`",
			l.Branch, l.Branch)
	}

	// git worktree add creates the dir; fail fast if it already exists.
	if _, err := os.Stat(l.WorktreeDir); err == nil {
		return fmt.Errorf("worktree path %s already exists", l.WorktreeDir)
	}

	// Submodules — `plugins/` (all 250+ skills) and `pkg/arch/` — MUST
	// populate cleanly: the AI running inside the worktree reads skills
	// from `plugins/`, and skill-less runs produce worse AI output. Any
	// submodule init failure here is a hard error and means the parent
	// repo's submodule pointer has drifted out of the remote's history;
	// fix the pointer at its source (push the unpushed commit or roll
	// the pointer back) rather than papering over it here.
	cmd := exec.CommandContext(ctx, "git", "-C", l.ProjectDir,
		"worktree", "add", l.WorktreeDir, "HEAD", "-b", l.Branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w\n%s", err, string(out))
	}
	return nil
}

// RemoveWorktree tears down the worktree + its branch. Idempotent: a
// missing worktree or missing branch does not error.
//
// Does NOT delete RunDir (which may contain valuable artifacts —
// iter<k>/*.yml, runner.log, build.log). Pruning RunDir is explicit
// via PruneRun.
func RemoveWorktree(ctx context.Context, l RunLayout) error {
	// `git worktree remove --force` handles both a live worktree and a
	// tree that has already been deleted from disk.
	_ = exec.CommandContext(ctx, "git", "-C", l.ProjectDir,
		"worktree", "remove", "--force", l.WorktreeDir).Run()

	// Prune stale metadata (handles the case where the dir was deleted
	// out-of-band).
	_ = exec.CommandContext(ctx, "git", "-C", l.ProjectDir,
		"worktree", "prune").Run()

	// Delete the branch. -D is forceful; acceptable because the branch
	// is scoped to a throwaway benchmark run.
	if branchExists(ctx, l.ProjectDir, l.Branch) {
		cmd := exec.CommandContext(ctx, "git", "-C", l.ProjectDir,
			"branch", "-D", l.Branch)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git branch -D %s: %w\n%s", l.Branch, err, string(out))
		}
	}
	return nil
}

// branchExists returns true if refs/heads/<branch> is present in the
// project's git database.
func branchExists(ctx context.Context, projectDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", projectDir,
		"show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

// ---------------------------------------------------------------------------
// Per-iteration commits
// ---------------------------------------------------------------------------

// CommitIteration stages all changes in the worktree and creates a
// commit on the ovbench branch. Hooks RUN — there is no --no-verify.
// --allow-empty is on so no-op iterations leave a marker commit.
//
// Returns the resulting commit SHA, or "" if hooks aborted the commit
// (an abort is NOT fatal — the iteration's verdict is independent of
// the commit).
func CommitIteration(ctx context.Context, l RunLayout, k int, score int, solvedIDs []string) (string, error) {
	// Stage everything. `git add -A` is intentional: we want the worktree
	// to be a complete snapshot of the AI's state, not a cherry-pick.
	addCmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A (iter%d): %w\n%s", k, err, string(out))
	}

	msg := formatCommitMessage(k, score, solvedIDs)

	// Check whether there's anything staged. `git diff --cached --quiet`
	// exits 0 when staged == HEAD. We still allow empty commits, but
	// the caller may want to distinguish.
	commitCmd := exec.CommandContext(ctx, "git", "-C", l.WorktreeDir,
		"commit", "--allow-empty", "-m", msg)
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		// Hook abort: return empty SHA + the error. The loop's error
		// matrix (see §2.5) treats this as a warning, not a failure.
		return "", fmt.Errorf("git commit (iter%d) rejected (likely by pre-commit hook): %w\n%s",
			k, err, string(out))
	}

	sha, err := resolveHeadSHA(ctx, l.WorktreeDir)
	if err != nil {
		// Commit succeeded but we couldn't read HEAD — odd but not fatal.
		return "", fmt.Errorf("commit succeeded but could not read HEAD: %w", err)
	}
	return sha, nil
}

// formatCommitMessage builds the canonical per-iteration commit message.
// Subject line: "iter<k>: score=<N>, solved=[<comma-sep-ids>]".
// Body: empty — the iter<k>/ artifacts carry the detail.
func formatCommitMessage(k int, score int, solvedIDs []string) string {
	idsTrunc := truncateIDs(solvedIDs, 6)
	return fmt.Sprintf("iter%d: score=%d, solved=[%s]", k, score, strings.Join(idsTrunc, ","))
}

// truncateIDs trims a slice to the first max entries, appending "..."
// if more were dropped. Keeps commit subjects readable.
func truncateIDs(ids []string, max int) []string {
	if len(ids) <= max {
		return ids
	}
	out := append([]string(nil), ids[:max]...)
	return append(out, fmt.Sprintf("...+%d", len(ids)-max))
}

// resolveHeadSHA returns the HEAD commit SHA for the given worktree.
func resolveHeadSHA(ctx context.Context, worktreeDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreeDir, "rev-parse", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ---------------------------------------------------------------------------
// Run enumeration
// ---------------------------------------------------------------------------

// RunSummary describes one past benchmark run found under .benchmark/.
type RunSummary struct {
	RunID        string
	RunDir       string
	Status       string    // "complete" (report.yml present) | "incomplete"
	StartedUTC   time.Time // parsed from RunID when possible
	HasWorktree  bool      // true iff worktree/ directory exists
	BranchExists bool      // true iff ovbench/<run-id> still exists
}

// ListRuns walks <projectDir>/.benchmark and returns one RunSummary
// per directory with a parsable run-id shape. Corrupted or partial
// runs are INCLUDED (never hidden) with Status == "incomplete".
//
// The returned slice is sorted by StartedUTC descending (newest first).
func ListRuns(ctx context.Context, projectDir string) ([]RunSummary, error) {
	base := filepath.Join(projectDir, ".benchmark")
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", base, err)
	}

	var out []RunSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runID := e.Name()
		s := RunSummary{
			RunID:  runID,
			RunDir: filepath.Join(base, runID),
		}
		// Status: complete iff report.yml is present.
		if _, err := os.Stat(filepath.Join(s.RunDir, "report.yml")); err == nil {
			s.Status = "complete"
		} else {
			s.Status = "incomplete"
		}
		// Worktree presence.
		if st, err := os.Stat(filepath.Join(s.RunDir, "worktree")); err == nil && st.IsDir() {
			s.HasWorktree = true
		}
		// Branch presence.
		s.BranchExists = branchExists(ctx, projectDir, "ovbench/"+runID)
		// Timestamp parse (best-effort).
		s.StartedUTC = parseRunIDTimestamp(runID)
		out = append(out, s)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedUTC.After(out[j].StartedUTC)
	})
	return out, nil
}

// parseRunIDTimestamp extracts the leading YYYYMMDD-HHMMSS portion of
// a run-id and parses it as UTC. Returns the zero time on malformed
// input — ListRuns sorts such entries last.
func parseRunIDTimestamp(runID string) time.Time {
	// Expected: 20060102-150405-abcdef
	if len(runID) < 15 {
		return time.Time{}
	}
	stamp := runID[:15]
	t, err := time.Parse("20060102-150405", stamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---------------------------------------------------------------------------
// Pruning (opt-in; not called by default)
// ---------------------------------------------------------------------------

// PruneRun deletes <run-id>'s RunDir AND removes its worktree + branch.
// Intended for `ov benchmark list --prune --older-than 7d` (v2). This
// helper is exported now so the cleanup path exists; the CLI wrapper
// is deferred.
func PruneRun(ctx context.Context, l RunLayout) error {
	if err := RemoveWorktree(ctx, l); err != nil {
		// Non-fatal — we still try to remove the on-disk dir.
		_ = err
	}
	if err := os.RemoveAll(l.RunDir); err != nil {
		return fmt.Errorf("remove run dir %s: %w", l.RunDir, err)
	}
	return nil
}
