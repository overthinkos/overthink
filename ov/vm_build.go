package main

import (
	"fmt"
	"os"
	"os/exec"
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

	var vmCfg *VmConfig
	var imageRef string

	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(imageName, calverTag, dir)
		if err != nil {
			return err
		}
		if !resolved.Bootc {
			return fmt.Errorf("image %q is not a bootc image (bootc: true required)", imageName)
		}
		vmCfg = resolved.Vm
		imageRef = resolved.FullTag
	} else {
		// Label path
		ref := fmt.Sprintf("%s:%s", imageName, calverTag)
		meta, metaErr := ExtractMetadata(ResolveImageEngineFromDir(dir, imageName, rt.RunEngine), ref)
		if metaErr != nil {
			return metaErr
		}
		if meta == nil {
			return fmt.Errorf("image %s has no embedded metadata; rebuild with latest ov", ref)
		}
		if !meta.Bootc {
			return fmt.Errorf("image %q is not a bootc image (bootc: true required)", imageName)
		}
		vmCfg = meta.Vm
		if meta.Registry != "" {
			imageRef = fmt.Sprintf("%s/%s:%s", meta.Registry, imageName, calverTag)
		} else {
			imageRef = ref
		}
	}

	if vmCfg == nil {
		vmCfg = &VmConfig{}
	}

	engine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)

	// CLI --size overrides config
	diskSize := normalizeSize(vmCfg.DiskSize)
	if diskSize == "" {
		diskSize = "10G"
	}
	if c.Size != "" {
		diskSize = normalizeSize(c.Size)
	}

	// CLI --root-size overrides config
	rootSize := normalizeSize(vmCfg.RootSize)
	if c.RootSize != "" {
		rootSize = normalizeSize(c.RootSize)
	}

	// CLI --transport overrides config
	transport := vmCfg.Transport
	if c.Transport != "" {
		transport = c.Transport
	}

	fmt.Fprintf(os.Stderr, "Building %s for %s\n", c.Type, imageRef)

	// Always build as raw first, convert to qcow2 after if needed
	rawOutputDir, err := filepath.Abs(filepath.Join("output", "raw"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(rawOutputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	rawDiskPath := filepath.Join(rawOutputDir, "disk.raw")

	// Build bootc install to-disk args inside the container
	bootcArgs := []string{"bootc", "install", "to-disk", "--generic-image", "--via-loopback"}
	if rootSize != "" {
		bootcArgs = append(bootcArgs, "--root-size", rootSize)
	}
	if vmCfg.Rootfs != "" {
		bootcArgs = append(bootcArgs, "--filesystem", vmCfg.Rootfs)
	}
	if vmCfg.KernelArgs != "" {
		for _, karg := range strings.Fields(vmCfg.KernelArgs) {
			bootcArgs = append(bootcArgs, "--karg", karg)
		}
	}
	if transport != "" {
		bootcArgs = append(bootcArgs, "--target-imgref", transport+"://"+imageRef)
	}
	bootcArgs = append(bootcArgs, "/output/disk.raw")

	// Create a sparse raw disk image of the requested size
	if err := createSparseFile(rawDiskPath, diskSize); err != nil {
		return fmt.Errorf("creating disk image: %w", err)
	}

	// Resolve rootful engine (podman machine, sudo, or native)
	engineCmd, err := RootfulEngine(engine, rt.Rootful)
	if err != nil {
		return fmt.Errorf("resolving rootful engine: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Running bootc install to-disk via %s...\n", strings.Join(engineCmd, " "))

	// Build container run args. `-v /dev:/dev` is required so that the
	// privileged container shares the host's /dev namespace — `bootc install
	// to-disk --via-loopback` calls `losetup` which needs to create new
	// `/dev/loopN` device nodes via `/dev/loop-control`. Without the host
	// /dev mount, the container's default tmpfs /dev has `/dev/loop-control`
	// but new loop devices created by the kernel are invisible to the
	// container (different mount namespace), producing
	// `losetup: failed to set up loop device: No such file or directory`.
	args := []string{
		"run", "--rm", "--privileged",
		"--pid=host",
		"--security-opt", "label=type:unconfined_t",
		"-v", "/dev:/dev",
		"-v", rawDiskPath + ":/output/disk.raw",
		"-v", "/var/lib/containers:/var/lib/containers",
	}

	args = append(args, imageRef)
	args = append(args, bootcArgs...)

	cmdArgs := append(engineCmd[1:], args...)
	cmd := exec.Command(engineCmd[0], cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if !c.Console {
		cmd.Stdout = nil
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bootc install to-disk failed: %w", err)
	}

	// Convert raw → qcow2 if requested
	outputPath := filepath.Join("output", c.Type, "disk."+c.Type)
	if c.Type == "qcow2" {
		qcow2Dir := filepath.Join("output", "qcow2")
		if err := os.MkdirAll(qcow2Dir, 0755); err != nil {
			return fmt.Errorf("creating qcow2 output directory: %w", err)
		}
		absQcow2, _ := filepath.Abs(outputPath)
		os.Remove(absQcow2) // remove any existing file to avoid lock conflicts

		fmt.Fprintf(os.Stderr, "Converting raw → qcow2...\n")
		convertCmd := exec.Command("qemu-img", "convert", "-f", "raw", "-O", "qcow2", rawDiskPath, absQcow2)
		convertCmd.Stderr = os.Stderr
		if err := convertCmd.Run(); err != nil {
			return fmt.Errorf("qemu-img convert failed: %w", err)
		}

		// Clean up raw intermediate
		os.Remove(rawDiskPath)
	}

	fmt.Fprintf(os.Stderr, "%s written to %s\n", strings.ToUpper(c.Type), outputPath)
	return nil
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
