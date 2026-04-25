package main

// benchmark_clone.go — per-run clone lifecycle + iteration commit + push-back.
//
// Replaces the host-side worktree subsystem. The new model: the pod's
// `ov benchmark run-local` clones /workspace into a per-run scratch
// dir (.benchmark/<run-id>/repo) on a fresh ovbench/<run-id> branch.
// Per-iteration commits land in that clone. At end of run the branch
// pushes back to the bind-mounted host repo for an audit trail.
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
// rooted at ProjectDir. Inside the pod, ProjectDir == /workspace.
type RunLayout struct {
	ProjectDir string // project root containing .benchmark/
	RunID      string // "<UTC-timestamp>-<shorthash>"
	RunDir     string // <ProjectDir>/.benchmark/<run-id>
	RepoDir    string // <ProjectDir>/.benchmark/<run-id>/repo  (per-run clone)
	Branch     string // "ovbench/<run-id>"
}

// NewRunLayout constructs a RunLayout from projectDir. Generates run-id if empty.
func NewRunLayout(projectDir, runID string) RunLayout {
	if runID == "" {
		runID = GenerateRunID()
	}
	runDir := filepath.Join(projectDir, ".benchmark", runID)
	return RunLayout{
		ProjectDir: projectDir,
		RunID:      runID,
		RunDir:     runDir,
		RepoDir:    filepath.Join(runDir, "repo"),
		Branch:     "ovbench/" + runID,
	}
}

// GenerateRunID returns a fresh UTC-timestamp-prefixed identifier.
func GenerateRunID() string {
	ts := time.Now().UTC().Format("20060102-150405")
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		return ts + "-000000"
	}
	return ts + "-" + hex.EncodeToString(buf)
}

// IterDir returns the path for iteration k under this run.
func (l RunLayout) IterDir(k int) string {
	return filepath.Join(l.RunDir, fmt.Sprintf("iter%d", k))
}

// ---------------------------------------------------------------------------
// Clone lifecycle
// ---------------------------------------------------------------------------

// CreateRunClone creates a per-run scratch clone at l.RepoDir on a
// fresh branch ovbench/<run-id>. Source: l.ProjectDir (the bind-mounted
// /workspace inside the pod). Uses --no-local for true content-only
// copy (no hardlinks back to the host repo's .git, so the AI can't
// accidentally pollute host refs from inside the pod).
//
// Submodules are initialized + updated so the AI sees the same
// `plugins/` skill set as a normal checkout.
func CreateRunClone(ctx context.Context, l RunLayout) error {
	if err := os.MkdirAll(l.RunDir, 0o755); err != nil {
		return fmt.Errorf("create run dir %s: %w", l.RunDir, err)
	}
	if _, err := os.Stat(l.RepoDir); err == nil {
		return fmt.Errorf("repo path %s already exists", l.RepoDir)
	}

	cloneCmd := exec.CommandContext(ctx, "git", "clone", "--no-local", l.ProjectDir, l.RepoDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone --no-local %s %s: %w\n%s", l.ProjectDir, l.RepoDir, err, string(out))
	}

	branchCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "checkout", "-b", l.Branch)
	if out, err := branchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout -b %s: %w\n%s", l.Branch, err, string(out))
	}

	subCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "submodule", "update", "--init", "--recursive")
	if out, err := subCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git submodule update --init --recursive in %s: %w\n%s", l.RepoDir, err, string(out))
	}

	// Set a benchmark-local git identity so per-iteration `git commit` works
	// without depending on a global ~/.gitconfig (which the pod typically
	// doesn't have). These configs live only in the per-run clone.
	for _, kv := range [][2]string{
		{"user.email", "benchmark@overthinkos.local"},
		{"user.name", "ov benchmark"},
	} {
		c := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "config", kv[0], kv[1])
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s in %s: %w\n%s", kv[0], l.RepoDir, err, string(out))
		}
	}

	// Mirror untracked working-tree artifacts that the build needs but
	// that aren't in git history. `git clone --no-local` only copies
	// tracked files, so:
	//   - `bin/ov` (built by `task build:ov`, gitignored)
	//   - `layers/ov/bin/ov` (the same binary, copied for the in-image
	//     `ov` build stage via `COPY layers/ov/ /`)
	// are both missing from the clone, and the bench / fedora-coder
	// builds fail on the COPY of /bin/ov.
	for _, sub := range []string{"bin", "layers/ov/bin"} {
		src := filepath.Join(l.ProjectDir, sub)
		st, err := os.Stat(src)
		if err != nil || !st.IsDir() {
			continue
		}
		dst := filepath.Join(l.RepoDir, sub)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dst, err)
		}
		cp := exec.CommandContext(ctx, "cp", "-a", src+"/.", dst+"/")
		if out, err := cp.CombinedOutput(); err != nil {
			return fmt.Errorf("cp -a %s -> %s: %w\n%s", src, dst, err, string(out))
		}
	}

	return nil
}

// PushBranchToHost pushes the per-run branch back to the bind-mounted
// host repo at /workspace. Best-effort: any push failure is the caller's
// to log non-fatally — the on-disk artifacts under .benchmark/<run-id>/
// are already preserved via the bind-mount.
//
// Uses file:// transport to keep the operation rootless and credential-free.
// `--force-with-lease` is intentionally omitted: the branch is unique to
// this run and should never collide with anything in the host repo.
func PushBranchToHost(ctx context.Context, l RunLayout) error {
	target := "file://" + l.ProjectDir
	cmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "push", target, l.Branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s %s: %w\n%s", target, l.Branch, err, string(out))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-iteration commits
// ---------------------------------------------------------------------------

// CommitIterationInRepo stages all changes in the per-run clone and
// creates a commit on the ovbench branch. Hooks RUN — there is no
// --no-verify. --allow-empty is on so no-op iterations leave a marker.
func CommitIterationInRepo(ctx context.Context, l RunLayout, k int, score int, solvedIDs []string) (string, error) {
	addCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A (iter%d): %w\n%s", k, err, string(out))
	}
	msg := formatCommitMessage(k, score, solvedIDs)
	commitCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "commit", "--allow-empty", "-m", msg)
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git commit (iter%d) rejected (likely by pre-commit hook): %w\n%s",
			k, err, string(out))
	}
	sha, err := resolveHeadSHA(ctx, l.RepoDir)
	if err != nil {
		return "", fmt.Errorf("commit succeeded but could not read HEAD: %w", err)
	}
	return sha, nil
}

func formatCommitMessage(k int, score int, solvedIDs []string) string {
	idsTrunc := truncateIDs(solvedIDs, 6)
	return fmt.Sprintf("iter%d: score=%d, solved=[%s]", k, score, strings.Join(idsTrunc, ","))
}

func truncateIDs(ids []string, max int) []string {
	if len(ids) <= max {
		return ids
	}
	out := append([]string(nil), ids[:max]...)
	return append(out, fmt.Sprintf("...+%d", len(ids)-max))
}

func resolveHeadSHA(ctx context.Context, repoDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ---------------------------------------------------------------------------
// Run enumeration (host-side, reads .benchmark/ that was mirrored back)
// ---------------------------------------------------------------------------

// RunSummary describes one past benchmark run found under .benchmark/.
type RunSummary struct {
	RunID        string
	RunDir       string
	Status       string    // "complete" (report.yml present) | "incomplete"
	StartedUTC   time.Time // parsed from RunID when possible
	HasRepo      bool      // true iff repo/ directory exists
	BranchExists bool      // true iff ovbench/<run-id> still exists in projectDir
}

// ListRuns walks <projectDir>/.benchmark and returns one RunSummary
// per directory with a parsable run-id shape. Sorted newest first.
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
		if _, err := os.Stat(filepath.Join(s.RunDir, "report.yml")); err == nil {
			s.Status = "complete"
		} else {
			s.Status = "incomplete"
		}
		if st, err := os.Stat(filepath.Join(s.RunDir, "repo")); err == nil && st.IsDir() {
			s.HasRepo = true
		}
		s.BranchExists = branchExists(ctx, projectDir, "ovbench/"+runID)
		s.StartedUTC = parseRunIDTimestamp(runID)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedUTC.After(out[j].StartedUTC)
	})
	return out, nil
}

func branchExists(ctx context.Context, projectDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", projectDir,
		"show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return cmd.Run() == nil
}

func parseRunIDTimestamp(runID string) time.Time {
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
