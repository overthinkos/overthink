package main

import (
	_ "embed"
	"os/exec"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// vm_phaseA_shims.go — small impl helpers extracted from charly/vm.go (which stayed core, as the
// `charly vm` command host) plus one transitional Phase-A stub. The extracts are REAL impl the
// out-of-process plugin needs (the plugin runs on the host). They are tiny and currently
// DUPLICATED with core's vm.go; Phase B either extracts them to a shared package or accepts the
// per-module copy (R3 decision). The one true shim is unmarshalEmbeddedDefaults — see below.

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

// unmarshalEmbeddedDefaults decodes the plugin's embedded build vocab (a copy of charly's
// charly.yml — the ovmf_paths/distro sections the OVMF resolver reads). The out-of-process plugin
// self-resolves OVMF firmware paths from its own embedded vocab since it cannot reach charly's
// embed. DUP with core's charly.yml — Phase B+ extracts to a shared OVMF data file.
func unmarshalEmbeddedDefaults(dst any) {
	_ = yaml.Unmarshal(embeddedCharlyDefaults, dst)
}
