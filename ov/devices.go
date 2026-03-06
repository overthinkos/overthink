package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AutoDetectFlags provides --no-autodetect CLI flag via Kong.
// Embed in command structs that support device auto-detection.
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

// DetectedDevices holds the results of host device auto-detection.
type DetectedDevices struct {
	GPU     bool     // NVIDIA GPU detected (use CDI/--gpus)
	Devices []string // Other device paths to pass via --device
}

// DetectGPU checks whether an NVIDIA GPU is available by running nvidia-smi.
// It is a package-level var for testability.
var DetectGPU = defaultDetectGPU

func defaultDetectGPU() bool {
	cmd := exec.Command("nvidia-smi")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// devicePatterns lists device paths to auto-detect on the host.
// NVIDIA GPUs are handled separately via CDI/--gpus.
var devicePatterns = []string{
	"/dev/dri/renderD*",
	"/dev/kvm",
	"/dev/vhost-net",
	"/dev/vhost-vsock",
	"/dev/fuse",
	"/dev/net/tun",
	"/dev/hwrng",
}

// DetectHostDevices probes the host for available devices.
// It is a package-level var for testability.
var DetectHostDevices = defaultDetectHostDevices

func defaultDetectHostDevices() DetectedDevices {
	result := DetectedDevices{GPU: DetectGPU()}
	for _, pattern := range devicePatterns {
		matches, _ := filepath.Glob(pattern)
		result.Devices = append(result.Devices, matches...)
	}
	return result
}

// LogDetectedDevices prints detected devices to stderr.
func LogDetectedDevices(detected DetectedDevices) {
	var parts []string
	if detected.GPU {
		parts = append(parts, "NVIDIA GPU (CDI)")
	}
	parts = append(parts, detected.Devices...)
	if len(parts) > 0 {
		fmt.Fprintf(os.Stderr, "Auto-detected devices: %s\n", strings.Join(parts, ", "))
	}
}
