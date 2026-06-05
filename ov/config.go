package main

import (
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// Config represents the image.yml configuration file
type Config struct {
	Defaults BoxConfig            `yaml:"defaults"`
	Image    map[string]BoxConfig `yaml:"box"`
	// Local carries kind:local templates so remote-ref collection +
	// validation walk their layer: lists symmetrically with image layer
	// lists (kind:local templates compose remote @-ref layers too). Populated
	// from UnifiedFile.Local by ProjectConfig().
	Local map[string]*LocalSpec `yaml:"local,omitempty"`
	// Namespaces carries child namespaces mounted by namespaced `import:`
	// entries (alias → projected sub-Config). Qualified refs like
	// `cachyos.cachyos` resolve through here. Populated from
	// UnifiedFile.Namespaces by ProjectConfig(). See namespace.go.
	Namespaces map[string]*Config `yaml:"-"`
}

// BuildFormats handles YAML unmarshal of the build: field.
// Package formats tied to the defined builders, installed in list order.
// Single string "rpm" becomes ["rpm"]. List ["pac", "aur"] stays as-is.
type BuildFormats []string

func (b *BuildFormats) UnmarshalYAML(value *yaml.Node) error {
	// Try string first
	if value.Kind == yaml.ScalarNode {
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		if s != "" {
			*b = BuildFormats{s}
		}
		return nil
	}
	// Try list
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*b = BuildFormats(list)
	return nil
}

// MergeConfig configures post-build layer merging
type MergeConfig struct {
	Auto       bool `yaml:"auto,omitempty"`         // enable automatic merging after builds
	MaxMB      int  `yaml:"max_mb,omitempty"`       // maximum size of a merged layer (default: 128)
	MaxTotalMB int  `yaml:"max_total_mb,omitempty"` // maximum total image size for merge (0 = no limit)
}

// AliasConfig represents a command alias in image.yml
type AliasConfig struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command,omitempty"` // defaults to Name if empty
}

// SecurityConfig holds container security options (privileged, capabilities, devices).
type SecurityConfig struct {
	Privileged  bool     `yaml:"privileged,omitempty" json:"privileged,omitempty"`
	CgroupNS    string   `yaml:"cgroupns,omitempty" json:"cgroupns,omitempty"` // --cgroupns=<value>: "host" | "private" | "". Layer-intrinsic; needed by workloads like k3s that require host cgroup controllers (cpuset) not delegated to rootless sub-slices.
	CapAdd      []string `yaml:"cap_add,omitempty" json:"cap_add,omitempty"`
	Devices     []string `yaml:"devices,omitempty" json:"devices,omitempty"`
	SecurityOpt []string `yaml:"security_opt,omitempty" json:"security_opt,omitempty"`
	IpcMode     string   `yaml:"ipc_mode,omitempty" json:"ipc_mode,omitempty"`   // --ipc=<value>: "host" | "private" | "shareable" | "". When "host", podman REJECTS shm_size (the host's /dev/shm is shared in-kernel, sized by the host); the quadlet generator drops ShmSize= directives in that case.
	ShmSize     string   `yaml:"shm_size,omitempty" json:"shm_size,omitempty"`   // shared memory size (e.g. "1g", "256m")
	GroupAdd    []string `yaml:"group_add,omitempty" json:"group_add,omitempty"` // --group-add values (e.g. "keep-groups", "video")
	Mounts      []string `yaml:"mount,omitempty" json:"mounts,omitempty"`        // host mounts (e.g. "/dev/input:/dev/input:rw", "tmpfs:/run/udev:rw,size=1m")
	// Resource caps. Sizes use the same suffixes as ShmSize ("6g", "500m", "1024k").
	// Layer merging is smallest-wins (tightest cap is safest); image-level values override.
	MemoryMax     string `yaml:"memory_max,omitempty" json:"memory_max,omitempty"`           // hard OOM threshold (cgroup memory.max, podman --memory, systemd MemoryMax)
	MemoryHigh    string `yaml:"memory_high,omitempty" json:"memory_high,omitempty"`         // soft limit — reclaim pressure kicks in (systemd MemoryHigh)
	MemorySwapMax string `yaml:"memory_swap_max,omitempty" json:"memory_swap_max,omitempty"` // swap ceiling (podman --memory-swap, systemd MemorySwapMax)
	Cpus          string `yaml:"cpus,omitempty" json:"cpus,omitempty"`                       // CPU quota in cores ("2.5" = 2.5 cores → podman --cpus / systemd CPUQuota=250%)
}

// BuilderMap is a map of build type → builder image name.
// Valid build types: pixi, npm, cargo, aur.
type BuilderMap map[string]string

// BuilderFor returns the builder image name for the given format, or "".
func (m BuilderMap) BuilderFor(format string) string {
	return m[format]
}

// HasBuilder returns true if a builder is configured for the given format.
func (m BuilderMap) HasBuilder(format string) bool {
	return m[format] != ""
}

// AllBuilder returns a deduplicated sorted list of builder image names.
func (m BuilderMap) AllBuilder() []string {
	seen := make(map[string]bool)
	var builders []string
	for _, b := range m {
		if b != "" && !seen[b] {
			seen[b] = true
			builders = append(builders, b)
		}
	}
	sortStrings(builders)
	return builders
}

// ImageConfig represents configuration for a single image or defaults
type BoxConfig struct {
	Enabled     *bool        `yaml:"enabled,omitempty"`
	Version     string       `yaml:"version,omitempty"`     // CalVer version (YYYY.DDD.HHMM) of this image definition
	Description *Description `yaml:"description,omitempty"` // Gherkin-shaped self-description; replaces retired info:/status:
	Base        string       `yaml:"base,omitempty"`
	// From selects a non-registry base via "builder:<name>" — the named
	// builder must be kind: bootstrap and runs as a pre-build privileged
	// container that produces a rootfs tarball, then the Containerfile
	// emits FROM scratch + ADD. Mutually exclusive with Base.
	From                  string        `yaml:"from,omitempty"`
	BootstrapBuilderImage string        `yaml:"bootstrap_builder_image,omitempty"`
	Platforms             []string      `yaml:"platform,omitempty"`
	Tag                   string        `yaml:"tag,omitempty"`
	Registry              string        `yaml:"registry,omitempty"`
	Distro                []string      `yaml:"distro,omitempty"` // distro tags ["fedora:43", "fedora"] — first-match for packages
	Build                 BuildFormats  `yaml:"build,omitempty"`  // package formats ["rpm"] — all installed in order
	Layer                 []string      `yaml:"candy,omitempty"`
	Port                  []string      `yaml:"port,omitempty"`        // runtime port mappings ["host:container"]
	User                  string        `yaml:"user,omitempty"`        // username (default: "user")
	UID                   *int          `yaml:"uid,omitempty"`         // user ID (default: 1000)
	GID                   *int          `yaml:"gid,omitempty"`         // group ID (default: 1000)
	UserPolicy            string        `yaml:"user_policy,omitempty"` // how to reconcile user: with base_image's pre-existing account ("auto" (default) | "adopt" | "create")
	Merge                 *MergeConfig  `yaml:"merge,omitempty"`       // layer merge settings
	Alias                 []AliasConfig `yaml:"alias,omitempty"`       // command aliases
	Builder               BuilderMap    `yaml:"builder,omitempty"`     // build type → builder image (pixi, npm, cargo, aur)
	Produce               []string      `yaml:"produce,omitempty"`     // what this builder image can produce (pixi, npm, cargo, aur). Renamed from `builds:` to avoid yaml key collision with the `build:` BuildFormats above (field-singular cutover, 2026-05).
	// Schema v4: DNS / AcmeEmail / Tunnel / Engine removed — they are
	// deployment choices with no declaration meaning. They live on
	// DeploymentNode and flow through to consumers via ImageMetadata.
	Env       []string        `yaml:"env,omitempty"`        // runtime env vars (KEY=VALUE) — declaration of vars the image consumes
	EnvFile   string          `yaml:"env_file,omitempty"`   // path to env file for runtime injection
	Security  *SecurityConfig `yaml:"security,omitempty"`   // container security options — declaration of required capabilities
	Network   string          `yaml:"network,omitempty"`    // container network mode — declaration of required/recommended mode
	Init      string          `yaml:"init,omitempty"`       // explicit init system override ("supervisord", "systemd", "")
	DataImage bool            `yaml:"data_image,omitempty"` // true = scratch-based data-only image (no runtime, no init)

	// Build-speed tunables — authored under `defaults:` (project-wide build
	// knobs, not per-image-output settings). The CLI flag / env layer wins,
	// then these `defaults:` values, then a named Go fallback (see build.go
	// jobsFallback / podmanJobsCapFallback). Pointers distinguish "unset"
	// from a deliberate zero so the precedence chain is exact.
	Jobs          *int     `yaml:"jobs,omitempty"`            // outer: concurrent IMAGE builds per DAG level (flag --jobs / env OV_BUILD_JOBS)
	PodmanJobs    *int     `yaml:"podman_jobs,omitempty"`     // inner: stages per `podman build` (0 = auto; flag --podman-jobs / env OV_PODMAN_JOBS)
	PodmanJobsCap *int     `yaml:"podman_jobs_cap,omitempty"` // ceiling for the auto podman-jobs calc: min(NCPU, cap)
	ContextIgnore []string `yaml:"context_ignore,omitempty"`  // extra build-context excludes merged into the generated .containerignore/.dockerignore
	Cache         string   `yaml:"cache,omitempty"`           // default build cache mode (image|registry|gha|none); flag --cache / env OV_BUILD_CACHE wins

	// Reusable-artifact retention (project-wide; authored under defaults:).
	// keep_images = newest CalVer tags to keep per image after `ov image build`;
	// keep_eval_runs = newest run dirs to keep per bed/score after `ov eval run`.
	// 0 (or absent → Go fallback 0) disables pruning. See `ov clean`.
	KeepImages   *int `yaml:"keep_images,omitempty"`
	KeepEvalRuns *int `yaml:"keep_eval_runs,omitempty"`

	// Tests are image-level declarative checks (cross-layer invariants).
	// Entries without explicit scope default to "build" and land in the
	// image section of the OCI label.
	Eval []Check `yaml:"eval,omitempty"`

	// DeployTests are image-author-supplied deploy-level defaults. All
	// entries default to scope: deploy and land in the deploy section of
	// the OCI label; local deploy.yml can override them by id.
	DeployEval []Check `yaml:"deploy_eval,omitempty"`

	// Shell is an image-level shell-init contribution layered on top of
	// what the included layers contribute. Same shape as the layer.yml
	// `shell:` field — generic body + per-shell overrides. Travels in
	// the org.overthinkos.shell OCI label under the Image section.
	// 2026-05 cutover.
	Shell *ShellConfig `yaml:"shell,omitempty"`
}

// IsEnabled returns true if the image is enabled (nil defaults to true)
func (ic *BoxConfig) IsEnabled() bool {
	if ic.Enabled == nil {
		return true
	}
	return *ic.Enabled
}

// boolPtr returns a pointer to a bool value
func boolPtr(v bool) *bool {
	return &v
}

// ResolvedImage represents a fully resolved image configuration
type ResolvedBox struct {
	Name    string
	Version string `json:"version,omitempty"` // authored per-entity CalVer (image.yml `version:`); optional
	// EffectiveVersion is the content-derived identity emitted as the
	// org.overthinkos.version label: the dedicated Version if set, else the
	// highest layer version across the full chain (computeEffectiveVersions in
	// effective_version.go, run by the generator once the base chain +
	// auto-intermediates are materialized). Stable across builds when no layer
	// changed — this is what keeps a child's FROM <base> SHA from shifting.
	EffectiveVersion string `json:"effective_version,omitempty"`
	Status           string `json:"status,omitempty"` // effective status (worst of image + layers)
	Info             string `json:"info,omitempty"`   // aggregated info from image + layers
	Base             string // Resolved base (external OCI ref or internal image name)
	// From mirrors ImageConfig.From after resolution. When non-empty
	// (e.g. "builder:pacstrap"), the generator emits FROM scratch +
	// ADD <staged-rootfs.tar.gz> instead of FROM <base>.
	From                  string
	BootstrapBuilderImage string
	Platforms             []string
	Tag                   string
	Registry              string
	Pkg                   string   // primary build format (first entry in BuildFormats) — for cache mounts, bootstrap
	Distro                []string // resolved distro tags: ["fedora:43", "fedora"]
	BuildFormats          []string // resolved build formats: ["rpm"] or ["pac", "aur"] — all installed in order
	Tags                  []string // union: ["all"] + Distro + BuildFormats — for task matching
	Layer                 []string
	Port                  []string // runtime port mappings

	// User configuration
	User string // username
	UID  int    // user ID
	GID  int    // group ID
	Home string // resolved home directory (detected or /home/<user>)
	// UserAdopted is true when the resolved user came from the distro's
	// base_user declaration (build.yml `distro.<name>.base_user`) rather
	// than being created by the bootstrap. Consulted by writeBootstrap to
	// skip the useradd step — the base image already ships this account.
	UserAdopted bool

	// Merge configuration
	Merge *MergeConfig // layer merge settings (nil means use CLI defaults)

	// Builder configuration (resolved: image -> base image -> defaults -> {})
	Builder BuilderMap // build type → builder image name
	// Builder capability declaration (image-specific, not inherited)
	BuilderCapabilities []string // what this builder image can build (from builds: field)

	// Auto-generated intermediate image
	Auto bool // true for auto-generated intermediate images

	// Schema v4: DNS / AcmeEmail / Tunnel / Engine removed from
	// ResolvedImage — they are deployment choices with no declaration
	// meaning. Consumers read them from ImageMetadata (post deploy-overlay)
	// instead of from the resolved image config.

	// Container network mode (e.g. "host", "none") — declaration of
	// required/recommended network mode. Deployment overrides via
	// MergeDeployOntoMetadata.
	Network string

	// Build config (resolved per-image from format_config ref to build.yml)
	DistroConfig  *DistroConfig  `json:"-"` // distro section of build.yml
	DistroDef     *DistroDef     `json:"-"` // resolved distro definition (cached)
	BuilderConfig *BuilderConfig `json:"-"` // builder section of build.yml
	InitConfig    *InitConfig    `json:"-"` // init section of build.yml
	InitSystem    string         `json:"-"` // resolved init system name ("supervisord", "systemd", "")
	InitDef       *InitDef       `json:"-"` // resolved init definition (cached)

	// Data image (scratch-based, data-only)
	DataImage bool // true = FROM scratch, no runtime, no init, no services

	// LayerCaps aggregates layer-contributed capabilities from this
	// image's resolved layer composition (preserve_user, data_only,
	// init_system_hint, oci_labels, etc.). Populated by ResolveImage
	// via AggregateLayerCapabilities. Replaces the magic image-level
	// flags (Bootc, DataImage) with a layer-derived surface — those
	// fields remain during the cutover transition and are removed in
	// the same commit once consumers migrate to LayerCaps.
	LayerCaps *AggregatedCandyCaps `json:"-"`

	// Derived fields
	IsExternalBase bool   // true if base is external OCI image, false if internal
	FullTag        string // registry/name:tag
}

// SupportsTag returns true if this image has the given tag.
// Tags include format (rpm, deb, pac), distro (fedora, arch),
// version (fedora:43), and the implicit "all".
func (img *ResolvedBox) SupportsTag(tag string) bool {
	for _, t := range img.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// SupportsBuild returns true if this image has the given build format.
func (img *ResolvedBox) SupportsBuild(format string) bool {
	for _, b := range img.BuildFormats {
		if b == format {
			return true
		}
	}
	return false
}

// LoadConfig reads overthink.yml and returns the Config (defaults + images)
// projection. Mode purity preserved: this never merges deploy.yml content.
// Deploy-mode commands must call LoadDeployConfig + MergeDeployOverlay
// explicitly.
func LoadConfig(dir string) (*Config, error) {
	return LoadConfigRaw(dir)
}

// LoadConfigRaw is an alias retained for call sites that previously
// distinguished raw-vs-merged loads. Both forms now read overthink.yml via
// LoadUnified and return the Images projection.
func LoadConfigRaw(dir string) (*Config, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !present {
		return nil, fmt.Errorf("no overthink.yml found in %s (run `ov migrate` to convert legacy image.yml/build.yml/deploy.yml)", dir)
	}
	cfg := uf.ProjectConfig()
	return cfg, nil
}

// ResolveOpts carries optional knobs for ResolveImage. Zero value is the
// default behavior used by every code path EXCEPT the explicit operational
// overrides on `ov image build/inspect/validate --include-disabled` —
// those set IncludeDisabled to bypass the `enabled: false` gate without
// requiring the operator to flip authored config.
//
// IncludeDisabledNames scopes the override: when non-empty, ONLY images in
// the set bypass the disabled check; other disabled images stay filtered.
// Used by `ov image build <name> --include-disabled` so widening the
// working set doesn't surface unrelated disabled-image dep errors (e.g.
// images with remote layers that aren't fetched yet). Empty + IncludeDisabled
// = include every disabled image (the inspect/validate behavior).
type ResolveOpts struct {
	IncludeDisabled      bool            // skip the `enabled: false` check
	IncludeDisabledNames map[string]bool // when non-empty, scope IncludeDisabled to these names only
	// RequestedImages are the explicit build targets (`ov image build <name>`).
	// A qualified name here (e.g. `ov.arch-builder`) is pulled into the resolved
	// set even when it isn't reachable as a base/builder of a root image — so a
	// namespaced image can be an on-demand build target, not only a transitive
	// base. Bare names are ignored here (they resolve through the root loop).
	RequestedImages []string
}

// shouldIncludeDisabled reports whether name's disabled gate should be
// bypassed under opts. Centralizes the IncludeDisabled + IncludeDisabledNames
// interaction so call sites stay simple.
func (opts ResolveOpts) shouldIncludeDisabled(name string) bool {
	if !opts.IncludeDisabled {
		return false
	}
	if len(opts.IncludeDisabledNames) == 0 {
		return true
	}
	return opts.IncludeDisabledNames[name]
}

// ResolveImage resolves a single image's configuration by applying defaults
func (c *Config) ResolveImage(name string, calverTag string, dir string, opts ResolveOpts) (*ResolvedBox, error) {
	// Namespace-aware entry: a qualified name (e.g. `ov.arch-builder`,
	// `cachyos.cachyos`) resolves inside the Config of the namespace that
	// owns it, where its base:/builder: refs are relative. This mirrors
	// resolveImageRef's descent (namespace.go) so that EVERY ResolveImage
	// caller — `ov image inspect/generate/merge/pull/validate`,
	// ensure-image's build-fallback, `ov deploy add`/`ov update` — is
	// namespace-aware through this single chokepoint instead of each
	// re-implementing (or omitting) the descent. Additive: a bare name
	// takes the flat tail below exactly as before, so existing behaviour
	// is unchanged; only qualified names (which previously hard-errored
	// "not found") gain resolution.
	if ns, rest, ok := splitNamespaceRef(name); ok {
		sub, found := c.Namespaces[ns]
		if !found {
			return nil, fmt.Errorf("import namespace %q not found (resolving image %q)", ns, name)
		}
		return sub.ResolveImage(rest, calverTag, dir, opts)
	}
	img, ok := c.Image[name]
	if !ok {
		return nil, fmt.Errorf("image %q not found in image.yml", name)
	}
	if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
		return nil, fmt.Errorf("image %q is disabled (pass --include-disabled to operate on it without flipping authored config)", name)
	}

	resolved := &ResolvedBox{
		Name:    name,
		Version: img.Version,
		Status:  descriptionStatus(img.Description),
		Info:    descriptionInfo(img.Description),
	}

	// `from: builder:<name>` — non-registry base via a kind: bootstrap
	// builder. Mutually exclusive with base:; pre-build phase produces
	// a rootfs tarball, generator emits FROM scratch + ADD.
	if img.From != "" {
		if img.Base != "" {
			return nil, fmt.Errorf("image %s: from: and base: are mutually exclusive", name)
		}
		resolved.From = img.From
		resolved.BootstrapBuilderImage = img.BootstrapBuilderImage
		resolved.Base = "scratch"
		resolved.IsExternalBase = true
	} else if img.DataImage {
		resolved.Base = "scratch"
		resolved.IsExternalBase = true
	} else {
		// Resolve base: image -> defaults -> "quay.io/fedora/fedora:43"
		resolved.Base = img.Base
		if resolved.Base == "" {
			resolved.Base = c.Defaults.Base
		}
		if resolved.Base == "" {
			resolved.Base = "quay.io/fedora/fedora:43"
		}

		// Check if base is internal (another enabled image — local OR resolved
		// through an import namespace, e.g. `cachyos.cachyos`) or external.
		if baseImg, _, isInternal := c.resolveImageRef(resolved.Base); isInternal && baseImg.IsEnabled() {
			resolved.IsExternalBase = false
		} else {
			resolved.IsExternalBase = true
		}
	}

	// Resolve platforms: image -> defaults -> ["linux/amd64", "linux/arm64"]
	resolved.Platforms = img.Platforms
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = c.Defaults.Platforms
	}
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = []string{"linux/amd64", "linux/arm64"}
	}

	// Resolve tag: image -> defaults -> "auto"
	resolved.Tag = img.Tag
	if resolved.Tag == "" {
		resolved.Tag = c.Defaults.Tag
	}
	if resolved.Tag == "" {
		resolved.Tag = "auto"
	}
	// If tag is "auto", use the computed calver
	if resolved.Tag == "auto" {
		resolved.Tag = calverTag
	}

	// Resolve registry: image -> defaults -> ""
	resolved.Registry = img.Registry
	if resolved.Registry == "" {
		resolved.Registry = c.Defaults.Registry
	}

	// Resolve distro: image -> walk base chain (if internal) -> defaults -> []
	resolved.Distro = img.Distro
	if len(resolved.Distro) == 0 {
		resolved.Distro = c.walkBaseChainDistro(resolved.Base)
	}
	if len(resolved.Distro) == 0 {
		resolved.Distro = c.Defaults.Distro
	}

	// Resolve build: image -> walk base chain (if internal) -> defaults (required, except for data images)
	buildFmts := []string(img.Build)
	if len(buildFmts) == 0 {
		buildFmts = c.walkBaseChainBuild(resolved.Base)
	}
	if len(buildFmts) == 0 {
		buildFmts = []string(c.Defaults.Build)
	}
	if len(buildFmts) == 0 && !img.DataImage {
		return nil, fmt.Errorf("image %s: build: field required (set in image, base, or defaults)", name)
	}
	resolved.BuildFormats = buildFmts
	if len(buildFmts) > 0 {
		resolved.Pkg = buildFmts[0] // primary format for cache mounts
	}

	// Build unified Tags for task matching: ["all"] + Distro + BuildFormats
	resolved.Tags = append([]string{"all"}, resolved.Distro...)
	resolved.Tags = append(resolved.Tags, resolved.BuildFormats...)

	// Layers are not inherited, they're image-specific
	// Strip @ prefix and :version suffixes — layer map keys use bare refs
	resolved.Layer = make([]string, len(img.Layer))
	for i, ref := range img.Layer {
		resolved.Layer[i] = BareRef(ref)
	}

	// Resolve ports: image -> defaults -> nil
	resolved.Port = img.Port
	if len(resolved.Port) == 0 {
		resolved.Port = c.Defaults.Port
	}

	// Resolve user: image -> defaults -> "user"
	resolved.User = img.User
	if resolved.User == "" {
		resolved.User = c.Defaults.User
	}
	if resolved.User == "" {
		resolved.User = "user"
	}

	// Resolve UID: image -> defaults -> 1000
	resolved.UID = resolveIntPtr(img.UID, c.Defaults.UID, 1000)

	// Resolve GID: image -> defaults -> 1000
	resolved.GID = resolveIntPtr(img.GID, c.Defaults.GID, 1000)

	// Resolve merge config: image -> defaults -> nil
	if img.Merge != nil {
		resolved.Merge = img.Merge
	} else if c.Defaults.Merge != nil {
		resolved.Merge = c.Defaults.Merge
	}

	// Builder resolution flows through the ONE canonical method so it can't
	// diverge across commands (build/generate/inspect via ResolveImage,
	// `ov deploy add`'s synthetic host/VM image, and the remote-ref fetch walk
	// via effectiveBuilderForImage all call resolveEffectiveBuilder).
	resolved.Builder = c.resolveEffectiveBuilder(name, resolved.Distro, resolved.Base, resolved.IsExternalBase, img.Builder)

	// BuilderCapabilities: image-specific capability declaration, NOT inherited
	resolved.BuilderCapabilities = img.Produce

	// Schema v4: DNS / AcmeEmail / Tunnel / Engine no longer resolve from
	// image config — they are deployment choices and flow through
	// MergeDeployOntoMetadata → ImageMetadata directly.

	// VM configuration (disk_size, ram, cpus, firmware, libvirt, …) lives
	// on `kind: vm` entities in vm.yml, NOT on image.yml entries. The
	// legacy ImageConfig.Vm / .Libvirt fields were removed in the VM
	// hard-cutover; `bootc: true` on an image now only declares that the
	// container image is bootc-bootable (for `ov vm build` to produce a
	// qcow2 via `bootc install to-disk`). To run that bootc image as a
	// VM, declare a paired `kind: vm` entity with `source.kind: bootc`
	// in vm.yml (see `ov migrate`).

	// Resolve network: image -> defaults -> ""
	resolved.Network = img.Network
	if resolved.Network == "" {
		resolved.Network = c.Defaults.Network
	}

	// Data image flag (not inherited from defaults)
	resolved.DataImage = img.DataImage

	// Home directory will be resolved later (after inspecting base image)
	if resolved.User == "root" {
		resolved.Home = "/root"
	} else {
		resolved.Home = fmt.Sprintf("/home/%s", resolved.User)
	}

	// Compute full tag
	if resolved.Registry != "" {
		resolved.FullTag = fmt.Sprintf("%s/%s:%s", resolved.Registry, name, resolved.Tag)
	} else {
		resolved.FullTag = fmt.Sprintf("%s:%s", name, resolved.Tag)
	}

	// Resolve build config from overthink.yml. Unconditional — caller must
	// supply a project dir containing overthink.yml. Tests that need
	// in-memory-only resolution use testProjectDir(t).
	distroCfg, builderCfg, initCfg, err := LoadBuildConfigForImage(dir)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", name, err)
	}
	resolved.DistroConfig = distroCfg
	resolved.BuilderConfig = builderCfg
	resolved.InitConfig = initCfg
	if distroCfg != nil {
		resolved.DistroDef = distroCfg.ResolveDistro(resolved.Distro)
	}

	// Reconcile user_policy against the distro's base_user declaration.
	// Must run after DistroDef is resolved. Updates resolved.User/UID/GID/
	// Home when adopting so all downstream substitution (${USER}, ${HOME},
	// COPY --chown, sudoers writes) sees the adopted identity.
	policy := img.UserPolicy
	if policy == "" {
		policy = c.Defaults.UserPolicy
	}
	if policy == "" {
		policy = "auto"
	}
	baseUser := (*BaseUserDef)(nil)
	if resolved.DistroDef != nil {
		baseUser = resolved.DistroDef.BaseUser
	}
	userExplicitlySet := img.User != "" || c.Defaults.User != ""
	switch policy {
	case "adopt":
		if baseUser == nil {
			return nil, fmt.Errorf("image %s: user_policy: adopt requires distro %v to declare base_user in build.yml", name, resolved.Distro)
		}
		resolved.User = baseUser.Name
		resolved.UID = baseUser.UID
		resolved.GID = baseUser.GID
		resolved.Home = baseUser.Home
		resolved.UserAdopted = true
	case "auto":
		if baseUser != nil && !userExplicitlySet {
			resolved.User = baseUser.Name
			resolved.UID = baseUser.UID
			resolved.GID = baseUser.GID
			resolved.Home = baseUser.Home
			resolved.UserAdopted = true
		}
	case "create":
		// no-op — resolved.User/UID/GID/Home already reflect image config +
		// defaults + hardcoded fallback, and writeBootstrap will useradd.
	default:
		return nil, fmt.Errorf("image %s: unknown user_policy %q (expected auto, adopt, or create)", name, policy)
	}

	return resolved, nil
}

// ResolveAllImage resolves all enabled images in the config. opts.IncludeDisabled
// extends the working set to images marked enabled: false (the build verb's
// `--include-disabled` flag flips this for one-off operational rebuilds
// without modifying authored config).
func (c *Config) ResolveAllImage(calverTag string, dir string, opts ResolveOpts) (map[string]*ResolvedBox, error) {
	resolved := make(map[string]*ResolvedBox)
	for name, img := range c.Image {
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		ri, err := c.ResolveImage(name, calverTag, dir, opts)
		if err != nil {
			return nil, err
		}
		resolved[name] = ri
	}
	// Pull in any namespace-qualified base images (e.g. versa's
	// `base: cachyos.cachyos`) resolved within their own namespace context,
	// keyed by the fully-qualified name, so the build graph + generator can
	// reference them. See namespace.go.
	// Pull in any explicitly-requested namespace-qualified targets BEFORE base
	// resolution. resolveNamespacedBases is reachability-scoped (it only follows
	// bases/builders of root images); an on-demand target like
	// `ov image build ov.arch-builder` — or ensure-image's build-fallback for a
	// namespaced builder — must be pulled explicitly so it lands in the resolved
	// map under its fully-qualified key. Pulling it FIRST lets the
	// resolveNamespacedBases fixpoint below also collect the target's own
	// transitive bases AND builders (it iterates the resolved set), so the build
	// graph + filterImage have every dependency. Uses the SAME
	// pullNamespacedImage path as the base pull.
	for _, name := range opts.RequestedImages {
		if _, _, qualified := splitNamespaceRef(name); !qualified {
			continue
		}
		if _, done := resolved[name]; done {
			continue
		}
		if err := c.pullNamespacedImage(c, name, "", calverTag, dir, opts, resolved); err != nil {
			return nil, err
		}
	}
	if err := c.resolveNamespacedBases(resolved, calverTag, dir, opts); err != nil {
		return nil, err
	}
	return resolved, nil
}

// ImageNames returns a sorted list of enabled image names
func (c *Config) ImageNames() []string {
	names := make([]string, 0, len(c.Image))
	for name, img := range c.Image {
		if !img.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	// Sort for deterministic output
	sortStrings(names)
	return names
}

// resolveIntPtr resolves a *int value through fallback chain: value -> fallback -> defaultVal
func resolveIntPtr(value, fallback *int, defaultVal int) int {
	if value != nil {
		return *value
	}
	if fallback != nil {
		return *fallback
	}
	return defaultVal
}

// intPtr returns a pointer to an int value
func intPtr(v int) *int {
	return &v
}

// resolveVmConfig was removed in the VM hard-cutover. VM configuration
// now lives on `kind: vm` entities in vm.yml (VmSpec); image.yml
// entries no longer carry vm: or libvirt: fields.

// resolveEffectiveBuilder computes an image's effective builder map via the
// SINGLE canonical precedence, lowest→highest:
//
//	defaults.builder       (the project-wide baseline)
//	→ distro-keyed default (the root image whose distro: matches THIS image's
//	                        resolved distro — so an arch/cachyos image
//	                        auto-selects arch-builder, a fedora image
//	                        fedora-builder, with NO per-image builder: map)
//	→ direct local base    (a same-namespace base's builder map)
//	→ per-image override    (img.Builder)
//
// then self-references are filtered (a builder image must not use itself).
//
// Why distro-keyed and not base-inherited: a builder map holds
// namespace-relative REFS, so it can't be copied across an import-namespace
// boundary (a base's `ov.arch-builder` would dangle in a consumer where `ov.`
// doesn't resolve — see ov/namespace.go). `distro:` IS a value and DOES cross
// the boundary, so we key off the resolved distro and source the builder map
// from a root-namespace image whose bare refs resolve HERE.
//
// EVERY builder-consuming path calls this — ResolveImage (image.yml images), the
// synthetic host/VM image in `ov deploy add` (deploy_add_cmd.go), and the
// remote-ref FETCH walk (effectiveBuilderForImage → CollectRemoteRefsOpts) — so
// the resolution can never drift between commands and the fetch set stays in
// lockstep with the resolve set.
func (c *Config) resolveEffectiveBuilder(name string, distro []string, base string, isExternalBase bool, imgBuilder BuilderMap) BuilderMap {
	out := make(BuilderMap)
	for typ, b := range c.Defaults.Builder {
		out[typ] = b
	}
	for typ, b := range c.distroBuilderMap(distro) {
		out[typ] = b
	}
	if !isExternalBase {
		// DELIBERATELY flat (not resolveImageRef): a base's builder map is only
		// inherited when the base is ROOT-local. A namespace-qualified base
		// (e.g. `cachyos.cachyos`) intentionally does NOT contribute its builder
		// map here — builder: is a map of namespace-relative refs that would
		// dangle in this consumer's namespace; the consumer instead gets its
		// builder distro-keyed via distroBuilderMap above. So the qualified-base
		// miss is correct, not a divergence bug. See namespace.go's header.
		if baseImg, ok := c.Image[base]; ok {
			for typ, b := range baseImg.Builder {
				out[typ] = b
			}
		}
	}
	for typ, b := range imgBuilder {
		out[typ] = b
	}
	for typ, b := range out {
		if b == name {
			delete(out, typ)
		}
	}
	return out
}

// effectiveBuilderForImage computes the builder image refs an image will build
// against, from a RAW ImageConfig — the FETCH-path counterpart to ResolveImage's
// resolved-value path. Both end at the ONE canonical resolveEffectiveBuilder;
// this helper just supplies its inputs (base, is-external-base, distro) using the
// SAME precedence + canonical helpers ResolveImage uses (Defaults fallback,
// resolveImageRef for the internal/external line, walkBaseChainDistro for distro
// inheritance). It exists because CollectRemoteRefsOpts.collectImage runs during
// the remote-ref FETCH phase, before any ResolvedImage exists (and without the
// dir/tag ResolveImage needs); reading the raw per-image img.Builder there
// under-collected builders supplied by defaults.builder / the distro-keyed
// default (whose per-image map is empty — e.g. bazzite/aurora -> ov.fedora-builder),
// surfacing as "unknown layer" at generate time. Routing through this keeps the
// FETCH set's builder edges in lockstep with the RESOLVE set's (resolveNamespacedBases).
func (c *Config) effectiveBuilderForImage(name string, img BoxConfig) BuilderMap {
	base := "scratch"
	isExternalBase := true
	if img.From == "" && !img.DataImage {
		base = img.Base
		if base == "" {
			base = c.Defaults.Base
		}
		if base == "" {
			base = "quay.io/fedora/fedora:43"
		}
		if baseImg, _, isInternal := c.resolveImageRef(base); isInternal && baseImg.IsEnabled() {
			isExternalBase = false
		}
	}
	distro := img.Distro
	if len(distro) == 0 {
		distro = c.walkBaseChainDistro(base)
	}
	if len(distro) == 0 {
		distro = c.Defaults.Distro
	}
	return c.resolveEffectiveBuilder(name, distro, base, isExternalBase, img.Builder)
}

// distroBuilderMap returns the builder map of the root-namespace image that
// owns the given distro — the distro-keyed builder default. This is what lets
// a cachyos/Arch image auto-select `arch-builder` (and a Fedora image
// `fedora-builder`) WITHOUT a per-image `builder:` declaration: the matching
// source image (e.g. base.yml's `arch`, distro [arch], builder arch-builder)
// lives in THIS root namespace, so its bare builder refs resolve here — unlike
// a base's namespace-relative builder map, which must NOT be copied across an
// import-namespace boundary (see ov/namespace.go).
//
// distroTags is the image's resolved distro in priority order (most-specific
// first, e.g. ["cachyos","arch"] or ["fedora:43","fedora"]); the first tag with
// a matching root image wins, so a cachyos image with no root `cachyos` image
// correctly falls through to its `arch` tag → arch-builder. Only root images
// that actually declare a non-empty builder map are considered. Root-image
// iteration is name-sorted so the result is deterministic when more than one
// image shares a distro tag.
func (c *Config) distroBuilderMap(distroTags []string) BuilderMap {
	if len(distroTags) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Image))
	for name := range c.Image {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, tag := range distroTags {
		for _, name := range names {
			img := c.Image[name]
			if len(img.Builder) == 0 {
				continue
			}
			for _, d := range img.Distro {
				if d == tag {
					return img.Builder
				}
			}
		}
	}
	return nil
}

// walkBaseChainDistro walks the base chain through image.yml entries to find
// the first ancestor with a distro: field set. Returns nil if no ancestor
// defines distro tags or the chain reaches an external base image.
func (c *Config) walkBaseChainDistro(baseName string) []string {
	seen := make(map[string]bool)
	cur := c
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		// resolveImageRef crosses import namespaces (`cachyos.cachyos`); distro
		// is a VALUE so inheriting it across a namespace boundary is correct.
		baseImg, sub, ok := cur.resolveImageRef(current)
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Distro) > 0 {
			return baseImg.Distro
		}
		if baseImg.Base == "" {
			return nil
		}
		cur = sub
		current = baseImg.Base
	}
}

// walkBaseChainBuild walks the base chain through image.yml entries to find
// the first ancestor with a build: field set. Returns nil if no ancestor
// defines build formats or the chain reaches an external base image.
func (c *Config) walkBaseChainBuild(baseName string) []string {
	seen := make(map[string]bool)
	cur := c
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		// Crosses import namespaces; build: is a VALUE (format list), inherited
		// across a namespace boundary like distro:.
		baseImg, sub, ok := cur.resolveImageRef(current)
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Build) > 0 {
			return []string(baseImg.Build)
		}
		if baseImg.Base == "" {
			return nil
		}
		cur = sub
		current = baseImg.Base
	}
}

// baseChainNode is one image visited while walking an internal base chain.
// Name is the ref as it was reached (bare for a root image, namespace-qualified
// for a base reached across an import boundary, e.g. `cachyos.cachyos`).
type baseChainNode struct {
	Name string
	Img  BoxConfig
}

// walkBaseChain walks imageName's ROOT-INTERNAL base-image chain and returns
// the images in walk order (self first, then each internal base). It is the ONE
// shared base-chain traversal used by every chain-walking collector
// (CollectHooks / CollectEval / CollectShell / CollectDescription /
// CollectImageVolume) — each previously re-implemented the identical
// `for { img := cfg.Image[current]; ...; current = img.Base }` loop (R3: one
// implementation, no divergent copies), now cycle-safe for all of them.
//
// It deliberately does NOT descend import namespaces. A namespace-qualified
// base (e.g. `selkies.selkies-labwc`) is a SEPARATELY-BUILT image that owns
// its own baked eval / hooks / shell / volume labels; re-collecting its layers
// into the consumer would DOUBLE-COUNT every layer the consumer also lists
// directly (the same layer reached bare here and via its `@github…` ref in the
// base), which the per-section id-uniqueness validator correctly rejects.
// Stopping at the namespace boundary (and at external / disabled / missing
// bases) is the long-standing, semantically-correct per-image collection
// behaviour — preserved here byte-for-byte. Namespace-AWARENESS belongs to
// NAME resolution (ResolveImage / resolveImageRef / findImageByLeaf), not to
// this per-image layer-collection walk; the distro/build VALUE walkers
// (walkBaseChainDistro / walkBaseChainBuild) cross namespaces precisely because
// those are inherited values, whereas layer contributions are not.
func (c *Config) walkBaseChain(imageName string) []baseChainNode {
	var out []baseChainNode
	seen := make(map[string]bool)
	current := imageName
	for {
		if current == "" || seen[current] {
			break
		}
		seen[current] = true
		img, ok := c.Image[current]
		if !ok {
			break
		}
		out = append(out, baseChainNode{Name: current, Img: img})
		baseImg, isInternal := c.Image[img.Base]
		if !isInternal || !baseImg.IsEnabled() {
			break
		}
		current = img.Base
	}
	return out
}

// sortStrings sorts a slice of strings in place
func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
