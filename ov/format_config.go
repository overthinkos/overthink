package main

import (
	"fmt"
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
	// BaseUser declares a pre-existing uid account that ships in the
	// upstream base image — e.g. ubuntu:ubuntu uid 1000 on ubuntu:24.04.
	// When present and the image's user_policy allows adoption, ov adopts
	// this account verbatim instead of creating a new user at the
	// configured uid. Nil on distros whose canonical base images ship no
	// pre-existing user account (fedora, arch, plain debian:13).
	BaseUser *BaseUserDef `yaml:"base_user,omitempty"`

	// Bootstrap-builder strategy configurations. Each is optional; only
	// distros that support the strategy populate the corresponding block.
	// Used by the kind:bootstrap builders in build.yml `builder:` section
	// to render the actual bootstrap command (pacstrap, debootstrap, etc.)
	Pacstrap        *PacstrapDef        `yaml:"pacstrap,omitempty"`
	Debootstrap     *DebootstrapDef     `yaml:"debootstrap,omitempty"`
	AlpineBootstrap *AlpineBootstrapDef `yaml:"alpine_bootstrap,omitempty"`

	// Bootloader install templates rendered during VM disk builds for
	// bootstrap-flavored VMs. Drives the chroot grub-install + initramfs
	// generation. Distinct from bootc-VM which handles its own bootloader
	// install internally via `bootc install to-disk`.
	Bootloader *BootloaderDef `yaml:"bootloader,omitempty"`
}

// PacstrapDef configures pacstrap-flavored bootstrap (Arch, CachyOS).
type PacstrapDef struct {
	BasePackages    []string         `yaml:"base_packages,omitempty"`
	KeyringInitCmd  string           `yaml:"keyring_init_cmd,omitempty"`
	MirrorlistURL   string           `yaml:"mirrorlist_url,omitempty"`
	ExtraRepos      []PacstrapRepo   `yaml:"extra_repos,omitempty"`
}

// PacstrapRepo describes an additional pacman repo (e.g. CachyOS repos)
// to inject into /etc/pacman.conf inside the bootstrap target before
// running pacstrap.
type PacstrapRepo struct {
	Name   string `yaml:"name"`
	Server string `yaml:"server"`
}

// DebootstrapDef configures debootstrap-flavored bootstrap (Debian, Ubuntu).
type DebootstrapDef struct {
	Suite   string `yaml:"suite,omitempty"`
	Mirror  string `yaml:"mirror,omitempty"`
	Variant string `yaml:"variant,omitempty"` // default: minbase
}

// AlpineBootstrapDef configures `apk add --root` style bootstrap (Alpine).
type AlpineBootstrapDef struct {
	MirrorURL string `yaml:"mirror_url,omitempty"`
}

// BootloaderDef holds per-distro bootloader-install templates rendered
// during VM disk builds. {{.Mnt}} expands to the target mount point.
type BootloaderDef struct {
	InstallTemplate   string `yaml:"install_template,omitempty"`
	InitramfsTemplate string `yaml:"initramfs_template,omitempty"`
	FstabTemplate     string `yaml:"fstab_template,omitempty"`
}

// BaseUserDef describes a user account that already exists in a base image.
// All four fields are required when the block is declared.
type BaseUserDef struct {
	Name string `yaml:"name"`
	UID  int    `yaml:"uid"`
	GID  int    `yaml:"gid"`
	Home string `yaml:"home"`
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
//
// Template resolution:
//
//   - Legacy path: `install_template:` holds a monolithic Containerfile-
//     shaped template used by the OCI target.
//   - New path: `phases:` holds three-phase × two-venue templates where
//     each entry carries both a container: (Containerfile directives with
//     BuildKit cache mounts) and a host: (plain shell) rendering of the
//     same operation. The host target requires the new path; the OCI
//     target prefers phases.install.container when set and falls back to
//     install_template otherwise.
//
// Keeping both fields lets us migrate build.yml per-format one at a time
// (Task 4 / 7 migrations) without breaking OCI output for the rest.
type FormatDef struct {
	CacheMounts     []CacheMountDef   `yaml:"cache_mounts"`
	SectionFields   map[string]string `yaml:"section_fields"`
	InstallTemplate string            `yaml:"install_template,omitempty"`
	Phases          *PhaseSet         `yaml:"phases,omitempty"`
	Validate        []FormatRule      `yaml:"validate,omitempty"`
}

// PhaseSet carries three-phase templates for a format or builder.
// Phases run in order: prepare (repo config, key import, copr enable)
// → install (the actual dnf/apt/pacman/pixi/cargo command) → cleanup
// (copr disable, scratch cleanup). Each phase has separate container
// and host renderings so cache-mount directives stay out of the host
// path and shell-specific wrappers stay out of the container path.
type PhaseSet struct {
	Prepare *PhaseTemplates `yaml:"prepare,omitempty"`
	Install *PhaseTemplates `yaml:"install,omitempty"`
	Cleanup *PhaseTemplates `yaml:"cleanup,omitempty"`
}

// PhaseTemplates carries both renderings (container + host) of one
// phase. Either may be empty — a phase with only a host: block is valid
// (e.g. repo mutations that only make sense on a real host), as is a
// phase with only a container: block (cache cleanup inside the build).
type PhaseTemplates struct {
	Container string `yaml:"container,omitempty"`
	Host      string `yaml:"host,omitempty"`
}

// PhaseTemplate looks up the template string for a (phase, venue)
// lookup, with documented fallback behavior: if the new phases: block
// lacks the requested cell, fall back to the legacy InstallTemplate for
// (PhaseInstall, container) only — the combination covered by the
// legacy field. All other lookups return "" when the new path is absent.
func (f *FormatDef) PhaseTemplate(phase Phase, venue Venue) string {
	if f == nil {
		return ""
	}
	if f.Phases != nil {
		var pt *PhaseTemplates
		switch phase {
		case PhasePrepare:
			pt = f.Phases.Prepare
		case PhaseInstall:
			pt = f.Phases.Install
		case PhaseCleanup:
			pt = f.Phases.Cleanup
		}
		if pt != nil {
			switch venue {
			case VenueHostNative:
				if pt.Host != "" {
					return pt.Host
				}
			case VenueContainerBuilder:
				if pt.Container != "" {
					return pt.Container
				}
			}
		}
	}
	// Legacy fallback: the old InstallTemplate only describes the
	// install-phase in container venue.
	if phase == PhaseInstall && venue == VenueContainerBuilder {
		return f.InstallTemplate
	}
	return ""
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

	// "Child wins per non-nil/non-empty field, else inherit from parent"
	// applied uniformly across every optional sub-block. This pattern
	// scales as new sub-blocks are added (Pacstrap, Bootloader, etc.).
	pickPtr := func(child, parent interface{}) interface{} {
		// Caller passes typed pointers; return whichever is non-nil.
		if child != nil && !isNilPtr(child) {
			return child
		}
		return parent
	}
	_ = pickPtr // (placeholder; explicit per-field merges below for clarity)

	baseUser := def.BaseUser
	if baseUser == nil {
		baseUser = resolved.BaseUser
	}
	pacstrap := def.Pacstrap
	if pacstrap == nil {
		pacstrap = resolved.Pacstrap
	}
	debootstrap := def.Debootstrap
	if debootstrap == nil {
		debootstrap = resolved.Debootstrap
	}
	alpineBootstrap := def.AlpineBootstrap
	if alpineBootstrap == nil {
		alpineBootstrap = resolved.AlpineBootstrap
	}
	bootloader := def.Bootloader
	if bootloader == nil {
		bootloader = resolved.Bootloader
	}

	if def.Bootstrap.InstallCmd != "" {
		// Child has its own bootstrap. Merge inherited optional sub-blocks
		// onto it.
		formats := def.Formats
		if len(formats) == 0 {
			formats = resolved.Formats
		}
		merged := &DistroDef{
			Inherits:        def.Inherits,
			Bootstrap:       def.Bootstrap,
			Workarounds:     def.Workarounds,
			Formats:         formats,
			BaseUser:        baseUser,
			Pacstrap:        pacstrap,
			Debootstrap:     debootstrap,
			AlpineBootstrap: alpineBootstrap,
			Bootloader:      bootloader,
		}
		return merged
	}
	// Child has no bootstrap — inherit parent's bootstrap + workarounds,
	// overlay child's formats / baseuser / new sub-blocks.
	formats := resolved.Formats
	if len(def.Formats) > 0 {
		formats = def.Formats
	}
	merged := &DistroDef{
		Inherits:        def.Inherits,
		Bootstrap:       resolved.Bootstrap,
		Workarounds:     resolved.Workarounds,
		Formats:         formats,
		BaseUser:        baseUser,
		Pacstrap:        pacstrap,
		Debootstrap:     debootstrap,
		AlpineBootstrap: alpineBootstrap,
		Bootloader:      bootloader,
	}
	return merged
}

// isNilPtr is a small helper used by resolveInherits's per-field merge
// pattern. Returns true for typed nil pointers; false for everything else.
func isNilPtr(v interface{}) bool {
	if v == nil {
		return true
	}
	// Reflection-free shortcut: the merger only passes nilable pointer
	// types; non-pointers reach here with non-nil interface boxes.
	switch p := v.(type) {
	case *BaseUserDef:
		return p == nil
	case *PacstrapDef:
		return p == nil
	case *DebootstrapDef:
		return p == nil
	case *AlpineBootstrapDef:
		return p == nil
	case *BootloaderDef:
		return p == nil
	}
	return false
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
//
// The legacy `stage_template:` / `install_template:` fields emit a
// single Containerfile chunk. The new `phases:` field matches FormatDef
// and lets a builder specify separate container + host renderings for
// each of prepare/install/cleanup — required for HostDeployTarget to
// invoke the builder via `podman run` with HOME-remapped bind-mounts
// rather than as a build stage.
type BuilderDef struct {
	DetectFiles     []string          `yaml:"detect_files,omitempty"`
	DetectConfig    string            `yaml:"detect_config,omitempty"`
	RequiresSrcDir  bool              `yaml:"requires_src_dir,omitempty"`
	Inline          bool              `yaml:"inline,omitempty"`
	CacheMounts     []CacheMountDef   `yaml:"cache_mounts"`
	Env             map[string]string `yaml:"env,omitempty"`
	StageTemplate   string            `yaml:"stage_template,omitempty"`
	InstallTemplate string            `yaml:"install_template,omitempty"`
	Phases          *PhaseSet         `yaml:"phases,omitempty"`
	InstallCommands map[string]string `yaml:"install_commands,omitempty"`
	ManylinuxFix    string            `yaml:"manylinux_fix,omitempty"`
	BuildScript     string            `yaml:"build_script,omitempty"`
	CopyArtifacts   []CopyDef         `yaml:"copy_artifacts,omitempty"`
	CopyBinary      *CopyDef          `yaml:"copy_binary,omitempty"`

	// PathContributions lists sub-paths under the builder's install prefix
	// that should be added to PATH on deploy targets. Used by the compiler
	// (install_build.go) to derive implicit PATH additions from builder
	// outputs (pixi env bin, cargo bin, npm-global bin). Authors can
	// override per-layer via layer.yml path_append: as today. Empty list
	// means "this builder doesn't contribute to PATH" (aur — installs
	// to /usr/bin via pacman -U).
	PathContributions []string `yaml:"path_contributions,omitempty"`

	// Kind discriminates between layer builders (default — produce
	// artifacts COPY'd into the final image via multi-stage Containerfile)
	// and bootstrap builders (produce a complete rootfs that REPLACES the
	// FROM line via `FROM scratch + ADD`). Empty defaults to "layer".
	Kind string `yaml:"kind,omitempty"`

	// Privileged builders run as a pre-build podman invocation outside
	// `podman build` (because pacstrap/debootstrap need /dev, namespaces,
	// mount, which buildah's RUN does not reliably grant). The output
	// (typically a tarball) is staged and then ADDed by the Containerfile.
	Privileged bool `yaml:"privileged,omitempty"`

	// OutputArtifact is the absolute path inside the privileged builder
	// container where the produced artifact lands. The pre-build phase
	// copies it out to .build/<image>/<builder-name>.<ext>. Required when
	// Privileged is true.
	OutputArtifact string `yaml:"output_artifact,omitempty"`
}

// IsBootstrap reports whether this builder produces a rootfs that
// replaces the FROM line (kind: bootstrap). Defaults to false (layer
// builder) when Kind is empty.
func (b *BuilderDef) IsBootstrap() bool {
	if b == nil {
		return false
	}
	return b.Kind == "bootstrap"
}

// PhaseTemplate is the BuilderDef analog of FormatDef.PhaseTemplate.
// Same fallback rules apply: (PhaseInstall, container) falls back to
// legacy InstallTemplate or StageTemplate when Phases is absent.
func (b *BuilderDef) PhaseTemplate(phase Phase, venue Venue) string {
	if b == nil {
		return ""
	}
	if b.Phases != nil {
		var pt *PhaseTemplates
		switch phase {
		case PhasePrepare:
			pt = b.Phases.Prepare
		case PhaseInstall:
			pt = b.Phases.Install
		case PhaseCleanup:
			pt = b.Phases.Cleanup
		}
		if pt != nil {
			switch venue {
			case VenueHostNative:
				if pt.Host != "" {
					return pt.Host
				}
			case VenueContainerBuilder:
				if pt.Container != "" {
					return pt.Container
				}
			}
		}
	}
	// Legacy fallbacks. Builders have two legacy fields: Inline builders
	// (cargo) use InstallTemplate; non-inline (pixi/npm/aur) use
	// StageTemplate. The host path needs the container-shaped template to
	// synthesize a podman-run equivalent.
	if phase == PhaseInstall && venue == VenueContainerBuilder {
		if b.Inline && b.InstallTemplate != "" {
			return b.InstallTemplate
		}
		return b.StageTemplate
	}
	return ""
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
//
// ResolveFormatConfigData has been removed. Build config resolution now goes
// through LoadUnified — which reads overthink.yml + includes: (local and
// remote-ref). See ov/unified.go.

// BuildFile is the on-disk schema of build.yml — three optional top-level
// sections that map directly onto DistroConfig/BuilderConfig/InitConfig.
type BuildFile struct {
	Distro  map[string]*DistroDef  `yaml:"distro"`
	Builder map[string]*BuilderDef `yaml:"builder"`
	Init    map[string]*InitDef    `yaml:"init"`
}

// LoadBuildConfigForImage loads distro, builder, and init configs for the
// project at dir. Post-unified-cutover this reads from overthink.yml (via
// LoadUnified) rather than following a format_config: pointer.
//
// The init section is optional: projects without an `inits:` block return a
// nil *InitConfig (no init system, no entrypoint beyond the base image default).
func LoadBuildConfigForImage(dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !present {
		return nil, nil, nil, fmt.Errorf("no overthink.yml found in %s (run `ov migrate unified`)", dir)
	}
	return uf.ProjectDistroConfig(), uf.ProjectBuilderConfig(), uf.ProjectInitConfig(), nil
}

// LoadDefaultBuildConfig is retained as an alias for the single-argument form.
// Former call sites pass just the project directory; the legacy (defaultRef,
// dir) two-argument form is gone.
func LoadDefaultBuildConfig(dir string) (*DistroConfig, *BuilderConfig, *InitConfig, error) {
	return LoadBuildConfigForImage(dir)
}
