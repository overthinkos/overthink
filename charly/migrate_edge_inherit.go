package main

// migrate_edge_inherit.go — EDGE-INHERIT cutover B: eliminate the `bundle:` kind;
// the substrate kind is the EDGE discriminator and the cross-ref becomes `from:`
// (same-kind inherit) / `image:` (the box a pod runs).
//
//	bundle:{box: I, …}      → pod:{image: I, …}
//	bundle:{vm: V, …}       → vm:{from: V, …}
//	bundle:{k8s: K, …}      → k8s:{from: K, …}
//	bundle:{local: T, …}    → local:{from: T, …}
//	bundle:{android: D, …}  → android:{from: D, …}
//	bundle:{…no cross-ref…} → group:{…}   (a targetless deploy group)
//	bundle:{vm_state: …}    → vm:{…}       (a per-host VM overlay, no authored cross-ref)
//
// Raises LatestSchemaVersion: the `bundle` kind + the box/pod/vm/k8s/local/android
// cross-ref fields are GONE from the schema, so an un-migrated config is rejected
// with a `Run: charly migrate` hint. Comment-preserving (renames key nodes in place),
// idempotent (a config already in substrate-kind form has no `bundle:` key). The
// per-host overlay rides along (migrateHostOverlayDoc) — R3 symmetry with the other
// node-form migrators.

import "gopkg.in/yaml.v3"

// bundleCrossRefTarget maps a `bundle:` cross-ref scalar key to the substrate kind
// it deploys to and the non-kind field the cross-ref becomes.
var bundleCrossRefTarget = map[string]struct{ kind, field string }{
	"box":     {"pod", "image"},
	"vm":      {"vm", "from"},
	"k8s":     {"k8s", "from"},
	"local":   {"local", "from"},
	"android": {"android", "from"},
}

// MigrateEdgeInherit converts every `bundle:` node to a substrate-kind-at-the-edge
// node across a project's candy/ + box/ dirs + root YAML, plus the per-host overlay.
func MigrateEdgeInherit(ctx *MigrateContext) (bool, error) {
	w, err := runDocMigration(ctx.Dir, ctx.DryRun, opUnifyCandidateFiles, edgeInheritDoc)
	if err != nil {
		return len(w) > 0, err
	}
	hostChanged, herr := migrateHostOverlayDoc(ctx, edgeInheritDoc)
	return len(w) > 0 || hostChanged, herr
}

// edgeInheritDoc rewrites every top-level entity whose discriminator is `bundle:`.
func edgeInheritDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		entityVal := root.Content[i+1]
		if entityVal != nil && entityVal.Kind == yaml.MappingNode && migrateBundleEntity(entityVal) {
			changed = true
		}
	}
	return changed
}

// migrateBundleEntity rewrites every `bundle:` discriminator in an entity value to
// the substrate kind at the edge, RECURSING into member / nested-entity child values
// (a deploy's nested:/peer: members carry their own `bundle:`). Returns whether
// anything changed.
func migrateBundleEntity(entityVal *yaml.Node) bool {
	if entityVal == nil || entityVal.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for i := 0; i+1 < len(entityVal.Content); i += 2 {
		key, val := entityVal.Content[i], entityVal.Content[i+1]
		if key.Value == "bundle" {
			key.Value = bundleTargetFromValue(val) // bundle → pod/vm/k8s/local/android/group
			changed = true
			continue // the bundle VALUE holds scalars only — no nested entity to recurse into
		}
		if val != nil && val.Kind == yaml.MappingNode && migrateBundleEntity(val) {
			changed = true
		}
	}
	return changed
}

// bundleTargetFromValue renames the cross-ref scalar in a bundle value (box→image,
// vm/k8s/local/android→from) and returns the substrate kind the bundle becomes: a
// cross-ref names the substrate, a vm_state-only per-host overlay is a vm, and
// anything else (no cross-ref) is a targetless deploy group.
func bundleTargetFromValue(bundleVal *yaml.Node) string {
	if bundleVal != nil && bundleVal.Kind == yaml.MappingNode {
		for j := 0; j+1 < len(bundleVal.Content); j += 2 {
			k := bundleVal.Content[j]
			if t, ok := bundleCrossRefTarget[k.Value]; ok {
				k.Value = t.field
				return t.kind
			}
		}
		if v, _ := mappingEntry(bundleVal, "vm_state"); v != nil {
			return "vm"
		}
	}
	return "group"
}

// mappingEntry returns the key+value nodes for key in a mapping, or (nil, nil).
func mappingEntry(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}
