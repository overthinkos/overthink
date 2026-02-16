package main

import (
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
func CollectImageVolumes(cfg *Config, layers map[string]*Layer, imageName string, home string) ([]VolumeMount, error) {
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

	// Collect volumes, dedup by name (first wins)
	seen := make(map[string]bool)
	var mounts []VolumeMount
	for _, layerName := range allLayerNames {
		layer, ok := layers[layerName]
		if !ok || !layer.HasVolumes {
			continue
		}
		for _, vol := range layer.Volumes() {
			if seen[vol.Name] {
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
