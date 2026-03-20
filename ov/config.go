package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the images.yml configuration file
type Config struct {
	Defaults ImageConfig            `yaml:"defaults"`
	Images   map[string]ImageConfig `yaml:"images"`
}

// MergeConfig configures post-build layer merging
type MergeConfig struct {
	Auto  bool `yaml:"auto,omitempty"`   // enable automatic merging after builds
	MaxMB int  `yaml:"max_mb,omitempty"` // maximum size of a merged layer (default: 1024)
}

// AliasConfig represents a command alias in images.yml
type AliasConfig struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command,omitempty"` // defaults to Name if empty
}

// VmConfig configures virtual machine settings for bootc images
type VmConfig struct {
	DiskSize   string `yaml:"disk_size,omitempty" json:"disk_size,omitempty"`     // e.g. "10 GiB"
	RootSize   string `yaml:"root_size,omitempty" json:"root_size,omitempty"`     // root partition size (e.g. "10G")
	Ram        string `yaml:"ram,omitempty" json:"ram,omitempty"`                 // e.g. "4G"
	Cpus       int    `yaml:"cpus,omitempty" json:"cpus,omitempty"`              // e.g. 2
	KernelArgs string `yaml:"kernel_args,omitempty" json:"kernel_args,omitempty"` // extra kernel cmdline
	Rootfs     string `yaml:"rootfs,omitempty" json:"rootfs,omitempty"`          // root filesystem type (ext4, xfs, btrfs)
	Transport  string `yaml:"transport,omitempty" json:"transport,omitempty"`     // image transport (registry, containers-storage)
	SshPort    int    `yaml:"ssh_port,omitempty" json:"ssh_port,omitempty"`       // host SSH port (default: 2222)
	Firmware   string `yaml:"firmware,omitempty" json:"firmware,omitempty"`       // uefi-secure, uefi-insecure, bios
	Network    string `yaml:"network,omitempty" json:"network,omitempty"`         // network mode: user, bridge name
}

// SecurityConfig holds container security options (privileged, capabilities, devices).
type SecurityConfig struct {
	Privileged  bool     `yaml:"privileged,omitempty" json:"privileged,omitempty"`
	CapAdd      []string `yaml:"cap_add,omitempty" json:"cap_add,omitempty"`
	Devices     []string `yaml:"devices,omitempty" json:"devices,omitempty"`
	SecurityOpt []string `yaml:"security_opt,omitempty" json:"security_opt,omitempty"`
	ShmSize     string   `yaml:"shm_size,omitempty" json:"shm_size,omitempty"` // shared memory size (e.g. "1g", "256m")
}

// ImageConfig represents configuration for a single image or defaults
type ImageConfig struct {
	Enabled   *bool         `yaml:"enabled,omitempty"`
	Base      string        `yaml:"base,omitempty"`
	Bootc     bool          `yaml:"bootc,omitempty"`
	Platforms []string      `yaml:"platforms,omitempty"`
	Tag       string        `yaml:"tag,omitempty"`
	Registry  string        `yaml:"registry,omitempty"`
	Pkg       string        `yaml:"pkg,omitempty"`
	Layers    []string      `yaml:"layers,omitempty"`
	Ports     []string      `yaml:"ports,omitempty"`    // runtime port mappings ["host:container"]
	User      string        `yaml:"user,omitempty"`     // username (default: "user")
	UID       *int          `yaml:"uid,omitempty"`      // user ID (default: 1000)
	GID       *int          `yaml:"gid,omitempty"`      // group ID (default: 1000)
	Merge     *MergeConfig  `yaml:"merge,omitempty"`    // layer merge settings
	Aliases    []AliasConfig     `yaml:"aliases,omitempty"`      // command aliases
	Builder    string            `yaml:"builder,omitempty"`      // builder image name (per-image, falls back to defaults)
	DNS        string            `yaml:"dns,omitempty"`          // DNS hostname for traefik routing and tunnels
	AcmeEmail  string            `yaml:"acme_email,omitempty"`   // email for Let's Encrypt notifications
	Tunnel     *TunnelYAML       `yaml:"tunnel,omitempty"`       // tunnel configuration (tailscale or cloudflare)
	BindMounts []BindMountConfig `yaml:"bind_mounts,omitempty"`  // bind mount declarations (image-level only)
	Env        []string          `yaml:"env,omitempty"`          // runtime env vars (KEY=VALUE)
	EnvFile    string            `yaml:"env_file,omitempty"`     // path to env file for runtime injection
	Security   *SecurityConfig   `yaml:"security,omitempty"`     // container security options
	Network    string            `yaml:"network,omitempty"`      // container network mode (e.g. "host", "none", "slirp4netns")
	Engine     string            `yaml:"engine,omitempty" json:"engine,omitempty"` // per-image run engine override ("docker", "podman", or "")
	Vm         *VmConfig         `yaml:"vm,omitempty"`           // virtual machine settings (bootc images)
	Libvirt    []string          `yaml:"libvirt,omitempty"`      // raw libvirt XML snippets for VM configuration
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
	Base      string   // Resolved base (external OCI ref or internal image name)
	Bootc     bool
	Platforms []string
	Tag       string
	Registry  string
	Pkg       string
	Layers    []string
	Ports     []string // runtime port mappings

	// User configuration
	User string // username
	UID  int    // user ID
	GID  int    // group ID
	Home string // resolved home directory (detected or /home/<user>)

	// Merge configuration
	Merge *MergeConfig // layer merge settings (nil means use CLI defaults)

	// Builder image name (resolved: image -> defaults -> "")
	Builder string

	// Auto-generated intermediate image
	Auto bool // true for auto-generated intermediate images

	// DNS and ACME configuration
	DNS       string // DNS hostname for traefik routing and tunnels
	AcmeEmail string // email for Let's Encrypt notifications

	// Tunnel configuration
	Tunnel *TunnelConfig `json:",omitempty"` // resolved tunnel config (nil if no tunnel)

	// VM configuration (bootc images)
	Vm *VmConfig `json:",omitempty"` // resolved VM settings

	// Container network mode (e.g. "host", "none")
	Network string

	// Per-image run engine override (resolved from image config and layer requirements)
	Engine string `json:"engine,omitempty"`

	// Derived fields
	IsExternalBase bool   // true if base is external OCI image, false if internal
	FullTag        string // registry/name:tag
}

// LoadConfig reads and parses images.yml, then merges deploy.yml overrides.
func LoadConfig(dir string) (*Config, error) {
	cfg, err := LoadConfigRaw(dir)
	if err != nil {
		return nil, err
	}

	// Merge per-deployment overrides from deploy.yml
	dc, dcErr := LoadDeployConfig()
	if dcErr != nil {
		return nil, dcErr
	}
	MergeDeployOverlay(cfg, dc)

	return cfg, nil
}

// LoadConfigRaw reads and parses images.yml without merging deploy.yml overrides.
func LoadConfigRaw(dir string) (*Config, error) {
	path := filepath.Join(dir, "images.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading images.yml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing images.yml: %w", err)
	}

	return &cfg, nil
}

// ResolveImage resolves a single image's configuration by applying defaults
func (c *Config) ResolveImage(name string, calverTag string) (*ResolvedImage, error) {
	img, ok := c.Images[name]
	if !ok {
		return nil, fmt.Errorf("image %q not found in images.yml", name)
	}
	if !img.IsEnabled() {
		return nil, fmt.Errorf("image %q is disabled", name)
	}

	resolved := &ResolvedImage{
		Name: name,
	}

	// Resolve base: image -> defaults -> "quay.io/fedora/fedora:43"
	resolved.Base = img.Base
	if resolved.Base == "" {
		resolved.Base = c.Defaults.Base
	}
	if resolved.Base == "" {
		resolved.Base = "quay.io/fedora/fedora:43"
	}

	// Check if base is internal (another enabled image in images.yml) or external
	if baseImg, isInternal := c.Images[resolved.Base]; isInternal && baseImg.IsEnabled() {
		resolved.IsExternalBase = false
	} else {
		resolved.IsExternalBase = true
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

	// Resolve pkg: image -> defaults -> "rpm"
	resolved.Pkg = img.Pkg
	if resolved.Pkg == "" {
		resolved.Pkg = c.Defaults.Pkg
	}
	if resolved.Pkg == "" {
		resolved.Pkg = "rpm"
	}

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

	// Resolve builder: image -> defaults -> ""
	resolved.Builder = img.Builder
	if resolved.Builder == "" {
		resolved.Builder = c.Defaults.Builder
	}

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

	// Resolve tunnel: image -> defaults -> nil
	if img.Tunnel != nil {
		resolved.Tunnel = ResolveTunnelConfig(img.Tunnel, name, resolved.DNS, nil, nil, nil, resolved.Ports)
	} else if c.Defaults.Tunnel != nil {
		resolved.Tunnel = ResolveTunnelConfig(c.Defaults.Tunnel, name, resolved.DNS, nil, nil, nil, resolved.Ports)
	}

	// Resolve VM config: only for bootc images, and only when configured
	if img.Bootc && (img.Vm != nil || c.Defaults.Vm != nil) {
		resolved.Vm = resolveVmConfig(img.Vm, c.Defaults.Vm)
	}

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

	return resolved, nil
}

// ResolveAllImages resolves all enabled images in the config
func (c *Config) ResolveAllImages(calverTag string) (map[string]*ResolvedImage, error) {
	resolved := make(map[string]*ResolvedImage)
	for name, img := range c.Images {
		if !img.IsEnabled() {
			continue
		}
		ri, err := c.ResolveImage(name, calverTag)
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

// resolveVmConfig merges image-level and default-level VM config with hardcoded fallbacks.
func resolveVmConfig(img, defaults *VmConfig) *VmConfig {
	vm := &VmConfig{
		DiskSize:   "10 GiB",
		Ram:        "4G",
		Cpus:       2,
		KernelArgs: "console=ttyS0,115200n8",
		Rootfs:     "ext4",
	}
	// Apply defaults first
	if defaults != nil {
		if defaults.DiskSize != "" {
			vm.DiskSize = defaults.DiskSize
		}
		if defaults.RootSize != "" {
			vm.RootSize = defaults.RootSize
		}
		if defaults.Ram != "" {
			vm.Ram = defaults.Ram
		}
		if defaults.Cpus > 0 {
			vm.Cpus = defaults.Cpus
		}
		if defaults.KernelArgs != "" {
			vm.KernelArgs = defaults.KernelArgs
		}
		if defaults.Rootfs != "" {
			vm.Rootfs = defaults.Rootfs
		}
		if defaults.Transport != "" {
			vm.Transport = defaults.Transport
		}
		if defaults.SshPort > 0 {
			vm.SshPort = defaults.SshPort
		}
		if defaults.Firmware != "" {
			vm.Firmware = defaults.Firmware
		}
		if defaults.Network != "" {
			vm.Network = defaults.Network
		}
	}
	// Apply image-level overrides
	if img != nil {
		if img.DiskSize != "" {
			vm.DiskSize = img.DiskSize
		}
		if img.RootSize != "" {
			vm.RootSize = img.RootSize
		}
		if img.Ram != "" {
			vm.Ram = img.Ram
		}
		if img.Cpus > 0 {
			vm.Cpus = img.Cpus
		}
		if img.KernelArgs != "" {
			vm.KernelArgs = img.KernelArgs
		}
		if img.Rootfs != "" {
			vm.Rootfs = img.Rootfs
		}
		if img.Transport != "" {
			vm.Transport = img.Transport
		}
		if img.SshPort > 0 {
			vm.SshPort = img.SshPort
		}
		if img.Firmware != "" {
			vm.Firmware = img.Firmware
		}
		if img.Network != "" {
			vm.Network = img.Network
		}
	}
	return vm
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
