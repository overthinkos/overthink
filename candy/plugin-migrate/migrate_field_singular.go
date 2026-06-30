package migrate

// migrate_field_singular.go — `charly migrate`.
//
// Hard cutover that singularizes every plural YAML field name in the
// project schema. Rewrites every reachable .yml under the project root
// (overthink.yml siblings + includes:/include: + discover: + ov/testdata
// when self-running). Idempotent: a second run reports zero changes.
//
// Modeled after rewriteServiceKeys in migrate_unified.go:397 — line-level
// rewriter preserving comments and indentation. Longest-match-first key
// ordering ensures `requires_capabilities:` is rewritten before the
// shorter `requires:` rule fires on the same file.
//
// The `pluralToSingularYAMLKeys` map is the single source of truth for
// (a) the migrator's substitution table, AND (b) the loader-rejection
// helper `RejectLegacyPluralKeys` used by parseCandyYAML / LoadUnified
// / LoadBundleConfig (R3 no-duplication).

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The canonical plural→singular map (pluralToSingularYAMLKeys) moved to
// charly/plugin/kit (the ONE copy shared with charly core's load-time
// RejectLegacyPluralKeys gate — R3, C13a); it is aliased back in aliases.go so this
// migrator's references are unchanged. RejectLegacyPluralKeys itself moved to core
// (the loader needs it long before any plugin connects).

// orderedPluralKeys returns the keys of pluralToSingularYAMLKeys sorted
// by length descending. Longest-match-first is required so that
// `requires_capabilities:` matches its full key before the shorter
// `requires:` rule would otherwise fire (and corrupt the longer key
// into `require_capabilities:`).
func orderedPluralKeys() []string {
	keys := make([]string, 0, len(pluralToSingularYAMLKeys))
	for k := range pluralToSingularYAMLKeys {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// MigrateFieldSingularOpts configures MigrateFieldSingular.
type MigrateFieldSingularOpts struct {
	Dir    string
	DryRun bool
	// BackupSuffix overrides the default `.bak.<unix-ts>` suffix; used by
	// tests to make output deterministic. Empty means generate from
	// time.Now().
	BackupSuffix string
}

// FileRewrite is one file's outcome.
type FileRewrite struct {
	Path string   // absolute path to the rewritten file
	Keys []string // singularized keys observed in the file (one entry per distinct key, sorted)
}

// MigrateFieldSingularResult reports the outcome of a migrator invocation.
type MigrateFieldSingularResult struct {
	Rewritten    []FileRewrite
	BackupSuffix string
}

// MigrateFieldSingular walks the project rooted at opts.Dir and applies
// the plural→singular rename to every reachable .yml. Idempotent: a
// second invocation on the same tree returns Rewritten==nil.
func MigrateFieldSingular(opts MigrateFieldSingularOpts) (MigrateFieldSingularResult, error) {
	var res MigrateFieldSingularResult

	files, err := discoverProjectYAMLs(opts.Dir)
	if err != nil {
		return res, err
	}

	suffix := opts.BackupSuffix
	if suffix == "" {
		suffix = fmt.Sprintf(".bak.%d", time.Now().Unix())
	}
	res.BackupSuffix = suffix

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			// Skip unreadable files silently (e.g. dangling symlinks);
			// don't abort the whole migration on one bad sibling.
			continue
		}
		newData, touched := rewriteFieldSingular(data)
		if len(touched) == 0 {
			continue
		}
		// Idempotency invariant — after rewrite, no plural keys should
		// remain. If any do, surface the bug rather than silently shipping
		// a half-migrated file.
		_, residual := rewriteFieldSingular(newData)
		if len(residual) != 0 {
			return res, fmt.Errorf("%s: post-rewrite residual plural keys %v — this is a migrator bug", path, residual)
		}
		if !opts.DryRun {
			backupPath := path + suffix
			if _, err := os.Stat(backupPath); err == nil {
				return res, fmt.Errorf("%s: backup file already exists; refusing to overwrite (delete it or wait one second and re-run)", backupPath)
			}
			if err := os.WriteFile(backupPath, data, 0644); err != nil {
				return res, fmt.Errorf("writing backup %s: %w", backupPath, err)
			}
			if err := os.WriteFile(path, newData, 0644); err != nil {
				return res, fmt.Errorf("writing %s: %w", path, err)
			}
		}
		sort.Strings(touched)
		res.Rewritten = append(res.Rewritten, FileRewrite{Path: path, Keys: touched})
	}

	return res, nil
}

// rewriteFieldSingular performs the line-level rewrite on raw YAML
// bytes. Returns the rewritten bytes and the sorted unique list of
// plural keys that were touched.
//
// Algorithm (mirrors migrate_unified.go:rewriteServiceKeys):
//
//   - Split into lines.
//   - For each line, strip leading whitespace to find the indent.
//   - For each plural key in longest-first order, check whether the
//     trimmed line is `<plural>:` followed by `:`-end-of-line OR by a
//     space (block-mapping shapes) OR by inline-mapping content (`{...}`,
//     value, comment). If matched, rewrite the key in-place and record.
//   - Reassemble; return.
//
// The algorithm is purely lexical — quoted string values containing the
// word `layers:` mid-content are NOT rewritten because the trim+prefix
// check requires the key to start at column-after-indent.
func rewriteFieldSingular(data []byte) ([]byte, []string) {
	lines := strings.Split(string(data), "\n")
	keys := orderedPluralKeys()
	touched := map[string]struct{}{}

	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := ln[:len(ln)-len(trimmed)]
		for _, plural := range keys {
			prefix := plural + ":"
			if !strings.HasPrefix(trimmed, prefix) {
				continue
			}
			// Boundary check: the character after the colon must be the
			// end of the line, a space, or a tab. This prevents
			// `layers_unmodified:` (hypothetical) from matching the
			// `layers:` rule.
			rest := trimmed[len(prefix):]
			if rest != "" && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '\r' {
				continue
			}
			singular := pluralToSingularYAMLKeys[plural]
			lines[i] = indent + singular + ":" + rest
			touched[plural] = struct{}{}
			break // longest-first matched; don't fire shorter rules on the same line
		}
	}

	out := make([]string, 0, len(touched))
	for k := range touched {
		out = append(out, k)
	}
	sort.Strings(out)
	return []byte(strings.Join(lines, "\n")), out
}

// discoverProjectYAMLs returns the absolute paths of every .yml file
// reachable from a project rooted at dir. Reads overthink.yml raw (NOT
// via LoadUnified) so the walker can run on a tree that still has
// legacy plural keys.
//
// Sources:
//   - overthink.yml itself
//   - top-level includes: / include: entries
//   - discover: scan paths (recursive *.yml under each)
//   - project-root .yml siblings (image.yml, deploy.yml, pod.yml,
//     k8s.yml, vm.yml, eval.yml, local.yml) — fallback regardless of
//     whether they appear in includes:
//   - ov/testdata/**/*.yml when the cwd looks like the overthink repo
//     itself (self-migration mode)
//
// Returns sorted, deduplicated absolute paths.
func discoverProjectYAMLs(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}

	discoverRootYAMLs(abs, seen)
	if err := discoverLayersYAMLs(abs, seen); err != nil {
		return nil, err
	}

	if err := discoverImportedYAMLs(abs, seen); err != nil {
		return nil, err
	}

	if err := discoverTestdataYAMLs(abs, seen); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// addSeenPath records a (possibly relative) yml path into the seen set,
// resolving it against abs and cleaning it.
func addSeenPath(abs, p string, seen map[string]struct{}) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(abs, p)
	}
	seen[filepath.Clean(p)] = struct{}{}
}

// discoverRootYAMLs adds the project-root .yml siblings (always included when
// present) to the seen set.
func discoverRootYAMLs(abs string, seen map[string]struct{}) {
	// Project-root .yml siblings — always include if present.
	for _, name := range []string{
		"overthink.yml", "image.yml", "deploy.yml", "pod.yml",
		"k8s.yml", "vm.yml", "eval.yml", "local.yml",
	} {
		p := filepath.Join(abs, name)
		if _, err := os.Stat(p); err == nil {
			addSeenPath(abs, p, seen)
		}
	}
}

// discoverLayersYAMLs walks the standard layers/<name>/layer.yml layout into
// the seen set. Every OpenCharly project has this by convention, so it is
// scanned without requiring an explicit discover: block.
func discoverLayersYAMLs(abs string, seen map[string]struct{}) error {
	if td := filepath.Join(abs, "layers"); statIsDir(td) {
		if err := walkYAMLs(td, seen); err != nil {
			return err
		}
	}
	return nil
}

// discoverImportedYAMLs reads overthink.yml raw (NOT via LoadUnified, so it runs
// on trees still carrying legacy plural keys) and adds every
// import:/includes:/include: entry plus every discover: scan path to the seen
// set.
func discoverImportedYAMLs(abs string, seen map[string]struct{}) error {
	rootPath := filepath.Join(abs, "overthink.yml")
	data, err := os.ReadFile(rootPath)
	if err != nil {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	rootMap := doc.Content[0]
	// import: (canonical) / legacy includes: / include:
	for _, key := range []string{"import", "includes", "include"} {
		if seq := lookupMapNode(rootMap, key); seq != nil && seq.Kind == yaml.SequenceNode {
			for _, item := range seq.Content {
				if item.Kind == yaml.ScalarNode && item.Value != "" {
					addSeenPath(abs, item.Value, seen)
				}
			}
		}
	}
	// discover: recursive scan
	if discNode := lookupMapNode(rootMap, "discover"); discNode != nil && discNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(discNode.Content); i += 2 {
			valNode := discNode.Content[i+1]
			switch valNode.Kind {
			case yaml.SequenceNode:
				for _, item := range valNode.Content {
					var p string
					switch item.Kind {
					case yaml.ScalarNode:
						p = item.Value
					case yaml.MappingNode:
						if pn := lookupMapNode(item, "path"); pn != nil {
							p = pn.Value
						}
					}
					if p == "" {
						continue
					}
					if err := walkYAMLs(filepath.Join(abs, p), seen); err != nil {
						return err
					}
				}
			case yaml.ScalarNode:
				if valNode.Value != "" {
					if err := walkYAMLs(filepath.Join(abs, valNode.Value), seen); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// discoverTestdataYAMLs adds ov/testdata/**/*.yml to the seen set when running
// from the overthink repo root (detected via the ov/go.mod sentinel). These
// fixtures are loaded by Go tests and must stay in lockstep with the schema.
func discoverTestdataYAMLs(abs string, seen map[string]struct{}) error {
	if _, err := os.Stat(filepath.Join(abs, "ov", "go.mod")); err != nil {
		return nil
	}
	td := filepath.Join(abs, "ov", "testdata")
	if statIsDir(td) {
		if err := walkYAMLs(td, seen); err != nil {
			return err
		}
	}
	return nil
}

func statIsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// walkYAMLs recursively adds every *.yml under root to the seen set.
// Non-existent roots are silently skipped.
func walkYAMLs(root string, seen map[string]struct{}) error {
	info, err := os.Stat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		if strings.HasSuffix(root, ".yml") || strings.HasSuffix(root, ".yaml") {
			seen[filepath.Clean(root)] = struct{}{}
		}
		return nil
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		seen[filepath.Clean(path)] = struct{}{}
		return nil
	})
}

// lookupMapNode returns the value node for the given key in a YAML
// mapping node, or nil if absent. Identical to the helper used in
// migrate_kind_files.go but local-scoped to avoid coupling.
func lookupMapNode(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// RejectLegacyPluralKeys moved to charly core (charly/migrate_legacy_plural.go) —
// the loader calls it on every parse, before any plugin connects (C13a). It reads
// the same kit.PluralToSingularYAMLKeys this migrator does.
