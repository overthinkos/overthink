package main

import (
	"reflect"
	"testing"
)

func TestScanLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	expectedLayers := []string{"pixi", "python", "nodejs", "cargo-tool"}
	for _, name := range expectedLayers {
		if _, ok := layers[name]; !ok {
			t.Errorf("missing layer %q", name)
		}
	}
}

func TestLayerPixi(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi == nil {
		t.Fatal("pixi layer not found")
	}

	if !pixi.HasRootYml {
		t.Error("pixi should have root.yml")
	}
	if !pixi.HasUserYml {
		t.Error("pixi should have user.yml")
	}
	if pixi.HasRpmList {
		t.Error("pixi should not have rpm.list")
	}
	if pixi.HasPixiToml {
		t.Error("pixi should not have pixi.toml")
	}
	if len(pixi.Depends) != 0 {
		t.Errorf("pixi should have no depends, got %v", pixi.Depends)
	}
}

func TestLayerPython(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python layer not found")
	}

	if !python.HasPixiToml {
		t.Error("python should have pixi.toml")
	}
	if !reflect.DeepEqual(python.Depends, []string{"pixi"}) {
		t.Errorf("python.Depends = %v, want [pixi]", python.Depends)
	}
}

func TestLayerNodejs(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	nodejs := layers["nodejs"]
	if nodejs == nil {
		t.Fatal("nodejs layer not found")
	}

	if !nodejs.HasRpmList {
		t.Error("nodejs should have rpm.list")
	}
	if !nodejs.HasDebList {
		t.Error("nodejs should have deb.list")
	}

	// Test package reading
	rpms, err := nodejs.RpmPackages()
	if err != nil {
		t.Fatalf("RpmPackages() error = %v", err)
	}
	if !reflect.DeepEqual(rpms, []string{"nodejs", "npm"}) {
		t.Errorf("RpmPackages() = %v, want [nodejs npm]", rpms)
	}

	debs, err := nodejs.DebPackages()
	if err != nil {
		t.Fatalf("DebPackages() error = %v", err)
	}
	if !reflect.DeepEqual(debs, []string{"nodejs", "npm"}) {
		t.Errorf("DebPackages() = %v, want [nodejs npm]", debs)
	}
}

func TestLayerCargoTool(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	cargoTool := layers["cargo-tool"]
	if cargoTool == nil {
		t.Fatal("cargo-tool layer not found")
	}

	if !cargoTool.HasCargoToml {
		t.Error("cargo-tool should have Cargo.toml")
	}
	if !cargoTool.HasSrcDir {
		t.Error("cargo-tool should have src/ directory")
	}
}

func TestHasInstallFiles(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	for name, layer := range layers {
		if !layer.HasInstallFiles() {
			t.Errorf("layer %q should have install files", name)
		}
	}
}

func TestLayerNames(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	names := LayerNames(layers)
	if len(names) != 4 {
		t.Errorf("LayerNames() returned %d names, want 4", len(names))
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("LayerNames() not sorted: %v", names)
			break
		}
	}
}

func TestReadLineFile(t *testing.T) {
	// Test the function with a known file
	lines, err := readLineFile("testdata/layers/nodejs/rpm.list")
	if err != nil {
		t.Fatalf("readLineFile() error = %v", err)
	}

	expected := []string{"nodejs", "npm"}
	if !reflect.DeepEqual(lines, expected) {
		t.Errorf("readLineFile() = %v, want %v", lines, expected)
	}
}
