package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestEmbeddedCUE_DataEquivalentToLegacyYAML is the data-equivalence gate for the
// YAML→CUE embed migration. The hand-authored idiomatic charly/charly.cue must
// produce a UnifiedFile byte-for-byte identical to the frozen pre-migration
// charly.yml (testdata/embedded_legacy.yml). Guards every step of the idiomatic
// refactor AND any future drift between the CUE source and the legacy data.
func TestEmbeddedCUE_DataEquivalentToLegacyYAML(t *testing.T) {
	legacy, err := os.ReadFile("testdata/embedded_legacy.yml")
	if err != nil {
		t.Fatalf("read frozen legacy fixture: %v", err)
	}
	var ufYAML UnifiedFile
	if _, err := mergeUnifiedDocs(&ufYAML, legacy, "embedded_legacy.yml", ""); err != nil {
		t.Fatalf("parse legacy fixture: %v", err)
	}

	cueSrc, err := os.ReadFile("charly.cue")
	if err != nil {
		t.Fatalf("read charly.cue: %v", err)
	}
	yb, err := compileCUEToYAML(cueSrc, "charly.cue")
	if err != nil {
		t.Fatalf("compileCUEToYAML(charly.cue): %v", err)
	}
	var ufCUE UnifiedFile
	if _, err := mergeUnifiedDocs(&ufCUE, yb, "charly.cue", ""); err != nil {
		t.Fatalf("parse charly.cue export: %v", err)
	}
	if !reflect.DeepEqual(ufCUE, ufYAML) {
		t.Errorf("charly.cue is NOT data-equivalent to the frozen legacy YAML")
	}

	// The real consumed path (embeddedDefaults) must also equal the legacy data —
	// holds pre-flip (identical bytes) and post-flip (via equivalence above).
	def, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	if !reflect.DeepEqual(*def, ufYAML) {
		t.Errorf("embeddedDefaults() is NOT data-equivalent to the frozen legacy YAML")
	}
}

// TestEmbeddedDefaults_SchemaConformance proves the embedded vocabulary validates
// against charly/schema: every distro/builder/init/resource/sidecar entity in
// charly.cue unifies concretely with its registered #Kind, through the SAME
// validateVocabularyCollections path `charly box validate` uses for project files
// (R3). This is the canonical schema-validation gate for the embedded charly.cue.
func TestEmbeddedDefaults_SchemaConformance(t *testing.T) {
	cueSrc, err := os.ReadFile("charly.cue")
	if err != nil {
		t.Fatalf("read charly.cue: %v", err)
	}
	v := cueSchemaCtx.CompileBytes(cueSrc)
	if v.Err() != nil {
		t.Fatalf("compile charly.cue: %v", v.Err())
	}
	var viol []string
	validateVocabularyCollections(v,
		[]string{"distro", "builder", "init", "resource", "sidecar"},
		"charly.cue",
		func(format string, args ...any) { viol = append(viol, fmt.Sprintf(format, args...)) })
	if len(viol) > 0 {
		t.Errorf("embedded charly.cue has %d #Kind schema violations:\n%s",
			len(viol), strings.Join(viol, "\n"))
	}
}

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
// SINGLE binary-embedded charly.cue is compiled + parsed by the SAME unified
// loader core (embeddedDefaults → compileCUEToYAML → mergeUnifiedDocs) and yields
// BOTH the build vocabulary
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

// TestEmbeddedDefaults_AllVocabKindsOverridable proves Req #2 for the three
// vocabulary kinds not already covered by TestEmbeddedBuildDefaults_ProjectWins
// (distro) and TestEmbeddedDefaults_SidecarProjectWins (sidecar): builder, init,
// and resource each stay project-charly.yml OVERRIDABLE (project wins wholesale
// via the gap-filling applyEmbeddedDefaults merge) AND EXTENDABLE (a new entry
// coexists), while untouched embedded entries survive — independent of the
// embedded config now being authored in CUE.
func TestEmbeddedDefaults_AllVocabKindsOverridable(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: `+LatestSchemaVersion().String()+`
builder:
  pixi:
    detect_config: marker99
  mybuilder:
    detect_config: custom
init:
  systemd:
    model: file_copy
  myinit:
    model: file_copy
resource:
  nvidia-gpu:
    gpu:
      vendor: marker-vendor
  amd-gpu:
    gpu:
      vendor: "0x1002"
`)
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}

	// builder: override wins WHOLESALE (gap-fill replaces, never deep-merges), a
	// new entry coexists, an untouched embedded entry survives.
	if b := uf.Builder["pixi"]; b == nil || b.DetectConfig != "marker99" {
		t.Errorf("builder pixi override lost (embed wrongly won); got %+v", uf.Builder["pixi"])
	}
	if b := uf.Builder["pixi"]; b != nil && len(b.DetectFiles) != 0 {
		t.Errorf("builder pixi override must replace wholesale (embed DetectFiles must be gone); got %+v", b.DetectFiles)
	}
	if uf.Builder["mybuilder"] == nil {
		t.Error("project-added builder mybuilder missing")
	}
	if uf.Builder["cargo"] == nil {
		t.Error("embedded builder cargo missing (embed not applied as base)")
	}

	// init
	if i := uf.Init["systemd"]; i == nil || i.Model != "file_copy" {
		t.Errorf("init systemd override lost; got %+v", uf.Init["systemd"])
	}
	if uf.Init["myinit"] == nil {
		t.Error("project-added init myinit missing")
	}
	if uf.Init["supervisord"] == nil {
		t.Error("embedded init supervisord missing")
	}

	// resource
	if r := uf.Resource["nvidia-gpu"]; r == nil || r.Gpu == nil || r.Gpu.Vendor != "marker-vendor" {
		t.Errorf("resource nvidia-gpu override lost; got %+v", uf.Resource["nvidia-gpu"])
	}
	if uf.Resource["amd-gpu"] == nil {
		t.Error("project-added resource amd-gpu missing")
	}
}

// TestProjectVocabOverride_IsSchemaValidated proves the Req #2 boundary: a
// project's vocabulary override is validated against the SAME #Kind schemas as
// the embedded defaults (validateVocabularyCollections — the shared helper). An
// unknown key (typo) in a project builder override is rejected by the closed
// #Builder, exactly as it would be in the embedded charly.cue.
func TestProjectVocabOverride_IsSchemaValidated(t *testing.T) {
	proj := []byte(`version: ` + LatestSchemaVersion().String() + `
builder:
  badbuilder:
    bogus_field: true
`)
	doc, err := cueDocFromYAML("proj.yml", proj)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	var viol []string
	validateVocabularyCollections(doc, []string{"builder"}, "proj.yml",
		func(format string, args ...any) { viol = append(viol, fmt.Sprintf(format, args...)) })
	if len(viol) == 0 {
		t.Error("expected closed #Builder to reject unknown key bogus_field in a project builder override")
	}
}

// TestNoHardcodedYAMLFilenames is the file-agnostic invariant guard. charly.yml
// is the ONE YAML filename the code knows. The binary-embedded default config is
// authored in CUE (//go:embed charly.cue, embed_defaults.go) and compiled to data
// by the CUE-source front-end. Outside migration code (which must name legacy
// files to migrate FROM) and tests, no source may hardcode a per-kind project
// filename — discovery + the UnifiedFileName constant cover them all. build.yml
// and sidecar.yml are now legacy filenames (named only in migration + tests);
// deploy.yml (per-machine host state) and charly.yml are deliberately NOT in the
// forbidden set.
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
