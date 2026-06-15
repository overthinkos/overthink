package main

// cue_loader.go — the CUE decode path for the loader switch (Cutover 1). A
// root-shape charly.yml document is decoded into a UnifiedFile via CUE:
//
//  1. the loader ENVELOPE (import:/discover:) is decoded by yaml.v3 — those
//     fields drive the import/discover graph, which stays Go (ns_identity.go);
//  2. every ENTITY collection is canonicalized in place by NormalizeEntityNode
//     (run against the whole UnifiedFile type — it no-ops on import/discover,
//     whose wire shapes don't match their field structs);
//  3. import:/discover: are stripped from the node (their Go-only unmarshalers
//     can't CUE-decode) and the rest is CUE-ingested + Decoded into UnifiedFile;
//  4. the yaml.v3-decoded envelope is restored onto the result.
//
// This makes CUE the universal decode authority for the data model while the
// import/discover graph resolution remains Go. See memory cue-loader-switch-design.

import (
	"fmt"
	"reflect"

	cueyaml "cuelang.org/go/encoding/yaml"
	"gopkg.in/yaml.v3"
)

// decodeRootDocViaCUE decodes a root-shape document node into a UnifiedFile via
// the CUE path. It does NOT mutate the caller's node.
func decodeRootDocViaCUE(node *yaml.Node, srcLabel string) (*UnifiedFile, error) {
	// 1. envelope via yaml.v3 (keeps ImportList/ScanSpec unmarshalers).
	var env struct {
		Import   ImportList     `yaml:"import"`
		Discover DiscoverConfig `yaml:"discover"`
	}
	if err := node.Decode(&env); err != nil {
		return nil, fmt.Errorf("%s: decoding import/discover envelope: %w", srcLabel, err)
	}
	// 2. work on a copy (round-trip) so the input node is untouched.
	clone, err := cloneYAMLNode(node)
	if err != nil {
		return nil, fmt.Errorf("%s: clone: %w", srcLabel, err)
	}
	if err := NormalizeEntityNode(clone, reflect.TypeOf(UnifiedFile{})); err != nil {
		return nil, fmt.Errorf("%s: normalize entities: %w", srcLabel, err)
	}
	// 3. strip the Go-graph envelope keys, then CUE-ingest + Decode the rest.
	stripMapKeys(mappingRoot(clone), "import", "discover")
	b, err := yaml.Marshal(clone)
	if err != nil {
		return nil, fmt.Errorf("%s: re-marshal normalized doc: %w", srcLabel, err)
	}
	af, err := cueyaml.Extract(srcLabel, b)
	if err != nil {
		return nil, fmt.Errorf("%s: cue yaml ingest: %w", srcLabel, err)
	}
	cv := cueSchemaCtx.BuildFile(af)
	if cv.Err() != nil {
		return nil, fmt.Errorf("%s: cue build: %w", srcLabel, cv.Err())
	}
	var uf UnifiedFile
	if err := cv.Decode(&uf); err != nil {
		return nil, fmt.Errorf("%s: cue decode UnifiedFile: %w", srcLabel, err)
	}
	// 4. restore the Go-decoded envelope.
	uf.Import = env.Import
	uf.Discover = env.Discover
	return &uf, nil
}

// decodeEntityViaCUE normalizes a single entity node against its Go type and
// decodes it via CUE into out (a pointer). Does not mutate the input node. Used
// by the kind-keyed / candy / inline decode paths (the per-entity counterpart of
// decodeRootDocViaCUE).
func decodeEntityViaCUE(node *yaml.Node, t reflect.Type, out any, label string) error {
	return decodeAndValidateEntityCUE(node, t, out, label, "")
}

// decodeAndValidateEntityCUE is decodeEntityViaCUE plus, when validateKind is
// non-empty, CUE validation of the (normalized) entity against #<Kind> BEFORE
// decode — so the closed schema rejects unknown keys / bad types (restoring the
// rejection the deleted unmarshalers used to do). The node must BE the entity
// value (the candy body / a single kind entity), not a kind-keyed wrapper.
func decodeAndValidateEntityCUE(node *yaml.Node, t reflect.Type, out any, label, validateKind string) error {
	clone, err := cloneYAMLNode(node)
	if err != nil {
		return fmt.Errorf("%s: clone: %w", label, err)
	}
	// Validate the AUTHORED form (before normalize) against the authored #Kind
	// schema — it is closed (rejects unknown keys / typos) and already accepts
	// every shorthand the corpus uses, so no canonical-tightening is needed.
	if validateKind != "" {
		ba, err := yaml.Marshal(clone)
		if err != nil {
			return fmt.Errorf("%s: marshal (validate): %w", label, err)
		}
		afa, err := cueyaml.Extract(label, ba)
		if err != nil {
			return fmt.Errorf("%s: cue yaml ingest (validate): %w", label, err)
		}
		cva := cueSchemaCtx.BuildFile(afa)
		if cva.Err() != nil {
			return fmt.Errorf("%s: cue build (validate): %w", label, cva.Err())
		}
		if err := validateEntityClosedCUE(validateKind, label, cva); err != nil {
			return err
		}
	}
	// Normalize shorthand → canonical, then CUE-ingest + Decode into the struct.
	if err := NormalizeEntityNode(clone, t); err != nil {
		return fmt.Errorf("%s: normalize: %w", label, err)
	}
	b, err := yaml.Marshal(clone)
	if err != nil {
		return fmt.Errorf("%s: re-marshal: %w", label, err)
	}
	af, err := cueyaml.Extract(label, b)
	if err != nil {
		return fmt.Errorf("%s: cue yaml ingest: %w", label, err)
	}
	cv := cueSchemaCtx.BuildFile(af)
	if cv.Err() != nil {
		return fmt.Errorf("%s: cue build: %w", label, cv.Err())
	}
	if err := cv.Decode(out); err != nil {
		return fmt.Errorf("%s: cue decode: %w", label, err)
	}
	return nil
}

// cloneYAMLNode deep-copies a node by marshal+reparse (no cycles in raw input).
func cloneYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	b, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	var out yaml.Node
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// mappingRoot unwraps a document node to its top-level mapping node (or nil).
func mappingRoot(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// stripMapKeys removes the given top-level keys from a mapping node in place.
func stripMapKeys(m *yaml.Node, keys ...string) {
	if m == nil {
		return
	}
	drop := map[string]bool{}
	for _, k := range keys {
		drop[k] = true
	}
	kept := m.Content[:0:0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if drop[m.Content[i].Value] {
			continue
		}
		kept = append(kept, m.Content[i], m.Content[i+1])
	}
	m.Content = kept
}
