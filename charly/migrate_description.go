package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MigrateDescriptionOpts carries migration inputs.
type MigrateDescriptionOpts struct {
	Dir    string
	DryRun bool
}

// MigrateDescription walks Dir for every .yml / .yaml file and
// applies the description synthesis to each entity in the file.
// Returns the list of files it modified (or would modify).
func MigrateDescription(opts MigrateDescriptionOpts) ([]string, error) {
	var touched []string

	err := filepath.Walk(opts.Dir, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		if info.IsDir() {
			// Skip the shared build-artifact / cache + nested-submodule set, plus
			// bin/vendor/.claude which can't carry entity YAML either.
			if migrateSkipDir(path, opts.Dir) {
				return filepath.SkipDir
			}
			switch info.Name() {
			case "bin", "vendor", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".yml") && !strings.HasSuffix(lower, ".yaml") {
			return nil
		}

		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}

		newData, changed, mErr := migrateDescriptionInFile(data)
		if mErr != nil {
			// Non-fatal: skip files that don't parse. A migrator run
			// should never explode on unrelated YAML (kustomize
			// manifests, CI configs, etc.).
			return nil
		}
		if !changed {
			return nil
		}
		touched = append(touched, path)
		if !opts.DryRun {
			if werr := os.WriteFile(path, newData, info.Mode()); werr != nil {
				return fmt.Errorf("writing %s: %w", path, werr)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return touched, nil
}

// migrateDescriptionInFile parses a YAML document-stream and applies
// the description-synthesis to every entity it finds. Returns the
// re-serialized bytes plus a `changed` flag.
func migrateDescriptionInFile(data []byte) ([]byte, bool, error) {
	// Decode as a stream of yaml.Nodes so we preserve document
	// separators + comments.
	var docs []*yaml.Node
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var n yaml.Node
		if err := dec.Decode(&n); err != nil {
			break
		}
		docs = append(docs, &n)
	}
	if len(docs) == 0 {
		return data, false, nil
	}

	changed := false
	for _, doc := range docs {
		if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
			continue
		}
		top := doc.Content[0]
		if top.Kind != yaml.MappingNode {
			continue
		}
		// Process each top-level entity in the document. We handle:
		//   1. Kind-keyed wrapper form: `layer: {name: foo, info: ...}`
		//   2. Standalone form: `kind: layer, name: foo, info: ...`
		//   3. Root-shape form with maps of entities: `images: { foo: {...}, bar: {...} }`
		changed = migrateEntityMap(top) || changed
	}

	if !changed {
		return data, false, nil
	}
	// Re-serialize. Note: yaml.v3's encoder preserves comments on
	// Nodes that had them, so authored comments survive the round-trip.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return nil, false, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, false, err
	}
	return []byte(buf.String()), true, nil
}

// migrateEntityMap walks a mapping node and applies description-
// synthesis where applicable. Returns true if any entity was modified.
//
// Detection cases:
//
//  1. Root-shape with `layers:` / `images:` / `deployments:` / etc.
//     keyed by entity name → recurse into each value.
//  2. Kind-keyed wrapper: the map has one of `layer:` / `image:` /
//     ... as a key → treat the value as an entity map.
//  3. Standalone kind entity: map has `kind:` and `name:` keys →
//     treat the map itself as the entity map.
func migrateEntityMap(m *yaml.Node) bool {
	if m.Kind != yaml.MappingNode {
		return false
	}
	changed := false

	// Find top-level structural keys.
	var isStandalone bool
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == "kind" {
			isStandalone = true
			break
		}
	}

	if isStandalone {
		changed = synthesizeDescriptionOnEntity(m) || changed
		return changed
	}

	// Walk top-level keys looking for kind-keyed wrappers OR root-shape
	// maps-of-entities.
	kindKeyedKeys := map[string]bool{
		"layer": true, "image": true, "pod": true, "vm": true,
		"k8s": true, "host": true, "deployment": true, "builder": true,
		"distro": true, "init": true,
	}
	rootShapeKeys := map[string]bool{
		"layers": true, "images": true, "deployments": true,
		"builders": true, "distros": true, "inits": true,
		"vm":   true, // deploy.yml-style top-level vm: map
		"pods": true, "hosts": true,
	}

	for i := 0; i < len(m.Content)-1; i += 2 {
		keyNode := m.Content[i]
		valNode := m.Content[i+1]
		switch {
		case kindKeyedKeys[keyNode.Value] && valNode.Kind == yaml.MappingNode:
			changed = synthesizeDescriptionOnEntity(valNode) || changed
		case rootShapeKeys[keyNode.Value] && valNode.Kind == yaml.MappingNode:
			// Map of entity name → entity body. Recurse into each value.
			for j := 0; j < len(valNode.Content)-1; j += 2 {
				entityVal := valNode.Content[j+1]
				if entityVal.Kind == yaml.MappingNode {
					changed = synthesizeDescriptionOnEntity(entityVal) || changed
				}
			}
		}
	}
	return changed
}

// synthesizeDescriptionOnEntity injects a description: node on an
// entity-body map if legacy info:/status: are present without a
// description:. Also DELETES the legacy info:/status: keys from the
// entity map — the cutover requires that post-migration files are
// clean, not just augmented. Returns true if mutation occurred.
func synthesizeDescriptionOnEntity(entity *yaml.Node) bool {
	if entity.Kind != yaml.MappingNode {
		return false
	}
	var info, status string
	var nameVal string
	hasDescription := false
	infoIdx, statusIdx := -1, -1
	for i := 0; i < len(entity.Content)-1; i += 2 {
		k := entity.Content[i].Value
		v := entity.Content[i+1]
		switch k {
		case "info":
			if v.Kind == yaml.ScalarNode {
				info = v.Value
			}
			infoIdx = i
		case "status":
			if v.Kind == yaml.ScalarNode {
				status = v.Value
			}
			statusIdx = i
		case "description":
			hasDescription = true
		case "name":
			if v.Kind == yaml.ScalarNode {
				nameVal = v.Value
			}
		}
	}
	if info == "" && status == "" {
		// Nothing to migrate.
		return false
	}
	// Remove legacy keys from the entity map. Delete in reverse order
	// so the earlier index doesn't shift past the later one. Each key
	// occupies 2 content slots (key + value). This runs whether or not
	// description: already exists — a partial migration (description:
	// added but info:/status: not yet deleted) is a state that the
	// rejection trap would flag, so we finish the cleanup on every run.
	removeAt := func(i int) {
		if i < 0 || i+1 >= len(entity.Content) {
			return
		}
		entity.Content = append(entity.Content[:i], entity.Content[i+2:]...)
	}
	if infoIdx > statusIdx {
		removeAt(infoIdx)
		removeAt(statusIdx)
	} else {
		removeAt(statusIdx)
		removeAt(infoIdx)
	}
	if hasDescription {
		// description: already scaffolded by an earlier run. Legacy keys
		// have been cleaned up above; no need to add more.
		return true
	}

	feature := firstLineOf(info)
	if feature == "" {
		feature = nameVal
	}
	if feature == "" {
		feature = "TODO: entity description"
	}
	narrative := strings.TrimSpace(info)

	// Build the description node programmatically — keeps the output
	// stable across runs (no reliance on go-native map ordering).
	descNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	addMapEntry(descNode, "feature", scalarNode(feature))
	if narrative != "" {
		nNode := scalarNode(narrative)
		nNode.Style = yaml.LiteralStyle
		addMapEntry(descNode, "narrative", nNode)
	}
	if status != "" {
		tagsNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		tagsNode.Content = append(tagsNode.Content, scalarNode(status))
		addMapEntry(descNode, "tags", tagsNode)
	}

	// Skeleton scenario tagged @skeleton — marks the entity as
	// description-scaffolded-but-not-authored. `charly feature pending
	// --skeleton` enumerates these so authors can fill them in.
	scenarioNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addMapEntry(scenarioNode, "name", scalarNode("TODO: describe "+feature))
	skeletonTags := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	skeletonTags.Content = append(skeletonTags.Content, scalarNode("skeleton"))
	addMapEntry(scenarioNode, "tags", skeletonTags)
	addMapEntry(scenarioNode, "steps", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"})

	scenariosNode := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	scenariosNode.Content = append(scenariosNode.Content, scenarioNode)
	addMapEntry(descNode, "scenarios", scenariosNode)

	descKey := scalarNode("description")
	entity.Content = append(entity.Content, descKey, descNode)
	return true
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if before, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(before)
	}
	return s
}

func scalarNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func addMapEntry(m *yaml.Node, key string, val *yaml.Node) {
	m.Content = append(m.Content, scalarNode(key), val)
}
