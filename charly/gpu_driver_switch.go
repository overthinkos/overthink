package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// GPU driver-mode switch — the vfio-pci <-> nvidia rebind primitive that lets a
// single passthrough-capable NVIDIA card serve EITHER a VM (vfio) OR many shared
// pods (nvidia + CDI), one mode at a time. The mode is the real mutual exclusion
// (see preempt.go's shared/exclusive arbitration); this file is just "make the
// card's host driver be X".
//
// Two mutually exclusive host bindings of the GPU's DISPLAY function:
//
//	gpuModeVfio   — display function bound to vfio-pci. The card is free for VM
//	                passthrough (libvirt managed='yes' hostdev) and is the boot
//	                DEFAULT on a passthrough host (`options vfio-pci ids=...`).
//	gpuModeNvidia — display function bound to the nvidia driver, so the host
//	                nvidia-container runtime can SHARE one card across many
//	                rootless pods via CDI (--device nvidia.com/gpu=all each).
//
// Tooling + permissions decision (researched + RDD-proven on the live card):
//   - driverctl is REJECTED — absent on hosts here AND documented to hang hard
//     (reboot-only recovery) when switching a running nvidia driver.
//   - The rootless qemu:///session libvirt CANNOT rebind — `nodedev-reattach`
//     fails "Permission denied" writing driver_override (verified). libvirt's
//     managed='yes' stays the VM-side mechanism (a no-op safety net here, since
//     the card boots vfio), but the arbiter cannot delegate the flip to it.
//   - => sudo + sysfs driver_override is the correct, only reliable primitive.
//     PCI rebind needs root; sudo is charly's established host-op pattern
//     (mirrors target:local ScopeSystem `sudo bash` steps). No new PKGBUILD dep.
//
// Only the DISPLAY function is ever flipped; the sibling AUDIO function stays on
// vfio-pci. That is load-bearing, RDD-proven: on a single-GPU host the vfio-pci
// module deregisters once it holds no device, which broke the return-to-vfio
// path (`/sys/bus/pci/drivers/vfio-pci/bind` vanished). Keeping audio on vfio-pci
// keeps the module live; compute pods only need the display function anyway.
const (
	gpuModeVfio   = "vfio"
	gpuModeNvidia = "nvidia"
)

// runGPUSwitchScript executes a root sysfs-rebind script. Package var so tests
// fake it without touching the host or invoking sudo.
var runGPUSwitchScript = func(script string) ([]byte, error) {
	cmd := exec.Command("sudo", "bash", "-c", script)
	return cmd.CombinedOutput()
}

// gpuDisplayDriver reads the live driver bound to a PCI function from sysfs
// (basename of the driver symlink; "" when unbound). Package var for tests.
var gpuDisplayDriver = func(addr string) string {
	link, err := os.Readlink("/sys/bus/pci/devices/" + addr + "/driver")
	if err != nil {
		return ""
	}
	if i := strings.LastIndex(link, "/"); i >= 0 {
		return link[i+1:]
	}
	return link
}

// gpuModeFromDriver maps a live driver name to a mode. Anything that is NOT the
// nvidia driver (vfio-pci, unbound, nouveau, ...) is the vfio/default side — the
// only state from which a VM passthrough or a fresh nvidia flip can proceed.
func gpuModeFromDriver(driver string) string {
	if driver == gpuModeNvidia {
		return gpuModeNvidia
	}
	return gpuModeVfio
}

// currentGPUMode reports the live mode of a GPU's display function. Read from
// sysfs (not the cached VFIOGpu.Driver) so it reflects reality after a flip.
func currentGPUMode(gpu VFIOGpu) string {
	return gpuModeFromDriver(gpuDisplayDriver(gpu.Addr))
}

// switchScriptToNvidia / switchScriptToVfio build the exact RDD-proven sysfs
// sequences for the GPU display function `addr` (e.g. "0000:01:00.0"). They run
// as root via runGPUSwitchScript and self-verify the resulting driver.
func switchScriptToNvidia(addr string) string {
	return fmt.Sprintf(`set -u
addr=%q
# keep vfio-pci registered (it still holds the sibling audio function)
modprobe vfio-pci 2>/dev/null || true
# unbind the display function from whatever currently holds it (vfio-pci)
cur=$(readlink /sys/bus/pci/devices/$addr/driver 2>/dev/null); cur=${cur##*/}
if [ -n "$cur" ]; then echo "$addr" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi
# force nvidia: override, load nvidia+nvidia_uvm + create /dev/nvidiaN, bind
echo nvidia > /sys/bus/pci/devices/$addr/driver_override
nvidia-modprobe -c 0 -u 2>/dev/null || true
echo "$addr" > /sys/bus/pci/drivers/nvidia/bind 2>/dev/null || true
drv=$(readlink /sys/bus/pci/devices/$addr/driver 2>/dev/null); drv=${drv##*/}
[ "$drv" = nvidia ] || { echo "switch-to-nvidia FAILED: $addr driver=${drv:-unbound}" >&2; exit 1; }
`, addr)
}

func switchScriptToVfio(addr string) string {
	return fmt.Sprintf(`set -u
addr=%q
cur=$(readlink /sys/bus/pci/devices/$addr/driver 2>/dev/null); cur=${cur##*/}
if [ -n "$cur" ]; then echo "$addr" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi
# vfio-pci may have deregistered while the display fn was on nvidia — reload it
modprobe vfio-pci 2>/dev/null || true
echo vfio-pci > /sys/bus/pci/devices/$addr/driver_override
echo "$addr" > /sys/bus/pci/drivers/vfio-pci/bind 2>/dev/null || true
# clear the override so the device tracks the boot default (ids=) thereafter
echo "" > /sys/bus/pci/devices/$addr/driver_override
drv=$(readlink /sys/bus/pci/devices/$addr/driver 2>/dev/null); drv=${drv##*/}
[ "$drv" = vfio-pci ] || { echo "switch-to-vfio FAILED: $addr driver=${drv:-unbound}" >&2; exit 1; }
`, addr)
}

// switchGPUDriverMode rebinds the GPU's display function to the target mode.
// Idempotent: a no-op (no sudo call) when already in mode.
func switchGPUDriverMode(gpu VFIOGpu, mode string) error {
	if currentGPUMode(gpu) == mode {
		return nil
	}
	var script string
	switch mode {
	case gpuModeNvidia:
		script = switchScriptToNvidia(gpu.Addr)
	case gpuModeVfio:
		script = switchScriptToVfio(gpu.Addr)
	default:
		return fmt.Errorf("unknown GPU mode %q (want %q or %q)", mode, gpuModeVfio, gpuModeNvidia)
	}
	out, err := runGPUSwitchScript(script)
	if err != nil {
		return fmt.Errorf("switching GPU %s to %s mode: %w\n%s", gpu.Addr, mode, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureCDIRoot (re)generates the nvidia CDI spec at /etc/cdi/nvidia.yaml as
// ROOT, via the same sudo seam as the driver rebind. This is required because
// /etc/cdi is root-owned and the rootless user CANNOT write it — the user-level
// EnsureCDI (devices.go) fails "mkdir /etc/cdi: permission denied" on a host
// that has no spec yet (RDD-observed live). Called after a flip to nvidia so a
// shared pod can reach the card via `--device nvidia.com/gpu=all`. Best-effort:
// a failure is logged (podman surfaces a clear CDI error if the spec is
// genuinely needed and missing), and it is a no-op when nvidia-ctk is absent.
func ensureCDIRoot() {
	if _, err := exec.LookPath("nvidia-ctk"); err != nil {
		return
	}
	script := "set -e\nmkdir -p /etc/cdi\nnvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml\n"
	if out, err := runGPUSwitchScript(script); err != nil {
		fmt.Fprintf(os.Stderr, "gpu: CDI spec generation failed: %v\n%s\n", err, strings.TrimSpace(string(out)))
	}
}

// deployNodeSharesGPU reports whether a deploy node claims a SHARED resource
// backed by a gpu selector — so it must get the GPU device (--device
// nvidia.com/gpu=all via CDI) in its quadlet/run args EVEN when the host card is
// currently vfio-bound, because the arbiter flips it to nvidia at start. This is
// the config-time analogue of live `DetectHostDevices().GPU`, which would be
// false while the card is still in vfio mode.
func deployNodeSharesGPU(node DeploymentNode, resources map[string]*ResourceDef) bool {
	for _, tok := range node.RequiredShared() {
		if rdef := resources[tok]; rdef != nil && rdef.Gpu != nil {
			return true
		}
	}
	return false
}

// gpuSwitchModeTolerant detects the GPU matching `vendor` (PCI vendor hex, e.g.
// "0x10de") and flips its display function to `mode` — TOLERANT of an absent
// card. This is the arbiter's switchMode hook (charly/preempt.go), used for
// BOTH directions, so it MUST keep a claim portable across GPU and no-GPU hosts:
//
//   - card present → flip (no-op if already in mode); a real flip failure errors.
//   - card ABSENT → skip with a note, NO error. For the vfio direction there is
//     nothing to free (the VM-side autoAllocate/libvirt is the authority and
//     fails hard at create if a required card is missing); for the nvidia
//     direction a shared pod degrades to CPU-only (its GPU checks N/A). Erroring
//     here would break every requires_exclusive bed on a no-GPU host.
//
// The manual `charly vm gpu mode` verb deliberately does NOT use this — it
// errors on an absent card, because the operator asked for a specific device.
func gpuSwitchModeTolerant(vendor, mode string) error {
	gpu, found := selectGPUByVendor(DetectVFIO(), vendor)
	if !found {
		fmt.Fprintf(os.Stderr, "preempt: no GPU matching vendor %s on this host; skipping %s-mode flip (claim stays portable)\n", normalizePCIVendor(vendor), mode)
		return nil
	}
	return switchGPUDriverMode(gpu, mode)
}
