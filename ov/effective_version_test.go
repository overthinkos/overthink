package main

import "testing"

// TestComputeEffectiveVersions covers the image-version derivation that feeds
// the content-stable org.overthinkos.version label: a dedicated version: wins;
// otherwise the highest layer version across the chain; a layerless image
// recurses to its internal base; a layerless external-base image with no version
// is a HARD ERROR (no build-timestamp fallback — the per-kind versioning cutover
// dropped all backward compat).
func TestComputeEffectiveVersions(t *testing.T) {
	layers := map[string]*Layer{
		"a": {Name: "a", Version: "2026.100.0"},
		"b": {Name: "b", Version: "2026.200.0"}, // newest layer
	}
	images := map[string]*ResolvedBox{
		// dedicated version wins over the (newer) layer versions.
		"dedicated": {Name: "dedicated", Version: "2026.50.0", Layer: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// no dedicated version -> highest layer version (b = 2026.200.0).
		"derived": {Name: "derived", Layer: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// bare base: layerless + external + dedicated version (what `ov migrate` backfills).
		"barebase": {Name: "barebase", Version: "2026.300.0", IsExternalBase: true, Base: "quay.io/x:1"},
		// layerless on an INTERNAL base -> recurse to the base's effective version.
		"passthrough": {Name: "passthrough", Base: "barebase"},
	}
	g := &Generator{Images: images, Layers: layers}
	if err := g.computeEffectiveVersions(); err != nil {
		t.Fatalf("computeEffectiveVersions: %v", err)
	}

	cases := map[string]string{
		"dedicated":   "2026.50.0",  // dedicated wins
		"derived":     "2026.200.0", // highest layer version
		"barebase":    "2026.300.0", // dedicated bare-base version
		"passthrough": "2026.300.0", // recursed to barebase
	}
	for name, want := range cases {
		if got := images[name].EffectiveVersion; got != want {
			t.Errorf("%s: EffectiveVersion = %q, want %q", name, got, want)
		}
	}

	// A layer bump propagates to a deriving image's identity.
	layers["b"].Version = "2026.400.0"
	g2 := &Generator{Images: map[string]*ResolvedBox{
		"derived": {Name: "derived", Layer: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
	}, Layers: layers}
	if err := g2.computeEffectiveVersions(); err != nil {
		t.Fatal(err)
	}
	if got := g2.Images["derived"].EffectiveVersion; got != "2026.400.0" {
		t.Errorf("after layer bump: EffectiveVersion = %q, want 2026.400.0", got)
	}

	// Hard error: layerless external-base image with no version (no fallback).
	gErr := &Generator{
		Images: map[string]*ResolvedBox{"orphan": {Name: "orphan", IsExternalBase: true, Base: "quay.io/x:1"}},
		Layers: map[string]*Layer{},
	}
	if err := gErr.computeEffectiveVersions(); err == nil {
		t.Error("expected a hard error for a layerless external-base image with no version:")
	}
}
