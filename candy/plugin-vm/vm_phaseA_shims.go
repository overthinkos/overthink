package main

import (
	_ "embed"
	"os/exec"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// vm_phaseA_shims.go — small host-side impl helpers the out-of-process plugin needs (it runs on the
// host). vmDiskDir + unmarshalEmbeddedDefaults implement the vmshared.VmDiskDir +
// vmshared.UnmarshalEmbeddedDefaults injection seams (charly/vmshared/hooks.go, wired in
// vmshared_aliases.go). libvirtSessionURI / qemuSystemBinary / startLibvirtUserSession are a
// deliberate per-module copy of core's charly/vm.go host-detection helpers (a const + two tiny funcs,
// ~13 lines): the SUBSTANTIAL shared VM code already lives in vmshared, and these are below the bar
// for exporting trivia across the module boundary (R3 — the shared-vs-trivial line). NOT transitional.

// libvirtSessionURI is the rootless per-user libvirt endpoint (extract from vm.go).
const libvirtSessionURI = "qemu:///session"

// qemuSystemBinary picks the host's qemu binary by CPU arch (extract from vm.go; the plugin
// runs on the host, so host-arch detection stays correct).
func qemuSystemBinary() string {
	switch runtime.GOARCH {
	case "arm64":
		return "qemu-system-aarch64"
	default:
		return "qemu-system-x86_64"
	}
}

// startLibvirtUserSession ensures the per-user libvirt daemon is running (extract from vm.go).
var startLibvirtUserSession = func() {
	for _, unit := range []string{"virtqemud.service", "libvirtd.service"} {
		_ = exec.Command("systemctl", "--user", "start", unit).Run()
	}
	if _, err := exec.LookPath("virsh"); err == nil {
		_ = exec.Command("virsh", "-c", "qemu:///session", "list").Run()
	}
}

// vmDiskDir is the per-VM qcow2 disk directory (extract from vm.go).
func vmDiskDir(vmName string) string {
	return filepath.Join("output", "qcow2", vmName)
}

//go:embed build_defaults.yml
var embeddedCharlyDefaults []byte

// unmarshalEmbeddedDefaults decodes the plugin's embedded build vocab (build_defaults.yml, a copy of
// charly's charly.yml — the ovmf_paths/distro sections the OVMF resolver reads). The out-of-process
// plugin self-resolves OVMF firmware paths from its own embedded vocab since it cannot reach charly's
// //go:embed. Implements the vmshared.UnmarshalEmbeddedDefaults seam (wired in vmshared_aliases.go).
func unmarshalEmbeddedDefaults(dst any) {
	_ = yaml.Unmarshal(embeddedCharlyDefaults, dst)
}
