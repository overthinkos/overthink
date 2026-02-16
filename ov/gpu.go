package main

import (
	"fmt"
	"os"
	"os/exec"
)

// GPUMode represents the resolved GPU mode
type GPUMode int

const (
	GPUAuto GPUMode = iota
	GPUOn
	GPUOff
)

// GPUFlags provides --gpu and --no-gpu CLI flags via Kong.
// Embed in command structs that support GPU passthrough.
type GPUFlags struct {
	GPU   bool `long:"gpu" xor:"gpu" help:"Force GPU passthrough"`
	NoGPU bool `long:"no-gpu" xor:"gpu" help:"Disable GPU passthrough"`
}

// Mode returns the GPUMode from the flag combination.
func (f GPUFlags) Mode() GPUMode {
	switch {
	case f.GPU:
		return GPUOn
	case f.NoGPU:
		return GPUOff
	default:
		return GPUAuto
	}
}

// DetectGPU checks whether an NVIDIA GPU is available by running nvidia-smi.
// It is a package-level var for testability (same pattern as exec_LookPath).
var DetectGPU = defaultDetectGPU

func defaultDetectGPU() bool {
	cmd := exec.Command("nvidia-smi")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// ResolveGPU resolves the GPU mode to a final boolean.
// GPUOn always returns true, GPUOff always returns false,
// GPUAuto calls DetectGPU.
func ResolveGPU(mode GPUMode) bool {
	switch mode {
	case GPUOn:
		return true
	case GPUOff:
		return false
	default:
		return DetectGPU()
	}
}

// LogGPU prints GPU status to stderr when enabled.
func LogGPU(gpu bool) {
	if gpu {
		fmt.Fprintln(os.Stderr, "GPU detected, enabling passthrough")
	}
}
