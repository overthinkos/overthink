package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runVmSpecCreate is the VmCreateCmd.Run branch for kind:vm entities.
// Produces either a libvirt domain (via RenderDomain + virDomainDefine)
// or a QEMU process (via RenderQemuArgv + exec) depending on the
// resolved backend. Pre-conditions: `ov vm build <vm-name>` has run,
// placing disk.qcow2 (+ seed.iso for cloud_image sources) under
// output/qcow2/.
func (c *VmCreateCmd) runVmSpecCreate(vmName string, spec *VmSpec, backend string) error {
	name := vmName
	if c.Instance != "" {
		name = vmName + "-" + c.Instance
	}
	vmDomainName := "ov-" + name

	// Locate prebuilt disk + seed ISO.
	qcow2 := filepath.Join("output", "qcow2", "disk.qcow2")
	if _, err := os.Stat(qcow2); err != nil {
		return fmt.Errorf("disk.qcow2 not found at %s — run `ov vm build %s` first", qcow2, vmName)
	}
	qcow2Abs, _ := filepath.Abs(qcow2)

	seedISO := filepath.Join("output", "qcow2", "seed.iso")
	seedISOAbs := ""
	if _, err := os.Stat(seedISO); err == nil {
		seedISOAbs, _ = filepath.Abs(seedISO)
	}

	// Resolve SSH pubkey (honoring spec.SSH.KeySource).
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	vmStateDir := filepath.Join(home, ".local", "share", "ov", "vm", vmDomainName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return err
	}

	// For cloud_image sources, always regenerate the seed ISO so vms.yml
	// edits (cloud_init packages/runcmd/network-config/etc.) take effect on
	// `ov vm create` without forcing an explicit `ov vm build`. The qcow2
	// disk is left alone — only the seed ISO is cheap to rebuild.
	if spec.Source.Kind == "cloud_image" && seedISOAbs != "" {
		var existingState *VmDeployState
		if dc, _ := LoadDeployConfig(); dc != nil {
			if entry, ok := dc.Deployment["vm:"+vmName]; ok {
				existingState = entry.VmState
			}
		}
		if err := RegenerateSeedISO(spec, seedISOAbs, vmStateDir, existingState); err != nil {
			return fmt.Errorf("regenerating seed ISO: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Regenerated cloud-init seed ISO from vms.yml\n")
	}
	pubKey, err := resolveSSHPubKeyForSpec(spec, vmStateDir)
	if err != nil {
		return err
	}

	// Apply D13 key-injection resolution. The credential targets the
	// SSH user the spec asks for — root for bootc/legacy paths, the
	// named user for cloud_image / bootstrap VMs. The image MUST
	// already have the user account created (via cloud_image base or
	// bootloader install template); this only delivers the per-VM key.
	smbiosOn, _ := ResolveKeyInjectionChannels(spec)
	var smbiosCreds []string
	if smbiosOn && pubKey != "" {
		smbiosCreds = append(smbiosCreds, SmbiosCredForSSH(resolveVmSshUser(spec), "", pubKey))
	}

	// Resolve D17 firmware paths + per-VM NVRAM.
	ovmfCode, nvramPath, err := ResolveOvmfForSpec(spec, vmStateDir)
	if err != nil {
		return fmt.Errorf("resolving firmware: %w", err)
	}

	// Compose VmRuntimeParams.
	rt := VmRuntimeParams{
		Name:              vmDomainName,
		QCOW2Path:         qcow2Abs,
		SeedISOPath:       seedISOAbs,
		NVRAMPath:         nvramPath,
		OVMFCodePath:      ovmfCode,
		HostArch:          hostArchRuntime(),
		HostCPUVendor:     detectRuntimeHostVendor(),
		SMBIOSCredentials: smbiosCreds,
		RamMB:             parseRAMtoMB(resolveVmRam(spec)),
		Cpus:              resolveVmCpus(spec),
		SshPort:           resolveVmSshPort(spec),
		VmStateDir:        vmStateDir,
	}
	// ExtraPortForwards intentionally empty — spec.Network.PortForwards is
	// already read by the renderers directly. Populating rt here would
	// duplicate every entry.

	// Backend dispatch.
	switch backend {
	case "libvirt":
		xmlStr, err := RenderDomainXML(spec, rt)
		if err != nil {
			return fmt.Errorf("rendering domain XML for %s: %w", vmDomainName, err)
		}
		conn, err := connectLibvirt("")
		if err != nil {
			return fmt.Errorf("connecting to libvirt: %w", err)
		}
		defer conn.Close()
		if err := conn.defineAndStartDomain(xmlStr); err != nil {
			return fmt.Errorf("creating VM %s: %w", vmDomainName, err)
		}
		fmt.Fprintf(os.Stderr, "Created VM %s (libvirt session)\n", vmDomainName)

		// Inject any raw libvirt snippets from layers/spec.libvirt.snippets.
		if spec.Libvirt != nil && len(spec.Libvirt.Snippets) > 0 {
			if err := InjectLibvirtXML(vmDomainName, spec.Libvirt.Snippets); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: libvirt snippet injection: %v\n", err)
			}
		}
		fmt.Fprintf(os.Stderr, "SSH: ssh -p %d %s@127.0.0.1\n", rt.SshPort, resolveVmSshUser(spec))
		fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", vmName)
		return nil

	case "qemu":
		if spec.Libvirt != nil && len(spec.Libvirt.Snippets) > 0 {
			fmt.Fprintf(os.Stderr, "Warning: libvirt snippets are not supported with the QEMU backend (skipping %d snippet(s))\n", len(spec.Libvirt.Snippets))
		}

		// Prepare QEMU runtime paths.
		monitorSocket := filepath.Join(vmStateDir, "monitor.sock")
		qmpSocket := filepath.Join(vmStateDir, "qmp.sock")
		consoleSocket := filepath.Join(vmStateDir, "console.sock")
		pidFile := filepath.Join(vmStateDir, "qemu.pid")
		qemuPaths := QemuRuntimePaths{
			MonitorSocket: monitorSocket,
			QmpSocket:     qmpSocket,
			ConsoleSocket: consoleSocket,
			PidFile:       pidFile,
		}

		args := RenderQemuArgv(spec, rt, qemuPaths)
		bin := qemuSystemBinary()

		// Persist the command line so `ov vm start <name>` can relaunch.
		cmdLine := bin + " " + strings.Join(args, " ")
		if err := os.WriteFile(filepath.Join(vmStateDir, "command"), []byte(cmdLine), 0o644); err != nil {
			return err
		}

		cmd := exec.Command(bin, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("qemu failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Created and started VM %s (QEMU)\n", vmDomainName)
		fmt.Fprintf(os.Stderr, "SSH: ssh -p %d %s@127.0.0.1\n", rt.SshPort, resolveVmSshUser(spec))
		fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", vmName)
		return nil
	}
	return fmt.Errorf("unknown backend %q", backend)
}

// resolveVmRam picks the spec-declared RAM or falls back to "4G".
func resolveVmRam(spec *VmSpec) string {
	if spec.Ram != "" {
		return spec.Ram
	}
	return "4G"
}

// resolveVmCpus picks the spec-declared CPU count or falls back to 2.
func resolveVmCpus(spec *VmSpec) int {
	if spec.Cpus > 0 {
		return spec.Cpus
	}
	return 2
}

// detectRuntimeHostVendor reads /proc/cpuinfo to identify the host CPU
// vendor (GenuineIntel | AuthenticAMD | ""). Used by RenderDomain /
// RenderQemuArgv to auto-append the correct nested-virt feature (vmx
// vs svm) per D16.
func detectRuntimeHostVendor() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "vendor_id") {
			if idx := strings.Index(line, ":"); idx > 0 {
				return strings.TrimSpace(line[idx+1:])
			}
		}
	}
	return ""
}
