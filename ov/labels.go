package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// OCI label key constants (all namespaced under org.overthinkos.)
const (
	LabelVersion        = "org.overthinkos.version"
	LabelImage          = "org.overthinkos.image"
	LabelRegistry       = "org.overthinkos.registry"
	LabelBootc          = "org.overthinkos.bootc"
	LabelUID            = "org.overthinkos.uid"
	LabelGID            = "org.overthinkos.gid"
	LabelUser           = "org.overthinkos.user"
	LabelHome           = "org.overthinkos.home"
	LabelPorts          = "org.overthinkos.ports"
	LabelVolumes        = "org.overthinkos.volumes"
	LabelAliases        = "org.overthinkos.aliases"
	LabelSecurity       = "org.overthinkos.security"
	LabelNetwork        = "org.overthinkos.network"
	LabelTunnel         = "org.overthinkos.tunnel"
	LabelDNS            = "org.overthinkos.dns"
	LabelAcmeEmail      = "org.overthinkos.acme_email"
	LabelEnv            = "org.overthinkos.env"
	LabelHooks          = "org.overthinkos.hooks"
	LabelVm             = "org.overthinkos.vm"
	LabelLibvirt        = "org.overthinkos.libvirt"
	LabelRoutes         = "org.overthinkos.routes"
	LabelInit           = "org.overthinkos.init"
	LabelEnvLayers      = "org.overthinkos.env_layers"
	LabelPathAppend     = "org.overthinkos.path_append"
	LabelEngine         = "org.overthinkos.engine"
	LabelPortProtos     = "org.overthinkos.port_protos"
	LabelPortRelay      = "org.overthinkos.port_relay"
	LabelSkills         = "org.overthinkos.skills"
	LabelStatus         = "org.overthinkos.status"
	LabelInfo           = "org.overthinkos.info"
	LabelLayerVersions  = "org.overthinkos.layer_versions"
	LabelSecrets        = "org.overthinkos.secrets"
	LabelTags           = "org.overthinkos.tags"
	LabelDistro         = "org.overthinkos.distro"
	LabelBuild          = "org.overthinkos.build"
	LabelBuilders       = "org.overthinkos.builders"
	LabelBuilds         = "org.overthinkos.builds"
	LabelDataEntries    = "org.overthinkos.data"
	LabelDataImage      = "org.overthinkos.data_image"
	LabelEnvProvides    = "org.overthinkos.env_provides"
	LabelEnvRequires    = "org.overthinkos.env_requires"
	LabelEnvAccepts     = "org.overthinkos.env_accepts"
	LabelSecretAccepts  = "org.overthinkos.secret_accepts"  // credential-store-backed env vars this image can optionally use
	LabelSecretRequires = "org.overthinkos.secret_requires" // credential-store-backed env vars this image must have
	LabelMCPProvides    = "org.overthinkos.mcp_provides"
	LabelMCPRequires    = "org.overthinkos.mcp_requires"
	LabelMCPAccepts     = "org.overthinkos.mcp_accepts"
)

// LabelSchemaVersion is the current label schema version.
const LabelSchemaVersion = "1"

// LabelVolume represents a volume in the label JSON (short name form).
type LabelVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LabelRoute represents a traefik route in the label JSON.
type LabelRoute struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// LabelDataEntry represents a data mapping stored in the org.overthinkos.data label.
type LabelDataEntry struct {
	Volume  string `json:"volume"`          // target volume name
	Staging string `json:"staging"`         // path inside image (/data/<volume>/[dest/])
	Layer   string `json:"layer"`           // source layer name
	Dest    string `json:"dest,omitempty"`  // optional subdirectory within volume
}

// ImageMetadata is the runtime-relevant config extracted from image labels.
type ImageMetadata struct {
	Image          string
	Registry       string
	Bootc          bool
	UID            int
	GID            int
	User           string
	Home           string
	Ports          []string
	Volumes        []VolumeMount
	Aliases        []CollectedAlias
	Security       SecurityConfig
	Network        string
	Tunnel         *TunnelYAML
	DNS            string
	AcmeEmail      string
	Env            []string
	Hooks          *HooksConfig
	Vm             *VmConfig
	Libvirt        []string
	Routes         []LabelRoute
	Init           string            // active init system name ("supervisord", "systemd", "")
	Services       []string          // service names for the active init system
	EnvLayers      map[string]string
	PathAppend     []string
	Engine         string
	PortProtos     map[int]string    // container port -> protocol ("http" or "tcp")
	PortRelay      []int             // ports with socat relay (eth0 -> loopback)
	Skills         string            // skill documentation URL
	Status         string            // effective status (working, testing, broken)
	Info           string            // aggregated status info
	LayerVersions  map[string]string // layer name -> CalVer version
	Secrets        []LabelSecret     // secret requirements (metadata only, no values)
	Tags           []string          // union: all + distro + build formats (for task matching)
	Distro         []string          // distro identity tags
	BuildFormats   []string          // build format list (rpm, pac, etc.)
	Builders       map[string]string // build type → builder image
	Builds         []string          // what this builder can build
	DataEntries    []LabelDataEntry  // data staging entries for deploy-time provisioning
	DataImage      bool              // true if this is a data-only image (FROM scratch)
	EnvProvides    map[string]string // env vars provided to other containers (service discovery templates)
	EnvRequires    []EnvDependency   // env vars image must have from the environment
	EnvAccepts     []EnvDependency   // env vars image can optionally use
	SecretAccepts  []EnvDependency   // credential-store-backed env vars image can optionally use
	SecretRequires []EnvDependency   // credential-store-backed env vars image must have
	MCPProvides    []MCPServerYAML   // MCP servers provided to other containers (service discovery templates)
	MCPRequires    []EnvDependency   // MCP servers image must have from the environment
	MCPAccepts     []EnvDependency   // MCP servers image can optionally use
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
// Returns nil if the image has no org.overthinkos labels.
func ExtractMetadata(engine, imageRef string) (*ImageMetadata, error) {
	labels, err := InspectLabels(engine, imageRef)
	if err != nil {
		return nil, err
	}

	version := labels[LabelVersion]
	if version == "" {
		return nil, nil
	}

	meta := &ImageMetadata{
		Image:     labels[LabelImage],
		Registry:  labels[LabelRegistry],
		User:      labels[LabelUser],
		Home:      labels[LabelHome],
		DNS:       labels[LabelDNS],
		AcmeEmail: labels[LabelAcmeEmail],
		Network:   labels[LabelNetwork],
		Engine:    labels[LabelEngine],
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
	if v := labels[LabelPorts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Ports); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPorts, err)
		}
	}

	// Volumes
	if v := labels[LabelVolumes]; v != "" {
		var labelVols []LabelVolume
		if err := json.Unmarshal([]byte(v), &labelVols); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelVolumes, err)
		}
		for _, lv := range labelVols {
			meta.Volumes = append(meta.Volumes, VolumeMount{
				VolumeName:    "ov-" + meta.Image + "-" + lv.Name,
				ContainerPath: lv.Path,
			})
		}
	}

	// Aliases
	if v := labels[LabelAliases]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Aliases); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelAliases, err)
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
	if v := labels[LabelHooks]; v != "" {
		var hooks HooksConfig
		if err := json.Unmarshal([]byte(v), &hooks); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelHooks, err)
		}
		meta.Hooks = &hooks
	}

	// VM config
	if v := labels[LabelVm]; v != "" {
		var vm VmConfig
		if err := json.Unmarshal([]byte(v), &vm); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelVm, err)
		}
		meta.Vm = &vm
	}

	// Libvirt
	if v := labels[LabelLibvirt]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Libvirt); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelLibvirt, err)
		}
	}

	// Routes
	if v := labels[LabelRoutes]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Routes); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelRoutes, err)
		}
	}

	// Init system
	meta.Init = labels[LabelInit]

	// Services: read from init-specific label key
	// The label key is stored as org.overthinkos.services.<init> (e.g., org.overthinkos.services.supervisord)
	if meta.Init != "" {
		svcLabel := "org.overthinkos.services." + meta.Init
		if v := labels[svcLabel]; v != "" {
			if err := json.Unmarshal([]byte(v), &meta.Services); err != nil {
				return nil, fmt.Errorf("parsing %s: %w", svcLabel, err)
			}
		}
	}

	// Layer env vars
	if v := labels[LabelEnvLayers]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvLayers); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvLayers, err)
		}
	}

	// Path append
	if v := labels[LabelPathAppend]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.PathAppend); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPathAppend, err)
		}
	}

	// Port protocols
	if v := labels[LabelPortProtos]; v != "" {
		var protos map[string]string
		if err := json.Unmarshal([]byte(v), &protos); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelPortProtos, err)
		}
		meta.PortProtos = make(map[int]string)
		for k, v := range protos {
			if p, err := strconv.Atoi(k); err == nil {
				meta.PortProtos[p] = v
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
	meta.Skills = labels[LabelSkills]

	// Status and info
	meta.Status = labels[LabelStatus]
	meta.Info = labels[LabelInfo]

	// Layer versions
	if v := labels[LabelLayerVersions]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.LayerVersions); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelLayerVersions, err)
		}
	}

	// Secrets
	if v := labels[LabelSecrets]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Secrets); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecrets, err)
		}
	}

	// Tags (union: all + distro + build formats)
	if v := labels[LabelTags]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Tags); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelTags, err)
		}
	}

	// Distro tags
	if v := labels[LabelDistro]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Distro); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDistro, err)
		}
	}

	// Build formats
	if v := labels[LabelBuild]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.BuildFormats); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuild, err)
		}
	}

	// Builders
	if v := labels[LabelBuilders]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Builders); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilders, err)
		}
	}

	// Builds
	if v := labels[LabelBuilds]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Builds); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBuilds, err)
		}
	}

	// Data entries (staging paths for deploy-time provisioning)
	if v := labels[LabelDataEntries]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.DataEntries); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelDataEntries, err)
		}
	}

	// Data image flag
	if labels[LabelDataImage] == "true" {
		meta.DataImage = true
	}

	// Env provides (env vars for other containers, templates with {{.ContainerName}})
	if v := labels[LabelEnvProvides]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvProvides); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvProvides, err)
		}
	}

	// Env requires (env vars this image must have)
	if v := labels[LabelEnvRequires]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvRequires); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvRequires, err)
		}
	}

	// Env accepts (env vars this image can optionally use)
	if v := labels[LabelEnvAccepts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.EnvAccepts); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelEnvAccepts, err)
		}
	}

	// Secret requires (credential-store-backed env vars this image must have)
	if v := labels[LabelSecretRequires]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretRequires); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretRequires, err)
		}
	}

	// Secret accepts (credential-store-backed env vars this image can optionally use)
	if v := labels[LabelSecretAccepts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.SecretAccepts); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecretAccepts, err)
		}
	}

	// MCP provides (MCP servers for other containers, templates with {{.ContainerName}})
	if v := labels[LabelMCPProvides]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPProvides); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPProvides, err)
		}
	}

	// MCP requires (MCP servers this image must have)
	if v := labels[LabelMCPRequires]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPRequires); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPRequires, err)
		}
	}

	// MCP accepts (MCP servers this image can optionally use)
	if v := labels[LabelMCPAccepts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.MCPAccepts); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelMCPAccepts, err)
		}
	}

	return meta, nil
}
