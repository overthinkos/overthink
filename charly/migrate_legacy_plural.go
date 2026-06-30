package main

// migrate_legacy_plural.go — the load-time rejection gate for legacy plural
// schema keys. This helper MUST stay in core: it is called by the unified loader
// (unified.go) and the layer loader (layers.go) on EVERY parse, long before any
// plugin connects. The field-singular MIGRATOR that rewrites these keys lives in
// candy/plugin-migrate; both share the ONE canonical key map
// (kit.PluralToSingularYAMLKeys) across the module boundary (R3 — the map cannot
// be duplicated).

import (
	"bytes"
	"fmt"

	"github.com/overthinkos/overthink/charly/plugin/kit"
	"gopkg.in/yaml.v3"
)

// RejectLegacyPluralKeys is the single rejection point used by every YAML loader.
// Walks the top-level mapping of the document and returns an error if any legacy
// plural field name is present, with a remediation hint pointing at
// `charly migrate`. (R3 no-duplication: this loader gate and the field-singular
// migrator share kit.PluralToSingularYAMLKeys as the ONE source of truth.)
//
// Returns nil for documents that are already singular OR that can't be parsed as
// a top-level mapping (caller will surface the parse error itself with full context).
func RejectLegacyPluralKeys(path string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // let the caller's own decode produce the parse error
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i].Value
		if singular, ok := kit.PluralToSingularYAMLKeys[k]; ok {
			return fmt.Errorf("%s: legacy plural field %q rejected; the project moved to singular field names. Run `charly migrate` to rewrite this file (key %q → %q)", path, k, k, singular)
		}
	}
	return nil
}
