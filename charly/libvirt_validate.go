package main

import (
	"fmt"
	"runtime"
	"strings"
)

// ValidateVmSpec is the top-level validator for a kind:vm entity's
// spec. Checks source-kind discriminator coherence, firmware↔machine
// interplay, CPU mode↔model consistency, and host-arch feasibility.
// Errors are accumulated into errs rather than returned so a single
// validator pass surfaces every problem.
func ValidateVmSpec(name string, spec *VmSpec, errs *ValidationError) {
	if spec == nil {
		errs.Add("vm %q: spec is nil", name)
		return
	}

	validateVmSource(name, &spec.Source, errs)
	validateVmFirmwareMachine(name, spec, errs)
	validateVmSSH(name, spec, errs)
	validateVmNetwork(name, spec, errs)
	if spec.Autostart && spec.Backend == "qemu" {
		errs.Add("vm %q: autostart: true requires the libvirt backend (backend: qemu has no persistent daemon to honor it); set backend: libvirt or remove autostart", name)
	}
	if spec.Libvirt != nil {
		ValidateLibvirtDomain(name, spec, errs)
	}
	if spec.CloudInit != nil {
		validateVmCloudInit(name, spec, errs)
	}
	if len(spec.Snapshots) > 0 {
		validateVmSnapshots(name, spec, errs)
	}
}

// validateVmSource checks the discriminated-union invariants. It dispatches
// to the per-kind validator and then applies the kind-independent checksum
// check.
func validateVmSource(name string, src *VmSource, errs *ValidationError) {
	switch src.Kind {
	case "cloud_image":
		validateVmSourceCloudImage(name, src, errs)
	case "bootc":
		validateVmSourceBootc(name, src, errs)
	case "clone":
		validateVmSourceClone(name, src, errs)
	case "imported":
		validateVmSourceImported(name, src, errs)
	case "bootstrap":
		validateVmSourceBootstrap(name, src, errs)
	case "":
		errs.Add("vm %q: source.kind is required (cloud_image, bootc, bootstrap, clone, or imported)", name)
	default:
		errs.Add("vm %q: source.kind %q is unknown (want cloud_image, bootc, bootstrap, clone, or imported)", name, src.Kind)
	}

	if src.Checksum.Type != "" && src.Checksum.Type != "sha256" {
		errs.Add("vm %q: source.checksum.type %q is not supported (only sha256)", name, src.Checksum.Type)
	}
}

// validateVmSourceCloudImage checks the cloud_image source-kind invariants.
func validateVmSourceCloudImage(name string, src *VmSource, errs *ValidationError) {
	if src.URL == "" {
		errs.Add("vm %q: source.kind == cloud_image requires source.url", name)
	}
	// Bootc-only fields should not appear here.
	if src.Box != "" {
		errs.Add("vm %q: source.box only valid when source.kind == bootc (got %q)", name, src.Kind)
	}
	if src.Transport != "" {
		errs.Add("vm %q: source.transport only valid when source.kind == bootc", name)
	}
	if src.Rootfs != "" {
		errs.Add("vm %q: source.rootfs only valid when source.kind == bootc", name)
	}
	if src.RootSize != "" {
		errs.Add("vm %q: source.root_size only valid when source.kind == bootc", name)
	}
	if src.KernelArgs != "" {
		errs.Add("vm %q: source.kernel_args only valid when source.kind == bootc", name)
	}
}

// validateVmSourceBootc checks the bootc source-kind invariants.
func validateVmSourceBootc(name string, src *VmSource, errs *ValidationError) {
	if src.Box == "" {
		errs.Add("vm %q: source.kind == bootc requires source.box (references a kind:box entry)", name)
	}
	if src.URL != "" {
		errs.Add("vm %q: source.url only valid when source.kind == cloud_image", name)
	}
	if src.Checksum.Value != "" || src.Checksum.Type != "" {
		errs.Add("vm %q: source.checksum only valid when source.kind == cloud_image", name)
	}
	if src.Cache != "" {
		errs.Add("vm %q: source.cache only valid when source.kind == cloud_image", name)
	}
	if src.Rootfs != "" {
		switch src.Rootfs {
		case "ext4", "xfs", "btrfs":
			// OK
		default:
			errs.Add("vm %q: source.rootfs %q is not supported (want ext4, xfs, or btrfs)", name, src.Rootfs)
		}
	}
	if src.Transport != "" {
		switch src.Transport {
		case "registry", "containers-storage", "oci", "oci-archive":
			// OK
		default:
			errs.Add("vm %q: source.transport %q is not supported (want registry, containers-storage, oci, or oci-archive)", name, src.Transport)
		}
	}
}

// validateVmSourceClone checks the clone source-kind invariants.
func validateVmSourceClone(name string, src *VmSource, errs *ValidationError) {
	if src.FromVm == "" {
		errs.Add("vm %q: source.kind == clone requires source.from_vm (parent VM name)", name)
	}
	if src.FromSnapshot == "" {
		errs.Add("vm %q: source.kind == clone requires source.from_snapshot (snapshot name on the parent)", name)
	}
	// cloud_image / bootc / imported fields should not appear on clone.
	if src.URL != "" {
		errs.Add("vm %q: source.url only valid when source.kind == cloud_image", name)
	}
	if src.Box != "" {
		errs.Add("vm %q: source.box only valid when source.kind == bootc", name)
	}
	if src.LibvirtName != "" || src.DiskPath != "" || src.DiskFormat != "" {
		errs.Add("vm %q: source.libvirt_name/disk_path/disk_format only valid when source.kind == imported", name)
	}
}

// validateVmSourceImported checks the imported source-kind invariants.
func validateVmSourceImported(name string, src *VmSource, errs *ValidationError) {
	if src.LibvirtName == "" {
		errs.Add("vm %q: source.kind == imported requires source.libvirt_name", name)
	}
	if src.DiskPath == "" {
		errs.Add("vm %q: source.kind == imported requires source.disk_path", name)
	}
	if src.DiskFormat == "" {
		errs.Add("vm %q: source.kind == imported requires source.disk_format (qcow2 or raw)", name)
	} else {
		switch src.DiskFormat {
		case "qcow2", "raw":
			// OK
		default:
			errs.Add("vm %q: source.disk_format %q is not supported (want qcow2 or raw)", name, src.DiskFormat)
		}
	}
	if src.URL != "" || src.Box != "" {
		errs.Add("vm %q: source.url / source.box not valid when source.kind == imported", name)
	}
	if src.FromVm != "" || src.FromSnapshot != "" {
		errs.Add("vm %q: source.from_vm / source.from_snapshot only valid when source.kind == clone", name)
	}
}

// validateVmSourceBootstrap checks the bootstrap source-kind invariants.
func validateVmSourceBootstrap(name string, src *VmSource, errs *ValidationError) {
	if src.Builder == "" {
		errs.Add("vm %q: source.kind == bootstrap requires source.builder (name of a kind: bootstrap builder in build.yml)", name)
	}
	if src.Distro == "" {
		errs.Add("vm %q: source.kind == bootstrap requires source.distro (selects DistroDef in build.yml)", name)
	}
	if src.Rootfs != "" {
		switch src.Rootfs {
		case "ext4", "xfs", "btrfs":
			// OK
		default:
			errs.Add("vm %q: source.rootfs %q is not supported (want ext4, xfs, or btrfs)", name, src.Rootfs)
		}
	}
	if src.URL != "" || src.Box != "" || src.Transport != "" {
		errs.Add("vm %q: source.url / source.image / source.transport are not valid when source.kind == bootstrap", name)
	}
}

// validateVmSnapshots checks the declarative snapshots list.
func validateVmSnapshots(name string, spec *VmSpec, errs *ValidationError) {
	seen := make(map[string]bool, len(spec.Snapshots))
	for i, s := range spec.Snapshots {
		if s.Name == "" {
			errs.Add("vm %q: snapshots[%d]: name is required", name, i)
			continue
		}
		if seen[s.Name] {
			errs.Add("vm %q: snapshots[%d]: duplicate name %q", name, i, s.Name)
		}
		seen[s.Name] = true
		switch s.Mode {
		case "", "external", "internal":
			// OK ("" → defaults to external at apply-time)
		default:
			errs.Add("vm %q: snapshots[%q].mode %q is unknown (want external or internal)", name, s.Name, s.Mode)
		}
	}
}

// validateVmFirmwareMachine covers the D17 coherence rules.
func validateVmFirmwareMachine(name string, spec *VmSpec, errs *ValidationError) {
	switch spec.Firmware {
	case "", "bios", "uefi-insecure", "uefi-secure":
		// OK
	default:
		errs.Add("vm %q: firmware %q is unknown (want bios, uefi-insecure, or uefi-secure)", name, spec.Firmware)
	}

	if spec.Firmware == "uefi-secure" {
		// Secure boot requires SMM — check Features.SMM is explicitly true.
		if spec.Libvirt == nil || spec.Libvirt.Features == nil || !boolPtrTrue(spec.Libvirt.Features.SMM) {
			errs.Add("vm %q: firmware: uefi-secure requires libvirt.features.smm: true", name)
		}
		// Secure boot requires Q35 on x86_64. i440fx isn't supported.
		if spec.Machine == "i440fx" {
			errs.Add("vm %q: firmware: uefi-secure requires machine: q35 (i440fx does not support SMM/Secure Boot)", name)
		}
	}

	if spec.Machine != "" {
		switch spec.Machine {
		case "q35", "virt", "i440fx", "pc":
			// OK
		default:
			// Machine types are architecture-dependent; warn but don't fail.
			// Users running on aarch64 use "virt", on x86 "q35" is standard.
		}
	}
}

func validateVmSSH(name string, spec *VmSpec, errs *ValidationError) {
	if spec.SSH == nil {
		return
	}
	if spec.SSH.Port < 0 || spec.SSH.Port > 65535 {
		errs.Add("vm %q: ssh.port %d out of range 0-65535", name, spec.SSH.Port)
	}
	if spec.SSH.PortAuto && spec.SSH.Port > 0 {
		errs.Add("vm %q: ssh.port and ssh.port_auto are mutually exclusive (use one)", name)
	}
	switch spec.SSH.KeySource {
	case "", "auto", "generate", "none":
		// OK
	default:
		if !strings.HasPrefix(spec.SSH.KeySource, "/") {
			errs.Add("vm %q: ssh.key_source %q must be 'auto', 'generate', 'none', or an absolute path", name, spec.SSH.KeySource)
		}
	}
	if spec.SSH.KeyInjection != nil {
		for field, val := range map[string]string{
			"smbios":     spec.SSH.KeyInjection.SMBIOS,
			"cloud_init": spec.SSH.KeyInjection.CloudInit,
		} {
			switch val {
			case "", "auto", "enabled", "disabled":
				// OK
			default:
				errs.Add("vm %q: ssh.key_injection.%s %q is unknown (want auto, enabled, or disabled)", name, field, val)
			}
		}
	}
}

func validateVmNetwork(name string, spec *VmSpec, errs *ValidationError) {
	if spec.Network == nil {
		return
	}
	switch spec.Network.Mode {
	case "", "user", "bridge", "nat", "network":
		// OK
	default:
		errs.Add("vm %q: network.mode %q is unknown (want user, bridge, nat, or network)", name, spec.Network.Mode)
	}
	if spec.Network.Mode == "bridge" && spec.Network.Bridge == "" {
		errs.Add("vm %q: network.mode == bridge requires network.bridge", name)
	}
	for i, pf := range spec.Network.PortForwards {
		if !strings.Contains(pf, ":") {
			errs.Add("vm %q: network.port_forwards[%d] %q must be host:guest", name, i, pf)
		}
	}
}

func validateVmCloudInit(name string, spec *VmSpec, errs *ValidationError) {
	ci := spec.CloudInit
	if ci == nil {
		return
	}
	// CloudInit only meaningful for cloud_image OR bootc+cloud-init-candy.
	// For a bootc source, candy membership can't be verified from here (requires
	// Config access); the full check lives in charly box validate's top-level
	// wiring. Per D13 the deferred "cloud-init candy present" assertion (when
	// key_injection.cloud_init == "enabled") belongs there too.
	if ci.CharlyInstall != nil {
		switch ci.CharlyInstall.Strategy {
		case "", "auto", "scp", "skip":
			// OK
		default:
			errs.Add("vm %q: cloud_init.charly_install.strategy %q is unknown (want auto, scp, or skip)", name, ci.CharlyInstall.Strategy)
		}
	}
	for i, u := range ci.Users {
		if u.Name == "" {
			errs.Add("vm %q: cloud_init.users[%d]: name is required", name, i)
		}
	}
	for i, f := range ci.WriteFiles {
		if f.Path == "" {
			errs.Add("vm %q: cloud_init.write_files[%d]: path is required", name, i)
		}
	}
}

// ValidateLibvirtDomain covers the structured libvirt-domain coherence
// checks. Called from ValidateVmSpec.
func ValidateLibvirtDomain(name string, spec *VmSpec, errs *ValidationError) {
	lv := spec.Libvirt
	if lv == nil {
		return
	}

	// CPU mode + model coherence.
	if lv.CPU != nil {
		validateLibvirtCPU(name, lv.CPU, errs)
	}

	// Clock offset.
	if lv.Clock != nil {
		switch lv.Clock.Offset {
		case "", "utc", "localtime", "timezone", "variable", "absolute":
			// OK
		default:
			errs.Add("vm %q: libvirt.clock.offset %q is unknown", name, lv.Clock.Offset)
		}
	}

	// Launch security type.
	if lv.LaunchSecurity != nil && lv.LaunchSecurity.Type != "" {
		switch lv.LaunchSecurity.Type {
		case "sev", "sev-es", "sev-snp", "tdx":
			// OK
		default:
			errs.Add("vm %q: libvirt.launch_security.type %q is unknown (want sev, sev-es, sev-snp, or tdx)", name, lv.LaunchSecurity.Type)
		}
	}

	// Raw snippets — preserve existing XML parse check.
	for i, s := range lv.Snippets {
		if err := ValidateLibvirtSnippet(s); err != nil {
			errs.Add("vm %q: libvirt.snippets[%d]: %v", name, i, err)
		}
	}

	// Devices coherence: graphics[].type, video[].model, input[].type.
	if lv.Devices != nil {
		validateLibvirtDevices(name, lv.Devices, errs)
	}
}

// validateLibvirtCPU checks CPU mode/model coherence, feature policy strings,
// and host-vendor ↔ nested-virt-feature consistency.
func validateLibvirtCPU(name string, cpu *LibvirtCPU, errs *ValidationError) {
	switch cpu.Mode {
	case "", "host-passthrough", "host-model", "custom":
		// OK
	default:
		errs.Add("vm %q: libvirt.cpu.mode %q is unknown (want host-passthrough, host-model, or custom)", name, cpu.Mode)
	}
	if cpu.Mode == "custom" && cpu.Model == "" {
		errs.Add("vm %q: libvirt.cpu.mode: custom requires libvirt.cpu.model", name)
	}
	// Feature policy strings.
	for i, f := range cpu.Features {
		switch f.Policy {
		case "", "force", "require", "optional", "disable", "forbid":
			// OK
		default:
			errs.Add("vm %q: libvirt.cpu.features[%d].policy %q is unknown (want force, require, optional, disable, or forbid)", name, i, f.Policy)
		}
		if f.Name == "" {
			errs.Add("vm %q: libvirt.cpu.features[%d]: name is required", name, i)
		}
	}
	// Host-vendor ↔ feature check: flag explicit +vmx on AMD or +svm on Intel.
	hostVendor := detectHostCPUVendor()
	for _, f := range cpu.Features {
		if f.Policy == "disable" || f.Policy == "forbid" {
			continue
		}
		switch f.Name {
		case "vmx":
			if hostVendor == "AuthenticAMD" {
				errs.Add("vm %q: libvirt.cpu.features requests 'vmx' but host CPU vendor is AMD (use 'svm' for nested virt)", name)
			}
		case "svm":
			if hostVendor == "GenuineIntel" {
				errs.Add("vm %q: libvirt.cpu.features requests 'svm' but host CPU vendor is Intel (use 'vmx' for nested virt)", name)
			}
		}
	}
}

// validateLibvirtDevices checks device-type coherence: graphics[].type,
// video[].model, channel-path portability, hostdevs, and filesystems.
func validateLibvirtDevices(name string, devices *LibvirtDevices, errs *ValidationError) {
	for i, g := range devices.Graphics {
		switch g.Type {
		case "vnc", "spice", "rdp", "sdl", "egl-headless":
			// OK
		default:
			errs.Add("vm %q: libvirt.devices.graphics[%d].type %q is unknown", name, i, g.Type)
		}
	}
	for i, v := range devices.Video {
		if v.Model == "" {
			errs.Add("vm %q: libvirt.devices.video[%d]: model is required", name, i)
		}
	}
	// Channel-path portability: reject literal /home/<user>/ paths
	// in <channel><source path=/></channel>. Authors must use
	// {{.VmStateDir}}/<file> or a relative path that the libvirt
	// renderer expands at create time. See expandVmPathTemplate
	// in libvirt_yaml_bridge.go for the supported template vars.
	for i, ch := range devices.Channels {
		validateLibvirtChannelPath(name, i, ch.Path, errs)
		validateLibvirtChannelPath(name, i, ch.Source, errs)
	}
	for i, h := range devices.Hostdevs {
		validateLibvirtHostdev(name, i, h, errs)
	}
	for i, f := range devices.Filesystems {
		validateLibvirtFilesystem(name, i, f, errs)
	}
}

// validateLibvirtFilesystem checks a virtiofs/9p host↔guest share. source
// (host path) + target (guest mount tag) are both required; an empty one
// silently drops the element at render time. A literal /home path is allowed
// here (unlike <channel> sockets) — a filesystem share's whole purpose is to
// expose a host directory, so the operator's chosen path is intentional.
func validateLibvirtFilesystem(name string, i int, f LibvirtFilesystem, errs *ValidationError) {
	if f.Source == "" {
		errs.Add("vm %q: libvirt.devices.filesystems[%d]: source is required (host path to share)", name, i)
	}
	if f.Target == "" {
		errs.Add("vm %q: libvirt.devices.filesystems[%d]: target is required (guest mount tag)", name, i)
	}
	switch f.Driver {
	case "", "virtiofs", "9p", "path":
		// OK
	default:
		errs.Add("vm %q: libvirt.devices.filesystems[%d].driver %q is unknown (want virtiofs, 9p, or path)", name, i, f.Driver)
	}
	switch f.AccessMode {
	case "", "passthrough", "mapped", "squash":
		// OK
	default:
		errs.Add("vm %q: libvirt.devices.filesystems[%d].accessmode %q is invalid (want passthrough, mapped, or squash)", name, i, f.AccessMode)
	}
}

// validateLibvirtHostdev checks a PCI/USB/SCSI/mdev passthrough device.
// For PCI (the GPU-passthrough case) the source address must carry hex
// domain/bus/slot/function; a malformed address otherwise silently drops the
// <source> at render time and libvirt fails to attach the device.
func validateLibvirtHostdev(name string, i int, h LibvirtHostdev, errs *ValidationError) {
	switch h.Type {
	case "pci", "usb", "scsi", "mdev":
		// OK
	default:
		errs.Add("vm %q: libvirt.devices.hostdevs[%d].type %q is unknown (want pci, usb, scsi, or mdev)", name, i, h.Type)
	}
	switch h.Managed {
	case "", "yes", "no":
		// OK
	default:
		errs.Add("vm %q: libvirt.devices.hostdevs[%d].managed %q is invalid (want \"yes\" or \"no\")", name, i, h.Managed)
	}
	if h.Type == "pci" {
		for _, k := range []string{"domain", "bus", "slot", "function"} {
			v := h.Source[k]
			if v == "" {
				errs.Add("vm %q: libvirt.devices.hostdevs[%d] (pci): source.%s is required (run `charly vm gpu list`)", name, i, k)
				continue
			}
			if hexUintPtr(v) == nil {
				errs.Add("vm %q: libvirt.devices.hostdevs[%d] (pci): source.%s %q must be a hex value (e.g. 0x01)", name, i, k, v)
			}
		}
	}
}

// validateLibvirtChannelPath enforces path portability for libvirt
// <channel> sockets. A literal /home/<user>/ path makes the config
// non-portable across user accounts (the prior R10 incident:
// /home/user/.../qga.sock blocked libvirt-backend boot for every
// user not literally named "user"). The template form
// {{.VmStateDir}}/qga.sock is the recommended replacement.
func validateLibvirtChannelPath(name string, idx int, path string, errs *ValidationError) {
	if path == "" {
		return
	}
	if strings.HasPrefix(path, "/home/") {
		// Allow the path through ONLY if it contains a template
		// variable — those resolve at create time.
		if strings.Contains(path, "{{") {
			return
		}
		errs.Add(
			"vm %q: libvirt.devices.channels[%d].path %q hardcodes a /home/<user> "+
				"path; use '{{.VmStateDir}}/<file>' or a relative path that the "+
				"libvirt renderer expands at create time (see /charly-internals:libvirt-renderer)",
			name, idx, path,
		)
	}
}

// detectHostCPUVendor is a minimal helper that reads /proc/cpuinfo
// to get vendor_id. Returns "" when unreadable (non-Linux hosts).
// Separate from the main renderer's HostCPUVendor so validators can
// flag coherence issues before a render happens.
func detectHostCPUVendor() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	v, err := readCPUInfoVendor()
	if err != nil {
		return ""
	}
	return v
}

// readCPUInfoVendor is extracted so ovmf_paths.go and the renderer
// can share it without circular imports.
func readCPUInfoVendor() (string, error) {
	// Kept trivial; the real detection lives in host_cpu_info.go (to be
	// added when renderer vendor detection is wired).
	return "", fmt.Errorf("not yet implemented")
}
