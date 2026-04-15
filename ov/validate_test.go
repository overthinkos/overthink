package main

import (
	"strings"
	"testing"
)

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"base": {Layers: []string{"pixi"}},
		},
	}

	layers := map[string]*Layer{
		"pixi": {
			Name:       "pixi",
			HasRootYml: true,
			HasUserYml: true,
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateInvalidPkg(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build: BuildFormats{"invalid"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, ".")
	if err == nil {
		t.Error("expected error for invalid pkg")
	}
	if !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingLayer(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"nonexistent"}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing layer")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingLayerWithTypo(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"pixie"}}, // typo for "pixi"
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing layer")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected typo suggestion, got: %v", err)
	}
}

func TestValidateLayerNoInstallFiles(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"empty": {Name: "empty"}, // no install files
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for layer without install files")
	}
	if !strings.Contains(err.Error(), "must have at least one install file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCargoWithoutSrc(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"tool": {
			Name:         "tool",
			HasCargoToml: true,
			HasSrcDir:    false,
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for Cargo.toml without src/")
	}
	if !strings.Contains(err.Error(), "requires src/") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCoprWithoutPackages(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:       "layer",
			HasRootYml: true,
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]interface{}{"copr": []interface{}{"owner/project"}}},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for rpm.copr without rpm.packages")
	}
	if !strings.Contains(err.Error(), "rpm.copr requires rpm.packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateReposWithoutPackages(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:       "layer",
			HasRootYml: true, // needs some install file
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]interface{}{"repos": []interface{}{map[string]interface{}{"name": "test", "url": "http://example.com"}}}},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for rpm.repos without rpm.packages")
	}
	if !strings.Contains(err.Error(), "rpm.repos requires rpm.packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateModulesWithoutPackages(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:       "layer",
			HasRootYml: true,
			formatSections: map[string]*PackageSection{
				"rpm": {FormatName: "rpm", Raw: map[string]interface{}{"modules": []interface{}{"valkey:remi-9.0"}}},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for rpm.modules without rpm.packages")
	}
	if !strings.Contains(err.Error(), "rpm.modules requires rpm.packages") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateRepoUrlAndRpmBothSet removed — format-specific validation
// rules (e.g., "exactly one of url or rpm") are now in distro.yml validate
// section, not in Go code.

// TestValidateRepoNeitherUrlNorRpm removed — format-specific validation now in distro.yml

func TestValidatePacPkgValue(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"pac"}},
		Images:   map[string]ImageConfig{},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("pkg: pac should be valid, got error: %v", err)
	}
}

func TestValidateInvalidPkgValue(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build: BuildFormats{"zypper"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, ".")
	if err == nil {
		t.Error("expected error for invalid pkg value")
	}
	if !strings.Contains(err.Error(), "is not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidatePacReposMissingServer removed — format-specific field requirements
// (pac repos must have server) are now in distro.yml validate rules, not Go code.

func TestValidatePacReposMissingName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:      "layer",
			formatSections: map[string]*PackageSection{
				"pac": {FormatName: "pac", Packages: []string{"pkg"}, Raw: map[string]interface{}{
					"packages": []interface{}{"pkg"},
					"repos":    []interface{}{map[string]interface{}{"server": "https://example.com"}},
				}},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for pac.repos without name")
	}
	if !strings.Contains(err.Error(), "requires name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAurWithoutAurBuilder(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build: BuildFormats{"pac"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{
			"arch-img": {
				Base:   "archlinux:latest",
				Build:  BuildFormats{"pac"},
				Layers: []string{"aur-layer"},
			},
		},
	}
	layers := map[string]*Layer{
		"aur-layer": {
			Name:      "aur-layer",
			formatSections: map[string]*PackageSection{
				"aur": {FormatName: "aur", Packages: []string{"yay-bin"}, Raw: map[string]interface{}{"packages": []interface{}{"yay-bin"}}},
			},
		},
	}

	err := Validate(cfg, layers, ".")
	if err == nil {
		t.Fatal("expected error for aur packages without builders.aur")
	}
	if !strings.Contains(err.Error(), "no builders.aur configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateUnknownDependency(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:       "layer",
			HasRootYml: true,
			Depends:    []string{"unknown"},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for unknown dependency")
	}
	if !strings.Contains(err.Error(), "unknown layer") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageCycle(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"a": {Base: "b", Layers: []string{}},
			"b": {Base: "c", Layers: []string{}},
			"c": {Base: "a", Layers: []string{}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for image cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateLayerCycle(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"a"}},
		},
	}
	layers := map[string]*Layer{
		"a": {Name: "a", HasRootYml: true, Depends: []string{"b"}},
		"b": {Name: "b", HasRootYml: true, Depends: []string{"c"}},
		"c": {Name: "c", HasRootYml: true, Depends: []string{"a"}},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for layer cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMultipleErrors(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"invalid"}},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"missing1", "missing2"}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected errors")
	}

	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	// Should have at least 3 errors: invalid pkg, two missing layers
	if len(valErr.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(valErr.Errors), valErr.Errors)
	}
}

func TestValidateLayerPortsValid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"web": {
			Name:       "web",
			HasUserYml: true,
			HasPorts:   true,
			ports:      []string{"8080", "9090"},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateLayerPortsInvalid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"web": {
			Name:       "web",
			HasUserYml: true,
			HasPorts:   true,
			ports:      []string{"99999"},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid port number")
	}
	if !strings.Contains(err.Error(), "not a valid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateLayerPortsInvalidFromYAML(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"web": {
			Name:       "web",
			HasUserYml: true,
			HasPorts:   true,
			ports:      []string{"0"},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid port number")
	}
	if !strings.Contains(err.Error(), "layer.yml ports") {
		t.Errorf("expected layer.yml reference in error, got: %v", err)
	}
}

func TestValidateImagePortsValid(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"8080:8080", "9090"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateImagePortsInvalid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"abc:8080"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid port mapping")
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImagePortsBadFormat(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"8080:9090:1234"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for bad port format")
	}
	if !strings.Contains(err.Error(), "host:container") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteMissingHost(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "", Port: "8080"},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for route missing host")
	}
	if !strings.Contains(err.Error(), "missing required \"host\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteMissingPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: ""},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for route missing port")
	}
	if !strings.Contains(err.Error(), "missing required \"port\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteInvalidPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: "99999"},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for route invalid port")
	}
	if !strings.Contains(err.Error(), "not a valid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteWithoutTraefik(t *testing.T) {
	// Route without traefik is valid — routes are generic metadata consumed by traefik or tunnel
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"svc"}},
		},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasRoute:   true,
			HasUserYml: true,
			route:      &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateRouteWithTraefik(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"traefik", "svc"}},
		},
	}
	layers := map[string]*Layer{
		"traefik": {
			Name:       "traefik",
			HasRootYml: true,
		},
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateSkipsDisabledImages(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"good": {Layers: []string{"pixi"}},
			"bad-disabled": {
				Enabled: boolPtr(false),
				Layers:  []string{"nonexistent-layer"},
				Build:   BuildFormats{"invalid"},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() should pass when bad image is disabled, got: %v", err)
	}
}

func TestValidateVolumesValid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.myapp"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateVolumesMissingName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "", Path: "~/.myapp"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing volume name")
	}
	if !strings.Contains(err.Error(), "missing required \"name\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateVolumesMissingPath(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: ""}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing volume path")
	}
	if !strings.Contains(err.Error(), "missing required \"path\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateVolumesInvalidName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "My Data!", Path: "~/.myapp"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid volume name")
	}
	if !strings.Contains(err.Error(), "lowercase alphanumeric") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateVolumesDuplicate(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes: []VolumeYAML{
				{Name: "data", Path: "~/.myapp"},
				{Name: "data", Path: "~/.other"},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for duplicate volume name")
	}
	if !strings.Contains(err.Error(), "duplicate volume name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesValid(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"test": {
				Layers:  []string{"svc"},
				Aliases: []AliasConfig{{Name: "mycli", Command: "mycli-bin"}},
			},
		},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasAliases: true,
			aliases:    []AliasYAML{{Name: "svc-cli", Command: "svc-cli-bin"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateAliasesMissingName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasAliases: true,
			aliases:    []AliasYAML{{Name: "", Command: "cmd"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing alias name")
	}
	if !strings.Contains(err.Error(), "missing required \"name\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesMissingCommand(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasAliases: true,
			aliases:    []AliasYAML{{Name: "mycli", Command: ""}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for missing alias command")
	}
	if !strings.Contains(err.Error(), "missing required \"command\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesDuplicate(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasAliases: true,
			aliases: []AliasYAML{
				{Name: "mycli", Command: "cmd1"},
				{Name: "mycli", Command: "cmd2"},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for duplicate alias name")
	}
	if !strings.Contains(err.Error(), "duplicate alias name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAliasesInvalidName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasAliases: true,
			aliases:    []AliasYAML{{Name: "-bad", Command: "cmd"}},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid alias name")
	}
	if !strings.Contains(err.Error(), "must match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageAliasesDuplicate(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"svc"},
				Aliases: []AliasConfig{
					{Name: "mycli", Command: "cmd1"},
					{Name: "mycli", Command: "cmd2"},
				},
			},
		},
	}
	layers := map[string]*Layer{
		"svc": {Name: "svc", HasUserYml: true},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for duplicate image alias name")
	}
	if !strings.Contains(err.Error(), "duplicate alias name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateSelfBuilder(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build: BuildFormats{"rpm"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{
			"myimg": {
				Layers:   []string{"pixi"},
				Builders: BuildersMap{"pixi": "myimg"},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers, ".")
	if err == nil {
		t.Fatal("expected error for self-referencing builder")
	}
	if !strings.Contains(err.Error(), "cannot reference self") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBuilderInheritedSelfNotError(t *testing.T) {
	// Builder image inheriting defaults.builders that points to itself is NOT an error
	cfg := &Config{
		Defaults: ImageConfig{
			Build:    BuildFormats{"rpm"},
			Builders: BuildersMap{"pixi": "builder", "npm": "builder"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{
			"builder": {Layers: []string{"pixi"}},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers, ".")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidatePerImageBuilderNotFound(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build: BuildFormats{"rpm"},
			FormatConfig: &FormatConfigRefs{
				Distro:  "testdata/defaults/distro.yml",
				Builder: "testdata/defaults/builder.yml",
			},
		},
		Images: map[string]ImageConfig{
			"app": {
				Layers:   []string{"pixi"},
				Builders: BuildersMap{"pixi": "nonexistent"},
			},
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers, ".")
	if err == nil {
		t.Fatal("expected error for nonexistent per-image builder")
	}
	if !strings.Contains(err.Error(), "not found in images.yml") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIsValidPort(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"80", true},
		{"8080", true},
		{"65535", true},
		{"1", true},
		{"0", false},
		{"65536", false},
		{"abc", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidPort(tt.input)
			if got != tt.want {
				t.Errorf("isValidPort(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateLayerWithIncludesNoInstallFiles(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"sway-desktop"}},
		},
	}
	layers := map[string]*Layer{
		"pipewire":     {Name: "pipewire", HasRootYml: true},
		"wayvnc":       {Name: "wayvnc", HasRootYml: true},
		"sway-desktop": {Name: "sway-desktop", IncludedLayers: []string{"pipewire", "wayvnc"}},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("expected no error for composing layer without install files, got: %v", err)
	}
}

func TestValidateLayerIncludesCycle(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"a": {Name: "a", HasRootYml: true, IncludedLayers: []string{"b"}},
		"b": {Name: "b", HasRootYml: true, IncludedLayers: []string{"a"}},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for circular layer composition")
	}
}

func TestValidateLayerIncludesMissing(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"desktop": {Name: "desktop", IncludedLayers: []string{"nonexistent"}},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for unknown layer in includes")
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"pixi", "pixi", 0},
		{"pixi", "pixie", 1},
		{"pixi", "pxi", 1},
		{"pixi", "python", 5},
	}

	for _, tt := range tests {
		t.Run(tt.a+"-"+tt.b, func(t *testing.T) {
			got := levenshteinDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestValidatePortRelayValid(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"supervisord", "socat", "chrome"}},
		},
	}
	layers := map[string]*Layer{
		"supervisord": {Name: "supervisord", Depends: []string{"python"}, HasRootYml: true, formatSections: map[string]*PackageSection{"rpm": {FormatName: "rpm", Packages: []string{"supervisor"}}}},
		"python":      {Name: "python", HasRootYml: true},
		"socat":       {Name: "socat", HasRootYml: true, formatSections: map[string]*PackageSection{"rpm": {FormatName: "rpm", Packages: []string{"socat", "iproute"}}}},
		"chrome": {
			Name:         "chrome",
			HasUserYml:   true,
			HasPorts:     true,
			ports:        []string{"9222"},
			portSpecs:    []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidatePortRelayInvalidPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:         "svc",
			HasUserYml:   true,
			HasPorts:     true,
			ports:        []string{"99999"},
			portSpecs:    []PortSpec{{Port: 99999, Protocol: "http"}},
			PortRelayPorts: []int{99999},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for invalid port_relay port")
	}
	if !strings.Contains(err.Error(), "not a valid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayNotInPorts(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:         "svc",
			HasUserYml:   true,
			HasPorts:     true,
			ports:        []string{"8080"},
			portSpecs:    []PortSpec{{Port: 8080, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for port_relay port not in layer ports")
	}
	if !strings.Contains(err.Error(), "not declared in the layer's ports") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayNoPorts(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:         "svc",
			HasUserYml:   true,
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for port_relay without ports")
	}
	if !strings.Contains(err.Error(), "no ports declared") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayDuplicate(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:         "svc",
			HasUserYml:   true,
			HasPorts:     true,
			ports:        []string{"9222"},
			portSpecs:    []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222, 9222},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for duplicate port_relay port")
	}
	if !strings.Contains(err.Error(), "duplicate port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePortRelayMissingSocat(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"chrome"}},
		},
	}
	layers := map[string]*Layer{
		"chrome": {
			Name:         "chrome",
			HasUserYml:   true,
			HasPorts:     true,
			ports:        []string{"9222"},
			portSpecs:    []PortSpec{{Port: 9222, Protocol: "http"}},
			PortRelayPorts: []int{9222},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Error("expected error for port_relay without socat layer")
	}
	if !strings.Contains(err.Error(), "missing \"socat\" layer") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateDataEntryUnknownVolume guards the check at validate.go where a
// layer's data: entry references a volume name that is not declared by any
// layer in the composed image chain. Without this guard, a typo in a data
// layer (e.g. `volume: workspae`) silently produces an image that never
// seeds its workspace, and the error only surfaces at runtime as an empty
// directory.
func TestValidateDataEntryUnknownVolume(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"jupyter": {Layers: []string{"jupyter", "notebook-templates"}},
		},
	}
	layers := map[string]*Layer{
		"jupyter": {
			Name:       "jupyter",
			HasRootYml: true,
			HasVolumes: true,
			volumes: []VolumeYAML{
				{Name: "workspace", Path: "~/workspace"},
			},
		},
		"notebook-templates": {
			Name:    "notebook-templates",
			HasData: true,
			// Typo: "workspae" instead of "workspace" — must be caught.
			data: []DataYAML{
				{Src: "data/notebooks", Volume: "workspae"},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for data entry referencing unknown volume")
	}
	if !strings.Contains(err.Error(), "workspae") {
		t.Errorf("expected error to mention unknown volume name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not declared by any layer") {
		t.Errorf("expected 'not declared by any layer' phrasing, got: %v", err)
	}
}

// TestValidateDataEntryKnownVolume is the happy path for the same check:
// when the data entry's volume matches a declared volume anywhere in the
// image's layer chain, Validate succeeds.
func TestValidateDataEntryKnownVolume(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"jupyter": {Layers: []string{"jupyter", "notebook-templates"}},
		},
	}
	layers := map[string]*Layer{
		"jupyter": {
			Name:       "jupyter",
			HasRootYml: true,
			HasVolumes: true,
			volumes: []VolumeYAML{
				{Name: "workspace", Path: "~/workspace"},
			},
		},
		"notebook-templates": {
			Name:       "notebook-templates",
			HasRootYml: true,
			HasData:    true,
			data: []DataYAML{
				{Src: "data/notebooks", Volume: "workspace"},
			},
		},
	}

	err := Validate(cfg, layers, "")
	if err != nil && strings.Contains(err.Error(), "not declared by any layer") {
		t.Errorf("unexpected 'unknown volume' error for valid data entry: %v", err)
	}
}

// ---------------------------------------------------------------------------
// secret_accepts / secret_requires validation tests (Step 3 of the
// credential-backed secrets feature). Each test exercises one of the rules in
// plan §4.4.
// ---------------------------------------------------------------------------

// secretDepsLayer builds a minimal layer with the given secret dependency
// configuration, for reuse across tests.
func secretDepsLayer(name string, opts func(l *Layer)) *Layer {
	l := &Layer{Name: name, HasRootYml: true}
	if opts != nil {
		opts(l)
	}
	return l
}

// TestValidateSecretAcceptsHappyPath — valid secret_accepts entry with an
// explicit Key override that matches the ov/<service>/<key> format. No errors.
func TestValidateSecretAcceptsHappyPath(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "OpenRouter API key", Key: "ov/api-key/openrouter"},
			}
		}),
	}
	if err := Validate(cfg, layers, ""); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

// TestValidateSecretRequiresMissingDescription — secret_requires entry with
// empty description must be rejected (consistency with env_requires).
func TestValidateSecretRequiresMissingDescription(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretRequires = true
			l.secretRequires = []EnvDependency{
				{Name: "WEBUI_ADMIN_PASSWORD"}, // no Description
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for secret_requires entry with no description")
	}
	if !strings.Contains(err.Error(), "has no description") {
		t.Errorf("expected 'has no description' error, got: %v", err)
	}
}

// TestValidateSecretAcceptsInvalidName — name with invalid chars must be
// rejected by the env-var-name check.
func TestValidateSecretAcceptsInvalidName(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "OPENROUTER-API-KEY", Description: "hyphen not allowed"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for invalid env var name in secret_accepts")
	}
	if !strings.Contains(err.Error(), "not a valid environment variable name") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsCollidesWithEnvAccepts — plan §4.4 rule 1: a name
// cannot appear in both env_accepts and secret_accepts.
func TestValidateSecretAcceptsCollidesWithEnvAccepts(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasEnvAccepts = true
			l.envAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "plaintext"},
			}
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "OPENROUTER_API_KEY", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected collision error between env_accepts and secret_accepts")
	}
	if !strings.Contains(err.Error(), "appears in both env_accepts and secret_accepts") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretRequiresCollidesWithEnvRequires — same collision check for
// requires variants.
func TestValidateSecretRequiresCollidesWithEnvRequires(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasEnvRequires = true
			l.envRequires = []EnvDependency{
				{Name: "WEBUI_ADMIN_PASSWORD", Description: "plaintext"},
			}
			l.HasSecretRequires = true
			l.secretRequires = []EnvDependency{
				{Name: "WEBUI_ADMIN_PASSWORD", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected collision error between env_requires and secret_requires")
	}
	if !strings.Contains(err.Error(), "appears in both env_requires and secret_requires") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsCollidesWithSecretRequires — a name cannot appear
// in both secret_accepts and secret_requires in the same layer.
func TestValidateSecretAcceptsCollidesWithSecretRequires(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretRequires = true
			l.secretRequires = []EnvDependency{
				{Name: "API_TOKEN", Description: "required"},
			}
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "API_TOKEN", Description: "optional"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected collision between secret_requires and secret_accepts")
	}
	if !strings.Contains(err.Error(), "appears in both secret_requires and secret_accepts") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretCollidesWithEnvProvides — plan §4.4 rule 2: a secret_*
// entry name cannot also be in env_provides. env_provides is for plaintext
// service discovery URLs; credentials must not be advertised that way.
func TestValidateSecretCollidesWithEnvProvides(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasEnvProvides = true
			l.envProvides = map[string]string{
				"API_TOKEN": "http://{{.ContainerName}}:8080/token", // would be plaintext
			}
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "API_TOKEN", Description: "credential-backed"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error when secret_accepts overlaps env_provides")
	}
	if !strings.Contains(err.Error(), "also appears in env_provides") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsKeyMustStartWithOv — plan §4.4 rule 5: the
// optional Key override must start with "ov/" to prevent layers from
// exfiltrating unrelated user credentials.
func TestValidateSecretAcceptsKeyMustStartWithOv(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "AWS_ACCESS_KEY_ID", Description: "bad key", Key: "aws/access-key"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error when secret_accepts Key does not start with ov/")
	}
	if !strings.Contains(err.Error(), "must start with") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateSecretAcceptsKeyValidFormats — a handful of Key values that
// should parse cleanly as <ov/service/key>.
func TestValidateSecretAcceptsKeyValidFormats(t *testing.T) {
	cases := []string{
		"ov/api-key/openrouter",
		"ov/secret/webui_admin_password",
		"ov/api-key/openai",
		"ov/secret/immich-api-key",
	}
	for _, k := range cases {
		cfg := &Config{Images: map[string]ImageConfig{}}
		layers := map[string]*Layer{
			"svc": secretDepsLayer("svc", func(l *Layer) {
				l.HasSecretAccepts = true
				l.secretAccepts = []EnvDependency{
					{Name: "SOME_API_KEY", Description: "ok", Key: k},
				}
			}),
		}
		if err := Validate(cfg, layers, ""); err != nil {
			t.Errorf("Validate() unexpected error for Key=%q: %v", k, err)
		}
	}
}

// TestValidateSecretAcceptsKeyInvalidFormats — values that look plausible but
// fail the secretKeyPattern check.
func TestValidateSecretAcceptsKeyInvalidFormats(t *testing.T) {
	cases := []string{
		"openrouter",           // not <service>/<key>
		"ov/",                  // empty key segment
		"ov/api-key",           // only one segment after ov
		"ov/api-key/",          // empty key segment
		"ov//openrouter",       // empty service segment
		"ov/API-KEY/openrouter", // uppercase in service
	}
	for _, k := range cases {
		cfg := &Config{Images: map[string]ImageConfig{}}
		layers := map[string]*Layer{
			"svc": secretDepsLayer("svc", func(l *Layer) {
				l.HasSecretAccepts = true
				l.secretAccepts = []EnvDependency{
					{Name: "SOME_API_KEY", Description: "ok", Key: k},
				}
			}),
		}
		err := Validate(cfg, layers, "")
		if err == nil {
			t.Errorf("Validate() should have rejected Key=%q", k)
		}
	}
}

// TestValidateSecretAcceptsInvalidSlug — a name that would produce an invalid
// podman-secret slug (e.g., leading underscore → leading hyphen after
// lowercase-kebab) must be rejected.
func TestValidateSecretAcceptsInvalidSlug(t *testing.T) {
	cfg := &Config{Images: map[string]ImageConfig{}}
	layers := map[string]*Layer{
		"svc": secretDepsLayer("svc", func(l *Layer) {
			l.HasSecretAccepts = true
			l.secretAccepts = []EnvDependency{
				{Name: "_LEADING_UNDERSCORE", Description: "bad slug"},
			}
		}),
	}
	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected slug-validation error")
	}
	// The name itself is also invalid (starts with _ → isValidEnvVarName
	// permits it, but slug check rejects the leading hyphen).
	if !strings.Contains(err.Error(), "invalid podman secret slug") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestEnvVarNameToPodmanSecretSlug — unit test for the slug helper.
func TestEnvVarNameToPodmanSecretSlug(t *testing.T) {
	cases := map[string]string{
		"OPENROUTER_API_KEY":   "openrouter-api-key",
		"IMMICH_API_KEY":       "immich-api-key",
		"WEBUI_ADMIN_PASSWORD": "webui-admin-password",
		"TS_AUTHKEY":           "ts-authkey",
		"X":                    "x",
	}
	for in, want := range cases {
		if got := envVarNameToPodmanSecretSlug(in); got != want {
			t.Errorf("envVarNameToPodmanSecretSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
