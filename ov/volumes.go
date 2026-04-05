package main

import (
	"path/filepath"
	"strings"
)

// VolumeMount represents a resolved volume ready for docker/podman
type VolumeMount struct {
	VolumeName    string // e.g. "ov-openclaw-data"
	ContainerPath string // e.g. "/home/user/.openclaw" (~ expanded)
}

// CollectImageVolumes resolves all volumes for an image by traversing the
// full image chain (image → base → base's base) and collecting volume
// declarations from all layers. Volumes are deduplicated by name (first
// declaration wins — outermost image takes priority).
func CollectImageVolumes(cfg *Config, layers map[string]*Layer, imageName string, home string, excludeNames map[string]bool) ([]VolumeMount, error) {
	// Collect all layer names from the image chain (outermost first)
	var allLayerNames []string
	current := imageName
	for {
		img, ok := cfg.Images[current]
		if !ok {
			break
		}

		// Resolve layers for this image (includes transitive deps)
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			return nil, err
		}
		allLayerNames = append(allLayerNames, resolved...)

		// Walk to base if it's an internal image
		if baseImg, isInternal := cfg.Images[img.Base]; isInternal && baseImg.IsEnabled() {
			current = img.Base
		} else {
			break
		}
	}

	// Collect volumes, dedup by name (first wins), skip excluded names
	seen := make(map[string]bool)
	var mounts []VolumeMount
	for _, layerName := range allLayerNames {
		layer, ok := layers[layerName]
		if !ok || !layer.HasVolumes {
			continue
		}
		for _, vol := range layer.Volumes() {
			if seen[vol.Name] || excludeNames[vol.Name] {
				continue
			}
			seen[vol.Name] = true
			mounts = append(mounts, VolumeMount{
				VolumeName:    "ov-" + imageName + "-" + vol.Name,
				ContainerPath: expandHome(vol.Path, home),
			})
		}
	}

	// Sort by volume name for deterministic output
	sortVolumeMounts(mounts)
	return mounts, nil
}

// expandHome replaces ~ and $HOME with the resolved home directory
func expandHome(path, home string) string {
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	if path == "~" {
		return home
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	return path
}

// InstanceVolumes renames volume mounts for a specific instance.
// e.g. "ov-githubrunner-state" -> "ov-githubrunner-runner-1-state"
func InstanceVolumes(mounts []VolumeMount, imageName, instance string) []VolumeMount {
	if instance == "" {
		return mounts
	}
	prefix := "ov-" + imageName + "-"
	newPrefix := "ov-" + imageName + "-" + instance + "-"
	result := make([]VolumeMount, len(mounts))
	for i, m := range mounts {
		result[i] = VolumeMount{
			VolumeName:    strings.Replace(m.VolumeName, prefix, newPrefix, 1),
			ContainerPath: m.ContainerPath,
		}
	}
	return result
}

// resolveWorkingDir returns the container working directory.
// Prefers the "workspace" volume's container path if declared, else home.
func resolveWorkingDir(volumes []VolumeMount, bindMounts []ResolvedBindMount, home string) string {
	for _, v := range volumes {
		// Extract bare volume name from "ov-<image>-<name>"
		bare := v.VolumeName
		if strings.HasPrefix(bare, "ov-") {
			// ov-<image>-<name>: find the last hyphen-separated segment
			parts := strings.SplitN(bare, "-", 3)
			if len(parts) == 3 {
				bare = parts[2]
			}
		}
		if bare == "workspace" {
			return v.ContainerPath
		}
	}
	for _, bm := range bindMounts {
		if bm.Name == "workspace" {
			return bm.ContPath
		}
	}
	return home
}

// workspaceBindHost returns the host path of the "workspace" bind mount, or "".
func workspaceBindHost(bindMounts []ResolvedBindMount) string {
	for _, bm := range bindMounts {
		if bm.Name == "workspace" {
			return bm.HostPath
		}
	}
	return ""
}

// parseVolumeFlagsStandalone converts --volume and --bind CLI flags into DeployVolumeConfig.
// Extracted from ImageConfigSetupCmd.parseVolumeFlags for reuse in shell/start.
func parseVolumeFlagsStandalone(volumeFlags, bindFlags []string) []DeployVolumeConfig {
	var configs []DeployVolumeConfig
	seen := make(map[string]bool)

	for _, v := range volumeFlags {
		parts := strings.SplitN(v, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		if !seen[dv.Name] {
			configs = append(configs, dv)
			seen[dv.Name] = true
		}
	}

	for _, b := range bindFlags {
		if seen[b] || seen[strings.SplitN(b, "=", 2)[0]] {
			continue
		}
		if idx := strings.IndexByte(b, '='); idx >= 0 {
			name := b[:idx]
			host := b[idx+1:]
			// Resolve "." to absolute path
			if host == "." {
				if abs, err := filepath.Abs(host); err == nil {
					host = abs
				}
			}
			configs = append(configs, DeployVolumeConfig{Name: name, Type: "bind", Host: host})
			seen[name] = true
		} else {
			configs = append(configs, DeployVolumeConfig{Name: b, Type: "bind"})
			seen[b] = true
		}
	}

	return configs
}

// mergeVolumeConfigs merges CLI overrides onto deploy.yml volume configs.
// CLI overrides win by name.
func mergeVolumeConfigs(base, overrides []DeployVolumeConfig) []DeployVolumeConfig {
	if len(overrides) == 0 {
		return base
	}
	var result []DeployVolumeConfig
	seen := make(map[string]bool)
	// Overrides first (highest priority)
	for _, o := range overrides {
		result = append(result, o)
		seen[o.Name] = true
	}
	// Base configs that weren't overridden
	for _, b := range base {
		if !seen[b.Name] {
			result = append(result, b)
		}
	}
	return result
}

// sortVolumeMounts sorts volume mounts by name for deterministic output
func sortVolumeMounts(mounts []VolumeMount) {
	for i := 0; i < len(mounts)-1; i++ {
		for j := i + 1; j < len(mounts); j++ {
			if mounts[i].VolumeName > mounts[j].VolumeName {
				mounts[i], mounts[j] = mounts[j], mounts[i]
			}
		}
	}
}
