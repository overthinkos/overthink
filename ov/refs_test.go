package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayerRef(t *testing.T) {
	tests := []struct {
		raw      string
		bare     string
		version  string
		isRemote bool
	}{
		{"python", "python", "", false},
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda", "v1.0.0", true},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda", "", true},
	}
	for _, tt := range tests {
		r := LayerRef{Raw: tt.raw}
		if got := r.Bare(); got != tt.bare {
			t.Errorf("LayerRef{%q}.Bare() = %q, want %q", tt.raw, got, tt.bare)
		}
		if got := r.Version(); got != tt.version {
			t.Errorf("LayerRef{%q}.Version() = %q, want %q", tt.raw, got, tt.version)
		}
		if got := r.IsRemote(); got != tt.isRemote {
			t.Errorf("LayerRef{%q}.IsRemote() = %v, want %v", tt.raw, got, tt.isRemote)
		}
	}
	// A resolved sibling key overrides Bare() but leaves Raw (and thus the
	// transitive-fetch view) intact.
	r := LayerRef{Raw: "ffmpeg", resolved: "github.com/org/repo/layers/ffmpeg"}
	if r.Bare() != "github.com/org/repo/layers/ffmpeg" {
		t.Errorf("resolved Bare() = %q", r.Bare())
	}
	if r.Raw != "ffmpeg" {
		t.Errorf("resolved must leave Raw intact, got %q", r.Raw)
	}
}

// TestPickLayerVersion covers the per-entity-version arbiter (the sole
// layer-version resolver). Same per-entity `version:` across different git tags
// resolves with NO warning — the newest git tag wins for freshness — which is
// the Problem-B regression guard: a repo re-tag of an UNCHANGED layer must not
// warn. Different per-entity versions warn once and the newest version wins.
func TestPickLayerVersion(t *testing.T) {
	mk := func(ver, tag string) layerCandidate {
		return layerCandidate{
			layer:   &Layer{Name: "x", Version: ver},
			version: ver,
			gitTag:  tag,
			source:  "github.com/o/r@" + tag,
		}
	}
	capture := func(fn func() layerCandidate) (layerCandidate, string) {
		old := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		got := fn()
		_ = w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return got, buf.String()
	}

	// Same per-entity version, different git tags -> NO warning, newest tag wins.
	got, warn := capture(func() layerCandidate {
		return pickLayerVersion("github.com/o/r/layers/x", []layerCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.141.1600", "v2026.150.900"),
		})
	})
	if warn != "" {
		t.Errorf("same per-entity version must not warn, got: %q", warn)
	}
	if got.gitTag != "v2026.150.900" {
		t.Errorf("freshness tiebreak: want newest git tag v2026.150.900, got %q", got.gitTag)
	}

	// Different per-entity versions -> exactly one warning, newest version wins.
	got, warn = capture(func() layerCandidate {
		return pickLayerVersion("github.com/o/r/layers/x", []layerCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.144.531", "v2026.144.531"),
		})
	})
	if got.version != "2026.144.531" {
		t.Errorf("newest per-entity version must win, got %q", got.version)
	}
	if !strings.Contains(warn, "resolved to multiple versions") || !strings.Contains(warn, "2026.144.531") {
		t.Errorf("expected one multi-version warning naming the winner, got: %q", warn)
	}
}

func TestStripVersion(t *testing.T) {
	tests := []struct {
		ref     string
		wantRef string
		wantVer string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "@github.com/org/repo/layers/cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/cuda:main", "@github.com/org/repo/layers/cuda", "main"},
		{"@github.com/org/repo/layers/cuda", "@github.com/org/repo/layers/cuda", ""},
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
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo/layers/cuda"},
		{"@github.com/org/repo/layers/cuda", "github.com/org/repo/layers/cuda"},
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
		wantSub  string
		wantName string
		wantVer  string
	}{
		{"@github.com/org/repo/layers/cuda:v1.0.0", "github.com/org/repo", "layers/cuda", "cuda", "v1.0.0"},
		{"@github.com/org/repo/layers/image:main", "github.com/org/repo", "layers/image", "image", "main"},
		{"@github.com/org/repo/layers/name", "github.com/org/repo", "layers/name", "name", ""},
		{"@github.com/org/repo/image", "github.com/org/repo", "image", "image", ""},
		{"pixi", "", "", "pixi", ""},
	}

	for _, tt := range tests {
		got := ParseRemoteRef(tt.ref)
		if got.RepoPath != tt.wantRepo || got.SubPath != tt.wantSub || got.Name != tt.wantName || got.Version != tt.wantVer {
			t.Errorf("ParseRemoteRef(%q) = {Repo: %q, SubPath: %q, Name: %q, Version: %q}, want {%q, %q, %q, %q}",
				tt.ref, got.RepoPath, got.SubPath, got.Name, got.Version, tt.wantRepo, tt.wantSub, tt.wantName, tt.wantVer)
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
		{"@github.com/org/repo/layers/cuda", true},
		{"@github.com/overthinkos/overthink/layers/cuda", true},
		{"@gitlab.com/org/repo/layers/cuda", true},
		{"@github.com/org/repo/layers/cuda:v1.0.0", true},
		{"github.com/org/repo/layers/cuda", false}, // no @ prefix = not remote
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

func TestScanRemoteLayers(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "cuda"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "python-ml"), 0755)

	os.WriteFile(filepath.Join(layersDir, "cuda", "layer.yml"), []byte("layer:\n  name: cuda\n  package:\n    - cuda-toolkit\n"), 0644)
	os.WriteFile(filepath.Join(layersDir, "python-ml", "layer.yml"), []byte("layer:\n  name: python-ml\n  require:\n    - cuda\n"), 0644)
	os.WriteFile(filepath.Join(layersDir, "python-ml", "pixi.toml"), []byte("[project]\nname = \"python-ml\"\n"), 0644)

	wantRefs := map[string]bool{
		"github.com/overthinkos/ml-layers/layers/cuda":      true,
		"github.com/overthinkos/ml-layers/layers/python-ml": true,
	}
	layers, err := ScanRemoteLayer(dir, "github.com/overthinkos/ml-layers", wantRefs)
	if err != nil {
		t.Fatalf("ScanRemoteLayer() error = %v", err)
	}

	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}

	cuda, ok := layers["github.com/overthinkos/ml-layers/layers/cuda"]
	if !ok {
		t.Fatal("cuda layer not found")
	}
	if !cuda.Remote {
		t.Error("cuda should be remote")
	}
	if cuda.RepoPath != "github.com/overthinkos/ml-layers" {
		t.Errorf("cuda.RepoPath = %q", cuda.RepoPath)
	}
	if cuda.Name != "cuda" {
		t.Errorf("cuda.Name = %q, want %q", cuda.Name, "cuda")
	}
	if cuda.SubPathPrefix != "layers/" {
		t.Errorf("cuda.SubPathPrefix = %q, want %q", cuda.SubPathPrefix, "layers/")
	}

	pyml := layers["github.com/overthinkos/ml-layers/layers/python-ml"]
	if !pyml.HasPixiToml {
		t.Error("python-ml should have pixi.toml")
	}
	// A remote layer's plain-name sibling dep is qualified at scan time to the
	// sibling's fully-qualified map key, so the dependency graph resolves it
	// against the cuda layer fetched from the same repo (keyed identically).
	wantDep := "github.com/overthinkos/ml-layers/layers/cuda"
	if len(pyml.Require) != 1 || pyml.Require[0].Bare() != wantDep {
		t.Errorf("python-ml.Require = %v, want [%s]", pyml.Require, wantDep)
	}
	// LayerRef.Raw preserves the original short-name form for transitive fetch,
	// while .Bare() yields the qualified sibling key the graph resolves on.
	if pyml.Require[0].Raw != "cuda" {
		t.Errorf("python-ml.Require[0].Raw = %q, want cuda", pyml.Require[0].Raw)
	}
}

func TestScanAllLayersNoRemote(t *testing.T) {
	layers, err := ScanAllLayer("testdata")
	if err != nil {
		t.Fatalf("ScanAllLayer() error = %v", err)
	}

	localLayers, err := ScanLayer("testdata")
	if err != nil {
		t.Fatalf("ScanLayer() error = %v", err)
	}

	if len(layers) != len(localLayers) {
		t.Errorf("len(layers) = %d, want %d", len(layers), len(localLayers))
	}
}

func TestCollectRemoteRefs(t *testing.T) {
	cfg := &Config{
		Image: map[string]ImageConfig{
			"myapp": {
				Layer: []string{
					"pixi",
					"@github.com/overthinkos/ml-layers/layers/cuda:v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", Require: toLayerRefs([]string{})},
		"my-layer": {Name: "my-layer", Require: toLayerRefs([]string{
			"@github.com/myorg/service-layers/layers/svc:v2.0.0",
		})},
	}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	if len(downloads) != 2 {
		t.Fatalf("len(downloads) = %d, want 2", len(downloads))
	}
	// Check that both repos are present
	found := make(map[string]string)
	for _, dl := range downloads {
		found[dl.RepoPath] = dl.Version
	}
	if found["github.com/overthinkos/ml-layers"] != "v1.0.0" {
		t.Errorf("ml-layers version = %q, want %q", found["github.com/overthinkos/ml-layers"], "v1.0.0")
	}
	if found["github.com/myorg/service-layers"] != "v2.0.0" {
		t.Errorf("service-layers version = %q, want %q", found["github.com/myorg/service-layers"], "v2.0.0")
	}
}

func TestCollectRemoteRefsLocalTemplate(t *testing.T) {
	// kind:local template layer: lists must feed the same remote-ref collection
	// path as image layer: lists (regression guard for the 2026-05 CachyOS
	// migration, where the ov-cachyos kind:local template composes 30 remote
	// @-ref layers — previously invisible to CollectRemoteRefs).
	cfg := &Config{
		Image: map[string]ImageConfig{
			"myapp": {
				Layer: []string{
					"@github.com/overthinkos/overthink/layers/pixi:v1.0.0",
				},
			},
		},
		Local: map[string]*LocalSpec{
			"workstation": {
				Layer: []string{
					"@github.com/overthinkos/overthink/layers/nvidia:v1.0.0",
					"@github.com/myorg/extra-layers/layers/svc:v2.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	}
	found := make(map[string]string)
	for _, dl := range downloads {
		found[dl.RepoPath] = dl.Version
	}
	// The image ref and the kind:local template ref share a repo at the same
	// version → one download for overthink, one for the extra repo.
	if found["github.com/overthinkos/overthink"] != "v1.0.0" {
		t.Errorf("overthink version = %q, want %q", found["github.com/overthinkos/overthink"], "v1.0.0")
	}
	if found["github.com/myorg/extra-layers"] != "v2.0.0" {
		t.Errorf("extra-layers version = %q, want %q (kind:local template ref not collected)", found["github.com/myorg/extra-layers"], "v2.0.0")
	}
}

func TestCollectRemoteRefsOptsIncludeDisabled(t *testing.T) {
	// A disabled image's remote layer refs must be collected when a
	// `--include-disabled <name>` build scopes IncludeDisabled to that image —
	// so the FETCH set (CollectRemoteRefsOpts) stays in lockstep with the
	// RESOLVE set (ResolveAllImage/GlobalLayerOrder). Regression guard for the
	// 2026-05 deb-family split: no enabled debian image references `pixi`, so a
	// disabled `debian-builder --include-disabled` would otherwise hit
	// "unknown layer .../pixi" in computing global layer order.
	cfg := &Config{
		Image: map[string]ImageConfig{
			"debian-builder": {
				Enabled: boolPtr(false),
				Layer: []string{
					"@github.com/overthinkos/overthink/layers/pixi:v1.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{}

	// Default opts (enabled-only) → the disabled image is skipped, no downloads.
	if dls, err := CollectRemoteRefs(cfg, layers); err != nil {
		t.Fatalf("CollectRemoteRefs() error = %v", err)
	} else if len(dls) != 0 {
		t.Fatalf("default opts: len(downloads) = %d, want 0 (disabled image skipped)", len(dls))
	}

	// Scoped --include-disabled debian-builder → the ref IS collected.
	opts := ResolveOpts{IncludeDisabled: true, IncludeDisabledNames: map[string]bool{"debian-builder": true}}
	dls, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts() error = %v", err)
	}
	found := make(map[string]string)
	for _, dl := range dls {
		found[dl.RepoPath] = dl.Version
	}
	if found["github.com/overthinkos/overthink"] != "v1.0.0" {
		t.Errorf("scoped include-disabled: overthink version = %q, want %q (disabled image's remote layer not collected)", found["github.com/overthinkos/overthink"], "v1.0.0")
	}

	// A DIFFERENT disabled image must stay filtered under the scoped opts.
	cfg.Image["other-disabled"] = ImageConfig{
		Enabled: boolPtr(false),
		Layer:   []string{"@github.com/myorg/other/layers/x:v3.0.0"},
	}
	dls2, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		t.Fatalf("CollectRemoteRefsOpts() error = %v", err)
	}
	for _, dl := range dls2 {
		if dl.RepoPath == "github.com/myorg/other" {
			t.Errorf("scoped opts leaked an unscoped disabled image's refs: %s", dl.RepoPath)
		}
	}
}

func TestCollectRemoteRefsSameLayerBothTagsCollected(t *testing.T) {
	// Same bare ref at two git tags: collection now emits BOTH (the git tag is
	// only the FETCH coordinate). Per-entity-version arbitration (newest-wins,
	// or no-warning when the layer's own version: matches) happens AFTER fetch in
	// pickLayerVersion — see TestPickLayerVersion. Collection's job is just to
	// fetch every distinct (repo, git-tag).
	cfg := &Config{
		Image: map[string]ImageConfig{
			"myapp": {
				Layer: []string{
					"@github.com/org/repo/layers/cuda:v2.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{
		"local": {Name: "local", Version: "2026.1.1", Require: toLayerRefs([]string{
			"@github.com/org/repo/layers/cuda:v1.0.0",
		})},
	}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() unexpected error: %v", err)
	}
	cudaVers := map[string]bool{}
	for _, dl := range downloads {
		for _, ref := range dl.Refs {
			if ref == "github.com/org/repo/layers/cuda" {
				cudaVers[dl.Version] = true
			}
		}
	}
	if !cudaVers["v1.0.0"] || !cudaVers["v2.0.0"] || len(cudaVers) != 2 {
		t.Errorf("cuda fetch coordinates = %v, want both [v1.0.0 v2.0.0] collected", cudaVers)
	}
}

func TestCollectRemoteRefsDifferentLayersSameRepo(t *testing.T) {
	// Different layers from same repo at different versions should be OK
	cfg := &Config{
		Image: map[string]ImageConfig{
			"myapp": {
				Layer: []string{
					"@github.com/org/repo/layers/cuda:v1.0.0",
					"@github.com/org/repo/layers/python:v2.0.0",
				},
			},
		},
	}
	layers := map[string]*Layer{}

	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		t.Fatalf("CollectRemoteRefs() unexpected error: %v", err)
	}
	// Should have 2 downloads (same repo, different versions)
	if len(downloads) != 2 {
		t.Fatalf("len(downloads) = %d, want 2", len(downloads))
	}
}

func TestParseDefaultBranch(t *testing.T) {
	tests := []struct {
		output string
		want   string
	}{
		{"ref: refs/heads/main\tHEAD\nabc123\tHEAD\n", "main"},
		{"ref: refs/heads/master\tHEAD\ndef456\tHEAD\n", "master"},
		{"ref: refs/heads/develop\tHEAD\n789abc\tHEAD\n", "develop"},
		{"abc123\tHEAD\n", ""}, // no symref line
		{"", ""},               // empty output
	}

	for _, tt := range tests {
		got := parseDefaultBranch(tt.output)
		if got != tt.want {
			t.Errorf("parseDefaultBranch(%q) = %q, want %q", tt.output, got, tt.want)
		}
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

func TestRepoGitURL(t *testing.T) {
	got := RepoGitURL("github.com/overthinkos/ml-layers")
	want := "https://github.com/overthinkos/ml-layers.git"
	if got != want {
		t.Errorf("RepoGitURL() = %q, want %q", got, want)
	}
}

func TestDiscoverRemoteLayers(t *testing.T) {
	dir := t.TempDir()
	layersDir := filepath.Join(dir, "layers")
	os.MkdirAll(filepath.Join(layersDir, "beta"), 0755)
	os.MkdirAll(filepath.Join(layersDir, "alpha"), 0755)
	os.WriteFile(filepath.Join(layersDir, "README.md"), []byte("test"), 0644)

	names, err := DiscoverRemoteLayer(dir)
	if err != nil {
		t.Fatalf("DiscoverRemoteLayer() error = %v", err)
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
			"pixi":                             {Name: "pixi", Remote: false},
			"github.com/test/repo/layers/cuda": {Name: "cuda", Remote: true, RepoPath: "github.com/test/repo"},
		},
	}

	if got := g.layerCopySource("pixi"); got != "layers/pixi" {
		t.Errorf("local layer: got %q, want %q", got, "layers/pixi")
	}
	if got := g.layerCopySource("github.com/test/repo/layers/cuda"); got != ".build/_layers/cuda" {
		t.Errorf("remote layer: got %q, want %q", got, ".build/_layers/cuda")
	}
}
