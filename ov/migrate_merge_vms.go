package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// migrate_merge_vms.go — `ov migrate merge-vms`.
//
// Single atomic migration that performs four coupled transformations:
//
//   1. File merge     — vms.yml's content is appended to deploy.yml as
//                       a new top-level `vm:` key; vms.yml is deleted;
//                       overthink.yml's `includes:` drops `vms.yml`.
//   2. Key rename     — the top-level YAML key `vms:` becomes `vm:`
//                       (singular). The Go field name UnifiedFile.VM
//                       keeps its current spelling — only the tag changes.
//   3. Entity rename  — the sole current kind:vm entity `arch-cloud-base`
//                       becomes `arch`. References updated in all of:
//                         - vm map key in the merged block,
//                         - deployments["vm:arch-cloud-base"] key,
//                         - vm_source: values inside deployments,
//                         - artifact test paths (/tmp/arch-cloud-base-*).
//   4. Version bump   — overthink.yml `version: 1` → `version: 2`.
//
// Operates entirely on yaml.Node trees so deploy.yml's 60+ lines of
// hand-written test-group comments (C.1 through C.11) survive the
// round-trip. yaml.Marshal on a typed struct would silently discard
// them.
//
// Idempotent: running a second time against a migrated repo detects
// "nothing to do" (no vms.yml, no legacy keys, no legacy entity names,
// version already 2) and exits zero without touching the filesystem.

// MigrateMergeVmsCmd is `ov migrate merge-vms`.
type MigrateMergeVmsCmd struct {
	DryRun bool `long:"dry-run" help:"Print files that would be modified, don't touch the filesystem"`
}

// Run executes the migration.
func (c *MigrateMergeVmsCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	changed, err := MigrateMergeVms(MigrateMergeVmsOpts{
		Dir:    cwd,
		DryRun: c.DryRun,
	})
	if err != nil {
		return err
	}
	prefix := "modified "
	if c.DryRun {
		prefix = "[dry-run] would modify "
	}
	if len(changed) == 0 {
		fmt.Println("No legacy VM layout detected — nothing to migrate.")
		return nil
	}
	for _, p := range changed {
		fmt.Println(prefix + p)
	}
	if !c.DryRun {
		fmt.Println()
		fmt.Println("Migration complete. Stateful cleanup for the arch-cloud-base → arch rename")
		fmt.Println("(authorized by `disposable: true` on the VM):")
		fmt.Println()
		fmt.Println("  ov vm destroy arch-cloud-base --disk    # tear down old libvirt domain + qcow2")
		fmt.Println("  rm -rf ~/.local/share/ov/vm/ov-arch-cloud-base")
		fmt.Println("  ov rebuild arch                         # fresh-create under the new name")
		fmt.Println()
	}
	return nil
}

// MigrateMergeVmsOpts carries the migration-command inputs.
type MigrateMergeVmsOpts struct {
	Dir    string
	DryRun bool
}

// Legacy entity name that gets renamed during the merge. Kept as a
// package constant so the load-time rejection path in unified.go can
// reference the same spelling.
const (
	legacyVmEntityName  = "arch-cloud-base"
	currentVmEntityName = "arch"
	legacyVmFilename    = "vms.yml"
	legacyVmRootKey     = "vms"
	currentVmRootKey    = "vm"
	schemaVersion       = 4
)

// MigrateMergeVms performs the migration and returns the list of
// filesystem paths it modified (or would modify under --dry-run). A
// nil slice + nil error indicates "already migrated — nothing to do."
func MigrateMergeVms(opts MigrateMergeVmsOpts) ([]string, error) {
	var changed []string

	overthinkPath := filepath.Join(opts.Dir, UnifiedFileName)
	vmsPath := filepath.Join(opts.Dir, legacyVmFilename)
	deployPath := filepath.Join(opts.Dir, "deploy.yml")

	if !fileExists(overthinkPath) {
		return nil, fmt.Errorf("no %s in %s — run `ov migrate unified` first", UnifiedFileName, opts.Dir)
	}

	// Detect whether any legacy marker exists. If not, the migration
	// is a no-op (idempotency guarantee).
	needed, markers, err := detectLegacyMarkers(overthinkPath, vmsPath, deployPath)
	if err != nil {
		return nil, err
	}
	if !needed {
		return nil, nil
	}
	_ = markers // kept for potential future "what was found" reporting

	// Step 1: load all three files as yaml.Node trees so we can mutate
	// in place without losing comments.
	overthinkNode, err := readYamlDocument(overthinkPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", overthinkPath, err)
	}

	var vmsNode *yaml.Node
	if fileExists(vmsPath) {
		vmsNode, err = readYamlDocument(vmsPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", vmsPath, err)
		}
	}

	// deployExistedBefore tracks whether deploy.yml was on disk before
	// the migration ran. We only write deploy.yml back when it already
	// existed OR when we're merging real content into it (vm entities
	// from vms.yml, or a legacy root-key rename). A fixture with just a
	// version-1 overthink.yml should NOT gain an empty deploy.yml as a
	// side effect.
	deployExistedBefore := fileExists(deployPath)
	var deployNode *yaml.Node
	if deployExistedBefore {
		deployNode, err = readYamlDocument(deployPath)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", deployPath, err)
		}
	} else {
		deployNode = emptyMappingDocument()
	}
	deployNeedsWrite := deployExistedBefore

	// Step 2: rewrite arch-cloud-base → arch across every node before
	// we start moving subtrees between files. This is a pure string
	// substitution on scalar node values; map keys are also scalars and
	// get rewritten naturally.
	renameEntityInNode(overthinkNode, legacyVmEntityName, currentVmEntityName)
	if vmsNode != nil {
		renameEntityInNode(vmsNode, legacyVmEntityName, currentVmEntityName)
	}
	renameEntityInNode(deployNode, legacyVmEntityName, currentVmEntityName)

	// Step 3: if vms.yml exists, extract its `vms:` mapping and move
	// it into deploy.yml under the new `vm:` root key.
	if vmsNode != nil {
		vmsMap, err := extractVmsMapping(vmsNode, vmsPath)
		if err != nil {
			return nil, err
		}
		if vmsMap != nil {
			if err := mergeVmMappingIntoDeploy(deployNode, vmsMap, deployPath); err != nil {
				return nil, err
			}
			deployNeedsWrite = true
		}
	}

	// Step 4: if deploy.yml already carries a legacy `vms:` root key
	// (edge case — user hand-edited it into deploy.yml before running
	// the migration), rename it to `vm:` in place.
	renamed, err := renameRootKeyReport(deployNode, legacyVmRootKey, currentVmRootKey)
	if err != nil {
		return nil, err
	}
	if renamed {
		deployNeedsWrite = true
	}

	// Step 5: rewrite overthink.yml — drop vms.yml from includes, bump
	// version 1 → 2, verify no stray `vms:` root.
	overthinkChanged, err := rewriteOverthink(overthinkNode)
	if err != nil {
		return nil, err
	}

	// Step 6: write files back (honoring dry-run). deploy.yml only gets
	// written when it existed already OR when step 3/4 actually mutated
	// its tree — a pure overthink.yml version bump must not create an
	// empty deploy.yml as a side effect.
	if !opts.DryRun {
		if deployNeedsWrite {
			if err := writeYamlDocument(deployPath, deployNode); err != nil {
				return nil, fmt.Errorf("writing %s: %w", deployPath, err)
			}
		}
		if overthinkChanged {
			if err := writeYamlDocument(overthinkPath, overthinkNode); err != nil {
				return nil, fmt.Errorf("writing %s: %w", overthinkPath, err)
			}
		}
		if fileExists(vmsPath) {
			if err := os.Remove(vmsPath); err != nil {
				return nil, fmt.Errorf("removing %s: %w", vmsPath, err)
			}
		}
	}

	if deployNeedsWrite {
		changed = append(changed, deployPath)
	}
	if overthinkChanged {
		changed = append(changed, overthinkPath)
	}
	if fileExists(vmsPath) {
		changed = append(changed, vmsPath+" (delete)")
	}
	return changed, nil
}

// detectLegacyMarkers returns true when any of the four migration
// triggers are present. markers is a human-readable summary used for
// debug output; the caller may ignore it.
func detectLegacyMarkers(overthinkPath, vmsPath, deployPath string) (bool, []string, error) {
	var markers []string

	// Marker 1: vms.yml file exists at project root.
	if fileExists(vmsPath) {
		markers = append(markers, "vms.yml file present")
	}

	// Marker 2: overthink.yml version is below 2 or includes vms.yml.
	if fileExists(overthinkPath) {
		node, err := readYamlDocument(overthinkPath)
		if err != nil {
			return false, nil, fmt.Errorf("reading %s: %w", overthinkPath, err)
		}
		mapping := mappingOf(node)
		if mapping != nil {
			if versionNode := mapValue(mapping, "version"); versionNode != nil && versionNode.Value == "1" {
				markers = append(markers, "overthink.yml version: 1")
			}
			if includesNode := mapValue(mapping, "includes"); includesNode != nil && includesNode.Kind == yaml.SequenceNode {
				for _, inc := range includesNode.Content {
					if inc.Value == legacyVmFilename {
						markers = append(markers, "overthink.yml includes vms.yml")
						break
					}
				}
			}
		}
	}

	// Marker 3: deploy.yml carries a legacy `vms:` root key or
	// `arch` strings.
	if fileExists(deployPath) {
		raw, err := os.ReadFile(deployPath)
		if err != nil {
			return false, nil, fmt.Errorf("reading %s: %w", deployPath, err)
		}
		text := string(raw)
		if strings.Contains(text, "\nvms:") || strings.HasPrefix(text, "vms:") {
			markers = append(markers, "deploy.yml has legacy `vms:` root key")
		}
		if strings.Contains(text, legacyVmEntityName) {
			markers = append(markers, "deploy.yml references arch")
		}
	}

	return len(markers) > 0, markers, nil
}

// readYamlDocument reads a file as a yaml.Node tree. The returned
// node has Kind == DocumentNode; its single Content element is the
// root mapping (or sequence).
func readYamlDocument(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

// writeYamlDocument serializes a yaml.Node back to disk with 4-space
// indentation to match the repo's existing style.
func writeYamlDocument(path string, node *yaml.Node) error {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(node); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(buf.String()), 0o644)
}

// emptyMappingDocument constructs a DocumentNode whose single Content
// element is an empty mapping. Used when deploy.yml is missing.
func emptyMappingDocument() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.DocumentNode,
		Content: []*yaml.Node{
			{Kind: yaml.MappingNode, Tag: "!!map"},
		},
	}
}

// mappingOf returns the root mapping node inside a DocumentNode. For
// any other shape (sequence, scalar), returns nil.
func mappingOf(doc *yaml.Node) *yaml.Node {
	if doc == nil {
		return nil
	}
	root := doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return nil
		}
		root = doc.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

// mapValue returns the value node for the given key in a mapping, or
// nil if absent. Mappings are encoded as alternating key/value Content
// slices in yaml.v3.
func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMapValue upserts a key/value pair in a mapping. Preserves the
// existing order when the key is present; appends when absent.
func setMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"}
	mapping.Content = append(mapping.Content, keyNode, value)
}

// renameMapKey renames a key in a mapping. Returns true if the key
// was found and renamed. No-op (returns false) when the key is
// absent.
func renameMapKey(mapping *yaml.Node, oldKey, newKey string) bool {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == oldKey {
			mapping.Content[i].Value = newKey
			return true
		}
	}
	return false
}

// renameEntityInNode walks a yaml.Node tree and performs string
// substitution on every Value, HeadComment, LineComment, and
// FootComment. This catches scalar values (map keys + leaf strings)
// AND the hand-written comments preserved by yaml.v3's Node-level
// round-trip. Substring replace so composite names like
// `vm:arch` and `/tmp/arch-spice.png` update
// correctly.
func renameEntityInNode(node *yaml.Node, oldName, newName string) {
	if node == nil {
		return
	}
	if node.Kind == yaml.ScalarNode {
		if strings.Contains(node.Value, oldName) {
			node.Value = strings.ReplaceAll(node.Value, oldName, newName)
		}
	}
	if strings.Contains(node.HeadComment, oldName) {
		node.HeadComment = strings.ReplaceAll(node.HeadComment, oldName, newName)
	}
	if strings.Contains(node.LineComment, oldName) {
		node.LineComment = strings.ReplaceAll(node.LineComment, oldName, newName)
	}
	if strings.Contains(node.FootComment, oldName) {
		node.FootComment = strings.ReplaceAll(node.FootComment, oldName, newName)
	}
	for _, child := range node.Content {
		renameEntityInNode(child, oldName, newName)
	}
}

// extractVmsMapping pulls the value node of the `vms:` root key out of
// a vms.yml document. Returns nil when vms.yml has no such key (odd
// but valid — nothing to merge). Returns an error when the document
// has a shape we don't recognize (non-mapping root, etc.).
func extractVmsMapping(vmsDoc *yaml.Node, path string) (*yaml.Node, error) {
	root := mappingOf(vmsDoc)
	if root == nil {
		return nil, fmt.Errorf("%s: expected a mapping at the top level", path)
	}
	vms := mapValue(root, legacyVmRootKey)
	if vms == nil {
		return nil, nil
	}
	if vms.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: `vms:` is not a mapping", path)
	}
	return vms, nil
}

// mergeVmMappingIntoDeploy installs the `vm:` root key in deploy.yml
// carrying the contents of the extracted vms.yml mapping. Errors on
// rename-target collision (entity `arch` already present in deploy.yml
// under either `vm:` or legacy `vms:`).
func mergeVmMappingIntoDeploy(deployDoc, vmsMap *yaml.Node, path string) error {
	root := mappingOf(deployDoc)
	if root == nil {
		return fmt.Errorf("%s: expected a mapping at the top level", path)
	}
	if existing := mapValue(root, currentVmRootKey); existing != nil {
		// Already merged once. Merge-or-collide on per-entity basis.
		return mergeVmMaps(existing, vmsMap, path)
	}
	// No `vm:` root yet — attach the mapping directly.
	setMapValue(root, currentVmRootKey, vmsMap)
	return nil
}

// mergeVmMaps copies entries from src into dst. Errors on key
// collision (user would lose authored customizations; force manual
// resolution).
func mergeVmMaps(dst, src *yaml.Node, path string) error {
	if dst == nil || src == nil {
		return nil
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		key := src.Content[i].Value
		if mapValue(dst, key) != nil {
			return fmt.Errorf("%s: vm entity %q already exists — migration cannot safely merge without overwriting", path, key)
		}
		dst.Content = append(dst.Content, src.Content[i], src.Content[i+1])
	}
	return nil
}

// renameRootKeyReport renames a top-level key in a document and
// reports whether a rename actually happened. No-op (returns false)
// when the key is absent. Errors when both the old and new key
// coexist — forcing manual resolution rather than silent overwrite.
func renameRootKeyReport(doc *yaml.Node, oldKey, newKey string) (bool, error) {
	root := mappingOf(doc)
	if root == nil {
		return false, nil
	}
	if mapValue(root, oldKey) == nil {
		return false, nil
	}
	if mapValue(root, newKey) != nil {
		return false, fmt.Errorf("cannot rename root key %q → %q: both keys present", oldKey, newKey)
	}
	renameMapKey(root, oldKey, newKey)
	return true, nil
}

// rewriteOverthink performs three edits on overthink.yml:
//  1. Bump `version: 1` → `version: 2` (or insert `version: 2` when absent).
//  2. Drop `vms.yml` from the `includes:` sequence.
//  3. Reject an unexpected shape rather than silently succeeding.
//
// Returns true when the node tree was modified.
func rewriteOverthink(doc *yaml.Node) (bool, error) {
	root := mappingOf(doc)
	if root == nil {
		return false, fmt.Errorf("overthink.yml: expected a mapping at the top level")
	}

	changed := false

	// Version bump.
	if versionNode := mapValue(root, "version"); versionNode != nil {
		if versionNode.Value != fmt.Sprintf("%d", schemaVersion) {
			versionNode.Value = fmt.Sprintf("%d", schemaVersion)
			versionNode.Tag = "!!int"
			changed = true
		}
	} else {
		// Insert version at the top of the mapping.
		versionKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "version", Tag: "!!str"}
		versionVal := &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", schemaVersion), Tag: "!!int"}
		root.Content = append([]*yaml.Node{versionKey, versionVal}, root.Content...)
		changed = true
	}

	// Drop vms.yml from includes:.
	if includesNode := mapValue(root, "includes"); includesNode != nil && includesNode.Kind == yaml.SequenceNode {
		kept := includesNode.Content[:0]
		for _, inc := range includesNode.Content {
			if inc.Value == legacyVmFilename {
				changed = true
				continue
			}
			kept = append(kept, inc)
		}
		includesNode.Content = kept
	}

	return changed, nil
}
