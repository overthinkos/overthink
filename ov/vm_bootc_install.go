package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BootcVMResult mirrors CloudImageBuildResult / BootstrapVMResult.
type BootcVMResult struct {
	DiskPath        string
	SeedIsoPath     string
	InstanceID      string
	BaseImageSHA256 string
	CloudInitDigest string
}

// BuildBootcVM creates a fresh VM disk by running `bootc install
// to-disk` inside a privileged container that hosts the referenced
// kind:image entry. Replaces the Task-21 stub at vm_build.go:198.
//
// The bootc image carries its own kernel + initramfs + bootloader
// integration, so this path skips EmitDiskBuildScript (no chroot
// grub-install needed). It uses RunPrivileged for the privileged
// container and qemu-img convert raw → qcow2 (handled by bootc).
func BuildBootcVM(
	spec *VmSpec,
	outputDir, vmStateDir string,
	existingState *VmDeployState,
) (BootcVMResult, error) {
	if spec.Source.Kind != "bootc" {
		return BootcVMResult{}, fmt.Errorf("BuildBootcVM called with source.kind=%q (expected bootc)", spec.Source.Kind)
	}
	if spec.Source.Image == "" {
		return BootcVMResult{}, fmt.Errorf("source.image is required for bootc VMs")
	}
	if spec.DiskSize == "" {
		return BootcVMResult{}, fmt.Errorf("disk_size is required for bootc VMs")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BootcVMResult{}, fmt.Errorf("creating output dir: %w", err)
	}

	transport := spec.Source.Transport
	if transport == "" {
		transport = "registry"
	}
	rootfs := spec.Source.Rootfs
	if rootfs == "" {
		rootfs = "ext4"
	}

	// Resolve the bootc image ref. For internal kind:image entries,
	// the project's registry config provides a tag; for full OCI tags
	// (containing /), use as-is.
	imageRef := spec.Source.Image
	if !strings.Contains(imageRef, "/") {
		// Internal image name; default to ghcr.io/overthinkos/<name>:latest
		// unless explicit registry/tag is in the ref.
		imageRef = fmt.Sprintf("ghcr.io/overthinkos/%s:latest", imageRef)
	}

	// Render bootc install script. We allocate the raw disk on the host,
	// bind-mount it into the container, and let bootc write to /dev/loopX
	// via a loop device the container creates.
	rawHost := filepath.Join(outputDir, "disk.raw")
	qcowHost := filepath.Join(outputDir, "disk.qcow2")
	rootSizeFlag := ""
	if spec.Source.RootSize != "" {
		rootSizeFlag = fmt.Sprintf(" --root-size %s", spec.Source.RootSize)
	}
	kargFlag := ""
	if strings.TrimSpace(spec.Source.KernelArgs) != "" {
		kargFlag = fmt.Sprintf(" --karg %q", spec.Source.KernelArgs)
	}
	script := fmt.Sprintf(`set -euo pipefail
truncate -s %s /out/disk.raw
LOOP=$(losetup --find --show /out/disk.raw)
trap 'losetup -d "$LOOP" 2>/dev/null || true' EXIT
bootc install to-disk \
  --filesystem %s%s%s \
  --target-no-signature-verification \
  "$LOOP"
sync
losetup -d "$LOOP"
trap - EXIT
qemu-img convert -O qcow2 /out/disk.raw /out/disk.qcow2
rm -f /out/disk.raw
`, spec.DiskSize, rootfs, rootSizeFlag, kargFlag)

	if err := RunPrivileged(PrivilegedRun{
		Image:      imageRef,
		Script:     script,
		OutputPath: "/out/disk.qcow2",
		OutputDest: qcowHost,
	}); err != nil {
		return BootcVMResult{}, fmt.Errorf("running bootc install to-disk: %w", err)
	}
	_ = rawHost // raw is removed inside the container

	res := BootcVMResult{
		DiskPath: qcowHost,
	}
	if spec.CloudInit != nil {
		seedPath := filepath.Join(outputDir, "seed.iso")
		if err := RegenerateSeedISO(spec, seedPath, vmStateDir, existingState); err != nil {
			return BootcVMResult{}, fmt.Errorf("rendering cloud-init seed ISO: %w", err)
		}
		res.SeedIsoPath = seedPath
		if existingState != nil && existingState.InstanceID != "" {
			res.InstanceID = existingState.InstanceID
		}
	}
	return res, nil
}
