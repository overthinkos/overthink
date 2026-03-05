package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripVersion(t *testing.T) {
	tests := []struct {
		ref     string
		wantRef string
		wantVer string
	}{
		{"@github.com/org/repo/layer:v1.0.0", "@github.com/org/repo/layer", "v1.0.0"},
		{"@github.com/org/repo/layer:main", "@github.com/org/repo/layer", "main"},
		{"@github.com/org/repo/layer", "@github.com/org/repo/layer", ""},
		{"pixi", "pixi", ""},
		{"my-layer", "my-layer", ""},
	}

	for _, tt := range tests {
		gotRef, gotVer := StripVersion(tt.ref)
		if gotRef != tt.wantRef || gotVer != tt.wantVer {
			t.Errorf("StripVersion(%q) = (%q, %q), want (%q, %q)", tt.ref, gotRef, gotVer, tt.wantRef, tt.wantVer)
		}
	}
}

func TestBareRef(t *testing.T) {
	tests := []struct {
		ref  string
		want string
	}{
		{"@github.com/org/repo/layer:v1.0.0", "github.com/org/repo/layer"},
		{"@github.com/org/repo/layer", "github.com/org/repo/layer"},
		{"pixi", "pixi"},
		{"my-layer", "my-layer"},
	}

	for _, tt := range tests {
		got := BareRef(tt.ref)
		if got != tt.want {
			t.Errorf("BareRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestParseRemoteRef(t *testing.T) {
	tests := []struct {
		ref      string
		wantRepo string
		wantName string
		wantVer  string
	}{
		{"@github.com/org/repo/layer:v1.0.0", "github.com/org/repo", "layer", "v1.0.0"},
		{"@github.com/org/repo/image:main", "github.com/org/repo", "image", "main"},
		{"@github.com/org/repo/name", "github.com/org/repo", "name", ""},
		{"pixi", "", "pixi", ""},
	}

	for _, tt := range tests {
		got := ParseRemoteRef(tt.ref)
		if got.RepoPath != tt.wantRepo || got.Name != tt.wantName || got.Version != tt.wantVer {
			t.Errorf("ParseRemoteRef(%q) = {Repo: %q, Name: %q, Version: %q}, want {%q, %q, %q}",
				tt.ref, got.RepoPath, got.Name, got.Version, tt.wantRepo, tt.wantName, tt.wantVer)
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
		{"@github.com/org/repo/layer", true},
		{"@github.com/overthinkos/ml-layers/cuda", true},
		{"@gitlab.com/org/repo/layer", true},
		{"@github.com/org/repo/layer:v1.0.0", true},
		{"github.com/org/repo/layer", false}, // no @ prefix = not remote
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
		{"@github.com/org/repo/image", true},
		{"@github.com/org/repo/image:v1.0.0", true},
		{"github.com/org/repo/image", false}, // no @ prefix
	}

	for _, tt := range tests {
		got := IsRemoteImageRef(tt.ref)
		if got != tt.want {
			t.Errorf("IsRemoteImageRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestScanModuleLayers(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "cuda"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "python-ml"), 0755)

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

func TestCollectRemoteRefs(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{
					"pixi",
					"@github.com/overthinkos/ml-layers/cuda:v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", Depends: []string{}},
		"my-layer": {Name: "my-layer", RawDepends: []string{
			"@github.com/myorg/service-layers/svc:v2.0.0",
		}},
	}

	repos, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("len(repos) = %d, want 2", len(repos))
	}
	if repos["github.com/overthinkos/ml-layers"] != "v1.0.0" {
		t.Errorf("ml-layers version = %q, want %q", repos["github.com/overthinkos/ml-layers"], "v1.0.0")
	}
	if repos["github.com/myorg/service-layers"] != "v2.0.0" {
		t.Errorf("service-layers version = %q, want %q", repos["github.com/myorg/service-layers"], "v2.0.0")
	}
}

func TestCollectRemoteRefsVersionConflict(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{
					"@github.com/org/mod/a:v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"local": {Name: "local", RawDepends: []string{
			"@github.com/org/mod/b:v2.0.0",
		}},
	}

	_, err := CollectRemoteRefs(cfg, layers)
	if err == nil {
		t.Fatal("expected version conflict error")
	}
}

func TestParseTagRefs(t *testing.T) {
	output := `abc123def456	refs/tags/v0.1.0
def456abc789	refs/tags/v0.1.0^{}
111222333444	refs/tags/v1.0.0
555666777888	refs/tags/v1.0.0^{}
aaa111bbb222	refs/tags/v2.0.0
ccc333ddd444	refs/tags/v2.0.0^{}
eee555fff666	refs/tags/not-semver
`
	tags := parseTagRefs(output)
	if len(tags) != 3 {
		t.Fatalf("len(tags) = %d, want 3", len(tags))
	}
	// Should contain v0.1.0, v1.0.0, v2.0.0 (no ^{} or non-v tags)
	want := map[string]bool{"v0.1.0": true, "v1.0.0": true, "v2.0.0": true}
	for _, tag := range tags {
		if !want[tag] {
			t.Errorf("unexpected tag %q", tag)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v2.0.0", -1},
		{"v2.0.0", "v1.0.0", 1},
		{"v1.0.0", "v1.1.0", -1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.9.0", "v1.10.0", -1},
		{"v0.1.0", "v1.0.0", -1},
	}

	for _, tt := range tests {
		got := compareSemver(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
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

func TestModuleGitURL(t *testing.T) {
	got := ModuleGitURL("github.com/overthinkos/ml-layers")
	want := "https://github.com/overthinkos/ml-layers.git"
	if got != want {
		t.Errorf("ModuleGitURL() = %q, want %q", got, want)
	}
}

func TestDiscoverModuleLayers(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "beta"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "alpha"), 0755)
	os.WriteFile(filepath.Join(layersDir, "README.md"), []byte("test"), 0644)

	names, err := DiscoverModuleLayers(dir)
	if err != nil {
		t.Fatalf("DiscoverModuleLayers() error = %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("len(names) = %d, want 2", len(names))
	}
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
