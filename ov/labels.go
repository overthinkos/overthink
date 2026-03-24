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
	LabelBindMounts     = "org.overthinkos.bind_mounts"
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
	LabelSystemd     = "org.overthinkos.systemd"
	LabelSupervisord = "org.overthinkos.supervisord"
	LabelEnvLayers      = "org.overthinkos.env_layers"
	LabelPathAppend     = "org.overthinkos.path_append"
	LabelEngine         = "org.overthinkos.engine"
	LabelPortProtos     = "org.overthinkos.port_protos"
	LabelPortRelay      = "org.overthinkos.port_relay"
	LabelSkills         = "org.overthinkos.skills"
	LabelStatus         = "org.overthinkos.status"
	LabelInfo           = "org.overthinkos.info"
	LabelLayerVersions  = "org.overthinkos.layer_versions"
)

// LabelSchemaVersion is the current label schema version.
const LabelSchemaVersion = "1"

// LabelVolume represents a volume in the label JSON (short name form).
type LabelVolume struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LabelBindMount represents a bind mount in the label JSON (host-path-agnostic).
type LabelBindMount struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Encrypted bool   `json:"encrypted,omitempty"`
}

// LabelRoute represents a traefik route in the label JSON.
type LabelRoute struct {
	Host string `json:"host"`
	Port int    `json:"port"`
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
	BindMounts     []LabelBindMount
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
	Systemd      []string
	Supervisord  []string
	EnvLayers      map[string]string
	PathAppend     []string
	Engine         string
	PortProtos     map[int]string    // container port -> protocol ("http" or "tcp")
	PortRelay      []int             // ports with socat relay (eth0 -> loopback)
	Skills         string            // skill documentation URL
	Status         string            // effective status (working, testing, broken)
	Info           string            // aggregated status info
	LayerVersions  map[string]string // layer name -> CalVer version
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

	// Bind mounts
	if v := labels[LabelBindMounts]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.BindMounts); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelBindMounts, err)
		}
	}

	// Security
	if v := labels[LabelSecurity]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Security); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSecurity, err)
		}
	}

	// Tunnel
	if v := labels[LabelTunnel]; v != "" {
		var tunnel TunnelYAML
		if err := json.Unmarshal([]byte(v), &tunnel); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelTunnel, err)
		}
		meta.Tunnel = &tunnel
	}

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

	// Systemd units (bootc only)
	if v := labels[LabelSystemd]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Systemd); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSystemd, err)
		}
	}

	// Supervisord services
	if v := labels[LabelSupervisord]; v != "" {
		if err := json.Unmarshal([]byte(v), &meta.Supervisord); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", LabelSupervisord, err)
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

	return meta, nil
}
