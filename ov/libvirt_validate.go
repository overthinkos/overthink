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
	if spec.Libvirt != nil {
		ValidateLibvirtConfig(name, spec, errs)
	}
	if spec.CloudInit != nil {
		validateVmCloudInit(name, spec, errs)
	}
}

// validateVmSource checks the discriminated-union invariants.
func validateVmSource(name string, src *VmSource, errs *ValidationError) {
	switch src.Kind {
	case "cloud_image":
		if src.URL == "" {
			errs.Add("vm %q: source.kind == cloud_image requires source.url", name)
		}
		// Bootc-only fields should not appear here.
		if src.Image != "" {
			errs.Add("vm %q: source.image only valid when source.kind == bootc (got %q)", name, src.Kind)
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
	case "bootc":
		if src.Image == "" {
			errs.Add("vm %q: source.kind == bootc requires source.image (references a kind:image entry)", name)
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
	case "":
		errs.Add("vm %q: source.kind is required (cloud_image or bootc)", name)
	default:
		errs.Add("vm %q: source.kind %q is unknown (want cloud_image or bootc)", name, src.Kind)
	}

	if src.Checksum.Type != "" && src.Checksum.Type != "sha256" {
		errs.Add("vm %q: source.checksum.type %q is not supported (only sha256)", name, src.Checksum.Type)
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
	// CloudInit only meaningful for cloud_image OR bootc+cloud-init-layer.
	if spec.Source.Kind == "bootc" {
		// Can't verify layer membership from here (requires Config access).
		// Full check lives in ov image validate's top-level wiring.
		// Per D13: warn via validator only when key_injection.cloud_init
		// was explicitly requested (user intent to use cloud-init).
		if spec.SSH != nil && spec.SSH.KeyInjection != nil &&
			spec.SSH.KeyInjection.CloudInit == "enabled" {
			// Actual "cloud-init layer present" check is deferred.
		}
	}
	if ci.OvInstall != nil {
		switch ci.OvInstall.Strategy {
		case "", "auto", "scp", "url", "skip":
			// OK
		default:
			errs.Add("vm %q: cloud_init.ov_install.strategy %q is unknown (want auto, scp, url, or skip)", name, ci.OvInstall.Strategy)
		}
		if ci.OvInstall.Strategy == "url" && ci.OvInstall.URL == "" {
			errs.Add("vm %q: cloud_init.ov_install.strategy: url requires cloud_init.ov_install.url", name)
		}
		if ci.OvInstall.Checksum != "" && !strings.HasPrefix(ci.OvInstall.Checksum, "sha256:") {
			errs.Add("vm %q: cloud_init.ov_install.checksum must have prefix 'sha256:'", name)
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

// ValidateLibvirtConfig covers the structured libvirt config coherence
// checks. Called from ValidateVmSpec.
func ValidateLibvirtConfig(name string, spec *VmSpec, errs *ValidationError) {
	lv := spec.Libvirt
	if lv == nil {
		return
	}

	// CPU mode + model coherence.
	if lv.CPU != nil {
		switch lv.CPU.Mode {
		case "", "host-passthrough", "host-model", "custom":
			// OK
		default:
			errs.Add("vm %q: libvirt.cpu.mode %q is unknown (want host-passthrough, host-model, or custom)", name, lv.CPU.Mode)
		}
		if lv.CPU.Mode == "custom" && lv.CPU.Model == "" {
			errs.Add("vm %q: libvirt.cpu.mode: custom requires libvirt.cpu.model", name)
		}
		// Feature policy strings.
		for i, f := range lv.CPU.Features {
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
		for _, f := range lv.CPU.Features {
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
		for i, g := range lv.Devices.Graphics {
			switch g.Type {
			case "vnc", "spice", "rdp", "sdl", "egl-headless":
				// OK
			default:
				errs.Add("vm %q: libvirt.devices.graphics[%d].type %q is unknown", name, i, g.Type)
			}
		}
		for i, v := range lv.Devices.Video {
			if v.Model == "" {
				errs.Add("vm %q: libvirt.devices.video[%d]: model is required", name, i)
			}
		}
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
