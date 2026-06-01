package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLayerSet_DescendsIntoLayerWrapper guards the Bug-1 fix: `ov layer set`
// edits layer.<dotpath> inside the kind-keyed `layer:` wrapper, never a stray
// top-level key. Without the fix, setting `version` appended a second
// top-level `version:`, which the loader rejects as ambiguous.
func TestLayerSet_DescendsIntoLayerWrapper(t *testing.T) {
	dir := t.TempDir()
	layerDir := filepath.Join(dir, "layers", "foo")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const start = "layer:\n  name: foo\n  version: 2026.1.1\n"
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(start), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if err := (&LayerSetCmd{Name: "foo", Path: "version", Value: "2026.2.2"}).Run(); err != nil {
		t.Fatalf("LayerSetCmd.Run: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(layerDir, "layer.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("re-parsing result: %v\n%s", err, out)
	}
	if _, hasTop := root["version"]; hasTop {
		t.Fatalf("stray top-level version: introduced (the bug):\n%s", out)
	}
	layer, ok := root["layer"].(map[string]any)
	if !ok {
		t.Fatalf("layer: wrapper missing or wrong type:\n%s", out)
	}
	if got := layer["version"]; got != "2026.2.2" {
		t.Fatalf("layer.version = %v, want 2026.2.2:\n%s", got, out)
	}

	// An already-qualified path must not be double-prefixed (layer.layer.name).
	if err := (&LayerSetCmd{Name: "foo", Path: "layer.name", Value: "bar"}).Run(); err != nil {
		t.Fatalf("LayerSetCmd.Run (layer.-prefixed): %v", err)
	}
	out2, err := os.ReadFile(filepath.Join(layerDir, "layer.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root = nil
	if err := yaml.Unmarshal(out2, &root); err != nil {
		t.Fatalf("re-parsing result 2: %v\n%s", err, out2)
	}
	layer, _ = root["layer"].(map[string]any)
	if got := layer["name"]; got != "bar" {
		t.Fatalf("layer.name = %v, want bar (layer.-prefixed path mishandled?):\n%s", got, out2)
	}
}
