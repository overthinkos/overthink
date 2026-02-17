package main

// EngineBinary returns the binary name for the given engine.
func EngineBinary(engine string) string {
	switch engine {
	case "podman":
		return "podman"
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
