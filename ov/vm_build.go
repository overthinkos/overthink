package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	SshKeygen bool   `long:"ssh-keygen" short:"K" help:"Generate SSH keypair and inject via systemd credentials"`
}

func (c *VmBuildCmd) Run() error {
	// Validate output type
	switch c.Type {
	case "qcow2", "raw":
	case "iso":
		return fmt.Errorf("iso format is not supported by bcvk — use qcow2 or raw")
	default:
		return fmt.Errorf("unsupported disk type %q (valid: qcow2, raw)", c.Type)
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Parse image:tag format from positional arg
	imageName, imageTag := parseImageArg(c.Image)

	calverTag := "latest"
	if imageTag != "" {
		calverTag = imageTag
	} else if c.Tag != "" {
		calverTag = c.Tag
	}

	resolved, err := cfg.ResolveImage(imageName, calverTag)
	if err != nil {
		return err
	}

	if !resolved.Bootc {
		return fmt.Errorf("image %q is not a bootc image (bootc: true required)", imageName)
	}

	// Require bcvk
	if _, err := exec.LookPath("bcvk"); err != nil {
		return fmt.Errorf("bcvk is required for disk image builds (dnf install bcvk): %w", err)
	}

	// Resolve VM config (image → defaults → hardcoded defaults)
	vmCfg := resolved.Vm

	// CLI --size overrides config
	diskSize := normalizeSizeForBcvk(vmCfg.DiskSize)
	if c.Size != "" {
		diskSize = normalizeSizeForBcvk(c.Size)
	}

	// CLI --root-size overrides config
	rootSize := normalizeSizeForBcvk(vmCfg.RootSize)
	if c.RootSize != "" {
		rootSize = normalizeSizeForBcvk(c.RootSize)
	}

	// CLI --transport overrides config
	transport := vmCfg.Transport
	if c.Transport != "" {
		transport = c.Transport
	}

	imageRef := resolved.FullTag

	fmt.Fprintf(os.Stderr, "Building %s for %s\n", c.Type, imageRef)

	// Create output directory and resolve output path
	outputPath := filepath.Join("output", c.Type, "disk."+c.Type)
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}

	// Build bcvk args
	fmt.Fprintf(os.Stderr, "Running bcvk to-disk...\n")
	args := []string{
		"to-disk",
		"--format", c.Type,
		"--disk-size", diskSize,
		"--filesystem", vmCfg.Rootfs,
	}
	if rootSize != "" {
		args = append(args, "--root-size", rootSize)
	}
	if vmCfg.KernelArgs != "" {
		for _, karg := range strings.Fields(vmCfg.KernelArgs) {
			args = append(args, "--karg", karg)
		}
	}
	if vmCfg.Ram != "" {
		args = append(args, "--memory", normalizeSizeForBcvk(vmCfg.Ram))
	}
	if vmCfg.Cpus > 0 {
		args = append(args, "--vcpus", strconv.Itoa(vmCfg.Cpus))
	}
	if transport != "" {
		args = append(args, "--target-transport", transport)
	}
	if c.Console {
		args = append(args, "--console")
	}
	if c.SshKeygen {
		args = append(args, "-K")
	}
	args = append(args, imageRef, absOutputPath)

	cmd := exec.Command("bcvk", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bcvk failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "%s written to %s\n", strings.ToUpper(c.Type), outputPath)
	return nil
}

// parseImageArg splits "image:tag" into (image, tag). If no colon, tag is empty.
func parseImageArg(arg string) (string, string) {
	if i := strings.LastIndex(arg, ":"); i > 0 {
		return arg[:i], arg[i+1:]
	}
	return arg, ""
}

// normalizeSizeForBcvk converts size strings like "10 GiB" or "20 MiB" to bcvk-compatible
// format like "10G" or "20M". Strips spaces and converts GiB→G, MiB→M, etc.
func normalizeSizeForBcvk(size string) string {
	s := strings.ReplaceAll(size, " ", "")
	s = strings.ReplaceAll(s, "GiB", "G")
	s = strings.ReplaceAll(s, "MiB", "M")
	s = strings.ReplaceAll(s, "TiB", "T")
	return s
}
