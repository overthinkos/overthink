package main

// migrate_init_candy_keys.go — `charly migrate` step finishing the candy/box
// rebrand's INIT-SYSTEM vocabulary. The 2026-06 candy-box-rename renamed the kind
// DISCRIMINATORS (`layer:`→`candy:`) but left three `layer`-spelled keys inside the
// `init:` system definitions (build.yml init vocabulary):
//   - `layer_field:`   → `candy_field:`   (which candy field(s) hold services)
//   - `layer_file:`    → `candy_file:`    (which candy files to match, e.g. *.service)
//   - `depends_layer:` → `depends_candy:` (which candy must precede this init system)
// The Go struct (init_config.go) now reads `candy_field`/`candy_file`/`depends_candy`,
// so a config carrying the old keys silently loses them; this step rewrites them.
//
// Scoped to the `init:` subtree (these keys are init-vocabulary-exclusive in the
// schema), so no unrelated mapping key is touched. Comment-preserving (yaml.v3 node
// API); idempotent (a config already on candy_* is a no-op); per-file .bak.<unix-ts>.
// TouchesHost false → remote-cache auto-migration applies it to fetched repos that
// override `init:`. See CHANGELOG/.

import (
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var initCandyKeyRenames = map[string]string{
	"layer_field":   "candy_field",
	"layer_file":    "candy_file",
	"depends_layer": "depends_candy",
}

// MigrateInitCandyKeys rewrites the init-system `layer_*`/`depends_layer` keys to
// their `candy_*`/`depends_candy` form in a project tree (init: lives in charly.yml
// or an imported build-vocabulary file). Returns the list of changed files.
func MigrateInitCandyKeys(dir string, dryRun bool) ([]string, error) {
	var changed []string
	for _, f := range []string{UnifiedFileName, "build.yml", "base.yml"} {
		mod, err := rewriteDocFile(filepath.Join(dir, f), dryRun, rewriteInitSubtrees)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// rewriteInitSubtrees walks the node tree; for every top-level `init:` mapping it
// renames the candy-meaning keys anywhere within that init system's subtree. The
// walk is scoped to `init:` (init-vocabulary-exclusive in the schema), so no other
// mapping key is touched.
func rewriteInitSubtrees(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if rewriteInitSubtrees(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if key.Value == "init" {
				if renameInitKeysRecursive(val) {
					changed = true
				}
			}
			if rewriteInitSubtrees(val) {
				changed = true
			}
		}
	}
	return changed
}

// renameInitKeysRecursive renames the candy-meaning keys at every depth within an
// init: subtree (system def -> nested model config), in place.
func renameInitKeysRecursive(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if renameInitKeysRecursive(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if nn, ok := initCandyKeyRenames[key.Value]; ok {
				key.Value = nn
				changed = true
			}
			if renameInitKeysRecursive(val) {
				changed = true
			}
		}
	}
	return changed
}
