package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLockFile(t *testing.T) {
	dir := t.TempDir()

	// No layers.lock -- should return nil
	lf, err := ParseLockFile(dir)
	if err != nil {
		t.Fatalf("ParseLockFile() error = %v", err)
	}
	if lf != nil {
		t.Fatal("expected nil for missing layers.lock")
	}

	// Write a layers.lock
	content := `modules:
  - module: github.com/overthinkos/ml-layers
    version: v1.0.0
    commit: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
    hash: sha256:abc123
    layers: [cuda, python-ml]
`
	os.WriteFile(filepath.Join(dir, "layers.lock"), []byte(content), 0644)

	lf, err = ParseLockFile(dir)
	if err != nil {
		t.Fatalf("ParseLockFile() error = %v", err)
	}
	if len(lf.Modules) != 1 {
		t.Fatalf("len(modules) = %d, want 1", len(lf.Modules))
	}
	m := lf.Modules[0]
	if m.Module != "github.com/overthinkos/ml-layers" {
		t.Errorf("module = %q", m.Module)
	}
	if m.Commit != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" {
		t.Errorf("commit = %q", m.Commit)
	}
	if len(m.Layers) != 2 || m.Layers[0] != "cuda" || m.Layers[1] != "python-ml" {
		t.Errorf("layers = %v", m.Layers)
	}
}

func TestWriteAndParseLockFile(t *testing.T) {
	dir := t.TempDir()

	lf := &LockFile{
		Modules: []LockModule{
			{Module: "github.com/test/dep", Version: "v1.0.0", Commit: "abc123", Hash: "sha256:xyz", Layers: []string{"layer1"}},
		},
	}

	if err := WriteLockFile(dir, lf); err != nil {
		t.Fatalf("WriteLockFile() error = %v", err)
	}

	// Verify header
	data, _ := os.ReadFile(filepath.Join(dir, "layers.lock"))
	header := "# layers.lock (generated -- do not edit)\n"
	if len(data) < len(header) || string(data[:len(header)]) != header {
		preview := string(data)
		if len(preview) > 60 {
			preview = preview[:60]
		}
		t.Errorf("missing header in lock file, starts with: %q", preview)
	}

	parsed, err := ParseLockFile(dir)
	if err != nil {
		t.Fatalf("ParseLockFile() error = %v", err)
	}
	if len(parsed.Modules) != 1 || parsed.Modules[0].Module != "github.com/test/dep" {
		t.Errorf("unexpected modules: %+v", parsed.Modules)
	}
}

func TestStripVersion(t *testing.T) {
	tests := []struct {
		ref     string
		wantRef string
		wantVer string
	}{
		{"github.com/org/repo/layer@v1.0.0", "github.com/org/repo/layer", "v1.0.0"},
		{"github.com/org/repo/layer@main", "github.com/org/repo/layer", "main"},
		{"github.com/org/repo/layer", "github.com/org/repo/layer", ""},
		{"pixi", "pixi", ""},
		{"module@v2", "module", "v2"},
	}

	for _, tt := range tests {
		gotRef, gotVer := StripVersion(tt.ref)
		if gotRef != tt.wantRef || gotVer != tt.wantVer {
			t.Errorf("StripVersion(%q) = (%q, %q), want (%q, %q)", tt.ref, gotRef, gotVer, tt.wantRef, tt.wantVer)
		}
	}
}

func TestParseRemoteRef(t *testing.T) {
	tests := []struct {
		ref        string
		wantModule string
		wantName   string
		wantVer    string
	}{
		{"github.com/org/repo/layer@v1.0.0", "github.com/org/repo", "layer", "v1.0.0"},
		{"github.com/org/repo/image@main", "github.com/org/repo", "image", "main"},
		{"github.com/org/repo/name", "github.com/org/repo", "name", ""},
		{"pixi", "", "pixi", ""},
	}

	for _, tt := range tests {
		got := ParseRemoteRef(tt.ref)
		if got.ModulePath != tt.wantModule || got.Name != tt.wantName || got.Version != tt.wantVer {
			t.Errorf("ParseRemoteRef(%q) = {Module: %q, Name: %q, Version: %q}, want {%q, %q, %q}",
				tt.ref, got.ModulePath, got.Name, got.Version, tt.wantModule, tt.wantName, tt.wantVer)
		}
		if got.Raw != tt.ref {
			t.Errorf("ParseRemoteRef(%q).Raw = %q", tt.ref, got.Raw)
		}
	}
}

func TestIsRemoteLayerRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"pixi", false},
		{"my-layer", false},
		{"github.com/org/repo/layer", true},
		{"github.com/overthinkos/ml-layers/cuda", true},
		{"gitlab.com/org/repo/layer", true},
		{"org/repo/layer", false}, // only 2 slashes
		{"github.com/org/repo/layer@v1.0.0", true},
		{"github.com/org/repo/layer@main", true},
	}

	for _, tt := range tests {
		got := IsRemoteLayerRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteLayerRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestIsRemoteImageRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ollama", false},
		{"github.com/org/repo/image", true},
		{"github.com/org/repo/image@v1.0.0", true},
	}

	for _, tt := range tests {
		got := IsRemoteImageRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteImageRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestSplitRemoteLayerRef(t *testing.T) {
	tests := []struct {
		ref        string
		wantModule string
		wantLayer  string
	}{
		{"github.com/overthinkos/ml-layers/cuda", "github.com/overthinkos/ml-layers", "cuda"},
		{"github.com/myorg/service-layers/my-service", "github.com/myorg/service-layers", "my-service"},
		{"pixi", "", "pixi"},
		{"github.com/org/repo/layer@v1.0.0", "github.com/org/repo", "layer"},
	}

	for _, tt := range tests {
		gotModule, gotLayer := SplitRemoteLayerRef(tt.ref)
		if gotModule != tt.wantModule || gotLayer != tt.wantLayer {
			t.Errorf("SplitRemoteLayerRef(%q) = (%q, %q), want (%q, %q)", tt.ref, gotModule, gotLayer, tt.wantModule, tt.wantLayer)
		}
	}
}

func TestLockFileFindModule(t *testing.T) {
	lf := &LockFile{
		Modules: []LockModule{
			{Module: "github.com/test/dep", Version: "v1.0.0", Commit: "abc"},
		},
	}

	if lm := lf.FindLockModule("github.com/test/dep"); lm == nil {
		t.Error("expected to find lock module")
	} else if lm.Commit != "abc" {
		t.Errorf("commit = %q, want %q", lm.Commit, "abc")
	}

	if lm := lf.FindLockModule("github.com/test/other"); lm != nil {
		t.Error("expected nil for unknown module")
	}
}

func TestScanModuleLayers(t *testing.T) {
	// Create a fake module directory
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "cuda"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "python-ml"), 0755)

	// Create minimal layer.yml files
	os.WriteFile(filepath.Join(layersDir, "cuda", "layer.yml"), []byte("rpm:\n  packages:\n    - cuda-toolkit\n"), 0644)
	os.WriteFile(filepath.Join(layersDir, "python-ml", "layer.yml"), []byte("depends:\n  - cuda\n"), 0644)
	os.WriteFile(filepath.Join(layersDir, "python-ml", "pixi.toml"), []byte("[project]\nname = \"python-ml\"\n"), 0644)

	layers, err := ScanModuleLayers(dir, "github.com/overthinkos/ml-layers")
	if err != nil {
		t.Fatalf("ScanModuleLayers() error = %v", err)
	}

	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}

	cuda, ok := layers["github.com/overthinkos/ml-layers/cuda"]
	if !ok {
		t.Fatal("cuda layer not found")
	}
	if !cuda.Remote {
		t.Error("cuda should be remote")
	}
	if cuda.ModulePath != "github.com/overthinkos/ml-layers" {
		t.Errorf("cuda.ModulePath = %q", cuda.ModulePath)
	}
	if cuda.Name != "cuda" {
		t.Errorf("cuda.Name = %q, want %q", cuda.Name, "cuda")
	}

	pyml := layers["github.com/overthinkos/ml-layers/python-ml"]
	if !pyml.HasPixiToml {
		t.Error("python-ml should have pixi.toml")
	}
	if len(pyml.Depends) != 1 || pyml.Depends[0] != "cuda" {
		t.Errorf("python-ml.Depends = %v", pyml.Depends)
	}
}

func TestScanAllLayersNoRemote(t *testing.T) {
	// With no @version refs, ScanAllLayers should behave like ScanLayers
	layers, err := ScanAllLayers("testdata")
	if err != nil {
		t.Fatalf("ScanAllLayers() error = %v", err)
	}

	localLayers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	if len(layers) != len(localLayers) {
		t.Errorf("len(layers) = %d, want %d", len(layers), len(localLayers))
	}
}

func TestComputeModuleHash(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers", "test")
	os.MkdirAll(layersDir, 0755)
	os.WriteFile(filepath.Join(layersDir, "layer.yml"), []byte("rpm:\n  packages:\n    - vim\n"), 0644)

	hash1, err := ComputeModuleHash(dir)
	if err != nil {
		t.Fatalf("ComputeModuleHash() error = %v", err)
	}
	if hash1 == "" {
		t.Fatal("hash should not be empty")
	}
	if len(hash1) < 10 {
		t.Errorf("hash too short: %q", hash1)
	}
	if hash1[:7] != "sha256:" {
		t.Errorf("hash should start with sha256:, got %q", hash1[:7])
	}

	// Same content should produce same hash
	hash2, err := ComputeModuleHash(dir)
	if err != nil {
		t.Fatalf("ComputeModuleHash() error = %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("hashes differ for same content: %q != %q", hash1, hash2)
	}

	// Different content should produce different hash
	os.WriteFile(filepath.Join(layersDir, "layer.yml"), []byte("rpm:\n  packages:\n    - emacs\n"), 0644)
	hash3, err := ComputeModuleHash(dir)
	if err != nil {
		t.Fatalf("ComputeModuleHash() error = %v", err)
	}
	if hash1 == hash3 {
		t.Error("hashes should differ for different content")
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abc123", true},
		{"ABC123", true},
		{"deadbeef", true},
		{"", false},
		{"xyz", false},
		{"abc 123", false},
	}

	for _, tt := range tests {
		got := isHex(tt.s)
		if got != tt.want {
			t.Errorf("isHex(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestGitRemoteToModulePath(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"https://github.com/overthinkos/overthink.git", "github.com/overthinkos/overthink"},
		{"git@github.com:overthinkos/overthink.git", "github.com/overthinkos/overthink"},
		{"https://gitlab.com/org/repo", "gitlab.com/org/repo"},
	}

	for _, tt := range tests {
		got := gitRemoteToModulePath(tt.remote)
		if got != tt.want {
			t.Errorf("gitRemoteToModulePath(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}

func TestModuleGitURL(t *testing.T) {
	got := ModuleGitURL("github.com/overthinkos/ml-layers")
	want := "https://github.com/overthinkos/ml-layers.git"
	if got != want {
		t.Errorf("ModuleGitURL() = %q, want %q", got, want)
	}
}

func TestCollectRequiredModules(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{
					"pixi",
					"github.com/overthinkos/ml-layers/cuda",
					"github.com/myorg/service-layers/my-service",
					"github.com/overthinkos/ml-layers/python@v1.0.0",
				},
			},
		},
	}

	modules := CollectRequiredModules(cfg)
	if len(modules) != 2 {
		t.Fatalf("len(modules) = %d, want 2", len(modules))
	}
	if !modules["github.com/overthinkos/ml-layers"] {
		t.Error("expected github.com/overthinkos/ml-layers")
	}
	if !modules["github.com/myorg/service-layers"] {
		t.Error("expected github.com/myorg/service-layers")
	}
}

func TestCollectRequiredModulesVersioned(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{
					"pixi",
					"github.com/overthinkos/ml-layers/cuda@v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", Depends: []string{}},
		"my-layer": {Name: "my-layer", Depends: []string{
			"github.com/myorg/service-layers/svc@v2.0.0",
		}},
	}

	modules, err := CollectRequiredModulesVersioned(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRequiredModulesVersioned() error = %v", err)
	}
	if len(modules) != 2 {
		t.Fatalf("len(modules) = %d, want 2", len(modules))
	}
	if modules["github.com/overthinkos/ml-layers"] != "v1.0.0" {
		t.Errorf("ml-layers version = %q, want %q", modules["github.com/overthinkos/ml-layers"], "v1.0.0")
	}
	if modules["github.com/myorg/service-layers"] != "v2.0.0" {
		t.Errorf("service-layers version = %q, want %q", modules["github.com/myorg/service-layers"], "v2.0.0")
	}
}

func TestCollectRequiredModulesVersionedConflict(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{
					"github.com/org/mod/a@v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"local": {Name: "local", Depends: []string{
			"github.com/org/mod/b@v2.0.0",
		}},
	}

	_, err := CollectRequiredModulesVersioned(cfg, layers)
	if err == nil {
		t.Fatal("expected version conflict error")
	}
}

func TestDiscoverModuleLayers(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "beta"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "alpha"), 0755)
	// Create a file (should be ignored)
	os.WriteFile(filepath.Join(layersDir, "README.md"), []byte("test"), 0644)

	names, err := DiscoverModuleLayers(dir)
	if err != nil {
		t.Fatalf("DiscoverModuleLayers() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("len(names) = %d, want 2", len(names))
	}
	// Should be sorted
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("names = %v, want [alpha beta]", names)
	}
}

func TestLayerCopySource(t *testing.T) {
	g := &Generator{
		Layers: map[string]*Layer{
			"pixi": {Name: "pixi", Remote: false},
			"github.com/test/mod/cuda": {Name: "cuda", Remote: true, ModulePath: "github.com/test/mod"},
		},
	}

	if got := g.layerCopySource("pixi"); got != "layers/pixi" {
		t.Errorf("local layer: got %q, want %q", got, "layers/pixi")
	}
	if got := g.layerCopySource("github.com/test/mod/cuda"); got != ".build/_layers/cuda" {
		t.Errorf("remote layer: got %q, want %q", got, ".build/_layers/cuda")
	}
}

func TestParseModuleManifest(t *testing.T) {
	dir := t.TempDir()

	// No module.yml
	mm, err := ParseModuleManifest(dir)
	if err != nil {
		t.Fatalf("ParseModuleManifest() error = %v", err)
	}
	if mm != nil {
		t.Fatal("expected nil for missing module.yml")
	}

	// Write module.yml
	os.WriteFile(filepath.Join(dir, "module.yml"), []byte("module: github.com/test/mod\n"), 0644)

	mm, err = ParseModuleManifest(dir)
	if err != nil {
		t.Fatalf("ParseModuleManifest() error = %v", err)
	}
	if mm.Module != "github.com/test/mod" {
		t.Errorf("module = %q, want %q", mm.Module, "github.com/test/mod")
	}
}
