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

// VolumeYAML represents a volume declaration in the candy manifest
type VolumeYAML struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// AliasYAML represents a command alias declaration in the candy manifest
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
type CandyArtifact struct {
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
	Rewrite []CandyArtifactRewrite `yaml:"rewrite,omitempty" json:"rewrite,omitempty"`

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
	WaitSeconds int `yaml:"wait_second,omitempty" json:"wait_seconds,omitempty"`
}

// LayerArtifactRewrite is a single find/replace pair.
type CandyArtifactRewrite struct {
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

// MCPServerYAML represents an MCP server declaration in the candy manifest.
type MCPServerYAML struct {
	Name      string `yaml:"name" json:"name"`
	URL       string `yaml:"url" json:"url"`
	Transport string `yaml:"transport,omitempty" json:"transport,omitempty"` // "http" (default), "sse"
}

// ShellConfig represents a layer's shell-init declarations (the `shell:`
// field in the candy manifest). Mirrors the per-distro / per-package pattern: an
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

// LayerYAML represents the parsed candy manifest file.
// Unknown top-level keys are captured as tag-based package sections
// (e.g., "fedora:", "arch:", "fedora:43:", "debian,ubuntu:").
type CandyYAML struct {
	Version     string            `yaml:"version,omitempty"`     // CalVer version (YYYY.DDD.HHMM) of this layer definition
	Description *Description      `yaml:"description,omitempty"` // Gherkin-shaped self-description; replaces retired info:/status:
	Layer       []string          `yaml:"candy,omitempty"`
	Require     []string          `yaml:"require,omitempty"`
	Engine      string            `yaml:"engine,omitempty"` // required run engine: "docker" or "" (any)
	Env         map[string]string `yaml:"env,omitempty"`
	PathAppend  []string          `yaml:"path_append,omitempty"`
	Port        []PortSpec        `yaml:"port,omitempty"`
	Route       *RouteYAML        `yaml:"route,omitempty"`
	// Service is the unified service schema: a list of ServiceEntry.
	// Each entry either reuses a packaged unit (use_packaged:) or
	// defines a custom service (exec: ...).
	Service       []ServiceEntry    `yaml:"service,omitempty"`
	Volume        []VolumeYAML      `yaml:"volume,omitempty"`
	Alias         []AliasYAML       `yaml:"alias,omitempty"`
	Extract       []ExtractYAML     `yaml:"extract,omitempty"`
	Security      *SecurityConfig   `yaml:"security,omitempty"`
	Libvirt       []string          `yaml:"libvirt,omitempty"`
	Hook          *HooksConfig      `yaml:"hook,omitempty"`
	PortRelay     []int             `yaml:"port_relay,omitempty"`
	SecretYAML    []SecretYAML      `yaml:"secret,omitempty"`
	Data          []DataYAML        `yaml:"data,omitempty"`
	EnvProvides   map[string]string `yaml:"env_provide,omitempty"`    // env vars provided to OTHER containers when this service is deployed
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
	// repos, options, exclude, modules, arch AUR sub-block) live
	// under `distros:` keyed by distro name (or distro-version e.g.
	// `debian-13`, `ubuntu-24.04`).
	Package []PackageItem              `yaml:"package,omitempty"`
	Distro  map[string]*DistroPackages `yaml:"distro,omitempty"`

	// Apk is the Android app-install package format — a list of apps the
	// layer installs onto a `kind: android` device (apkeep by package id, or
	// a committed local APK). Parallel to package:/aur: but device-scoped, not
	// OS-distro-scoped, so it lives at the top level rather than under distro:.
	// Compiled into an ApkInstallStep; executed ONLY by a `target: android`
	// deploy (every other target skips it). See android_spec.go ApkPackageSpec.
	Apk []ApkPackageSpec `yaml:"apk,omitempty"`

	// LocalPkg maps a package FORMAT (pac/rpm/deb) to a bundled native-package
	// SOURCE directory (relative to the layer dir or the project root, or
	// absolute). On a DEPLOY target ov picks the entry matching the target
	// distro's package format, builds the package from that source on the HOST,
	// and installs the result onto the target via the format's auto-resolving
	// local-file install (pacman -U / dnf install / apt-get install) — delivering
	// an OS-package-tracked binary instead of an ad-hoc curl. Compiled into a
	// LocalPkgInstallStep; skipped at image build (no host package build in a
	// container). The canonical user is the `ov` layer:
	// {pac: pkg/arch, rpm: pkg/fedora, deb: pkg/debian}. A legacy scalar is
	// rejected at load with an `ov migrate` hint. See LocalPkgInstallStep.
	LocalPkg LocalPkgMap `yaml:"localpkg,omitempty"`

	// Reboot requests a reboot of the deploy target after this layer's
	// steps (a trailing RebootStep). For kernel-module layers (e.g.
	// nvidia-open-dkms) whose module only loads on a fresh boot with the
	// conflicting in-tree driver blacklisted. Honored by target:vm (reboots
	// the guest over SSH + waits for it to return); skipped at image build
	// (OCI/pod) and on target:local (never reboots the operator host).
	Reboot bool `yaml:"reboot,omitempty"`

	// Replaces root.yml / user.yml — see Task type and docs/plan.
	Vars map[string]string `yaml:"var,omitempty"`  // layer-local variables for ${VAR} substitution in tasks
	Task []Task            `yaml:"task,omitempty"` // ordered install operations

	// Shell-init declarations: an intrinsic body (init/path_append/path/
	// priority) plus per-shell sub-blocks (bash/zsh/fish/sh). Travels in
	// the org.overthinkos.shell OCI label (layer section) and is applied
	// at `ov box build` time (snippets land in /etc/profile.d/,
	// /etc/fish/conf.d/) and at `ov deploy add` time on target:local /
	// target:vm (managed-block in user rc files; per-layer drop-in for
	// fish). See ShellConfig type and /ov-build:layer "Shell Init Surface".
	Shell *ShellConfig `yaml:"shell,omitempty"`

	// Tests are declarative checks contributed by this layer. They travel
	// in the org.overthinkos.tests OCI label (layer section) and run under
	// `ov eval box` (build-time) and `ov eval live` (deploy-time).
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
	Artifact []CandyArtifact `yaml:"artifact,omitempty"`

	// Capabilities are layer-contributed image-level facts (preserve_user,
	// needs_root_after_init, init_system_hint, data_only, oci_labels).
	// Aggregated at image resolve time via AggregateLayerCapabilities.
	// Replaces the magic image-level booleans (image.bootc, image.data_image)
	// with a declarative layer-derived surface.
	Capability         *CandyCapabilities `yaml:"capability,omitempty"`
	RequiresCapability []string           `yaml:"requires_capability,omitempty"`

	// Populated by custom UnmarshalYAML:
	FormatSections map[string]*PackageSection `yaml:"-"` // format sections (rpm, deb, pac, aur, etc.)
	TagSections    map[string]*TagPkgConfig   `yaml:"-"` // distro/version tag sections
}

// layerYAMLKnownFields lists non-format top-level keys in the candy manifest.
// Unknown keys are routed to FormatSections (if matching a build.yml distro format)
// or TagSections (otherwise).
//
// `directory`, `info` deleted in the 2026-05 Calamares cutover (0 YAML files
// used either; `description:` carries the metadata `info:` previously held).
// `depends` renamed to `requires`. Calamares-shaped `packages` + `distros`
// added as the unified package surface; per-format `rpm:`/`deb:`/`pac:`/
// `aur:` and per-distro tag sections (debian:13: etc.) collapse into them
// via `ov migrate`.
var layerYAMLKnownFields = map[string]bool{
	"description": true, "version": true, "status": true,
	"name": true, "from": true,
	"candy": true, "require": true, "engine": true, "env": true,
	"path_append": true, "port": true, "route": true, "service": true,
	"volume": true, "alias": true, "extract": true, "security": true,
	"libvirt": true, "hook": true,
	"port_relay": true, "secret": true, "data": true,
	"env_provide": true, "env_require": true, "env_accept": true,
	"secret_accept": true, "secret_require": true,
	"mcp_provide": true, "mcp_require": true, "mcp_accept": true,
	"var": true, "task": true, "tests": true, "eval": true,
	"artifact":   true,
	"capability": true, "requires_capability": true,
	"package": true, "distro": true,
	"apk":      true,
	"shell":    true,
	"localpkg": true, "reboot": true,
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
//   - `distros.arch.*`  → FormatSections["pac"]   + raw extras
//   - `distros.arch.aur.*` → FormatSections["aur"]
//   - `distros.<name>-<ver>.*` → TagSections["<name>:<ver>"] (dash → colon)
func derivePackageSectionsFromCalamares(layer *Layer, ly *CandyYAML) {
	topPkgs := PackageNames(ly.Package)

	distroToFormat := map[string]string{
		"fedora": "rpm",
		"debian": "deb",
		"ubuntu": "deb",
		"arch":   "pac",
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
		// AUR sub-block under arch.
		if distroKey == "arch" && dp.AUR != nil {
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

// PackageSection represents a generic format-specific package config in the candy manifest.
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

// Task is a single install operation in the candy manifest `tasks:` list.
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
	// Cache declares additional BuildKit cache-mount paths for this task's
	// RUN, so a task that downloads or builds heavy artifacts (e.g. an SDK
	// installer) can persist them across builds the SAME way package caches
	// do — surviving an upstream layer cache-miss instead of re-fetching.
	// Ownership is derived from the task's `user:` (root → shared/locked,
	// non-root → uid/gid-owned). Paths must be absolute; ${VAR} is
	// substituted. The cache-USE logic (sentinel checks, copy-into-place)
	// lives in the task body — ov only provides the mount. Honored by
	// `cmd:` and `download:`.
	Cache []string `yaml:"cache,omitempty"`
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

func (ly *CandyYAML) UnmarshalYAML(value *yaml.Node) error {
	// Use type alias to avoid infinite recursion
	type layerYAMLAlias CandyYAML
	var alias layerYAMLAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*ly = CandyYAML(alias)

	// Capture unknown keys as format sections or tag sections.
	// Keys matching build.yml distro format names → FormatSections (parsed as raw maps).
	// All other unknown keys → TagSections (parsed as {packages: [...]}).
	if value.Kind == yaml.MappingNode {
		ly.FormatSections = make(map[string]*PackageSection)
		ly.TagSections = make(map[string]*TagPkgConfig)
		var unknownKeys []string
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
				// Not a known field and not a build.yml format: the only valid
				// remaining shape is a distro/version TAG section carrying a
				// package: list (e.g. fedora:43:, debian,ubuntu:). Decode BOTH the
				// typed struct (for Packages) and the raw map (repos/options
				// passthrough). Anything that does NOT decode to a tag-with-
				// packages is an unknown top-level key — almost always a
				// plural/singular typo of a singular field (task:/var:/layer:) —
				// collected for a hard error below instead of being silently
				// dropped (which previously masked typos until a runtime surprise).
				var cfg TagPkgConfig
				var raw map[string]interface{}
				if err := value.Content[i+1].Decode(&cfg); err != nil {
					unknownKeys = append(unknownKeys, key)
					continue
				}
				if err := value.Content[i+1].Decode(&raw); err != nil || len(cfg.Package) == 0 {
					unknownKeys = append(unknownKeys, key)
					continue
				}
				cfg.Raw = raw
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

		if len(unknownKeys) > 0 {
			return fmt.Errorf("layer has unknown top-level key(s) %v — each is neither a known field, a build.yml package format, nor a distro tag with a package: list. This is almost always a plural/singular typo: use the SINGULAR form (task: not tasks:, var: not vars:, layer: not layers:, env_provide: not env_provides:). Run `ov migrate` for a legacy config", unknownKeys)
		}
	}

	return nil
}

// RouteYAML represents a route declaration in the candy manifest
type RouteYAML struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Format-specific structs (RpmConfig, DebConfig, PacConfig, AurConfig) removed.
// All format sections are now parsed dynamically as PackageSection via build.yml distro format names.
// See PackageSection type and LayerYAML.UnmarshalYAML for the generic parsing.

// Layer represents a layer directory and its contents
type Layer struct {
	Name        string
	Path        string       // directory containing the candy manifest
	SourceDir   string       // anchor for relative file lookups (tasks.copy, data.src, install files); defaults to Path, overridden by the candy manifest's `directory:`
	Version     string       // CalVer version from the candy manifest
	Description *Description // Gherkin-shaped self-description (Feature/Narrative/Tag/Scenario)
	Status      string       // derived from Description.Tag — working/testing/broken (empty = testing)
	Info        string       // derived from Description.Feature+Narrative
	// Parse-time filesystem-probe caches: each caches a single fileExists /
	// dirExists check against SourceDir performed once at scan time. These stay
	// fields (a remote layer's cache dir may be evicted before they're read,
	// and re-probing on every access would re-do I/O) — they are NOT redundant.
	// Every OTHER Has* predicate (HasEnv/HasPorts/HasVolumes/HasData/…) is a
	// derived method computed from the populated field, not a stored bool.
	HasPixiToml       bool
	HasPyprojectToml  bool
	HasEnvironmentYml bool
	HasPackageJson    bool
	HasCargoToml      bool
	HasSrcDir         bool
	HasPixiLock       bool // the candy manifest has a non-empty tasks: list

	// Init system detection (populated by PopulateLayerInitSystem)
	InitSystems    map[string]bool // set of init system names this layer triggers
	PortRelayPorts []int           // port_relay: field (init-agnostic)

	// Layer references. Each LayerRef carries the original ref string; the bare
	// map-key form (.Bare()) and pinned version (.Version()) are derived. One
	// list per concern — no parallel bare/raw arrays (the duplication that
	// split version off the ref and enabled the silent version-collision bug).
	Require       []CandyRef // require: deps (ordering + resolution)
	IncludedLayer []CandyRef // layer: composition refs (splicing)

	// Remote layer metadata
	Remote        bool   // true if from a remote repo
	RepoPath      string // e.g. "github.com/overthinkos/overthink" (empty for local)
	SubPathPrefix string // e.g. "candy/" — parent directory within the repo for sibling resolution

	// Pre-populated from the candy manifest
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
	engine         string            // required run engine from the candy manifest ("docker", "podman", or "")
	vars           map[string]string // layer-local variables (from the candy manifest vars:)
	tasks          []Task            // ordered install operations (from the candy manifest tasks:)
	apk            []ApkPackageSpec  // Android apps to install on a kind:android device (from the candy manifest apk:)
	localpkg       map[string]string // per-format native-package source dirs (pac/rpm/deb → dir) from the candy manifest localpkg:
	reboot         bool              // reboot the deploy target after this layer (from the candy manifest reboot:)
	tests          []Check           // declarative checks (from the candy manifest tests:)
	artifacts      []CandyArtifact   // files to retrieve after setup (from the candy manifest artifacts:)
	shell          *ShellConfig      // shell-init declarations (from the candy manifest shell:)

	// Layer-contributed image-level facts (capabilities: block in the candy manifest)
	// and cross-layer requirement declarations (requires_capabilities:).
	capabilities         *CandyCapabilities
	requiresCapabilities []string
}

// ScanLayer returns all layers for the project at dir. Post-unified-cutover
// this loads overthink.yml via LoadUnified, applies discover:, and projects
// the layers map. Legacy `candy/` directory scan remains as a fallback when
// overthink.yml is absent (e.g., transitional test fixtures).
// DefaultCandyDir is the single source of truth for the on-disk directory that
// holds candy (layer) definitions. The discover: block overrides it per project
// for discovery; write/resolve paths fall back to this default. Renaming the
// candy directory project-wide is a one-line change here.
const DefaultCandyDir = "candy"

// DefaultManifest is the single convenience default for the per-directory
// discovery manifest filename. Every `discover[]` spec may override it via
// `manifest:` in overthink.yml, so the filename is configurable, never a
// baked-in requirement.
const DefaultManifest = "candy.yml"

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
	layersDir := filepath.Join(dir, DefaultCandyDir)
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
		layer, err := scanLayer(filepath.Join(layersDir, name), name, DefaultManifest)
		if err != nil {
			return nil, fmt.Errorf("scanning layer %s: %w", name, err)
		}
		layers[name] = layer
	}
	return layers, nil
}

// parseLayerYAML reads and unmarshals a candy manifest file. Strict schema:
//   - Empty / comment-only file → zero-value LayerYAML.
//   - Single top-level `candy:` key → decode its body as LayerYAML (canonical form).
//   - `candy:` + other top-level keys → error (ambiguous shape).
//   - Multi-document stream → error (the candy manifest is not a bundle file).
//   - Flat form (no `candy:` wrapper) → error with migration hint.
func parseLayerYAML(path string) (*CandyYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Empty / comment-only guard.
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &CandyYAML{}, nil
	}

	// Field-singular cutover hard-rejection: any legacy plural top-level
	// key (layers:/ports:/...) fires a clear remediation hint pointing
	// at `ov migrate` rather than letting the YAML decoder
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
		return &CandyYAML{}, nil
	}
	if len(docs) > 1 {
		return nil, fmt.Errorf("%s: the candy manifest is not a multi-document stream; bundle files belong in the unified overthink.yml", path)
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
		if k == "candy" {
			layerIdx = i + 1
		}
	}

	if layerIdx >= 0 {
		// Canonical kind-keyed form — `candy:` must be the only top-level key.
		if len(keys) != 1 {
			var other []string
			for _, k := range keys {
				if k != "candy" {
					other = append(other, k)
				}
			}
			return nil, fmt.Errorf("%s: ambiguous — `candy:` wrapper present AND other top-level keys %v (pick one form)", path, other)
		}
		// 2026-05 Calamares cutover: hard-fail on legacy field shapes.
		// Every legacy form has a one-shot remediation via `ov migrate`.
		body := inner.Content[layerIdx]
		if body != nil && body.Kind == yaml.MappingNode {
			if err := rejectLegacyLayerKeys(path, body); err != nil {
				return nil, err
			}
		}
		var ly CandyYAML
		if err := body.Decode(&ly); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return &ly, nil
	}

	// No `candy:` wrapper — legacy flat form. Reject with migration hint.
	return nil, fmt.Errorf("%s: legacy flat candy.yml form is no longer accepted. Run `ov migrate` to convert to the canonical `candy:` kind-keyed form", path)
}

// rejectLegacyLayerKeys is the 2026-05 Calamares-cutover hard-fail gate:
// every legacy field shape produces a clear error pointing at
// `ov migrate`. Runs before standard YAML decoding so the user
// sees the migration hint, not a generic "field not found" error.
func rejectLegacyLayerKeys(path string, body *yaml.Node) error {
	for i := 0; i+1 < len(body.Content); i += 2 {
		key := body.Content[i].Value
		switch key {
		case "depends":
			return fmt.Errorf("%s: the candy manifest uses legacy `depends:` field. Run: ov migrate", path)
		case "rpm", "deb", "pac", "aur":
			return fmt.Errorf("%s: the candy manifest uses legacy `%s:` block at top level. Calamares-aligned schema uses unified top-level `packages:` + per-distro `distros:` map. Run: ov migrate", path, key)
		case "directory":
			return fmt.Errorf("%s: the candy manifest uses legacy `directory:` field (removed in 2026-05 cutover). Run: ov migrate", path)
		case "info":
			return fmt.Errorf("%s: the candy manifest uses legacy `info:` field (removed; use `description:`). Run: ov migrate", path)
		}
		// Distro-tag sections like `debian:13:`, `ubuntu:24.04:`,
		// `debian,ubuntu:` — only fire when the bare leading segment
		// matches a known distro name (so we don't false-positive on
		// arbitrary YAML keys with colons).
		if d, isTag := classifyDistroTag(key); isTag && len(d) > 0 {
			return fmt.Errorf("%s: the candy manifest uses legacy distro tag section `%s:` at top level. Calamares-aligned schema nests distro overrides under `distros:`. Run: ov migrate", path, key)
		}
	}
	return nil
}

// resolveLayerSourceDir was the resolver for the legacy `directory:` field on
// the candy manifest. The field was deleted in the 2026-05 Calamares cutover; the
// helper is now a no-op kept only for any external import that still calls
// it. New code should use `path` directly.
func resolveLayerSourceDir(path, _ string) string {
	return path
}

// scanLayer scans a single layer directory
func scanLayer(path, name, manifest string) (*Layer, error) {
	layer := &Layer{
		Name: name,
		Path: path,
	}

	// Parse the candy manifest FIRST so `directory:` can redirect the anchor
	// used by install-file detection and service-file globbing below.
	var ly *CandyYAML
	if manifest == "" {
		manifest = DefaultManifest
	}
	yamlPath := filepath.Join(path, manifest)
	if fileExists(yamlPath) {
		parsed, err := parseLayerYAML(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", manifest, err)
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
		// Single shared post-parse populator (see unified.go). Both this
		// discovered-layer path and the overthink.yml inline path call it, so
		// they can never drift.
		populateLayerFromYAML(layer, ly)
	}

	return layer, nil
}

// HasInstallFiles returns true if the layer has at least one install file
func (l *Layer) HasInstallFiles() bool {
	return l.HasFormatPackages() ||
		l.HasPixiToml || l.HasPyprojectToml || l.HasEnvironmentYml ||
		l.HasPackageJson || l.HasCargoToml ||
		l.HasTasks() || l.HasApk()
}

// HasContent returns true if the layer has install files or any configuration
// that contributes to the Containerfile (env, ports, volumes, etc.)
func (l *Layer) HasContent() bool {
	return l.HasInstallFiles() || l.HasEnv() || l.HasPorts() || l.HasRoute() ||
		l.HasVolumes() || l.HasAliases() || l.HasExtract() || l.HasData() || l.HasLibvirt() ||
		l.HasAnyInit() || len(l.PortRelayPorts) > 0 ||
		len(l.serviceFiles) > 0 || len(l.service) > 0
}

// Derived Has* predicates. Each is computed from the populated field — there is
// no stored boolean (the former Has* fields were a denormalized cache that had
// to be kept in lockstep with the field at every parse site). The filesystem
// install-file probes (HasPixiToml/HasSrcDir/…) stay as cached fields; see the
// Layer struct.
func (l *Layer) HasEnv() bool     { return l.envConfig != nil }
func (l *Layer) HasPorts() bool   { return len(l.portSpecs) > 0 }
func (l *Layer) HasRoute() bool   { return l.route != nil }
func (l *Layer) HasVolumes() bool { return len(l.volumes) > 0 }
func (l *Layer) HasAliases() bool { return len(l.aliases) > 0 }
func (l *Layer) HasExtract() bool { return len(l.extract) > 0 }
func (l *Layer) HasData() bool    { return len(l.data) > 0 }
func (l *Layer) HasLibvirt() bool { return len(l.libvirt) > 0 }
func (l *Layer) HasTasks() bool   { return len(l.tasks) > 0 }
func (l *Layer) HasApk() bool     { return len(l.apk) > 0 }

// Apk returns the layer's Android app-install entries (the `apk:` package
// format). Empty for non-Android layers.
func (l *Layer) Apk() []ApkPackageSpec { return l.apk }

// LocalPkg returns the layer's native-package SOURCE dir for the given package
// FORMAT (pac/rpm/deb), or "" when the layer declares none for that format. See
// LocalPkgInstallStep.
func (l *Layer) LocalPkg(format string) string { return l.localpkg[format] }

// LocalPkgFormats returns the package formats (pac/rpm/deb) for which the layer
// declares a native-package source, sorted for deterministic iteration.
func (l *Layer) LocalPkgFormats() []string {
	out := make([]string, 0, len(l.localpkg))
	for f := range l.localpkg {
		out = append(out, f)
	}
	sortStrings(out)
	return out
}

// LocalPkgMap maps a package format (pac/rpm/deb) to a native-package source
// dir. Its UnmarshalYAML rejects the legacy scalar form with an `ov migrate`
// hint (hard cutover — the field went per-format in 2026-06).
type LocalPkgMap map[string]string

// UnmarshalYAML accepts only the per-format mapping shape; a legacy scalar
// (e.g. `localpkg: pkg/arch`) is a hard error pointing at `ov migrate`.
func (m *LocalPkgMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return fmt.Errorf("localpkg: legacy scalar form %q rejected; localpkg is now a per-format map (e.g. {pac: pkg/arch, rpm: pkg/fedora, deb: pkg/debian}). Run `ov migrate`", value.Value)
	}
	var mm map[string]string
	if err := value.Decode(&mm); err != nil {
		return err
	}
	*m = mm
	return nil
}
func (l *Layer) HasEnvProvides() bool    { return len(l.envProvides) > 0 }
func (l *Layer) HasEnvRequires() bool    { return len(l.envRequires) > 0 }
func (l *Layer) HasEnvAccepts() bool     { return len(l.envAccepts) > 0 }
func (l *Layer) HasSecretRequires() bool { return len(l.secretRequires) > 0 }
func (l *Layer) HasSecretAccepts() bool  { return len(l.secretAccepts) > 0 }
func (l *Layer) HasMCPProvides() bool    { return len(l.mcpProvides) > 0 }
func (l *Layer) HasMCPRequires() bool    { return len(l.mcpRequires) > 0 }
func (l *Layer) HasMCPAccepts() bool     { return len(l.mcpAccepts) > 0 }

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

// EnvConfig returns the environment config (pre-populated from the candy manifest)
func (l *Layer) EnvConfig() (*EnvConfig, error) {
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	return nil, nil
}

// Ports returns the ports (pre-populated from the candy manifest)
func (l *Layer) Port() ([]string, error) {
	if l.ports != nil {
		return l.ports, nil
	}
	return nil, nil
}

// PortSpecs returns the port specs with protocol info (pre-populated from the candy manifest)
func (l *Layer) PortSpecs() []PortSpec {
	return l.portSpecs
}

// Service returns the layer's unified service: list (ServiceEntry slice).
// This is the only service schema — legacy raw-INI and system_services: are
// retired entirely. External layers that still have the legacy forms must run
// `ov migrate`.
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

// Route returns the route config (pre-populated from the candy manifest)
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
		if layer.HasRoute() {
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

// Volume returns the volume declarations (pre-populated from the candy manifest)
func (l *Layer) Volume() []VolumeYAML {
	return l.volumes
}

// Extract returns the extract declarations (pre-populated from the candy manifest)
func (l *Layer) Extract() []ExtractYAML {
	return l.extract
}

// Data returns the data mappings (pre-populated from the candy manifest)
func (l *Layer) Data() []DataYAML {
	return l.data
}

// Security returns the security config (pre-populated from the candy manifest, nil if not set)
func (l *Layer) Security() *SecurityConfig {
	return l.security
}

// Libvirt returns the libvirt XML snippets (pre-populated from the candy manifest)
func (l *Layer) Libvirt() []string {
	return l.libvirt
}

// Hooks returns the lifecycle hooks config (pre-populated from the candy manifest, nil if not set)
func (l *Layer) Hooks() *HooksConfig {
	return l.hooks
}

// Shell returns the shell-init declarations (pre-populated from the candy manifest,
// nil if not set). The returned config carries an intrinsic body (init,
// path_append, path, priority) plus per-shell sub-blocks (bash/zsh/fish/
// sh) in ByShell. Selection rule applied at install time — see
// compileShellSnippetSteps in install_build.go.
func (l *Layer) Shell() *ShellConfig {
	return l.shell
}

// Secrets returns the secret declarations (pre-populated from the candy manifest)
func (l *Layer) Secret() []SecretYAML {
	return l.secrets
}

// Artifact returns the files this layer publishes back to the operator
// after its setup completes (pre-populated from the candy manifest artifact:).
func (l *Layer) Artifact() []CandyArtifact {
	return l.artifacts
}

// EnvProvides returns env vars this layer provides to other containers (pre-populated from the candy manifest)
func (l *Layer) EnvProvides() map[string]string {
	return l.envProvides
}

// EnvRequires returns env vars this layer must have from the environment (pre-populated from the candy manifest)
func (l *Layer) EnvRequire() []EnvDependency {
	return l.envRequires
}

// EnvAccepts returns env vars this layer can optionally use (pre-populated from the candy manifest)
func (l *Layer) EnvAccept() []EnvDependency {
	return l.envAccepts
}

// SecretAccepts returns credential-store-backed env vars this layer can optionally use.
// These entries flow through the credential store → podman secret → Secret=type=env quadlet
// directive pipeline, never touching plaintext deploy.yml or quadlet Environment= lines.
// Pre-populated from the candy manifest.
func (l *Layer) SecretAccept() []EnvDependency {
	return l.secretAccepts
}

// SecretRequires returns credential-store-backed env vars this layer MUST have.
// Missing entries cause ov config to hard-fail with actionable remediation.
// Pre-populated from the candy manifest.
func (l *Layer) SecretRequire() []EnvDependency {
	return l.secretRequires
}

// MCPProvides returns MCP servers this layer provides to other containers (pre-populated from the candy manifest)
func (l *Layer) MCPProvide() []MCPServerYAML {
	return l.mcpProvides
}

// MCPRequires returns MCP servers this layer must have from the environment (pre-populated from the candy manifest)
func (l *Layer) MCPRequire() []EnvDependency {
	return l.mcpRequires
}

// MCPAccepts returns MCP servers this layer can optionally use (pre-populated from the candy manifest)
func (l *Layer) MCPAccept() []EnvDependency {
	return l.mcpAccepts
}

// Engine returns the required run engine (pre-populated from the candy manifest, "" if not set)
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
		if layer.HasVolumes() {
			vols = append(vols, layer)
		}
	}
	return vols
}

// Alias returns the alias declarations (pre-populated from the candy manifest)
func (l *Layer) Alias() []AliasYAML {
	return l.aliases
}

// AliasLayer returns layers that have alias declarations
func AliasLayer(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAliases() {
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
// Bare refs use the full path format: "github.com/org/repo/candy/name".
// qualifyRemoteSiblingDeps records, for a freshly-scanned remote layer, the
// fully-qualified "<repo>/<subpathprefix><dep>" map key of each plain-name
// require:/layer: dep (the same form ScanRemoteLayer keys fetched siblings
// under). It sets each ref's resolved key (LayerRef.resolved) and leaves
// LayerRef.Raw intact, so the graph resolves on .Bare() (qualified) while the
// transitive fetch loop still keys on the original .Raw plain name. @-ref deps
// are left untouched — their bare path already resolves directly.
func qualifyRemoteSiblingDeps(layer *Layer) {
	for i := range layer.Require {
		if !layer.Require[i].IsRemote() {
			layer.Require[i].resolved = layer.RepoPath + "/" + layer.SubPathPrefix + layer.Require[i].Raw
		}
	}
	for i := range layer.IncludedLayer {
		if !layer.IncludedLayer[i].IsRemote() {
			layer.IncludedLayer[i].resolved = layer.RepoPath + "/" + layer.SubPathPrefix + layer.IncludedLayer[i].Raw
		}
	}
}

func ScanRemoteLayer(repoDir string, repoPath string, wantRefs map[string]bool) (map[string]*Layer, error) {
	layers := make(map[string]*Layer)

	for bareRef := range wantRefs {
		// Extract sub-path from bare ref: "github.com/org/repo/candy/name" -> "candy/name"
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

		layer, err := scanLayer(layerDir, name, DefaultManifest)
		if err != nil {
			return nil, fmt.Errorf("scanning remote layer %s: %w", bareRef, err)
		}
		layer.Remote = true
		layer.RepoPath = repoPath
		// Compute sub-path prefix for sibling dep resolution (e.g. "candy/")
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			layer.SubPathPrefix = subPath[:idx+1]
		}
		// Qualify plain-name sibling deps (require:/layers:) to fully-qualified
		// "<repo>/<subpathprefix><dep>" map keys, matching how fetched siblings
		// are keyed. This lets the dependency graph (graph.go) and validator
		// resolve a remote layer's transitive deps against siblings pulled from
		// the same repo, without per-call-site repo-path plumbing.
		qualifyRemoteSiblingDeps(layer)

		layers[bareRef] = layer
	}

	return layers, nil
}

// ScanAllLayer scans local layers and all remote layers, returning a merged map.
// Local layers are keyed by short name, remote layers by fully-qualified path.
// Remote refs are collected from @-prefixed refs in the candy manifest and overthink.yml.
func ScanAllLayer(dir string) (map[string]*Layer, error) {
	return ScanAllLayerWithConfig(dir, nil)
}

// ScanAllLayerWithConfig is the default-opts wrapper (enabled images only)
// around ScanAllLayerWithConfigOpts. Most call sites (deploy-mode, runtime,
// inspect) want enabled-only scanning and keep this two-arg form.
func ScanAllLayerWithConfig(dir string, cfg *Config) (map[string]*Layer, error) {
	return ScanAllLayerWithConfigOpts(dir, cfg, ResolveOpts{})
}

// ScanAllLayerWithConfigOpts scans local and remote layers.
// Collects remote refs from @-prefixed layer references and auto-downloads
// repos. opts is forwarded to CollectRemoteRefsOpts so a build with
// `--include-disabled <name>` also fetches the named disabled image's remote
// layers — keeping the FETCH set aligned with the RESOLVE set.
func ScanAllLayerWithConfigOpts(dir string, cfg *Config, opts ResolveOpts) (map[string]*Layer, error) {
	// 1. Scan local layers
	layers, err := ScanLayer(dir)
	if err != nil {
		return nil, err
	}

	// 2. Collect remote refs from @-prefixed layer references
	downloads, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		return nil, err
	}

	if len(downloads) == 0 {
		return layers, nil
	}

	// 3. Per-entity-version resolution. The git tag is ONLY the fetch coordinate;
	// the authority is each layer's own `version:`, read AFTER fetch. So fetch
	// EVERY distinct (repo, git-tag) referenced (directly or transitively),
	// collect each materialization as a candidate, then arbitrate per bare ref by
	// per-entity version (pickLayerVersion). A remote layer's plain-name
	// require:/layers: dep is a same-repo sibling at the SAME git tag; an @-ref
	// dep carries its own repo/git-tag. Fix-point until no new (repo, git-tag,
	// ref) surfaces, so cross-repo transitive closures are fully materialized.
	type repoVer struct{ repo, ver string }
	candidates := make(map[string][]layerCandidate) // bare ref -> all fetched materializations
	scanned := make(map[repoVer]map[string]bool)    // (repo, git-tag) -> refs already scanned
	defaultBranches := make(map[string]string)      // repo → resolved default branch

	queue := downloads
	for len(queue) > 0 {
		nextByKey := make(map[repoVer]map[string]bool)
		enqueue := func(repo, ver, bare string) error {
			if ver == "" {
				if b, ok := defaultBranches[repo]; ok {
					ver = b
				} else {
					b, err := GitDefaultBranch(RepoGitURL(repo))
					if err != nil {
						return fmt.Errorf("resolving default branch for %s: %w", repo, err)
					}
					defaultBranches[repo] = b
					ver = b
				}
			}
			key := repoVer{repo, ver}
			if scanned[key][bare] {
				return nil // this exact (repo, git-tag, ref) already scanned
			}
			if nextByKey[key] == nil {
				nextByKey[key] = make(map[string]bool)
			}
			nextByKey[key][bare] = true
			return nil
		}

		for _, dl := range queue {
			key := repoVer{dl.RepoPath, dl.Version}
			done := scanned[key]
			if done == nil {
				done = make(map[string]bool)
				scanned[key] = done
			}
			wantRefs := make(map[string]bool)
			for _, ref := range dl.Refs {
				if !done[ref] {
					wantRefs[ref] = true
				}
			}
			if len(wantRefs) == 0 {
				continue
			}
			cachePath, err := EnsureRepoDownloaded(dl.RepoPath, dl.Version)
			if err != nil {
				return nil, fmt.Errorf("downloading %s:%s: %w", dl.RepoPath, dl.Version, err)
			}
			remoteLayers, err := ScanRemoteLayer(cachePath, dl.RepoPath, wantRefs)
			if err != nil {
				return nil, fmt.Errorf("scanning %s:%s: %w", dl.RepoPath, dl.Version, err)
			}
			for ref := range wantRefs {
				done[ref] = true
			}
			for ref, layer := range remoteLayers {
				if layer.Version == "" {
					return nil, fmt.Errorf("remote layer %q (from %s@%s) declares no version:; its producer repo must declare one (run `ov migrate` there)", ref, dl.RepoPath, dl.Version)
				}
				candidates[ref] = append(candidates[ref], layerCandidate{
					layer:   layer,
					version: layer.Version,
					gitTag:  dl.Version,
					source:  dl.RepoPath + "@" + dl.Version,
				})

				// Enqueue this materialization's transitive deps. A plain-name dep
				// is a same-repo sibling at the SAME git tag; an @-ref dep carries
				// its own pinned repo/git-tag.
				enqueueDep := func(dep CandyRef) error {
					if dep.IsRemote() {
						p := ParseRemoteRef(dep.Raw)
						return enqueue(p.RepoPath, p.Version, dep.Bare())
					}
					return enqueue(dl.RepoPath, dl.Version, dl.RepoPath+"/"+layer.SubPathPrefix+dep.Raw)
				}
				for _, dep := range layer.Require {
					if err := enqueueDep(dep); err != nil {
						return nil, err
					}
				}
				for _, dep := range layer.IncludedLayer {
					if err := enqueueDep(dep); err != nil {
						return nil, err
					}
				}
			}
		}

		queue = nil
		for key, refs := range nextByKey {
			refList := make([]string, 0, len(refs))
			for r := range refs {
				refList = append(refList, r)
			}
			queue = append(queue, RemoteDownload{RepoPath: key.repo, Version: key.ver, Refs: refList})
		}
	}

	// 4. Arbitrate each bare ref by per-entity version; materialize the winner.
	for ref, cands := range candidates {
		winner := pickLayerVersion(ref, cands)
		if _, ok := layers[winner.layer.Name]; ok {
			fmt.Fprintf(os.Stderr, "Note: local layer %q shadows remote layer %q\n", winner.layer.Name, ref)
		}
		layers[ref] = winner.layer
	}

	return layers, nil
}

// layerCandidate is one fetched materialization of a bare layer ref. The git tag
// is the fetch coordinate; version is the layer's own per-entity `version:`.
type layerCandidate struct {
	layer   *Layer
	version string // per-entity version (layer.Version) — mandatory, never ""
	gitTag  string // fetch coordinate (the @github :vTAG)
	source  string // "<repo>@<git-tag>" for warning attribution
}

// pickLayerVersion arbitrates the candidates of ONE bare ref by per-entity
// version. Same per-entity version across different git tags => NO warning, the
// newest git tag wins (freshness). Different per-entity versions => warn once
// (naming the winner + a loser) and the newest per-entity version wins. This is
// the sole layer-version arbiter — direct and transitive refs both flow through
// it. cands is non-empty.
func pickLayerVersion(bareRef string, cands []layerCandidate) layerCandidate {
	best := cands[0]
	for _, c := range cands[1:] {
		if compareCalVer(c.version, best.version) > 0 {
			best = c // newer per-entity version
		} else if c.version == best.version && compareSemver(c.gitTag, best.gitTag) > 0 {
			best = c // same per-entity version: prefer the newest git tag
		}
	}
	for _, c := range cands {
		if c.version != best.version {
			fmt.Fprintf(os.Stderr,
				"Warning: layer %s resolved to multiple versions; using newest %s (from %s), ignoring %s (from %s)\n",
				bareRef, best.version, best.source, c.version, c.source)
			break
		}
	}
	return best
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
