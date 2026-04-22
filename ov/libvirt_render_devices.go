package main

import (
	"fmt"
	"strings"
)

// renderDevices emits the full <devices> block: auto-synthesized root
// disk + SSH interface from runtime params, plus every structured
// device type from spec.Libvirt.Devices. Canonical libvirt schema
// ordering isn't strict, but for readability we group by type.
func renderDevices(b *strings.Builder, spec *VmSpec, rt VmRuntimeParams) {
	b.WriteString("  <devices>\n")

	// Optional: emulator override
	if spec.Libvirt != nil && spec.Libvirt.Devices != nil && spec.Libvirt.Devices.Emulator != "" {
		fmt.Fprintf(b, "    <emulator>%s</emulator>\n", escapeXML(spec.Libvirt.Devices.Emulator))
	}

	// --- Auto-synthesized root disk ---
	if rt.QCOW2Path != "" {
		b.WriteString("    <disk type='file' device='disk'>\n")
		b.WriteString("      <driver name='qemu' type='qcow2'/>\n")
		fmt.Fprintf(b, "      <source file='%s'/>\n", escapeXML(rt.QCOW2Path))
		b.WriteString("      <target dev='vda' bus='virtio'/>\n")
		b.WriteString("    </disk>\n")
	}

	// --- Auto-synthesized seed ISO cdrom (D5) ---
	if rt.SeedISOPath != "" {
		b.WriteString("    <disk type='file' device='cdrom'>\n")
		b.WriteString("      <driver name='qemu' type='raw'/>\n")
		fmt.Fprintf(b, "      <source file='%s'/>\n", escapeXML(rt.SeedISOPath))
		b.WriteString("      <target dev='sda' bus='sata'/>\n")
		b.WriteString("      <readonly/>\n")
		b.WriteString("    </disk>\n")
	}

	// --- Additional disks ---
	if spec.Libvirt != nil && spec.Libvirt.Devices != nil {
		for _, d := range spec.Libvirt.Devices.Disks {
			renderDisk(b, d)
		}
	}

	// --- Auto-synthesized default interface (user-mode with SSH fwd) ---
	renderDefaultInterface(b, spec, rt)

	// --- Additional interfaces ---
	if spec.Libvirt != nil && spec.Libvirt.Devices != nil {
		for _, iface := range spec.Libvirt.Devices.Interfaces {
			renderInterface(b, iface)
		}
	}

	// --- Auto-synthesized serial console (so `ov vm console` works) ---
	b.WriteString("    <serial type='pty'><target port='0'/></serial>\n")
	b.WriteString("    <console type='pty'><target type='serial' port='0'/></console>\n")

	if spec.Libvirt != nil && spec.Libvirt.Devices != nil {
		d := spec.Libvirt.Devices

		for _, ch := range d.Channels {
			renderChannel(b, ch)
		}
		for _, s := range d.Serial {
			renderSerialChar(b, s)
		}
		for _, c := range d.Console {
			renderConsoleChar(b, c)
		}
		for _, p := range d.Parallel {
			renderParallel(b, p)
		}
		for _, g := range d.Graphics {
			renderGraphics(b, g)
		}
		for _, v := range d.Video {
			renderVideo(b, v)
		}
		for _, a := range d.Audio {
			renderAudio(b, a)
		}
		for _, s := range d.Sound {
			renderSound(b, s)
		}
		for _, i := range d.Inputs {
			renderInput(b, i)
		}
		for _, u := range d.USB {
			renderUSB(b, u)
		}
		for _, r := range d.RedirDev {
			renderRedirDev(b, r)
		}
		for _, h := range d.Hostdevs {
			renderHostdev(b, h)
		}
		for _, f := range d.Filesystems {
			renderFilesystem(b, f)
		}
		for _, r := range d.RNG {
			renderRNG(b, r)
		}
		for _, t := range d.TPM {
			renderTPM(b, t)
		}
		for _, w := range d.Watchdog {
			renderWatchdog(b, w)
		}
		if d.MemBalloon != nil {
			renderMemBalloon(b, d.MemBalloon)
		}
		for _, s := range d.Shmem {
			renderShmem(b, s)
		}
		if d.IOMMU != nil {
			renderIOMMU(b, d.IOMMU)
		}
		if d.Vsock != nil {
			renderVsock(b, d.Vsock)
		}
		for _, p := range d.Panic {
			renderPanic(b, p)
		}
		for _, sc := range d.Smartcard {
			renderSmartcard(b, sc)
		}
		for _, h := range d.Hub {
			renderHub(b, h)
		}
	}

	b.WriteString("  </devices>\n")
}

// renderDefaultInterface synthesizes the <interface> element from
// VmSpec.Network. Auto-adds the SSH hostfwd from VmRuntimeParams.SshPort
// plus any ExtraPortForwards.
func renderDefaultInterface(b *strings.Builder, spec *VmSpec, rt VmRuntimeParams) {
	net := &VmNetwork{Mode: "user", Model: "virtio-net-pci"}
	if spec.Network != nil {
		net = spec.Network
		if net.Mode == "" {
			net.Mode = "user"
		}
		if net.Model == "" {
			net.Model = "virtio-net-pci"
		}
	}

	switch net.Mode {
	case "bridge":
		b.WriteString("    <interface type='bridge'>\n")
		if net.Bridge != "" {
			fmt.Fprintf(b, "      <source bridge='%s'/>\n", escapeXMLAttr(net.Bridge))
		}
	case "nat", "network":
		b.WriteString("    <interface type='network'>\n")
		source := net.Bridge
		if source == "" {
			source = "default"
		}
		fmt.Fprintf(b, "      <source network='%s'/>\n", escapeXMLAttr(source))
	default: // user
		b.WriteString("    <interface type='user'>\n")
	}

	fmt.Fprintf(b, "      <model type='%s'/>\n", escapeXMLAttr(net.Model))
	if net.MAC != "" {
		fmt.Fprintf(b, "      <mac address='%s'/>\n", escapeXMLAttr(net.MAC))
	}

	// Port forwards (user-mode only in libvirt's schema).
	if net.Mode == "user" || net.Mode == "" {
		b.WriteString("      <portForward proto='tcp'>\n")
		if rt.SshPort > 0 {
			fmt.Fprintf(b, "        <range start='22' to='%d'/>\n", rt.SshPort)
		}
		for _, pf := range net.PortForwards {
			host, guest := splitPortForward(pf)
			if host != "" && guest != "" {
				fmt.Fprintf(b, "        <range start='%s' to='%s'/>\n", escapeXMLAttr(guest), escapeXMLAttr(host))
			}
		}
		for _, pf := range rt.ExtraPortForwards {
			host, guest := splitPortForward(pf)
			if host != "" && guest != "" {
				fmt.Fprintf(b, "        <range start='%s' to='%s'/>\n", escapeXMLAttr(guest), escapeXMLAttr(host))
			}
		}
		b.WriteString("      </portForward>\n")
	}

	b.WriteString("    </interface>\n")
}

func splitPortForward(pf string) (host, guest string) {
	parts := strings.SplitN(pf, ":", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// --- Per-device renderers ---

func renderDisk(b *strings.Builder, d LibvirtDisk) {
	dType := d.Type
	if dType == "" {
		dType = "file"
	}
	dev := d.Device
	if dev == "" {
		dev = "disk"
	}
	fmt.Fprintf(b, "    <disk type='%s' device='%s'>\n", escapeXMLAttr(dType), escapeXMLAttr(dev))
	if len(d.Driver) > 0 {
		b.WriteString("      <driver")
		for k, v := range d.Driver {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if len(d.Source) > 0 {
		b.WriteString("      <source")
		for k, v := range d.Source {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if len(d.Target) > 0 {
		b.WriteString("      <target")
		for k, v := range d.Target {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if boolPtrTrue(d.Readonly) {
		b.WriteString("      <readonly/>\n")
	}
	if d.Serial != "" {
		fmt.Fprintf(b, "      <serial>%s</serial>\n", escapeXML(d.Serial))
	}
	if d.WWN != "" {
		fmt.Fprintf(b, "      <wwn>%s</wwn>\n", escapeXML(d.WWN))
	}
	if d.Boot > 0 {
		fmt.Fprintf(b, "      <boot order='%d'/>\n", d.Boot)
	}
	b.WriteString("    </disk>\n")
}

func renderInterface(b *strings.Builder, iface LibvirtInterface) {
	t := iface.Type
	if t == "" {
		t = "user"
	}
	fmt.Fprintf(b, "    <interface type='%s'>\n", escapeXMLAttr(t))
	if len(iface.Source) > 0 {
		b.WriteString("      <source")
		for k, v := range iface.Source {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if iface.Model != "" {
		fmt.Fprintf(b, "      <model type='%s'/>\n", escapeXMLAttr(iface.Model))
	}
	if iface.MAC != "" {
		fmt.Fprintf(b, "      <mac address='%s'/>\n", escapeXMLAttr(iface.MAC))
	}
	if iface.MTU > 0 {
		fmt.Fprintf(b, "      <mtu size='%d'/>\n", iface.MTU)
	}
	if len(iface.Driver) > 0 {
		b.WriteString("      <driver")
		for k, v := range iface.Driver {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if iface.Boot > 0 {
		fmt.Fprintf(b, "      <boot order='%d'/>\n", iface.Boot)
	}
	if len(iface.PortForwards) > 0 {
		for _, pf := range iface.PortForwards {
			proto := pf.Proto
			if proto == "" {
				proto = "tcp"
			}
			fmt.Fprintf(b, "      <portForward proto='%s'>\n", escapeXMLAttr(proto))
			fmt.Fprintf(b, "        <range start='%d'", pf.Start)
			if pf.To > 0 {
				fmt.Fprintf(b, " to='%d'", pf.To)
			}
			b.WriteString("/>\n")
			b.WriteString("      </portForward>\n")
		}
	}
	b.WriteString("    </interface>\n")
}

func renderChannel(b *strings.Builder, ch LibvirtChannel) {
	t := ch.Type
	if t == "" {
		t = "unix"
	}
	fmt.Fprintf(b, "    <channel type='%s'>\n", escapeXMLAttr(t))
	if ch.Source != "" || ch.Path != "" {
		b.WriteString("      <source")
		if ch.Source != "" {
			fmt.Fprintf(b, " mode='bind' path='%s'", escapeXMLAttr(ch.Source))
		}
		b.WriteString("/>\n")
	}
	if ch.Name != "" {
		fmt.Fprintf(b, "      <target type='virtio' name='%s'/>\n", escapeXMLAttr(ch.Name))
	} else {
		b.WriteString("      <target type='virtio'/>\n")
	}
	b.WriteString("    </channel>\n")
}

func renderSerialChar(b *strings.Builder, s LibvirtSerial) {
	t := s.Type
	if t == "" {
		t = "pty"
	}
	fmt.Fprintf(b, "    <serial type='%s'>\n", escapeXMLAttr(t))
	if len(s.Source) > 0 {
		b.WriteString("      <source")
		for k, v := range s.Source {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if len(s.Target) > 0 {
		b.WriteString("      <target")
		for k, v := range s.Target {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </serial>\n")
}

func renderConsoleChar(b *strings.Builder, c LibvirtConsole) {
	t := c.Type
	if t == "" {
		t = "pty"
	}
	fmt.Fprintf(b, "    <console type='%s'>\n", escapeXMLAttr(t))
	if len(c.Target) > 0 {
		b.WriteString("      <target")
		for k, v := range c.Target {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </console>\n")
}

func renderParallel(b *strings.Builder, p LibvirtParallel) {
	t := p.Type
	if t == "" {
		t = "pty"
	}
	fmt.Fprintf(b, "    <parallel type='%s'>\n", escapeXMLAttr(t))
	if len(p.Source) > 0 {
		b.WriteString("      <source")
		for k, v := range p.Source {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if len(p.Target) > 0 {
		b.WriteString("      <target")
		for k, v := range p.Target {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </parallel>\n")
}

func renderGraphics(b *strings.Builder, g LibvirtGraphics) {
	fmt.Fprintf(b, "    <graphics type='%s'", escapeXMLAttr(g.Type))
	if g.Port != 0 {
		fmt.Fprintf(b, " port='%d'", g.Port)
	}
	if g.AutoPort != "" {
		fmt.Fprintf(b, " autoport='%s'", escapeXMLAttr(g.AutoPort))
	}
	if g.Listen != "" {
		fmt.Fprintf(b, " listen='%s'", escapeXMLAttr(g.Listen))
	}
	if g.Passwd != "" {
		fmt.Fprintf(b, " passwd='%s'", escapeXMLAttr(g.Passwd))
	}
	if g.Keymap != "" {
		fmt.Fprintf(b, " keymap='%s'", escapeXMLAttr(g.Keymap))
	}
	if g.GL == "" {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	fmt.Fprintf(b, "      <gl enable='%s'/>\n", escapeXMLAttr(g.GL))
	b.WriteString("    </graphics>\n")
}

func renderVideo(b *strings.Builder, v LibvirtVideo) {
	b.WriteString("    <video>\n")
	fmt.Fprintf(b, "      <model type='%s'", escapeXMLAttr(v.Model))
	if v.VRAM > 0 {
		fmt.Fprintf(b, " vram='%d'", v.VRAM)
	}
	if v.Heads > 0 {
		fmt.Fprintf(b, " heads='%d'", v.Heads)
	}
	if v.Accel3D != nil {
		fmt.Fprintf(b, " accel3d='%s'", boolPtrToYesNo(v.Accel3D))
	}
	if v.Primary != nil {
		fmt.Fprintf(b, " primary='%s'", boolPtrToYesNo(v.Primary))
	}
	b.WriteString("/>\n")
	b.WriteString("    </video>\n")
}

func renderAudio(b *strings.Builder, a LibvirtAudio) {
	fmt.Fprintf(b, "    <audio")
	if a.Type != "" {
		fmt.Fprintf(b, " type='%s'", escapeXMLAttr(a.Type))
	}
	if a.ID > 0 {
		fmt.Fprintf(b, " id='%d'", a.ID)
	}
	b.WriteString("/>\n")
}

func renderSound(b *strings.Builder, s LibvirtSound) {
	fmt.Fprintf(b, "    <sound model='%s'/>\n", escapeXMLAttr(s.Model))
}

func renderInput(b *strings.Builder, i LibvirtInput) {
	fmt.Fprintf(b, "    <input type='%s'", escapeXMLAttr(i.Type))
	if i.Bus != "" {
		fmt.Fprintf(b, " bus='%s'", escapeXMLAttr(i.Bus))
	}
	b.WriteString("/>\n")
}

func renderUSB(b *strings.Builder, u LibvirtUSB) {
	fmt.Fprintf(b, "    <controller type='usb'")
	if u.Model != "" {
		fmt.Fprintf(b, " model='%s'", escapeXMLAttr(u.Model))
	}
	if u.Ports > 0 {
		fmt.Fprintf(b, " ports='%d'", u.Ports)
	}
	b.WriteString("/>\n")
}

func renderRedirDev(b *strings.Builder, r LibvirtRedirDev) {
	bus := r.Bus
	if bus == "" {
		bus = "usb"
	}
	t := r.Type
	if t == "" {
		t = "spicevmc"
	}
	fmt.Fprintf(b, "    <redirdev bus='%s' type='%s'/>\n", escapeXMLAttr(bus), escapeXMLAttr(t))
}

func renderHostdev(b *strings.Builder, h LibvirtHostdev) {
	mode := h.Mode
	if mode == "" {
		mode = "subsystem"
	}
	fmt.Fprintf(b, "    <hostdev mode='%s' type='%s'", escapeXMLAttr(mode), escapeXMLAttr(h.Type))
	if h.Managed != "" {
		fmt.Fprintf(b, " managed='%s'", escapeXMLAttr(h.Managed))
	}
	b.WriteString(">\n")
	if len(h.Source) > 0 {
		b.WriteString("      <source>\n")
		// PCI address shape: <address domain=... bus=... slot=... function=.../>
		if h.Type == "pci" {
			b.WriteString("        <address")
			for k, v := range h.Source {
				fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
			}
			b.WriteString("/>\n")
		} else if h.Type == "usb" {
			// USB shape: <vendor id=.../><product id=.../> OR <address bus=... device=.../>
			if vendor, ok := h.Source["vendor"]; ok {
				fmt.Fprintf(b, "        <vendor id='%s'/>\n", escapeXMLAttr(vendor))
			}
			if product, ok := h.Source["product"]; ok {
				fmt.Fprintf(b, "        <product id='%s'/>\n", escapeXMLAttr(product))
			}
			if busAddr, ok := h.Source["bus"]; ok {
				device := h.Source["device"]
				fmt.Fprintf(b, "        <address bus='%s' device='%s'/>\n",
					escapeXMLAttr(busAddr), escapeXMLAttr(device))
			}
		} else {
			// Generic passthrough
			b.WriteString("        <address")
			for k, v := range h.Source {
				fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
			}
			b.WriteString("/>\n")
		}
		b.WriteString("      </source>\n")
	}
	if len(h.ROM) > 0 {
		b.WriteString("      <rom")
		for k, v := range h.ROM {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if len(h.Driver) > 0 {
		b.WriteString("      <driver")
		for k, v := range h.Driver {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </hostdev>\n")
}

func renderFilesystem(b *strings.Builder, f LibvirtFilesystem) {
	t := f.Type
	if t == "" {
		t = "mount"
	}
	fmt.Fprintf(b, "    <filesystem type='%s'", escapeXMLAttr(t))
	if f.AccessMode != "" {
		fmt.Fprintf(b, " accessmode='%s'", escapeXMLAttr(f.AccessMode))
	}
	b.WriteString(">\n")
	if f.Driver != "" {
		fmt.Fprintf(b, "      <driver type='%s'/>\n", escapeXMLAttr(f.Driver))
	}
	if len(f.Binary) > 0 {
		b.WriteString("      <binary")
		for k, v := range f.Binary {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if f.Source != "" {
		fmt.Fprintf(b, "      <source dir='%s'/>\n", escapeXMLAttr(f.Source))
	}
	if f.Target != "" {
		fmt.Fprintf(b, "      <target dir='%s'/>\n", escapeXMLAttr(f.Target))
	}
	if boolPtrTrue(f.Readonly) {
		b.WriteString("      <readonly/>\n")
	}
	b.WriteString("    </filesystem>\n")
}

func renderRNG(b *strings.Builder, r LibvirtRNG) {
	model := r.Model
	if model == "" {
		model = "virtio"
	}
	fmt.Fprintf(b, "    <rng model='%s'>\n", escapeXMLAttr(model))
	if r.Backend != "" {
		backendModel := "random"
		if r.Backend == "builtin" {
			backendModel = "builtin"
		}
		fmt.Fprintf(b, "      <backend model='%s'>%s</backend>\n",
			escapeXMLAttr(backendModel), escapeXML(r.Backend))
	}
	if len(r.Rate) > 0 {
		b.WriteString("      <rate")
		for k, v := range r.Rate {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </rng>\n")
}

func renderTPM(b *strings.Builder, t LibvirtTPM) {
	model := t.Model
	if model == "" {
		model = "tpm-crb"
	}
	fmt.Fprintf(b, "    <tpm model='%s'>\n", escapeXMLAttr(model))
	if len(t.Backend) > 0 {
		b.WriteString("      <backend")
		for k, v := range t.Backend {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </tpm>\n")
}

func renderWatchdog(b *strings.Builder, w LibvirtWatchdog) {
	fmt.Fprintf(b, "    <watchdog model='%s'", escapeXMLAttr(w.Model))
	if w.Action != "" {
		fmt.Fprintf(b, " action='%s'", escapeXMLAttr(w.Action))
	}
	b.WriteString("/>\n")
}

func renderMemBalloon(b *strings.Builder, m *LibvirtMemBalloon) {
	fmt.Fprintf(b, "    <memballoon model='%s'", escapeXMLAttr(m.Model))
	if m.Autodeflate != "" {
		fmt.Fprintf(b, " autodeflate='%s'", escapeXMLAttr(m.Autodeflate))
	}
	if len(m.Stats) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	for k, v := range m.Stats {
		fmt.Fprintf(b, "      <stats %s='%d'/>\n", escapeXMLAttr(k), v)
	}
	b.WriteString("    </memballoon>\n")
}

func renderShmem(b *strings.Builder, s LibvirtShmem) {
	fmt.Fprintf(b, "    <shmem name='%s'>\n", escapeXMLAttr(s.Name))
	if s.Role != "" {
		fmt.Fprintf(b, "      <role>%s</role>\n", escapeXML(s.Role))
	}
	if len(s.Model) > 0 {
		b.WriteString("      <model")
		for k, v := range s.Model {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	if s.Size != "" {
		fmt.Fprintf(b, "      <size unit='M'>%s</size>\n", escapeXML(s.Size))
	}
	if len(s.Server) > 0 {
		b.WriteString("      <server")
		for k, v := range s.Server {
			fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
		}
		b.WriteString("/>\n")
	}
	b.WriteString("    </shmem>\n")
}

func renderIOMMU(b *strings.Builder, i *LibvirtIOMMU) {
	fmt.Fprintf(b, "    <iommu model='%s'", escapeXMLAttr(i.Model))
	if len(i.Driver) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	b.WriteString("      <driver")
	for k, v := range i.Driver {
		fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
	}
	b.WriteString("/>\n")
	b.WriteString("    </iommu>\n")
}

func renderVsock(b *strings.Builder, v *LibvirtVsock) {
	model := v.Model
	if model == "" {
		model = "virtio"
	}
	fmt.Fprintf(b, "    <vsock model='%s'", escapeXMLAttr(model))
	if len(v.CID) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	b.WriteString("      <cid")
	for k, val := range v.CID {
		fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(val))
	}
	b.WriteString("/>\n")
	b.WriteString("    </vsock>\n")
}

func renderPanic(b *strings.Builder, p LibvirtPanic) {
	fmt.Fprintf(b, "    <panic")
	if p.Model != "" {
		fmt.Fprintf(b, " model='%s'", escapeXMLAttr(p.Model))
	}
	if len(p.Address) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	b.WriteString("      <address")
	for k, v := range p.Address {
		fmt.Fprintf(b, " %s='%s'", escapeXMLAttr(k), escapeXMLAttr(v))
	}
	b.WriteString("/>\n")
	b.WriteString("    </panic>\n")
}

func renderSmartcard(b *strings.Builder, s LibvirtSmartcard) {
	fmt.Fprintf(b, "    <smartcard")
	if s.Mode != "" {
		fmt.Fprintf(b, " mode='%s'", escapeXMLAttr(s.Mode))
	}
	if s.Type != "" {
		fmt.Fprintf(b, " type='%s'", escapeXMLAttr(s.Type))
	}
	b.WriteString("/>\n")
}

func renderHub(b *strings.Builder, h LibvirtHub) {
	fmt.Fprintf(b, "    <hub type='%s'/>\n", escapeXMLAttr(h.Type))
}

func boolPtrToYesNo(p *bool) string {
	if p == nil {
		return "no"
	}
	if *p {
		return "yes"
	}
	return "no"
}
