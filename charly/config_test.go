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
		if _, ok := cfg.Box[name]; !ok {
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
		boxName        string
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
			boxName:        "base",
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
			boxName:        "cuda",
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
			boxName:        "ml-cuda",
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
			boxName:        "inference",
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
			boxName:        "ubuntu-dev",
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
			boxName:        "bazzite",
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
			resolved, err := cfg.ResolveBox(tt.boxName, tt.calverTag, testProjectDir(t), ResolveOpts{})
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
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
			// Bootc was deleted as an image-level field; the layer-aggregated
			// LayerCaps.PreserveUser is the new signal. Tests that need this
			// behavior should compose a layer that contributes preserve_user.
			_ = tt.wantBootc
		})
	}
}

func TestResolveImageNotFound(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	_, err = cfg.ResolveBox("nonexistent", "2026.45.1415", testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("ResolveBox() expected error for nonexistent image")
	}
}

// TestMergeImageConfig_BuildTunables guards the regression where new
// BoxConfig fields are silently dropped during the unified loader's
// defaults: merge because mergeBoxConfig is a hand-maintained field-by-field
// merger. The build-speed tunables (jobs / podman_jobs / podman_jobs_cap /
// context_ignore / cache) MUST survive the merge, or defaults.context_ignore
// authored in charly.yml never reaches the generator.
func TestMergeImageConfig_BuildTunables(t *testing.T) {
	// dst empty → fills from src (the path that dropped these fields).
	dst := &BoxConfig{}
	src := &BoxConfig{
		Jobs:          intPtr(4),
		PodmanJobs:    intPtr(0),
		PodmanJobsCap: intPtr(8),
		ContextIgnore: []string{"image", ".eval"},
		Cache:         "image",
		KeepImages:    intPtr(5),
		KeepEvalRuns:  intPtr(10),
	}
	mergeBoxConfig(dst, src)
	if dst.KeepImages == nil || *dst.KeepImages != 5 {
		t.Errorf("KeepImages not merged from src: %v", dst.KeepImages)
	}
	if dst.KeepEvalRuns == nil || *dst.KeepEvalRuns != 10 {
		t.Errorf("KeepEvalRuns not merged from src: %v", dst.KeepEvalRuns)
	}
	if dst.Jobs == nil || *dst.Jobs != 4 {
		t.Errorf("Jobs not merged from src: %v", dst.Jobs)
	}
	if dst.PodmanJobs == nil || *dst.PodmanJobs != 0 {
		t.Errorf("PodmanJobs (explicit 0) not merged from src: %v", dst.PodmanJobs)
	}
	if dst.PodmanJobsCap == nil || *dst.PodmanJobsCap != 8 {
		t.Errorf("PodmanJobsCap not merged from src: %v", dst.PodmanJobsCap)
	}
	if len(dst.ContextIgnore) != 2 {
		t.Errorf("ContextIgnore not merged from src: %v", dst.ContextIgnore)
	}
	if dst.Cache != "image" {
		t.Errorf("Cache not merged from src: %q", dst.Cache)
	}

	// dst already set → src must NOT override (per-field "dst wins if set").
	dst2 := &BoxConfig{Jobs: intPtr(2), Cache: "registry"}
	mergeBoxConfig(dst2, &BoxConfig{Jobs: intPtr(9), Cache: "image"})
	if dst2.Jobs == nil || *dst2.Jobs != 2 {
		t.Errorf("dst Jobs should win, got %v", dst2.Jobs)
	}
	if dst2.Cache != "registry" {
		t.Errorf("dst Cache should win, got %q", dst2.Cache)
	}
}

func TestImageNames(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	names := cfg.BoxNames()
	// 7 total images in testdata, but disabled-image is excluded
	if len(names) != 6 {
		t.Errorf("BoxNames() returned %d names, want 6: %v", len(names), names)
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("BoxNames() not sorted: %v", names)
			break
		}
	}

	// disabled-image should not appear
	for _, name := range names {
		if name == "disabled-image" {
			t.Error("BoxNames() should not include disabled-image")
		}
	}
}

func TestResolveImageBuilders(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   BuilderMap{"pixi": "default-builder", "npm": "default-builder"},
		},
		Box: map[string]BoxConfig{
			"default-builder": {Layer: []string{}},
			"custom-builder":  {Layer: []string{}},
			"uses-default":    {Layer: []string{}},
			"uses-custom":     {Layer: []string{}, Builder: BuilderMap{"pixi": "custom-builder"}},
		},
	}

	// Image with no explicit builder inherits defaults.builder
	resolved, err := cfg.ResolveBox("uses-default", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.BuilderFor("pixi") != "default-builder" {
		t.Errorf("Builder[pixi] = %q, want %q", resolved.Builder.BuilderFor("pixi"), "default-builder")
	}

	// Image with explicit builder overrides defaults per-type
	resolved, err = cfg.ResolveBox("uses-custom", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
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
		Defaults: BoxConfig{Build: BuildFormats{"rpm"}, Platforms: []string{"linux/amd64"}},
		Box: map[string]BoxConfig{
			"app": {Layer: []string{}},
		},
	}
	resolved, err = cfg2.ResolveBox("app", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if len(resolved.Builder) != 0 {
		t.Errorf("Builder = %v, want empty", resolved.Builder)
	}

	// Self-reference filtered out
	cfg3 := &Config{
		Defaults: BoxConfig{
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Builder:   BuilderMap{"pixi": "my-builder"},
		},
		Box: map[string]BoxConfig{
			"my-builder": {Layer: []string{}},
		},
	}
	resolved, err = cfg3.ResolveBox("my-builder", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.HasBuilder("pixi") {
		t.Errorf("Self-referencing builder should be filtered, got %v", resolved.Builder)
	}

	// Inheritance from base image
	cfg4 := &Config{
		Defaults: BoxConfig{Build: BuildFormats{"pac"}, Platforms: []string{"linux/amd64"}},
		Box: map[string]BoxConfig{
			"base-img":    {Build: BuildFormats{"pac"}, Layer: []string{}, Builder: BuilderMap{"aur": "aur-builder"}},
			"aur-builder": {Layer: []string{}},
			"child-img":   {Base: "base-img", Layer: []string{}},
		},
	}
	resolved, err = cfg4.ResolveBox("child-img", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if resolved.Builder.BuilderFor("aur") != "aur-builder" {
		t.Errorf("Builder[aur] = %q, want %q (inherited from base)", resolved.Builder.BuilderFor("aur"), "aur-builder")
	}
}

func TestResolveImagePorts(t *testing.T) {
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
			Port:      []string{"80:80"},
		},
		Box: map[string]BoxConfig{
			"with-ports":    {Layer: []string{}, Port: []string{"9090:9090"}},
			"inherit-ports": {Layer: []string{}},
			"no-ports":      {Layer: []string{}, Port: []string{}},
		},
	}

	// Image with explicit ports
	resolved, err := cfg.ResolveBox("with-ports", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Port, []string{"9090:9090"}) {
		t.Errorf("Ports = %v, want [9090:9090]", resolved.Port)
	}

	// Image inheriting default ports
	resolved, err = cfg.ResolveBox("inherit-ports", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Port, []string{"80:80"}) {
		t.Errorf("Ports = %v, want [80:80]", resolved.Port)
	}

	// Image with empty ports (no inheritance since explicitly empty slice won't be set via JSON)
	resolved, err = cfg.ResolveBox("no-ports", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
	}
	// Empty slice in JSON becomes nil after unmarshal, but in Go struct it's []string{}
	// When len == 0, we fall through to defaults
	if !reflect.DeepEqual(resolved.Port, []string{"80:80"}) {
		t.Errorf("Ports = %v, want [80:80]", resolved.Port)
	}
}

func TestFullTag(t *testing.T) {
	cfg, err := LoadConfig("testdata")
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	resolved, err := cfg.ResolveBox("base", "2026.45.1415", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveBox() error = %v", err)
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
	disabledImg, ok := cfg.Box["disabled-image"]
	if !ok {
		t.Fatal("disabled-image not found in raw config")
	}
	if disabledImg.IsEnabled() {
		t.Error("disabled-image should not be enabled")
	}

	// disabled-image is excluded from BoxNames()
	for _, name := range cfg.BoxNames() {
		if name == "disabled-image" {
			t.Error("disabled-image should not appear in BoxNames()")
		}
	}

	// disabled-image is excluded from ResolveAllBox()
	all, err := cfg.ResolveAllBox("test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveAllBox() error = %v", err)
	}
	if _, ok := all["disabled-image"]; ok {
		t.Error("disabled-image should not appear in ResolveAllBox()")
	}

	// ResolveBox returns error for disabled image
	_, err = cfg.ResolveBox("disabled-image", "test", testProjectDir(t), ResolveOpts{})
	if err == nil {
		t.Error("ResolveBox() should return error for disabled image")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected 'disabled' in error, got: %v", err)
	}

	// Enabled images still work
	_, err = cfg.ResolveBox("base", "test", testProjectDir(t), ResolveOpts{})
	if err != nil {
		t.Errorf("ResolveBox() unexpected error for enabled box: %v", err)
	}

	// --include-disabled (global) reaches the disabled image
	_, err = cfg.ResolveBox("disabled-image", "test", testProjectDir(t), ResolveOpts{IncludeDisabled: true})
	if err != nil {
		t.Errorf("ResolveBox(IncludeDisabled=true) should succeed for disabled image, got: %v", err)
	}

	// --include-disabled scoped to a different name still rejects
	_, err = cfg.ResolveBox("disabled-image", "test", testProjectDir(t), ResolveOpts{
		IncludeDisabled:      true,
		IncludeDisabledNames: map[string]bool{"some-other-image": true},
	})
	if err == nil {
		t.Error("scoped IncludeDisabled to a different name should still reject")
	}

	// --include-disabled scoped to the requested name succeeds
	_, err = cfg.ResolveBox("disabled-image", "test", testProjectDir(t), ResolveOpts{
		IncludeDisabled:      true,
		IncludeDisabledNames: map[string]bool{"disabled-image": true},
	})
	if err != nil {
		t.Errorf("scoped IncludeDisabled to the requested name should succeed, got: %v", err)
	}
}

// TestResolveOpts_ShouldIncludeDisabled covers the scoping helper used by
// ResolveBox / ResolveAllBox / validateBoxDAG. The scope semantics
// matter for `charly box build <name> --include-disabled` so widening the
// working set doesn't surface unrelated disabled-image dep errors.
func TestResolveOpts_ShouldIncludeDisabled(t *testing.T) {
	cases := []struct {
		name string
		opts ResolveOpts
		want map[string]bool // image-name → expected return
	}{
		{
			name: "default opts: never include",
			opts: ResolveOpts{},
			want: map[string]bool{"foo": false, "bar": false},
		},
		{
			name: "global IncludeDisabled: include all",
			opts: ResolveOpts{IncludeDisabled: true},
			want: map[string]bool{"foo": true, "bar": true},
		},
		{
			name: "scoped IncludeDisabled: only listed names",
			opts: ResolveOpts{
				IncludeDisabled:      true,
				IncludeDisabledNames: map[string]bool{"foo": true},
			},
			want: map[string]bool{"foo": true, "bar": false},
		},
		{
			name: "scoped without IncludeDisabled flag: never include (flag is the gate)",
			opts: ResolveOpts{
				IncludeDisabledNames: map[string]bool{"foo": true},
			},
			want: map[string]bool{"foo": false, "bar": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for image, want := range tc.want {
				if got := tc.opts.shouldIncludeDisabled(image); got != want {
					t.Errorf("shouldIncludeDisabled(%q) = %v, want %v", image, got, want)
				}
			}
		})
	}
}

func TestResolveImageDistroBaseChain(t *testing.T) {
	// Tests that distro: tags propagate through the entire base chain,
	// not just the immediate parent.
	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Build:     BuildFormats{"rpm"},
			Platforms: []string{"linux/amd64"},
		},
		Box: map[string]BoxConfig{
			// Level 0: defines distro
			"fedora": {
				Base:   "quay.io/fedora/fedora:43",
				Distro: []string{"fedora:43", "fedora"},
				Layer:  []string{},
			},
			// Level 1: no distro set, should inherit from fedora
			"fedora-nonfree": {
				Base:  "fedora",
				Layer: []string{},
			},
			// Level 2: no distro set, should inherit through fedora-nonfree -> fedora
			"nvidia": {
				Base:  "fedora-nonfree",
				Layer: []string{},
			},
			// Level 3: no distro set, should inherit through nvidia -> fedora-nonfree -> fedora
			"ml-app": {
				Base:  "nvidia",
				Layer: []string{},
			},
		},
	}

	tests := []struct {
		name       string
		boxName    string
		wantDistro []string
	}{
		{"level 0: defines distro", "fedora", []string{"fedora:43", "fedora"}},
		{"level 1: inherits from parent", "fedora-nonfree", []string{"fedora:43", "fedora"}},
		{"level 2: inherits through chain", "nvidia", []string{"fedora:43", "fedora"}},
		{"level 3: inherits through deep chain", "ml-app", []string{"fedora:43", "fedora"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cfg.ResolveBox(tt.boxName, "test", testProjectDir(t), ResolveOpts{})
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
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
		Defaults: BoxConfig{
			Registry:  "ghcr.io/test",
			Platforms: []string{"linux/amd64"},
		},
		Box: map[string]BoxConfig{
			// Level 0: defines build
			"arch": {
				Base:  "docker.io/library/archlinux:latest",
				Build: BuildFormats{"pac"},
				Layer: []string{},
			},
			// Level 1: no build set, should inherit from arch
			"arch-extended": {
				Base:  "arch",
				Layer: []string{},
			},
			// Level 2: no build set, should inherit through chain
			"arch-app": {
				Base:  "arch-extended",
				Layer: []string{},
			},
		},
	}

	tests := []struct {
		name      string
		boxName   string
		wantBuild []string
	}{
		{"level 0: defines build", "arch", []string{"pac"}},
		{"level 1: inherits from parent", "arch-extended", []string{"pac"}},
		{"level 2: inherits through chain", "arch-app", []string{"pac"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := cfg.ResolveBox(tt.boxName, "test", testProjectDir(t), ResolveOpts{})
			if err != nil {
				t.Fatalf("ResolveBox() error = %v", err)
			}
			if !reflect.DeepEqual(resolved.BuildFormats, tt.wantBuild) {
				t.Errorf("BuildFormats = %v, want %v", resolved.BuildFormats, tt.wantBuild)
			}
		})
	}
}
