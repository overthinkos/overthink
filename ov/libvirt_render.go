package main

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// VmRuntimeParams carries the runtime-resolved state that the renderers
// need but isn't in the author's VmSpec: the VM name, disk paths, SSH
// pubkey, host architecture, host CPU vendor, etc. Both libvirt (XML)
// and QEMU (argv) renderers consume the same struct so the "rendered
// from a common source" invariant is preserved.
type VmRuntimeParams struct {
	// Name is the libvirt domain name / QEMU process handle.
	// Convention: "ov-<vm-name>" or "ov-<vm-name>-<instance>".
	Name string

	// QCOW2Path is the absolute path to the VM's root qcow2 disk.
	QCOW2Path string

	// SeedISOPath is the absolute path to the NoCloud cidata ISO.
	// Empty → no cdrom attached (bootc source with cloud-init disabled).
	SeedISOPath string

	// NVRAMPath is the absolute path to the per-VM UEFI NVRAM file.
	// Empty → firmware: bios (no pflash drives emitted).
	NVRAMPath string

	// OVMFCodePath is the absolute path to the OVMF_CODE firmware image.
	// Required when Firmware == "uefi-*"; empty when bios.
	OVMFCodePath string

	// HostArch is the host architecture string (e.g. "x86_64", "aarch64").
	// Mirrors `uname -m` — pinned at VM define time; determines
	// <os><type arch='...'> and whether nested-virt features are added.
	HostArch string

	// HostCPUVendor is "GenuineIntel" | "AuthenticAMD" | "". Used by
	// D16 to auto-append +vmx or +svm to LibvirtCPU.Features when
	// cpu.mode defaults to host-passthrough and the user hasn't
	// explicitly disabled nested virt.
	HostCPUVendor string

	// SMBIOSCredentials are pre-formatted systemd-credential oemString
	// entries (e.g. "io.systemd.credential.binary:tmpfiles.extra=<b64>").
	// Only populated when D13 resolves SMBIOS channel enabled.
	SMBIOSCredentials []string

	// RamMB is the resolved RAM in MiB (VmSpec.Ram parsed).
	RamMB int

	// Cpus is the resolved vCPU count (VmSpec.Cpus with defaults applied).
	Cpus int

	// SshPort is the host port forwarded to guest :22.
	SshPort int

	// ExtraPortForwards are additional "host:guest" TCP forwards on
	// top of the SSH port. Used with user-mode networking.
	ExtraPortForwards []string
}

// RenderDomain produces the complete libvirt domain XML for a VM from
// a VmSpec plus runtime parameters. Pure function: no filesystem,
// network, or libvirt calls — just string assembly. The result is fed
// to virDomainDefineXML. Subsequent raw-snippet injection is handled
// by the separate InjectLibvirtXML pass.
//
// The canonical element order matches libvirt's own schema.
func RenderDomain(spec *VmSpec, rt VmRuntimeParams) string {
	var b strings.Builder

	b.WriteString("<domain type='kvm'>\n")
	fmt.Fprintf(&b, "  <name>%s</name>\n", escapeXML(rt.Name))
	fmt.Fprintf(&b, "  <memory unit='MiB'>%d</memory>\n", rt.RamMB)
	fmt.Fprintf(&b, "  <currentMemory unit='MiB'>%d</currentMemory>\n", rt.RamMB)
	fmt.Fprintf(&b, "  <vcpu placement='static'>%d</vcpu>\n", rt.Cpus)

	renderTopLevelTuning(&b, spec)
	renderOS(&b, spec, rt)
	renderFeatures(&b, spec)
	renderCPU(&b, spec, rt)
	renderClock(&b, spec)

	b.WriteString("  <on_poweroff>destroy</on_poweroff>\n")
	b.WriteString("  <on_reboot>restart</on_reboot>\n")
	b.WriteString("  <on_crash>destroy</on_crash>\n")

	renderDevices(&b, spec, rt)
	renderSecLabel(&b, spec)
	renderLaunchSecurity(&b, spec)
	renderSysInfo(&b, spec, rt)
	renderResource(&b, spec)

	b.WriteString("</domain>\n")
	return b.String()
}

// renderTopLevelTuning emits <iothreads>, <cputune>, <memtune>,
// <numatune>, and <memoryBacking> — all under <domain> directly.
func renderTopLevelTuning(b *strings.Builder, spec *VmSpec) {
	lv := spec.Libvirt
	if lv == nil {
		return
	}

	if lv.IOThreads > 0 {
		fmt.Fprintf(b, "  <iothreads>%d</iothreads>\n", lv.IOThreads)
	}

	if ct := lv.CPUTune; ct != nil {
		b.WriteString("  <cputune>\n")
		if ct.Shares > 0 {
			fmt.Fprintf(b, "    <shares>%d</shares>\n", ct.Shares)
		}
		if ct.Period > 0 {
			fmt.Fprintf(b, "    <period>%d</period>\n", ct.Period)
		}
		if ct.Quota > 0 {
			fmt.Fprintf(b, "    <quota>%d</quota>\n", ct.Quota)
		}
		if ct.GlobalPeriod > 0 {
			fmt.Fprintf(b, "    <global_period>%d</global_period>\n", ct.GlobalPeriod)
		}
		if ct.GlobalQuota > 0 {
			fmt.Fprintf(b, "    <global_quota>%d</global_quota>\n", ct.GlobalQuota)
		}
		if ct.EmulatorPeriod > 0 {
			fmt.Fprintf(b, "    <emulator_period>%d</emulator_period>\n", ct.EmulatorPeriod)
		}
		if ct.EmulatorQuota > 0 {
			fmt.Fprintf(b, "    <emulator_quota>%d</emulator_quota>\n", ct.EmulatorQuota)
		}
		if ct.IOThreadPeriod > 0 {
			fmt.Fprintf(b, "    <iothread_period>%d</iothread_period>\n", ct.IOThreadPeriod)
		}
		if ct.IOThreadQuota > 0 {
			fmt.Fprintf(b, "    <iothread_quota>%d</iothread_quota>\n", ct.IOThreadQuota)
		}
		for _, p := range ct.VCPUPin {
			fmt.Fprintf(b, "    <vcpupin vcpu='%d' cpuset='%s'/>\n", p.VCPU, escapeXMLAttr(p.CPUSet))
		}
		if ct.EmulatorPin != nil {
			fmt.Fprintf(b, "    <emulatorpin cpuset='%s'/>\n", escapeXMLAttr(ct.EmulatorPin.CPUSet))
		}
		for _, p := range ct.IOThreadPin {
			fmt.Fprintf(b, "    <iothreadpin iothread='%d' cpuset='%s'/>\n", p.IOThread, escapeXMLAttr(p.CPUSet))
		}
		b.WriteString("  </cputune>\n")
	}

	if mt := lv.MemTune; mt != nil {
		b.WriteString("  <memtune>\n")
		if mt.HardLimit != "" {
			fmt.Fprintf(b, "    <hard_limit unit='KiB'>%s</hard_limit>\n", escapeXML(sizeToKiB(mt.HardLimit)))
		}
		if mt.SoftLimit != "" {
			fmt.Fprintf(b, "    <soft_limit unit='KiB'>%s</soft_limit>\n", escapeXML(sizeToKiB(mt.SoftLimit)))
		}
		if mt.SwapHardLimit != "" {
			fmt.Fprintf(b, "    <swap_hard_limit unit='KiB'>%s</swap_hard_limit>\n", escapeXML(sizeToKiB(mt.SwapHardLimit)))
		}
		if mt.MinGuarantee != "" {
			fmt.Fprintf(b, "    <min_guarantee unit='KiB'>%s</min_guarantee>\n", escapeXML(sizeToKiB(mt.MinGuarantee)))
		}
		b.WriteString("  </memtune>\n")
	}

	if nt := lv.NUMATune; nt != nil {
		b.WriteString("  <numatune>\n")
		if nt.Memory != nil {
			fmt.Fprintf(b, "    <memory")
			if nt.Memory.Mode != "" {
				fmt.Fprintf(b, " mode='%s'", escapeXMLAttr(nt.Memory.Mode))
			}
			if nt.Memory.Nodeset != "" {
				fmt.Fprintf(b, " nodeset='%s'", escapeXMLAttr(nt.Memory.Nodeset))
			}
			if nt.Memory.Placement != "" {
				fmt.Fprintf(b, " placement='%s'", escapeXMLAttr(nt.Memory.Placement))
			}
			b.WriteString("/>\n")
		}
		for _, mn := range nt.MemNodes {
			fmt.Fprintf(b, "    <memnode cellid='%d' mode='%s' nodeset='%s'/>\n",
				mn.CellID, escapeXMLAttr(mn.Mode), escapeXMLAttr(mn.Nodeset))
		}
		b.WriteString("  </numatune>\n")
	}

	if mb := lv.MemoryBacking; mb != nil {
		b.WriteString("  <memoryBacking>\n")
		if mb.Hugepages != nil {
			b.WriteString("    <hugepages>\n")
			fmt.Fprintf(b, "      <page size='%s'", escapeXMLAttr(strings.TrimSuffix(mb.Hugepages.Size, "B")))
			if strings.HasSuffix(mb.Hugepages.Size, "M") || strings.HasSuffix(mb.Hugepages.Size, "m") {
				b.WriteString(" unit='M'")
			} else if strings.HasSuffix(mb.Hugepages.Size, "G") || strings.HasSuffix(mb.Hugepages.Size, "g") {
				b.WriteString(" unit='G'")
			}
			if mb.Hugepages.NodeSet != "" {
				fmt.Fprintf(b, " nodeset='%s'", escapeXMLAttr(mb.Hugepages.NodeSet))
			}
			b.WriteString("/>\n")
			b.WriteString("    </hugepages>\n")
		}
		if boolPtrTrue(mb.NoSharepages) {
			b.WriteString("    <nosharepages/>\n")
		}
		if boolPtrTrue(mb.Locked) {
			b.WriteString("    <locked/>\n")
		}
		if mb.Source != "" {
			fmt.Fprintf(b, "    <source type='%s'/>\n", escapeXMLAttr(mb.Source))
		}
		if mb.Access != "" {
			fmt.Fprintf(b, "    <access mode='%s'/>\n", escapeXMLAttr(mb.Access))
		}
		if mb.Allocation != "" {
			fmt.Fprintf(b, "    <allocation mode='%s'/>\n", escapeXMLAttr(mb.Allocation))
		}
		if boolPtrTrue(mb.Discard) {
			b.WriteString("    <discard/>\n")
		}
		b.WriteString("  </memoryBacking>\n")
	}
}

// renderOS emits the <os> element including D17 firmware plumbing.
func renderOS(b *strings.Builder, spec *VmSpec, rt VmRuntimeParams) {
	arch := rt.HostArch
	if arch == "" {
		arch = "x86_64"
	}
	machine := spec.Machine
	if machine == "" {
		machine = defaultMachineForArch(arch)
	}

	firmware := spec.Firmware
	if firmware == "" {
		firmware = "bios"
	}

	// Open <os> with firmware attribute when UEFI.
	switch firmware {
	case "uefi-insecure", "uefi-secure":
		b.WriteString("  <os firmware='efi'>\n")
	default:
		b.WriteString("  <os>\n")
	}

	fmt.Fprintf(b, "    <type arch='%s' machine='%s'>hvm</type>\n",
		escapeXMLAttr(arch), escapeXMLAttr(machine))

	// UEFI firmware descriptor: declare whether secure-boot should be
	// enabled. libvirt's firmware autoselection uses this to pick the
	// correct OVMF_CODE/OVMF_VARS pair.
	if firmware == "uefi-secure" || firmware == "uefi-insecure" {
		b.WriteString("    <firmware>\n")
		if firmware == "uefi-secure" {
			b.WriteString("      <feature enabled='yes' name='secure-boot'/>\n")
			b.WriteString("      <feature enabled='yes' name='enrolled-keys'/>\n")
		} else {
			b.WriteString("      <feature enabled='no' name='secure-boot'/>\n")
		}
		b.WriteString("    </firmware>\n")

		// Explicit loader + nvram paths when runtime provides them
		// (QEMU backend always does; libvirt backend may rely on
		// autoselection and leave these empty).
		if rt.OVMFCodePath != "" {
			secureAttr := "no"
			if firmware == "uefi-secure" {
				secureAttr = "yes"
			}
			fmt.Fprintf(b, "    <loader readonly='yes' secure='%s' type='pflash'>%s</loader>\n",
				secureAttr, escapeXML(rt.OVMFCodePath))
		}
		if rt.NVRAMPath != "" {
			fmt.Fprintf(b, "    <nvram>%s</nvram>\n", escapeXML(rt.NVRAMPath))
		}
	}

	b.WriteString("    <boot dev='hd'/>\n")
	b.WriteString("  </os>\n")
}

// renderFeatures emits <features> with D14 hypervisor toggles.
// Defaults: ACPI=true, APIC=true, SMM only if explicitly set.
func renderFeatures(b *strings.Builder, spec *VmSpec) {
	f := emptyFeatures()
	if spec.Libvirt != nil && spec.Libvirt.Features != nil {
		f = spec.Libvirt.Features
	}
	b.WriteString("  <features>\n")
	if boolPtrDefaultTrue(f.ACPI) {
		b.WriteString("    <acpi/>\n")
	}
	if boolPtrDefaultTrue(f.APIC) {
		b.WriteString("    <apic/>\n")
	}
	if boolPtrTrue(f.PAE) {
		b.WriteString("    <pae/>\n")
	}
	if boolPtrTrue(f.SMM) {
		b.WriteString("    <smm state='on'/>\n")
	}
	if boolPtrTrue(f.HAP) {
		b.WriteString("    <hap/>\n")
	}
	if boolPtrTrue(f.VMPort) {
		b.WriteString("    <vmport state='on'/>\n")
	}
	if boolPtrTrue(f.PMU) {
		b.WriteString("    <pmu state='on'/>\n")
	}
	if f.IBS != "" {
		fmt.Fprintf(b, "    <ibs value='%s'/>\n", escapeXMLAttr(f.IBS))
	}
	if f.HyperV != nil {
		renderHyperV(b, f.HyperV)
	}
	if f.KVM != nil {
		renderKVM(b, f.KVM)
	}
	b.WriteString("  </features>\n")
}

func renderHyperV(b *strings.Builder, h *LibvirtHyperV) {
	b.WriteString("    <hyperv>\n")
	if h.Relaxed != "" {
		fmt.Fprintf(b, "      <relaxed state='%s'/>\n", escapeXMLAttr(h.Relaxed))
	}
	if h.VAPIC != "" {
		fmt.Fprintf(b, "      <vapic state='%s'/>\n", escapeXMLAttr(h.VAPIC))
	}
	if h.Spinlocks != nil {
		fmt.Fprintf(b, "      <spinlocks state='%s'", escapeXMLAttr(h.Spinlocks.State))
		if h.Spinlocks.Retries > 0 {
			fmt.Fprintf(b, " retries='%d'", h.Spinlocks.Retries)
		}
		b.WriteString("/>\n")
	}
	if h.VPIndex != "" {
		fmt.Fprintf(b, "      <vpindex state='%s'/>\n", escapeXMLAttr(h.VPIndex))
	}
	if h.Runtime != "" {
		fmt.Fprintf(b, "      <runtime state='%s'/>\n", escapeXMLAttr(h.Runtime))
	}
	if h.Synic != "" {
		fmt.Fprintf(b, "      <synic state='%s'/>\n", escapeXMLAttr(h.Synic))
	}
	if h.STimer != "" {
		fmt.Fprintf(b, "      <stimer state='%s'/>\n", escapeXMLAttr(h.STimer))
	}
	if h.Reset != "" {
		fmt.Fprintf(b, "      <reset state='%s'/>\n", escapeXMLAttr(h.Reset))
	}
	if h.VendorID != nil && h.VendorID.State != "" {
		fmt.Fprintf(b, "      <vendor_id state='%s'", escapeXMLAttr(h.VendorID.State))
		if h.VendorID.Value != "" {
			fmt.Fprintf(b, " value='%s'", escapeXMLAttr(h.VendorID.Value))
		}
		b.WriteString("/>\n")
	}
	if h.Frequencies != "" {
		fmt.Fprintf(b, "      <frequencies state='%s'/>\n", escapeXMLAttr(h.Frequencies))
	}
	if h.Reenlightenment != "" {
		fmt.Fprintf(b, "      <reenlightenment state='%s'/>\n", escapeXMLAttr(h.Reenlightenment))
	}
	if h.TLBFlush != "" {
		fmt.Fprintf(b, "      <tlbflush state='%s'/>\n", escapeXMLAttr(h.TLBFlush))
	}
	if h.IPI != "" {
		fmt.Fprintf(b, "      <ipi state='%s'/>\n", escapeXMLAttr(h.IPI))
	}
	if h.EVMCS != "" {
		fmt.Fprintf(b, "      <evmcs state='%s'/>\n", escapeXMLAttr(h.EVMCS))
	}
	b.WriteString("    </hyperv>\n")
}

func renderKVM(b *strings.Builder, k *LibvirtKVM) {
	b.WriteString("    <kvm>\n")
	if k.Hidden != "" {
		fmt.Fprintf(b, "      <hidden state='%s'/>\n", escapeXMLAttr(k.Hidden))
	}
	if k.HintDedicated != "" {
		fmt.Fprintf(b, "      <hint-dedicated state='%s'/>\n", escapeXMLAttr(k.HintDedicated))
	}
	if k.PollControl != "" {
		fmt.Fprintf(b, "      <poll-control state='%s'/>\n", escapeXMLAttr(k.PollControl))
	}
	if k.PVIPI != "" {
		fmt.Fprintf(b, "      <pv-ipi state='%s'/>\n", escapeXMLAttr(k.PVIPI))
	}
	if k.DirtyRingSize > 0 {
		fmt.Fprintf(b, "      <dirty-ring size='%d'/>\n", k.DirtyRingSize)
	}
	b.WriteString("    </kvm>\n")
}

// renderCPU emits <cpu> with D16 defaults: host-passthrough + nested
// virtualization feature (vmx on Intel, svm on AMD) auto-appended
// unless the user explicitly disables it.
func renderCPU(b *strings.Builder, spec *VmSpec, rt VmRuntimeParams) {
	cpu := resolveCPUDefaults(spec, rt)
	fmt.Fprintf(b, "  <cpu mode='%s'", escapeXMLAttr(cpu.Mode))
	if cpu.Check != "" {
		fmt.Fprintf(b, " check='%s'", escapeXMLAttr(cpu.Check))
	}
	if cpu.Migratable != "" {
		fmt.Fprintf(b, " migratable='%s'", escapeXMLAttr(cpu.Migratable))
	}
	// Self-closing form when no child elements.
	if cpu.Model == "" && cpu.Topology == nil && len(cpu.Features) == 0 && cpu.Cache == nil && len(cpu.NUMA) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	if cpu.Model != "" {
		fmt.Fprintf(b, "    <model>%s</model>\n", escapeXML(cpu.Model))
	}
	if t := cpu.Topology; t != nil {
		b.WriteString("    <topology")
		if t.Sockets > 0 {
			fmt.Fprintf(b, " sockets='%d'", t.Sockets)
		}
		if t.Dies > 0 {
			fmt.Fprintf(b, " dies='%d'", t.Dies)
		}
		if t.Cores > 0 {
			fmt.Fprintf(b, " cores='%d'", t.Cores)
		}
		if t.Threads > 0 {
			fmt.Fprintf(b, " threads='%d'", t.Threads)
		}
		b.WriteString("/>\n")
	}
	for _, f := range cpu.Features {
		fmt.Fprintf(b, "    <feature policy='%s' name='%s'/>\n",
			escapeXMLAttr(f.Policy), escapeXMLAttr(f.Name))
	}
	if c := cpu.Cache; c != nil {
		fmt.Fprintf(b, "    <cache")
		if c.Level > 0 {
			fmt.Fprintf(b, " level='%d'", c.Level)
		}
		if c.Mode != "" {
			fmt.Fprintf(b, " mode='%s'", escapeXMLAttr(c.Mode))
		}
		b.WriteString("/>\n")
	}
	if len(cpu.NUMA) > 0 {
		b.WriteString("    <numa>\n")
		for _, n := range cpu.NUMA {
			fmt.Fprintf(b, "      <cell id='%d'", n.ID)
			if n.CPUs != "" {
				fmt.Fprintf(b, " cpus='%s'", escapeXMLAttr(n.CPUs))
			}
			if n.Memory != "" {
				fmt.Fprintf(b, " memory='%s'", escapeXMLAttr(n.Memory))
			}
			if n.Unit != "" {
				fmt.Fprintf(b, " unit='%s'", escapeXMLAttr(n.Unit))
			}
			if n.MemAccess != "" {
				fmt.Fprintf(b, " memAccess='%s'", escapeXMLAttr(n.MemAccess))
			}
			b.WriteString("/>\n")
		}
		b.WriteString("    </numa>\n")
	}
	b.WriteString("  </cpu>\n")
}

// renderClock emits <clock>.
func renderClock(b *strings.Builder, spec *VmSpec) {
	c := &LibvirtClock{Offset: "utc"}
	if spec.Libvirt != nil && spec.Libvirt.Clock != nil {
		c = spec.Libvirt.Clock
		if c.Offset == "" {
			c.Offset = "utc"
		}
	}
	fmt.Fprintf(b, "  <clock offset='%s'", escapeXMLAttr(c.Offset))
	if c.Timezone != "" {
		fmt.Fprintf(b, " timezone='%s'", escapeXMLAttr(c.Timezone))
	}
	if c.Adjustment != "" {
		fmt.Fprintf(b, " adjustment='%s'", escapeXMLAttr(c.Adjustment))
	}
	if c.Basis != "" {
		fmt.Fprintf(b, " basis='%s'", escapeXMLAttr(c.Basis))
	}
	if len(c.Timers) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	for _, t := range c.Timers {
		fmt.Fprintf(b, "    <timer name='%s'", escapeXMLAttr(t.Name))
		if t.Present != "" {
			fmt.Fprintf(b, " present='%s'", escapeXMLAttr(t.Present))
		}
		if t.Track != "" {
			fmt.Fprintf(b, " track='%s'", escapeXMLAttr(t.Track))
		}
		if t.TickPolicy != "" {
			fmt.Fprintf(b, " tickpolicy='%s'", escapeXMLAttr(t.TickPolicy))
		}
		if t.Frequency > 0 {
			fmt.Fprintf(b, " frequency='%d'", t.Frequency)
		}
		if t.Mode != "" {
			fmt.Fprintf(b, " mode='%s'", escapeXMLAttr(t.Mode))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("  </clock>\n")
}

// renderSecLabel emits <seclabel>.
func renderSecLabel(b *strings.Builder, spec *VmSpec) {
	if spec.Libvirt == nil || spec.Libvirt.SecLabel == nil {
		return
	}
	s := spec.Libvirt.SecLabel
	fmt.Fprintf(b, "  <seclabel")
	if s.Type != "" {
		fmt.Fprintf(b, " type='%s'", escapeXMLAttr(s.Type))
	}
	if s.Model != "" {
		fmt.Fprintf(b, " model='%s'", escapeXMLAttr(s.Model))
	}
	if s.Relabel != "" {
		fmt.Fprintf(b, " relabel='%s'", escapeXMLAttr(s.Relabel))
	}
	if s.Label == "" && s.BaseLabel == "" && s.ImageLabel == "" {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	if s.Label != "" {
		fmt.Fprintf(b, "    <label>%s</label>\n", escapeXML(s.Label))
	}
	if s.BaseLabel != "" {
		fmt.Fprintf(b, "    <baselabel>%s</baselabel>\n", escapeXML(s.BaseLabel))
	}
	if s.ImageLabel != "" {
		fmt.Fprintf(b, "    <imagelabel>%s</imagelabel>\n", escapeXML(s.ImageLabel))
	}
	b.WriteString("  </seclabel>\n")
}

// renderLaunchSecurity emits <launchSecurity> for confidential VMs (SEV/TDX).
func renderLaunchSecurity(b *strings.Builder, spec *VmSpec) {
	if spec.Libvirt == nil || spec.Libvirt.LaunchSecurity == nil {
		return
	}
	ls := spec.Libvirt.LaunchSecurity
	if ls.Type == "" {
		return
	}
	fmt.Fprintf(b, "  <launchSecurity type='%s'>\n", escapeXMLAttr(ls.Type))
	if ls.CBitPos > 0 {
		fmt.Fprintf(b, "    <cbitpos>%d</cbitpos>\n", ls.CBitPos)
	}
	if ls.ReducedPhysBits > 0 {
		fmt.Fprintf(b, "    <reducedPhysBits>%d</reducedPhysBits>\n", ls.ReducedPhysBits)
	}
	if ls.Policy != "" {
		fmt.Fprintf(b, "    <policy>%s</policy>\n", escapeXML(ls.Policy))
	}
	if ls.DhCert != "" {
		fmt.Fprintf(b, "    <dhCert>%s</dhCert>\n", escapeXML(ls.DhCert))
	}
	if ls.Session != "" {
		fmt.Fprintf(b, "    <session>%s</session>\n", escapeXML(ls.Session))
	}
	if ls.KernelHashes != "" {
		fmt.Fprintf(b, "    <kernelHashes>%s</kernelHashes>\n", escapeXML(ls.KernelHashes))
	}
	b.WriteString("  </launchSecurity>\n")
}

// renderSysInfo emits <sysinfo>. Auto-injects SMBIOS oemStrings from
// RuntimeParams.SMBIOSCredentials when D13 resolves SMBIOS-channel-enabled,
// merging with any user-declared entries.
func renderSysInfo(b *strings.Builder, spec *VmSpec, rt VmRuntimeParams) {
	var userSys *LibvirtSysInfo
	if spec.Libvirt != nil {
		userSys = spec.Libvirt.SysInfo
	}

	// Compose final oemStrings = runtime SMBIOS creds + user-declared OEM strings.
	var oem []string
	oem = append(oem, rt.SMBIOSCredentials...)
	if userSys != nil {
		oem = append(oem, userSys.OEMStrings...)
	}

	hasContent := len(oem) > 0 ||
		(userSys != nil && (len(userSys.BIOS) > 0 || len(userSys.System) > 0 ||
			len(userSys.BaseBoard) > 0 || len(userSys.Chassis) > 0 || len(userSys.Processor) > 0))
	if !hasContent {
		return
	}

	sysType := "smbios"
	if userSys != nil && userSys.Type != "" {
		sysType = userSys.Type
	}
	fmt.Fprintf(b, "  <sysinfo type='%s'>\n", escapeXMLAttr(sysType))

	if userSys != nil {
		renderSysInfoEntrySection(b, "bios", userSys.BIOS)
		renderSysInfoEntrySection(b, "system", userSys.System)
		for _, bb := range userSys.BaseBoard {
			renderSysInfoEntrySection(b, "baseBoard", bb)
		}
		renderSysInfoEntrySection(b, "chassis", userSys.Chassis)
		for _, pr := range userSys.Processor {
			renderSysInfoEntrySection(b, "processor", pr)
		}
	}

	if len(oem) > 0 {
		b.WriteString("    <oemStrings>\n")
		for _, s := range oem {
			fmt.Fprintf(b, "      <entry>%s</entry>\n", escapeXML(s))
		}
		b.WriteString("    </oemStrings>\n")
	}

	b.WriteString("  </sysinfo>\n")
}

func renderSysInfoEntrySection(b *strings.Builder, name string, entries map[string]string) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintf(b, "    <%s>\n", name)
	for k, v := range entries {
		fmt.Fprintf(b, "      <entry name='%s'>%s</entry>\n", escapeXMLAttr(k), escapeXML(v))
	}
	fmt.Fprintf(b, "    </%s>\n", name)
}

// renderResource emits <resource>.
func renderResource(b *strings.Builder, spec *VmSpec) {
	if spec.Libvirt == nil || spec.Libvirt.Resource == nil {
		return
	}
	r := spec.Libvirt.Resource
	if r.Partition == "" && len(r.FibreChannel) == 0 {
		return
	}
	b.WriteString("  <resource>\n")
	if r.Partition != "" {
		fmt.Fprintf(b, "    <partition>%s</partition>\n", escapeXML(r.Partition))
	}
	if len(r.FibreChannel) > 0 {
		b.WriteString("    <fibrechannel")
		for k, v := range r.FibreChannel {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("  </resource>\n")
}

// --- Helpers ---

// resolveCPUDefaults applies D16: host-passthrough + nested-virt feature
// auto-append. Returns a COPY so the caller's spec isn't mutated.
func resolveCPUDefaults(spec *VmSpec, rt VmRuntimeParams) LibvirtCPU {
	var src LibvirtCPU
	if spec.Libvirt != nil && spec.Libvirt.CPU != nil {
		src = *spec.Libvirt.CPU
	}
	if src.Mode == "" {
		src.Mode = "host-passthrough"
	}
	if src.Check == "" {
		src.Check = "none"
	}

	// Nested-virt auto-append: only when mode == host-passthrough AND
	// user hasn't already declared vmx/svm (with any policy).
	if src.Mode == "host-passthrough" {
		nested := nestedFeatureForVendor(rt.HostCPUVendor)
		if nested != "" && !hasCPUFeature(src.Features, nested) {
			src.Features = append(src.Features, LibvirtCPUFeature{
				Policy: "require",
				Name:   nested,
			})
		}
	}
	return src
}

func nestedFeatureForVendor(vendor string) string {
	switch vendor {
	case "GenuineIntel":
		return "vmx"
	case "AuthenticAMD":
		return "svm"
	}
	return ""
}

func hasCPUFeature(features []LibvirtCPUFeature, name string) bool {
	for _, f := range features {
		if f.Name == name {
			return true
		}
	}
	return false
}

func emptyFeatures() *LibvirtFeatures {
	return &LibvirtFeatures{}
}

func defaultMachineForArch(arch string) string {
	switch arch {
	case "x86_64", "amd64":
		return "q35"
	case "aarch64", "arm64":
		return "virt"
	case "riscv64":
		return "virt"
	default:
		return "q35"
	}
}

// escapeXML escapes a string for use as XML text content.
func escapeXML(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

// escapeXMLAttr is a lenient attribute-value escaper covering the
// characters that break single-quoted attributes. xml.EscapeText is
// text-content focused; for attribute values we also escape the single
// quote.
func escapeXMLAttr(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"'", "&apos;",
		"\"", "&quot;",
	)
	return r.Replace(s)
}

func boolPtrTrue(p *bool) bool {
	return p != nil && *p
}

// boolPtrDefaultTrue returns true when nil (default enabled) OR *p is true.
func boolPtrDefaultTrue(p *bool) bool {
	return p == nil || *p
}

// sizeToKiB converts a size string like "8G", "2048M", "512K" to the
// numeric kilobyte quantity used in libvirt <*_limit unit='KiB'>...
func sizeToKiB(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	mult := int64(1)
	last := s[len(s)-1]
	num := s
	switch last {
	case 'T', 't':
		mult = 1024 * 1024 * 1024
		num = s[:len(s)-1]
	case 'G', 'g':
		mult = 1024 * 1024
		num = s[:len(s)-1]
	case 'M', 'm':
		mult = 1024
		num = s[:len(s)-1]
	case 'K', 'k':
		mult = 1
		num = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return "0"
	}
	return strconv.FormatInt(n*mult, 10)
}
