package main

// migrate_localpkg_map.go — `charly migrate`.
//
// 2026-06 localpkg per-format cutover. The candy `localpkg:` field went from a
// single scalar (Arch-only — `localpkg: pkg/arch`) to a per-format MAP so one
// `ov` candy carries a native-package SOURCE per distro format:
//
//	localpkg:
//	    pac: pkg/arch
//	    rpm: pkg/fedora
//	    deb: pkg/debian
//
// The legacy scalar always pointed at the Arch PKGBUILD, so it migrates to the
// `pac` key. The loader hard-rejects the scalar form (LocalPkgMap.UnmarshalYAML)
// with an `charly migrate` hint, so this step is the remediation.
//
// Comment-preserving, line-level (mirrors rewriteFieldSingular). Idempotent: a
// localpkg already in map form (block or inline) is left untouched.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MigrateLocalpkgMap rewrites the legacy scalar `localpkg: <dir>` candy field to
// the per-format map form `localpkg:\n    pac: <dir>` across every project YAML
// that can carry a candy definition (candy/<name>/charly.yml, root-level YAML
// siblings such as overthink.yml / per-kind files, and ov/testdata when
// self-migrating). Returns the rewritten file paths. Idempotent.
func MigrateLocalpkgMap(dir string, dryRun bool) ([]string, error) {
	var rewritten []string
	for _, path := range localpkgCandidateFiles(dir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable siblings; don't abort the chain
		}
		newData, changed := rewriteLocalpkgMap(data)
		if !changed {
			continue
		}
		// Idempotency invariant — a second pass must be a no-op.
		if _, again := rewriteLocalpkgMap(newData); again {
			return rewritten, fmt.Errorf("%s: localpkg migration not idempotent — migrator bug", path)
		}
		if !dryRun {
			if err := os.WriteFile(path, newData, 0o644); err != nil {
				return rewritten, fmt.Errorf("writing %s: %w", path, err)
			}
		}
		rewritten = append(rewritten, path)
	}
	return rewritten, nil
}

// rewriteLocalpkgMap converts each scalar `localpkg: <value>` line to a
// block-map with one `pac: <value>` child at indent+4, preserving any trailing
// `# comment` on the header line. A `localpkg:` line whose value is empty
// (already a block map) or inline (`{...}`) is left unchanged → idempotent.
func rewriteLocalpkgMap(data []byte) ([]byte, bool) {
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+2)
	changed := false
	for _, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if !strings.HasPrefix(trimmed, "localpkg:") {
			out = append(out, ln)
			continue
		}
		indent := ln[:len(ln)-len(trimmed)]
		rest := trimmed[len("localpkg:"):]
		value, comment := rest, ""
		if ci := strings.Index(rest, " #"); ci >= 0 {
			value, comment = rest[:ci], rest[ci:]
		}
		value = strings.TrimSpace(value)
		// Empty (block map follows) or inline map ({...}) → already migrated.
		if value == "" || strings.HasPrefix(value, "{") {
			out = append(out, ln)
			continue
		}
		out = append(out, indent+"localpkg:"+comment)
		out = append(out, indent+"    pac: "+value)
		changed = true
	}
	return []byte(strings.Join(out, "\n")), changed
}

// localpkgCandidateFiles returns the YAML files in a project that can carry a
// candy's `localpkg:` field: candy/<name>/*.yml (the candy dir), the root-level
// *.yml siblings (inline candies in overthink.yml / per-kind files), and
// ov/testdata/**/*.yml when run from the overthink repo itself. It deliberately
// does NOT recurse into box/<distro> submodules (separate repos, migrated on
// their own). Sorted, deduplicated.
func localpkgCandidateFiles(dir string) []string {
	seen := map[string]struct{}{}
	addYAMLTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml") {
				seen[filepath.Clean(p)] = struct{}{}
			}
			return nil
		})
	}
	// candy/<name>/charly.yml — the rebranded candy dir.
	addYAMLTree(filepath.Join(dir, "candy"))
	// Root-level YAML siblings (overthink.yml + per-kind files with inline candies).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
				seen[filepath.Clean(filepath.Join(dir, e.Name()))] = struct{}{}
			}
		}
	}
	// Self-migration: keep the repo's own ov/testdata fixtures in lockstep.
	if _, err := os.Stat(filepath.Join(dir, "ov", "go.mod")); err == nil {
		addYAMLTree(filepath.Join(dir, "ov", "testdata"))
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sortStrings(out)
	return out
}
