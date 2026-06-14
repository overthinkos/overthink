package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// AutoDetectFlags provides --no-autodetect CLI flag via Kong.
// Embed in command structs that support device auto-detection.
type AutoDetectFlags struct {
	NoAutoDetect bool `long:"no-autodetect" help:"Disable automatic device detection"`
}

// DetectedDevices holds the results of host device auto-detection.
type DetectedDevices struct {
	GPU           bool     // NVIDIA GPU detected AND CDI achievable (driver + (spec OR nvidia-ctk))
	AMDGPU        bool     // AMD GPU detected (/dev/kfd + video/render groups)
	AMDGFXVersion string   // AMD GFX version for HSA_OVERRIDE_GFX_VERSION (e.g. "10.3.0")
	RenderNode    string   // First /dev/dri/renderD* path for DRINODE/DRI_NODE
	Devices       []string // Other device paths to pass via --device
}

// DetectGPU checks whether an NVIDIA GPU is usable via CDI: the driver must
// be loaded AND a CDI spec must be reachable (existing at /etc/cdi/nvidia.yaml
// or /var/run/cdi/nvidia.yaml, OR nvidia-ctk on PATH so EnsureCDI() can
// generate one). Driver-only is NOT enough — emitting AddDevice=nvidia.com/gpu=all
// in a quadlet whose host has no CDI spec causes podman setup to fail.
// It is a package-level var for testability.
var DetectGPU = defaultDetectGPU

func defaultDetectGPU() bool {
	cmd := exec.Command("nvidia-smi")
	cmd.Stdout = nil
	cmd.Stderr = nil
	driverLoaded := cmd.Run() == nil
	return gpuUsableViaCDI(driverLoaded,
		func(p string) error { _, err := os.Stat(p); return err },
		func(name string) error { _, err := exec.LookPath(name); return err },
	)
}

// gpuUsableViaCDI is the pure decision helper: given whether the NVIDIA
// driver is loaded plus injected stat / look-path probes, return whether
// the GPU is usable via CDI. Tests inject closures so the real filesystem
// and PATH are not touched.
func gpuUsableViaCDI(driverLoaded bool, statFn func(string) error, lookPathFn func(string) error) bool {
	if !driverLoaded {
		return false
	}
	for _, p := range []string{"/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"} {
		if statFn(p) == nil {
			return true
		}
	}
	return lookPathFn("nvidia-ctk") == nil
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
	defer f.Close() //nolint:errcheck

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "gfx_target_version "); ok {
			valStr := after
			val, err := strconv.Atoi(strings.TrimSpace(valStr))
			if err != nil || val == 0 {
				return "" // node 0 is CPU (version 0)
			}
			major := val / 10000
			minor := (val % 10000) / 100
			return fmt.Sprintf("%d.%d.0", major, minor)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: KFD properties scan error: %v\n", err)
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
	// Pick the render node for DRINODE/DRI_NODE auto-injection, preferring a
	// real GPU over the paravirtual virtio-gpu (see pickRenderNode).
	result.RenderNode = pickRenderNode(result.Devices)
	return result
}

// renderNodeVendor reads the PCI vendor id of a /dev/dri/renderD* node from
// sysfs (e.g. "0x10de" NVIDIA, "0x1002" AMD, "0x8086" Intel, "0x1af4" virtio).
// Package var so tests can supply a fake without a real /sys.
var renderNodeVendor = func(node string) string {
	b, err := os.ReadFile("/sys/class/drm/" + filepath.Base(node) + "/device/vendor")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// pickRenderNode chooses the DRINODE/DRI_NODE render node from the detected
// device list. It PREFERS a real GPU (NVIDIA/AMD/Intel) over the paravirtual
// virtio-gpu (0x1af4): on a GPU-passthrough VM the seat's virtio head is
// renderD128 and the passed-through card is renderD129, so the old first-wins
// default pointed VAAPI/encoder probing at the encode-incapable virtio node.
// Falls back to the first renderD* when no vendor distinguishes them, so
// single-GPU hosts (the common case) are unchanged.
func pickRenderNode(devices []string) string {
	var first string
	for _, d := range devices {
		if !strings.HasPrefix(filepath.Base(d), "renderD") {
			continue
		}
		if first == "" {
			first = d
		}
		switch renderNodeVendor(d) {
		case "0x10de", "0x1002", "0x8086": // NVIDIA / AMD / Intel — a real, encode-capable GPU
			return d
		}
	}
	return first
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
	for _, d := range detected.Devices {
		label := d
		if d == detected.RenderNode {
			label = d + " (DRINODE)"
		}
		parts = append(parts, label)
	}
	if len(parts) > 0 {
		fmt.Fprintf(os.Stderr, "Auto-detected devices: %s\n", strings.Join(parts, ", "))
	}
}

// EnsureCDI checks if NVIDIA CDI specs exist for podman. If not, attempts
// to generate them via nvidia-ctk. This enables GPU access in nested containers
// where CDI specs from the host are not inherited.
func EnsureCDI() {
	// Check if CDI spec already exists
	cdiPaths := []string{"/etc/cdi/nvidia.yaml", "/var/run/cdi/nvidia.yaml"}
	for _, p := range cdiPaths {
		if _, err := os.Stat(p); err == nil {
			return // CDI spec exists
		}
	}

	// Try to generate CDI spec
	ctk, err := exec.LookPath("nvidia-ctk")
	if err != nil {
		return // nvidia-ctk not installed, can't generate
	}

	_ = os.MkdirAll("/etc/cdi", 0755) // best-effort — nvidia-ctk surfaces a clear error otherwise
	cmd := exec.Command(ctk, "cdi", "generate", "--output=/etc/cdi/nvidia.yaml")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	_ = cmd.Run() // Best effort — if it fails, podman gives a clear CDI error
}

// appendGroupsForAMDGPU adds "keep-groups" for AMD GPU access. Podman's
// keep-groups preserves all host supplementary groups (video, render, etc.)
// inside the container. It is mutually exclusive with explicit group names.
func appendGroupsForAMDGPU(groups []string) []string {
	if slices.Contains(groups, "keep-groups") {
		return groups
	}
	return appendUnique(groups, "keep-groups")
}

// appendAutoDetectedEnv injects GPU-related env vars from auto-detection results.
// Uses appendEnvUnique so user-supplied env vars always take priority.
func appendAutoDetectedEnv(envVars []string, detected DetectedDevices) []string {
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}
	if detected.RenderNode != "" {
		envVars = appendEnvUnique(envVars, "DRINODE="+detected.RenderNode)
		envVars = appendEnvUnique(envVars, "DRI_NODE="+detected.RenderNode)
	}
	return envVars
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

// ---------------- VFIO / GPU passthrough host detection ----------------

// VFIOPCIDevice is a single PCI function discovered under sysfs.
type VFIOPCIDevice struct {
	Addr       string // 0000:01:00.0 (sysfs device-directory name = libvirt domain:bus:slot.function)
	VendorID   string // 0x10de
	DeviceID   string // 0x2704
	Class      string // 0x0300 (high 16 bits of the PCI class code)
	ClassLabel string // human label, e.g. "VGA controller"
	Driver     string // nvidia | nouveau | vfio-pci | "" (unbound)
	IOMMUGroup int    // -1 when the device has no iommu_group (IOMMU disabled)
}

// VFIOGpu is a display-class device plus every other function sharing its
// IOMMU group. Passthrough must move the whole group together, so the renderer
// emits one <hostdev> per GroupMember.
type VFIOGpu struct {
	VFIOPCIDevice
	GroupMembers []VFIOPCIDevice // includes the GPU function itself; sorted by Addr
}

// VFIOReport summarizes host readiness for VFIO GPU passthrough.
type VFIOReport struct {
	IOMMUEnabled bool   // /sys/kernel/iommu_groups is populated
	IOMMUKind    string // intel | amd | "" (from kernel cmdline)
	GPUs         []VFIOGpu
}

// DetectVFIO probes the host for IOMMU readiness and passthrough-capable GPUs.
// Package-level var for testability (mirrors DetectGPU).
var DetectVFIO = defaultDetectVFIO

func defaultDetectVFIO() VFIOReport {
	return scanVFIO("/sys", "/proc/cmdline")
}

// scanVFIO is the pure detector: it reads only from sysRoot + cmdlinePath, so
// tests inject a synthetic sysfs tree and a fake /proc/cmdline.
func scanVFIO(sysRoot, cmdlinePath string) VFIOReport {
	rep := VFIOReport{IOMMUKind: iommuKindFromCmdline(cmdlinePath)}

	groupsDir := filepath.Join(sysRoot, "kernel", "iommu_groups")
	if entries, err := os.ReadDir(groupsDir); err == nil && len(entries) > 0 {
		rep.IOMMUEnabled = true
	}

	// Enumerate every PCI function once so group members resolve via map lookup.
	devDirs, _ := filepath.Glob(filepath.Join(sysRoot, "bus", "pci", "devices", "*"))
	all := make(map[string]VFIOPCIDevice, len(devDirs))
	for _, dir := range devDirs {
		d := readPCIDevice(sysRoot, filepath.Base(dir))
		all[d.Addr] = d
	}

	for _, d := range all {
		if !isDisplayClass(d.Class) {
			continue
		}
		gpu := VFIOGpu{VFIOPCIDevice: d}
		for _, m := range iommuGroupMembers(sysRoot, d.IOMMUGroup) {
			if md, ok := all[m]; ok {
				gpu.GroupMembers = append(gpu.GroupMembers, md)
			}
		}
		if len(gpu.GroupMembers) == 0 {
			gpu.GroupMembers = []VFIOPCIDevice{d} // IOMMU off: the GPU alone
		}
		sort.Slice(gpu.GroupMembers, func(i, j int) bool {
			return gpu.GroupMembers[i].Addr < gpu.GroupMembers[j].Addr
		})
		rep.GPUs = append(rep.GPUs, gpu)
	}
	sort.Slice(rep.GPUs, func(i, j int) bool { return rep.GPUs[i].Addr < rep.GPUs[j].Addr })
	return rep
}

// iommuKindFromCmdline returns "intel" / "amd" / "" from the kernel cmdline.
func iommuKindFromCmdline(cmdlinePath string) string {
	b, err := os.ReadFile(cmdlinePath)
	if err != nil {
		return ""
	}
	c := string(b)
	if strings.Contains(c, "intel_iommu=on") {
		return "intel"
	}
	if strings.Contains(c, "amd_iommu=on") {
		return "amd"
	}
	return ""
}

func readPCIDevice(sysRoot, addr string) VFIOPCIDevice {
	base := filepath.Join(sysRoot, "bus", "pci", "devices", addr)
	d := VFIOPCIDevice{
		Addr:       addr,
		VendorID:   readSysTrim(filepath.Join(base, "vendor")),
		DeviceID:   readSysTrim(filepath.Join(base, "device")),
		IOMMUGroup: -1,
	}
	// class file is e.g. "0x030000"; keep the high 16 bits (0x0300).
	if raw := readSysTrim(filepath.Join(base, "class")); len(raw) >= 6 {
		d.Class = raw[:6]
		d.ClassLabel = pciClassLabel(d.Class)
	}
	if target, err := os.Readlink(filepath.Join(base, "driver")); err == nil {
		d.Driver = filepath.Base(target)
	}
	if target, err := os.Readlink(filepath.Join(base, "iommu_group")); err == nil {
		if n, err := strconv.Atoi(filepath.Base(target)); err == nil {
			d.IOMMUGroup = n
		}
	}
	return d
}

// iommuGroupMembers lists the PCI addresses in an IOMMU group.
func iommuGroupMembers(sysRoot string, group int) []string {
	if group < 0 {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(sysRoot, "kernel", "iommu_groups", strconv.Itoa(group), "devices", "*"))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, filepath.Base(m))
	}
	return out
}

// MemlockLimitBytes returns the current process's RLIMIT_MEMLOCK (soft, hard).
// VFIO passthrough pins all guest RAM, so QEMU needs a memlock limit ≥ guest
// RAM. Under rootless qemu:///session the limit is inherited from the login
// session (commonly 8 MiB), so this is a passthrough-readiness signal.
func MemlockLimitBytes() (soft, hard uint64) { //nolint:unparam // soft returned for rlimit-pair API completeness
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_MEMLOCK, &rl); err != nil {
		return 0, 0
	}
	return rl.Cur, rl.Max
}

// VfioGroupAccessible reports whether the current user can open the VFIO group
// device node (/dev/vfio/<group>). Default perms are root-only, so rootless
// passthrough needs a udev rule / ownership grant. group < 0 → no IOMMU group.
func VfioGroupAccessible(group int) bool {
	if group < 0 {
		return false
	}
	return unix.Access(fmt.Sprintf("/dev/vfio/%d", group), unix.R_OK|unix.W_OK) == nil
}

// memlockUnlimited reports whether the hard limit is effectively unlimited.
func memlockUnlimited(hard uint64) bool { return hard >= 1<<62 }

func readSysTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// isDisplayClass reports whether a PCI class (0x03xx) is a display controller.
func isDisplayClass(class string) bool {
	return strings.HasPrefix(class, "0x03")
}

func pciClassLabel(class string) string {
	switch class {
	case "0x0300":
		return "VGA controller"
	case "0x0302":
		return "3D controller"
	case "0x0380":
		return "Display controller"
	case "0x0403":
		return "Audio device"
	case "0x0604":
		return "PCI bridge"
	case "0x0c03":
		return "USB controller"
	case "0x0c0330":
		return "USB controller"
	default:
		return class
	}
}
