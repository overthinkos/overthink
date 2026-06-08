package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrImageNotLocal is returned when ExtractMetadata is called on an image
// that is not present in the engine's local storage. Deploy-mode commands
// unwrap this sentinel at the error boundary to render a recommendation
// pointing users to `charly box pull`.
var ErrImageNotLocal = errors.New("image not found in local storage")

// OCI label key constants (all namespaced under ai.opencharly.)
const (
	LabelVersion  = "ai.opencharly.version"
	LabelBox      = "ai.opencharly.box"
	LabelRegistry = "ai.opencharly.registry"
	LabelBootc    = "ai.opencharly.bootc"
	LabelUID      = "ai.opencharly.uid"
	LabelGID      = "ai.opencharly.gid"
	LabelUser     = "ai.opencharly.user"
	LabelHome     = "ai.opencharly.home"
	LabelPort     = "ai.opencharly.port"
	LabelVolume   = "ai.opencharly.volume"
	LabelAlias    = "ai.opencharly.alias"
	LabelSecurity = "ai.opencharly.security"
	LabelNetwork  = "ai.opencharly.network"
	// Schema v4: LabelTunnel / LabelDNS / LabelAcmeEmail / LabelEngine
	// removed — these are deployment choices with no image-declaration
	// meaning. Deploy-time values flow through DeploymentNode →
	// ImageMetadata, not through OCI labels.
	LabelEnv  = "ai.opencharly.env"
	LabelHook = "ai.opencharly.hook"
	// LabelVm + LabelLibvirt: removed in the VM hard-cutover. VM specs
	// now live in vm.yml as `kind: vm` entities; no longer embedded
	// in container image OCI labels.
	LabelRoute          = "ai.opencharly.route"
	LabelInit           = "ai.opencharly.init"
	LabelEnvCandy       = "ai.opencharly.env_candy"
	LabelPathAppend     = "ai.opencharly.path_append"
	LabelPortProto      = "ai.opencharly.port_proto"
	LabelPortRelay      = "ai.opencharly.port_relay"
	LabelSkill          = "ai.opencharly.skill"
	LabelStatus         = "ai.opencharly.status"
	LabelInfo           = "ai.opencharly.info"
	LabelCandyVersion   = "ai.opencharly.candy_version"
	LabelSecret         = "ai.opencharly.secret"
	LabelPlatformDistro = "ai.opencharly.platform.distro"
	LabelPlatformFormat = "ai.opencharly.platform.format"
	LabelBuilderUse     = "ai.opencharly.builder.use"
	LabelBuilderProvide = "ai.opencharly.builder.provide"
	LabelDataEntries    = "ai.opencharly.data"
	LabelDataBox        = "ai.opencharly.data_box"
	LabelEnvProvide     = "ai.opencharly.env_provide"
	LabelEnvRequire     = "ai.opencharly.env_require"
	LabelEnvAccept      = "ai.opencharly.env_accept"
	LabelSecretAccept   = "ai.opencharly.secret_accept"  // credential-store-backed env vars this image can optionally use
	LabelSecretRequire  = "ai.opencharly.secret_require" // credential-store-backed env vars this image must have
	LabelMCPProvide     = "ai.opencharly.mcp_provide"
	LabelMCPRequire     = "ai.opencharly.mcp_require"
	LabelMCPAccept      = "ai.opencharly.mcp_accept"
	LabelEval           = "ai.opencharly.eval" // three-section test manifest (layer/image/deploy)
	// LabelDescription — three-section Gherkin-shaped self-description for
	// every `kind:` entity the image rolled up. Each section carries one
	// LabeledDescription per contributing entity (layer/image/deploy).
	// Authored inline in YAML under `description:` on each kind; collected
	// via CollectDescriptions following the same base-chain walk as
	// CollectEval. Subject to a 256 KiB soft cap with narrative truncation.
	LabelDescription = "ai.opencharly.description"
	// LabelService — structured JSON array of CapabilityService (full
	// per-entry spec, not just names). Source-less deploy (`charly deploy from-image`)
	// reads this to reconstruct every service's config without the repo.
	LabelService = "ai.opencharly.service"
	// LabelShell — three-section JSON shell-init manifest.
	// Each section (layer/image/deploy) carries an ordered list of
	// ShellEntry contributions (origin = layer name / "image" / "deploy",
	// id, generic body, per-shell ByShell map). Source of truth for
	// `charly box inspect`, `charly deploy from-image`, and the deploy.yml
	// `shell:` overlay merge — same shape as LabelEval.
	LabelShell = "ai.opencharly.shell"
)

// LabelVolumeEntry represents a volume in the label JSON (short name form).
type LabelVolumeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LabelRouteEntry represents a traefik route in the label JSON.
type LabelRouteEntry struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// CapabilityService is the full structured spec of a single service entry
// baked into an OCI label. Mirrors ServiceEntry's fields plus two origin
// annotations (Init, Layer) so a source-less consumer can reconstruct
// everything `charly deploy` needs without the source repo.
type CapabilityService struct {
	Name             string            `json:"name"`
	Scope            string            `json:"scope,omitempty"`
	Enable           bool              `json:"enable,omitempty"`
	UsePackaged      string            `json:"use_packaged,omitempty"`
	Exec             string            `json:"exec,omitempty"`
	Env              map[string]string `json:"env,omitempty"`
	Restart          string            `json:"restart,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	User             string            `json:"user,omitempty"`
	After            []string          `json:"after,omitempty"`
	Before           []string          `json:"before,omitempty"`
	Stdout           string            `json:"stdout,omitempty"`
	StopTimeout      string            `json:"stop_timeout,omitempty"`
	Kind             string            `json:"kind,omitempty"`
	Events           string            `json:"events,omitempty"`
	AutoStart        *bool             `json:"auto_start,omitempty"`
	StartRetries     int               `json:"start_retries,omitempty"`
	StartSec         int               `json:"start_sec,omitempty"`
	StopSignal       string            `json:"stop_signal,omitempty"`
	ExitCode         string            `json:"exit_code,omitempty"`
	Priority         int               `json:"priority,omitempty"`
	Init             string            `json:"init,omitempty"`  // which init system owns this entry
	Layer            string            `json:"candy,omitempty"` // source layer name
}

// LabelDataEntry represents a data mapping stored in the ai.opencharly.data label.
type LabelDataEntry struct {
	Volume  string `json:"volume"`         // target volume name
	Staging string `json:"staging"`        // path inside image (/data/<volume>/[dest/])
	Layer   string `json:"candy"`          // source layer name
	Dest    string `json:"dest,omitempty"` // optional subdirectory within volume
}

// ImageMetadata is the runtime-relevant config extracted from image labels.
type BoxMetadata struct {
	Image     string
	Version   string // ai.opencharly.version — content-derived EffectiveVersion (highest layer version, or the image's dedicated version:)
	Registry  string
	Bootc     bool
	UID       int
	GID       int
	User      string
	Home      string
	Port      []string
	Volume    []VolumeMount
	Alias     []CollectedAlias
	Security  SecurityConfig
	Network   string
	Tunnel    *TunnelYAML // populated from deploy.yml overlay (not labels)
	DNS       string
	AcmeEmail string
	Env       []string
	Hook      *HooksConfig
	// Vm / Libvirt: removed in the VM hard-cutover. VM config lives on
	// `kind: vm` entities in vm.yml (VmSpec / LibvirtDomain), not on
	// container image OCI labels.
	Route         []LabelRouteEntry
	Init          string              // active init system name ("supervisord", "systemd", "")
	Service       []CapabilityService // structured per-entry service specs (LabelService); source-less deploy reads these
	ServiceNames  []string            // per-init service names (LabelInit companion); used by `charly service status/restart`
	EnvLayer      map[string]string
	PathAppend    []string
	Engine        string
	PortProto     map[int]string       // container port -> protocol ("http" or "tcp")
	PortRelay     []int                // ports with socat relay (eth0 -> loopback)
	Skill         string               // skill documentation URL
	Status        string               // effective status (working, testing, broken)
	Info          string               // aggregated status info
	LayerVersion  map[string]string    // layer name -> CalVer version
	Secret        []LabelSecretEntry   // secret requirements (metadata only, no values)
	Distro        []string             // distro identity tags (ai.opencharly.platform.distro)
	BuildFormat   []string             // package formats installed (ai.opencharly.platform.format)
	Builder       map[string]string    // format → builder image (ai.opencharly.builder.use)
	Build         []string             // builder capability: formats this image can build (ai.opencharly.builder.provide)
	DataEntries   []LabelDataEntry     // data staging entries for deploy-time provisioning
	DataImage     bool                 // true if this is a data-only image (FROM scratch)
	EnvProvide    map[string]string    // env vars provided to other containers (service discovery templates)
	EnvRequire    []EnvDependency      // env vars image must have from the environment
	EnvAccept     []EnvDependency      // env vars image can optionally use
	SecretAccept  []EnvDependency      // credential-store-backed env vars image can optionally use
	SecretRequire []EnvDependency      // credential-store-backed env vars image must have
	MCPProvide    []MCPServerYAML      // MCP servers provided to other containers (service discovery templates)
	MCPRequire    []EnvDependency      // MCP servers image must have from the environment
	MCPAccept     []EnvDependency      // MCP servers image can optionally use
	Eval          *LabelEvalSet        // three-section (layer/image/deploy) declarative test spec
	Description   *LabelDescriptionSet // three-section Gherkin-shaped self-description (layer/image/deploy)
	Shell         *LabelShellSet       // three-section (layer/image/deploy) shell-init manifest (2026-05 cutover)
}

// LabelShellSet is the three-section JSON manifest carried in
// ai.opencharly.shell. Mirrors LabelEvalSet's bucketing — Layer
// holds per-layer contributions (origin = layer name); Image holds
// charly.yml-level shell: declarations; Deploy holds deploy-scope
// defaults baked at build time. The deploy.yml `shell:` overlay
// merges into a separate runtime-only set via MergeDeployShell.
type LabelShellSet struct {
	Layer  []ShellEntry `json:"candy,omitempty"`
	Image  []ShellEntry `json:"box,omitempty"`
	Deploy []ShellEntry `json:"deploy,omitempty"`
}

// ShellEntry is one origin's full shell-init contribution. ID is the
// stable handle for deploy.yml overlay keying ("<origin>" or
// "<origin>:<shell>"). Origin = layer name / "image" / "deploy".
// Generic body + per-shell ByShell map mirror the in-memory
// ShellConfig struct.
type ShellEntry struct {
	Origin   string                `json:"origin"`
	ID       string                `json:"id,omitempty"`
	Generic  *ShellSpec            `json:"generic,omitempty"`
	ByShell  map[string]*ShellSpec `json:"by_shell,omitempty"`
	Priority int                   `json:"priority,omitempty"`
}

// InspectLabels reads OCI labels from a local image via engine inspect.
// Package-level var for testability.
var InspectLabels = defaultInspectLabels

func defaultInspectLabels(engine, imageRef string) (map[string]string, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", "--format", "{{json .Config.Labels}}", imageRef)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", imageRef, err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("parsing labels from %s: %w", imageRef, err)
	}
	return labels, nil
}

// ExtractMetadata reads OCI labels from a local image and returns parsed ImageMetadata.
// Returns nil if the image has no ai.opencharly labels.
// Returns ErrImageNotLocal wrapped with the image ref if the image is not in local storage.
func ExtractMetadata(engine, imageRef string) (*BoxMetadata, error) {
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		if !LocalImageExists(engine, imageRef) {
			return nil, fmt.Errorf("%w: %s", ErrImageNotLocal, imageRef)
		}
		return nil, err
	}

	version := labels[LabelVersion]
	if version == "" {
		// Empty ai.opencharly.version => not an overthink image (a plain
		// registry base). This is the charly-vs-non-charly boundary, NOT a
		// backward-compat shim: every overthink image always emits a
		// non-empty EffectiveVersion.
		return nil, nil
	}

	// Schema v4: DNS / AcmeEmail / Engine no longer read from OCI labels —
	// they are deployment choices and flow onto ImageMetadata via
	// MergeDeployOntoMetadata (deploy.yml → metadata).
	meta := &BoxMetadata{
		Image:    labels[LabelBox],
		Version:  version,
		Registry: labels[LabelRegistry],
		User:     labels[LabelUser],
		Home:     labels[LabelHome],
		Network:  labels[LabelNetwork],
	}

	// Bootc
	if labels[LabelBootc] == "true" {
		meta.Bootc = true
	}

	// UID
	if v := labels[LabelUID]; v != "" {
		uid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelUID, v, err)
		}
		meta.UID = uid
	}

	// GID
	if v := labels[LabelGID]; v != "" {
		gid, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing %s=%q: %w", LabelGID, v, err)
		}
		meta.GID = gid
	}

	// Ports
	if v := labels[LabelPort]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Port); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPort, err)
		}
	}

	// Volumes
	if v := labels[LabelVolume]; v != "" {
		var labelVols []LabelVolumeEntry
		if err := json.Unmarshal([]byte(v), &labelVols); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelVolume, err)
		}
		for _, lv := range labelVols {
			meta.Volume = append(meta.Volume, VolumeMount{
				VolumeName:    "charly-" + meta.Image + "-" + lv.Name,
				ContainerPath: lv.Path,
			})
		}
	}

	// Aliases
	if v := labels[LabelAlias]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Alias); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelAlias, err)
		}
	}

	// Security
	if v := labels[LabelSecurity]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Security); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecurity, err)
		}
	}

	// Tunnel config is a deploy-time concern — read from deploy.yml only.
	// Label is no longer written or read.

	// Env
	if v := labels[LabelEnv]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Env); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnv, err)
		}
	}

	// Hooks
	if v := labels[LabelHook]; v != "" {
		var hooks HooksConfig
		if err := json.Unmarshal([]byte(v), &hooks); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelHook, err)
		}
		meta.Hook = &hooks
	}

	// VM config + libvirt snippets: removed in the VM hard-cutover. No
	// longer emitted as OCI labels; VM definitions live in vm.yml as
	// `kind: vm` entities.

	// Routes
	if v := labels[LabelRoute]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Route); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelRoute, err)
		}
	}

	// Init system
	meta.Init = labels[LabelInit]

	// ServiceNames: read from init-specific label key
	// The label key is stored as ai.opencharly.service.<init> (e.g., ai.opencharly.service.supervisord)
	if meta.Init != "" {
		svcLabel := "ai.opencharly.service." + meta.Init
		if v := labels[svcLabel]; v != "" {
			if err := json.Unmarshal([]byte(v), &meta.ServiceNames); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", svcLabel, err)
			}
		}
	}

	// Services: full structured per-entry data (LabelService).
	if v := labels[LabelService]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Service); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelService, err)
		}
	}

	// Layer env vars
	if v := labels[LabelEnvCandy]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvLayer); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvCandy, err)
		}
	}

	// Path append
	if v := labels[LabelPathAppend]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PathAppend); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPathAppend, err)
		}
	}

	// Port protocols
	if v := labels[LabelPortProto]; v != "" {
		var protos map[string]string
		if err := json.Unmarshal([]byte(v), &protos); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPortProto, err)
		}
		meta.PortProto = make(map[int]string)
		for k, v := range protos {
			if p, err := strconv.Atoi(k); err == nil {
				meta.PortProto[p] = v
			}
		}
	}

	// Port relay
	if v := labels[LabelPortRelay]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PortRelay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPortRelay, err)
		}
	}

	// Skills
	meta.Skill = labels[LabelSkill]

	// Status and info
	meta.Status = labels[LabelStatus]
	meta.Info = labels[LabelInfo]

	// Layer versions
	if v := labels[LabelCandyVersion]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.LayerVersion); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelCandyVersion, err)
		}
	}

	// Secrets
	if v := labels[LabelSecret]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Secret); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecret, err)
		}
	}

	// Platform distro (distro identity tags; first match picks bootstrap/format templates)
	if v := labels[LabelPlatformDistro]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Distro); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPlatformDistro, err)
		}
	}

	// Platform formats (package formats installed in this image: pac, rpm, pixi, …)
	if v := labels[LabelPlatformFormat]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.BuildFormat); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPlatformFormat, err)
		}
	}

	// Builder uses (consumer-side routing: format → builder-image name)
	if v := labels[LabelBuilderUse]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Builder); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilderUse, err)
		}
	}

	// Builder provides (producer-side capability: formats this image can build for others)
	if v := labels[LabelBuilderProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Build); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilderProvide, err)
		}
	}

	// Data entries (staging paths for deploy-time provisioning)
	if v := labels[LabelDataEntries]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.DataEntries); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDataEntries, err)
		}
	}

	// Data image flag
	if labels[LabelDataBox] == "true" {
		meta.DataImage = true
	}

	// Env provides (env vars for other containers, templates with {{.ContainerName}})
	if v := labels[LabelEnvProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvProvide, err)
		}
	}

	// Env requires (env vars this image must have)
	if v := labels[LabelEnvRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvRequire, err)
		}
	}

	// Env accepts (env vars this image can optionally use)
	if v := labels[LabelEnvAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvAccept, err)
		}
	}

	// Secret requires (credential-store-backed env vars this image must have)
	if v := labels[LabelSecretRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretRequire, err)
		}
	}

	// Secret accepts (credential-store-backed env vars this image can optionally use)
	if v := labels[LabelSecretAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretAccept, err)
		}
	}

	// MCP provides (MCP servers for other containers, templates with {{.ContainerName}})
	if v := labels[LabelMCPProvide]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPProvide); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPProvide, err)
		}
	}

	// MCP requires (MCP servers this image must have)
	if v := labels[LabelMCPRequire]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPRequire); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPRequire, err)
		}
	}

	// MCP accepts (MCP servers this image can optionally use)
	if v := labels[LabelMCPAccept]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPAccept); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPAccept, err)
		}
	}

	// Tests (three-section declarative test manifest)
	if v := labels[LabelEval]; v != "" {
		var ts LabelEvalSet
		if err := json.Unmarshal([]byte(v), &ts); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEval, err)
		}
		meta.Eval = &ts
	}

	// Shell-init manifest (three-section, layer/image/deploy)
	if v := labels[LabelShell]; v != "" {
		var ss LabelShellSet
		if err := json.Unmarshal([]byte(v), &ss); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelShell, err)
		}
		meta.Shell = &ss
	}

	// Description (three-section Gherkin-shaped self-description)
	if v := labels[LabelDescription]; v != "" {
		var ds LabelDescriptionSet
		if err := json.Unmarshal([]byte(v), &ds); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDescription, err)
		}
		meta.Description = &ds
	}

	return meta, nil
}
