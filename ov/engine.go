package main

// EngineBinary returns the binary name for the given engine.
// The "auto" case should not normally be reached (resolved earlier by detectEngine),
// but is handled defensively.
func EngineBinary(engine string) string {
	switch engine {
	case "podman":
		return "podman"
	case "auto":
		if detected, err := detectEngine(); err == nil {
			return detected
		}
		return "docker"
	default:
		return "docker"
	}
}

// ResolveImageEngine returns the run engine for a specific image.
// Schema v4: ImageConfig.Engine removed (deploy-only choice). Priority is
// now: layer engine requirements > global default. Deploy-time overrides
// come from DeploymentNode.Engine via ResolveImageEngineForDeploy /
// ResolveImageEngineFromMeta.
func ResolveImageEngine(cfg *Config, layers map[string]*Layer, imageName string, globalRunEngine string) string {
	img, ok := cfg.Image[imageName]
	if !ok {
		return globalRunEngine
	}

	// Layer-level engine requirements (transitive closure)
	resolved, err := ResolveLayerOrder(img.Layer, layers, nil)
	if err == nil {
		for _, layerName := range resolved {
			if layer, ok := layers[layerName]; ok && layer.Engine() != "" {
				return layer.Engine()
			}
		}
	}

	return globalRunEngine
}

// ImageRuntime returns a copy of rt with RunEngine adjusted for the given image.
// If imageEngine is empty or matches the existing RunEngine, returns the original runtime.
func ImageRuntime(rt *ResolvedRuntime, imageEngine string) *ResolvedRuntime {
	if imageEngine == "" || imageEngine == rt.RunEngine {
		return rt
	}
	rtCopy := *rt
	rtCopy.RunEngine = imageEngine
	return &rtCopy
}

// ResolveImageEngineFromDir resolves the run engine for an image using image.yml
// from the given directory. Falls back to globalEngine if no config is available.
func ResolveImageEngineFromDir(dir, imageName, globalEngine string) string {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return globalEngine
	}
	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return globalEngine
	}
	return ResolveImageEngine(cfg, layers, imageName, globalEngine)
}

// ResolveImageEngineForDeploy resolves the run engine from deploy.yml,
// falling back to globalEngine. No image.yml dependency.
func ResolveImageEngineForDeploy(imageName, instance, globalEngine string) string {
	if entry, ok := loadDeployConfigForRead("ResolveImageEngineForDeploy").Lookup(imageName, instance); ok && entry.Engine != "" {
		return entry.Engine
	}
	return globalEngine
}

// ResolveImageEngineFromMeta returns the engine from image metadata labels,
// falling back to globalEngine if not set.
func ResolveImageEngineFromMeta(meta *ImageMetadata, globalEngine string) string {
	if meta != nil && meta.Engine != "" {
		return meta.Engine
	}
	return globalEngine
}

// GPURunArgs returns the engine-specific CLI arguments for GPU passthrough.
func GPURunArgs(engine string) []string {
	switch engine {
	case "podman":
		return []string{"--device", "nvidia.com/gpu=all"}
	default:
		return []string{"--gpus", "all"}
	}
}
