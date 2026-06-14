package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedBuildDefaults_NoBuildYml proves a project with NO build.yml import
// still resolves the default distro/builder vocabulary from the binary embed.
func TestEmbeddedBuildDefaults_NoBuildYml(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
defaults:
  registry: ghcr.io/example
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	for _, d := range []string{"fedora", "arch"} {
		if uf.Distro[d] == nil {
			t.Errorf("embedded distro %q missing with no build.yml import", d)
		}
	}
	if uf.Builder["pixi"] == nil {
		t.Error("embedded builder pixi missing with no build.yml import")
	}
}

// TestEmbeddedBuildDefaults_ProjectWins proves the base/overlay precedence: a
// project that redefines an embedded distro OVERRIDES it (project wins), a NEW
// distro EXTENDS the vocabulary, and untouched embedded distros remain. This is
// the high-risk RDD assertion — the embed must be the base, the project the
// overlay that wins.
func TestEmbeddedBuildDefaults_ProjectWins(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
distro:
  fedora:
    version: "marker99"
    bootstrap:
      install_cmd: custom-fedora
  mydistro:
    bootstrap:
      install_cmd: custom-mine
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	// Override: the project's fedora WINS over the embedded fedora.
	if uf.Distro["fedora"] == nil || uf.Distro["fedora"].Version != "marker99" {
		t.Errorf("project fedora override lost (embed wrongly won); got %+v", uf.Distro["fedora"])
	}
	// Extend: the project's new distro coexists with the embedded ones.
	if uf.Distro["mydistro"] == nil {
		t.Error("project-added distro mydistro missing")
	}
	// Untouched: an embedded distro the project didn't mention is still present.
	if uf.Distro["arch"] == nil {
		t.Error("embedded distro arch missing (embed not applied as base)")
	}
}

// TestEmbeddedDefaults_SameLoaderPath proves the consolidation invariant: the
// SINGLE binary-embedded charly.yml is parsed by the SAME unified loader core
// (embeddedDefaults → mergeUnifiedDocs) and yields BOTH the build vocabulary
// AND the sidecar-template library from one parse — no bespoke per-section
// loader. This is the core "parse its own charly.yml with exactly the same code
// path" guarantee.
func TestEmbeddedDefaults_SameLoaderPath(t *testing.T) {
	def, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	// Build vocabulary view.
	for _, d := range []string{"fedora", "arch"} {
		if def.Distro[d] == nil {
			t.Errorf("embedded distro %q missing from unified parse", d)
		}
	}
	if def.Builder["pixi"] == nil {
		t.Error("embedded builder pixi missing from unified parse")
	}
	if def.Resource["nvidia-gpu"] == nil {
		t.Error("embedded resource nvidia-gpu missing from unified parse")
	}
	// Sidecar-template view — from the SAME parse, the SAME UnifiedFile.
	ts, ok := def.Sidecar["tailscale"]
	if !ok {
		t.Fatal("embedded sidecar tailscale missing from unified parse")
	}
	if ts.Image != "ghcr.io/tailscale/tailscale:latest" {
		t.Errorf("tailscale sidecar image = %q, want ghcr.io/tailscale/tailscale:latest", ts.Image)
	}
}

// TestEmbeddedDefaults_SidecarProjectWins proves a project may declare a root
// `sidecar:` template that OVERRIDES the embedded one (project-wins), routed
// through LoadUnified exactly like the build vocabulary.
func TestEmbeddedDefaults_SidecarProjectWins(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
sidecar:
  tailscale:
    image: example.com/custom-tailscale:pinned
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if uf.Sidecar["tailscale"].Image != "example.com/custom-tailscale:pinned" {
		t.Errorf("project sidecar override lost (embed wrongly won); got %q", uf.Sidecar["tailscale"].Image)
	}
}

// TestNoHardcodedYAMLFilenames is the file-agnostic invariant guard. charly.yml
// is the ONE YAML filename the code knows — including the binary-embedded
// default config (//go:embed charly.yml, embed_defaults.go). Outside migration
// code (which must name legacy files to migrate FROM) and tests, no source may
// hardcode a per-kind project filename — discovery + the UnifiedFileName
// constant cover them all. build.yml and sidecar.yml are now legacy filenames
// (named only in migration + tests); deploy.yml (per-machine host state) and
// charly.yml are deliberately NOT in the forbidden set.
func TestNoHardcodedYAMLFilenames(t *testing.T) {
	forbidden := regexp.MustCompile(`"(box|candy|base|vm|pod|k8s|check|local|android|image|images|layer)\.yml"`)
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range entries {
		if strings.HasSuffix(f, "_test.go") || strings.HasPrefix(f, "migrate_") {
			continue // tests + migration are the sanctioned homes for legacy names
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if m := forbidden.FindString(line); m != "" {
				t.Errorf("%s:%d hardcodes per-kind filename %s — use discovery / UnifiedFileName:\n  %s",
					f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}
