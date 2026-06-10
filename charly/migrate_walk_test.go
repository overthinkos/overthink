package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateSkipDir covers the shared migration walk-skip predicate that all
// project migrators route through: it skips build-artifact / cache dirs by name
// and ANY nested git submodule by structure (a nested `.git`), while keeping the
// walk root and a project's own box/<name> dirs in scope.
func TestMigrateSkipDir(t *testing.T) {
	root := t.TempDir()
	// The walk root carries a `.git` (mimics a linked-worktree root).
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A nested submodule (a `.git` gitfile makes it a separate repo).
	sub := filepath.Join(root, "box", "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A normal own box dir (no `.git`).
	own := filepath.Join(root, "box", "mybox")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want bool
		why  string
	}{
		{root, false, "walk root kept in scope despite its own .git"},
		{filepath.Join(root, ".build"), true, "build artifact dir skipped by name"},
		{filepath.Join(root, "node_modules"), true, "dependency dir skipped by name"},
		{filepath.Join(root, ".cache"), true, "cache dir skipped by name"},
		{filepath.Join(root, ".eval"), true, "eval artifacts skipped by name"},
		{filepath.Join(root, "box"), false, "the box/ parent itself is walked"},
		{own, false, "a project's own box/<name> dir is walked"},
		{sub, true, "a nested git submodule is skipped by structure, not by name"},
	}
	for _, c := range cases {
		if got := migrateSkipDir(c.path, root); got != c.want {
			t.Errorf("migrateSkipDir(%q) = %v, want %v (%s)", c.path, got, c.want, c.why)
		}
	}
}
