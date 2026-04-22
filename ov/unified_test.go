package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper — write a file under root, creating parent directories as needed
func writeFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadUnified_AbsentFileReturnsNotPresent(t *testing.T) {
	root := t.TempDir()
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if present {
		t.Error("present = true, want false for empty dir")
	}
	if uf != nil {
		t.Error("uf != nil, want nil when file absent")
	}
}

func TestLoadUnified_BasicRoot(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
defaults:
  registry: quay.io/example
  build: [rpm]
images:
  fedora:
    base: quay.io/fedora/fedora:43
    distro: [fedora:43, fedora]
    layers: [base]
`)
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if uf.Version != 1 {
		t.Errorf("Version = %d, want 1", uf.Version)
	}
	if uf.Defaults.Registry != "quay.io/example" {
		t.Errorf("Defaults.Registry = %q, want quay.io/example", uf.Defaults.Registry)
	}
	fedora, ok := uf.Images["fedora"]
	if !ok {
		t.Fatal("images.fedora missing")
	}
	if fedora.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("Base = %q", fedora.Base)
	}
}

func TestLoadUnified_IncludesMerge(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
includes:
  - build.yml
  - images.yml
defaults:
  registry: override.example.com
`)
	writeFixture(t, root, "build.yml", `distros:
  fedora:
    bootstrap:
      install_cmd: "dnf install"
      packages: [dnf5]
`)
	writeFixture(t, root, "images.yml", `defaults:
  registry: included.example.com
  build: [rpm]
images:
  fedora:
    base: quay.io/fedora/fedora:43
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// Root-wins: overthink.yml's registry must override includes.
	if uf.Defaults.Registry != "override.example.com" {
		t.Errorf("Registry = %q, want override.example.com (root wins)", uf.Defaults.Registry)
	}
	// Includes contribute fields not set in root.
	if len(uf.Defaults.Build) != 1 || uf.Defaults.Build[0] != "rpm" {
		t.Errorf("Defaults.Build = %v, want [rpm]", uf.Defaults.Build)
	}
	if uf.Distros["fedora"] == nil {
		t.Error("Distros.fedora missing")
	}
	if _, ok := uf.Images["fedora"]; !ok {
		t.Error("Images.fedora missing")
	}
}

func TestLoadUnified_IncludeCycleDetected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
includes: [a.yml]
`)
	writeFixture(t, root, "a.yml", `includes: [b.yml]
`)
	writeFixture(t, root, "b.yml", `includes: [a.yml]
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want message mentioning cycle", err)
	}
}

func TestLoadUnified_MultiDocumentKindKeyed(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
includes: [bundle.yml]
`)
	writeFixture(t, root, "bundle.yml", `---
layer:
  name: chrome
  rpm:
    packages: [chromium]
---
layer:
  name: firefox
  rpm:
    packages: [firefox]
---
image:
  name: browsers
  base: quay.io/fedora/fedora:43
  layers: [chrome, firefox]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if _, ok := uf.Layers["chrome"]; !ok {
		t.Error("layers.chrome missing")
	}
	if _, ok := uf.Layers["firefox"]; !ok {
		t.Error("layers.firefox missing")
	}
	if _, ok := uf.Images["browsers"]; !ok {
		t.Error("images.browsers missing")
	}
}

func TestLoadUnified_AmbiguousDocRejected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
`)
	writeFixture(t, root, "overthink.yml", `version: 1
includes: [bundle.yml]
`)
	writeFixture(t, root, "bundle.yml", `layer:
  name: broken
image:
  name: broken-too
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected ambiguous-doc error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %v, want message mentioning ambiguous", err)
	}
}

func TestLoadUnified_DiscoverLayers(t *testing.T) {
	root := t.TempDir()
	// Two traditional flat layer.yml files (without the kind-keyed wrapper —
	// that's what scanLayer currently parses).
	writeFixture(t, root, "layers/chrome/layer.yml", `version: "1"
rpm:
  packages: [chromium]
`)
	writeFixture(t, root, "layers/firefox/layer.yml", `version: "1"
rpm:
  packages: [firefox]
`)
	writeFixture(t, root, "overthink.yml", `version: 1
discover:
  layers: [layers]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Layers["chrome"]; !ok {
		t.Error("discovered layers.chrome missing")
	}
	if _, ok := uf.Layers["firefox"]; !ok {
		t.Error("discovered layers.firefox missing")
	}
}

func TestLoadUnified_DiscoverExplicitWinsOverDiscovered(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "layers/chrome/layer.yml", `version: "from-disk"
rpm: { packages: [chromium] }
`)
	writeFixture(t, root, "overthink.yml", `version: 1
discover:
  layers: [layers]
layers:
  chrome: { from: custom/chrome }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	il := uf.Layers["chrome"]
	if il == nil {
		t.Fatal("layers.chrome missing")
	}
	if il.From != "custom/chrome" {
		t.Errorf("explicit map entry lost: From = %q, want custom/chrome", il.From)
	}
}

func TestLoadUnified_ScanSpecStringShorthand(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
discover:
  layers:
    - layers
    - { path: vendor, recursive: false }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if uf.Discover == nil || len(uf.Discover.Layers) != 2 {
		t.Fatalf("Discover.Layers = %#v, want 2 entries", uf.Discover)
	}
	if uf.Discover.Layers[0].Path != "layers" || !uf.Discover.Layers[0].Recursive {
		t.Errorf("[0] = %+v, want {Path:layers Recursive:true}", uf.Discover.Layers[0])
	}
	if uf.Discover.Layers[1].Path != "vendor" || uf.Discover.Layers[1].Recursive {
		t.Errorf("[1] = %+v, want {Path:vendor Recursive:false}", uf.Discover.Layers[1])
	}
}

func TestLoadUnified_DeploymentsSection(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
deployments:
  images:
    openclaw:
      ports: ["8080:80"]
      target: container
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if uf.Deployments == nil || uf.Deployments.Images == nil {
		t.Fatal("Deployments section missing")
	}
	d := uf.Deployments.Images["openclaw"]
	if len(d.Ports) != 1 || d.Ports[0] != "8080:80" {
		t.Errorf("openclaw.Ports = %v, want [8080:80]", d.Ports)
	}
}

func TestLoadUnified_ProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 1
defaults: { registry: r.example.com }
images:
  foo: { base: alpine }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()
	if cfg.Defaults.Registry != "r.example.com" {
		t.Errorf("Defaults.Registry = %q", cfg.Defaults.Registry)
	}
	if cfg.Images["foo"].Base != "alpine" {
		t.Errorf("Images.foo.Base = %q", cfg.Images["foo"].Base)
	}
}
