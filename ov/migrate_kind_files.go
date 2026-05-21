package main

// migrate_kind_files.go — `ov migrate`.
//
// Combined idempotent migration for the 2026-05 cutover that lands:
//
//   1. Schema kind rename: `kind: deployment` → `kind: deploy` everywhere
//      (kind-keyed standalone docs and root-shape collection maps).
//   2. Per-kind file split: extract inline `image:` and `vm:` blocks from
//      overthink.yml into sibling `image.yml` and `vm.yml`. Create empty
//      `pod.yml` and `k8s.yml` stubs for symmetry. Append the new files
//      to overthink.yml's `includes:` list.
//
// Preserves YAML comments via yaml.Node round-trip (same approach as
// migrate_schema_v4.go). Idempotent on already-migrated trees (each
// transform checks for its end-state before mutating; re-runs are no-ops).
//
// Hard load-time errors in unified.go point at this command for residual
// `kind: deployment` and `deployment:` root collection keys.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateKindFilesResult reports the outcome.
type MigrateKindFilesResult struct {
	Transforms   []string
	WrittenFiles []string
	NoChanges    bool
}

// MigrateKindFiles applies the cutover transforms to the project rooted at dir.
// In dry-run mode no files are written.
func MigrateKindFiles(dir string, dryRun bool) (MigrateKindFilesResult, error) {
	var res MigrateKindFilesResult
	root := filepath.Join(dir, "overthink.yml")

	// Load overthink.yml.
	rootData, err := os.ReadFile(root)
	if err != nil {
		return res, fmt.Errorf("reading %s: %w", root, err)
	}
	var rootDoc yaml.Node
	if err := yaml.Unmarshal(rootData, &rootDoc); err != nil {
		return res, fmt.Errorf("parsing %s: %w", root, err)
	}
	if len(rootDoc.Content) == 0 || rootDoc.Content[0].Kind != yaml.MappingNode {
		return res, fmt.Errorf("%s: not a mapping document", root)
	}
	rootMap := rootDoc.Content[0]
	rootChanged := false

	// Transform 1: extract image: from overthink.yml → image.yml.
	imagePath := filepath.Join(dir, "image.yml")
	if extracted, n, err := extractRootMapToFile(rootMap, "image", imagePath, "image", dryRun); err != nil {
		return res, err
	} else if extracted {
		res.Transforms = append(res.Transforms, fmt.Sprintf("extracted image: (%d entries) → image.yml", n))
		res.WrittenFiles = append(res.WrittenFiles, imagePath)
		rootChanged = true
	}

	// Transform 2: extract vm: (root-shape templates only) from overthink.yml → vm.yml.
	vmPath := filepath.Join(dir, "vm.yml")
	if extracted, n, err := extractRootMapToFile(rootMap, "vm", vmPath, "vm", dryRun); err != nil {
		return res, err
	} else if extracted {
		res.Transforms = append(res.Transforms, fmt.Sprintf("extracted vm: (%d entries) → vm.yml", n))
		res.WrittenFiles = append(res.WrittenFiles, vmPath)
		rootChanged = true
	}

	// Transform 3: create empty pod.yml and k8s.yml stubs.
	for _, kind := range []string{"pod", "k8s"} {
		stubPath := filepath.Join(dir, kind+".yml")
		if _, err := os.Stat(stubPath); os.IsNotExist(err) {
			res.Transforms = append(res.Transforms, fmt.Sprintf("created %s.yml stub", kind))
			res.WrittenFiles = append(res.WrittenFiles, stubPath)
			if !dryRun {
				if err := writeKindStub(stubPath, kind); err != nil {
					return res, err
				}
			}
		}
	}

	// Transform 4: append per-kind files to overthink.yml's include: list, but
	// ONLY those that actually exist (or were created/extracted this run). A
	// project with no VMs has no vm.yml — adding it to include: would make the
	// loader fail on a missing file. (vm.yml/image.yml come from extraction;
	// pod.yml/k8s.yml from the stub transform above.)
	createdThisRun := map[string]bool{}
	for _, w := range res.WrittenFiles {
		createdThisRun[filepath.Base(w)] = true
	}
	var desiredIncludes []string
	for _, f := range []string{"image.yml", "vm.yml", "pod.yml", "k8s.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil || createdThisRun[f] {
			desiredIncludes = append(desiredIncludes, f)
		}
	}
	if added := ensureIncludes(rootMap, desiredIncludes); added > 0 {
		res.Transforms = append(res.Transforms, fmt.Sprintf("added %d entry/entries to includes:", added))
		rootChanged = true
	}

	// Transform 5: rename root-key `deployment:` → `deploy:` in deploy.yml.
	deployPath := filepath.Join(dir, "deploy.yml")
	if _, err := os.Stat(deployPath); err == nil {
		renamed, err := renameDeployRootKey(deployPath, dryRun)
		if err != nil {
			return res, err
		}
		if renamed {
			res.Transforms = append(res.Transforms, "deploy.yml: root-key deployment: → deploy:")
			res.WrittenFiles = append(res.WrittenFiles, deployPath)
		}
	}

	// Transform 6: rename `kind: deployment` → `kind: deploy` in every
	// reachable YAML file (overthink.yml + includes + every *.yml at the
	// project root). Idempotent — files with no kind: deployment are no-ops.
	yamlFiles, err := collectProjectYAMLs(dir)
	if err != nil {
		return res, err
	}
	for _, p := range yamlFiles {
		renamed, err := renameKindDocKey(p, "deployment", "deploy", dryRun)
		if err != nil {
			return res, err
		}
		if renamed > 0 {
			res.Transforms = append(res.Transforms,
				fmt.Sprintf("%s: %d kind: deployment → kind: deploy", filepath.Base(p), renamed))
			res.WrittenFiles = append(res.WrittenFiles, p)
		}
	}

	// Write overthink.yml if changed.
	if rootChanged {
		if !dryRun {
			if err := writeYAMLDoc(root, &rootDoc); err != nil {
				return res, err
			}
		}
		res.WrittenFiles = append(res.WrittenFiles, root)
	}

	res.NoChanges = !rootChanged && len(res.WrittenFiles) == 0
	return res, nil
}

// extractRootMapToFile extracts the named root-shape map from rootMap and
// writes it to dstPath. Returns (extracted, entryCount, error). Idempotent:
// returns (false, 0, nil) when there's no work to do.
//
// The extracted file's shape is `kindKey: <map>` with a `version: 4` prefix
// and a comment header. Kind-keyed entries (mapping with a `name:` child) are
// LEFT in place — only root-shape collection maps are extracted.
func extractRootMapToFile(rootMap *yaml.Node, kindKey, dstPath, kindLabel string, dryRun bool) (bool, int, error) {
	val := findMappingValue(rootMap, kindKey)
	if val == nil || val.Kind != yaml.MappingNode || len(val.Content) == 0 {
		return false, 0, nil
	}
	// Disambiguate: if val is kind-keyed (has a `name:` child), leave it alone.
	if findMappingValue(val, "name") != nil {
		return false, 0, nil
	}
	// Count entries (each entry is a key/value pair → 2 nodes).
	count := len(val.Content) / 2

	// If dstPath exists, merge root-wins (existing keys win).
	merged := val
	if data, err := os.ReadFile(dstPath); err == nil {
		var existing yaml.Node
		if err := yaml.Unmarshal(data, &existing); err == nil && len(existing.Content) > 0 {
			if existingRoot := existing.Content[0]; existingRoot.Kind == yaml.MappingNode {
				if existingMap := findMappingValue(existingRoot, kindKey); existingMap != nil && existingMap.Kind == yaml.MappingNode {
					merged = mergeMappingsRootWins(existingMap, val)
				}
			}
		}
	}

	// Build the new file's document.
	dstDoc := buildKindFileDoc(kindKey, kindLabel, merged)

	if !dryRun {
		if err := writeYAMLDoc(dstPath, dstDoc); err != nil {
			return false, 0, err
		}
	}

	// Remove the kind from rootMap.
	removeMappingKey(rootMap, kindKey)
	return true, count, nil
}

// mergeMappingsRootWins merges entries from `src` into `dst`. Entries in `dst`
// (the existing target file) win; entries in `src` (overthink.yml inline) are
// added only if not present.
func mergeMappingsRootWins(dst, src *yaml.Node) *yaml.Node {
	if dst == nil || dst.Kind != yaml.MappingNode {
		return src
	}
	if src == nil || src.Kind != yaml.MappingNode {
		return dst
	}
	have := map[string]bool{}
	for i := 0; i < len(dst.Content)-1; i += 2 {
		if dst.Content[i].Kind == yaml.ScalarNode {
			have[dst.Content[i].Value] = true
		}
	}
	for i := 0; i < len(src.Content)-1; i += 2 {
		k := src.Content[i]
		if k.Kind != yaml.ScalarNode || have[k.Value] {
			continue
		}
		dst.Content = append(dst.Content, k, src.Content[i+1])
	}
	return dst
}

// buildKindFileDoc constructs a fully-formed yaml.Node document with
// `version: 4` + a comment header + the named kind map.
func buildKindFileDoc(kindKey, kindLabel string, kindMap *yaml.Node) *yaml.Node {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	root := &yaml.Node{Kind: yaml.MappingNode}
	root.HeadComment = fmt.Sprintf(
		" Per-kind file for %s entries. Sibling of overthink.yml — included via\n"+
			" overthink.yml's `includes:` list. The schema kind discriminator is\n"+
			" `kind: %s` for kind-keyed entries; root-shape map is `%s:` (this file).\n"+
			" 2026-05 kind-files cutover.",
		kindLabel, kindLabel, kindKey)

	// version: 4
	versionKey := &yaml.Node{Kind: yaml.ScalarNode, Value: "version"}
	versionVal := &yaml.Node{Kind: yaml.ScalarNode, Value: "4", Tag: "!!int"}
	root.Content = append(root.Content, versionKey, versionVal)

	// kindKey: <map>
	mapKey := &yaml.Node{Kind: yaml.ScalarNode, Value: kindKey}
	root.Content = append(root.Content, mapKey, kindMap)

	doc.Content = []*yaml.Node{root}
	return doc
}

// writeKindStub creates an empty `version: 4` + `<kind>: {}` file with a header.
func writeKindStub(path, kindKey string) error {
	emptyMap := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}
	doc := buildKindFileDoc(kindKey, kindKey, emptyMap)
	return writeYAMLDoc(path, doc)
}

// writeYAMLDoc serializes a yaml.Node tree with 4-space indent (matches the
// existing migrate_schema_v4.go style).
func writeYAMLDoc(path string, doc *yaml.Node) error {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	enc.Close()
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// ensureIncludes appends each desired entry to rootMap's include: list if
// absent. Returns the count appended. Accepts either the canonical singular
// `include:` (post-2026-05 field-singular cutover, which is what the loader
// reads) or the legacy plural `includes:` (kind-files predates field-singular,
// so an ancient config replayed through the chain still has the plural key at
// this point). Only creates a new list when NEITHER is present, using the
// canonical singular key — without this dual lookup the migrator never finds
// the existing `include:` and spuriously injects a redundant `includes:` block.
func ensureIncludes(rootMap *yaml.Node, desired []string) int {
	includes := findMappingValue(rootMap, "include")
	if includes == nil {
		includes = findMappingValue(rootMap, "includes")
	}
	if includes == nil {
		// Create include: as a SequenceNode and prepend to the mapping.
		seq := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range desired {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e})
		}
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: "include"}
		// Insert after `version:` if present, else at the start.
		insertAt := 0
		for i := 0; i < len(rootMap.Content)-1; i += 2 {
			if rootMap.Content[i].Value == "version" {
				insertAt = i + 2
				break
			}
		}
		rootMap.Content = append(rootMap.Content[:insertAt],
			append([]*yaml.Node{key, seq}, rootMap.Content[insertAt:]...)...)
		return len(desired)
	}
	if includes.Kind != yaml.SequenceNode {
		return 0
	}
	have := map[string]bool{}
	for _, n := range includes.Content {
		if n.Kind == yaml.ScalarNode {
			have[n.Value] = true
		}
	}
	added := 0
	for _, e := range desired {
		if have[e] {
			continue
		}
		includes.Content = append(includes.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e})
		added++
	}
	return added
}

// renameDeployRootKey renames the root-shape `deployment:` key to `deploy:`
// in a YAML file. Returns (renamed, error). Idempotent.
func renameDeployRootKey(path string, dryRun bool) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	root := doc.Content[0]

	// Disambiguation: only rename if the value is a root-shape collection map
	// (NOT kind-keyed with a `name:` child).
	val := findMappingValue(root, "deployment")
	if val == nil {
		return false, nil
	}
	if val.Kind == yaml.MappingNode && findMappingValue(val, "name") != nil {
		// Kind-keyed form — handled by renameKindDocKey, not here.
		return false, nil
	}

	if !renameRootKey(root, "deployment", "deploy") {
		return false, nil
	}

	if !dryRun {
		if err := writeYAMLDoc(path, &doc); err != nil {
			return false, err
		}
	}
	return true, nil
}

// renameKindDocKey rewrites kind-keyed standalone docs and any flat
// occurrence of `kind: <oldKind>` → `kind: <newKind>`. Walks every YAML
// document in the file (multi-document streams supported via `---`).
// Returns the count of renames applied.
func renameKindDocKey(path, oldKind, newKind string, dryRun bool) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}
	// Decode as a multi-doc stream.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var docs []*yaml.Node
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err.Error() == "EOF" {
				break
			}
			// Plain io.EOF check via string match — yaml.v3 doesn't expose io.EOF directly here.
			break
		}
		docs = append(docs, &doc)
	}
	if len(docs) == 0 {
		return 0, nil
	}
	count := 0
	for _, doc := range docs {
		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		root := doc.Content[0]
		// Case A: `kind: <old>` scalar at root.
		if v := findMappingValue(root, "kind"); v != nil && v.Kind == yaml.ScalarNode && v.Value == oldKind {
			v.Value = newKind
			count++
		}
		// Case B: kind-keyed wrapper key `<old>:` with a `name:` child.
		// In that shape, the kind is encoded by the wrapper key itself.
		if val := findMappingValue(root, oldKind); val != nil && val.Kind == yaml.MappingNode {
			if findMappingValue(val, "name") != nil {
				if renameRootKey(root, oldKind, newKind) {
					count++
				}
			}
		}
	}
	if count == 0 || dryRun {
		return count, nil
	}
	// Re-encode all docs.
	var out bytes.Buffer
	for i, doc := range docs {
		if i > 0 {
			out.WriteString("---\n")
		}
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(4)
		if err := enc.Encode(doc); err != nil {
			return count, fmt.Errorf("encoding %s: %w", path, err)
		}
		enc.Close()
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		return count, fmt.Errorf("writing %s: %w", path, err)
	}
	return count, nil
}

// collectProjectYAMLs returns every *.yml file at dir's top level. The
// migration only touches files at the project root; nested layer.yml files
// are left alone (they don't carry kind: deployment).
func collectProjectYAMLs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	return paths, nil
}
