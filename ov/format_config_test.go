package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadDistroConfigFromFile(t *testing.T) {
	distroCfg, _, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		t.Fatalf("loading distro config: %v", err)
	}
	if distroCfg == nil || len(distroCfg.Distro) == 0 {
		t.Fatal("expected non-empty distro config")
	}

	// Check fedora exists
	fedora, ok := distroCfg.Distro["fedora"]
	if !ok {
		t.Fatal("expected fedora distro definition")
	}
	if fedora.Bootstrap.InstallCmd == "" {
		t.Error("fedora bootstrap.install_cmd is empty")
	}
	if len(fedora.Bootstrap.CacheMount) == 0 {
		t.Error("fedora bootstrap.cache_mounts is empty")
	}
	if fedora.Bootstrap.CacheMount[0].Dst != "/var/cache/libdnf5" {
		t.Errorf("fedora cache mount = %q, want /var/cache/libdnf5", fedora.Bootstrap.CacheMount[0].Dst)
	}

	// Check fedora has rpm format
	if fedora.Format == nil || fedora.Format["rpm"] == nil {
		t.Fatal("expected fedora to have rpm format")
	}
	rpm := fedora.Format["rpm"]
	if rpm.InstallTemplate == "" {
		t.Error("rpm install_template is empty")
	}
	if len(rpm.CacheMount) == 0 {
		t.Error("rpm cache_mounts is empty")
	}
	if len(rpm.SectionFields) == 0 {
		t.Error("rpm section_fields is empty")
	}

	// Check ubuntu inherits debian (including formats)
	ubuntu, ok := distroCfg.Distro["ubuntu"]
	if !ok {
		t.Fatal("expected ubuntu distro definition")
	}
	if ubuntu.Inherits != "debian" {
		t.Errorf("ubuntu.inherits = %q, want debian", ubuntu.Inherits)
	}

	// Test ResolveDistro
	resolved := distroCfg.ResolveDistro([]string{"fedora:43", "fedora"})
	if resolved == nil {
		t.Fatal("ResolveDistro returned nil for fedora:43")
	}
	if resolved.Bootstrap.InstallCmd != fedora.Bootstrap.InstallCmd {
		t.Error("ResolveDistro did not resolve to fedora")
	}

	// Test inherits resolution includes formats
	resolvedUbuntu := distroCfg.ResolveDistro([]string{"ubuntu"})
	if resolvedUbuntu == nil {
		t.Fatal("ResolveDistro returned nil for ubuntu")
	}
	if resolvedUbuntu.Bootstrap.InstallCmd == "" {
		t.Error("ubuntu should inherit debian's bootstrap install_cmd")
	}
	if resolvedUbuntu.Format == nil || resolvedUbuntu.Format["deb"] == nil {
		t.Error("ubuntu should inherit debian's deb format")
	}

	// Check arch has both pac and aur formats
	archResolved := distroCfg.ResolveDistro([]string{"arch"})
	if archResolved == nil {
		t.Fatal("ResolveDistro returned nil for arch")
	}
	if archResolved.Format["pac"] == nil {
		t.Error("arch should have pac format")
	}
	if archResolved.Format["aur"] == nil {
		t.Error("arch should have aur format")
	}
}

func TestAllFormatNames(t *testing.T) {
	dc := testDistroConfig()
	names := dc.AllFormatNames()
	if len(names) != 4 {
		t.Errorf("expected 4 format names, got %d: %v", len(names), names)
	}
	// Should be sorted
	if names[0] != "aur" || names[1] != "deb" || names[2] != "pac" || names[3] != "rpm" {
		t.Errorf("format names not sorted: %v", names)
	}
}

func TestValidFormat(t *testing.T) {
	dc := testDistroConfig()
	for _, name := range []string{"rpm", "deb", "pac", "aur"} {
		if !dc.ValidFormat(name) {
			t.Errorf("expected format %q to be valid", name)
		}
	}
	if dc.ValidFormat("apk") {
		t.Error("apk should not be valid in default config")
	}
}

func TestLoadBuilderConfigFromFile(t *testing.T) {
	_, builderCfg, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		t.Fatalf("loading builder config: %v", err)
	}
	if builderCfg == nil || len(builderCfg.Builder) == 0 {
		t.Fatal("expected non-empty builder config")
	}

	// Check all four builders exist (pixi, npm, cargo, aur)
	for _, name := range []string{"pixi", "npm", "cargo", "aur"} {
		if !builderCfg.ValidBuilderType(name) {
			t.Errorf("expected builder %q to be valid", name)
		}
	}

	// Check pixi detect files
	pixi := builderCfg.Builder["pixi"]
	if len(pixi.DetectFiles) == 0 {
		t.Error("pixi detect_files is empty")
	}
	if pixi.StageTemplate == "" {
		t.Error("pixi stage_template is empty")
	}

	// Check cargo is inline
	cargo := builderCfg.Builder["cargo"]
	if !cargo.Inline {
		t.Error("cargo should be inline")
	}
	if !cargo.RequiresSrcDir {
		t.Error("cargo should require src dir")
	}
}

func TestBuilderNames(t *testing.T) {
	_, builderCfg, _, _ := LoadBuildConfigForImage(testdataDir)
	names := builderCfg.BuilderNames()
	if len(names) != 4 {
		t.Errorf("expected 4 builder names, got %d: %v", len(names), names)
	}
}

func TestDynamicFormatSectionParsing(t *testing.T) {
	// Ensure format names are registered for YAML parsing
	SetFormatNames(testDistroConfig())

	// Test that YAML with format sections parses into FormatSections
	yamlData := `
rpm:
  package:
    - vim
    - git
  copr:
    - owner/repo
  repo:
    - name: test
      url: https://example.com/repo
  option:
    - --nogpgcheck
`
	var ly LayerYAML
	if err := yaml.Unmarshal([]byte(yamlData), &ly); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	section, ok := ly.FormatSections["rpm"]
	if !ok {
		t.Fatal("expected rpm format section")
	}
	if section.FormatName != "rpm" {
		t.Errorf("FormatName = %q, want rpm", section.FormatName)
	}
	if len(section.Packages) != 2 {
		t.Errorf("Packages count = %d, want 2", len(section.Packages))
	}
	if section.Raw == nil {
		t.Fatal("Raw is nil")
	}
	if _, ok := section.Raw["copr"]; !ok {
		t.Error("Raw missing copr field")
	}
	if _, ok := section.Raw["repo"]; !ok {
		t.Error("Raw missing repo field")
	}
	if _, ok := section.Raw["option"]; !ok {
		t.Error("Raw missing option field")
	}
}

func TestAurBuilderDetectConfig(t *testing.T) {
	builderCfg := testBuilderCfg()
	aur := builderCfg.Builder["aur"]
	if aur == nil {
		t.Fatal("expected aur builder definition")
	}
	if aur.DetectConfig != "aur" {
		t.Errorf("aur detect_config = %q, want \"aur\"", aur.DetectConfig)
	}
	if aur.StageTemplate == "" {
		t.Error("aur stage_template is empty")
	}
}

// ResolveFormatConfigData tests removed — the helper was deleted as part of
// the unified-cutover (format_config: field replaced by overthink.yml's
// includes: mechanism).

func TestLoadBuildConfigForImageFallback(t *testing.T) {
	// Post-unified-cutover there is no per-image / per-default fallback — the
	// unified loader reads overthink.yml in the project directory. This test
	// now verifies that reading via LoadBuildConfigForImage(dir) produces the
	// same config twice (i.e., is deterministic and idempotent).
	distroCfg, builderCfg, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if distroCfg == nil || len(distroCfg.Distro) == 0 {
		t.Error("expected distro config from overthink.yml")
	}
	if builderCfg == nil || len(builderCfg.Builder) == 0 {
		t.Error("expected builder config from overthink.yml")
	}

	distroCfg2, _, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if distroCfg2 == nil || len(distroCfg2.Distro) == 0 {
		t.Error("expected distro config from default ref")
	}
}

func TestDnfConfigParse(t *testing.T) {
	var d DistroDef
	if err := yaml.Unmarshal([]byte("dnf:\n  max_parallel_downloads: 10\n  fastestmirror: true\n"), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Dnf == nil {
		t.Fatal("expected Dnf parsed")
	}
	if d.Dnf.MaxParallelDownloads != 10 || !d.Dnf.Fastestmirror {
		t.Errorf("Dnf = %+v, want {10 true}", *d.Dnf)
	}
}

// TestDnfConfigInherit verifies a child distro inherits the parent's Dnf when
// it declares none, and its own Dnf wins when set — same per-field merge as
// the other DistroDef sub-blocks (BaseUser, Pacstrap, …).
func TestDnfConfigInherit(t *testing.T) {
	dc := &DistroConfig{Distro: map[string]*DistroDef{
		"fedora": {
			Bootstrap: BootstrapDef{InstallCmd: "dnf install -y"},
			Dnf:       &DnfConfig{MaxParallelDownloads: 10, Fastestmirror: true},
		},
		"fedora-child":  {Inherits: "fedora"},                                           // no own Dnf → inherits
		"fedora-child2": {Inherits: "fedora", Dnf: &DnfConfig{MaxParallelDownloads: 3}}, // own Dnf wins
	}}

	got := dc.ResolveDistro([]string{"fedora-child"})
	if got == nil || got.Dnf == nil {
		t.Fatal("expected inherited Dnf on child")
	}
	if got.Dnf.MaxParallelDownloads != 10 || !got.Dnf.Fastestmirror {
		t.Errorf("inherited Dnf = %+v, want {10 true}", *got.Dnf)
	}

	got2 := dc.ResolveDistro([]string{"fedora-child2"})
	if got2 == nil || got2.Dnf == nil || got2.Dnf.MaxParallelDownloads != 3 {
		t.Errorf("child's own Dnf should win, got %+v", got2.Dnf)
	}
}
