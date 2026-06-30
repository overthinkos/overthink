package migrate

// migrate_recipe_section_values.go — `charly migrate` step finishing the candy/box
// rebrand's DATA VALUES. The 2026.156 candy-box-rename renamed the kind
// DISCRIMINATORS (`layer:`→`candy:`, `image:`→`box:`) but missed two NESTED value
// surfaces inside the eval HARNESS recipes, which still used the pre-rebrand
// "layer"/"image" vocabulary:
//   - a recipe `from[i].kind:` selector — "layer"→"candy", "image"→"box"
//   - a recipe `from[i].scope:` section-filter list — "layer"→"candy", "image"→"box"
//     ("deploy"/"pod"/"vm" unchanged)
// The eval label WIRE keys were already candy/box; only these config VALUES and
// the matching Go section-filter strings lagged. This step rewrites them so a
// config matches the new code (which hard-rejects `kind: layer` in a recipe with
// "invalid kind ... (one of: candy, box, pod, vm)").
//
// Scoped to `from:` SEQUENCE items so a builder `kind: layer` (build.yml) and a
// check-level `scope: build|deploy` are NEVER touched. Comment-preserving
// (yaml.v3 node API); idempotent (a config on candy/box is a no-op); per-file
// .bak.<unix-ts>. TouchesHost false → remote-cache auto-migration applies it to
// fetched repos too. See CHANGELOG/.

import (
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// MigrateRecipeSectionValues rewrites recipe from.kind / scope section VALUES in
// a project tree (recipes live in charly.yml; eval.yml is processed for legacy
// trees). Returns the list of changed files.
func MigrateRecipeSectionValues(dir string, dryRun bool) ([]string, error) {
	var changed []string
	for _, f := range []string{UnifiedFileName, "eval.yml"} {
		mod, err := rewriteDocFile(filepath.Join(dir, f), dryRun, rewriteRecipeFromValues)
		if err != nil {
			return changed, err
		}
		if mod {
			changed = append(changed, f)
		}
	}
	return changed, nil
}

// rewriteRecipeFromValues walks the node tree; for every `from:` SEQUENCE it
// rewrites each item's recipe-from kind + section scope values. The walk is
// scoped to `from:` sequences (recipe-exclusive in the schema), so no other
// `kind:`/`scope:` surface is touched.
func rewriteRecipeFromValues(n *yaml.Node) bool {
	changed := false
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			if rewriteRecipeFromValues(c) {
				changed = true
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			val := n.Content[i+1]
			if key.Value == "from" && val.Kind == yaml.SequenceNode {
				for _, item := range val.Content {
					if rewriteRecipeFromItem(item) {
						changed = true
					}
				}
			}
			if rewriteRecipeFromValues(val) {
				changed = true
			}
		}
	}
	return changed
}

// rewriteRecipeFromItem rewrites one recipe-from entry's kind + scope VALUES.
func rewriteRecipeFromItem(item *yaml.Node) bool {
	if item.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for i := 0; i+1 < len(item.Content); i += 2 {
		key := item.Content[i]
		val := item.Content[i+1]
		switch key.Value {
		case "kind":
			if val.Kind == yaml.ScalarNode && renameSectionValue(val) {
				changed = true
			}
		case "scope":
			if val.Kind == yaml.SequenceNode {
				for _, s := range val.Content {
					if s.Kind == yaml.ScalarNode && renameSectionValue(s) {
						changed = true
					}
				}
			}
		}
	}
	return changed
}

// renameSectionValue maps a single layer/image section value to candy/box in
// place; returns whether it changed. Any other value (candy/box/deploy/pod/vm)
// is left untouched (idempotent).
func renameSectionValue(node *yaml.Node) bool {
	switch node.Value {
	case "layer":
		node.Value = "candy"
		return true
	case "image":
		node.Value = "box"
		return true
	}
	return false
}
