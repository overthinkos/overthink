package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// VmBuildCmd builds a QCOW2/RAW disk image from a bootc container image.
type VmBuildCmd struct {
	Image     string `arg:"" help:"Bootc image name"`
	Size      string `long:"size" help:"Override disk size (e.g. 20G, '20 GiB')"`
	RootSize  string `long:"root-size" help:"Override root partition size (e.g. 10G)"`
	Tag       string `long:"tag" help:"Image tag override"`
	Type      string `long:"type" default:"qcow2" help:"Output format: qcow2, raw"`
	Transport string `long:"transport" help:"Image transport: registry, containers-storage, oci, oci-archive"`
	Console   bool   `long:"console" help:"Enable console output for debugging"`
}

func (c *VmBuildCmd) Run() error {
	// Validate output type
	switch c.Type {
	case "qcow2", "raw":
	case "iso":
		return fmt.Errorf("iso format is not supported — use qcow2 or raw")
	default:
		return fmt.Errorf("unsupported disk type %q (valid: qcow2, raw)", c.Type)
	}

	// Parse image:tag format from positional arg
	imageName, imageTag := parseImageArg(c.Image)

	calverTag := "latest"
	if imageTag != "" {
		calverTag = imageTag
	} else if c.Tag != "" {
		calverTag = c.Tag
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	rt, rtErr := ResolveRuntime()
	if rtErr != nil {
		return rtErr
	}

	// --- New kind:vm entity path (D1, D4, D12) ---
	// When imageName matches a kind:vm entity in overthink.yml, branch
	// into the VmSpec-driven build pipeline: cloud_image → fetch+
	// resize+seed ISO; bootc → bootc install reading the bootc-branch
	// fields from VmSpec.Source.
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf.VM != nil {
		if spec, hit := uf.VM[imageName]; hit {
			return c.runVmSpecBuild(imageName, spec, rt)
		}
	}

	// Reached here = no `kind: vm` entity matched imageName. The legacy
	// ImageConfig.Vm / ImageConfig.Bootc fallback for VM builds was
	// removed in the VM hard-cutover. Users must declare a `kind: vm`
	// entity in vms.yml — paired with the bootc image if applicable.
	_ = calverTag
	return fmt.Errorf(
		"VM %q has no kind:vm entity in vms.yml.\n"+
			"  For a bootc VM, declare one in vms.yml:\n"+
			"      vms:\n"+
			"        %s-bootc:\n"+
			"          source:\n"+
			"            kind: bootc\n"+
			"            image: %s\n"+
			"          disk_size: 20G\n"+
			"  Or auto-generate paired entries for bootc:true images:\n"+
			"      ov migrate vm-spec",
		imageName, imageName, imageName)
}

// createSparseFile creates a sparse file of the given size (e.g. "10G", "20G").
func createSparseFile(path, size string) error {
	// Parse size to bytes
	sizeBytes, err := parseSizeToBytes(size)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return f.Truncate(sizeBytes)
}

// parseSizeToBytes converts "10G", "20M", "1T" etc. to bytes.
func parseSizeToBytes(size string) (int64, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 0, fmt.Errorf("empty size")
	}

	multiplier := int64(1)
	numStr := size

	switch {
	case strings.HasSuffix(size, "T") || strings.HasSuffix(size, "t"):
		multiplier = 1024 * 1024 * 1024 * 1024
		numStr = size[:len(size)-1]
	case strings.HasSuffix(size, "G") || strings.HasSuffix(size, "g"):
		multiplier = 1024 * 1024 * 1024
		numStr = size[:len(size)-1]
	case strings.HasSuffix(size, "M") || strings.HasSuffix(size, "m"):
		multiplier = 1024 * 1024
		numStr = size[:len(size)-1]
	}

	var val int64
	if _, err := fmt.Sscanf(numStr, "%d", &val); err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", size, err)
	}
	return val * multiplier, nil
}

// parseImageArg splits "image:tag" into (image, tag). If no colon, tag is empty.
func parseImageArg(arg string) (string, string) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// normalizeSize converts size strings like "10 GiB" or "20 MiB" to
// compact format like "10G" or "20M". Strips spaces and converts GiB→G, MiB→M, etc.
func normalizeSize(size string) string {
	s := strings.ReplaceAll(size, " ", "")
	s = strings.ReplaceAll(s, "GiB", "G")
	s = strings.ReplaceAll(s, "MiB", "M")
	s = strings.ReplaceAll(s, "TiB", "T")
	return s
}

// runVmSpecBuild handles `ov vm build <vm-name>` where <vm-name>
// matches a kind:vm entity in overthink.yml. Branches on source.kind:
//
//	cloud_image → fetch URL + qcow2 overlay + resize + seed ISO (D4)
//	bootc       → bootc install to-disk reading VmSpec.Source fields (D12)
//
// The new path lives alongside the legacy VmConfig path in VmBuildCmd.Run
// until the Hard Cutover commit (Task 21) deletes the legacy branch.
func (c *VmBuildCmd) runVmSpecBuild(vmName string, spec *VmSpec, rt *ResolvedRuntime) error {
	fmt.Fprintf(os.Stderr, "Building VM %q (source.kind=%s)\n", vmName, spec.Source.Kind)

	switch spec.Source.Kind {
	case "cloud_image":
		outputDir, err := filepath.Abs(filepath.Join("output", "qcow2"))
		if err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		vmStateDir := filepath.Join(home, ".local", "share", "ov", "vm", "ov-"+vmName)
		if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
			return err
		}
		// Hydrate existing VmDeployState (for stable instance-id across
		// rebuilds).
		var existingState *VmDeployState
		if dc, _ := LoadDeployConfig(); dc != nil {
			if e, ok := dc.Images["vm:"+vmName]; ok {
				existingState = e.VmState
			}
		}
		res, err := BuildCloudImage(spec, outputDir, vmStateDir, existingState)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s (base sha256=%s)\n", res.DiskPath, res.BaseImageSHA256)
		fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		fmt.Fprintf(os.Stderr, "Instance-id: %s\n", res.InstanceID)
		return nil

	case "bootc":
		// Bootc VMs need an existing container image to `bootc install`
		// against. Look up the referenced kind:image and delegate to the
		// existing bootc install pipeline via a synthetic VmConfig.
		// This is a temporary shim while the legacy VmConfig machinery
		// is still in place; the Hard Cutover commit will collapse the
		// two paths into one.
		_ = rt
		return fmt.Errorf("bootc source via kind:vm entity: full wiring lands in the Hard Cutover commit (Task 21). Workaround: use the existing legacy kind:image + bootc: true path for now")

	default:
		return fmt.Errorf("vm %q: unsupported source.kind %q (want cloud_image or bootc)", vmName, spec.Source.Kind)
	}
}
