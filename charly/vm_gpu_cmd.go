package main

import (
	"fmt"
	"os"
	"strings"
)

// VmGpuCmd groups the host-side VFIO/GPU-passthrough inspection + mode verbs.
type VmGpuCmd struct {
	Status VmGpuStatusCmd `cmd:"" help:"Report host IOMMU readiness for GPU passthrough"`
	List   VmGpuListCmd   `cmd:"" help:"List passthrough-capable GPUs and emit a ready-to-paste hostdevs block"`
	Mode   VmGpuModeCmd   `cmd:"" help:"Show or set the GPU driver mode: vfio (VM passthrough) | nvidia (shared CDI pods)"`
}

// VmGpuModeCmd shows or sets the host GPU driver mode for a vendor-matched card.
// This is the manual operator interface for the vfio<->nvidia switch (the
// charly-CLI way, vs ad-hoc sysfs). The arbiter (charly/preempt.go) flips
// automatically for requires_exclusive (vfio) / requires_shared (nvidia)
// claims; this verb is the explicit override + inspector. It does NOT consult
// the arbiter ledger — a manual flip while an exclusive lease is active is the
// operator's call.
type VmGpuModeCmd struct {
	Mode   string `arg:"" optional:"" help:"Target mode: vfio (passthrough) | nvidia (CDI pods). Omit to SHOW the current mode."`
	Vendor string `long:"vendor" default:"0x10de" help:"PCI vendor of the GPU to switch (default 0x10de = NVIDIA)."`
}

func (c *VmGpuModeCmd) Run() error {
	if c.Mode != "" && c.Mode != gpuModeVfio && c.Mode != gpuModeNvidia {
		return fmt.Errorf("invalid mode %q — want %q or %q (or omit to show the current mode)", c.Mode, gpuModeVfio, gpuModeNvidia)
	}
	gpu, found := selectGPUByVendor(DetectVFIO(), c.Vendor)
	if !found {
		return fmt.Errorf("no GPU matching vendor %s on this host — check `charly vm gpu status`", normalizePCIVendor(c.Vendor))
	}
	cur := currentGPUMode(gpu)
	ids := trim0x(gpu.VendorID) + ":" + trim0x(gpu.DeviceID)
	if c.Mode == "" {
		fmt.Printf("%s (%s) GPU driver mode: %s\n", gpu.Addr, ids, cur)
		return nil
	}
	if cur == c.Mode {
		fmt.Printf("%s (%s) already in %s mode\n", gpu.Addr, ids, c.Mode)
		return nil
	}
	if err := switchGPUDriverMode(gpu, c.Mode); err != nil {
		return err
	}
	if c.Mode == gpuModeNvidia {
		ensureCDIRoot() // /etc/cdi is root-owned — generate the CDI spec as root
	}
	fmt.Printf("%s (%s) switched %s -> %s\n", gpu.Addr, ids, cur, c.Mode)
	return nil
}

// VmGpuStatusCmd reports whether the host is configured for VFIO passthrough.
type VmGpuStatusCmd struct{}

func (c *VmGpuStatusCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	rep := DetectVFIO()
	fmt.Println("VFIO / GPU passthrough status")
	fmt.Println()

	if rep.IOMMUEnabled {
		kind := rep.IOMMUKind
		if kind == "" {
			kind = "unknown vendor"
		}
		fmt.Printf("  IOMMU:           enabled (%s)\n", kind)
	} else {
		fmt.Println("  IOMMU:           DISABLED — add intel_iommu=on (Intel) or amd_iommu=on (AMD)")
		fmt.Println("                   plus iommu=pt to the kernel cmdline, then reboot.")
	}

	if vfioPciAvailable() {
		fmt.Println("  vfio-pci driver: available")
	} else {
		fmt.Println("  vfio-pci driver: NOT loaded — `sudo modprobe vfio-pci` (libvirt managed='yes' loads it on VM start)")
	}

	// memlock: VFIO pins all guest RAM, so a rootless qemu:///session needs a
	// memlock limit >= guest RAM. The login-session default (often 8 MiB) is
	// far too low and produces a cryptic "cannot limit locked memory" failure.
	_, hard := MemlockLimitBytes()
	switch {
	case memlockUnlimited(hard):
		fmt.Println("  memlock limit:   unlimited")
	case hard >= 16<<30:
		fmt.Printf("  memlock limit:   %d MiB (ok)\n", hard>>20)
	default:
		fmt.Printf("  memlock limit:   %d MiB — TOO LOW for passthrough (needs >= guest RAM).\n", hard>>20)
		fmt.Println("                   Raise it for the libvirt session, e.g. add to /etc/security/limits.d:")
		fmt.Println("                       <user> hard memlock unlimited")
		fmt.Println("                   then re-login, or `sudo prlimit --pid $(pgrep -x virtqemud) --memlock=unlimited`.")
	}

	fmt.Println()
	if len(rep.GPUs) == 0 {
		fmt.Println("  GPUs: none detected")
		return nil
	}
	fmt.Println("  GPUs:")
	for _, g := range rep.GPUs {
		grp := "no IOMMU group"
		access := ""
		if g.IOMMUGroup >= 0 {
			grp = fmt.Sprintf("group %d (%d device(s))", g.IOMMUGroup, len(g.GroupMembers))
			if VfioGroupAccessible(g.IOMMUGroup) {
				access = "  /dev/vfio: rw"
			} else {
				access = fmt.Sprintf("  /dev/vfio/%d: NO ACCESS (run `charly udev install`)", g.IOMMUGroup)
			}
		}
		drv := g.Driver
		if drv == "" {
			drv = "unbound"
		}
		fmt.Printf("    %s  %s:%s  %s  driver=%s  %s%s\n",
			g.Addr, trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel, drv, grp, access)
	}
	return nil
}

// VmGpuListCmd lists each GPU plus a ready-to-paste hostdevs YAML block.
type VmGpuListCmd struct{}

func (c *VmGpuListCmd) Run() error { //nolint:unparam // error return kept for interface/API stability
	rep := DetectVFIO()
	if len(rep.GPUs) == 0 {
		fmt.Println("No GPUs detected under /sys/bus/pci/devices.")
		return nil
	}
	if !rep.IOMMUEnabled {
		fmt.Fprintln(os.Stderr, "warning: IOMMU is not enabled — the hostdevs block below will not work until")
		fmt.Fprintln(os.Stderr, "         you enable IOMMU (intel_iommu=on / amd_iommu=on iommu=pt) and reboot.")
		fmt.Fprintln(os.Stderr)
	}

	for i, g := range rep.GPUs {
		fmt.Printf("# GPU %d: %s  %s:%s  %s\n", i, g.Addr, trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel)
		if g.IOMMUGroup >= 0 {
			fmt.Printf("#   IOMMU group %d — every function below must be passed through together:\n", g.IOMMUGroup)
		}
		for _, m := range g.GroupMembers {
			drv := m.Driver
			if drv == "" {
				drv = "unbound"
			}
			fmt.Printf("#     %s  %s  driver=%s\n", m.Addr, m.ClassLabel, drv)
		}
		fmt.Print(renderHostdevsBlock(g.GroupMembers))
		fmt.Println()
	}

	fmt.Println("# Paste the block under the kind:vm entity, e.g.:")
	fmt.Println("#   vm:")
	fmt.Println("#     my-gpu-vm:")
	fmt.Println("#       firmware: uefi-insecure   # passthrough wants UEFI")
	fmt.Println("#       libvirt:")
	fmt.Println("#         devices:")
	fmt.Println("#           <hostdevs block here>")
	return nil
}

// renderHostdevsBlock emits a `hostdevs:` YAML fragment (managed='yes') covering
// every device in the IOMMU group. managed='yes' lets libvirt auto-bind each
// function to vfio-pci on VM start and re-bind the host driver on stop. It is a
// text view over vfioGpuToHostdevs (gpu_allocate.go) — the SINGLE builder of
// which devices + PCI fields — so this `charly vm gpu list` output and the
// create-time auto-allocation can never diverge (R3).
func renderHostdevsBlock(members []VFIOPCIDevice) string {
	var b strings.Builder
	b.WriteString("hostdevs:\n")
	for _, h := range vfioGpuToHostdevs(members) {
		b.WriteString("  - type: pci\n")
		b.WriteString("    managed: \"yes\"\n")
		b.WriteString("    source:\n")
		fmt.Fprintf(&b, "      domain: %q\n", h.Source["domain"])
		fmt.Fprintf(&b, "      bus: %q\n", h.Source["bus"])
		fmt.Fprintf(&b, "      slot: %q\n", h.Source["slot"])
		fmt.Fprintf(&b, "      function: %q\n", h.Source["function"])
	}
	return b.String()
}

// parsePCIAddr splits "0000:01:00.0" into libvirt-form hex fields.
func parsePCIAddr(addr string) (domain, bus, slot, function string, ok bool) {
	// DDDD:BB:SS.F
	colon := strings.Split(addr, ":")
	if len(colon) != 3 {
		return "", "", "", "", false
	}
	dot := strings.SplitN(colon[2], ".", 2)
	if len(dot) != 2 {
		return "", "", "", "", false
	}
	return "0x" + colon[0], "0x" + colon[1], "0x" + dot[0], "0x" + dot[1], true
}

// trim0x drops a leading 0x for compact "vendor:device" display.
func trim0x(s string) string { return strings.TrimPrefix(s, "0x") }

// vfioPciAvailable reports whether the vfio-pci driver is present on the host.
func vfioPciAvailable() bool {
	for _, p := range []string{"/sys/bus/pci/drivers/vfio-pci", "/sys/module/vfio_pci"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
