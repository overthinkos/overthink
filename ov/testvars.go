package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ContainerInspection is the subset of `podman inspect` JSON that the test
// runner needs to resolve runtime ${…} variables. Field names mirror the
// podman/docker inspect schema — we read the JSON the engine produces, we
// do not construct it.
type ContainerInspection struct {
	Name            string             `json:"Name"`
	Config          InspectConfig      `json:"Config"`
	NetworkSettings InspectNetwork     `json:"NetworkSettings"`
	Mounts          []InspectMount     `json:"Mounts"`
}

// InspectConfig carries the fields inside the "Config" object that we need:
// the hostname (container's internal name) and the effective env.
type InspectConfig struct {
	Hostname string   `json:"Hostname"`
	Env      []string `json:"Env"`
}

// InspectNetwork carries IP address and port-binding data. Both top-level
// IPAddress and per-network IPAddress are captured — podman rootless with
// the default network uses the per-network form; docker bridge uses the
// top-level form.
type InspectNetwork struct {
	IPAddress string                         `json:"IPAddress"`
	Networks  map[string]InspectNetworkBind  `json:"Networks"`
	// Ports keys are like "6379/tcp"; values are nil when unexposed or
	// a slice of bindings when published.
	Ports map[string][]InspectPortBind `json:"Ports"`
}

// InspectNetworkBind is the per-network record under NetworkSettings.Networks.
type InspectNetworkBind struct {
	IPAddress string `json:"IPAddress"`
}

// InspectPortBind is the host-side record of a port publication.
type InspectPortBind struct {
	HostIp   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// InspectMount describes a single mount attached to the container. For named
// volumes Name is set and Source is the volume's _data path; for bind mounts
// Name is empty and Source is the host directory.
type InspectMount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name,omitempty"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// InspectContainer is swappable for tests. Real calls shell out to
// `<engine> inspect <name>` (which returns a one-element JSON array).
var InspectContainer = defaultInspectContainer

func defaultInspectContainer(engine, containerName string) (*ContainerInspection, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", containerName)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting container %s: %w", containerName, err)
	}
	var arr []ContainerInspection
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("parsing inspect output for %s: %w", containerName, err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("inspect returned no objects for %s", containerName)
	}
	return &arr[0], nil
}

// TestVarResolver holds the materials for variable expansion under `ov test`
// (running container present) and `ov image test` (build-time, no container).
// Builders for each scope are ResolveTestVarsRuntime and ResolveTestVarsBuild.
type TestVarResolver struct {
	// Env is the final flat map: plain refs use the name as key, parameterized
	// refs use "name:arg" as key (e.g., "HOST_PORT:6379"). Matches the format
	// expected by ExpandTestVars.
	Env map[string]string
	// HasRuntime is true when the resolver was populated from a running
	// container. When false, runtime-only vars return unresolved (skip path).
	HasRuntime bool
}

// ResolveTestVarsBuild builds a variable map for build-time tests (no running
// container). Only ImageMetadata-derived vars are populated.
func ResolveTestVarsBuild(meta *ImageMetadata) *TestVarResolver {
	env := buildTimeVars(meta, "" /* no instance at build time */)
	return &TestVarResolver{Env: env, HasRuntime: false}
}

// ResolveTestVarsRuntime builds a variable map for `ov test` against a
// running container. It combines image metadata (build-time knowledge),
// deploy overlay (per-host knowledge), and podman inspect (effective runtime
// state). Any of the inputs may be nil.
//
// On inspect failure the function still returns a resolver with the
// build-time portion populated; HasRuntime is false and runtime-only vars
// will be unresolved downstream.
func ResolveTestVarsRuntime(meta *ImageMetadata, deploy *DeployImageConfig, engine, containerName string) (*TestVarResolver, error) {
	instance := ""
	if deploy != nil {
		// DeployImageConfig is per-map-key; the instance lives in the map key
		// (see parseDeployKey) so pass it in via containerName context if the
		// caller has already resolved it. For now leave the value empty —
		// callers that want INSTANCE populated should set it via the
		// resolver's Env map after this returns.
		_ = deploy
	}
	env := buildTimeVars(meta, instance)

	inspection, err := InspectContainer(engine, containerName)
	if err != nil {
		return &TestVarResolver{Env: env, HasRuntime: false}, err
	}

	mergeRuntimeVars(env, meta, inspection)
	return &TestVarResolver{Env: env, HasRuntime: true}, nil
}

// buildTimeVars populates the ImageMetadata-derived subset of the variable
// map. This is the only part of the map available at `ov image test` time.
func buildTimeVars(meta *ImageMetadata, instance string) map[string]string {
	env := map[string]string{}
	if meta == nil {
		return env
	}
	if meta.User != "" {
		env["USER"] = meta.User
	}
	if meta.Home != "" {
		env["HOME"] = meta.Home
	}
	if meta.UID != 0 {
		env["UID"] = strconv.Itoa(meta.UID)
	}
	if meta.GID != 0 {
		env["GID"] = strconv.Itoa(meta.GID)
	}
	if meta.Image != "" {
		env["IMAGE"] = meta.Image
	}
	if meta.DNS != "" {
		env["DNS"] = meta.DNS
	}
	if meta.AcmeEmail != "" {
		env["ACME_EMAIL"] = meta.AcmeEmail
	}
	if instance != "" {
		env["INSTANCE"] = instance
	}
	return env
}

// mergeRuntimeVars augments env with values extracted from a container
// inspection: IP, hostname, HOST_PORT:<N>, VOLUME_PATH:<name>,
// VOLUME_CONTAINER_PATH:<name>, ENV_<NAME>.
//
// For volume mapping, the layer-declared short name is recovered by
// stripping the "ov-<image>-" prefix from the engine's full volume name.
// Bind mounts without a named volume are cross-referenced by destination
// path against meta.Volumes to recover the short name.
func mergeRuntimeVars(env map[string]string, meta *ImageMetadata, c *ContainerInspection) {
	if c == nil {
		return
	}

	// Container name: strip leading "/" which Docker prefixes on inspect.
	name := strings.TrimPrefix(c.Name, "/")
	if name != "" {
		env["CONTAINER_NAME"] = name
	}

	// IP: prefer a per-network record if the top-level is empty (rootless podman).
	ip := c.NetworkSettings.IPAddress
	if ip == "" {
		for _, n := range c.NetworkSettings.Networks {
			if n.IPAddress != "" {
				ip = n.IPAddress
				break
			}
		}
	}
	if ip != "" {
		env["CONTAINER_IP"] = ip
	}

	// HOST_PORT:<container-port> → effective host port from the first binding.
	for k, binds := range c.NetworkSettings.Ports {
		portStr := k
		if i := strings.IndexByte(k, '/'); i >= 0 {
			portStr = k[:i] // strip "/tcp" / "/udp"
		}
		if len(binds) == 0 {
			continue
		}
		hostPort := binds[0].HostPort
		if hostPort == "" {
			continue
		}
		env["HOST_PORT:"+portStr] = hostPort
	}

	// VOLUME_PATH / VOLUME_CONTAINER_PATH — short name comes from either the
	// volume's ov-<image>- prefix or the image metadata's Volumes list.
	destToShort := map[string]string{}
	if meta != nil {
		prefix := ""
		if meta.Image != "" {
			prefix = "ov-" + meta.Image + "-"
		}
		for _, v := range meta.Volumes {
			short := strings.TrimPrefix(v.VolumeName, prefix)
			destToShort[v.ContainerPath] = short
		}
	}
	for _, m := range c.Mounts {
		short := ""
		if m.Name != "" && meta != nil && meta.Image != "" {
			short = strings.TrimPrefix(m.Name, "ov-"+meta.Image+"-")
			// Instance-qualified volumes: ov-<image>-<instance>-<short>
			// Fall back to destination lookup if the strip didn't find a short name.
			if short == m.Name {
				short = ""
			}
		}
		if short == "" {
			short = destToShort[m.Destination]
		}
		if short == "" {
			continue
		}
		env["VOLUME_PATH:"+short] = m.Source
		env["VOLUME_CONTAINER_PATH:"+short] = m.Destination
	}

	// ENV_<NAME>: effective env vars from the container.
	for _, kv := range c.Config.Env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		env["ENV_"+kv[:idx]] = kv[idx+1:]
	}
}
