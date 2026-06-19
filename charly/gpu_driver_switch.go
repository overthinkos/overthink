package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GPU driver-mode switch — the vfio-pci <-> nvidia rebind primitive that lets a
// single passthrough-capable NVIDIA card serve EITHER a VM (vfio) OR many shared
// pods (nvidia + CDI), one mode at a time. The mode is the real mutual exclusion
// (see preempt.go's shared/exclusive arbitration); this file is just "make the
// card's host driver be X".
//
// Two mutually exclusive host bindings of the GPU's IOMMU group:
//
//	gpuModeVfio   — EVERY function of the group bound to vfio-pci. The card is
//	                free for VM passthrough (libvirt managed='yes' hostdev) and is
//	                the boot DEFAULT on a passthrough host (`options vfio-pci
//	                ids=...`). A VFIO group is usable from a guest only when EVERY
//	                member is vfio-bound (VFIO_GROUP_FLAGS_VIABLE) — so both the
//	                display AND the sibling HDMI-audio function must move together.
//	gpuModeNvidia — each function bound to its CORRECT host driver: the display
//	                function to `nvidia` (so the host nvidia-container runtime can
//	                SHARE one card across many rootless pods via CDI), the
//	                HDMI-audio function to `snd_hda_intel`. The whole card is
//	                switched, never just the display function.
//
// THE DEVICE-LOCK HAZARD (root cause, source-confirmed + RDD-proven 2026-06-17,
// memory gpu-driver-switch-wedge-rca.md):
//
//	The nvidia driver's PCI `.remove` (nv_pci_remove, kernel-open nv-pci.c) is
//	reached by a sysfs `unbind`. With a non-zero `usage_count` (any open
//	/dev/nvidia* fd, live CUDA context, or nvidia_uvm/modeset/drm still
//	attached) it BLOCKS FOREVER in an `os_delay` poll loop — while the kernel
//	PCI core holds the per-device `device_lock` across the whole `.remove`. That
//	wedges every later bind/reset/remove on the device in unkillable D-state →
//	recovery is REBOOT-ONLY (no userspace primitive releases a held device_lock).
//
//	THE FIX: never sysfs-`unbind` a busy nvidia. `modprobe -r` is refcount-
//	guarded — it returns EBUSY *immediately* if any client holds the GPU and
//	NEVER reaches the blocking loop — so module-unload IS the safe, deterministic
//	detach gate. switchScriptToVfio() detaches nvidia via `modprobe -r` (EBUSY =>
//	refuse, exit 3, NEVER force-unbind), and runGPUSwitchScript bounds the whole
//	operation (context deadline + WaitDelay) as defense-in-depth so a rare
//	GSP-teardown stall frees charly + the arbiter lock instead of blocking
//	forever; a confirmed wedge poisons the resource (preempt.go) until reboot.
//
// Tooling + permissions: sudo + sysfs is the only reliable primitive (rootless
// qemu:///session libvirt cannot rebind — nodedev-reattach fails Permission
// denied writing driver_override; driverctl is absent and hangs on a running
// nvidia driver). sudo is charly's established host-op pattern. No new PKGBUILD
// dep. driver_override + drivers_probe forces the exact target driver regardless
// of the boot-time `ids=` dynamic-id table.
const (
	gpuModeVfio   = "vfio"
	gpuModeNvidia = "nvidia"

	// nvidiaVendorID is the normalized PCI vendor of NVIDIA cards (the device_lock
	// wedge is an nvidia-driver concept; status flags wedges only on these cards).
	nvidiaVendorID = "0x10de"

	// hostDriverDisplay / hostDriverAudio / hostDriverVfio are the host drivers a
	// group function binds to. The display (VGA/3D, class 0x03xx) function takes
	// nvidia in host mode; the HDMI-audio (class 0x0403) function takes
	// snd_hda_intel; everything takes vfio-pci for passthrough.
	hostDriverDisplay = "nvidia"
	hostDriverAudio   = "snd_hda_intel"
	hostDriverVfio    = "vfio-pci"

	// gpuSwitchTimeout bounds the whole sudo rebind script. The RDD-proven switch
	// completes in seconds; a script that runs longer is either a GSP-teardown
	// stall or the device_lock wedge — either way charly must stop waiting (and
	// release the arbiter lock) rather than block forever. gpuSwitchWaitDelay
	// bounds how long Cmd.Wait lingers for output after the deadline kills the
	// shell, so a leaked D-state grandchild holding the pipe cannot hang charly.
	gpuSwitchTimeout   = 90 * time.Second
	gpuSwitchWaitDelay = 5 * time.Second

	// nvidiaInUseMarker is the stable substring switchScriptToVfio prints (and
	// switchGPUDriverMode detects) when `modprobe -r nvidia` returns EBUSY — the
	// card is still held by a client. ONE source for the bash echo + the Go
	// detection so they never drift (R3). When detected, switchGPUDriverMode
	// enriches the refusal with the actual holding process(es).
	nvidiaInUseMarker = "nvidia module still in use"
)

// errGPUSwitchWedged signals that a switch did not complete because the nvidia
// `.remove` is stuck holding the device_lock (deadline exceeded, or the script
// self-detected a D-state task in nv_pci_remove). The arbiter POISONS the
// resource on this error (preempt.go) so no later claimant re-wedges the card;
// recovery is a host reboot.
var errGPUSwitchWedged = errors.New("GPU driver switch wedged: nvidia .remove stuck holding the device_lock — host reboot required")

// runGPUSwitchScript executes a root sysfs-rebind script under a bounded context
// so a kernel-side stall can never block charly forever. Package var so tests
// fake it without touching the host or invoking sudo. A deadline timeout maps to
// errGPUSwitchWedged (the only thing that makes a brief rebind run >90s is the
// device_lock wedge / GSP-teardown stall).
var runGPUSwitchScript = func(script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gpuSwitchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "bash", "-c", script)
	cmd.WaitDelay = gpuSwitchWaitDelay // bound Wait after kill — a D-state child can't be reaped
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, errGPUSwitchWedged
	}
	return out, err
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

// currentGPUMode reports the live mode of a GPU's DISPLAY function — the
// indicator of which mode the card as a whole is in. Read from sysfs (not the
// cached VFIOGpu.Driver) so it reflects reality after a flip.
func currentGPUMode(gpu VFIOGpu) string {
	return gpuModeFromDriver(gpuDisplayDriver(gpu.Addr))
}

// hostDriverForFunction maps an IOMMU-group member (by its PCI class) + target
// mode to the host driver it should bind to. vfio mode => every function on
// vfio-pci (group viability). nvidia/host mode => display on nvidia, HDMI-audio
// on snd_hda_intel, any other sibling on vfio-pci (left safe — host use of the
// GPU needs only the display function; group viability is a passthrough-only
// requirement).
func hostDriverForFunction(class, mode string) string {
	if mode == gpuModeVfio {
		return hostDriverVfio
	}
	switch {
	case strings.HasPrefix(class, "0x03"): // VGA / 3D / display controller
		return hostDriverDisplay
	case class == "0x0403": // HDMI/DisplayPort audio
		return hostDriverAudio
	default:
		return hostDriverVfio
	}
}

// groupInMode reports whether EVERY function of the GPU's IOMMU group is already
// bound to the driver the target mode wants (live sysfs read) — the idempotency
// gate, group-aware (a half-switched group, e.g. display on nvidia but audio
// stranded on vfio, is NOT "in mode" and gets completed).
func groupInMode(gpu VFIOGpu, mode string) bool {
	for _, m := range gpu.GroupMembers {
		if gpuDisplayDriver(m.Addr) != hostDriverForFunction(m.Class, mode) {
			return false
		}
	}
	return true
}

// switchScriptToNvidia builds the group-aware vfio->host rebind: each function to
// its host driver (display->nvidia, audio->snd_hda_intel), then create the
// /dev/nvidia* nodes. The BIND direction never enters the nvidia .remove hazard.
func switchScriptToNvidia(gpu VFIOGpu) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	for _, m := range gpu.GroupMembers {
		target := hostDriverForFunction(m.Class, gpuModeNvidia)
		fmt.Fprintf(&b, "modprobe %s 2>/dev/null || true\n", target)
		fmt.Fprintf(&b, "a=%q; want=%q\n", m.Addr, target)
		b.WriteString("cur=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); cur=${cur##*/}\n")
		b.WriteString("if [ -n \"$cur\" ] && [ \"$cur\" != \"$want\" ]; then echo \"$a\" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi\n")
		b.WriteString("echo \"$want\" > /sys/bus/pci/devices/$a/driver_override\n")
		b.WriteString("echo \"$a\" > /sys/bus/pci/drivers_probe 2>/dev/null || true\n")
	}
	// create /dev/nvidiaN + /dev/nvidiactl + /dev/nvidia-uvm for CDI/container use
	b.WriteString("nvidia-modprobe -c 0 -u 2>/dev/null || true\n")
	fmt.Fprintf(&b, "d=%q\n", gpu.Addr)
	b.WriteString("drv=$(readlink /sys/bus/pci/devices/$d/driver 2>/dev/null); drv=${drv##*/}\n")
	b.WriteString("[ \"$drv\" = nvidia ] || { echo \"switch-to-nvidia FAILED: $d driver=${drv:-unbound}\" >&2; exit 1; }\n")
	return b.String()
}

// switchScriptToVfio builds the group-aware host->vfio rebind via the RDD-proven
// SAFE detach: guarded module unload (NEVER a sysfs-unbind of a busy nvidia, the
// device_lock wedge). modprobe -r returns EBUSY immediately if a client still
// holds the GPU => exit 3 (refuse, do not force). Then bind every function to
// vfio-pci. A post-bind verification failure WITH a D-state task in
// nv_pci_remove => exit 4 (WEDGED, reboot required).
func switchScriptToVfio(gpu VFIOGpu) string {
	var b strings.Builder
	b.WriteString("set -u\n")
	// best-effort host-side quiesce of the common host holder
	b.WriteString("systemctl stop nvidia-persistenced 2>/dev/null || true\n")
	// SAFE detach: unload the dependent stack, then nvidia itself — refcount-guarded.
	b.WriteString("modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem 2>/dev/null || true\n")
	b.WriteString("if lsmod | grep -q '^nvidia '; then\n")
	b.WriteString("  if ! modprobe -r nvidia 2>/dev/null; then\n")
	fmt.Fprintf(&b, "    echo \"switch-to-vfio REFUSED: %s (a GPU client holds the card) — refusing to force-unbind (would wedge the device_lock)\" >&2\n", nvidiaInUseMarker)
	b.WriteString("    exit 3\n")
	b.WriteString("  fi\n")
	b.WriteString("fi\n")
	b.WriteString("modprobe vfio-pci 2>/dev/null || true\n")
	for _, m := range gpu.GroupMembers {
		fmt.Fprintf(&b, "a=%q\n", m.Addr)
		b.WriteString("cur=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); cur=${cur##*/}\n")
		b.WriteString("if [ -n \"$cur\" ] && [ \"$cur\" != vfio-pci ]; then echo \"$a\" > /sys/bus/pci/drivers/$cur/unbind 2>/dev/null || true; fi\n")
		b.WriteString("echo vfio-pci > /sys/bus/pci/devices/$a/driver_override\n")
		b.WriteString("echo \"$a\" > /sys/bus/pci/drivers_probe 2>/dev/null || true\n")
		b.WriteString("echo \"\" > /sys/bus/pci/devices/$a/driver_override\n") // track the boot ids= default thereafter
	}
	b.WriteString("rc=0\n")
	for _, m := range gpu.GroupMembers {
		fmt.Fprintf(&b, "a=%q\n", m.Addr)
		b.WriteString("drv=$(readlink /sys/bus/pci/devices/$a/driver 2>/dev/null); drv=${drv##*/}\n")
		b.WriteString("if [ \"$drv\" != vfio-pci ]; then\n")
		b.WriteString("  if grep -lqs -e nv_pci_remove -e os_delay /proc/*/stack 2>/dev/null; then echo \"switch-to-vfio WEDGED: $a driver=${drv:-unbound}; nv_pci_remove in D-state — host reboot required\" >&2; exit 4; fi\n")
		b.WriteString("  echo \"switch-to-vfio FAILED: $a driver=${drv:-unbound}\" >&2; rc=1\n")
		b.WriteString("fi\n")
	}
	b.WriteString("exit $rc\n")
	return b.String()
}

// switchGPUDriverMode rebinds the GPU's WHOLE IOMMU group to the target mode.
// Idempotent: a no-op (no sudo call) when every function is already in mode. A
// wedge (deadline / self-detected D-state) returns errGPUSwitchWedged so callers
// can poison the resource.
func switchGPUDriverMode(gpu VFIOGpu, mode string) error {
	if groupInMode(gpu, mode) {
		return nil
	}
	var script string
	switch mode {
	case gpuModeNvidia:
		script = switchScriptToNvidia(gpu)
	case gpuModeVfio:
		script = switchScriptToVfio(gpu)
	default:
		return fmt.Errorf("unknown GPU mode %q (want %q or %q)", mode, gpuModeVfio, gpuModeNvidia)
	}
	out, err := runGPUSwitchScript(script)
	if err == nil {
		return nil
	}
	if errors.Is(err, errGPUSwitchWedged) || strings.Contains(string(out), "WEDGED") {
		return fmt.Errorf("%w\n%s", errGPUSwitchWedged, strings.TrimSpace(string(out)))
	}
	// EBUSY refusal (exit 3): nvidia is still held. The arbiter auto-preempts
	// charly-managed GPU holders BEFORE this switch, so any remaining holder is
	// necessarily EXTERNAL (btop, nvidia-smi -l, a desktop GPU widget, …) — name
	// it so the refusal is actionable instead of cryptic. Exit 3 + no-force are
	// unchanged; this only enriches the message.
	if mode == gpuModeVfio && strings.Contains(string(out), nvidiaInUseMarker) {
		return nvidiaInUseRefusal(discoverNvidiaHolders())
	}
	return fmt.Errorf("switching GPU %s to %s mode: %w\n%s", gpu.Addr, mode, err, strings.TrimSpace(string(out)))
}

// NvidiaHolder is one process holding an /dev/nvidia* device open — discovered
// when a vfio switch is REFUSED because the card is still in use. Since the
// arbiter auto-preempts charly-managed GPU pods before the switch, a holder
// found here is necessarily an EXTERNAL / non-charly client.
type NvidiaHolder struct {
	PID  int
	Comm string
}

// discoverNvidiaHolders enumerates processes with an open /dev/nvidia* fd by
// walking /proc/<pid>/fd symlinks. Best-effort + never errors (it only enriches
// a refusal message): a process that exited mid-scan, or a root-owned fd
// unreadable without privilege, is silently skipped. Deduped by PID, sorted for
// deterministic output. Package var so tests inject a fixture without /proc.
var discoverNvidiaHolders = defaultDiscoverNvidiaHolders

func defaultDiscoverNvidiaHolders() []NvidiaHolder {
	fdDirs, _ := filepath.Glob("/proc/[0-9]*/fd")
	var holders []NvidiaHolder
	for _, fdDir := range fdDirs {
		pid, err := strconv.Atoi(filepath.Base(filepath.Dir(fdDir)))
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(fdDir)
		for _, fd := range fds {
			target, lerr := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if lerr == nil && strings.HasPrefix(target, "/dev/nvidia") {
				holders = append(holders, NvidiaHolder{PID: pid, Comm: procComm(pid)})
				break // one entry per process even if it holds several nvidia fds
			}
		}
	}
	sort.Slice(holders, func(i, j int) bool { return holders[i].PID < holders[j].PID })
	return holders
}

// procComm reads a process's command name from /proc/<pid>/comm ("?" when gone).
func procComm(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return "?"
	}
	return strings.TrimSpace(string(b))
}

// formatNvidiaHolders renders the holder list into the actionable clause of a
// vfio-switch refusal. Empty (none identified) → a generic clause; one or many
// → "external process(es): <comm> (pid N), …". Pure + unit-tested; the discovery
// (defaultDiscoverNvidiaHolders) is best-effort and may legitimately return none
// (a holder that exited between the EBUSY and the scan).
func formatNvidiaHolders(holders []NvidiaHolder) string {
	if len(holders) == 0 {
		return "an external GPU client (the holding process could not be identified)"
	}
	parts := make([]string, 0, len(holders))
	for _, h := range holders {
		parts = append(parts, fmt.Sprintf("%s (pid %d)", h.Comm, h.PID))
	}
	return "external process(es): " + strings.Join(parts, ", ")
}

// nvidiaInUseRefusal builds the actionable error returned when a vfio switch is
// refused because a NON-charly process still holds the card. The arbiter
// auto-preempts charly-managed GPU pods BEFORE the switch, so the holder is
// external — point the operator at it. Force-unbind stays REFUSED (it would
// wedge the device_lock); this message only tells them what to close.
func nvidiaInUseRefusal(holders []NvidiaHolder) error {
	return fmt.Errorf("switch-to-vfio REFUSED: nvidia still held by %s. charly auto-preempts its own GPU pods; close these external GPU clients and retry (refusing to force-unbind — would wedge the device_lock)", formatNvidiaHolders(holders))
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
func deployNodeSharesGPU(node BundleNode, resources map[string]*ResourceDef) bool {
	for _, tok := range node.RequiredShared() {
		if rdef := resources[tok]; rdef != nil && rdef.Gpu != nil {
			return true
		}
	}
	return false
}

// nvidiaTokenFromResources returns the `resource:` token whose gpu selector
// matches the NVIDIA PCI vendor (0x10de) — the arbitration token that the
// auto-detected nvidia GPU device (engine.go's `--device nvidia.com/gpu=all`)
// maps onto. "" when no gpu-backed nvidia token is configured (then there is no
// arbitration label to claim). Lowest token name wins on the degenerate
// multi-match case (determinism). Derived from the embedded `resource:`
// vocabulary (charly/charly.yml) —
// never hardcoded; the vendor match mirrors requiredGPUResource (gpu_allocate.go,
// R3).
func nvidiaTokenFromResources(resources map[string]*ResourceDef) string {
	best := ""
	for tok, rdef := range resources {
		if rdef != nil && rdef.Gpu != nil && normalizePCIVendor(rdef.Gpu.Vendor) == nvidiaVendorID {
			if best == "" || tok < best {
				best = tok
			}
		}
	}
	return best
}

// nodeSecurityListsNvidiaDevice reports whether a node's security.devices
// explicitly references the NVIDIA GPU — the CDI name (`nvidia.com/gpu`) or a
// `/dev/nvidia*` node. A node-intrinsic "this deploy consumes the GPU" signal,
// independent of host auto-detection.
func nodeSecurityListsNvidiaDevice(node BundleNode) bool {
	if node.Security == nil {
		return false
	}
	for _, d := range node.Security.Devices {
		if strings.Contains(d, "nvidia.com/gpu") || strings.HasPrefix(d, "/dev/nvidia") {
			return true
		}
	}
	return false
}

// nodeConsumesNvidiaGPU reports whether a deploy node WOULD receive the nvidia
// GPU device at bring-up: either the host presents a usable nvidia GPU
// (DetectGPU — the SAME signal config_image uses to emit
// `--device nvidia.com/gpu=all` on every pod of a GPU host), or the node
// explicitly lists an nvidia device in security.devices. The card being usable
// via the nvidia driver is exactly the state in which an untracked GPU pod grabs
// `/dev/nvidia*` and blocks a later vfio claim — so it is the state in which the
// pod MUST become a tracked, preemptable shared claimant.
func nodeConsumesNvidiaGPU(node BundleNode) bool {
	return DetectGPU() || nodeSecurityListsNvidiaDevice(node)
}

// impliedGPUSharedToken returns the gpu-backed `resource:` token a node
// implicitly claims as SHARED because it consumes the auto-detected nvidia GPU
// device — "" when the node is not a GPU consumer, claims a resource
// exclusively, or no gpu token is configured. This is what makes EVERY
// GPU-using deployment a tracked, preemptable shared claimant with NO per-deploy
// config: AcquireShared registers a refcount lease for the token, and
// AcquireExclusive can then stop the pod to free the card for a vfio claimant.
// An EXCLUSIVE claimant (a VM via vfio passthrough — it gets a PCI <hostdev>,
// never the pod `--device`) is never treated as a pod GPU consumer.
func impliedGPUSharedToken(node BundleNode, resources map[string]*ResourceDef) string {
	if len(node.RequiredExclusive()) > 0 {
		return ""
	}
	if !nodeConsumesNvidiaGPU(node) {
		return ""
	}
	return nvidiaTokenFromResources(resources)
}

// applyImpliedGPUShared returns node with its RequiresShared unioned with the
// implied gpu token (impliedGPUSharedToken) — a no-op copy when nothing is
// implied OR the node already claims the token (no double-claim). Pure (resources
// injected) so it is unit-testable without touching disk.
func applyImpliedGPUShared(node BundleNode, resources map[string]*ResourceDef) BundleNode {
	tok := impliedGPUSharedToken(node, resources)
	if tok == "" || slices.Contains(node.RequiresShared, tok) {
		return node
	}
	// node is a value (passed by value to the arbiter); a fresh slice avoids
	// aliasing the caller's RequiresShared.
	node.RequiresShared = append(append([]string(nil), node.RequiresShared...), tok)
	return node
}

// withImpliedGPUShared is the disk-backed wrapper of applyImpliedGPUShared used
// at the single arbiter-claim entry point (acquireResourceForClaimant): it loads
// the project resource map and unions the implied gpu token onto node, so a
// GPU-consuming pod that declared NO explicit claim still acquires a shared lease
// and becomes preemptable by an exclusive claimant.
func withImpliedGPUShared(node BundleNode) BundleNode {
	return applyImpliedGPUShared(node, gatherResources())
}

// gpuSwitchModeTolerant detects the GPU matching `vendor` (PCI vendor hex, e.g.
// "0x10de") and flips its WHOLE IOMMU group to `mode` — TOLERANT of an absent
// card. This is the arbiter's switchMode hook (charly/preempt.go), used for
// BOTH directions, so it MUST keep a claim portable across GPU and no-GPU hosts:
//
//   - card present → flip (no-op if already in mode); a real flip failure errors
//     (errGPUSwitchWedged on a device_lock wedge → the arbiter poisons it).
//   - card ABSENT → skip with a note, NO error. For the vfio direction there is
//     nothing to free; for the nvidia direction a shared pod degrades to CPU-only.
//     Erroring here would break every requires_exclusive bed on a no-GPU host.
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
