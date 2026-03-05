package main

// CollectSecurity merges security configs from all layers in an image,
// then applies image-level overrides. Returns a merged SecurityConfig.
// If any layer sets privileged: true, the result is privileged.
// cap_add, devices, and security_opt are unioned across all layers.
// Image-level security (from images.yml) overrides layer-level settings.
func CollectSecurity(cfg *Config, layers map[string]*Layer, imageName string) SecurityConfig {
	var merged SecurityConfig

	img, ok := cfg.Images[imageName]
	if !ok {
		return merged
	}

	// Collect from layers
	for _, layerName := range img.Layers {
		bare := BareRef(layerName)
		ly, ok := layers[bare]
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
		return []string{"--privileged"}
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
	return args
}
