package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// vm_host_helpers.go — pure-host VM helpers extracted from the deleted go-libvirt/govmm impl files
// (vm_libvirt.go / vm_qemu.go) during the go-libvirt + govmm shed. These touch only the filesystem
// + the OS process table — no go-libvirt, no govmm — so they stay in charly's core while the
// libvirt/QMP impl moved to candy/plugin-vm.

// libvirtSessionSocket returns the path to the user's libvirt session socket. Modern libvirt (≥ 8.0)
// uses per-driver modular daemons (virtqemud-sock); legacy libvirt (< 8.0) uses the monolithic
// libvirt-sock. Probe the modular socket first (every current distro), fall back to legacy.
func libvirtSessionSocket() string {
	picked, _ := libvirtSessionSocketWithProbes()
	return picked
}

func libvirtSessionSocketWithProbes() (picked string, probed []string) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	libvirtDir := filepath.Join(dir, "libvirt")

	// Probe order: modular (virtqemud) first — standard on libvirt ≥ 8.0 — then legacy monolithic.
	modular := filepath.Join(libvirtDir, "virtqemud-sock")
	legacy := filepath.Join(libvirtDir, "libvirt-sock")
	probed = []string{modular, legacy}

	if _, err := os.Stat(modular); err == nil {
		return modular, probed
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy, probed
	}
	return legacy, probed
}

// killQemuByPID force-kills a direct-QEMU VM by the PID recorded in its state dir (the last-resort
// path when QMP graceful/force shutdown is unavailable). Pure OS process kill — no govmm.
func killQemuByPID(stateDir string) {
	pidFile := filepath.Join(stateDir, "qemu.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Kill()
}

// writeJSON encodes v as indented JSON to w (the `--json` output helper, formerly in the deleted
// libvirt_cmd.go; the `charly vm snapshot list --json` path uses it).
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
