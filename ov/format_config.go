package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultBuildFormat is the fallback when no build: field is specified anywhere.
// This is only used as a last resort — images.yml defaults.build should always be set.
const DefaultBuildFormat = "rpm"

// --- Distro Config ---

// DistroConfig represents the distro.yml configuration.
type DistroConfig struct {
	Distros map[string]*DistroDef `yaml:"distros"`
}

// DistroDef defines distro-specific bootstrap and workarounds.
type DistroDef struct {
	Inherits    string       `yaml:"inherits,omitempty"`
	Bootstrap   BootstrapDef `yaml:"bootstrap"`
	Workarounds []string     `yaml:"workarounds,omitempty"`
}

// BootstrapDef defines how to bootstrap a base image.
type BootstrapDef struct {
	InstallCmd string          `yaml:"install_cmd"`
	Packages   []string        `yaml:"packages"`
	CacheMounts []CacheMountDef `yaml:"cache_mounts"`
}

// CacheMountDef defines a BuildKit cache mount.
type CacheMountDef struct {
	Dst     string `yaml:"dst"`
	Sharing string `yaml:"sharing,omitempty"` // default: "locked"
}

// ResolveDistro finds the distro definition matching the image's distro tags.
// Walks tags in order, strips :version suffix to match base distro name.
// Follows inherits: chains with cycle detection.
func (dc *DistroConfig) ResolveDistro(distroTags []string) *DistroDef {
	if dc == nil {
		return nil
	}
	for _, tag := range distroTags {
		// Try exact match first (e.g., "fedora:43")
		if def, ok := dc.Distros[tag]; ok {
			return dc.resolveInherits(def, 10)
		}
		// Try base name (e.g., "fedora" from "fedora:43")
		base := tag
		if idx := indexOf(tag, ':'); idx >= 0 {
			base = tag[:idx]
		}
		if def, ok := dc.Distros[base]; ok {
			return dc.resolveInherits(def, 10)
		}
	}
	return nil
}

func (dc *DistroConfig) resolveInherits(def *DistroDef, maxDepth int) *DistroDef {
	if def.Inherits == "" || maxDepth <= 0 {
		return def
	}
	parent, ok := dc.Distros[def.Inherits]
	if !ok {
		return def
	}
	resolved := dc.resolveInherits(parent, maxDepth-1)
	// Child overrides parent for non-zero fields
	if def.Bootstrap.InstallCmd != "" {
		return def
	}
	return resolved
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// --- Build Config ---

// BuildConfig represents the build.yml configuration.
type BuildConfig struct {
	Formats map[string]*FormatDef `yaml:"formats"`
}

// FormatDef defines a package format (rpm, deb, pac, aur, apk, etc.).
type FormatDef struct {
	CacheMounts     []CacheMountDef   `yaml:"cache_mounts"`
	SectionFields   map[string]string  `yaml:"section_fields"`
	InstallTemplate string            `yaml:"install_template"`
	Validate        []FormatRule       `yaml:"validate,omitempty"`
}

// FormatRule is a validation rule for format section fields.
type FormatRule struct {
	Field string `yaml:"field"`
	Rule  string `yaml:"rule"`
}

// ValidFormat returns true if the given name is a defined format.
func (bc *BuildConfig) ValidFormat(name string) bool {
	if bc == nil {
		return false
	}
	_, ok := bc.Formats[name]
	return ok
}

// FormatNames returns sorted list of defined format names.
func (bc *BuildConfig) FormatNames() []string {
	if bc == nil {
		return nil
	}
	names := make([]string, 0, len(bc.Formats))
	for name := range bc.Formats {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// --- Builder Config ---

// BuilderConfig represents the builder.yml configuration.
type BuilderConfig struct {
	Builders map[string]*BuilderDef `yaml:"builders"`
}

// BuilderDef defines a multi-stage builder (pixi, npm, cargo, etc.).
type BuilderDef struct {
	DetectFiles     []string          `yaml:"detect_files,omitempty"`
	DetectConfig    string            `yaml:"detect_config,omitempty"`
	RequiresSrcDir  bool              `yaml:"requires_src_dir,omitempty"`
	Inline          bool              `yaml:"inline,omitempty"`
	CacheMounts     []CacheMountDef   `yaml:"cache_mounts"`
	Env             map[string]string `yaml:"env,omitempty"`
	StageTemplate   string            `yaml:"stage_template,omitempty"`
	InstallTemplate string            `yaml:"install_template,omitempty"`
	InstallCommands map[string]string `yaml:"install_commands,omitempty"`
	ManylinuxFix    string            `yaml:"manylinux_fix,omitempty"`
	CopyArtifacts   []CopyDef         `yaml:"copy_artifacts,omitempty"`
	CopyBinary      *CopyDef          `yaml:"copy_binary,omitempty"`
}

// CopyDef defines a COPY directive for builder artifacts.
type CopyDef struct {
	Src   string `yaml:"src"`
	Dst   string `yaml:"dst"`
	Chown bool   `yaml:"chown,omitempty"`
}

// ValidBuilderType returns true if the given name is a defined builder.
func (bc *BuilderConfig) ValidBuilderType(name string) bool {
	if bc == nil {
		return false
	}
	_, ok := bc.Builders[name]
	return ok
}

// BuilderNames returns sorted list of defined builder names.
func (bc *BuilderConfig) BuilderNames() []string {
	if bc == nil {
		return nil
	}
	names := make([]string, 0, len(bc.Builders))
	for name := range bc.Builders {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// --- Loading ---

// LoadFormatConfigs loads distro.yml, build.yml, builder.yml.
// Project-level files override embedded defaults. Missing files use defaults.
func LoadFormatConfigs(dir string) (*DistroConfig, *BuildConfig, *BuilderConfig, error) {
	distro, err := loadDistroConfig(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading distro.yml: %w", err)
	}
	build, err := loadBuildConfig(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading build.yml: %w", err)
	}
	builder, err := loadBuilderConfig(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading builder.yml: %w", err)
	}
	return distro, build, builder, nil
}

func loadDistroConfig(dir string) (*DistroConfig, error) {
	data := loadOrDefault(filepath.Join(dir, "distro.yml"), defaultDistroYAML)
	var cfg DistroConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadBuildConfig(dir string) (*BuildConfig, error) {
	data := loadOrDefault(filepath.Join(dir, "build.yml"), defaultBuildYAML)
	var cfg BuildConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadBuilderConfig(dir string) (*BuilderConfig, error) {
	data := loadOrDefault(filepath.Join(dir, "builder.yml"), defaultBuilderYAML)
	var cfg BuilderConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// loadOrDefault reads a file; if it doesn't exist, returns the default bytes.
func loadOrDefault(path string, defaultData []byte) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultData
	}
	return data
}
