package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/overthinkos/overthink/charly/spec"
)

// TestLoadUnified_PackageGroupPluginKind proves the FIRST kind→plugin extraction
// end-to-end through the REAL loader: a `package-group:` node (the former core
// builtin kind, now a dedicated plugin unit) is
//
//	(1) RECOGNIZED by the loader as a registered non-core ClassKind discriminator
//	    (classifyDisc → providerRegistry.ResolveKind) and PASSES the #NodeDoc gate
//	    (package-group was removed from the closed #Node arms + _reservedNode);
//	(2) VALIDATED at load time against the plugin's served #PackageGroupInput schema
//	    (runPluginKind → loadBuiltinPluginUnits gate → validateAuthoredPluginInput);
//	(3) DECODED out-of-the-closed-core via Invoke(OpLoad) into
//	    uf.PluginKinds["package-group"] — NOT a typed core map (the former core map
//	    was removed);
//	(4) CARRIED through mergeUnified (the mergePluginKindsMap merge) so the full
//	    loader path keeps it instead of dropping the per-document `sub`.
//
// The values round-trip: the canonical entity JSON decodes back into spec.Group.
func TestLoadUnified_PackageGroupPluginKind(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mygroup:
  package-group:
    description: a test netinstall group
    critical: true
    hidden: false
    source: netinstall.yaml
  mygroup-require:
    require: [base, core]
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified package-group plugin kind: %v", err)
	}

	entities := uf.PluginKinds["package-group"]
	if len(entities) != 1 {
		t.Fatalf("expected 1 package-group entity in uf.PluginKinds, got %d (%v)", len(entities), uf.PluginKinds)
	}

	// The plugin returns canonical entity JSON; a consumer reads it back into spec.Group.
	var g spec.Group
	if err := json.Unmarshal(entities[0], &g); err != nil {
		t.Fatalf("decode plugin-kind entity JSON into spec.Group: %v", err)
	}
	if g.Description != "a test netinstall group" {
		t.Errorf("description = %q, want %q", g.Description, "a test netinstall group")
	}
	if !g.Critical {
		t.Errorf("critical = %v, want true", g.Critical)
	}
	if g.Hidden {
		t.Errorf("hidden = %v, want false", g.Hidden)
	}
	if g.Source != "netinstall.yaml" {
		t.Errorf("source = %q, want %q", g.Source, "netinstall.yaml")
	}
	// The non-scalar `require` field, authored as a folded child node, round-trips.
	if len(g.Require) != 2 || g.Require[0] != "base" || g.Require[1] != "core" {
		t.Errorf("require = %v, want [base core]", g.Require)
	}
}

// TestLoadUnified_PackageGroupRejectsBadInput proves the load-time schema gate:
// the plugin's served #PackageGroupInput validates the authored entity body, so a
// type-violating field is a hard load error (not a silent drop).
func TestLoadUnified_PackageGroupRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
mygroup:
  package-group:
    critical: "not-a-bool"
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadUnified(dir); err == nil {
		t.Fatal("expected a load error for a non-bool critical:, got nil")
	}
}
