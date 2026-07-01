package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/overthinkos/overthink/charly/spec"
)

// gpu_shim.go — the in-core SHIMS for GPU/VFIO host detection (cutover C11). The
// sysfs/exec detection LOGIC moved into the COMPILED-IN candy/plugin-gpu (verb:gpu);
// these shims resolve that provider and Invoke it, so the ~10 in-core consumers
// (config_image.go/start.go/shell.go CDI-env sites, `charly doctor`, `charly vm gpu`,
// `charly vm create`, and gpu_allocate.go / gpu_driver_switch.go which already call
// DetectVFIO) compile against the SAME symbol names and are invisible above the shim.
//
// host→plugin dispatch mirrors k8sgen/egress (plain resolve+Invoke). Compiled-in
// placement keeps verb:gpu resolvable with no connect step and runs the probe IN-PROC
// (so MemlockLimitBytes reads charly's OWN RLIMIT_MEMLOCK — the semantics the callers
// expect). The detection RESULT types alias package spec so consumers keep referring
// to VFIOReport/VFIOGpu/VFIOPCIDevice/DetectedDevices unchanged.

// Type aliases — the detection result types live in package spec (the SDK-importable
// home the plugin also constructs them from) and are aliased here so every package-main
// consumer compiles unchanged (R3, invisible above the shim).
type (
	VFIOReport      = spec.VFIOReport
	VFIOGpu         = spec.VFIOGpu
	VFIOPCIDevice   = spec.VFIOPCIDevice
	DetectedDevices = spec.DetectedDevices
)

// gpuProbeReply resolves verb:gpu and Invokes it with the action-multiplexed input.
// plugin-gpu is compiled-in, so resolve never misses in a correctly-built binary; a
// miss (charly built without candy/plugin-gpu) degrades to a zero reply + a loud
// stderr note rather than crashing a hot deploy path — matching the original
// best-effort, never-fail detection semantics.
func gpuProbeReply(in spec.GpuProbeInput) spec.GpuProbeReply {
	prov, ok := providerRegistry.resolve(ClassVerb, "gpu")
	if !ok {
		fmt.Fprintln(os.Stderr, "warning: gpu plugin (verb:gpu) not registered — charly built without candy/plugin-gpu; GPU/VFIO detection unavailable")
		return spec.GpuProbeReply{}
	}
	params, err := marshalJSON(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe marshal (%s): %v\n", in.Action, err)
		return spec.GpuProbeReply{}
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "gpu", Op: OpRun, Params: params})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gpu probe %s: %v\n", in.Action, err)
		return spec.GpuProbeReply{}
	}
	var reply spec.GpuProbeReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			fmt.Fprintf(os.Stderr, "warning: gpu probe %s decode: %v\n", in.Action, err)
			return spec.GpuProbeReply{}
		}
	}
	return reply
}

// DetectGPU checks whether an NVIDIA GPU is usable via CDI (driver loaded AND a CDI
// spec reachable or nvidia-ctk on PATH). Package-level var for testability (tests swap
// it with a fake); the real probe runs in candy/plugin-gpu.
var DetectGPU = func() bool {
	return gpuProbeReply(spec.GpuProbeInput{Action: "detect-gpu"}).Bool
}

// DetectAMDGPU checks whether an AMD GPU is available (amdgpu DRM driver bound).
// Package-level var for testability.
var DetectAMDGPU = func() bool {
	return gpuProbeReply(spec.GpuProbeInput{Action: "detect-amd-gpu"}).Bool
}

// DetectVFIO probes the host for IOMMU readiness and passthrough-capable GPUs.
// Package-level var for testability (mirrors DetectGPU). The pci_class_labels table
// stays in core (devices.go); the shim threads it to the plugin.
var DetectVFIO = func() VFIOReport {
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "detect-vfio", PCIClassLabels: pciClassLabels})
	if reply.Vfio == nil {
		return VFIOReport{}
	}
	return *reply.Vfio
}

// DetectHostDevices probes the host for available devices. Package-level var for
// testability. The device_patterns + gpu_vendors tables stay in core (devices.go); the
// shim threads them to the plugin.
var DetectHostDevices = func() DetectedDevices {
	reply := gpuProbeReply(spec.GpuProbeInput{
		Action:         "detect-host-devices",
		DevicePatterns: devicePatterns,
		GpuVendors:     gpuRenderVendors,
	})
	if reply.HostDevices == nil {
		return DetectedDevices{}
	}
	return *reply.HostDevices
}

// EnsureCDI generates the NVIDIA CDI spec via nvidia-ctk if none exists (user-scope,
// best-effort). The generation runs in candy/plugin-gpu.
func EnsureCDI() { gpuProbeReply(spec.GpuProbeInput{Action: "ensure-cdi"}) }

// MemlockLimitBytes returns the current process's RLIMIT_MEMLOCK (soft, hard). VFIO
// passthrough pins all guest RAM, so QEMU needs a memlock limit ≥ guest RAM. Runs
// IN-PROC in the compiled-in plugin, so it reads charly's own limit.
func MemlockLimitBytes() (soft, hard uint64) { //nolint:unparam // soft returned for rlimit-pair API completeness
	reply := gpuProbeReply(spec.GpuProbeInput{Action: "memlock"})
	return reply.MemlockSoft, reply.MemlockHard
}

// VfioGroupAccessible reports whether the current user can open the VFIO group device
// node (/dev/vfio/<group>). group < 0 → no IOMMU group.
func VfioGroupAccessible(group int) bool {
	if group < 0 {
		return false
	}
	return gpuProbeReply(spec.GpuProbeInput{Action: "vfio-group-accessible", Group: group}).Bool
}

// detectAMDGFXVersion reads the AMD GPU architecture version from KFD topology (e.g.
// "10.3.0"), for `charly doctor`'s HSA_OVERRIDE_GFX_VERSION hint. The read runs in
// candy/plugin-gpu.
func detectAMDGFXVersion() string {
	return gpuProbeReply(spec.GpuProbeInput{Action: "amd-gfx-version"}).Str
}
