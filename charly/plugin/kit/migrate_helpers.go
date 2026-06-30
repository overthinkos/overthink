package kit

// migrate_helpers.go — small generic helpers shared by charly core AND the
// compiled-in candy/plugin-migrate (a separate module that cannot import package
// main). Core aliases each via `var x = kit.X` (so core call sites are unchanged);
// the candy aliases them in aliases.go. ONE copy each (R3).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/overthinkos/overthink/charly/spec"
	"gopkg.in/yaml.v3"
)

// FileExists reports whether path exists and is a regular (non-dir) file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// DirExists reports whether path exists and is a directory.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// SortStrings sorts s in place (ascending). A small insertion-free bubble sort,
// kept identical to the original package-main helper.
func SortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// FirstNonEmpty returns the first non-empty string in vals, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// MapHasKey reports whether the YAML mapping node has the given top-level key.
func MapHasKey(node *yaml.Node, key string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			return true
		}
	}
	return false
}

// ScalarNode builds a string scalar YAML node.
func ScalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// FindMappingValue returns the value node for key in a YAML mapping node, or nil.
// (Like MapValue, but requires the key node to be a scalar — the form the
// migration transforms + the core loader's legacy-shape detection both use.)
func FindMappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// MigrateCandidateYAMLFiles is the ONE candidate-file scanner the multi-document
// doc-migration steps share AND the core loader's legacy-vocab rejection scan uses:
// every `.yml`/`.yaml` under each of treeSubdirs (walked recursively, skipping
// nested git submodules + any `testdata` dir) plus the root-level YAML siblings in
// dir. Sorted, deduplicated.
func MigrateCandidateYAMLFiles(dir string, treeSubdirs []string) []string {
	seen := map[string]struct{}{}
	addYAMLTree := func(root string) {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if filepath.Base(p) == "testdata" || IsGitSubmoduleDir(p, root) {
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
	SortStrings(out)
	return out
}

// OpUnifyCandidateFiles is the candidate-file set the op/plan-unify migrators AND
// the core loader's legacy-test-vocab rejection scan walk (candy/ + box/ trees +
// root siblings).
func OpUnifyCandidateFiles(dir string) []string {
	return MigrateCandidateYAMLFiles(dir, []string{"candy", "box"})
}

// MapValue returns the value node for key in a YAML mapping node, or nil.
func MapValue(m *yaml.Node, key string) *yaml.Node {
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

// kindWordSet is the CUE-derived reserved kind-word set (spec.KindWords), used by
// NodeShapedValue to detect a name-first node-form value.
var kindWordSet = func() map[string]bool {
	m := make(map[string]bool, len(spec.KindWords))
	for _, k := range spec.KindWords {
		m[k] = true
	}
	return m
}()

// NodeShapedValue reports whether a mapping node carries a reserved kind word as a
// key (i.e. it is a name-first node-form value).
func NodeShapedValue(val *yaml.Node) bool {
	if val == nil || val.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(val.Content); i += 2 {
		if kindWordSet[val.Content[i].Value] {
			return true
		}
	}
	return false
}

// FirstYAMLVersionLine extracts the value of the first top-level `version:` line.
func FirstYAMLVersionLine(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		if after, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// IsGitSubmoduleDir reports whether p (≠ root) contains a .git entry (a nested
// submodule/repo boundary).
func IsGitSubmoduleDir(p, root string) bool {
	if p == root {
		return false
	}
	_, err := os.Stat(filepath.Join(p, ".git"))
	return err == nil
}

// HasLegacyImagesKey reports whether the raw YAML body has a top-level `images:`
// key but no `deploy:` key (the legacy local-images shape).
func HasLegacyImagesKey(data []byte) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return false
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return false
	}
	hasImages := false
	hasDeploy := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "images":
			hasImages = true
		case "deploy":
			hasDeploy = true
		}
	}
	return hasImages && !hasDeploy
}

// IsNestedGitRepo reports whether dir is the root of a SEPARATE git repo — a
// submodule checkout or nested clone (it carries a `.git` entry).
func IsNestedGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// MigrateSkipDir reports whether a project-walking migrator (or the core
// legacy-images validator) should skip dir: a build-artifact / cache dir, or a
// NESTED git submodule (which migrates in its OWN repo). root is the walk root,
// kept in scope so the project's own top-level files migrate even though the root
// carries a `.git`.
func MigrateSkipDir(path, root string) bool {
	switch filepath.Base(path) {
	case ".git", "node_modules", ".build", ".cache", ".eval":
		return true
	}
	return path != root && IsNestedGitRepo(path)
}

// NOTE: EnvdDir + StripLegacyOverthinkBlocks already live in kit (profile.go) —
// core aliases those copies (kit_aliases.go), the candy aliases them in aliases.go.
