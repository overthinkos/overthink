package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
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
// Priority: libvirt → qemu
func resolveVmBackend(configured string) (string, error) {
	if configured == "libvirt" || configured == "auto" {
		// Check for libvirt session socket
		sockPath := libvirtSessionSocket()
		if _, err := os.Stat(sockPath); err == nil {
			return "libvirt", nil
		}
		if configured == "libvirt" {
			return "", fmt.Errorf("libvirt backend requires libvirt session daemon (socket not found at %s)", sockPath)
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
	return "", fmt.Errorf("no VM backend available (install libvirt or qemu-system)")
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

// qemuMachineType returns the architecture-appropriate QEMU machine type.
func qemuMachineType() string {
	switch runtime.GOARCH {
	case "arm64":
		return "virt"
	default:
		return "q35"
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
	SshKey   string `long:"ssh-key" default:"auto" help:"SSH public key: path to .pub file, 'auto' (default ~/.ssh key), 'generate', or 'none'"`
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
	var libvirtSnippets []string

	var cfg *Config
	if loadedCfg, cfgErr := LoadConfig(dir); cfgErr == nil {
		cfg = loadedCfg
		calverTag := "latest"
		if resolved, resolveErr := cfg.ResolveImage(c.Image, calverTag); resolveErr == nil {
			if resolved.Vm != nil {
				ram = resolved.Vm.Ram
				cpus = resolved.Vm.Cpus
			}
			ports = resolved.Ports
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
			}
			ports = meta.Ports
			libvirtSnippets = meta.Libvirt
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

	// Resolve SSH public key for SMBIOS credential injection
	sshPubKey, err := resolveSSHPubKey(c.SshKey, name)
	if err != nil {
		return err
	}

	switch backend {
	case "libvirt":
		qcow2, err := resolveQcow2Path(c.Image)
		if err != nil {
			return err
		}
		if err := c.createLibvirt(name, qcow2, ram, cpus, ports, sshPubKey); err != nil {
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
		return c.createQemu(name, qcow2, ram, cpus, ports, sshPubKey)
	}

	// Inject libvirt XML snippets for libvirt backend
	if len(libvirtSnippets) > 0 {
		if err := InjectLibvirtXML(name, libvirtSnippets); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to inject libvirt config: %v\n", err)
		}
	}

	return nil
}

func (c *VmCreateCmd) createLibvirt(name, qcow2, ram string, cpus int, ports []string, sshPubKey string) error {
	ramMB := parseRAMtoMB(ram)

	gpu := ResolveGPU(c.GPUFlags.Mode())
	if gpu {
		fmt.Fprintf(os.Stderr, "Warning: GPU passthrough for libvirt VMs requires manual --host-device configuration\n")
	}

	var smbiosCreds []string
	if sshPubKey != "" {
		smbiosCreds = append(smbiosCreds, SmbiosCredForRootSSH(sshPubKey))
		fmt.Fprintf(os.Stderr, "Injecting SSH key via SMBIOS credential\n")
	}

	xmlStr := buildDomainXML(name, qcow2, ramMB, cpus, ports, gpu, smbiosCreds...)

	conn, err := connectLibvirt()
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close()

	if err := conn.defineAndStartDomain(xmlStr); err != nil {
		return fmt.Errorf("creating VM: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Created VM %s (libvirt session)\n", name)
	fmt.Fprintf(os.Stderr, "Console: ov vm console %s\n", c.Image)
	return nil
}

func (c *VmCreateCmd) createQemu(name, qcow2, ram string, cpus int, ports []string, sshPubKey string) error {
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
	qmpSocket := filepath.Join(stateDir, "qmp.sock")

	args := []string{
		"-machine", qemuMachineType(),
		"-m", ram,
		"-smp", strconv.Itoa(cpus),
		"-cpu", "host",
		"-enable-kvm",
		"-drive", fmt.Sprintf("file=%s,format=qcow2,if=virtio", qcow2),
		"-monitor", fmt.Sprintf("unix:%s,server,nowait", monitorSocket),
		"-qmp", fmt.Sprintf("unix:%s,server,nowait", qmpSocket),
		"-serial", fmt.Sprintf("unix:%s,server,nowait", filepath.Join(stateDir, "console.sock")),
		"-nographic",
		"-daemonize",
		"-pidfile", filepath.Join(stateDir, "qemu.pid"),
	}

	// SSH key injection via systemd credentials (SMBIOS type 11)
	if sshPubKey != "" {
		cred := SmbiosCredForRootSSH(sshPubKey)
		args = append(args, "-smbios", fmt.Sprintf("type=11,value=%s", cred))
		fmt.Fprintf(os.Stderr, "Injecting SSH key via SMBIOS credential\n")
	}

	// Port forwarding
	hostfwds := "hostfwd=tcp::2222-:22"
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			hostfwds += fmt.Sprintf(",hostfwd=tcp::%s-:%s", parts[0], parts[1])
		}
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
	case "libvirt":
		conn, err := connectLibvirt()
		if err != nil {
			return err
		}
		defer conn.Close()

		dom, err := conn.lookupDomain(name)
		if err != nil {
			return fmt.Errorf("VM %s not found: %w", name, err)
		}
		if err := conn.startDomain(dom); err != nil {
			return fmt.Errorf("starting VM %s: %w", name, err)
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
	case "libvirt":
		conn, err := connectLibvirt()
		if err != nil {
			return err
		}
		defer conn.Close()

		dom, err := conn.lookupDomain(name)
		if err != nil {
			return fmt.Errorf("VM %s not found: %w", name, err)
		}
		if c.Force {
			_ = conn.destroyDomain(dom)
		} else {
			if err := conn.shutdownDomain(dom); err != nil {
				return fmt.Errorf("shutting down VM %s: %w", name, err)
			}
		}
		fmt.Fprintf(os.Stderr, "Stopped VM %s\n", name)
	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)
		if c.Force {
			// Try QMP quit first, fall back to process kill
			if err := qemuForceShutdown(stateDir); err != nil {
				// Fallback: kill via PID
				killQemuByPID(stateDir)
			}
		} else {
			// Graceful ACPI shutdown via QMP
			if err := qemuGracefulShutdown(stateDir); err != nil {
				// Fallback: SIGTERM via PID
				pidFile := filepath.Join(stateDir, "qemu.pid")
				if data, readErr := os.ReadFile(pidFile); readErr == nil {
					if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
						if proc, findErr := os.FindProcess(pid); findErr == nil {
							proc.Signal(syscall.SIGTERM)
						}
					}
				}
			}
		}
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
	case "libvirt":
		conn, err := connectLibvirt()
		if err != nil {
			return err
		}
		defer conn.Close()

		dom, err := conn.lookupDomain(name)
		if err != nil {
			return fmt.Errorf("VM %s not found: %w", name, err)
		}

		// Stop if running
		state, _ := conn.domainState(dom)
		if state == domainStateRunning {
			_ = conn.destroyDomain(dom)
		}

		// Undefine
		if err := conn.undefineDomain(dom, c.Disk); err != nil {
			return fmt.Errorf("undefining VM %s: %w", name, err)
		}
		fmt.Fprintf(os.Stderr, "Destroyed VM %s\n", name)

	case "qemu":
		dir, err := vmDir()
		if err != nil {
			return err
		}
		stateDir := filepath.Join(dir, name)

		// Kill process — try QMP quit first, fall back to PID kill
		if err := qemuForceShutdown(stateDir); err != nil {
			killQemuByPID(stateDir)
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
	case "libvirt":
		conn, err := connectLibvirt()
		if err != nil {
			return err
		}
		defer conn.Close()

		domains, err := conn.listOvDomains()
		if err != nil {
			return fmt.Errorf("listing VMs: %w", err)
		}
		if len(domains) == 0 {
			fmt.Fprintln(os.Stderr, "No VMs found")
			return nil
		}
		fmt.Println("NAME\tSTATE")
		for _, d := range domains {
			fmt.Printf("%s\t%s\n", d.Name, d.State)
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
	case "libvirt":
		// Keep virsh console for interactive serial — libvirt console streams are complex
		bin, err := exec.LookPath("virsh")
		if err != nil {
			return fmt.Errorf("virsh is required for libvirt console access: %w", err)
		}
		return syscall.Exec(bin, []string{"virsh", "-c", libvirtSessionURI, "console", name}, os.Environ())

	case "qemu":
		// Pure Go unix socket relay (replaces socat)
		dir, err := vmDir()
		if err != nil {
			return err
		}
		monitorSocket := filepath.Join(dir, name, "monitor.sock")
		if _, err := os.Stat(monitorSocket); err != nil {
			return fmt.Errorf("VM %s monitor socket not found — is the VM running?", name)
		}
		return connectUnixConsole(monitorSocket)
	}
	return nil
}

// connectUnixConsole connects stdin/stdout to a unix socket in raw terminal mode.
// This replaces the socat dependency for QEMU console access.
func connectUnixConsole(socketPath string) error {
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", socketPath, err)
	}
	defer conn.Close()

	// Switch terminal to raw mode
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return fmt.Errorf("setting raw terminal mode: %w", err)
		}
		defer term.Restore(fd, oldState)
	}

	// Bidirectional copy
	done := make(chan struct{})
	go func() {
		io.Copy(conn, os.Stdin)
		close(done)
	}()
	io.Copy(os.Stdout, conn)
	<-done
	return nil
}

// resolveSSHPubKey resolves the --ssh-key flag to a public key string.
// Values: "auto" (default ~/.ssh key), "none", "generate", or a file path.
func resolveSSHPubKey(flag, vmName string) (string, error) {
	switch flag {
	case "none":
		return "", nil
	case "auto":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		for _, name := range []string{"id_ed25519.pub", "id_rsa.pub", "id_ecdsa.pub"} {
			path := filepath.Join(home, ".ssh", name)
			if data, err := os.ReadFile(path); err == nil {
				pubkey := strings.TrimSpace(string(data))
				fmt.Fprintf(os.Stderr, "Using SSH key from %s\n", path)
				return pubkey, nil
			}
		}
		return "", fmt.Errorf("no SSH public key found in ~/.ssh/ — use --ssh-key <path> or --ssh-key generate")
	case "generate":
		dir, err := vmDir()
		if err != nil {
			return "", err
		}
		stateDir := filepath.Join(dir, vmName)
		if err := os.MkdirAll(stateDir, 0755); err != nil {
			return "", err
		}
		pubkey, err := generateSSHKeypair(stateDir)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "Generated SSH keypair in %s\n", stateDir)
		return pubkey, nil
	default:
		// Treat as file path
		data, err := os.ReadFile(flag)
		if err != nil {
			return "", fmt.Errorf("reading SSH public key %s: %w", flag, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
}

// generateSSHKeypair creates an ed25519 keypair in the given directory.
// Returns the public key in authorized_keys format.
func generateSSHKeypair(dir string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generating ed25519 key: %w", err)
	}

	privKey, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", fmt.Errorf("marshaling private key: %w", err)
	}
	privPEM := pem.EncodeToMemory(privKey)
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519"), privPEM, 0600); err != nil {
		return "", err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("creating SSH public key: %w", err)
	}
	authorizedKey := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	if err := os.WriteFile(filepath.Join(dir, "id_ed25519.pub"), []byte(authorizedKey+"\n"), 0644); err != nil {
		return "", err
	}

	return authorizedKey, nil
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
	// Resolve SSH port and user from images.yml if not explicitly overridden
	dir, _ := os.Getwd()
	if cfg, cfgErr := LoadConfig(dir); cfgErr == nil {
		if resolved, resolveErr := cfg.ResolveImage(c.Image, "latest"); resolveErr == nil {
			if resolved.Vm != nil && resolved.Vm.SshPort != 0 && c.Port == 2222 {
				c.Port = resolved.Vm.SshPort
			}
		}
	}

	// All backends use direct SSH
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
	}

	// Auto-detect generated SSH key from VM state dir
	name := vmName(c.Image, c.Instance)
	if dir, err := vmDir(); err == nil {
		keyPath := filepath.Join(dir, name, "id_ed25519")
		if _, err := os.Stat(keyPath); err == nil {
			args = append(args, "-i", keyPath)
		}
	}

	args = append(args, fmt.Sprintf("%s@localhost", c.User))
	args = append(args, c.Args...)

	return syscall.Exec(sshBin, args, os.Environ())
}
