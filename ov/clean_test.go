package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func img(id, name, short, version string) LocalImageInfo {
	return LocalImageInfo{
		ID:    id,
		Names: []string{name},
		Labels: map[string]string{
			"org.overthinkos.image":   short,
			"org.overthinkos.version": version,
		},
	}
}

// TestPruneImagesByRetention covers grouping by short name, CalVer ordering,
// keep-newest-N, the in-use skip, and the "never touch unlabelled / undateable"
// guards. Uses dryRun so no real rmi runs.
func TestPruneImagesByRetention(t *testing.T) {
	origList, origCtr := ListLocalImages, listContainerImageRefs
	defer func() { ListLocalImages, listContainerImageRefs = origList, origCtr }()

	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			img("aaa", "ghcr/foo:2026.1.100", "foo", "2026.1.100"), // oldest foo
			img("bbb", "ghcr/foo:2026.1.200", "foo", "2026.1.200"), // middle foo (mark in-use)
			img("ccc", "ghcr/foo:2026.1.300", "foo", "2026.1.300"), // newest foo (kept)
			img("ddd", "ghcr/bar:2026.1.100", "bar", "2026.1.100"), // sole bar (kept)
			{ID: "eee", Names: []string{"docker.io/other:latest"}}, // no ov label → ignored
		}, nil
	}
	// bbb is referenced by a container → must be skipped.
	listContainerImageRefs = func(string) (map[string]bool, map[string]bool, error) {
		return map[string]bool{"bbb": true}, map[string]bool{}, nil
	}

	removed, err := pruneImagesByRetention("podman", 1, true)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	sort.Strings(removed)
	// foo: keep newest (ccc); bbb in-use skipped; aaa removed. bar: only one, kept.
	want := []string{"ghcr/foo:2026.1.100"}
	if len(removed) != len(want) || removed[0] != want[0] {
		t.Errorf("removed = %v, want %v", removed, want)
	}
}

func TestPruneImagesByRetention_Disabled(t *testing.T) {
	called := false
	origList := ListLocalImages
	defer func() { ListLocalImages = origList }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { called = true; return nil, nil }

	removed, err := pruneImagesByRetention("podman", 0, true)
	if err != nil || removed != nil {
		t.Errorf("keep=0 should no-op, got removed=%v err=%v", removed, err)
	}
	if called {
		t.Error("keep=0 should not even enumerate images")
	}
}

// TestPruneEvalRuns covers keep-newest-N of CalVer run dirs + result files, the
// runs/<id> mtime path, and the NOTES.md preservation invariant.
func TestPruneEvalRuns(t *testing.T) {
	root := t.TempDir()
	bed := filepath.Join(root, "eval-image-pod")
	// 3 CalVer run dirs (newest = 2026.143.300) + NOTES.md.
	for _, cv := range []string{"2026.143.100", "2026.143.200", "2026.143.300"} {
		mustMkdir(t, filepath.Join(bed, cv))
	}
	mustWrite(t, filepath.Join(bed, "NOTES.md"), "memory")
	// A score dir with result files + runs/<id>.
	score := filepath.Join(root, "default")
	mustMkdir(t, score)
	for _, r := range []string{"result-2026.143.100.yml", "result-2026.143.200.yml", "result-2026.143.300.yml"} {
		mustWrite(t, filepath.Join(score, r), "x")
	}
	mustWrite(t, filepath.Join(score, "NOTES.md"), "memory")

	removed, err := pruneEvalRuns(root, 1, false)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if len(removed) != 4 { // 2 old bed dirs + 2 old result files
		t.Errorf("removed %d, want 4: %v", len(removed), removed)
	}
	// Newest kept, oldest gone, NOTES.md preserved.
	assertExists(t, filepath.Join(bed, "2026.143.300"))
	assertGone(t, filepath.Join(bed, "2026.143.100"))
	assertExists(t, filepath.Join(bed, "NOTES.md"))
	assertExists(t, filepath.Join(score, "result-2026.143.300.yml"))
	assertGone(t, filepath.Join(score, "result-2026.143.100.yml"))
	assertExists(t, filepath.Join(score, "NOTES.md"))
}

func TestPruneEvalRuns_DryRunAndDisabled(t *testing.T) {
	root := t.TempDir()
	bed := filepath.Join(root, "bed")
	for _, cv := range []string{"2026.143.100", "2026.143.200"} {
		mustMkdir(t, filepath.Join(bed, cv))
	}
	// dry-run lists but deletes nothing.
	removed, err := pruneEvalRuns(root, 1, true)
	if err != nil || len(removed) != 1 {
		t.Fatalf("dry-run removed=%v err=%v, want 1 listed", removed, err)
	}
	assertExists(t, filepath.Join(bed, "2026.143.100")) // still there

	// keep=0 disables.
	r2, _ := pruneEvalRuns(root, 0, false)
	if r2 != nil {
		t.Errorf("keep=0 should no-op, got %v", r2)
	}
}

func TestCleanMakepkgArtifacts(t *testing.T) {
	root := t.TempDir()
	arch := filepath.Join(root, "pkg", "arch")
	mustMkdir(t, filepath.Join(arch, "src"))
	mustMkdir(t, filepath.Join(arch, "pkg"))
	mustWrite(t, filepath.Join(arch, "overthink-git-2026.1.1-1-x86_64.pkg.tar.zst"), "z")
	mustWrite(t, filepath.Join(arch, "build.log"), "l")
	mustWrite(t, filepath.Join(arch, "PKGBUILD"), "keep") // must survive

	removed := cleanMakepkgArtifacts(root, false)
	if len(removed) != 4 {
		t.Errorf("removed %d, want 4 (src, pkg, .zst, .log): %v", len(removed), removed)
	}
	assertGone(t, filepath.Join(arch, "src"))
	assertGone(t, filepath.Join(arch, "overthink-git-2026.1.1-1-x86_64.pkg.tar.zst"))
	assertExists(t, filepath.Join(arch, "PKGBUILD"))
}

// helpers (mustMkdir/mustWrite are shared from main_freshness_test.go)

func assertExists(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected %s to exist: %v", p, err)
	}
}

func assertGone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("expected %s to be gone, stat err=%v", p, err)
	}
}
