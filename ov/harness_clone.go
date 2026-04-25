package main

// harness_clone.go — per-run clone lifecycle + iteration commit + push-back.
//
// The harness's per-target driver clones the project's bind-mounted
// /workspace (or $PWD on host targets) into a per-run scratch dir on a
// fresh ovharness/<run-id> branch. Per-iteration commits land in that
// clone. At end of run the branch pushes back to the project repo for
// an audit trail.
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

// RunLayout is the canonical set of paths for one harness run, rooted
// at ProjectDir/.harness/<recipe>/runs/<run-id>.
type RunLayout struct {
	ProjectDir string // project root containing .harness/
	Recipe     string // recipe name (drives the namespace under .harness/)
	RunID      string // "<UTC-timestamp>-<shorthash>"
	RunDir     string // <ProjectDir>/.harness/<recipe>/runs/<run-id>
	RepoDir    string // <ProjectDir>/.harness/<recipe>/runs/<run-id>/repo
	Branch     string // "ovharness/<run-id>"
}

// NewRunLayout constructs a RunLayout. Generates run-id if empty.
func NewRunLayout(projectDir, recipe, runID string) RunLayout {
	if runID == "" {
		runID = GenerateRunID()
	}
	if recipe == "" {
		recipe = "default"
	}
	runDir := filepath.Join(projectDir, ".harness", recipe, "runs", runID)
	return RunLayout{
		ProjectDir: projectDir,
		Recipe:     recipe,
		RunID:      runID,
		RunDir:     runDir,
		RepoDir:    filepath.Join(runDir, "repo"),
		Branch:     "ovharness/" + runID,
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

// ResultsDir returns the per-recipe results directory (sibling of runs/).
func (l RunLayout) ResultsDir() string {
	return filepath.Join(l.ProjectDir, ".harness", l.Recipe, "results")
}

// CreateRunClone creates a per-run scratch clone at l.RepoDir on a
// fresh branch ovharness/<run-id>. Source: l.ProjectDir.
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

	for _, kv := range [][2]string{
		{"user.email", "harness@overthinkos.local"},
		{"user.name", "ov harness"},
	} {
		c := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "config", kv[0], kv[1])
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git config %s in %s: %w\n%s", kv[0], l.RepoDir, err, string(out))
		}
	}

	// Mirror untracked working-tree artifacts the build needs.
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

// PushBranchToHost pushes the per-run branch back to ProjectDir's git.
func PushBranchToHost(ctx context.Context, l RunLayout) error {
	target := "file://" + l.ProjectDir
	cmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "push", target, l.Branch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push %s %s: %w\n%s", target, l.Branch, err, string(out))
	}
	return nil
}

// CommitIterationInRepo stages all changes + commits with the standard
// message. Hooks run; --allow-empty is on so no-op iterations leave a marker.
func CommitIterationInRepo(ctx context.Context, l RunLayout, k int, score int, solvedIDs []string) (string, error) {
	addCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A (iter%d): %w\n%s", k, err, string(out))
	}
	msg := formatCommitMessage(k, score, solvedIDs)
	commitCmd := exec.CommandContext(ctx, "git", "-C", l.RepoDir, "commit", "--allow-empty", "-m", msg)
	out, err := commitCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git commit (iter%d) rejected: %w\n%s", k, err, string(out))
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

// RunSummary describes one past harness run found under .harness/<recipe>/runs.
type RunSummary struct {
	Recipe       string
	RunID        string
	RunDir       string
	Status       string    // "complete" (result.{calver}.yml present) | "incomplete"
	StartedUTC   time.Time // parsed from RunID
	HasRepo      bool
	BranchExists bool
}

// ListRuns walks <projectDir>/.harness/*/runs/ across all recipes and
// returns one RunSummary per run dir. Sorted newest first.
func ListRuns(ctx context.Context, projectDir string) ([]RunSummary, error) {
	base := filepath.Join(projectDir, ".harness")
	recipes, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", base, err)
	}
	var out []RunSummary
	for _, rEntry := range recipes {
		if !rEntry.IsDir() {
			continue
		}
		runsDir := filepath.Join(base, rEntry.Name(), "runs")
		entries, err := os.ReadDir(runsDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			runID := e.Name()
			s := RunSummary{
				Recipe: rEntry.Name(),
				RunID:  runID,
				RunDir: filepath.Join(runsDir, runID),
			}
			s.Status = "incomplete"
			// Look in the per-recipe results dir for any result file.
			resultsDir := filepath.Join(base, rEntry.Name(), "results")
			if results, err := os.ReadDir(resultsDir); err == nil {
				for _, r := range results {
					if strings.HasPrefix(r.Name(), "result.") && strings.HasSuffix(r.Name(), ".yml") {
						s.Status = "complete"
						break
					}
				}
			}
			if st, err := os.Stat(filepath.Join(s.RunDir, "repo")); err == nil && st.IsDir() {
				s.HasRepo = true
			}
			s.BranchExists = branchExists(ctx, projectDir, "ovharness/"+runID)
			s.StartedUTC = parseRunIDTimestamp(runID)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedUTC.After(out[j].StartedUTC) })
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
