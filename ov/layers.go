package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortSpec represents a port declaration with an optional protocol annotation.
// Supports both plain integer (defaults to "http") and "tcp:5900" string forms.
type PortSpec struct {
	Port     int
	Protocol string // backend scheme: "http" (default), "https", "https+insecure", "tcp", "tls-terminated-tcp", "ssh", "rdp", "smb"
}

// UnmarshalYAML handles both integer and string forms for port specs.
func (p *PortSpec) UnmarshalYAML(value *yaml.Node) error {
	// Try integer first
	if value.Kind == yaml.ScalarNode {
		// Try as int
		if n, err := strconv.Atoi(value.Value); err == nil {
			p.Port = n
			p.Protocol = "http"
			return nil
		}
		// Try as "proto:port" string
		s := value.Value
		if idx := strings.Index(s, ":"); idx != -1 {
			proto := s[:idx]
			portStr := s[idx+1:]
			n, err := strconv.Atoi(portStr)
			if err != nil {
				return fmt.Errorf("invalid port spec %q: port must be a number", s)
			}
			p.Port = n
			p.Protocol = proto
			return nil
		}
		// Plain number as string
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid port spec %q: must be a number or proto:number", s)
		}
		p.Port = n
		p.Protocol = "http"
		return nil
	}
	return fmt.Errorf("invalid port spec: expected scalar, got %v", value.Kind)
}

// VolumeYAML represents a volume declaration in layer.yml
type VolumeYAML struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// AliasYAML represents a command alias declaration in layer.yml
type AliasYAML struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// ExtractYAML represents a file extraction from a Docker image
type ExtractYAML struct {
	Source string `yaml:"source"` // Source image (e.g., "ghcr.io/immich-app/immich-server:v1.106.4")
	Path   string `yaml:"path"`   // Path to extract (e.g., "/usr/src/app")
	Dest   string `yaml:"dest"`   // Destination in target image (e.g., "/opt/immich/server")
}

// DataYAML represents a data mapping from the layer directory to a volume staging area.
// Data files are COPYed into /data/<volume>/[dest/] at build time and provisioned
// into bind-backed volumes by ov config / ov update at deploy time.
type DataYAML struct {
	Src    string `yaml:"src"`            // source dir relative to layer dir (e.g., "data/notebooks")
	Volume string `yaml:"volume"`         // target volume name (must match a volumes[].name in the image chain)
	Dest   string `yaml:"dest,omitempty"` // optional subdirectory within the volume path
}

// LayerArtifact declares a file the layer publishes back to the operator
// after its setup completes. Retrieval happens at `ov deploy add` finalization
// via the target's back-channel (scp for SSH/VM, cp for host, podman cp for
// container). The retrieved file is written to `RetrieveTo` with shell-style
// ${ENV} expansion on the path. Optional `Rewrite` rules perform a literal
// find/replace on the file contents before writing — used for rewriting
// loopback addresses in kubeconfig files, etc.
type LayerArtifact struct {
	// Name is a human-readable identifier (e.g. "kubeconfig"). Used in
	// log messages and as a dedupe key when multiple layers in the same
	// deploy declare overlapping artifacts.
	Name string `yaml:"name" json:"name"`

	// Path is the on-target absolute path to retrieve. Required.
	Path string `yaml:"path" json:"path"`

	// RetrieveTo is the operator-side destination. Supports ${ENV} expansion
	// (including ${deploy_name}, ${vm_name}, and any env vars already in
	// scope). Parent directories are created with mode 0755. Required.
	RetrieveTo string `yaml:"retrieve_to" json:"retrieve_to"`

	// Mode is the file mode to apply on the retrieved destination (octal
	// string like "0600"). Empty defaults to 0644.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// Rewrite applies literal find/replace pairs to the file contents
	// before writing. Evaluated in order. Typical use: rewrite
	// "server: https://127.0.0.1:6443" in a kubeconfig to the VM's
	// reachable hostname so the kubeconfig is usable from the operator.
	Rewrite []LayerArtifactRewrite `yaml:"rewrite,omitempty" json:"rewrite,omitempty"`

	// Optional is ignored when true and the file doesn't exist on the
	// target. Default: required (missing file fails the deploy).
	Optional bool `yaml:"optional,omitempty" json:"optional,omitempty"`

	// WaitSeconds is the deadline (in seconds) for the file to appear on
	// the target before retrieval. Useful for layers whose service unit
	// transitions to "active" BEFORE the artifact file is written —
	// canonical case: k3s.service reaches active when the binary execs,
	// but /etc/rancher/k3s/k3s.yaml lands ~3-15s later when the API
	// server starts. Polls exec.GetFile every 1s until success or
	// deadline. 0 (default) disables the wait — file must already exist
	// at retrieval time. Recommended: 60-120s for k3s-class artifacts.
	//
	// This is a readiness probe (file existence is the synchronization
	// primitive), not a sleep workaround — R4-compliant.
	WaitSeconds int `yaml:"wait_seconds,omitempty" json:"wait_seconds,omitempty"`
}

// LayerArtifactRewrite is a single find/replace pair.
type LayerArtifactRewrite struct {
	Find    string `yaml:"find" json:"find"`
	Replace string `yaml:"replace" json:"replace"`
}

// EnvDependency declares an env var or MCP server that a layer needs or can use.
// Reused for env_requires, env_accepts, mcp_requires, mcp_accepts,
// secret_accepts, and secret_requires.
//
// The Key field is only meaningful for secret_accepts/secret_requires entries:
// it optionally overrides the credential store lookup key. Default is
// ("ov/secret", Name). When set, the format is "<service>/<key>" and must
// start with "ov/" (enforced by validate.go to prevent exfiltration of
// unrelated user credentials). See plan §2.7.
type EnvDependency struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Default     string `yaml:"default,omitempty" json:"default,omitempty"`
	Key         string `yaml:"key,omitempty" json:"key,omitempty"` // credential store path override (secret_* only), format "<service>/<key>", must start with "ov/"
}

// MCPServerYAML represents an MCP server declaration in layer.yml.
type MCPServerYAML struct {
	Name      string `yaml:"name" json:"name"`
	URL       string `yaml:"url" json:"url"`
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"` // "http" (default), "sse"
}

// ShellConfig represents a layer's shell-init declarations (the `shell:`
// field in layer.yml). Mirrors the per-distro / per-package pattern: an
// intrinsic body (init, path_append, path, priority) plus per-shell
// sub-blocks (bash, zsh, fish, sh) that override the intrinsic for that
// shell. Selection rule applied at install time: per-shell ByShell entry
// wins when present; otherwise the intrinsic body is used with
// ${SHELL_NAME} substituted.
type ShellConfig struct {
	Init       string                `yaml:"init,omitempty" json:"init,omitempty"`
	PathAppend []string              `yaml:"path_append,omitempty" json:"path_append,omitempty"`
	Path       string                `yaml:"path,omitempty" json:"path,omitempty"`
	Priority   int                   `yaml:"priority,omitempty" json:"priority,omitempty"`
	ByShell    map[string]*ShellSpec `yaml:"-" json:"by_shell,omitempty"` // populated by UnmarshalYAML for bash/zsh/fish/sh keys
}

// ShellSpec is one per-shell init declaration nested inside a ShellConfig
// (the body of shell.bash:, shell.zsh:, shell.fish:, shell.sh:).
type ShellSpec struct {
	Init       string   `yaml:"init,omitempty" json:"init,omitempty"`
	PathAppend []string `yaml:"path_append,omitempty" json:"path_append,omitempty"`
	Path       string   `yaml:"path,omitempty" json:"path,omitempty"`
}

// ShellAllowlist enumerates valid per-shell sub-block keys inside `shell:`.
// Adding a new shell here is a renderer change (new managed-block / drop-in
// destination); keep in sync with deploy_target_local.go shell-detection
// probe and OCITarget.emitShellSnippet destination table.
var ShellAllowlist = map[string]bool{"bash": true, "zsh": true, "fish": true, "sh": true}

// shellConfigKnownFields enumerates the intrinsic field keys inside `shell:`.
var shellConfigKnownFields = map[string]bool{
	"init": true, "path_append": true, "path": true, "priority": true,
}

// UnmarshalYAML two-pass parses the `shell:` body: standard decode for
// intrinsic fields (init/path_append/path/priority), then a manual walk
// over remaining keys to pick up per-shell sub-blocks (bash/zsh/fish/sh)
// into ByShell. Mirrors the LayerYAML.UnmarshalYAML alias-trick pattern.
// Unknown keys (not intrinsic, not in ShellAllowlist) raise a hard error
// so authors don't silently typo a shell name.
func (sc *ShellConfig) UnmarshalYAML(value *yaml.Node) error {
	type shellConfigAlias ShellConfig
	var alias shellConfigAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*sc = ShellConfig(alias)

	if value.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		if shellConfigKnownFields[key] {
			continue
		}
		if !ShellAllowlist[key] {
			return fmt.Errorf("shell: unknown key %q (expected one of init/path_append/path/priority or bash/zsh/fish/sh)", key)
		}
		var spec ShellSpec
		if err := value.Content[i+1].Decode(&spec); err != nil {
			return fmt.Errorf("shell.%s: %w", key, err)
		}
		if sc.ByShell == nil {
			sc.ByShell = make(map[string]*ShellSpec)
		}
		sc.ByShell[key] = &spec
	}
	return nil
}

// sortedEnvDeps returns a deterministic slice from a name-keyed map, sorted by Name.
func sortedEnvDeps(m map[string]EnvDependency) []EnvDependency {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]EnvDependency, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// LayerYAML represents the parsed layer.yml file.
// Unknown top-level keys are captured as tag-based package sections
// (e.g., "fedora:", "archlinux:", "fedora:43:", "debian,ubuntu:").
type LayerYAML struct {
	Version     string       `yaml:"version,omitempty"`     // CalVer version (YYYY.DDD.HHMM) of this layer definition
	Description *Description `yaml:"description,omitempty"` // Gherkin-shaped self-description; replaces retired info:/status:
	Layer      []string          `yaml:"layer,omitempty"`
	Require    []string          `yaml:"require,omitempty"`
	Engine     string            `yaml:"engine,omitempty"` // required run engine: "docker" or "" (any)
	Env        map[string]string `yaml:"env,omitempty"`
	PathAppend []string          `yaml:"path_append,omitempty"`
	Port       []PortSpec        `yaml:"port,omitempty"`
	Route      *RouteYAML        `yaml:"route,omitempty"`
	// Service is the unified service schema: a list of ServiceEntry.
	// Each entry either reuses a packaged unit (use_packaged:) or
	// defines a custom service (exec: ...).
	Service       []ServiceEntry    `yaml:"service,omitempty"`
	Volume        []VolumeYAML      `yaml:"volume,omitempty"`
	Alias         []AliasYAML       `yaml:"alias,omitempty"`
	Extract       []ExtractYAML     `yaml:"extract,omitempty"`
	Security      *SecurityConfig   `yaml:"security,omitempty"`
	Libvirt       []string          `yaml:"libvirt,omitempty"`
	Hooks         *HooksConfig      `yaml:"hooks,omitempty"`
	PortRelay     []int             `yaml:"port_relay,omitempty"`
	SecretYAML    []SecretYAML      `yaml:"secret,omitempty"`
	Data          []DataYAML        `yaml:"data,omitempty"`
	EnvProvides   map[string]string `yaml:"env_provides,omitempty"`   // env vars provided to OTHER containers when this service is deployed
	EnvRequire    []EnvDependency   `yaml:"env_require,omitempty"`    // env vars this layer MUST have from the environment
	EnvAccept     []EnvDependency   `yaml:"env_accept,omitempty"`     // env vars this layer CAN optionally use
	SecretAccept  []EnvDependency   `yaml:"secret_accept,omitempty"`  // credential-store-backed env vars this layer CAN optionally use
	SecretRequire []EnvDependency   `yaml:"secret_require,omitempty"` // credential-store-backed env vars this layer MUST have
	MCPProvide    []MCPServerYAML   `yaml:"mcp_provide,omitempty"`    // MCP servers provided to OTHER containers when this service is deployed
	MCPRequire    []EnvDependency   `yaml:"mcp_require,omitempty"`    // MCP servers this layer MUST have from the environment
	MCPAccept     []EnvDependency   `yaml:"mcp_accept,omitempty"`     // MCP servers this layer CAN optionally use

	// Calamares-aligned package surface (2026-05 cutover). The unified
	// flat top-level `packages:` is the Calamares group / module package
	// list shape. Per-distro overrides + format-specific extras (copr,
	// repos, options, exclude, modules, archlinux AUR sub-block) live
	// under `distros:` keyed by distro name (or distro-version e.g.
	// `debian-13`, `ubuntu-24.04`).
	Package []PackageItem              `yaml:"package,omitempty"`
	Distro  map[string]*DistroPackages `yaml:"distro,omitempty"`

	// Replaces root.yml / user.yml — see Task type and docs/plan.
	Vars map[string]string `yaml:"vars,omitempty"` // layer-local variables for ${VAR} substitution in tasks
	Task []Task            `yaml:"task,omitempty"` // ordered install operations

	// Shell-init declarations: an intrinsic body (init/path_append/path/
	// priority) plus per-shell sub-blocks (bash/zsh/fish/sh). Travels in
	// the org.overthinkos.shell OCI label (layer section) and is applied
	// at `ov image build` time (snippets land in /etc/profile.d/,
	// /etc/fish/conf.d/) and at `ov deploy add` time on target:local /
	// target:vm (managed-block in user rc files; per-layer drop-in for
	// fish). See ShellConfig type and /ov-build:layer "Shell Init Surface".
	Shell *ShellConfig `yaml:"shell,omitempty"`

	// Tests are declarative checks contributed by this layer. They travel
	// in the org.overthinkos.tests OCI label (layer section) and run under
	// `ov eval image` (build-time) and `ov eval live` (deploy-time).
	// See testspec.go for the Check type.
	Eval []Check `yaml:"eval,omitempty"`

	// Artifacts are files a layer publishes back to the operator after its
	// setup runs successfully. Each artifact is retrieved from the deploy
	// venue (scp for VM/SSH targets, plain copy for host target, podman cp
	// for container target) and written to `retrieve_to:`, optionally
	// rewritten via `rewrite:` rules. Used by e.g. the k3s-server layer to
	// publish `/etc/rancher/k3s/k3s.yaml` back to `~/.cache/ov/clusters/
	// <deploy>/kubeconfig.yaml` so the operator can `kubectl` the new
	// cluster without manual scp. Generic — not k3s-specific.
	Artifact []LayerArtifact `yaml:"artifact,omitempty"`

	// Capabilities are layer-contributed image-level facts (preserve_user,
	// needs_root_after_init, init_system_hint, data_only, oci_labels).
	// Aggregated at image resolve time via AggregateLayerCapabilities.
	// Replaces the magic image-level booleans (image.bootc, image.data_image)
	// with a declarative layer-derived surface.
	Capabilities         *LayerCapabilities `yaml:"capabilities,omitempty"`
	RequiresCapability []string             `yaml:"requires_capability,omitempty"`

	// Populated by custom UnmarshalYAML:
	FormatSections map[string]*PackageSection `yaml:"-"` // format sections (rpm, deb, pac, aur, etc.)
	TagSections    map[string]*TagPkgConfig   `yaml:"-"` // distro/version tag sections
}

// layerYAMLKnownFields lists non-format top-level keys in layer.yml.
// Unknown keys are routed to FormatSections (if matching a build.yml distro format)
// or TagSections (otherwise).
//
// `directory`, `info` deleted in the 2026-05 Calamares cutover (0 YAML files
// used either; `description:` carries the metadata `info:` previously held).
// `depends` renamed to `requires`. Calamares-shaped `packages` + `distros`
// added as the unified package surface; per-format `rpm:`/`deb:`/`pac:`/
// `aur:` and per-distro tag sections (debian:13: etc.) collapse into them
// via `ov migrate calamares`.
var layerYAMLKnownFields = map[string]bool{
	"description": true, "version": true, "status": true,
	"name": true, "from": true,
	"layer": true, "require": true, "engine": true, "env": true,
	"path_append": true, "port": true, "route": true, "service": true,
	"volume": true, "alias": true, "extract": true, "security": true,
	"libvirt": true, "hooks": true,
	"port_relay": true, "secret": true, "data": true,
	"env_provides": true, "env_require": true, "env_accept": true,
	"secret_accept": true, "secret_require": true,
	"mcp_provide": true, "mcp_require": true, "mcp_accept": true,
	"vars": true, "task": true, "tests": true, "eval": true,
	"artifact":     true,
	"capabilities": true, "requires_capability": true,
	"package":      true, "distro": true,
	"shell":        true,
}

// layerYAMLFormatNames caches known format names from build.yml for YAML parsing.
// Must be populated by calling SetFormatNames before scanning layers.
var layerYAMLFormatNames map[string]bool

// SetFormatNames registers format names from a DistroConfig for layer YAML parsing.
// Collects all format names across all distros (including inherited ones).
// Must be called before ScanAllLayerWithConfig to ensure format sections
// (e.g., rpm:, deb:) are correctly distinguished from tag sections.
func SetFormatNames(dc *DistroConfig) {
	layerYAMLFormatNames = make(map[string]bool)
	if dc == nil {
		return
	}
	for _, name := range dc.AllFormatNames() {
		layerYAMLFormatNames[name] = true
	}
}

// derivePackageSectionsFromCalamares populates layer.formatSections and
// layer.tagSections from the post-2026-05 Calamares-aligned top-level
// `packages:` + `distros:` map. Transitional bridge so the existing
// install-template renderer (build.go / generate.go) keeps working without
// per-renderer changes during the cutover. Same root data flows through;
// only the YAML surface changed.
//
// Mapping rules:
//   - Top-level `packages:` flows into every format section that any
//     `distros:` entry triggers (the intersection layer).
//   - `distros.fedora.*`     → FormatSections["rpm"]   + raw extras
//   - `distros.debian.*`     → FormatSections["deb"]   + raw extras
//   - `distros.ubuntu.*`     → FormatSections["deb"]   (unioned with debian)
//   - `distros.archlinux.*`  → FormatSections["pac"]   + raw extras
//   - `distros.archlinux.aur.*` → FormatSections["aur"]
//   - `distros.<name>-<ver>.*` → TagSections["<name>:<ver>"] (dash → colon)
func derivePackageSectionsFromCalamares(layer *Layer, ly *LayerYAML) {
	topPkgs := PackageNames(ly.Package)

	distroToFormat := map[string]string{
		"fedora":    "rpm",
		"debian":    "deb",
		"ubuntu":    "deb",
		"archlinux": "pac",
	}

	ensureFormat := func(fmtName string) *PackageSection {
		if layer.formatSections == nil {
			layer.formatSections = map[string]*PackageSection{}
		}
		ps := layer.formatSections[fmtName]
		if ps == nil {
			ps = &PackageSection{FormatName: fmtName, Raw: map[string]interface{}{}}
			layer.formatSections[fmtName] = ps
		}
		if ps.Raw == nil {
			ps.Raw = map[string]interface{}{}
		}
		return ps
	}

	addPackages := func(ps *PackageSection, pkgs []string) {
		seen := map[string]bool{}
		for _, p := range ps.Packages {
			seen[p] = true
		}
		for _, p := range pkgs {
			if !seen[p] {
				ps.Packages = append(ps.Packages, p)
				seen[p] = true
			}
		}
		// Reflect into Raw so install templates that read .Raw.packages
		// (rather than .Packages directly) see the same list.
		ps.Raw["package"] = ps.Packages
	}
	mergeRaw := func(ps *PackageSection, key string, val interface{}) {
		if val == nil {
			return
		}
		// Skip overwriting populated Raw entries; first writer wins.
		if _, exists := ps.Raw[key]; !exists {
			ps.Raw[key] = val
		}
	}

	// Walk distros. Versioned keys (debian-13 etc.) feed TagSections.
	for distroKey, dp := range ly.Distro {
		if dp == nil {
			continue
		}
		// Versioned form (e.g. debian-13, ubuntu-24.04) → tag section.
		if i := strings.IndexByte(distroKey, '-'); i > 0 {
			bare := distroKey[:i]
			version := distroKey[i+1:]
			if knownDistroNames[bare] {
				tagKey := bare + ":" + version
				if layer.tagSections == nil {
					layer.tagSections = map[string]*TagPkgConfig{}
				}
				cfg := layer.tagSections[tagKey]
				if cfg == nil {
					cfg = &TagPkgConfig{Raw: map[string]interface{}{}}
					layer.tagSections[tagKey] = cfg
				}
				cfg.Package = append(cfg.Package, PackageNames(dp.Package)...)
				if cfg.Raw == nil {
					cfg.Raw = map[string]interface{}{}
				}
				if cfg.Raw["package"] == nil {
					cfg.Raw["package"] = cfg.Package
				}
				if dp.Repo != nil {
					cfg.Raw["repo"] = dp.Repo
				}
				if dp.Copr != nil {
					cfg.Raw["copr"] = dp.Copr
				}
				if dp.Options != nil {
					cfg.Raw["options"] = dp.Options
				}
				if dp.Exclude != nil {
					cfg.Raw["exclude"] = dp.Exclude
				}
				if dp.Module != nil {
					cfg.Raw["module"] = dp.Module
				}
				continue
			}
		}

		fmtName, ok := distroToFormat[distroKey]
		if !ok {
			continue
		}
		ps := ensureFormat(fmtName)
		// Top-level packages contribute once; track via the helper's seen set.
		if len(topPkgs) > 0 {
			addPackages(ps, topPkgs)
		}
		addPackages(ps, PackageNames(dp.Package))
		if dp.Copr != nil {
			mergeRaw(ps, "copr", dp.Copr)
		}
		if dp.Repo != nil {
			mergeRaw(ps, "repo", dp.Repo)
		}
		if dp.Exclude != nil {
			mergeRaw(ps, "exclude", dp.Exclude)
		}
		if dp.Options != nil {
			mergeRaw(ps, "options", dp.Options)
		}
		if dp.Module != nil {
			mergeRaw(ps, "module", dp.Module)
		}
		// AUR sub-block under archlinux.
		if distroKey == "archlinux" && dp.AUR != nil {
			aurPS := ensureFormat("aur")
			addPackages(aurPS, PackageNames(dp.AUR.Package))
			if dp.AUR.Options != nil {
				mergeRaw(aurPS, "options", dp.AUR.Options)
			}
			if dp.AUR.Replaces != nil {
				mergeRaw(aurPS, "replaces", dp.AUR.Replaces)
			}
		}
	}

	// Top-level packages without any distros entries: feed all known formats
	// so the install templates pick them up regardless of the resolved image's
	// distro. Only fires when `distros:` is empty (single-format author intent).
	if len(ly.Distro) == 0 && len(topPkgs) > 0 {
		for _, fmtName := range []string{"rpm", "deb", "pac"} {
			ps := ensureFormat(fmtName)
			addPackages(ps, topPkgs)
		}
	}
}

// PackageSection represents a generic format-specific package config in layer.yml.
// All fields from the YAML section are available in Raw for template rendering.
type PackageSection struct {
	FormatName string                 // "rpm", "deb", "pac", "aur", etc.
	Packages   []string               // extracted from Raw["package"] for quick access
	Raw        map[string]interface{} // all fields from YAML, passed to templates
}

// TagPkgConfig is a distro/version-specific package config (e.g. `debian:13:`,
// `ubuntu:24.04:`, `fedora:43:`). Packages are installed using the primary
// format's tool (dnf, apt, pacman). Raw captures the full YAML so that tag
// sections can carry `repos:`, `options:`, `keys:` — the same schema as the
// generic format section — for version-specific upstream repo configurations.
type TagPkgConfig struct {
	Package []string               `yaml:"package,omitempty"`
	Raw     map[string]interface{} `yaml:"-"`
}

// Task is a single install operation in layer.yml `tasks:` list.
// Exactly one of the verb-discriminator fields (Cmd, Mkdir, Copy, Write,
// Link, Download, Setcap, Build) must be non-empty — enforced by Kind().
// The remaining fields are shared modifiers; validator enforces which
// modifiers are legal per verb.
//
// See plan: /home/atrawog/.claude/plans/can-you-ultrathink-and-partitioned-codd.md
type Task struct {
	// Verb discriminators — exactly one non-empty
	Cmd      string `yaml:"cmd,omitempty"`      // shell command (escape hatch)
	Mkdir    string `yaml:"mkdir,omitempty"`    // directory path to create
	Copy     string `yaml:"copy,omitempty"`     // layer-dir file to copy (src)
	Write    string `yaml:"write,omitempty"`    // destination path for inline content
	Link     string `yaml:"link,omitempty"`     // link path (where the symlink goes)
	Download string `yaml:"download,omitempty"` // URL to fetch
	Setcap   string `yaml:"setcap,omitempty"`   // file path for capability operation
	Build    string `yaml:"build,omitempty"`    // builder selector, currently only "all"

	// Shared modifiers — validity depends on verb
	User            string   `yaml:"user,omitempty"`             // user context: root / ${USER} / name / uid:gid
	Mode            string   `yaml:"mode,omitempty"`             // octal permissions
	To              string   `yaml:"to,omitempty"`               // destination (copy, download)
	Target          string   `yaml:"target,omitempty"`           // symlink target
	Content         string   `yaml:"content,omitempty"`          // inline content for write
	Extract         string   `yaml:"extract,omitempty"`          // archive format for download
	Include         []string `yaml:"include,omitempty"`          // path filter for download
	StripComponents int      `yaml:"strip_components,omitempty"` // strip N leading path components from tar entries
	// Uninstall lists file paths that `ov deploy del` should remove when
	// reversing this task. Needed for download tasks that extract into a
	// shared directory (e.g. /usr/local/bin): the default reverse uses
	// task.To which would be the whole dir; authors declare the actual
	// files here so teardown is clean without wiping unrelated binaries.
	Uninstall []string          `yaml:"uninstall,omitempty"`
	Env       map[string]string `yaml:"env,omitempty"`     // env vars for download install scripts
	Caps      string            `yaml:"caps,omitempty"`    // capability spec for setcap (empty = strip)
	Comment   string            `yaml:"comment,omitempty"` // optional Containerfile comment
}

// TaskVerbs is the set of valid discriminator keys on a Task.
// Order is stable (used for deterministic error messages).
var TaskVerbs = []string{"cmd", "mkdir", "copy", "write", "link", "download", "setcap", "build"}

// Kind returns the task's verb ("cmd", "mkdir", …) and an error if zero or
// multiple verbs are set. Callers in the generator assume Kind returned nil
// before branching on the returned string.
func (t *Task) Kind() (string, error) {
	verbs := t.presentVerbs()
	if len(verbs) == 0 {
		return "", fmt.Errorf("task has no action (expected exactly one of: %s)", strings.Join(TaskVerbs, ", "))
	}
	if len(verbs) > 1 {
		return "", fmt.Errorf("task has conflicting actions: %s (expected exactly one)", strings.Join(verbs, ", "))
	}
	return verbs[0], nil
}

// presentVerbs returns the discriminator field names that are non-empty.
// Deterministic order (matches TaskVerbs) for stable error messages.
func (t *Task) presentVerbs() []string {
	out := make([]string, 0, 1)
	if t.Cmd != "" {
		out = append(out, "cmd")
	}
	if t.Mkdir != "" {
		out = append(out, "mkdir")
	}
	if t.Copy != "" {
		out = append(out, "copy")
	}
	if t.Write != "" {
		out = append(out, "write")
	}
	if t.Link != "" {
		out = append(out, "link")
	}
	if t.Download != "" {
		out = append(out, "download")
	}
	if t.Setcap != "" {
		out = append(out, "setcap")
	}
	if t.Build != "" {
		out = append(out, "build")
	}
	return out
}

func (ly *LayerYAML) UnmarshalYAML(value *yaml.Node) error {
	// Use type alias to avoid infinite recursion
	type layerYAMLAlias LayerYAML
	var alias layerYAMLAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*ly = LayerYAML(alias)

	// Capture unknown keys as format sections or tag sections.
	// Keys matching build.yml distro format names → FormatSections (parsed as raw maps).
	// All other unknown keys → TagSections (parsed as {packages: [...]}).
	if value.Kind == yaml.MappingNode {
		ly.FormatSections = make(map[string]*PackageSection)
		ly.TagSections = make(map[string]*TagPkgConfig)
		for i := 0; i < len(value.Content)-1; i += 2 {
			key := value.Content[i].Value
			if layerYAMLKnownFields[key] {
				continue // handled by standard YAML decoder
			}

			if layerYAMLFormatNames[key] {
				// Format section: parse as raw map for template rendering
				var raw map[string]interface{}
				if err := value.Content[i+1].Decode(&raw); err != nil {
					continue
				}
				section := &PackageSection{
					FormatName: key,
					Raw:        raw,
				}
				if pkgs, ok := raw["package"]; ok {
					section.Packages = toStringSlice(pkgs)
				}
				if len(section.Packages) > 0 {
					ly.FormatSections[key] = section
				}
			} else {
				// Tag section: parse BOTH the typed struct (for Packages access)
				// AND the raw map (for repos/options/keys passthrough to the
				// install template). Same dual-decode pattern as FormatSections.
				var cfg TagPkgConfig
				if err := value.Content[i+1].Decode(&cfg); err != nil {
					continue
				}
				var raw map[string]interface{}
				if err := value.Content[i+1].Decode(&raw); err != nil {
					continue
				}
				cfg.Raw = raw
				if len(cfg.Package) == 0 {
					continue
				}
				// Expand comma-separated keys (e.g., "debian,ubuntu")
				parts := strings.Split(key, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part != "" {
						ly.TagSections[part] = &cfg
					}
				}
			}
		}
	}

	return nil
}

// RouteYAML represents a route declaration in layer.yml
type RouteYAML struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Format-specific structs (RpmConfig, DebConfig, PacConfig, AurConfig) removed.
// All format sections are now parsed dynamically as PackageSection via build.yml distro format names.
// See PackageSection type and LayerYAML.UnmarshalYAML for the generic parsing.

// Layer represents a layer directory and its contents
type Layer struct {
	Name              string
	Path              string       // directory containing layer.yml
	SourceDir         string       // anchor for relative file lookups (tasks.copy, data.src, install files); defaults to Path, overridden by layer.yml `directory:`
	Version           string       // CalVer version from layer.yml
	Description       *Description // Gherkin-shaped self-description (Feature/Narrative/Tag/Scenario)
	Status            string       // derived from Description.Tag — working/testing/broken (empty = testing)
	Info              string       // derived from Description.Feature+Narrative
	HasPixiToml       bool
	HasPyprojectToml  bool
	HasEnvironmentYml bool
	HasPackageJson    bool
	HasCargoToml      bool
	HasSrcDir         bool
	HasEnv            bool
	HasPorts          bool
	HasRoute          bool
	HasVolumes        bool
	HasAliases        bool
	HasPixiLock       bool
	HasExtract        bool
	HasData           bool
	HasEnvProvides    bool
	HasEnvRequires    bool
	HasEnvAccepts     bool
	HasSecretAccepts  bool
	HasSecretRequires bool
	HasMCPProvides    bool
	HasMCPRequires    bool
	HasMCPAccepts     bool
	HasLibvirt        bool
	HasTasks          bool // layer.yml has a non-empty tasks: list

	// Init system detection (populated by PopulateLayerInitSystem)
	InitSystems    map[string]bool // set of init system names this layer triggers
	PortRelayPorts []int           // port_relay: field (init-agnostic)

	Require           []string // bare refs (version stripped) for resolution
	RawRequire        []string // original refs with :version for remote ref collection
	IncludedLayer     []string // bare refs from layer: field (version stripped)
	RawIncludedLayer  []string // original layer: refs with :version

	// Remote layer metadata
	Remote        bool   // true if from a remote repo
	RepoPath      string // e.g. "github.com/overthinkos/overthink" (empty for local)
	SubPathPrefix string // e.g. "layers/" — parent directory within the repo for sibling resolution

	// Pre-populated from layer.yml
	formatSections map[string]*PackageSection // generic format sections (rpm, deb, pac, aur, etc.)
	tagSections    map[string]*TagPkgConfig   // distro/version-specific package sections
	ports          []string
	portSpecs      []PortSpec // full PortSpec data with protocol info
	envConfig      *EnvConfig
	route          *RouteConfig
	serviceFiles   []string       // paths to *.service files in layer dir (systemd user-level, file_copy model)
	service        []ServiceEntry // unified service: list (the only service schema)
	volumes        []VolumeYAML
	aliases        []AliasYAML
	extract        []ExtractYAML
	data           []DataYAML
	security       *SecurityConfig
	libvirt        []string
	hooks          *HooksConfig
	secrets        []SecretYAML
	envProvides    map[string]string // env vars provided to other containers (service discovery)
	envRequires    []EnvDependency   // env vars this layer must have
	envAccepts     []EnvDependency   // env vars this layer can optionally use
	secretAccepts  []EnvDependency   // credential-store-backed env vars this layer can optionally use
	secretRequires []EnvDependency   // credential-store-backed env vars this layer must have
	mcpProvides    []MCPServerYAML   // MCP servers provided to other containers
	mcpRequires    []EnvDependency   // MCP servers this layer must have
	mcpAccepts     []EnvDependency   // MCP servers this layer can optionally use
	engine         string            // required run engine from layer.yml ("docker", "podman", or "")
	vars           map[string]string // layer-local variables (from layer.yml vars:)
	tasks          []Task            // ordered install operations (from layer.yml tasks:)
	tests          []Check           // declarative checks (from layer.yml tests:)
	artifacts      []LayerArtifact   // files to retrieve after setup (from layer.yml artifacts:)
	shell          *ShellConfig      // shell-init declarations (from layer.yml shell:)
	description    *Description      // Gherkin-shaped self-description (from layer.yml description:)

	// Layer-contributed image-level facts (capabilities: block in layer.yml)
	// and cross-layer requirement declarations (requires_capabilities:).
	capabilities         *LayerCapabilities
	requiresCapabilities []string
}

// ScanLayer returns all layers for the project at dir. Post-unified-cutover
// this loads overthink.yml via LoadUnified, applies discover:, and projects
// the layers map. Legacy `layers/` directory scan remains as a fallback when
// overthink.yml is absent (e.g., transitional test fixtures).
func ScanLayer(dir string) (map[string]*Layer, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading overthink.yml: %w", err)
	}
	if present {
		if err := uf.ApplyDiscover(dir); err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		return uf.ProjectLayers(dir)
	}
	return legacyScanLayersDir(dir)
}

// legacyScanLayersDir is the pre-unified filesystem walk. Kept for test
// fixtures (and the migration tool) that don't yet have an overthink.yml.
func legacyScanLayersDir(dir string) (map[string]*Layer, error) {
	layersDir := filepath.Join(dir, "layers")
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Layer), nil
		}
		return nil, fmt.Errorf("reading layers directory: %w", err)
	}
	layers := make(map[string]*Layer)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		layer, err := scanLayer(filepath.Join(layersDir, name), name)
		if err != nil {
			return nil, fmt.Errorf("scanning layer %s: %w", name, err)
		}
		layers[name] = layer
	}
	return layers, nil
}

// parseLayerYAML reads and unmarshals a layer.yml file. Strict schema:
//   - Empty / comment-only file → zero-value LayerYAML.
//   - Single top-level `layer:` key → decode its body as LayerYAML (canonical form).
//   - `layer:` + other top-level keys → error (ambiguous shape).
//   - Multi-document stream → error (layer.yml is not a bundle file).
//   - Flat form (no `layer:` wrapper) → error with migration hint.
func parseLayerYAML(path string) (*LayerYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Empty / comment-only guard.
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &LayerYAML{}, nil
	}

	// Field-singular cutover hard-rejection: any legacy plural top-level
	// key (layers:/ports:/...) fires a clear remediation hint pointing
	// at `ov migrate field-singular` rather than letting the YAML decoder
	// silently drop the unknown field.
	if err := RejectLegacyPluralKeys(path, data); err != nil {
		return nil, err
	}

	// Parse as a multi-document stream — reject if more than one non-empty doc.
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var docs []yaml.Node
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// Skip empty (null-valued) docs.
		if node.Kind == 0 || (node.Kind == yaml.DocumentNode && (len(node.Content) == 0 || (len(node.Content) == 1 && node.Content[0].Tag == "!!null"))) {
			continue
		}
		docs = append(docs, node)
	}
	if len(docs) == 0 {
		return &LayerYAML{}, nil
	}
	if len(docs) > 1 {
		return nil, fmt.Errorf("%s: layer.yml is not a multi-document stream; bundle files belong in the unified overthink.yml", path)
	}

	node := &docs[0]
	// Unwrap the DocumentNode wrapper.
	inner := node
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		inner = node.Content[0]
	}
	if inner.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level must be a mapping (got kind=%v)", path, inner.Kind)
	}

	// Collect top-level keys.
	var keys []string
	var layerIdx = -1
	for i := 0; i < len(inner.Content); i += 2 {
		k := inner.Content[i].Value
		keys = append(keys, k)
		if k == "layer" {
			layerIdx = i + 1
		}
	}

	if layerIdx >= 0 {
		// Canonical kind-keyed form — `layer:` must be the only top-level key.
		if len(keys) != 1 {
			var other []string
			for _, k := range keys {
				if k != "layer" {
					other = append(other, k)
				}
			}
			return nil, fmt.Errorf("%s: ambiguous — `layer:` wrapper present AND other top-level keys %v (pick one form)", path, other)
		}
		// 2026-05 Calamares cutover: hard-fail on legacy field shapes.
		// Every legacy form has a one-shot remediation via `ov migrate calamares`.
		body := inner.Content[layerIdx]
		if body != nil && body.Kind == yaml.MappingNode {
			if err := rejectLegacyLayerKeys(path, body); err != nil {
				return nil, err
			}
		}
		var ly LayerYAML
		if err := body.Decode(&ly); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return &ly, nil
	}

	// No `layer:` wrapper — legacy flat form. Reject with migration hint.
	return nil, fmt.Errorf("%s: legacy flat layer.yml form is no longer accepted. Run `ov migrate unified --rewrite-layers` to convert to the canonical `layer:` kind-keyed form", path)
}

// rejectLegacyLayerKeys is the 2026-05 Calamares-cutover hard-fail gate:
// every legacy field shape produces a clear error pointing at
// `ov migrate calamares`. Runs before standard YAML decoding so the user
// sees the migration hint, not a generic "field not found" error.
func rejectLegacyLayerKeys(path string, body *yaml.Node) error {
	for i := 0; i+1 < len(body.Content); i += 2 {
		key := body.Content[i].Value
		switch key {
		case "depends":
			return fmt.Errorf("%s: layer.yml uses legacy `depends:` field. Run: ov migrate calamares", path)
		case "rpm", "deb", "pac", "aur":
			return fmt.Errorf("%s: layer.yml uses legacy `%s:` block at top level. Calamares-aligned schema uses unified top-level `packages:` + per-distro `distros:` map. Run: ov migrate calamares", path, key)
		case "directory":
			return fmt.Errorf("%s: layer.yml uses legacy `directory:` field (removed in 2026-05 cutover). Run: ov migrate calamares", path)
		case "info":
			return fmt.Errorf("%s: layer.yml uses legacy `info:` field (removed; use `description:`). Run: ov migrate calamares", path)
		}
		// Distro-tag sections like `debian:13:`, `ubuntu:24.04:`,
		// `debian,ubuntu:` — only fire when the bare leading segment
		// matches a known distro name (so we don't false-positive on
		// arbitrary YAML keys with colons).
		if d, isTag := classifyDistroTag(key); isTag && len(d) > 0 {
			return fmt.Errorf("%s: layer.yml uses legacy distro tag section `%s:` at top level. Calamares-aligned schema nests distro overrides under `distros:`. Run: ov migrate calamares", path, key)
		}
	}
	return nil
}

// resolveLayerSourceDir was the resolver for the legacy `directory:` field on
// layer.yml. The field was deleted in the 2026-05 Calamares cutover; the
// helper is now a no-op kept only for any external import that still calls
// it. New code should use `path` directly.
func resolveLayerSourceDir(path, _ string) string {
	return path
}

// scanLayer scans a single layer directory
func scanLayer(path string, name string) (*Layer, error) {
	layer := &Layer{
		Name: name,
		Path: path,
	}

	// Parse layer.yml FIRST so `directory:` can redirect the anchor
	// used by install-file detection and service-file globbing below.
	var ly *LayerYAML
	yamlPath := filepath.Join(path, "layer.yml")
	if fileExists(yamlPath) {
		parsed, err := parseLayerYAML(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("parsing layer.yml: %w", err)
		}
		ly = parsed
	}

	// SourceDir always equals layer Path (the `directory:` field was deleted
	// in the 2026-05 Calamares cutover; 0 layers used it).
	_ = ly
	layer.SourceDir = path

	// Check for install files (anchored at SourceDir — honors `directory:`)
	layer.HasPixiToml = fileExists(filepath.Join(layer.SourceDir, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(layer.SourceDir, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(layer.SourceDir, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(layer.SourceDir, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(layer.SourceDir, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(layer.SourceDir, "src"))
	layer.HasPixiLock = fileExists(filepath.Join(layer.SourceDir, "pixi.lock"))

	// Scan for systemd service files (init system detection happens in PopulateLayerInitSystem)
	svcFiles, _ := filepath.Glob(filepath.Join(layer.SourceDir, "*.service"))
	if len(svcFiles) > 0 {
		layer.serviceFiles = svcFiles
	}

	if ly != nil {

		// Pre-populate version + Description (which carries Tag/Feature/
		// Narrative — the post-cutover replacements for legacy
		// status:/info:). Layer.Status/Info derive from Description.
		layer.Version = ly.Version
		layer.Description = ly.Description
		layer.Status = descriptionStatus(ly.Description)
		layer.Info = descriptionInfo(ly.Description)

		// Keep raw depends for remote ref collection
		layer.RawRequire = ly.Require
		// Strip :version from remote refs for layer resolution (map keys use bare refs)
		layer.Require = make([]string, len(ly.Require))
		for i, dep := range ly.Require {
			layer.Require[i] = BareRef(dep)
		}

		// Parse layers: field for layer composition
		layer.RawIncludedLayer = ly.Layer
		layer.IncludedLayer = make([]string, len(ly.Layer))
		for i, ref := range ly.Layer {
			layer.IncludedLayer[i] = BareRef(ref)
		}
		layer.service = ly.Service
		layer.HasEnv = len(ly.Env) > 0 || len(ly.PathAppend) > 0
		layer.HasPorts = len(ly.Port) > 0
		layer.HasRoute = ly.Route != nil

		// Package config: format sections and tag sections are populated by
		// the custom UnmarshalYAML on LayerYAML. Format sections are detected
		// by matching top-level keys against build.yml distro format names.
		layer.formatSections = ly.FormatSections
		if layer.formatSections == nil {
			layer.formatSections = make(map[string]*PackageSection)
		}
		layer.tagSections = ly.TagSections

		// 2026-05 Calamares cutover: derive format/tag sections from the new
		// top-level `packages:` + `distros:` map so the legacy renderer reads
		// the post-migration shape without changes. Transitional during the
		// cutover; FormatSections/TagSections deletion happens after the
		// renderer is ported in a follow-up pass.
		if len(ly.Package) > 0 || len(ly.Distro) > 0 {
			derivePackageSectionsFromCalamares(layer, ly)
		}

		// Pre-populate ports cache
		if layer.HasPorts {
			layer.ports = make([]string, len(ly.Port))
			layer.portSpecs = make([]PortSpec, len(ly.Port))
			for i, p := range ly.Port {
				if p.Protocol == "udp" {
					layer.ports[i] = strconv.Itoa(p.Port) + "/udp"
				} else {
					layer.ports[i] = strconv.Itoa(p.Port)
				}
				layer.portSpecs[i] = p
			}
		}

		// Pre-populate env cache
		if layer.HasEnv {
			env := ly.Env
			if env == nil {
				env = make(map[string]string)
			}
			layer.envConfig = &EnvConfig{
				Vars:       env,
				PathAppend: ly.PathAppend,
			}
		}

		// Pre-populate route cache
		if ly.Route != nil {
			layer.route = &RouteConfig{
				Host: ly.Route.Host,
				Port: strconv.Itoa(ly.Route.Port),
			}
		}

		// Pre-populate volumes
		layer.HasVolumes = len(ly.Volume) > 0
		layer.volumes = ly.Volume

		// Pre-populate aliases
		layer.HasAliases = len(ly.Alias) > 0
		layer.aliases = ly.Alias

		// Pre-populate extract
		layer.HasExtract = len(ly.Extract) > 0
		layer.extract = ly.Extract

		// Pre-populate data mappings
		layer.HasData = len(ly.Data) > 0
		layer.data = ly.Data

		// Pre-populate security
		layer.security = ly.Security

		// Pre-populate libvirt snippets
		if len(ly.Libvirt) > 0 {
			layer.HasLibvirt = true
			layer.libvirt = ly.Libvirt
		}

		// Pre-populate hooks
		layer.hooks = ly.Hooks

		// Pre-populate tests (declarative checks)
		layer.tests = ly.Eval

		// Pre-populate description (Gherkin-shaped self-description)
		layer.description = ly.Description

		// Pre-populate artifacts (files to retrieve after setup)
		layer.artifacts = ly.Artifact

		// Pre-populate layer-contributed capabilities + requirements
		layer.capabilities = ly.Capabilities
		layer.requiresCapabilities = ly.RequiresCapability

		// Pre-populate port relay
		layer.PortRelayPorts = ly.PortRelay

		// Pre-populate secrets
		layer.secrets = ly.SecretYAML

		// Pre-populate env_provides (env vars for other containers)
		if len(ly.EnvProvides) > 0 {
			layer.HasEnvProvides = true
			layer.envProvides = ly.EnvProvides
		}

		// Pre-populate env_requires and env_accepts
		if len(ly.EnvRequire) > 0 {
			layer.HasEnvRequires = true
			layer.envRequires = ly.EnvRequire
		}
		if len(ly.EnvAccept) > 0 {
			layer.HasEnvAccepts = true
			layer.envAccepts = ly.EnvAccept
		}

		// Pre-populate secret_accepts and secret_requires (credential-store-backed env vars)
		if len(ly.SecretRequire) > 0 {
			layer.HasSecretRequires = true
			layer.secretRequires = ly.SecretRequire
		}
		if len(ly.SecretAccept) > 0 {
			layer.HasSecretAccepts = true
			layer.secretAccepts = ly.SecretAccept
		}

		// Pre-populate mcp_provides (MCP servers for other containers)
		if len(ly.MCPProvide) > 0 {
			layer.HasMCPProvides = true
			layer.mcpProvides = ly.MCPProvide
		}

		// Pre-populate mcp_requires and mcp_accepts
		if len(ly.MCPRequire) > 0 {
			layer.HasMCPRequires = true
			layer.mcpRequires = ly.MCPRequire
		}
		if len(ly.MCPAccept) > 0 {
			layer.HasMCPAccepts = true
			layer.mcpAccepts = ly.MCPAccept
		}

		// Pre-populate engine requirement
		layer.engine = ly.Engine

		// Pre-populate vars + tasks (replacement for root.yml/user.yml)
		layer.vars = ly.Vars
		layer.tasks = ly.Task
		layer.HasTasks = len(ly.Task) > 0
		layer.shell = ly.Shell
	}

	return layer, nil
}

// HasInstallFiles returns true if the layer has at least one install file
func (l *Layer) HasInstallFiles() bool {
	return l.HasFormatPackages() ||
		l.HasPixiToml || l.HasPyprojectToml || l.HasEnvironmentYml ||
		l.HasPackageJson || l.HasCargoToml ||
		l.HasTasks
}

// HasContent returns true if the layer has install files or any configuration
// that contributes to the Containerfile (env, ports, volumes, etc.)
func (l *Layer) HasContent() bool {
	return l.HasInstallFiles() || l.HasEnv || l.HasPorts || l.HasRoute ||
		l.HasVolumes || l.HasAliases || l.HasExtract || l.HasData || l.HasLibvirt ||
		l.HasAnyInit() || len(l.PortRelayPorts) > 0 ||
		len(l.serviceFiles) > 0 || len(l.service) > 0
}

// PixiManifest returns the filename of the pixi manifest if it exists
func (l *Layer) PixiManifest() string {
	if l.HasPixiToml {
		return "pixi.toml"
	}
	if l.HasPyprojectToml {
		return "pyproject.toml"
	}
	if l.HasEnvironmentYml {
		return "environment.yml"
	}
	return ""
}

// FormatSection returns the generic package section for a format, or nil.
func (l *Layer) FormatSection(name string) *PackageSection {
	if l.formatSections == nil {
		return nil
	}
	return l.formatSections[name]
}

// HasFormatPackages returns true if any format section has packages.
func (l *Layer) HasFormatPackages() bool {
	for _, s := range l.formatSections {
		if len(s.Packages) > 0 {
			return true
		}
	}
	return false
}

// TagSection returns the tag-based package config for the given tag, or nil.
func (l *Layer) TagSection(tag string) *TagPkgConfig {
	if l.tagSections == nil {
		return nil
	}
	return l.tagSections[tag]
}

// EnvConfig returns the environment config (pre-populated from layer.yml)
func (l *Layer) EnvConfig() (*EnvConfig, error) {
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	return nil, nil
}

// Ports returns the ports (pre-populated from layer.yml)
func (l *Layer) Port() ([]string, error) {
	if l.ports != nil {
		return l.ports, nil
	}
	return nil, nil
}

// PortSpecs returns the port specs with protocol info (pre-populated from layer.yml)
func (l *Layer) PortSpecs() []PortSpec {
	return l.portSpecs
}

// Service returns the layer's unified service: list (ServiceEntry slice).
// This is the only service schema — legacy raw-INI and system_services: are
// retired entirely. External layers that still have the legacy forms must run
// `ov migrate unified --rewrite-layers`.
func (l *Layer) Service() []ServiceEntry {
	return l.service
}

// HasService returns true when the layer declares at least one service entry.
func (l *Layer) HasService() bool {
	return len(l.service) > 0
}

// ServiceFiles returns detected *.service file paths from the layer directory
// (consumed by the systemd init's file_copy model, e.g. *.service globs).
func (l *Layer) ServiceFiles() []string {
	return l.serviceFiles
}

// HasAnyInit returns true if this layer triggers any init system.
func (l *Layer) HasAnyInit() bool {
	return len(l.InitSystems) > 0
}

// HasInit returns true if this layer triggers the named init system.
func (l *Layer) HasInit(initName string) bool {
	return l.InitSystems[initName]
}

// PopulateLayerInitSystem sets InitSystems on all layers based on the init config.
// Must be called after scanning layers and loading init config.
func PopulateLayerInitSystem(layers map[string]*Layer, initCfg *InitConfig) {
	if initCfg == nil {
		return
	}
	for _, layer := range layers {
		layer.InitSystems = make(map[string]bool)
		for initName, def := range initCfg.Init {
			// Schema-driven detection: iterate the unified service: entries.
			// Each entry binds to init systems per per-entry routing:
			//   - IsPackaged()  → inits with ServiceSchema.SupportsPackaged
			//   - custom exec   → inits with ServiceSchema.ServiceTemplate != ""
			// The legacy `layer_fields: [service]` config just gates whether
			// this init participates in schema detection at all.
			participatesInSchema := false
			for _, field := range def.LayerFields {
				if field == "service" {
					participatesInSchema = true
					break
				}
			}
			if participatesInSchema {
				for i := range layer.service {
					entry := &layer.service[i]
					if entry.IsPackaged() {
						if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
							layer.InitSystems[initName] = true
							break
						}
					} else {
						if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
							layer.InitSystems[initName] = true
							break
						}
					}
				}
			}
			// Check layer_files (anchored at SourceDir — honors `directory:`)
			// for init systems like systemd that use the file_copy model.
			for _, pattern := range def.LayerFiles {
				matches, _ := filepath.Glob(filepath.Join(layer.SourceDir, pattern))
				if len(matches) > 0 {
					layer.InitSystems[initName] = true
				}
			}
		}
	}
}

// RouteConfig represents a route file declaration
type RouteConfig struct {
	Host string
	Port string
}

// Route returns the route config (pre-populated from layer.yml)
func (l *Layer) Route() (*RouteConfig, error) {
	if l.route != nil {
		return l.route, nil
	}
	return nil, nil
}

// RouteLayer returns layers that have a route file
func RouteLayer(layers map[string]*Layer) []*Layer {
	var routes []*Layer
	for _, layer := range layers {
		if layer.HasRoute {
			routes = append(routes, layer)
		}
	}
	return routes
}

// LayerNames returns a sorted list of layer names
func LayerNames(layers map[string]*Layer) []string {
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// Volume returns the volume declarations (pre-populated from layer.yml)
func (l *Layer) Volume() []VolumeYAML {
	return l.volumes
}

// Extract returns the extract declarations (pre-populated from layer.yml)
func (l *Layer) Extract() []ExtractYAML {
	return l.extract
}

// Data returns the data mappings (pre-populated from layer.yml)
func (l *Layer) Data() []DataYAML {
	return l.data
}

// Security returns the security config (pre-populated from layer.yml, nil if not set)
func (l *Layer) Security() *SecurityConfig {
	return l.security
}

// Libvirt returns the libvirt XML snippets (pre-populated from layer.yml)
func (l *Layer) Libvirt() []string {
	return l.libvirt
}

// Hooks returns the lifecycle hooks config (pre-populated from layer.yml, nil if not set)
func (l *Layer) Hooks() *HooksConfig {
	return l.hooks
}

// Shell returns the shell-init declarations (pre-populated from layer.yml,
// nil if not set). The returned config carries an intrinsic body (init,
// path_append, path, priority) plus per-shell sub-blocks (bash/zsh/fish/
// sh) in ByShell. Selection rule applied at install time — see
// compileShellSnippetSteps in install_build.go.
func (l *Layer) Shell() *ShellConfig {
	return l.shell
}

// Secrets returns the secret declarations (pre-populated from layer.yml)
func (l *Layer) Secret() []SecretYAML {
	return l.secrets
}

// Artifact returns the files this layer publishes back to the operator
// after its setup completes (pre-populated from layer.yml artifact:).
func (l *Layer) Artifact() []LayerArtifact {
	return l.artifacts
}

// EnvProvides returns env vars this layer provides to other containers (pre-populated from layer.yml)
func (l *Layer) EnvProvides() map[string]string {
	return l.envProvides
}

// EnvRequires returns env vars this layer must have from the environment (pre-populated from layer.yml)
func (l *Layer) EnvRequire() []EnvDependency {
	return l.envRequires
}

// EnvAccepts returns env vars this layer can optionally use (pre-populated from layer.yml)
func (l *Layer) EnvAccept() []EnvDependency {
	return l.envAccepts
}

// SecretAccepts returns credential-store-backed env vars this layer can optionally use.
// These entries flow through the credential store → podman secret → Secret=type=env quadlet
// directive pipeline, never touching plaintext deploy.yml or quadlet Environment= lines.
// Pre-populated from layer.yml.
func (l *Layer) SecretAccept() []EnvDependency {
	return l.secretAccepts
}

// SecretRequires returns credential-store-backed env vars this layer MUST have.
// Missing entries cause ov config to hard-fail with actionable remediation.
// Pre-populated from layer.yml.
func (l *Layer) SecretRequire() []EnvDependency {
	return l.secretRequires
}

// MCPProvides returns MCP servers this layer provides to other containers (pre-populated from layer.yml)
func (l *Layer) MCPProvide() []MCPServerYAML {
	return l.mcpProvides
}

// MCPRequires returns MCP servers this layer must have from the environment (pre-populated from layer.yml)
func (l *Layer) MCPRequire() []EnvDependency {
	return l.mcpRequires
}

// MCPAccepts returns MCP servers this layer can optionally use (pre-populated from layer.yml)
func (l *Layer) MCPAccept() []EnvDependency {
	return l.mcpAccepts
}

// Engine returns the required run engine (pre-populated from layer.yml, "" if not set)
func (l *Layer) Engine() string {
	return l.engine
}

// InitLayer returns layers that trigger any init system.
func InitLayer(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAnyInit() || len(layer.PortRelayPorts) > 0 {
			result = append(result, layer)
		}
	}
	return result
}

// VolumeLayer returns layers that have volume declarations
func VolumeLayer(layers map[string]*Layer) []*Layer {
	var vols []*Layer
	for _, layer := range layers {
		if layer.HasVolumes {
			vols = append(vols, layer)
		}
	}
	return vols
}

// Alias returns the alias declarations (pre-populated from layer.yml)
func (l *Layer) Alias() []AliasYAML {
	return l.aliases
}

// AliasLayer returns layers that have alias declarations
func AliasLayer(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAliases {
			result = append(result, layer)
		}
	}
	return result
}

// NeedsGit returns true if the pixi manifest contains git-based dependencies
func (l *Layer) NeedsGit() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.SourceDir, manifest))
	if err != nil {
		return false
	}
	content := string(data)
	// Check for PyPI git+ format and pixi { git = "..." } format
	return strings.Contains(content, "git+") || strings.Contains(content, "{ git =")
}

// HasPypiDeps returns true if the pixi manifest has PyPI dependencies
func (l *Layer) HasPypiDeps() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.SourceDir, manifest))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[pypi-dependencies]")
}

// ScanRemoteLayer scans specific layers from a downloaded remote repository.
// Only imports layers whose bare refs are in the wantRefs set.
// Bare refs use the full path format: "github.com/org/repo/layers/name".
func ScanRemoteLayer(repoDir string, repoPath string, wantRefs map[string]bool) (map[string]*Layer, error) {
	layers := make(map[string]*Layer)

	for bareRef := range wantRefs {
		// Extract sub-path from bare ref: "github.com/org/repo/layers/name" -> "layers/name"
		subPath := strings.TrimPrefix(bareRef, repoPath+"/")
		layerDir := filepath.Join(repoDir, subPath)

		// Derive name from last segment
		name := subPath
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			name = subPath[idx+1:]
		}

		if _, err := os.Stat(layerDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("remote layer %s not found at %s", bareRef, layerDir)
		}

		layer, err := scanLayer(layerDir, name)
		if err != nil {
			return nil, fmt.Errorf("scanning remote layer %s: %w", bareRef, err)
		}
		layer.Remote = true
		layer.RepoPath = repoPath
		// Compute sub-path prefix for sibling dep resolution (e.g. "layers/")
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			layer.SubPathPrefix = subPath[:idx+1]
		}

		layers[bareRef] = layer
	}

	return layers, nil
}

// ScanAllLayer scans local layers and all remote layers, returning a merged map.
// Local layers are keyed by short name, remote layers by fully-qualified path.
// Remote refs are collected from @-prefixed refs in layer.yml and image.yml.
func ScanAllLayer(dir string) (map[string]*Layer, error) {
	return ScanAllLayerWithConfig(dir, nil)
}

// ScanAllLayerWithConfig scans local and remote layers.
// Collects remote refs from @-prefixed layer references and auto-downloads repos.
func ScanAllLayerWithConfig(dir string, cfg *Config) (map[string]*Layer, error) {
	// 1. Scan local layers
	layers, err := ScanLayer(dir)
	if err != nil {
		return nil, err
	}

	// 2. Collect remote refs from @-prefixed layer references
	downloads, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		return nil, err
	}

	if len(downloads) == 0 {
		return layers, nil
	}

	// 3. Auto-download and scan each required (repo, version) pair
	for _, dl := range downloads {
		cachePath, err := EnsureRepoDownloaded(dl.RepoPath, dl.Version)
		if err != nil {
			return nil, fmt.Errorf("downloading %s:%s: %w", dl.RepoPath, dl.Version, err)
		}

		// Build set of wanted bare refs
		wantRefs := make(map[string]bool)
		for _, ref := range dl.Refs {
			wantRefs[ref] = true
		}

		// Scan only the specific layers referenced
		remoteLayers, err := ScanRemoteLayer(cachePath, dl.RepoPath, wantRefs)
		if err != nil {
			return nil, fmt.Errorf("scanning %s:%s: %w", dl.RepoPath, dl.Version, err)
		}

		// Merge into main map
		for ref, layer := range remoteLayers {
			if existing, ok := layers[ref]; ok && existing.Remote {
				return nil, fmt.Errorf("layer reference conflict: %q provided by both %s and %s", ref, existing.RepoPath, layer.RepoPath)
			}
			if _, ok := layers[layer.Name]; ok {
				fmt.Fprintf(os.Stderr, "Note: local layer %q shadows remote layer %q\n", layer.Name, ref)
			}
			layers[ref] = layer
		}
	}

	return layers, nil
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
