package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Check defaults
	if cfg.Defaults.Registry != "ghcr.io/test" {
		t.Errorf("Defaults.Registry = %q, want %q", cfg.Defaults.Registry, "ghcr.io/test")
	}
	if len(cfg.Defaults.Build) != 1 || cfg.Defaults.Build[0] != "rpm" {
		t.Errorf("Defaults.Build = %v, want [rpm]", cfg.Defaults.Build)
	}

	// Check images exist
	expectedImages := []string{"base", "cuda", "ml-cuda", "inference", "ubuntu-dev", "bazzite"}
	for _, name := range expectedImages {
		if _, ok := cfg.Images[name]; !ok {
			t.Errorf("missing image %q", name)
		}
	}
}

func TestResolveImage(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	tests := []struct {
		name           string
		imageName      string
		calverTag      string
		wantBase       string
		wantIsExternal bool
		wantPkg        string
		wantTag        string
		wantPlatforms  []string
		wantBootc      bool
	}{
		{
			name:           "base image inherits defaults",
			imageName:      "base",
			calverTag:      "2026.45.1415",
			wantBase:       "quay.io/fedora/fedora:43",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.45.1415", // auto -> calver
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
			wantBootc:      false,
		},
		{
			name:           "cuda overrides platforms",
			imageName:      "cuda",
			calverTag:      "2026.45.1415",
			wantBase:       "quay.io/fedora/fedora:43",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.45.1415",
			wantPlatforms:  []string{"linux/amd64"},
			wantBootc:      false,
		},
		{
			name:           "ml-cuda has internal base",
			imageName:      "ml-cuda",
			calverTag:      "2026.45.1415",
			wantBase:       "cuda",
			wantIsExternal: false,
			wantPkg:        "rpm",
			wantTag:        "2026.45.1415",
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
			wantBootc:      false,
		},
		{
			name:           "inference has pinned tag",
			imageName:      "inference",
			calverTag:      "2026.45.1415",
			wantBase:       "ml-cuda",
			wantIsExternal: false,
			wantPkg:        "rpm",
			wantTag:        "nightly", // pinned, not calver
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
			wantBootc:      false,
		},
		{
			name:           "ubuntu-dev uses deb",
			imageName:      "ubuntu-dev",
			calverTag:      "2026.45.1415",
			wantBase:       "ubuntu:24.04",
			wantIsExternal: true,
			wantPkg:        "deb",
			wantTag:        "2026.45.1415",
			wantPlatforms:  []string{"linux/amd64", "linux/arm64"},
			wantBootc:      false,
		},
		{
			name:           "bazzite is bootc",
			imageName:      "bazzite",
			calverTag:      "2026.45.1415",
			wantBase:       "ghcr.io/ublue-os/bazzite:stable",
			wantIsExternal: true,
			wantPkg:        "rpm",
			wantTag:        "2026.45.1415",
			wantPlatforms:  []string{"linux/amd64"},
			wantBootc:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cfg.ResolveImage(tt.imageName, tt.calverTag, "")
			if err != nil {
				t.Fatalf("ResolveImage() error = %v", err)
			}

			if resolved.Base != tt.wantBase {
				t.Errorf("Base = %q, want %q", resolved.Base, tt.wantBase)
			}
			if resolved.IsExternalBase != tt.wantIsExternal {
				t.Errorf("IsExternalBase = %v, want %v", resolved.IsExternalBase, tt.wantIsExternal)
			}
			if resolved.Pkg != tt.wantPkg {
				t.Errorf("Pkg = %q, want %q", resolved.Pkg, tt.wantPkg)
			}
			if resolved.Tag != tt.wantTag {
				t.Errorf("Tag = %q, want %q", resolved.Tag, tt.wantTag)
			}
			if !reflect.DeepEqual(resolved.Platforms, tt.wantPlatforms) {
				t.Errorf("Platforms = %v, want %v", resolved.Platforms, tt.wantPlatforms)
			}
			if resolved.Bootc != tt.wantBootc {
				t.Errorf("Bootc = %v, want %v", resolved.Bootc, tt.wantBootc)
			}
		})
	}
}

func TestResolveImageNotFound(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	_, err = cfg.ResolveImage("nonexistent", "2026.45.1415", "")
	if err == nil {
		t.Error("ResolveImage() expected error for nonexistent image")
	}
}

func TestImageNames(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	names := cfg.ImageNames()
	// 7 total images in testdata, but disabled-image is excluded
	if len(names) != 6 {
		t.Errorf("ImageNames() returned %d names, want 6: %v", len(names), names)
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("ImageNames() not sorted: %v", names)
			break
		}
	}

	// disabled-image should not appear
	for _, name := range names {
		if name == "disabled-image" {
			t.Error("ImageNames() should not include disabled-image")
		}
	}
}

func TestResolveImageBuilders(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   BuilderMap{"pixi": "default-builder", "npm": "default-builder"},
		},
		Images: map[string]ImageConfig{
			"default-builder": {Layers: []string{}},
			"custom-builder":  {Layers: []string{}},
			"uses-default":    {Layers: []string{}},
			"uses-custom":     {Layers: []string{}, Builder: BuilderMap{"pixi": "custom-builder"}},
		},
	}

	// Image with no explicit builder inherits defaults.builder
	resolved, err := cfg.ResolveImage("uses-default", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if resolved.Builder.BuilderFor("pixi") != "default-builder" {
		t.Errorf("Builder[pixi] = %q, want %q", resolved.Builder.BuilderFor("pixi"), "default-builder")
	}

	// Image with explicit builder overrides defaults per-type
	resolved, err = cfg.ResolveImage("uses-custom", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if resolved.Builder.BuilderFor("pixi") != "custom-builder" {
		t.Errorf("Builder[pixi] = %q, want %q", resolved.Builder.BuilderFor("pixi"), "custom-builder")
	}
	// npm should still be inherited from defaults
	if resolved.Builder.BuilderFor("npm") != "default-builder" {
		t.Errorf("Builder[npm] = %q, want %q", resolved.Builder.BuilderFor("npm"), "default-builder")
	}

	// No defaults.builder → empty
	cfg2 := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}, Platforms: []string{"linux/amd64"}},
		Images: map[string]ImageConfig{
			"app": {Layers: []string{}},
		},
	}
	resolved, err = cfg2.ResolveImage("app", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if len(resolved.Builder) != 0 {
		t.Errorf("Builder = %v, want empty", resolved.Builder)
	}

	// Self-reference filtered out
	cfg3 := &Config{
		Defaults: ImageConfig{
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   BuilderMap{"pixi": "my-builder"},
		},
		Images: map[string]ImageConfig{
			"my-builder": {Layers: []string{}},
		},
	}
	resolved, err = cfg3.ResolveImage("my-builder", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if resolved.Builder.HasBuilder("pixi") {
		t.Errorf("Self-referencing builder should be filtered, got %v", resolved.Builder)
	}

	// Inheritance from base image
	cfg4 := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"pac"}, Platforms: []string{"linux/amd64"}},
		Images: map[string]ImageConfig{
			"base-img":    {Build: BuildFormats{"pac"}, Layers: []string{}, Builder: BuilderMap{"aur": "aur-builder"}},
			"aur-builder": {Layers: []string{}},
			"child-img":   {Base: "base-img", Layers: []string{}},
		},
	}
	resolved, err = cfg4.ResolveImage("child-img", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if resolved.Builder.BuilderFor("aur") != "aur-builder" {
		t.Errorf("Builder[aur] = %q, want %q (inherited from base)", resolved.Builder.BuilderFor("aur"), "aur-builder")
	}
}

func TestResolveImagePorts(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Ports:     []string{"80:80"},
		},
		Images: map[string]ImageConfig{
			"with-ports":    {Layers: []string{}, Ports: []string{"9090:9090"}},
			"inherit-ports": {Layers: []string{}},
			"no-ports":      {Layers: []string{}, Ports: []string{}},
		},
	}

	// Image with explicit ports
	resolved, err := cfg.ResolveImage("with-ports", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Ports, []string{"9090:9090"}) {
		t.Errorf("Ports = %v, want [9090:9090]", resolved.Ports)
	}

	// Image inheriting default ports
	resolved, err = cfg.ResolveImage("inherit-ports", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Ports, []string{"80:80"}) {
		t.Errorf("Ports = %v, want [80:80]", resolved.Ports)
	}

	// Image with empty ports (no inheritance since explicitly empty slice won't be set via JSON)
	resolved, err = cfg.ResolveImage("no-ports", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}
	// Empty slice in JSON becomes nil after unmarshal, but in Go struct it's []string{}
	// When len == 0, we fall through to defaults
	if !reflect.DeepEqual(resolved.Ports, []string{"80:80"}) {
		t.Errorf("Ports = %v, want [80:80]", resolved.Ports)
	}
}

func TestFullTag(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	resolved, err := cfg.ResolveImage("base", "2026.45.1415", "")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}

	want := "ghcr.io/test/base:2026.45.1415"
	if resolved.FullTag != want {
		t.Errorf("FullTag = %q, want %q", resolved.FullTag, want)
	}
}

func TestEnabledField(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// disabled-image exists in raw config
	disabledImg, ok := cfg.Images["disabled-image"]
	if !ok {
		t.Fatal("disabled-image not found in raw config")
	}
	if disabledImg.IsEnabled() {
		t.Error("disabled-image should not be enabled")
	}

	// disabled-image is excluded from ImageNames()
	for _, name := range cfg.ImageNames() {
		if name == "disabled-image" {
			t.Error("disabled-image should not appear in ImageNames()")
		}
	}

	// disabled-image is excluded from ResolveAllImages()
	all, err := cfg.ResolveAllImages("test", "")
	if err != nil {
		t.Fatalf("ResolveAllImages() error = %v", err)
	}
	if _, ok := all["disabled-image"]; ok {
		t.Error("disabled-image should not appear in ResolveAllImages()")
	}

	// ResolveImage returns error for disabled image
	_, err = cfg.ResolveImage("disabled-image", "test", "")
	if err == nil {
		t.Error("ResolveImage() should return error for disabled image")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got: %v", err)
	}

	// Enabled images still work
	_, err = cfg.ResolveImage("base", "test", "")
	if err != nil {
		t.Errorf("ResolveImage() unexpected error for enabled image: %v", err)
	}
}

func TestResolveImageDistroBaseChain(t *testing.T) {
	// Tests that distro: tags propagate through the entire base chain,
	// not just the immediate parent.
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			// Level 0: defines distro
			"fedora": {
				Base:   "quay.io/fedora/fedora:43",
				Distro: []string{"fedora:43", "fedora"},
				Layers: []string{},
			},
			// Level 1: no distro set, should inherit from fedora
			"fedora-nonfree": {
				Base:   "fedora",
				Layers: []string{},
			},
			// Level 2: no distro set, should inherit through fedora-nonfree -> fedora
			"nvidia": {
				Base:   "fedora-nonfree",
				Layers: []string{},
			},
			// Level 3: no distro set, should inherit through nvidia -> fedora-nonfree -> fedora
			"ml-app": {
				Base:   "nvidia",
				Layers: []string{},
			},
		},
	}

	tests := []struct {
		name       string
		imageName  string
		wantDistro []string
	}{
		{"level 0: defines distro", "fedora", []string{"fedora:43", "fedora"}},
		{"level 1: inherits from parent", "fedora-nonfree", []string{"fedora:43", "fedora"}},
		{"level 2: inherits through chain", "nvidia", []string{"fedora:43", "fedora"}},
		{"level 3: inherits through deep chain", "ml-app", []string{"fedora:43", "fedora"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cfg.ResolveImage(tt.imageName, "test", "")
			if err != nil {
				t.Fatalf("ResolveImage() error = %v", err)
			}
			if !reflect.DeepEqual(resolved.Distro, tt.wantDistro) {
				t.Errorf("Distro = %v, want %v", resolved.Distro, tt.wantDistro)
			}
		})
	}
}

func TestResolveImageBuildBaseChain(t *testing.T) {
	// Tests that build: formats propagate through the base chain.
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			// Level 0: defines build
			"archlinux": {
				Base:   "docker.io/library/archlinux:latest",
				Build:  BuildFormats{"pac"},
				Layers: []string{},
			},
			// Level 1: no build set, should inherit from archlinux
			"arch-extended": {
				Base:   "archlinux",
				Layers: []string{},
			},
			// Level 2: no build set, should inherit through chain
			"arch-app": {
				Base:   "arch-extended",
				Layers: []string{},
			},
		},
	}

	tests := []struct {
		name      string
		imageName string
		wantBuild []string
	}{
		{"level 0: defines build", "archlinux", []string{"pac"}},
		{"level 1: inherits from parent", "arch-extended", []string{"pac"}},
		{"level 2: inherits through chain", "arch-app", []string{"pac"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cfg.ResolveImage(tt.imageName, "test", "")
			if err != nil {
				t.Fatalf("ResolveImage() error = %v", err)
			}
			if !reflect.DeepEqual(resolved.BuildFormats, tt.wantBuild) {
				t.Errorf("BuildFormats = %v, want %v", resolved.BuildFormats, tt.wantBuild)
			}
		})
	}
}
