package main

import (
	"testing"
)

func TestLoadEmbeddedDistroConfig(t *testing.T) {
	cfg, err := loadDistroConfig("/nonexistent")
	if err != nil {
		t.Fatalf("loading embedded distro config: %v", err)
	}
	if cfg == nil || len(cfg.Distros) == 0 {
		t.Fatal("expected non-empty distro config")
	}

	// Check fedora exists
	fedora, ok := cfg.Distros["fedora"]
	if !ok {
		t.Fatal("expected fedora distro definition")
	}
	if fedora.Bootstrap.InstallCmd == "" {
		t.Error("fedora bootstrap.install_cmd is empty")
	}
	if len(fedora.Bootstrap.CacheMounts) == 0 {
		t.Error("fedora bootstrap.cache_mounts is empty")
	}
	if fedora.Bootstrap.CacheMounts[0].Dst != "/var/cache/libdnf5" {
		t.Errorf("fedora cache mount = %q, want /var/cache/libdnf5", fedora.Bootstrap.CacheMounts[0].Dst)
	}

	// Check ubuntu inherits debian
	ubuntu, ok := cfg.Distros["ubuntu"]
	if !ok {
		t.Fatal("expected ubuntu distro definition")
	}
	if ubuntu.Inherits != "debian" {
		t.Errorf("ubuntu.inherits = %q, want debian", ubuntu.Inherits)
	}

	// Test ResolveDistro
	resolved := cfg.ResolveDistro([]string{"fedora:43", "fedora"})
	if resolved == nil {
		t.Fatal("ResolveDistro returned nil for fedora:43")
	}
	if resolved.Bootstrap.InstallCmd != fedora.Bootstrap.InstallCmd {
		t.Error("ResolveDistro did not resolve to fedora")
	}

	// Test inherits resolution
	resolvedUbuntu := cfg.ResolveDistro([]string{"ubuntu"})
	if resolvedUbuntu == nil {
		t.Fatal("ResolveDistro returned nil for ubuntu")
	}
	if resolvedUbuntu.Bootstrap.InstallCmd == "" {
		t.Error("ubuntu should inherit debian's bootstrap install_cmd")
	}
}

func TestLoadEmbeddedBuildConfig(t *testing.T) {
	cfg, err := loadBuildConfig("/nonexistent")
	if err != nil {
		t.Fatalf("loading embedded build config: %v", err)
	}
	if cfg == nil || len(cfg.Formats) == 0 {
		t.Fatal("expected non-empty build config")
	}

	// Check all four formats exist
	for _, name := range []string{"rpm", "deb", "pac", "aur"} {
		if !cfg.ValidFormat(name) {
			t.Errorf("expected format %q to be valid", name)
		}
	}

	// Check unknown format is invalid
	if cfg.ValidFormat("apk") {
		t.Error("apk should not be valid in default config")
	}

	// Check rpm has install template
	rpm := cfg.Formats["rpm"]
	if rpm.InstallTemplate == "" {
		t.Error("rpm install_template is empty")
	}
	if len(rpm.CacheMounts) == 0 {
		t.Error("rpm cache_mounts is empty")
	}
	if len(rpm.SectionFields) == 0 {
		t.Error("rpm section_fields is empty")
	}

	// Check aur format exists and has install template
	aur := cfg.Formats["aur"]
	if aur == nil {
		t.Fatal("expected aur format definition")
	}
	if aur.InstallTemplate == "" {
		t.Error("aur install_template is empty")
	}
}

func TestLoadEmbeddedBuilderConfig(t *testing.T) {
	cfg, err := loadBuilderConfig("/nonexistent")
	if err != nil {
		t.Fatalf("loading embedded builder config: %v", err)
	}
	if cfg == nil || len(cfg.Builders) == 0 {
		t.Fatal("expected non-empty builder config")
	}

	// Check all four builders exist (pixi, npm, cargo, aur)
	for _, name := range []string{"pixi", "npm", "cargo", "aur"} {
		if !cfg.ValidBuilderType(name) {
			t.Errorf("expected builder %q to be valid", name)
		}
	}

	// Check pixi detect files
	pixi := cfg.Builders["pixi"]
	if len(pixi.DetectFiles) == 0 {
		t.Error("pixi detect_files is empty")
	}
	if pixi.StageTemplate == "" {
		t.Error("pixi stage_template is empty")
	}

	// Check cargo is inline
	cargo := cfg.Builders["cargo"]
	if !cargo.Inline {
		t.Error("cargo should be inline")
	}
	if !cargo.RequiresSrcDir {
		t.Error("cargo should require src dir")
	}
}

func TestFormatNames(t *testing.T) {
	cfg, _ := loadBuildConfig("/nonexistent")
	names := cfg.FormatNames()
	if len(names) != 4 {
		t.Errorf("expected 4 format names, got %d: %v", len(names), names)
	}
	// Should be sorted
	if names[0] != "aur" || names[1] != "deb" || names[2] != "pac" || names[3] != "rpm" {
		t.Errorf("format names not sorted: %v", names)
	}
}

func TestBuilderNames(t *testing.T) {
	cfg, _ := loadBuilderConfig("/nonexistent")
	names := cfg.BuilderNames()
	if len(names) != 4 {
		t.Errorf("expected 4 builder names, got %d: %v", len(names), names)
	}
}

func TestTypedToPackageSection(t *testing.T) {
	rpm := &RpmConfig{
		Packages: []string{"vim", "git"},
		Copr:     []string{"owner/repo"},
		Repos: []RpmRepo{
			{Name: "test", URL: "https://example.com/repo"},
		},
		Options: []string{"--nogpgcheck"},
	}

	section := typedToPackageSection("rpm", rpm)
	if section == nil {
		t.Fatal("expected non-nil section")
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
	// Verify raw fields are accessible
	if _, ok := section.Raw["copr"]; !ok {
		t.Error("Raw missing copr field")
	}
	if _, ok := section.Raw["repos"]; !ok {
		t.Error("Raw missing repos field")
	}
	if _, ok := section.Raw["options"]; !ok {
		t.Error("Raw missing options field")
	}
}

func TestAurBuilderDetectConfig(t *testing.T) {
	cfg, _ := loadBuilderConfig("/nonexistent")
	aur := cfg.Builders["aur"]
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
