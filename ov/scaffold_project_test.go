package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestScaffoldProject covers the happy path + the don't-clobber guard.
// Doesn't run `image validate`, that's exercised in TestScaffoldProject_AddImageRoundtrip.
func TestScaffoldProject(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	for _, p := range []string{"overthink.yml", "candy", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s to exist: %v", p, err)
		}
	}
	// The scaffolder must NEVER write a per-kind image.yml — schema v4
	// canonical authoring target is overthink.yml only.
	if _, err := os.Stat(filepath.Join(dir, "box.yml")); err == nil {
		t.Errorf("expected NO image.yml at scaffold root (schema v4); found one")
	}
	// Idempotency: re-scaffolding the same dir should fail (we never
	// silently clobber an existing overthink.yml).
	if err := ScaffoldProject(dir); err == nil {
		t.Errorf("expected re-scaffold to error; got nil")
	}
}

// TestScaffoldProject_AddImageRoundtrip is the integration test the plan
// names. Scaffold a project, add an image, round-trip through the parser,
// and confirm both the image appears AND the leading comment block at
// top of overthink.yml is preserved (proves the yaml.Node API is wired
// correctly and not destroying authoring metadata).
func TestScaffoldProject_AddImageRoundtrip(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddImage(dir, "hello", "quay.io/fedora/fedora:43", []string{"sshd"}); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "overthink.yml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "overthink.yml — unified project root.") {
		t.Errorf("scaffold's leading comment was destroyed by AddImage; overthink.yml=\n%s", data)
	}
	// Confirm the structure is parseable AND the image is present. The
	// canonical singular `box:` key is what the scaffold emits.
	var root struct {
		Image map[string]struct {
			Base   string   `yaml:"base"`
			Layers []string `yaml:"candy"`
		} `yaml:"box"`
	}
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	got, ok := root.Image["hello"]
	if !ok {
		t.Fatalf("hello image missing; overthink.yml=\n%s", data)
	}
	if got.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("base = %q; want quay.io/fedora/fedora:43", got.Base)
	}
	if len(got.Layers) != 1 || got.Layers[0] != "sshd" {
		t.Errorf("layers = %v; want [sshd]", got.Layers)
	}
}

// TestAddLayerToImage covers the idempotent-append behaviour.
func TestAddLayerToImage(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddImage(dir, "hello", "fedora", nil); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	if err := AddLayerToImage(dir, "hello", "sshd"); err != nil {
		t.Fatalf("AddLayerToImage first: %v", err)
	}
	if err := AddLayerToImage(dir, "hello", "sshd"); err != nil {
		t.Fatalf("AddLayerToImage second (idempotent): %v", err)
	}
	if err := AddLayerToImage(dir, "hello", "tmux"); err != nil {
		t.Fatalf("AddLayerToImage tmux: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "overthink.yml"))
	// sshd should appear exactly once, tmux exactly once.
	if got := strings.Count(string(data), "- sshd"); got != 1 {
		t.Errorf("sshd appears %d times; want 1\n%s", got, data)
	}
	if got := strings.Count(string(data), "- tmux"); got != 1 {
		t.Errorf("tmux appears %d times; want 1\n%s", got, data)
	}
}

// TestRemoveLayerFromImage covers the remove path including the no-op when
// the layer isn't present.
func TestRemoveLayerFromImage(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldProject(dir); err != nil {
		t.Fatalf("ScaffoldProject: %v", err)
	}
	if err := AddImage(dir, "hello", "fedora", []string{"sshd", "tmux"}); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	if err := RemoveLayerFromImage(dir, "hello", "sshd"); err != nil {
		t.Fatalf("RemoveLayerFromImage: %v", err)
	}
	// No-op for missing layer.
	if err := RemoveLayerFromImage(dir, "hello", "not-there"); err != nil {
		t.Fatalf("RemoveLayerFromImage no-op: %v", err)
	}
	// Error path: missing image.
	if err := RemoveLayerFromImage(dir, "ghost", "sshd"); err == nil {
		t.Errorf("expected error for missing image; got nil")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "overthink.yml"))
	if strings.Contains(string(data), "sshd") {
		t.Errorf("sshd should be removed; overthink.yml=\n%s", data)
	}
	if !strings.Contains(string(data), "tmux") {
		t.Errorf("tmux should remain; overthink.yml=\n%s", data)
	}
}
