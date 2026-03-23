package main

import (
	"strconv"
	"strings"
)

// CollectSecurity merges security configs from all layers in an image,
// then applies image-level overrides. Returns a merged SecurityConfig.
// If any layer sets privileged: true, the result is privileged.
// cap_add, devices, and security_opt are unioned across all layers.
// shm_size takes the largest value from any layer.
// Image-level security (from images.yml) overrides layer-level settings.
func CollectSecurity(cfg *Config, layers map[string]*Layer, imageName string) SecurityConfig {
	var merged SecurityConfig

	img, ok := cfg.Images[imageName]
	if !ok {
		return merged
	}

	// Resolve full layer tree (including composing layers' sub-layers)
	allLayers, err := ResolveLayerOrder(img.Layers, layers, nil)
	if err != nil {
		allLayers = img.Layers // fall back to direct layers on error
	}

	// Collect from all layers
	for _, layerName := range allLayers {
		ly, ok := layers[layerName]
		if !ok {
			continue
		}
		sec := ly.Security()
		if sec == nil {
			continue
		}
		if sec.Privileged {
			merged.Privileged = true
		}
		merged.CapAdd = appendUnique(merged.CapAdd, sec.CapAdd...)
		merged.Devices = appendUnique(merged.Devices, sec.Devices...)
		merged.SecurityOpt = appendUnique(merged.SecurityOpt, sec.SecurityOpt...)
		merged.GroupAdd = appendUnique(merged.GroupAdd, sec.GroupAdd...)
		merged.Mounts = appendUnique(merged.Mounts, sec.Mounts...)
		if sec.ShmSize != "" {
			merged.ShmSize = maxShmSize(merged.ShmSize, sec.ShmSize)
		}
	}

	// Image-level overrides
	if img.Security != nil {
		merged.Privileged = img.Security.Privileged
		if len(img.Security.CapAdd) > 0 {
			merged.CapAdd = appendUnique(merged.CapAdd, img.Security.CapAdd...)
		}
		if len(img.Security.Devices) > 0 {
			merged.Devices = appendUnique(merged.Devices, img.Security.Devices...)
		}
		if len(img.Security.SecurityOpt) > 0 {
			merged.SecurityOpt = appendUnique(merged.SecurityOpt, img.Security.SecurityOpt...)
		}
		if img.Security.ShmSize != "" {
			merged.ShmSize = img.Security.ShmSize
		}
		if len(img.Security.GroupAdd) > 0 {
			merged.GroupAdd = appendUnique(merged.GroupAdd, img.Security.GroupAdd...)
		}
		if len(img.Security.Mounts) > 0 {
			merged.Mounts = appendUnique(merged.Mounts, img.Security.Mounts...)
		}
	}

	return merged
}

// appendUnique appends items to a slice, skipping duplicates.
func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range items {
		if !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return dst
}

// SecurityArgs returns the container run arguments for the given security config.
func SecurityArgs(sec SecurityConfig) []string {
	if sec.Privileged {
		args := []string{"--privileged"}
		if sec.ShmSize != "" {
			args = append(args, "--shm-size", sec.ShmSize)
		}
		return args
	}
	var args []string
	for _, cap := range sec.CapAdd {
		args = append(args, "--cap-add", cap)
	}
	for _, dev := range sec.Devices {
		args = append(args, "--device", dev)
	}
	for _, opt := range sec.SecurityOpt {
		args = append(args, "--security-opt", opt)
	}
	for _, group := range sec.GroupAdd {
		args = append(args, "--group-add", group)
	}
	if sec.ShmSize != "" {
		args = append(args, "--shm-size", sec.ShmSize)
	}
	return args
}

// parseShmBytes parses a size string like "256m", "1g", "1024" into bytes.
func parseShmBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(s, "g"):
		multiplier = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		multiplier = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		multiplier = 1024
		s = strings.TrimSuffix(s, "k")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n * multiplier
}

// maxShmSize returns the larger of two shm size strings.
func maxShmSize(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if parseShmBytes(a) >= parseShmBytes(b) {
		return a
	}
	return b
}
