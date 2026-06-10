package main

// migrate_discover_flatten.go — `charly migrate` step.
//
// The kind-keyed `discover:` block (`discover: {candy: [...], box: [...]}`) is
// replaced by a FLAT, generic scan-spec list (`discover: [{path, recursive,
// manifest}]`). YAML files are generic kind-containers routed by SHAPE, so
// discovery no longer keys on a per-kind sub-map; each spec carries an explicit
// manifest filename (the old per-kind convention: candy→candy.yml, box→box.yml,
// …), which is itself configurable in overthink.yml. Comment-preserving via the
// yaml.v3 node API; idempotent (a discover that is already a flat sequence, or
// absent, is a no-op). Per-file backups follow the <file>.bak.<unix-ts>
// convention.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// discoverKindManifest maps a legacy kind-keyed discover sub-key to its
// conventional manifest filename (the per-kind default the flat form makes
// explicit + overridable).
func discoverKindManifest(kind string) string {
	return kind + ".yml"
}

// MigrateDiscoverFlatten rewrites overthink.yml's kind-keyed `discover:` map into
// the flat generic scan-spec list.
func MigrateDiscoverFlatten(dir string, dryRun bool) ([]string, error) {
	path := filepath.Join(dir, "overthink.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil // missing entry point — nothing to migrate
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil // not parseable as a single doc — leave untouched
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	root := doc.Content[0]
	var discVal *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "discover" {
			discVal = root.Content[i+1]
			break
		}
	}
	// No discover, or already flat (sequence) → idempotent no-op.
	if discVal == nil || discVal.Kind != yaml.MappingNode {
		return nil, nil
	}
	flat := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := 0; i+1 < len(discVal.Content); i += 2 {
		kind := discVal.Content[i].Value
		specs := discVal.Content[i+1]
		if specs.Kind != yaml.SequenceNode {
			continue
		}
		manifest := discoverKindManifest(kind)
		for _, spec := range specs.Content {
			flat.Content = append(flat.Content, flattenScanSpecNode(spec, manifest))
		}
	}
	// Carry any head/line comment on the discover value over to the new list.
	flat.HeadComment = discVal.HeadComment
	flat.LineComment = discVal.LineComment
	flat.FootComment = discVal.FootComment
	*discVal = *flat

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	_ = enc.Close()
	if dryRun {
		return []string{path}, nil
	}
	bak := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	_ = os.WriteFile(bak, data, 0644)
	if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// flattenScanSpecNode converts one legacy scan-spec node (scalar shorthand or a
// {path, recursive} mapping) into the flat {path, recursive, manifest} mapping.
func flattenScanSpecNode(spec *yaml.Node, manifest string) *yaml.Node {
	scalar := func(v, tag string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v}
	}
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	switch spec.Kind {
	case yaml.ScalarNode:
		// String shorthand "candy" → {path, recursive: true, manifest}.
		m.HeadComment = spec.HeadComment
		m.Content = append(m.Content,
			scalar("path", "!!str"), scalar(spec.Value, "!!str"),
			scalar("recursive", "!!str"), scalar("true", "!!bool"),
			scalar("manifest", "!!str"), scalar(manifest, "!!str"),
		)
	case yaml.MappingNode:
		m.HeadComment = spec.HeadComment
		m.Content = append(m.Content, spec.Content...)
		hasManifest := false
		for i := 0; i+1 < len(spec.Content); i += 2 {
			if spec.Content[i].Value == "manifest" {
				hasManifest = true
			}
		}
		if !hasManifest {
			m.Content = append(m.Content, scalar("manifest", "!!str"), scalar(manifest, "!!str"))
		}
	default:
		return spec
	}
	return m
}
