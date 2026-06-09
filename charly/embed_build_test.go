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

// TestNoHardcodedYAMLFilenames is the file-agnostic invariant guard. charly.yml
// is the ONE YAML filename the code knows; outside migration code (which must
// name legacy files to migrate FROM) and tests, no source may hardcode a
// per-kind project filename — discovery + the UnifiedFileName constant cover
// them all. deploy.yml (per-machine host state), build.yml (the embed source +
// directive), charly.yml, and sidecar.yml are deliberately NOT in the forbidden
// set.
func TestNoHardcodedYAMLFilenames(t *testing.T) {
	forbidden := regexp.MustCompile(`"(box|candy|base|vm|pod|k8s|eval|local|android|image|images|layer)\.yml"`)
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
