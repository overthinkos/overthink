package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AutoDetectFlags provides --no-autodetect CLI flag via Kong.
// Embed in command structs that support device auto-detection.
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

// DetectedDevices holds the results of host device auto-detection.
type DetectedDevices struct {
	GPU            bool     // NVIDIA GPU detected (use CDI/--gpus)
	AMDGPU         bool     // AMD GPU detected (/dev/kfd + video/render groups)
	AMDGFXVersion  string   // AMD GFX version for HSA_OVERRIDE_GFX_VERSION (e.g. "10.3.0")
	Devices        []string // Other device paths to pass via --device
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

// DetectAMDGPU checks whether an AMD GPU is available by reading the DRM driver
// name from sysfs. Returns true if any DRM card uses the "amdgpu" driver.
// It is a package-level var for testability.
var DetectAMDGPU = defaultDetectAMDGPU

func defaultDetectAMDGPU() bool {
	matches, _ := filepath.Glob("/sys/class/drm/card[0-9]*/device/driver")
	for _, driverLink := range matches {
		target, err := os.Readlink(driverLink)
		if err == nil && filepath.Base(target) == "amdgpu" {
			return true
		}
	}
	return false
}

// detectAMDGFXVersion reads the AMD GPU architecture version from KFD topology.
// Returns a version string like "10.3.0" or "" if not available.
// Reads /sys/class/kfd/kfd/topology/nodes/*/properties for gfx_target_version.
func detectAMDGFXVersion() string {
	matches, _ := filepath.Glob("/sys/class/kfd/kfd/topology/nodes/*/properties")
	for _, path := range matches {
		ver := parseKFDGFXVersion(path)
		if ver != "" {
			return ver
		}
	}
	return ""
}

// parseKFDGFXVersion reads a KFD node properties file and extracts the GFX version.
// The gfx_target_version field encodes MAJOR*10000 + MINOR*100 + STEPPING.
// Returns "MAJOR.MINOR.0" (stepping dropped) or "" if not found/zero.
func parseKFDGFXVersion(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "gfx_target_version ") {
			valStr := strings.TrimPrefix(line, "gfx_target_version ")
			val, err := strconv.Atoi(strings.TrimSpace(valStr))
			if err != nil || val == 0 {
				return "" // node 0 is CPU (version 0)
			}
			major := val / 10000
			minor := (val % 10000) / 100
			return fmt.Sprintf("%d.%d.0", major, minor)
		}
	}
	return ""
}

// devicePatterns lists device paths to auto-detect on the host.
// NVIDIA GPUs are handled separately via CDI/--gpus.
// AMD GPUs need /dev/kfd for ROCm compute access.
var devicePatterns = []string{
	"/dev/dri/renderD*",
	"/dev/kfd",
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
	result := DetectedDevices{
		GPU:    DetectGPU(),
		AMDGPU: DetectAMDGPU(),
	}
	if result.AMDGPU {
		result.AMDGFXVersion = detectAMDGFXVersion()
	}
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
	if detected.AMDGPU {
		label := "AMD GPU (kfd+render)"
		if detected.AMDGFXVersion != "" {
			label = fmt.Sprintf("AMD GPU gfx %s (kfd+render)", detected.AMDGFXVersion)
		}
		parts = append(parts, label)
	}
	parts = append(parts, detected.Devices...)
	if len(parts) > 0 {
		fmt.Fprintf(os.Stderr, "Auto-detected devices: %s\n", strings.Join(parts, ", "))
	}
}

// appendGroupsForAMDGPU adds "keep-groups" for AMD GPU access. Podman's
// keep-groups preserves all host supplementary groups (video, render, etc.)
// inside the container. It is mutually exclusive with explicit group names.
func appendGroupsForAMDGPU(groups []string) []string {
	for _, g := range groups {
		if g == "keep-groups" {
			return groups
		}
	}
	return appendUnique(groups, "keep-groups")
}

// appendEnvUnique appends an env var (KEY=VALUE) to a slice only if the key
// is not already present. This ensures user-supplied env vars take priority.
func appendEnvUnique(envVars []string, kv string) []string {
	key := strings.SplitN(kv, "=", 2)[0] + "="
	for _, e := range envVars {
		if strings.HasPrefix(e, key) {
			return envVars // key already set, don't override
		}
	}
	return append(envVars, kv)
}
