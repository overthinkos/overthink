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
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
defaults:
  registry: quay.io/example
  build: [rpm]
image:
  fedora:
    base: quay.io/fedora/fedora:43
    distro: [fedora:43, fedora]
    layer: [base]
`)
	uf, present, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !present {
		t.Fatal("present = false, want true")
	}
	if uf.Version != LatestSchemaVersion().String() {
		t.Errorf("Version = %q, want %q", uf.Version, LatestSchemaVersion().String())
	}
	if uf.Defaults.Registry != "quay.io/example" {
		t.Errorf("Defaults.Registry = %q, want quay.io/example", uf.Defaults.Registry)
	}
	fedora, ok := uf.Image["fedora"]
	if !ok {
		t.Fatal("images.fedora missing")
	}
	if fedora.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("Base = %q", fedora.Base)
	}
}

func TestLoadUnified_NewerSchemaRejectedWithUpdateHint(t *testing.T) {
	root := t.TempDir()
	// A version far past LatestSchemaVersion(): the binary is behind the
	// config, so migrating cannot help — the user must update ov.
	writeFixture(t, root, "overthink.yml", `version: 9999.141.1530
image:
  fedora:
    base: quay.io/fedora/fedora:43
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard-fail for a config newer than the binary supports")
	}
	msg := err.Error()
	if !strings.Contains(msg, "newer than this ov supports") {
		t.Errorf("error %q missing 'newer than this ov supports'", msg)
	}
	if !strings.Contains(msg, "Update ov") {
		t.Errorf("error %q missing 'Update ov' advice", msg)
	}
	if strings.Contains(msg, "ov migrate") {
		t.Errorf("error %q wrongly advises 'ov migrate' for a too-new config", msg)
	}
}

func TestLoadUnified_IncludesMerge(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
import:
  - build.yml
  - images.yml
defaults:
  registry: override.example.com
`)
	writeFixture(t, root, "build.yml", `distro:
  fedora:
    bootstrap:
      install_cmd: "dnf install"
      package: [dnf5]
`)
	writeFixture(t, root, "images.yml", `defaults:
  registry: included.example.com
  build: [rpm]
image:
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
	if uf.Distro["fedora"] == nil {
		t.Error("Distros.fedora missing")
	}
	if _, ok := uf.Image["fedora"]; !ok {
		t.Error("Images.fedora missing")
	}
}

func TestLoadUnified_IncludeCycleDetected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
import: [a.yml]
`)
	writeFixture(t, root, "a.yml", `import: [b.yml]
`)
	writeFixture(t, root, "b.yml", `import: [a.yml]
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
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
import: [bundle.yml]
`)
	writeFixture(t, root, "bundle.yml", `---
layer:
  name: chrome
  rpm:
    package: [chromium]
---
layer:
  name: firefox
  rpm:
    package: [firefox]
---
image:
  name: browsers
  base: quay.io/fedora/fedora:43
  layer: [chrome, firefox]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if _, ok := uf.Layer["chrome"]; !ok {
		t.Error("layers.chrome missing")
	}
	if _, ok := uf.Layer["firefox"]; !ok {
		t.Error("layers.firefox missing")
	}
	if _, ok := uf.Image["browsers"]; !ok {
		t.Error("images.browsers missing")
	}
}

func TestLoadUnified_AmbiguousDocRejected(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
`)
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
import: [bundle.yml]
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
  package: [chromium]
`)
	writeFixture(t, root, "layers/firefox/layer.yml", `version: "1"
rpm:
  package: [firefox]
`)
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
discover:
  layer: [layers]
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	if _, ok := uf.Layer["chrome"]; !ok {
		t.Error("discovered layers.chrome missing")
	}
	if _, ok := uf.Layer["firefox"]; !ok {
		t.Error("discovered layers.firefox missing")
	}
}

func TestLoadUnified_DiscoverExplicitWinsOverDiscovered(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "layers/chrome/layer.yml", `version: "from-disk"
rpm: { packages: [chromium] }
`)
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
discover:
  layer: [layers]
layer:
  chrome: { from: custom/chrome }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if err := uf.ApplyDiscover(root); err != nil {
		t.Fatalf("ApplyDiscover: %v", err)
	}
	il := uf.Layer["chrome"]
	if il == nil {
		t.Fatal("layers.chrome missing")
	}
	if il.From != "custom/chrome" {
		t.Errorf("explicit map entry lost: From = %q, want custom/chrome", il.From)
	}
}

func TestLoadUnified_ScanSpecStringShorthand(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
discover:
  layer:
    - layers
    - { path: vendor, recursive: false }
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if uf.Discover == nil || len(uf.Discover.Layer) != 2 {
		t.Fatalf("Discover.Layers = %#v, want 2 entries", uf.Discover)
	}
	// Post-2026-05 "discover anchoring" cutover (commit 460fabb): scan
	// specs are anchored to the including file's directory at merge time
	// so a relative `- layers` entry inside an included overthink.yml
	// resolves against THAT file's location, not the consumer's cwd. The
	// test fixture lives at <tempdir>/overthink.yml, so the anchored path
	// is filepath.Join(<tempdir>, "layers"). Asserting against the suffix
	// keeps the test portable across tempdir layouts.
	wantLayers := filepath.Join(root, "layers")
	wantVendor := filepath.Join(root, "vendor")
	if uf.Discover.Layer[0].Path != wantLayers || !uf.Discover.Layer[0].Recursive {
		t.Errorf("[0] = %+v, want {Path:%s Recursive:true}", uf.Discover.Layer[0], wantLayers)
	}
	if uf.Discover.Layer[1].Path != wantVendor || uf.Discover.Layer[1].Recursive {
		t.Errorf("[1] = %+v, want {Path:%s Recursive:false}", uf.Discover.Layer[1], wantVendor)
	}
}

// TestLoadUnified_DeploymentsSection — post-2026-05 kind-files cutover,
// the legacy v3 plural `deployments:` is hard-rejected at load time with
// a hint pointing at `ov migrate`. Pre-cutover this test
// asserted the alias was accepted; the inverse assertion enforces R5
// (no stale references).
func TestLoadUnified_DeploymentsSection(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
deployments:
  openclaw:
    port: ["8080:80"]
    target: container
`)
	_, _, err := LoadUnified(root)
	if err == nil {
		t.Fatal("expected hard-error for legacy v3 plural deployments:, got nil")
	}
	if !strings.Contains(err.Error(), "ov migrate") {
		t.Errorf("error must point at an `ov migrate` command, got: %v", err)
	}
	if !strings.Contains(err.Error(), "deployments") {
		t.Errorf("error must mention the offending root-key, got: %v", err)
	}
}

func TestLoadUnified_ProjectConfig(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.155.1801
defaults: { registry: r.example.com }
image:
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
	if cfg.Image["foo"].Base != "alpine" {
		t.Errorf("Images.foo.Base = %q", cfg.Image["foo"].Base)
	}
}
