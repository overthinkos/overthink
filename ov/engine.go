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

// GPURunArgs returns the engine-specific CLI arguments for GPU passthrough.
func GPURunArgs(engine string) []string {
	switch engine {
	case "podman":
		return []string{"--device", "nvidia.com/gpu=all"}
	default:
		return []string{"--gpus", "all"}
	}
}
