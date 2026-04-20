package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNormalizeRepoSpec covers all four spec shapes plus the "default"
// sentinel. Pure unit test, no I/O.
func TestNormalizeRepoSpec(t *testing.T) {
	cases := []struct {
		name        string
		spec        string
		wantRepo    string
		wantVersion string
	}{
		{name: "default sentinel", spec: "default",
			wantRepo: "github.com/overthinkos/overthink", wantVersion: ""},
		{name: "bare owner/repo", spec: "overthinkos/overthink",
			wantRepo: "github.com/overthinkos/overthink", wantVersion: ""},
		{name: "bare owner/repo @ ref", spec: "overthinkos/overthink@main",
			wantRepo: "github.com/overthinkos/overthink", wantVersion: "main"},
		{name: "host-qualified, no ref", spec: "github.com/foo/bar",
			wantRepo: "github.com/foo/bar", wantVersion: ""},
		{name: "host-qualified gitlab @ ref", spec: "gitlab.com/foo/bar@v1.0",
			wantRepo: "gitlab.com/foo/bar", wantVersion: "v1.0"},
		// Whitespace tolerance.
		{name: "leading/trailing whitespace", spec: "  overthinkos/overthink@main  ",
			wantRepo: "github.com/overthinkos/overthink", wantVersion: "main"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRepo, gotVersion := normalizeRepoSpec(tc.spec)
			if gotRepo != tc.wantRepo || gotVersion != tc.wantVersion {
				t.Errorf("normalizeRepoSpec(%q) = (%q, %q); want (%q, %q)",
					tc.spec, gotRepo, gotVersion, tc.wantRepo, tc.wantVersion)
			}
		})
	}
}

// TestOvRepo_FlagChdir verifies that --repo / OV_PROJECT_REPO drives main()
// to chdir into the cache path before dispatching. Stays hermetic by
// pre-populating OV_REPO_CACHE so EnsureRepoDownloaded short-circuits via
// IsRepoCached and never shells out to git.
func TestOvRepo_FlagChdir(t *testing.T) {
	bin := buildOvBinary(t)

	cacheRoot := t.TempDir()
	// Pre-seed cache at <root>/github.com/foo/bar@main/ with a valid project.
	cachedRepo := filepath.Join(cacheRoot, "github.com", "foo", "bar@main")
	if err := os.MkdirAll(cachedRepo, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	writeMinProject(t, cachedRepo)

	// Spawn from /tmp so a missed chdir would fail loudly.
	startCwd := os.TempDir()

	cases := []struct {
		name string
		args []string
		env  []string
	}{
		{
			name: "long flag --repo with @ref",
			args: []string{"--repo", "foo/bar@main", "image", "list", "images"},
			env:  []string{"OV_REPO_CACHE=" + cacheRoot},
		},
		{
			name: "env var OV_PROJECT_REPO",
			args: []string{"image", "list", "images"},
			env:  []string{"OV_REPO_CACHE=" + cacheRoot, "OV_PROJECT_REPO=foo/bar@main"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Dir = startCwd
			cmd.Env = append(append([]string{}, os.Environ()...), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ov failed: %v\noutput: %s", err, out)
			}
			if !strings.Contains(string(out), "testimage") {
				t.Errorf("did not see scratch project's image; output:\n%s", out)
			}
		})
	}
}

// TestOvRepo_DirConflict verifies --repo and --dir together fast-fail.
func TestOvRepo_DirConflict(t *testing.T) {
	bin := buildOvBinary(t)
	scratch := t.TempDir()

	cmd := exec.Command(bin, "--repo", "foo/bar@main", "--dir", scratch, "version")
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success; output:\n%s", out)
	}
	if !strings.Contains(string(out), "mutually exclusive") {
		t.Errorf("expected mutually-exclusive error, got: %s", out)
	}
}

// TestOvRepo_DefaultExpansion verifies that --repo default normalizes to
// the canonical github.com/overthinkos/overthink path. Pure unit-level
// check, exercised through normalizeRepoSpec to avoid live network.
func TestOvRepo_DefaultExpansion(t *testing.T) {
	repo, version := normalizeRepoSpec("default")
	if repo != DefaultProjectRepo {
		t.Errorf("default normalized to %q; want %q", repo, DefaultProjectRepo)
	}
	if version != "" {
		t.Errorf("default version should be empty (resolved later); got %q", version)
	}
}
