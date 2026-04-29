package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// vm_snapshot_internal.go — internal qcow2 snapshot operations via
// qemu-img. Internal snapshots are embedded inside the primary qcow2
// file (no separate file is created); deltas are stored as part of the
// disk's own metadata. Disk-efficient and revert-fast, but cannot be
// directly used as a clone backing target — promotion (qemu-img
// convert) extracts an external file when cloning is needed.

// createInternalSnapshot adds a named internal snapshot to the VM's
// primary qcow2 via `qemu-img snapshot -c`. The VM should ideally be
// stopped or quiesced (qemu-img refuses to write to a live qcow2 in
// most cases); for live snapshots the libvirt path with mode=internal
// would be the right tool, but V1 keeps the qemu-img path simple and
// expects the VM to be stopped.
func createInternalSnapshot(opts SnapshotCreateOpts) error {
	disk, err := vmDiskPath(opts.VmName)
	if err != nil {
		return err
	}
	if opts.Quiesce {
		// Best-effort quiesce via guest-agent; no-op on QEMU backend
		// without virtio-serial. The libvirt path supports proper
		// quiesce; qemu-img doesn't. We surface a one-line note rather
		// than failing — internal snapshots have no formal quiesce
		// channel via qemu-img.
		fmt.Fprintln(os.Stderr, "note: --quiesce on internal-mode snapshots requires a stopped or guest-agent-fsfrozen VM; qemu-img cannot enforce")
	}
	cmd := exec.Command("qemu-img", "snapshot", "-c", opts.SnapName, disk)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img snapshot -c %q %s: %w", opts.SnapName, disk, err)
	}
	return nil
}

// deleteInternalSnapshot removes a named internal snapshot via
// `qemu-img snapshot -d`.
func deleteInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	disk, err := vmDiskPath(vmName)
	if err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "snapshot", "-d", entry.Name, disk)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img snapshot -d %q %s: %w", entry.Name, disk, err)
	}
	return nil
}

// revertInternalSnapshot reverts the primary qcow2 to a named internal
// snapshot via `qemu-img snapshot -a`. Active VMs MUST be stopped first
// — qemu-img refuses to mutate a live qcow2.
func revertInternalSnapshot(vmName string, entry *SnapshotEntry) error {
	disk, err := vmDiskPath(vmName)
	if err != nil {
		return err
	}
	cmd := exec.Command("qemu-img", "snapshot", "-a", entry.Name, disk)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img snapshot -a %q %s (is the VM stopped?): %w", entry.Name, disk, err)
	}
	return nil
}

// listInternalSnapshots returns the snapshot names embedded in the
// primary qcow2 via `qemu-img snapshot -l`. Used by validation paths
// to detect drift between the registry and the actual on-disk
// snapshots; not in the V1 CLI hot-path.
func listInternalSnapshots(vmName string) ([]string, error) {
	disk, err := vmDiskPath(vmName)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("qemu-img", "snapshot", "-l", disk).Output()
	if err != nil {
		return nil, fmt.Errorf("qemu-img snapshot -l %s: %w", disk, err)
	}
	// Output format:
	//   Snapshot list:
	//   ID    TAG       VM SIZE   DATE          VM CLOCK
	//   1     baseline  0 B       2026-04-29... 00:00:00.000
	//
	// Parse the second column (TAG) of each non-header line.
	var names []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Skip header rows. The header always has "ID" as field 0
		// and "TAG" as field 1; the divider line has only dashes.
		if fields[0] == "ID" || fields[0] == "Snapshot" {
			continue
		}
		// Numeric ID followed by tag — both expected.
		names = append(names, fields[1])
	}
	return names, nil
}

// promoteInternalToExternal extracts an internal snapshot into a new
// qcow2 file via `qemu-img convert`. The output file becomes a usable
// backing target for clone overlays; the original internal snapshot is
// not deleted (promotion is non-destructive — callers can still revert
// to the internal snapshot afterwards).
func promoteInternalToExternal(vmName string, entry *SnapshotEntry, outPath string) error {
	disk, err := vmDiskPath(vmName)
	if err != nil {
		return err
	}
	// `qemu-img convert -l snapshot.name=<name> -O qcow2 <input> <output>`
	// — the -l flag selects which internal snapshot to extract. Without
	// it, convert extracts the current state, not the snapshot.
	args := []string{
		"convert",
		"-l", "snapshot.name=" + entry.Name,
		"-O", "qcow2",
		disk,
		outPath,
	}
	cmd := exec.Command("qemu-img", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img convert -l snapshot.name=%q %s %s: %w", entry.Name, disk, outPath, err)
	}
	return nil
}
