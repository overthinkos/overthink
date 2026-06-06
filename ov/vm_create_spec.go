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
// placing disk.qcow2 (+ seed.iso for cloud_image sources) under the per-VM
// disk dir output/qcow2/<vm>/.
func (c *VmCreateCmd) runVmSpecCreate(vmName string, spec *VmSpec, backend string) error {
	name := vmName
	if c.Instance != "" {
		name = vmName + "-" + c.Instance
	}
	vmDomainName := "ov-" + name

	// Merge this host's per-domain instance override (~/.local/share/ov/vm/
	// <domain>/instance.yml) onto the spec BEFORE any rendering. Its `libvirt:`
	// overlay carries the host-specific GPU <hostdev> + host-path virtiofs
	// shares the committed vm.yml deliberately omits, so the portable entity
	// attaches this host's real devices for a live run. No-op when absent.
	ovr, err := LoadVmInstanceOverride(vmDomainName)
	if err != nil {
		return fmt.Errorf("loading instance override for %s: %w", vmDomainName, err)
	}
	ovr.ApplyToVmSpec(spec)

	// Locate prebuilt disk + seed ISO in this VM's OWN per-VM disk dir, so a
	// fresh create can never adopt a sibling VM's stale disk/seed (whose
	// embedded SSH key would mismatch this VM's id_ed25519).
	qcow2 := filepath.Join(vmDiskDir(vmName), "disk.qcow2")
	if _, err := os.Stat(qcow2); err != nil {
		return fmt.Errorf("disk.qcow2 not found at %s — run `ov vm build %s` first", qcow2, vmName)
	}
	qcow2Abs, _ := filepath.Abs(qcow2)

	seedISO := filepath.Join(vmDiskDir(vmName), "seed.iso")
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

	// For cloud_image sources, always regenerate the seed ISO so vm.yml
	// edits (cloud_init packages/runcmd/network-config/etc.) take effect on
	// `ov vm create` without forcing an explicit `ov vm build`. The qcow2
	// disk is left alone — only the seed ISO is cheap to rebuild.
	if spec.Source.Kind == "cloud_image" && seedISOAbs != "" {
		var existingState *VmDeployState
		if entry, ok := loadDeployConfigForRead("ov vm create seed-iso").LookupKey("vm:" + vmName); ok {
			existingState = entry.VmState
		}
		if err := RegenerateSeedISO(spec, seedISOAbs, vmStateDir, existingState); err != nil {
			return fmt.Errorf("regenerating seed ISO: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Regenerated cloud-init seed ISO from vm.yml\n")
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

	// Compose VmRuntimeParams. SSH port resolves here so ssh.port_auto can
	// allocate (or reuse the persisted) host port before the libvirt forward
	// is rendered.
	sshPort, err := resolveVmSshPort(spec, vmName)
	if err != nil {
		return fmt.Errorf("resolving SSH port: %w", err)
	}
	// For ssh.port_auto, persist the resolved port NOW so deploy-add's
	// reachability probe (and every later read) reuses THIS exact port. The
	// auto-allocation must be stable across the vm-create → deploy-add sequence;
	// without persisting here deploy-add re-resolves, finds no persisted port,
	// allocates a DIFFERENT one, probes the wrong port, declares the VM
	// unreachable, and auto-boots `ov vm create` into an "already exists" error.
	if spec.SSH != nil && spec.SSH.PortAuto {
		deployKey := "vm:" + vmName
		st := &VmDeployState{}
		if entry, ok := loadDeployConfigForRead("ov vm create persist-auto-port").LookupKey(deployKey); ok && entry.VmState != nil {
			st = entry.VmState
		}
		st.SshPort = sshPort
		if err := saveVmDeployState(deployKey, st, spec); err != nil {
			return fmt.Errorf("persisting auto-allocated ssh port: %w", err)
		}
	}
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
		SshPort:           sshPort,
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

		// Boot-autostart: set the libvirt flag, then wire the session-boot
		// trigger (linger + user socket). The flag is load-bearing; the
		// prereqs are best-effort with actionable warnings on failure.
		if spec.Autostart {
			if err := conn.setDomainAutostart(vmDomainName, true); err != nil {
				return fmt.Errorf("enabling autostart for %s: %w", vmDomainName, err)
			}
			fmt.Fprintf(os.Stderr, "Autostart enabled for %s\n", vmDomainName)
			ensureBootAutostartPrereqs(vmDomainName)
		}

		// Inject any raw libvirt snippets from candy/spec.libvirt.snippets.
		if spec.Libvirt != nil && len(spec.Libvirt.Snippets) > 0 {
			if err := InjectLibvirtXML(vmDomainName, spec.Libvirt.Snippets); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: libvirt snippet injection: %v\n", err)
			}
		}
		if err := publishVmSshAlias(home, vmName, name, spec, rt); err != nil {
			return fmt.Errorf("publishing ssh-config alias: %w", err)
		}
		fmt.Fprintf(os.Stderr, "SSH: ssh %s    (managed alias resolves user/port/key from ~/.config/ov/ssh_config)\n", VmSshAlias(name))
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
		if err := publishVmSshAlias(home, vmName, name, spec, rt); err != nil {
			return fmt.Errorf("publishing ssh-config alias: %w", err)
		}
		fmt.Fprintf(os.Stderr, "SSH: ssh %s    (managed alias resolves user/port/key from ~/.config/ov/ssh_config)\n", VmSshAlias(name))
		fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", vmName)
		return nil
	}
	return fmt.Errorf("unknown backend %q", backend)
}

// publishVmSshAlias writes (or refreshes) the managed ssh-config Host
// stanza for this VM and ensures the Include line is present in the
// user's ~/.ssh/config. Idempotent — safe to call on every `ov vm
// create` invocation including reruns.
//
// Clears the per-VM known_hosts file as part of the refresh: each
// `ov vm create` boots a fresh guest (or recreates a destroyed one)
// whose sshd regenerates its host key on first boot. The stale entry
// in known_hosts from a previous incarnation would trigger ssh's
// "REMOTE HOST IDENTIFICATION HAS CHANGED" rejection — and because
// the stanza sets `StrictHostKeyChecking accept-new`, ssh accepts
// brand-new keys but REFUSES changed ones. Clearing on every create
// matches the disposable-VM semantics: the on-disk state machine
// resets to empty when the domain is recreated. The first ssh after
// vm create writes the new key into known_hosts.
//
// Without this fix, dispatcher loops that destroy + recreate VMs
// (`ov eval run eval-k3s-vm`, `ov update <vm-bed>`) fail at the post-create
// SSH step with "Host key verification failed", which surfaces in
// VmDeployTarget.WaitForSSH as "Could not resolve hostname" — see the
// 2026-05-06 R10 follow-up RCA.
func publishVmSshAlias(home, vmName, deployName string, spec *VmSpec, rt VmRuntimeParams) error {
	stateDir := filepath.Join(home, ".local", "share", "ov", "vm", "ov-"+vmName)
	knownHostsPath := filepath.Join(stateDir, "known_hosts")
	// Best-effort: ignore "no such file" on first-create.
	_ = os.Remove(knownHostsPath)
	stanza := VmSshStanza{
		Alias:          VmSshAlias(deployName),
		Hostname:       "127.0.0.1",
		Port:           rt.SshPort,
		User:           resolveVmSshUser(spec),
		IdentityFile:   filepath.Join(stateDir, "id_ed25519"),
		KnownHostsFile: knownHostsPath,
	}
	if err := WriteVmSshStanza(home, stanza); err != nil {
		return err
	}
	return EnsureSshConfigInclude(home)
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
