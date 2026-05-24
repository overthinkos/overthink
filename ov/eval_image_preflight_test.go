package main

import (
	"strings"
	"testing"
)

// TestResolveImageRefForEnsure_RemoteRef — `@github.com/...`
// passes through unchanged; the pull path routes via
// ResolveRemoteImage at pull time.
func TestResolveImageRefForEnsure_RemoteRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("@github.com/overthinkos/overthink/eval-target:latest", nil, "")
	if err != nil {
		t.Fatalf("remote ref err: %v", err)
	}
	if !strings.HasPrefix(ref, "@github.com/") {
		t.Errorf("remote ref should pass through, got %q", ref)
	}
}

// TestResolveImageRefForEnsure_FullRef — fully-qualified registry
// refs pass through unchanged.
func TestResolveImageRefForEnsure_FullRef(t *testing.T) {
	ref, err := resolveImageRefForEnsure("ghcr.io/overthinkos/eval-target:2026.124.1253", nil, "")
	if err != nil {
		t.Fatalf("full ref err: %v", err)
	}
	if ref != "ghcr.io/overthinkos/eval-target:2026.124.1253" {
		t.Errorf("full ref should pass through, got %q", ref)
	}
}

// TestResolveImageRefForEnsure_ShortNameRequiresCfg — short names
// without a *Config error with a friendly message naming image.yml.
func TestResolveImageRefForEnsure_ShortNameRequiresCfg(t *testing.T) {
	_, err := resolveImageRefForEnsure("eval-target", nil, "")
	if err == nil {
		t.Fatal("expected error for short name with nil cfg")
	}
	if !strings.Contains(err.Error(), "image.yml") {
		t.Errorf("error should mention image.yml, got: %v", err)
	}
}

// TestBuildableShortName_FullRefBasenameLookup — the build-fallback
// path for full registry refs reverse-resolves the basename against
// cfg.Image. This is what lets
// `ghcr.io/overthinkos/arch-builder:<tag>` build locally on a
// host with no ghcr.io credentials.
func TestBuildableShortName_FullRefBasenameLookup(t *testing.T) {
	cfg := &Config{Image: map[string]ImageConfig{
		"arch-builder":   {},
		"fedora-builder": {},
	}}
	cases := []struct {
		image string
		want  string
	}{
		{"ghcr.io/overthinkos/arch-builder:2026.122.2252", "arch-builder"},
		{"ghcr.io/overthinkos/fedora-builder:latest", "fedora-builder"},
		{"localhost:5000/arch-builder:dev", "arch-builder"},
		{"arch-builder", "arch-builder"},
		{"some-unknown-image", ""},
		{"ghcr.io/owner/totally-unknown:v1", ""},
	}
	for _, c := range cases {
		got := buildableShortName(c.image, cfg)
		if got != c.want {
			t.Errorf("buildableShortName(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

// TestBuildableShortName_NilCfg returns "" cleanly.
func TestBuildableShortName_NilCfg(t *testing.T) {
	if got := buildableShortName("anything", nil); got != "" {
		t.Errorf("expected '' for nil cfg, got %q", got)
	}
}

// TestBuildableShortName_RemoteRef returns "" — remote refs use the
// remote project's image.yml; local build is not applicable.
func TestBuildableShortName_RemoteRef(t *testing.T) {
	cfg := &Config{Image: map[string]ImageConfig{"x": {}}}
	if got := buildableShortName("@github.com/owner/repo/x:tag", cfg); got != "" {
		t.Errorf("expected '' for remote ref, got %q", got)
	}
}

// TestEnsureScoreImages_NilScore returns nil (no-op).
func TestEnsureScoreImages_NilScore(t *testing.T) {
	if err := ensureScoreImages(nil, nil, nil, ""); err != nil {
		t.Errorf("nil score should be a no-op, got %v", err)
	}
}

// TestEnsureScoreImages_EmptyTargetAndNoScenarios returns nil.
func TestEnsureScoreImages_EmptyTargetAndNoScenarios(t *testing.T) {
	score := &HarnessScore{}
	uf := &UnifiedFile{}
	if err := ensureScoreImages(nil, score, uf, ""); err != nil {
		t.Errorf("score with no images to ensure should be a no-op, got %v", err)
	}
}

// TestEnsureImagePresent_EmptyImageErrors guards against silent
// no-ops on empty input.
func TestEnsureImagePresent_EmptyImageErrors(t *testing.T) {
	err := EnsureImagePresent(nil, "", nil, "")
	if err == nil {
		t.Error("expected error on empty image identifier")
	}
}
