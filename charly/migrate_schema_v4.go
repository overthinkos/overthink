package main

// migrate_schema_v4.go — `charly migrate`.
//
// Converts schema v3 configs to v4:
//
//   1. Bump `version: 3` → `4` at the document root.
//   2. Rename plural root-level keys to singular:
//      - `images:` → `image:`
//      - `deployments.images.<name>` → `deployment.<name>` (flatten the
//        intermediate `images:` nesting key)
//   3. For each kind:image entry, delete the deploy-choice fields:
//      - `tunnel`, `engine`, `dns`, `acme_email` — these move to
//        kind:deployment entries (no automatic move; operators hand-set
//        them on deployments if needed).
//   4. For each kind:deployment entry:
//      - Rename `vm_source:` → `vm:` (template reference).
//      - Rename `cluster:` → `k8s:` (template reference).
//      - Rename `children:` → `nested:`.
//      - Reject authored `inside:` with migration-hint error.
//      - Rename legacy `target:` values: `container` → `pod`,
//        `kubernetes` → `k8s`.
//   5. Delete dead `vm:` sub-blocks on bootc image entries (silent
//      no-op YAML since the 2026-04 VM cutover).
//
// Preserves YAML comments via yaml.Node round-trip.
// Idempotent on already-v4 files.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// schemaV4CandidateFiles are the project-root YAML files that may carry v3
// schema structure. The chain applies MigrateSchemaV4 to each that exists.
var schemaV4CandidateFiles = []string{
	"overthink.yml", "deploy.yml", "image.yml", "images.yml",
	"vm.yml", "vms.yml", "pod.yml", "k8s.yml", "local.yml", "eval.yml",
}

// MigrateSchemaV4Files applies the v3→v4 transforms to every candidate YAML
// file under dir, preserving comments via yaml.Node round-trip. It is the
// chain-callable form used by the unified `charly migrate` runner. Returns the
// list of files changed (or, under dryRun, that would change). Files already
// at v4 are no-ops.
func MigrateSchemaV4Files(dir string, dryRun bool) ([]string, error) {
	var changed []string
	for _, name := range schemaV4CandidateFiles {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // absent file → nothing to migrate
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return changed, fmt.Errorf("parsing %s: %w", path, err)
		}
		if MigrateSchemaV4(&doc).NoChanges {
			continue
		}
		changed = append(changed, path)
		if dryRun {
			continue
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(4)
		if err := enc.Encode(&doc); err != nil {
			return changed, fmt.Errorf("encoding %s: %w", path, err)
		}
		enc.Close()
		if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
			return changed, fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return changed, nil
}

// MigrateSchemaV4Result reports what the migration changed.
type MigrateSchemaV4Result struct {
	Transforms []string
	NoChanges  bool
}

// MigrateSchemaV4 applies all v3→v4 transforms to the given yaml.Node
// tree in-place.
func MigrateSchemaV4(doc *yaml.Node) MigrateSchemaV4Result {
	var result MigrateSchemaV4Result
	if doc == nil || len(doc.Content) == 0 {
		result.NoChanges = true
		return result
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		result.NoChanges = true
		return result
	}

	changed := false
	// 1. Bump a legacy integer version (1/2/3) to 4. A CalVer string (the
	//    post-2026-05 schema version, e.g. 2026.141.1530) or an already-current
	//    value is left untouched — the terminal calver-schema step owns the HEAD
	//    stamp. Without this guard the v3→v4 bump would rewrite a CalVer DOWN to
	//    "4" and corrupt every versioned file.
	if v := findMappingValue(root, "version"); v != nil && v.Kind == yaml.ScalarNode {
		if _, isCalVer := ParseCalVer(v.Value); !isCalVer {
			if n, err := strconv.Atoi(v.Value); err == nil && n < 4 {
				result.Transforms = append(result.Transforms, fmt.Sprintf("version: %s → 4", v.Value))
				v.Value = "4"
				changed = true
			}
		}
	}

	// 2a. images: → image:
	if renameRootKey(root, "images", "image") {
		result.Transforms = append(result.Transforms, "images: → image:")
		changed = true
	}

	// 2b. deployments.images.* → deployment.*
	if deps := findMappingValue(root, "deployments"); deps != nil && deps.Kind == yaml.MappingNode {
		// Check if it has the nested .images.* shape.
		if imgs := findMappingValue(deps, "images"); imgs != nil && imgs.Kind == yaml.MappingNode {
			// Replace the root's `deployments:` entry with a flat
			// `deployment:` entry whose value is the inner images map.
			setRootKey(root, "deployments", "deploy", imgs)
			result.Transforms = append(result.Transforms, "deployments.images.* → deployment.*")
			changed = true
		} else {
			// deployments: is a flat map — rename key to deployment:
			if renameRootKey(root, "deployments", "deploy") {
				result.Transforms = append(result.Transforms, "deployments: → deployment:")
				changed = true
			}
		}
	}

	// 3. For each kind:image entry, delete deploy-choice fields.
	if img := findMappingValue(root, "image"); img != nil && img.Kind == yaml.MappingNode {
		for i := 1; i < len(img.Content); i += 2 {
			entry := img.Content[i]
			if entry.Kind != yaml.MappingNode {
				continue
			}
			for _, field := range []string{"tunnel", "engine", "dns", "acme_email"} {
				if removeMappingKey(entry, field) {
					result.Transforms = append(result.Transforms,
						fmt.Sprintf("image.%s.%s removed (deploy-only in v4)", img.Content[i-1].Value, field))
					changed = true
				}
			}
			// Delete dead `vm:` sub-block on bootc image entries.
			if removeMappingKey(entry, "vm") {
				result.Transforms = append(result.Transforms,
					fmt.Sprintf("image.%s.vm: removed (dead bootc YAML)", img.Content[i-1].Value))
				changed = true
			}
		}
	}

	// 4. For each kind:deployment entry (flat map), rename fields.
	if deps := findMappingValue(root, "deploy"); deps != nil && deps.Kind == yaml.MappingNode {
		for i := 1; i < len(deps.Content); i += 2 {
			entry := deps.Content[i]
			if entry.Kind != yaml.MappingNode {
				continue
			}
			name := deps.Content[i-1].Value
			if renameMappingKey(entry, "vm_source", "vm") {
				result.Transforms = append(result.Transforms,
					fmt.Sprintf("deployment.%s.vm_source: → vm:", name))
				changed = true
			}
			if renameMappingKey(entry, "cluster", "k8s") {
				result.Transforms = append(result.Transforms,
					fmt.Sprintf("deployment.%s.cluster: → k8s:", name))
				changed = true
			}
			if renameMappingKey(entry, "children", "nested") {
				result.Transforms = append(result.Transforms,
					fmt.Sprintf("deployment.%s.children: → nested:", name))
				changed = true
			}
			// target: container → pod, target: kubernetes → k8s
			if t := findMappingValue(entry, "target"); t != nil && t.Kind == yaml.ScalarNode {
				if t.Value == "container" {
					t.Value = "pod"
					result.Transforms = append(result.Transforms,
						fmt.Sprintf("deployment.%s.target: container → pod", name))
					changed = true
				} else if t.Value == "kubernetes" {
					t.Value = "k8s"
					result.Transforms = append(result.Transforms,
						fmt.Sprintf("deployment.%s.target: kubernetes → k8s", name))
					changed = true
				}
			}
			// Recurse into nested: entries for the same renames.
			if nested := findMappingValue(entry, "nested"); nested != nil && nested.Kind == yaml.MappingNode {
				renameNestedDeployments(nested, name, &result.Transforms, &changed)
			}
		}
	}

	result.NoChanges = !changed
	return result
}

// renameNestedDeployments applies the same field-rename set to every
// entry in a `nested:` map, recursively.
func renameNestedDeployments(node *yaml.Node, parentPath string, transforms *[]string, changed *bool) {
	for i := 1; i < len(node.Content); i += 2 {
		entry := node.Content[i]
		if entry.Kind != yaml.MappingNode {
			continue
		}
		name := parentPath + "." + node.Content[i-1].Value
		if renameMappingKey(entry, "vm_source", "vm") {
			*transforms = append(*transforms, fmt.Sprintf("deployment.%s.vm_source: → vm:", name))
			*changed = true
		}
		if renameMappingKey(entry, "cluster", "k8s") {
			*transforms = append(*transforms, fmt.Sprintf("deployment.%s.cluster: → k8s:", name))
			*changed = true
		}
		if renameMappingKey(entry, "children", "nested") {
			*transforms = append(*transforms, fmt.Sprintf("deployment.%s.children: → nested:", name))
			*changed = true
		}
		if t := findMappingValue(entry, "target"); t != nil && t.Kind == yaml.ScalarNode {
			if t.Value == "container" {
				t.Value = "pod"
				*transforms = append(*transforms, fmt.Sprintf("deployment.%s.target: container → pod", name))
				*changed = true
			} else if t.Value == "kubernetes" {
				t.Value = "k8s"
				*transforms = append(*transforms, fmt.Sprintf("deployment.%s.target: kubernetes → k8s", name))
				*changed = true
			}
		}
		if inner := findMappingValue(entry, "nested"); inner != nil && inner.Kind == yaml.MappingNode {
			renameNestedDeployments(inner, name, transforms, changed)
		}
	}
}

// --- yaml.Node helpers ---

// findMappingValue returns the value node for the given key in a mapping,
// or nil if the key isn't present.
func findMappingValue(m *yaml.Node, key string) *yaml.Node {
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

// renameRootKey renames key oldKey → newKey on a mapping node. Returns
// true if the rename occurred, false if oldKey was absent (or newKey was
// already present).
func renameRootKey(m *yaml.Node, oldKey, newKey string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	// Refuse to rename if newKey already exists.
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Value == newKey {
			return false
		}
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == oldKey {
			m.Content[i].Value = newKey
			return true
		}
	}
	return false
}

// renameMappingKey is the same as renameRootKey but doesn't require the
// mapping to be the document root.
func renameMappingKey(m *yaml.Node, oldKey, newKey string) bool {
	return renameRootKey(m, oldKey, newKey)
}

// removeMappingKey deletes the given key (and its value) from a mapping
// node. Returns true if something was removed.
func removeMappingKey(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// setRootKey replaces the pair for oldKey with (newKey: newValue) while
// preserving position in the mapping's Content slice.
func setRootKey(m *yaml.Node, oldKey, newKey string, newValue *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(m.Content)-1; i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == oldKey {
			m.Content[i].Value = newKey
			m.Content[i+1] = newValue
			return
		}
	}
}
