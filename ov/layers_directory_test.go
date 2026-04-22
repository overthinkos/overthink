package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLayerSourceDir(t *testing.T) {
	cases := []struct {
		name, path, directory, want string
	}{
		{"empty defaults to path", "/repo/layers/chrome", "", "/repo/layers/chrome"},
		{"dot defaults to path", "/repo/layers/chrome", ".", "/repo/layers/chrome"},
		{"relative joins onto path", "/repo/layers/chrome", "configs", "/repo/layers/chrome/configs"},
		{"sibling via dot-dot", "/repo/layers/chrome", "../shared", "/repo/layers/shared"},
		{"absolute used as-is", "/repo/layers/chrome", "/opt/custom", "/opt/custom"},
		{"cleans redundant parts", "/repo/layers/chrome", "./configs/./", "/repo/layers/chrome/configs"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveLayerSourceDir(c.path, c.directory)
			if got != c.want {
				t.Errorf("resolveLayerSourceDir(%q, %q) = %q, want %q", c.path, c.directory, got, c.want)
			}
		})
	}
}

// TestScanLayerWithDirectoryRedirect verifies that when layer.yml declares
// `directory: ../configs`, install-file detection (HasPixiToml etc.) probes the
// redirected directory rather than the layer.yml's own directory.
func TestScanLayerWithDirectoryRedirect(t *testing.T) {
	root := t.TempDir()
	layerDir := filepath.Join(root, "layers", "myapp")
	configDir := filepath.Join(root, "configs", "myapp")

	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Config files live in the sibling dir, NOT in layers/myapp
	if err := os.WriteFile(filepath.Join(configDir, "pixi.toml"), []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// layer.yml points at it
	yml := `layer:
  name: myapp
  version: "1"
  directory: ../../configs/myapp
`
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(yml), 0644); err != nil {
		t.Fatal(err)
	}

	layer, err := scanLayer(layerDir, "myapp")
	if err != nil {
		t.Fatalf("scanLayer: %v", err)
	}

	if layer.Path != layerDir {
		t.Errorf("Path = %q, want %q", layer.Path, layerDir)
	}
	wantSourceDir := filepath.Clean(configDir)
	if layer.SourceDir != wantSourceDir {
		t.Errorf("SourceDir = %q, want %q", layer.SourceDir, wantSourceDir)
	}
	if !layer.HasPixiToml {
		t.Errorf("HasPixiToml = false, want true (pixi.toml lives in redirected SourceDir)")
	}
}

// TestScanLayerNoDirectoryDefaults confirms that existing layers (without
// `directory:`) behave exactly as before — SourceDir matches Path.
func TestScanLayerNoDirectoryDefaults(t *testing.T) {
	root := t.TempDir()
	layerDir := filepath.Join(root, "layers", "classic")
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "pixi.toml"), []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte("layer:\n  name: classic\n  version: \"1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	layer, err := scanLayer(layerDir, "classic")
	if err != nil {
		t.Fatalf("scanLayer: %v", err)
	}
	if layer.SourceDir != layerDir {
		t.Errorf("SourceDir = %q, want %q (no directory: override ⇒ SourceDir == Path)", layer.SourceDir, layerDir)
	}
	if !layer.HasPixiToml {
		t.Errorf("HasPixiToml = false, want true")
	}
}
