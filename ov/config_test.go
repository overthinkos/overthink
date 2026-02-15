package main

import (
	"reflect"
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
	if cfg.Defaults.Pkg != "rpm" {
		t.Errorf("Defaults.Pkg = %q, want %q", cfg.Defaults.Pkg, "rpm")
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
			wantBase:       "fedora:43",
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
			wantBase:       "fedora:43",
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
			resolved, err := cfg.ResolveImage(tt.imageName, tt.calverTag)
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

	_, err = cfg.ResolveImage("nonexistent", "2026.45.1415")
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
	if len(names) != 6 {
		t.Errorf("ImageNames() returned %d names, want 6", len(names))
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("ImageNames() not sorted: %v", names)
			break
		}
	}
}

func TestFullTag(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	resolved, err := cfg.ResolveImage("base", "2026.45.1415")
	if err != nil {
		t.Fatalf("ResolveImage() error = %v", err)
	}

	want := "ghcr.io/test/base:2026.45.1415"
	if resolved.FullTag != want {
		t.Errorf("FullTag = %q, want %q", resolved.FullTag, want)
	}
}
