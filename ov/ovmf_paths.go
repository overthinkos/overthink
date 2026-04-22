package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// OvmfPaths is the pair of firmware image paths needed to boot a UEFI
// VM: OVMF_CODE (read-only, shared across all VMs) and an OVMF_VARS
// template (read-only, copied to a per-VM writable NVRAM file on first
// VM create).
type OvmfPaths struct {
	// CodePath is the OVMF_CODE firmware image. Read-only.
	CodePath string

	// VarsTemplate is the OVMF_VARS template with standard UEFI CA
	// keys pre-enrolled (when secure=true). The per-VM NVRAM file is
	// copied from this template on first VM create.
	VarsTemplate string

	// Secure indicates whether this is the secure-boot-enabled variant.
	Secure bool
}

// ResolveOvmfPaths picks the correct OVMF_CODE + OVMF_VARS paths for
// the host distro + secure-boot setting. Returns an error when no
// candidate path exists on disk so `ov vm create` fails with a clean
// remediation hint instead of a cryptic QEMU pflash error.
//
// D17 path table:
//
//	Fedora         /usr/share/OVMF/OVMF_CODE{.secboot,}.fd
//	               /usr/share/OVMF/OVMF_VARS{.secboot,}.fd
//	Arch           /usr/share/edk2/x64/OVMF_CODE{.secboot,}.4m.fd
//	               /usr/share/edk2/x64/OVMF_VARS.4m.fd
//	Debian/Ubuntu  /usr/share/OVMF/OVMF_CODE_4M{.ms,}.fd
//	               /usr/share/OVMF/OVMF_VARS_4M{.ms,}.fd
func ResolveOvmfPaths(distroID string, secure bool) (OvmfPaths, error) {
	candidates := ovmfCandidatesForDistro(distroID, secure)
	for _, c := range candidates {
		if _, err := os.Stat(c.CodePath); err != nil {
			continue
		}
		if _, err := os.Stat(c.VarsTemplate); err != nil {
			continue
		}
		c.Secure = secure
		return c, nil
	}
	return OvmfPaths{}, ovmfNotFoundError(distroID, secure)
}

// ovmfCandidatesForDistro returns the ordered candidate path pairs for
// a given distro + secure-boot combination. More than one candidate
// per distro covers historical path variation (e.g. Fedora pre-40 used
// /usr/share/edk2-ovmf/ instead of /usr/share/OVMF/).
func ovmfCandidatesForDistro(distroID string, secure bool) []OvmfPaths {
	switch distroID {
	case "fedora", "centos", "rhel", "rocky", "alma":
		if secure {
			return []OvmfPaths{
				{CodePath: "/usr/share/OVMF/OVMF_CODE.secboot.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS.secboot.fd"},
				{CodePath: "/usr/share/edk2/ovmf/OVMF_CODE.secboot.fd", VarsTemplate: "/usr/share/edk2/ovmf/OVMF_VARS.secboot.fd"},
			}
		}
		return []OvmfPaths{
			{CodePath: "/usr/share/OVMF/OVMF_CODE.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS.fd"},
			{CodePath: "/usr/share/edk2/ovmf/OVMF_CODE.fd", VarsTemplate: "/usr/share/edk2/ovmf/OVMF_VARS.fd"},
		}

	case "arch", "manjaro", "endeavouros":
		if secure {
			return []OvmfPaths{
				{CodePath: "/usr/share/edk2/x64/OVMF_CODE.secboot.4m.fd", VarsTemplate: "/usr/share/edk2/x64/OVMF_VARS.4m.fd"},
				{CodePath: "/usr/share/edk2-ovmf/x64/OVMF_CODE.secboot.fd", VarsTemplate: "/usr/share/edk2-ovmf/x64/OVMF_VARS.fd"},
			}
		}
		return []OvmfPaths{
			{CodePath: "/usr/share/edk2/x64/OVMF_CODE.4m.fd", VarsTemplate: "/usr/share/edk2/x64/OVMF_VARS.4m.fd"},
			{CodePath: "/usr/share/edk2-ovmf/x64/OVMF_CODE.fd", VarsTemplate: "/usr/share/edk2-ovmf/x64/OVMF_VARS.fd"},
		}

	case "debian", "ubuntu":
		if secure {
			return []OvmfPaths{
				{CodePath: "/usr/share/OVMF/OVMF_CODE_4M.ms.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS_4M.ms.fd"},
				{CodePath: "/usr/share/OVMF/OVMF_CODE.secboot.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS.secboot.fd"},
			}
		}
		return []OvmfPaths{
			{CodePath: "/usr/share/OVMF/OVMF_CODE_4M.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS_4M.fd"},
			{CodePath: "/usr/share/OVMF/OVMF_CODE.fd", VarsTemplate: "/usr/share/OVMF/OVMF_VARS.fd"},
		}
	}

	// Unknown distro — try the union of common paths.
	merged := []OvmfPaths{}
	merged = append(merged, ovmfCandidatesForDistro("fedora", secure)...)
	merged = append(merged, ovmfCandidatesForDistro("arch", secure)...)
	merged = append(merged, ovmfCandidatesForDistro("debian", secure)...)
	return merged
}

// ovmfNotFoundError returns a clear error with distro-appropriate
// install recipe.
func ovmfNotFoundError(distroID string, secure bool) error {
	var installHint string
	switch distroID {
	case "fedora", "centos", "rhel", "rocky", "alma":
		installHint = "sudo dnf install edk2-ovmf"
	case "arch", "manjaro", "endeavouros":
		installHint = "sudo pacman -S edk2-ovmf"
	case "debian", "ubuntu":
		installHint = "sudo apt-get install ovmf"
	default:
		installHint = "install the 'edk2-ovmf' (Fedora/Arch) or 'ovmf' (Debian/Ubuntu) package"
	}
	mode := "insecure"
	if secure {
		mode = "secure"
	}
	return fmt.Errorf("OVMF firmware (%s) not found for host distro %q; %s", mode, distroID, installHint)
}

// EnsurePerVmNvram copies the OVMF_VARS template to a per-VM NVRAM
// file on first use. Returns the absolute path of the per-VM NVRAM
// (which is what rt.NVRAMPath should be set to). Idempotent: if the
// per-VM file already exists, it's preserved (contains the guest's
// accumulated UEFI variables).
func EnsurePerVmNvram(templatePath, perVmDir string) (string, error) {
	if err := os.MkdirAll(perVmDir, 0o755); err != nil {
		return "", fmt.Errorf("creating per-VM NVRAM dir: %w", err)
	}
	dst := filepath.Join(perVmDir, "nvram.fd")
	if _, err := os.Stat(dst); err == nil {
		// Already present — preserve accumulated UEFI variables.
		return dst, nil
	}
	src, err := os.Open(templatePath)
	if err != nil {
		return "", fmt.Errorf("opening OVMF_VARS template %q: %w", templatePath, err)
	}
	defer src.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating per-VM NVRAM file %q: %w", dst, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("copying OVMF_VARS template: %w", err)
	}
	return dst, nil
}

// ResolveOvmfForSpec is a convenience wrapper: detects the host distro,
// picks the correct OVMF_CODE/OVMF_VARS pair for the VmSpec's firmware
// setting, and provisions the per-VM NVRAM file. Returns (CodePath,
// NVRAMPath) — the two values needed to populate VmRuntimeParams.
//
// Returns ("", "", nil) when firmware == "bios" (BIOS boot needs no
// firmware images).
func ResolveOvmfForSpec(spec *VmSpec, vmStateDir string) (codePath, nvramPath string, err error) {
	if spec.Firmware == "" || spec.Firmware == "bios" {
		return "", "", nil
	}
	secure := spec.Firmware == "uefi-secure"

	hd, err := DetectHostDistro()
	if err != nil {
		return "", "", fmt.Errorf("ResolveOvmfForSpec: %w", err)
	}
	paths, err := ResolveOvmfPaths(hd.ID, secure)
	if err != nil {
		return "", "", err
	}
	nvram, err := EnsurePerVmNvram(paths.VarsTemplate, vmStateDir)
	if err != nil {
		return "", "", err
	}
	return paths.CodePath, nvram, nil
}
