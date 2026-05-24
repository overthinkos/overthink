package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Tests for deploy_ref.go.

func TestResolveDeployRefLocalImage(t *testing.T) {
	dir := t.TempDir()
	// Schema v4: ResolveDeployRef calls LoadUnified which reads
	// overthink.yml as the entry point. Fixture must use the unified
	// shape with version: 2026.144.1443 and the singular image: kind map.
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(`
version: 2026.144.1443
image:
  myimg:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef("myimg", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindImage || got.Source != RefSourceLocalName || got.Name != "myimg" {
		t.Errorf("unexpected parse: %+v", got)
	}
}

func TestResolveDeployRefLocalLayer(t *testing.T) {
	dir := t.TempDir()
	lyrDir := filepath.Join(dir, "layers", "ripgrep")
	if err := os.MkdirAll(lyrDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lyrDir, "layer.yml"), []byte(`
rpm:
  package: [ripgrep]
`), 0644); err != nil {
		t.Fatal(err)
	}
	// Also create overthink.yml so the local-name resolver has something
	// to search — but we don't add "ripgrep" to it, so it's layer-only.
	_ = os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte("version: 2026.144.1443\nimage: {}\n"), 0644)

	got, err := ResolveDeployRef("ripgrep", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindLayer || got.Source != RefSourceLocalName {
		t.Errorf("unexpected parse: %+v", got)
	}
	if got.Name != "ripgrep" {
		t.Errorf("name = %q, want ripgrep", got.Name)
	}
}

// TestResolveDeployRefCrossKindNameReuse — same name in both image: and
// layers/ is permitted (cross-kind name reuse, 2026-05 cutover). The
// primary `<ref>` resolver returns image-first; ResolveDeployRefAsLayer
// (used by `--add-layer <ref>`) returns layer-first. Each kind remains
// reachable via explicit paths.
func TestResolveDeployRefCrossKindNameReuse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(`
version: 2026.144.1443
image:
  dup:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "layers", "dup"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layers", "dup", "layer.yml"), []byte(`
rpm:
  package: [foo]
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Primary `<ref>` positional → image-first.
	got, err := ResolveDeployRef("dup", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindImage {
		t.Errorf("primary ref: kind = %v, want image (image-first precedence)", got.Kind)
	}

	// `--add-layer` context → layer-first.
	got, err = ResolveDeployRefAsLayer("dup", dir)
	if err != nil {
		t.Fatalf("ResolveDeployRefAsLayer: %v", err)
	}
	if got.Kind != RefKindLayer {
		t.Errorf("add-layer ref: kind = %v, want layer (layer-first precedence)", got.Kind)
	}
}

func TestResolveDeployRefUnknownName(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte("version: 2026.144.1443\nimage: {}\n"), 0644)
	_, err := ResolveDeployRef("nope", dir)
	if err == nil {
		t.Fatalf("expected not-found error, got nil")
	}
}

func TestResolveDeployRefLocalPathImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-image.yml")
	if err := os.WriteFile(path, []byte(`
defaults:
  registry: ghcr.io/example
image:
  foo:
    base: fedora
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef(path, dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindImage {
		t.Errorf("kind = %v, want image", got.Kind)
	}
	if got.Source != RefSourceLocalPath {
		t.Errorf("source = %v, want local-path", got.Source)
	}
}

func TestResolveDeployRefLocalPathLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "custom-layer.yml")
	if err := os.WriteFile(path, []byte(`
description: A layer
rpm:
  package: [bat]
`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDeployRef(path, dir)
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindLayer {
		t.Errorf("kind = %v, want layer", got.Kind)
	}
}

func TestResolveDeployRefRemoteGitHubLegacy(t *testing.T) {
	// Legacy @-prefixed form used by today's depends:/layers: fields.
	ref := "@github.com/overthinkos/overthink/layers/ripgrep:main"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindLayer {
		t.Errorf("kind = %v, want layer", got.Kind)
	}
	if got.Source != RefSourceRemote {
		t.Errorf("source = %v, want remote", got.Source)
	}
	if got.Remote == nil {
		t.Fatalf("Remote is nil")
	}
	if got.Remote.Name != "ripgrep" {
		t.Errorf("remote name = %q", got.Remote.Name)
	}
	if got.Remote.Version != "main" {
		t.Errorf("remote version = %q", got.Remote.Version)
	}
}

func TestResolveDeployRefRemoteGitHubNewSyntax(t *testing.T) {
	// New plan-approved syntax: no @ prefix, @ as version separator.
	ref := "github.com/overthinkos/overthink/layers/ripgrep@main"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindLayer {
		t.Errorf("kind = %v, want layer", got.Kind)
	}
	if got.Remote.Version != "main" {
		t.Errorf("remote version = %q", got.Remote.Version)
	}
}

func TestResolveDeployRefRemoteImage(t *testing.T) {
	ref := "github.com/overthinkos/overthink/images/fedora-coder"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindImage {
		t.Errorf("kind = %v, want image", got.Kind)
	}
}

func TestResolveDeployRefRemoteBareRepo(t *testing.T) {
	// A remote repo without /layers/ or /images/ — defaults to image
	// (points at the project's image.yml).
	ref := "github.com/overthinkos/overthink"
	got, err := ResolveDeployRef(ref, "")
	if err != nil {
		t.Fatalf("ResolveDeployRef: %v", err)
	}
	if got.Kind != RefKindImage {
		t.Errorf("bare repo kind = %v, want image (default)", got.Kind)
	}
}

func TestTranslateAtVersion(t *testing.T) {
	tests := map[string]string{
		"github.com/foo/bar":                 "github.com/foo/bar",
		"github.com/foo/bar/layers/x":        "github.com/foo/bar/layers/x",
		"github.com/foo/bar/layers/x@main":   "github.com/foo/bar/layers/x:main",
		"github.com/foo/bar/layers/x@v1.2.3": "github.com/foo/bar/layers/x:v1.2.3",
		"github.com/foo/bar@main/layers/x":   "github.com/foo/bar:main/layers/x",
	}
	for in, want := range tests {
		if got := translateAtVersion(in); got != want {
			t.Errorf("translateAtVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassifyYAMLFile(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "img.yml")
	layer := filepath.Join(dir, "lyr.yml")
	unknown := filepath.Join(dir, "u.yml")

	_ = os.WriteFile(image, []byte("images:\n  x: {}\n"), 0644)
	_ = os.WriteFile(layer, []byte("rpm:\n  packages: [a]\n"), 0644)
	_ = os.WriteFile(unknown, []byte("foo: bar\n"), 0644)

	if k, err := classifyYAMLFile(image); err != nil || k != RefKindImage {
		t.Errorf("image file: kind=%v err=%v, want image nil", k, err)
	}
	if k, err := classifyYAMLFile(layer); err != nil || k != RefKindLayer {
		t.Errorf("layer file: kind=%v err=%v, want layer nil", k, err)
	}
	if _, err := classifyYAMLFile(unknown); err == nil {
		t.Errorf("unknown file should return error")
	}
}
