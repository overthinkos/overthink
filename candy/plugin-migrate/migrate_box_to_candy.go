package migrate

// migrate_box_to_candy.go — EDGE-INHERIT cutover D: the `box:` KIND merges INTO
// `candy:`. A candy carrying a `base:` (external base) or `from:` (builder ref) marker
// is a full IMAGE (the former box:), routed to uf.Box by the loader; a candy without
// those markers is a LAYER fragment. The migration is the keyword rename `box:`→`candy:`
// at every entity — the base⊻from marker the box already carries keeps it an image.
//
// NO collision renames: a `box/redis` (image, has base:) and a `candy/redis` (layer)
// live in SEPARATE files and route to distinct maps (uf.Box vs uf.Candy by marker), so
// both become `candy: redis` without clashing (the standing cross-kind-reuse rule).
//
// Raises LatestSchemaVersion: the `box` kind is GONE from the schema, so an un-migrated
// `box:` config is rejected with a `Run: charly migrate` hint. Comment-preserving,
// idempotent (a config already in candy-only form has no `box:` key). The per-host
// overlay rides along (R3 symmetry with the other node-form migrators).

import "gopkg.in/yaml.v3"

// MigrateBoxToCandy renames every `box:` discriminator to `candy:` across a project's
// candy/ + box/ dirs + root YAML, plus the per-host overlay.
func MigrateBoxToCandy(ctx *MigrateContext) (bool, error) {
	w, err := runDocMigration(ctx.Dir, ctx.DryRun, opUnifyCandidateFiles, boxToCandyDoc)
	if err != nil {
		return len(w) > 0, err
	}
	hostChanged, herr := migrateHostOverlayDoc(ctx, boxToCandyDoc)
	return len(w) > 0 || hostChanged, herr
}

// boxToCandyDoc renames every top-level entity's `box:` discriminator to `candy:`.
func boxToCandyDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		entityVal := root.Content[i+1]
		if entityVal != nil && entityVal.Kind == yaml.MappingNode && renameBoxDisc(entityVal) {
			changed = true
		}
	}
	return changed
}

// renameBoxDisc renames a `box:` KIND DISCRIMINATOR key to `candy:`, RECURSING into
// nested member / sub-entity child values. A box: discriminator's value is the image
// BODY — a MAPPING (`box: {base: …}`); a box: FIELD (the image cross-ref on #K8s /
// #Android / #Pod / …) is a SCALAR (`box: android-emulator`). Renaming is therefore
// gated on a MAPPING value, so a box: cross-ref field is never touched.
func renameBoxDisc(entityVal *yaml.Node) bool {
	if entityVal == nil || entityVal.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for i := 0; i+1 < len(entityVal.Content); i += 2 {
		key, val := entityVal.Content[i], entityVal.Content[i+1]
		if val == nil || val.Kind != yaml.MappingNode {
			continue // scalar/sequence value — e.g. a box: cross-ref FIELD; never the disc
		}
		if key.Value == "box" {
			key.Value = "candy" // the box KIND discriminator (its value is the image body)
			changed = true
			continue
		}
		if renameBoxDisc(val) {
			changed = true
		}
	}
	return changed
}
