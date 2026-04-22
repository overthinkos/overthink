package main

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Config represents the image.yml configuration file
type Config struct {
	Defaults ImageConfig            `yaml:"defaults"`
	Images   map[string]ImageConfig `yaml:"images"`
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
	CapAdd      []string `yaml:"cap_add,omitempty" json:"cap_add,omitempty"`
	Devices     []string `yaml:"devices,omitempty" json:"devices,omitempty"`
	SecurityOpt []string `yaml:"security_opt,omitempty" json:"security_opt,omitempty"`
	ShmSize     string   `yaml:"shm_size,omitempty" json:"shm_size,omitempty"`   // shared memory size (e.g. "1g", "256m")
	GroupAdd    []string `yaml:"group_add,omitempty" json:"group_add,omitempty"` // --group-add values (e.g. "keep-groups", "video")
	Mounts      []string `yaml:"mounts,omitempty" json:"mounts,omitempty"`       // host mounts (e.g. "/dev/input:/dev/input:rw", "tmpfs:/run/udev:rw,size=1m")
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

// AllBuilders returns a deduplicated sorted list of builder image names.
func (m BuilderMap) AllBuilders() []string {
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
type ImageConfig struct {
	Enabled   *bool         `yaml:"enabled,omitempty"`
	Version   string        `yaml:"version,omitempty"`  // CalVer version (YYYY.DDD.HHMM) of this image definition
	Status    string        `yaml:"status,omitempty"`   // working, testing, broken (default: testing)
	Info      string        `yaml:"info,omitempty"`     // free-form description of what works/doesn't
	Base      string        `yaml:"base,omitempty"`
	Bootc     bool          `yaml:"bootc,omitempty"`
	Platforms []string      `yaml:"platforms,omitempty"`
	Tag       string        `yaml:"tag,omitempty"`
	Registry  string        `yaml:"registry,omitempty"`
	Distro    []string      `yaml:"distro,omitempty"`       // distro tags ["fedora:43", "fedora"] — first-match for packages
	Build     BuildFormats  `yaml:"build,omitempty"`        // package formats ["rpm"] — all installed in order
	Layers    []string      `yaml:"layers,omitempty"`
	Ports     []string      `yaml:"ports,omitempty"`    // runtime port mappings ["host:container"]
	User       string        `yaml:"user,omitempty"`         // username (default: "user")
	UID        *int          `yaml:"uid,omitempty"`          // user ID (default: 1000)
	GID        *int          `yaml:"gid,omitempty"`          // group ID (default: 1000)
	UserPolicy string        `yaml:"user_policy,omitempty"`  // how to reconcile user: with base_image's pre-existing account ("auto" (default) | "adopt" | "create")
	Merge     *MergeConfig  `yaml:"merge,omitempty"`    // layer merge settings
	Aliases    []AliasConfig     `yaml:"aliases,omitempty"`      // command aliases
	Builder    BuilderMap        `yaml:"builder,omitempty"`      // build type → builder image (pixi, npm, cargo, aur)
	Builds     []string          `yaml:"builds,omitempty"`       // what this builder image can build (pixi, npm, cargo, aur)
	DNS        string            `yaml:"dns,omitempty"`          // DNS hostname for traefik routing and tunnels
	AcmeEmail  string            `yaml:"acme_email,omitempty"`   // email for Let's Encrypt notifications
	Tunnel     *TunnelYAML       `yaml:"tunnel,omitempty"`       // tunnel configuration (tailscale or cloudflare)
	Env        []string          `yaml:"env,omitempty"`          // runtime env vars (KEY=VALUE)
	EnvFile    string            `yaml:"env_file,omitempty"`     // path to env file for runtime injection
	Security   *SecurityConfig   `yaml:"security,omitempty"`     // container security options
	Network    string            `yaml:"network,omitempty"`      // container network mode (e.g. "host", "none", "slirp4netns")
	Engine     string            `yaml:"engine,omitempty" json:"engine,omitempty"` // per-image run engine override ("docker", "podman", or "")
	Init         string            `yaml:"init,omitempty"`          // explicit init system override ("supervisord", "systemd", "")
	DataImage    bool              `yaml:"data_image,omitempty"`    // true = scratch-based data-only image (no runtime, no init)

	// Tests are image-level declarative checks (cross-layer invariants).
	// Entries without explicit scope default to "build" and land in the
	// image section of the OCI label.
	Tests []Check `yaml:"tests,omitempty"`

	// DeployTests are image-author-supplied deploy-level defaults. All
	// entries default to scope: deploy and land in the deploy section of
	// the OCI label; local deploy.yml can override them by id.
	DeployTests []Check `yaml:"deploy_tests,omitempty"`
}

// IsEnabled returns true if the image is enabled (nil defaults to true)
func (ic *ImageConfig) IsEnabled() bool {
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
type ResolvedImage struct {
	Name      string
	Version   string `json:"version,omitempty"`  // CalVer version from image.yml
	Status    string `json:"status,omitempty"`   // effective status (worst of image + layers)
	Info      string `json:"info,omitempty"`     // aggregated info from image + layers
	Base      string   // Resolved base (external OCI ref or internal image name)
	Bootc     bool
	Platforms []string
	Tag       string
	Registry  string
	Pkg          string   // primary build format (first entry in BuildFormats) — for cache mounts, bootstrap
	Distro       []string // resolved distro tags: ["fedora:43", "fedora"]
	BuildFormats []string // resolved build formats: ["rpm"] or ["pac", "aur"] — all installed in order
	Tags         []string // union: ["all"] + Distro + BuildFormats — for task matching
	Layers    []string
	Ports     []string // runtime port mappings

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

	// DNS and ACME configuration
	DNS       string // DNS hostname for traefik routing and tunnels
	AcmeEmail string // email for Let's Encrypt notifications

	// Tunnel configuration
	Tunnel *TunnelConfig `json:",omitempty"` // resolved tunnel config (nil if no tunnel)

	// Container network mode (e.g. "host", "none")
	Network string

	// Per-image run engine override (resolved from image config and layer requirements)
	Engine string `json:"engine,omitempty"`

	// Build config (resolved per-image from format_config ref to build.yml)
	DistroConfig  *DistroConfig  `json:"-"` // distro section of build.yml
	DistroDef     *DistroDef     `json:"-"` // resolved distro definition (cached)
	BuilderConfig *BuilderConfig `json:"-"` // builder section of build.yml
	InitConfig    *InitConfig    `json:"-"` // init section of build.yml
	InitSystem    string         `json:"-"` // resolved init system name ("supervisord", "systemd", "")
	InitDef       *InitDef       `json:"-"` // resolved init definition (cached)

	// Data image (scratch-based, data-only)
	DataImage bool // true = FROM scratch, no runtime, no init, no services

	// Derived fields
	IsExternalBase bool   // true if base is external OCI image, false if internal
	FullTag        string // registry/name:tag
}

// SupportsTag returns true if this image has the given tag.
// Tags include format (rpm, deb, pac), distro (fedora, archlinux),
// version (fedora:43), and the implicit "all".
func (img *ResolvedImage) SupportsTag(tag string) bool {
	for _, t := range img.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// SupportsBuild returns true if this image has the given build format.
func (img *ResolvedImage) SupportsBuild(format string) bool {
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
		return nil, fmt.Errorf("no overthink.yml found in %s (run `ov migrate unified` to convert legacy image.yml/build.yml/deploy.yml)", dir)
	}
	return uf.ProjectConfig(), nil
}

// ResolveImage resolves a single image's configuration by applying defaults
func (c *Config) ResolveImage(name string, calverTag string, dir string) (*ResolvedImage, error) {
	img, ok := c.Images[name]
	if !ok {
		return nil, fmt.Errorf("image %q not found in image.yml", name)
	}
	if !img.IsEnabled() {
		return nil, fmt.Errorf("image %q is disabled", name)
	}

	resolved := &ResolvedImage{
		Name:    name,
		Version: img.Version,
		Status:  img.Status,
		Info:    img.Info,
	}

	// Data images are always FROM scratch — no base resolution needed
	if img.DataImage {
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

		// Check if base is internal (another enabled image in image.yml) or external
		if baseImg, isInternal := c.Images[resolved.Base]; isInternal && baseImg.IsEnabled() {
			resolved.IsExternalBase = false
		} else {
			resolved.IsExternalBase = true
		}
	}

	// Resolve bootc: image -> defaults -> false
	resolved.Bootc = img.Bootc
	if !resolved.Bootc {
		resolved.Bootc = c.Defaults.Bootc
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
	resolved.Layers = make([]string, len(img.Layers))
	for i, ref := range img.Layers {
		resolved.Layers[i] = BareRef(ref)
	}

	// Resolve ports: image -> defaults -> nil
	resolved.Ports = img.Ports
	if len(resolved.Ports) == 0 {
		resolved.Ports = c.Defaults.Ports
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

	// Resolve builder: image -> base image (if internal) -> defaults -> {}
	resolved.Builder = make(BuilderMap)
	for typ, builder := range c.Defaults.Builder {
		resolved.Builder[typ] = builder
	}
	if !resolved.IsExternalBase {
		if baseImg, ok := c.Images[resolved.Base]; ok {
			for typ, builder := range baseImg.Builder {
				resolved.Builder[typ] = builder
			}
		}
	}
	for typ, builder := range img.Builder {
		resolved.Builder[typ] = builder
	}
	// Filter self-references (builder images must not use themselves)
	for typ, builder := range resolved.Builder {
		if builder == name {
			delete(resolved.Builder, typ)
		}
	}

	// BuilderCapabilities: image-specific capability declaration, NOT inherited
	resolved.BuilderCapabilities = img.Builds

	// Resolve DNS: image -> defaults -> ""
	resolved.DNS = img.DNS
	if resolved.DNS == "" {
		resolved.DNS = c.Defaults.DNS
	}

	// Resolve AcmeEmail: image -> defaults -> ""
	resolved.AcmeEmail = img.AcmeEmail
	if resolved.AcmeEmail == "" {
		resolved.AcmeEmail = c.Defaults.AcmeEmail
	}

	// Tunnel config is a deploy-time concern — resolved from deploy.yml only.
	// image.yml tunnel: field is ignored (kept in struct for YAML compat).

	// VM configuration (disk_size, ram, cpus, firmware, libvirt, …) lives
	// on `kind: vm` entities in vms.yml, NOT on image.yml entries. The
	// legacy ImageConfig.Vm / .Libvirt fields were removed in the VM
	// hard-cutover; `bootc: true` on an image now only declares that the
	// container image is bootc-bootable (for `ov vm build` to produce a
	// qcow2 via `bootc install to-disk`). To run that bootc image as a
	// VM, declare a paired `kind: vm` entity with `source.kind: bootc`
	// in vms.yml (see `ov migrate vm-spec`).

	// Resolve network: image -> defaults -> ""
	resolved.Network = img.Network
	if resolved.Network == "" {
		resolved.Network = c.Defaults.Network
	}

	// Resolve engine: image -> defaults -> "" (layer requirements resolved separately)
	resolved.Engine = img.Engine
	if resolved.Engine == "" {
		resolved.Engine = c.Defaults.Engine
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

// ResolveAllImages resolves all enabled images in the config
func (c *Config) ResolveAllImages(calverTag string, dir string) (map[string]*ResolvedImage, error) {
	resolved := make(map[string]*ResolvedImage)
	for name, img := range c.Images {
		if !img.IsEnabled() {
			continue
		}
		ri, err := c.ResolveImage(name, calverTag, dir)
		if err != nil {
			return nil, err
		}
		resolved[name] = ri
	}
	return resolved, nil
}

// ImageNames returns a sorted list of enabled image names
func (c *Config) ImageNames() []string {
	names := make([]string, 0, len(c.Images))
	for name, img := range c.Images {
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
// now lives on `kind: vm` entities in vms.yml (VmSpec); image.yml
// entries no longer carry vm: or libvirt: fields.

// walkBaseChainDistro walks the base chain through image.yml entries to find
// the first ancestor with a distro: field set. Returns nil if no ancestor
// defines distro tags or the chain reaches an external base image.
func (c *Config) walkBaseChainDistro(baseName string) []string {
	seen := make(map[string]bool)
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		baseImg, ok := c.Images[current]
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Distro) > 0 {
			return baseImg.Distro
		}
		if baseImg.Base == "" {
			return nil
		}
		current = baseImg.Base
	}
}

// walkBaseChainBuild walks the base chain through image.yml entries to find
// the first ancestor with a build: field set. Returns nil if no ancestor
// defines build formats or the chain reaches an external base image.
func (c *Config) walkBaseChainBuild(baseName string) []string {
	seen := make(map[string]bool)
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		baseImg, ok := c.Images[current]
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Build) > 0 {
			return []string(baseImg.Build)
		}
		if baseImg.Base == "" {
			return nil
		}
		current = baseImg.Base
	}
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
