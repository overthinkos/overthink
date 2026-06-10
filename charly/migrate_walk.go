package main

// migrate_walk.go — shared directory-walk helpers for the project migrators.
//
// Several `charly migrate` steps walk the project tree looking for YAML to
// rewrite. They all need to skip the same build-artifact / cache dirs AND must
// NOT descend into a nested git submodule (which owns its OWN `charly migrate`,
// directly or via remote-cache auto-migration when fetched). This file is the
// single home for that skip logic so it stays consistent and submodule-aware
// regardless of where a submodule is mounted (box/<distro>, plugins, pkg/*).

import (
	"os"
	"path/filepath"
)

// isNestedGitRepo reports whether dir is the root of a SEPARATE git repo — a
// submodule checkout or a nested clone. Such a dir carries a `.git` entry: a
// FILE (a `gitdir:` pointer) for a submodule or linked worktree, a DIR for a
// plain clone.
func isNestedGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// migrateSkipDir reports whether a project-walking migrator should skip dir: a
// build-artifact / cache dir, or a NESTED git submodule (which migrates in its
// OWN repo). root is the walk root, kept in scope so the project's own top-level
// files migrate even though the root carries a `.git`. This is the ONE shared
// skip set — a submodule is skipped by structure (`isNestedGitRepo`), not by a
// hardcoded dir name, so the skip survives any mount relocation. Walkers with
// extra needs layer them on top: the entity-version backfill ALSO skips
// output/testdata (never rewriting a hand-authored fixture's version:), and the
// description scaffolder ALSO skips bin/vendor/.claude.
func migrateSkipDir(path, root string) bool {
	switch filepath.Base(path) {
	case ".git", "node_modules", ".build", ".cache", ".eval":
		return true
	}
	return path != root && isNestedGitRepo(path)
}
