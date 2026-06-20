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
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

// migrateCandidateYAMLFiles is the ONE candidate-file scanner the multi-document
// doc-migration steps share (R3): every `.yml`/`.yaml` under each of treeSubdirs
// (walked recursively) plus the root-level YAML siblings in dir. It is the
// single home for the skip set so every doc-migration step honors it identically:
//   - a NESTED git submodule (box/<distro>, plugins, pkg/*) is skipped — it owns
//     its OWN `charly migrate` (directly or via remote-cache auto-migration), via
//     isGitSubmoduleDir, and
//   - any `testdata` directory is skipped — a migrator must NEVER rewrite a
//     hand-frozen legacy migration fixture: doing so makes `charly migrate
//     --dry-run` from the repo root perpetually report fixture work (the scan is
//     no longer idempotent) AND corrupts the very inputs the migration tests
//     replay. The skip is keyed on the path COMPONENT (`testdata`), so it holds
//     wherever a fixture dir is mounted, not just charly/testdata.
//
// Sorted, deduplicated.
func migrateCandidateYAMLFiles(dir string, treeSubdirs []string) []string {
	seen := map[string]struct{}{}
	addYAMLTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if filepath.Base(p) == "testdata" || isGitSubmoduleDir(p, root) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml") {
				seen[filepath.Clean(p)] = struct{}{}
			}
			return nil
		})
	}
	for _, sub := range treeSubdirs {
		addYAMLTree(filepath.Join(dir, sub))
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
				seen[filepath.Clean(filepath.Join(dir, e.Name()))] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sortStrings(out)
	return out
}
