package main

import "testing"

// TestComputeEffectiveVersions covers the image-version derivation that feeds
// the content-stable ai.opencharly.version label: a dedicated version: wins;
// otherwise the highest candy version across the chain; a candyless image
// recurses to its internal base; a candyless external-base image with no version
// is a HARD ERROR (no build-timestamp fallback — the per-kind versioning cutover
// dropped all backward compat).
func TestComputeEffectiveVersions(t *testing.T) {
	layers := map[string]*Candy{
		"a": {Name: "a", Version: "2026.100.0000"},
		"b": {Name: "b", Version: "2026.200.0000"}, // newest candy
	}
	images := map[string]*ResolvedBox{
		// dedicated version wins over the (newer) candy versions.
		"dedicated": {Name: "dedicated", Version: "2026.050.0000", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// no dedicated version -> highest candy version (b = 2026.200.0000).
		"derived": {Name: "derived", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
		// bare base: candyless + external + dedicated version (what `charly migrate` backfills).
		"barebase": {Name: "barebase", Version: "2026.300.0000", IsExternalBase: true, Base: "quay.io/x:1"},
		// candyless on an INTERNAL base -> recurse to the base's effective version.
		"passthrough": {Name: "passthrough", Base: "barebase"},
	}
	g := &Generator{Boxes: images, Candies: layers}
	if err := g.computeEffectiveVersions(); err != nil {
		t.Fatalf("computeEffectiveVersions: %v", err)
	}

	cases := map[string]string{
		"dedicated":   "2026.050.0000", // dedicated wins
		"derived":     "2026.200.0000", // highest candy version
		"barebase":    "2026.300.0000", // dedicated bare-base version
		"passthrough": "2026.300.0000", // recursed to barebase
	}
	for name, want := range cases {
		if got := images[name].EffectiveVersion; got != want {
			t.Errorf("%s: EffectiveVersion = %q, want %q", name, got, want)
		}
	}

	// A candy bump propagates to a deriving image's identity.
	layers["b"].Version = "2026.400.0000"
	g2 := &Generator{Boxes: map[string]*ResolvedBox{
		"derived": {Name: "derived", Candy: []string{"a", "b"}, IsExternalBase: true, Base: "quay.io/x:1"},
	}, Candies: layers}
	if err := g2.computeEffectiveVersions(); err != nil {
		t.Fatal(err)
	}
	if got := g2.Boxes["derived"].EffectiveVersion; got != "2026.400.0000" {
		t.Errorf("after candy bump: EffectiveVersion = %q, want 2026.400.0000", got)
	}

	// Hard error: candyless external-base image with no version (no fallback).
	gErr := &Generator{
		Boxes:   map[string]*ResolvedBox{"orphan": {Name: "orphan", IsExternalBase: true, Base: "quay.io/x:1"}},
		Candies: map[string]*Candy{},
	}
	if err := gErr.computeEffectiveVersions(); err == nil {
		t.Error("expected a hard error for a candyless external-base image with no version:")
	}
}
