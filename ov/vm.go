package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const libvirtSessionURI = "qemu:///session"

// VmCmd groups VM management subcommands.
type VmCmd struct {
	Build   VmBuildCmd   `cmd:"" help:"Build QCOW2/RAW disk image from bootc container"`
	Create  VmCreateCmd  `cmd:"" help:"Create a VM from a disk image"`
	Start   VmStartCmd   `cmd:"" help:"Start a VM"`
	Stop    VmStopCmd    `cmd:"" help:"Stop a VM (graceful shutdown)"`
	Destroy VmDestroyCmd `cmd:"" help:"Remove VM definition and optionally delete disk"`
	List    VmListCmd    `cmd:"" help:"List VMs and their status"`
	Console VmConsoleCmd `cmd:"" help:"Attach to VM serial console"`
	Ssh     VmSshCmd     `cmd:"" help:"SSH into a VM"`
}

// virshCmd creates an exec.Cmd for virsh with the session connection URI.
func virshCmd(args ...string) *exec.Cmd {
	fullArgs := append([]string{"-c", libvirtSessionURI}, args...)
	return exec.Command("virsh", fullArgs...)
}

// vmName returns the VM name for an image and optional instance.
func vmName(image, instance string) string {
	name := "ov-" + image
	if instance != "" {
		name += "-" + instance
	}
	return name
}

// vmDir returns the directory for storing VM state (QEMU backend).
func vmDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "ov", "vm"), nil
}

// resolveVmBackend detects the available VM backend.
// Priority: bcvk → libvirt → qemu
func resolveVmBackend(configured string) (string, error) {
	if configured == "bcvk" || configured == "auto" {
		if _, err := exec.LookPath("bcvk"); err == nil {
			if _, err := exec.LookPath("virsh"); err == nil {
				return "bcvk", nil
			}
		}
		if configured == "bcvk" {
			return "", fmt.Errorf("bcvk backend requires bcvk and virsh")
		}
	}
	if configured == "libvirt" || configured == "auto" {
		if _, err := exec.LookPath("virsh"); err == nil {
			if _, err := exec.LookPath("virt-install"); err == nil {
				return "libvirt", nil
			}
		}
		if configured == "libvirt" {
			return "", fmt.Errorf("libvirt backend requires virsh and virt-install")
		}
	}
	if configured == "qemu" || configured == "auto" {
		qemuBin := qemuSystemBinary()
		if _, err := exec.LookPath(qemuBin); err == nil {
			return "qemu", nil
		}
		if configured == "qemu" {
			return "", fmt.Errorf("qemu backend requires %s", qemuBin)
		}
	}
	return "", fmt.Errorf("no VM backend available (install bcvk, virsh+virt-install, or qemu-system)")
}

// qemuSystemBinary returns the architecture-appropriate QEMU binary name.
func qemuSystemBinary() string {
	switch runtime.GOARCH {
	case "arm64":
		return "qemu-system-aarch64"
	default:
		return "qemu-system-x86_64"
	}
}

// resolveQcow2Path finds the QCOW2 disk image for the given image name.
func resolveQcow2Path(image string) (string, error) {
	path := filepath.Join("output", "qcow2", "disk.qcow2")
	if _, err := os.Stat(path); err == nil {
		abs, _ := filepath.Abs(path)
		return abs, nil
	}
	return "", fmt.Errorf("QCOW2 not found for %q — run 'ov vm build %s' first", image, image)
}

// --- VmCreateCmd ---

// VmCreateCmd creates a VM from a QCOW2 disk image.
type VmCreateCmd struct {
	Image    string `arg:"" help:"Image name"`
	Ram      string `long:"ram" help:"Override RAM size (e.g. 4G, 8192M)"`
	Cpus     int    `long:"cpus" help:"Override CPU count"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	GPUFlags `embed:""`
}

func (c *VmCreateCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	// Resolve VM settings from images.yml or image labels
	dir, _ := os.Getwd()
	ram := "4G"
	cpus := 2
	var ports []string
	var imageRef string
	var vmCfg *VmConfig
	var libvirtSnippets []string

	var cfg *Config
	if loadedCfg, cfgErr := LoadConfig(dir); cfgErr == nil {
		cfg = loadedCfg
		calverTag := "latest"
		if resolved, resolveErr := cfg.ResolveImage(c.Image, calverTag); resolveErr == nil {
			if resolved.Vm != nil {
				ram = resolved.Vm.Ram
				cpus = resolved.Vm.Cpus
				vmCfg = resolved.Vm
			}
			ports = resolved.Ports
			imageRef = resolved.FullTag
		}

		// Collect libvirt snippets from layers and image config
		if layers, scanErr := ScanAllLayersWithConfig(dir, cfg); scanErr == nil {
			libvirtSnippets = CollectLibvirtSnippets(cfg, layers, c.Image)
		}
	} else {
		// Label path
		engine := rt.RunEngine
		ref := fmt.Sprintf("%s:latest", c.Image)
		meta, metaErr := ExtractMetadata(engine, ref)
		if metaErr == nil && meta != nil {
			dc, _ := LoadDeployConfig()
			MergeDeployOntoMetadata(meta, dc)
			if meta.Vm != nil {
				ram = meta.Vm.Ram
				cpus = meta.Vm.Cpus
				vmCfg = meta.Vm
			}
			ports = meta.Ports
			libvirtSnippets = meta.Libvirt
			if meta.Registry != "" {
				imageRef = fmt.Sprintf("%s/%s:latest", meta.Registry, c.Image)
			} else {
				imageRef = ref
			}
		}
	}

	// CLI flags override config
	if c.Ram != "" {
		ram = c.Ram
	}
	if c.Cpus > 0 {
		cpus = c.Cpus
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "bcvk":
		if err := c.createBcvk(name, imageRef, ram, cpus, ports, vmCfg); err != nil {
			return err
		}
	case "libvirt":
		qcow2, err := resolveQcow2Path(c.Image)
		if err != nil {
			return err
		}
		if err := c.createLibvirt(name, qcow2, ram, cpus, ports); err != nil {
			return err
		}
	case "qemu":
		qcow2, err := resolveQcow2Path(c.Image)
		if err != nil {
			return err
		}
		if len(libvirtSnippets) > 0 {
			fmt.Fprintf(os.Stderr, "Warning: libvirt snippets are not supported with the QEMU backend (skipping %d snippet(s))\n", len(libvirtSnippets))
		}
		return c.createQemu(name, qcow2, ram, cpus, ports)
	}

	// Inject libvirt XML snippets for bcvk/libvirt backends
	if len(libvirtSnippets) > 0 {
		if err := InjectLibvirtXML(name, libvirtSnippets); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to inject libvirt config: %v\n", err)
		}
	}

	return nil
}

func (c *VmCreateCmd) createBcvk(name, imageRef, ram string, cpus int, ports []string, vmCfg *VmConfig) error {
	args := []string{
		"libvirt", "run",
		"--name", name,
		"--memory", normalizeSizeForBcvk(ram),
		"--cpus", strconv.Itoa(cpus),
		"--detach",
	}
	if vmCfg != nil {
		if vmCfg.Rootfs != "" {
			args = append(args, "--filesystem", vmCfg.Rootfs)
		}
		if vmCfg.DiskSize != "" {
			args = append(args, "--disk-size", normalizeSizeForBcvk(vmCfg.DiskSize))
		}
		if vmCfg.RootSize != "" {
			args = append(args, "--root-size", normalizeSizeForBcvk(vmCfg.RootSize))
		}
		if vmCfg.KernelArgs != "" {
			for _, karg := range strings.Fields(vmCfg.KernelArgs) {
				args = append(args, "--karg", karg)
			}
		}
		if vmCfg.Firmware != "" {
			args = append(args, "--firmware", vmCfg.Firmware)
		}
	}
	for _, p := range ports {
		args = append(args, "-p", p)
	}
	args = append(args, imageRef)

	fmt.Fprintf(os.Stderr, "Creating VM %s via bcvk...\n", name)
	cmd := exec.Command("bcvk", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("bcvk libvirt run failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created VM %s (bcvk)\n", name)
	fmt.Fprintf(os.Stderr, "SSH: ov vm ssh %s\n", c.Image)
	return nil
}

func (c *VmCreateCmd) createLibvirt(name, qcow2, ram string, cpus int, ports []string) error {
	// Convert RAM to MB for virt-install
	ramMB := parseRAMtoMB(ram)

	args := []string{
		"--connect", libvirtSessionURI,
		"--name", name,
		"--memory", strconv.Itoa(ramMB),
		"--vcpus", strconv.Itoa(cpus),
		"--disk", fmt.Sprintf("path=%s,format=qcow2,bus=virtio", qcow2),
		"--import",
		"--noautoconsole",
		"--os-variant", "fedora-unknown",
		"--network", fmt.Sprintf("user,model=virtio,%s", libvirtPortForwards(ports)),
		"--graphics", "none",
		"--serial", "pty",
		"--console", "pty,target_type=serial",
	}

	gpu := ResolveGPU(c.GPUFlags.Mode())
	if gpu {
		// TODO: PCI passthrough for GPU requires device identification
		fmt.Fprintf(os.Stderr, "Warning: GPU passthrough for libvirt VMs requires manual --host-device configuration\n")
	}

	cmd := exec.Command("virt-install", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("virt-install failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created VM %s (session connection)\n", name)
	fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", c.Image)
	return nil
}

func (c *VmCreateCmd) createQemu(name, qcow2, ram string, cpus int, ports []string) error {
	dir, err := vmDir()
	if err != nil {
		return err
	}
	stateDir := filepath.Join(dir, name)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}

	qemuBin := qemuSystemBinary()
	monitorSocket := filepath.Join(stateDir, "monitor.sock")

	args := []string{
		"-m", ram,
		"-smp", strconv.Itoa(cpus),
		"-cpu", "host",
		"-enable-kvm",
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", qcow2),
		"-monitor", fmt.Sprintf("unix:%s,server,nowait", monitorSocket),
		"-serial", "mon:stdio",
		"-nographic",
		"-daemonize",
		"-pidfile", filepath.Join(stateDir, "qemu.pid"),
	}

	// Port forwarding
	hostfwds := "hostfwd=tcp::2222-:22" // always forward SSH
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			hostfwds += fmt.Sprintf(",hostfwd=tcp::%s-:%s", parts[0], parts[1])
		}
	}

	if runtime.GOARCH == "arm64" {
		args = append([]string{"-machine", "virt"}, args...)
	}

	args = append(args, "-nic", "user,model=virtio-net-pci,"+hostfwds)

	// Save command for later use
	cmdLine := qemuBin + " " + strings.Join(args, " ")
	cmdFile := filepath.Join(stateDir, "command")
	if err := os.WriteFile(cmdFile, []byte(cmdLine), 0644); err != nil {
		return err
	}

	// Start the VM
	cmd := exec.Command(qemuBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created and started VM %s (QEMU)\n", name)
	fmt.Fprintf(os.Stderr, "SSH: ssh -p 2222 user@localhost\n")
	fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", c.Image)
	return nil
}

// libvirtPortForwards builds the hostfwd string for libvirt user-mode networking.
func libvirtPortForwards(ports []string) string {
	hostfwds := "hostfwd=tcp::2222-:22" // always forward SSH
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			hostfwds += fmt.Sprintf(",hostfwd=tcp::%s-:%s", parts[0], parts[1])
		}
	}
	return hostfwds
}

// parseRAMtoMB converts a RAM string like "4G" or "8192M" to megabytes.
func parseRAMtoMB(ram string) int {
	ram = strings.TrimSpace(ram)
	if strings.HasSuffix(ram, "G") || strings.HasSuffix(ram, "g") {
		val, err := strconv.Atoi(strings.TrimRight(ram, "Gg"))
		if err == nil {
			return val * 1024
		}
	}
	if strings.HasSuffix(ram, "M") || strings.HasSuffix(ram, "m") {
		val, err := strconv.Atoi(strings.TrimRight(ram, "Mm"))
		if err == nil {
			return val
		}
	}
	// Try plain number (assume MB)
	val, err := strconv.Atoi(ram)
	if err == nil {
		return val
	}
	return 4096 // fallback 4G
}

// --- VmStartCmd ---

type VmStartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VmStartCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "bcvk":
		cmd := exec.Command("bcvk", "libvirt", "start", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("bcvk libvirt start failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Started VM %s\n", name)
	case "libvirt":
		cmd := virshCmd("start", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("virsh start failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Started VM %s\n", name)
	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)
		cmdFile := filepath.Join(stateDir, "command")
		data, err := os.ReadFile(cmdFile)
		if err != nil {
			return fmt.Errorf("VM %s not found — run 'ov vm create %s' first", name, c.Image)
		}
		parts := strings.Fields(string(data))
		if len(parts) < 2 {
			return fmt.Errorf("invalid stored command for VM %s", name)
		}
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("qemu start failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Started VM %s\n", name)
	}
	return nil
}

// --- VmStopCmd ---

type VmStopCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Force    bool   `long:"force" help:"Force stop (destroy) instead of graceful shutdown"`
}

func (c *VmStopCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "bcvk":
		args := []string{"libvirt", "stop"}
		if c.Force {
			args = append(args, "-f")
		}
		args = append(args, name)
		cmd := exec.Command("bcvk", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("bcvk libvirt stop failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	case "libvirt":
		if c.Force {
			cmd := virshCmd("destroy", name)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
		} else {
			cmd := virshCmd("shutdown", name)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("virsh shutdown failed: %w", err)
			}
		}
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		pidFile := filepath.Join(dir, name, "qemu.pid")
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return fmt.Errorf("VM %s is not running (no PID file)", name)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("invalid PID file for VM %s", name)
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("process %d not found", pid)
		}
		if c.Force {
			proc.Kill()
		} else {
			// Send SIGTERM for graceful shutdown
			proc.Signal(syscall.SIGTERM)
		}
		os.Remove(pidFile)
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	}
	return nil
}

// --- VmDestroyCmd ---

type VmDestroyCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Disk     bool   `long:"disk" help:"Also delete the QCOW2 disk image"`
}

func (c *VmDestroyCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "bcvk":
		cmd := exec.Command("bcvk", "libvirt", "rm", "-f", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("bcvk libvirt rm failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)

	case "libvirt":
		// Stop if running
		stop := virshCmd("destroy", name)
		_ = stop.Run()

		// Undefine
		args := []string{"undefine", name}
		if c.Disk {
			args = append(args, "--remove-all-storage")
		}
		cmd := virshCmd(args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("virsh undefine failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)

	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)

		// Kill process if running
		pidFile := filepath.Join(stateDir, "qemu.pid")
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if proc, err := os.FindProcess(pid); err == nil {
					proc.Kill()
				}
			}
		}

		// Remove state directory
		os.RemoveAll(stateDir)
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)
	}

	if c.Disk {
		// Remove QCOW2 output
		qcow2Dir := filepath.Join("output", "qcow2")
		os.RemoveAll(qcow2Dir)
		fmt.Fprintf(os.Stderr, "Deleted disk images in %s\n", qcow2Dir)
	}

	return nil
}

// --- VmListCmd ---

type VmListCmd struct {
	All bool `short:"a" long:"all" help:"Show all VMs including stopped"`
}

func (c *VmListCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	switch backend {
	case "bcvk":
		args := []string{"libvirt", "list"}
		if c.All {
			args = append(args, "--all")
		}
		cmd := exec.Command("bcvk", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	case "libvirt":
		cmd := virshCmd("list", "--all")
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("virsh list failed: %w", err)
		}
		// Filter for ov- prefixed VMs
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		headerPrinted := false
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "ov-") {
				if !headerPrinted {
					fmt.Println("NAME\tSTATE")
					headerPrinted = true
				}
				fields := strings.Fields(line)
				if len(fields) >= 3 {
					name := fields[1]
					state := strings.Join(fields[2:], " ")
					fmt.Printf("%s\t%s\n", name, state)
				}
			}
		}
		if !headerPrinted {
			fmt.Fprintln(os.Stderr, "No VMs found")
		}

	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "No VMs found")
				return nil
			}
			return err
		}

		headerPrinted := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			pidFile := filepath.Join(dir, name, "qemu.pid")
			state := "stopped"
			if data, err := os.ReadFile(pidFile); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
					if proc, err := os.FindProcess(pid); err == nil {
						// Check if process exists
						if err := proc.Signal(syscall.Signal(0)); err == nil {
							state = "running"
						}
					}
				}
			}
			if !headerPrinted {
				fmt.Println("NAME\tSTATE")
				headerPrinted = true
			}
			fmt.Printf("%s\t%s\n", name, state)
		}
		if !headerPrinted {
			fmt.Fprintln(os.Stderr, "No VMs found")
		}
	}
	return nil
}

// --- VmConsoleCmd ---

type VmConsoleCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VmConsoleCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "bcvk":
		// bcvk uses libvirt, so delegate to virsh console
		bin, err := exec.LookPath("virsh")
		if err != nil {
			return err
		}
		return syscall.Exec(bin, []string{"virsh", "-c", libvirtSessionURI, "console", name}, os.Environ())
	case "libvirt":
		bin, err := exec.LookPath("virsh")
		if err != nil {
			return err
		}
		return syscall.Exec(bin, []string{"virsh", "-c", libvirtSessionURI, "console", name}, os.Environ())

	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		monitorSocket := filepath.Join(dir, name, "monitor.sock")
		if _, err := os.Stat(monitorSocket); err != nil {
			return fmt.Errorf("VM %s monitor socket not found — is the VM running?", name)
		}
		// Use socat to connect to the monitor socket
		socatBin, err := exec.LookPath("socat")
		if err != nil {
			return fmt.Errorf("socat is required for QEMU console access: %w", err)
		}
		return syscall.Exec(socatBin, []string{"socat", "stdio", "unix-connect:" + monitorSocket}, os.Environ())
	}
	return nil
}

// --- VmSshCmd ---

type VmSshCmd struct {
	Image    string   `arg:"" help:"Image name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Port     int      `short:"p" long:"port" default:"2222" help:"SSH port on host"`
	User     string   `short:"l" long:"user" default:"user" help:"SSH username"`
	Args     []string `arg:"" optional:"" help:"Additional SSH arguments or command"`
}

func (c *VmSshCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	backend, err := resolveVmBackend(rt.VmBackend)
	if err != nil {
		return err
	}

	// Resolve SSH port and user from images.yml if not explicitly overridden
	dir, _ := os.Getwd()
	if cfg, cfgErr := LoadConfig(dir); cfgErr == nil {
		if resolved, resolveErr := cfg.ResolveImage(c.Image, "latest"); resolveErr == nil {
			if resolved.Vm != nil && resolved.Vm.SshPort != 0 && c.Port == 2222 {
				c.Port = resolved.Vm.SshPort
			}
		}
	}

	name := vmName(c.Image, c.Instance)

	if backend == "bcvk" {
		bcvkBin, err := exec.LookPath("bcvk")
		if err != nil {
			return fmt.Errorf("bcvk not found: %w", err)
		}
		args := []string{"bcvk", "libvirt", "ssh"}
		if c.User != "user" {
			args = append(args, "--user", c.User)
		}
		args = append(args, name)
		// Pass through remaining args, filtering out the "--" separator
		for _, a := range c.Args {
			if a != "--" {
				args = append(args, a)
			}
		}
		return syscall.Exec(bcvkBin, args, os.Environ())
	}

	// libvirt and qemu backends use direct SSH
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	args := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(c.Port),
		fmt.Sprintf("%s@localhost", c.User),
	}
	args = append(args, c.Args...)

	return syscall.Exec(sshBin, args, os.Environ())
}
