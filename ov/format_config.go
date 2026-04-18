package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// --- Distro Config ---

// DistroConfig represents the `distro:` section of build.yml.
// Each distro defines bootstrap behavior AND package format definitions.
type DistroConfig struct {
	Distro map[string]*DistroDef `yaml:"distro"`
}

// DistroDef defines distro-specific bootstrap, workarounds, and package formats.
type DistroDef struct {
	Inherits    string                `yaml:"inherits,omitempty"`
	Bootstrap   BootstrapDef          `yaml:"bootstrap"`
	Workarounds []string              `yaml:"workarounds,omitempty"`
	Formats     map[string]*FormatDef `yaml:"formats,omitempty"`
}

// BootstrapDef defines how to bootstrap a base image.
type BootstrapDef struct {
	InstallCmd  string          `yaml:"install_cmd"`
	Packages    []string        `yaml:"packages"`
	CacheMounts []CacheMountDef `yaml:"cache_mounts"`
}

// CacheMountDef defines a BuildKit cache mount.
type CacheMountDef struct {
	Dst     string `yaml:"dst"`
	Sharing string `yaml:"sharing,omitempty"` // default: "locked"
}

// FormatDef defines a package format (rpm, deb, pac, aur, apk, etc.).
type FormatDef struct {
	CacheMounts     []CacheMountDef  `yaml:"cache_mounts"`
	SectionFields   map[string]string `yaml:"section_fields"`
	InstallTemplate string            `yaml:"install_template"`
	Validate        []FormatRule      `yaml:"validate,omitempty"`
}

// FormatRule is a validation rule for format section fields.
type FormatRule struct {
	Field string `yaml:"field"`
	Rule  string `yaml:"rule"`
}

// ResolveDistro finds the distro definition matching the image's distro tags.
// Walks tags in order, strips :version suffix to match base distro name.
// Follows inherits: chains with cycle detection, inheriting formats from parent.
func (dc *DistroConfig) ResolveDistro(distroTags []string) *DistroDef {
	if dc == nil {
		return nil
	}
	for _, tag := range distroTags {
		// Try exact match first (e.g., "fedora:43")
		if def, ok := dc.Distro[tag]; ok {
			return dc.resolveInherits(def, 10)
		}
		// Try base name (e.g., "fedora" from "fedora:43")
		base := tag
		if idx := indexOf(tag, ':'); idx >= 0 {
			base = tag[:idx]
		}
		if def, ok := dc.Distro[base]; ok {
			return dc.resolveInherits(def, 10)
		}
	}
	return nil
}

func (dc *DistroConfig) resolveInherits(def *DistroDef, maxDepth int) *DistroDef {
	if def.Inherits == "" || maxDepth <= 0 {
		return def
	}
	parent, ok := dc.Distro[def.Inherits]
	if !ok {
		return def
	}
	resolved := dc.resolveInherits(parent, maxDepth-1)
	// Child overrides parent for non-zero fields
	if def.Bootstrap.InstallCmd != "" {
		// Child has its own bootstrap — use child, but inherit formats if missing
		if len(def.Formats) == 0 && len(resolved.Formats) > 0 {
			merged := &DistroDef{
				Inherits:    def.Inherits,
				Bootstrap:   def.Bootstrap,
				Workarounds: def.Workarounds,
				Formats:     resolved.Formats,
			}
			return merged
		}
		return def
	}
	// Child has no bootstrap — inherit everything from parent, but overlay child's formats
	if len(def.Formats) > 0 {
		merged := &DistroDef{
			Inherits:    def.Inherits,
			Bootstrap:   resolved.Bootstrap,
			Workarounds: resolved.Workarounds,
			Formats:     def.Formats,
		}
		return merged
	}
	return resolved
}

// AllFormatNames returns a sorted, deduplicated list of all format names across all distros.
func (dc *DistroConfig) AllFormatNames() []string {
	if dc == nil {
		return nil
	}
	seen := make(map[string]bool)
	for _, distro := range dc.Distro {
		resolved := dc.resolveInherits(distro, 10)
		for name := range resolved.Formats {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// ValidFormat returns true if any distro defines this format name.
func (dc *DistroConfig) ValidFormat(name string) bool {
	if dc == nil {
		return false
	}
	for _, distro := range dc.Distro {
		resolved := dc.resolveInherits(distro, 10)
		if _, ok := resolved.Formats[name]; ok {
			return true
		}
	}
	return false
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// --- Builder Config ---

// BuilderConfig represents the `builder:` section of build.yml.
type BuilderConfig struct {
	Builder map[string]*BuilderDef `yaml:"builder"`
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
	BuildScript     string            `yaml:"build_script,omitempty"`
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
	_, ok := bc.Builder[name]
	return ok
}

// BuilderNames returns sorted list of defined builder names.
func (bc *BuilderConfig) BuilderNames() []string {
	if bc == nil {
		return nil
	}
	names := make([]string, 0, len(bc.Builder))
	for name := range bc.Builder {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// --- Loading ---

// ResolveFormatConfigData resolves a format config reference to raw YAML bytes.
// ref can be:
//   - empty string: returns nil (fall through to next level)
//   - @host/org/repo/path:version: downloads remote repo and reads file from cache
//   - bare path: reads relative to dir
func ResolveFormatConfigData(ref, dir string) ([]byte, error) {
	if ref == "" {
		return nil, nil
	}

	if strings.HasPrefix(ref, "@") {
		parsed := ParseRemoteRef(ref)
		cachePath, err := EnsureRepoDownloaded(parsed.RepoPath, parsed.Version)
		if err != nil {
			return nil, fmt.Errorf("downloading %s: %w", ref, err)
		}
		filePath := filepath.Join(cachePath, parsed.SubPath)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("reading remote config %s (at %s): %w", ref, filePath, err)
		}
		return data, nil
	}

	// Local path relative to project dir
	path := filepath.Join(dir, ref)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	return data, nil
}

// BuildFile is the on-disk schema of build.yml — three optional top-level
// sections that map directly onto DistroConfig/BuilderConfig/InitConfig.
type BuildFile struct {
	Distro  map[string]*DistroDef  `yaml:"distro"`
	Builder map[string]*BuilderDef `yaml:"builder"`
	Init    map[string]*InitDef    `yaml:"init"`
}

// LoadBuildConfigForImage loads distro, builder, and init configs from a single
// build.yml for a given image. Resolution chain: per-image format_config →
// defaults format_config → error.
//
// The init section is optional: if build.yml has no `init:` key (or an empty
// one), the returned InitConfig is nil — images without init config are
// allowed (no init system, no entrypoint beyond the base image default).
func LoadBuildConfigForImage(imgRef, defaultRef, dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	ref := imgRef
	if ref == "" {
		ref = defaultRef
	}
	if ref == "" {
		return nil, nil, nil, fmt.Errorf("build.yml: no format_config ref specified (set in defaults or per-image)")
	}

	data, err := ResolveFormatConfigData(ref, dir)
	if err != nil {
		return nil, nil, nil, err
	}

	var bf BuildFile
	if err := yaml.Unmarshal(data, &bf); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing build.yml: %w", err)
	}

	distroCfg := &DistroConfig{Distro: bf.Distro}
	builderCfg := &BuilderConfig{Builder: bf.Builder}
	var initCfg *InitConfig
	if len(bf.Init) > 0 {
		initCfg = &InitConfig{Init: bf.Init}
	}

	return distroCfg, builderCfg, initCfg, nil
}

// LoadDefaultBuildConfig loads build config from the defaults format_config ref.
// Used during early initialization (before per-image resolution) to get the default
// DistroConfig for format name registration.
func LoadDefaultBuildConfig(defaultRef, dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	return LoadBuildConfigForImage("", defaultRef, dir)
}
