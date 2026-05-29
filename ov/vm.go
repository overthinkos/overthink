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
	"text/tabwriter"

	libvirt "github.com/digitalocean/go-libvirt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

const libvirtSessionURI = "qemu:///session"

// VmCmd groups VM management subcommands.
type VmCmd struct {
	Build    VmBuildCmd    `cmd:"" help:"Build QCOW2/RAW disk image from bootc container"`
	Clone    VmCloneCmd    `cmd:"" help:"Clone a new VM from another VM's snapshot (writes a kind:vm declaration)"`
	Console  VmConsoleCmd  `cmd:"" help:"Attach to VM serial console"`
	CpImage  VmCpImageCmd  `cmd:"" name:"cp-image" help:"Load a host image into a running VM guest's podman storage"`
	Create   VmCreateCmd   `cmd:"" help:"Create a VM from a disk image"`
	Destroy  VmDestroyCmd  `cmd:"" help:"Remove VM definition and optionally delete disk"`
	Gpu      VmGpuCmd      `cmd:"" help:"Inspect host VFIO/GPU-passthrough readiness (status, list)"`
	Import   VmImportCmd   `cmd:"" help:"Adopt an existing libvirt-managed VM into ov configuration"`
	List     VmListCmd     `cmd:"" help:"List VMs and their status"`
	Snapshot VmSnapshotCmd `cmd:"" help:"Manage VM snapshots (create, list, delete, revert, promote)"`
	Ssh      VmSshCmd      `cmd:"" help:"SSH into a VM"`
	Start    VmStartCmd    `cmd:"" help:"Start a VM"`
	Stop     VmStopCmd     `cmd:"" help:"Stop a VM (graceful shutdown)"`
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
		picked, probed := libvirtSessionSocketWithProbes()
		// `picked` is the last-resort dial target; we still need to
		// confirm it exists. The earlier probes (in `probed`) ARE
		// already stat'd inside libvirtSessionSocketWithProbes, but
		// that function returns the legacy path when neither exists,
		// so we re-stat here to be sure.
		if _, err := os.Stat(picked); err == nil {
			return "libvirt", nil
		}
		if configured == "libvirt" {
			var trail strings.Builder
			for _, p := range probed {
				_, err := os.Stat(p)
				if err == nil {
					trail.WriteString(fmt.Sprintf("\n  %s — found", p))
				} else {
					trail.WriteString(fmt.Sprintf("\n  %s — not found", p))
				}
			}
			return "", fmt.Errorf(
				"libvirt backend requires libvirt session daemon (probed:%s\n"+
					"configure libvirt session daemon or run: ov settings set vm.backend qemu)",
				trail.String(),
			)
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

// vmConfiguredBackend returns the backend string to feed resolveVmBackend for
// a vm entity: the entity's `backend:` pin (VmSpec.Backend) when set, else the
// global vm.backend setting. THE single source so EVERY vm verb (create /
// destroy / start / stop / console) resolves the SAME backend for a given
// entity. Without it, `ov vm create` (honoring the pin) and `ov vm destroy`
// (using the global setting) can pick DIFFERENT backends — the destroy then
// silently operates on the wrong backend's (non-existent) domain and leaves
// the created libvirt domain running, surfacing as "domain already exists" on
// the next create (the eval-k3s-vm `ov update` failure when vm.backend=qemu
// but the bed pins backend: libvirt).
func vmConfiguredBackend(vmName, rtBackend string) string {
	if vmName == "" {
		return rtBackend
	}
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil && uf.VM != nil {
			if spec, hit := uf.VM[vmName]; hit && spec.Backend != "" {
				return spec.Backend
			}
		}
	}
	return rtBackend
}

// startLibvirtUserSession ensures the libvirt user-session daemon is
// running. Modular libvirt's `virtqemud --timeout=120` auto-exits
// after 120 s of idle, so consecutive `ov eval libvirt …` calls
// spaced wider than that find the socket gone.
//
// Three start mechanisms tried in order, all best-effort:
//
//  1. `systemctl --user start virtqemud.service` — preferred when the
//     unit is installed (Debian/Ubuntu mostly).
//  2. `systemctl --user start libvirtd.service` — legacy monolithic
//     libvirt.
//  3. `virsh -c qemu:///session list` — works on Arch and any host
//     where libvirt installs WITHOUT systemd user units. virsh
//     dispatches to `virt-ssh-helper` / `virtqemud` directly, which
//     spawns the daemon and creates `/run/user/$UID/libvirt/
//     virtqemud-sock` on first connect.
//
// The function silently ignores all failures. Two outcomes:
//   - Daemon now running → caller's subsequent socket dial succeeds.
//   - Daemon not installable (no libvirt on this host) → caller's
//     downstream socket dial returns "no such file or directory",
//     which surfaces the real error.
//
// Reason for best-effort: don't block legitimate non-libvirt users.
func startLibvirtUserSession() {
	// Try systemd user-units first.
	for _, unit := range []string{"virtqemud.service", "libvirtd.service"} {
		// Idempotent: systemctl start on an already-active unit is a no-op.
		_ = exec.Command("systemctl", "--user", "start", unit).Run()
	}
	// Fall back to virsh-driven spawn for Arch-class hosts that ship
	// libvirt WITHOUT systemd user units (the binary is launched on-
	// demand via D-Bus or virt-ssh-helper). `list` is read-only and
	// returns 0 even with no domains.
	if _, err := exec.LookPath("virsh"); err == nil {
		_ = exec.Command("virsh", "-c", "qemu:///session", "list").Run()
	}
}

// ensureBootAutostartPrereqs makes a qemu:///session domain actually start at
// host boot. Two pieces are required:
//
//  1. Lingering — so the invoking user's systemd instance starts at boot
//     (without a login session). Idempotent.
//  2. A boot trigger that starts the domain. libvirt's own per-domain autostart
//     flag (set by the caller) only fires once the SESSION virtqemud is running,
//     and there is no portable user-level virtqemud.socket to socket-activate it
//     at boot — Arch/CachyOS ships none. So instead of relying on a shipped
//     socket unit, we generate a per-VM user systemd oneshot that runs
//     `virsh -c qemu:///session start <domain>` at boot; virsh spawns the
//     session daemon on demand and starts the (already-defined) domain. This is
//     deterministic and cross-distro.
//
// Best-effort with actionable warnings — the libvirt autostart flag is already
// set by the caller, so a failure here only loses the boot trigger.
func ensureBootAutostartPrereqs(domainName string) {
	username := currentUsername()
	if username != "" && !lingerEnabled(username) {
		if err := exec.Command("loginctl", "enable-linger", username).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not enable systemd linger for %s (%v); the VM will not autostart at boot until you run: loginctl enable-linger %s\n", username, err, username)
		} else {
			fmt.Fprintf(os.Stderr, "Enabled systemd linger for %s (user session persists across logout so the VM autostarts at boot)\n", username)
		}
	}
	if err := writeAutostartUserUnit(domainName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not install the boot-autostart user unit for %s (%v); the VM may not start at boot\n", domainName, err)
	}
}

// autostartUnitName is the per-domain user unit that starts a session VM at boot.
func autostartUnitName(domainName string) string {
	return "ov-autostart-" + domainName + ".service"
}

// writeAutostartUserUnit writes + enables the per-VM boot-autostart user unit.
func writeAutostartUserUnit(domainName string) error {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	unitDir := filepath.Join(cfgDir, "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	virsh, err := exec.LookPath("virsh")
	if err != nil || virsh == "" {
		virsh = "virsh"
	}
	unit := fmt.Sprintf(`[Unit]
Description=Overthink autostart for libvirt session domain %[1]s
After=default.target

[Service]
Type=oneshot
ExecStart=/bin/sh -c 'exec %[2]s -c qemu:///session start %[1]s 2>/dev/null || true'
RemainAfterExit=yes

[Install]
WantedBy=default.target
`, domainName, virsh)
	unitName := autostartUnitName(domainName)
	if err := os.WriteFile(filepath.Join(unitDir, unitName), []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if err := exec.Command("systemctl", "--user", "enable", unitName).Run(); err != nil {
		return fmt.Errorf("systemctl --user enable %s: %w", unitName, err)
	}
	fmt.Fprintf(os.Stderr, "Installed boot-autostart user unit %s (starts %s at boot under the lingering session)\n", unitName, domainName)
	return nil
}

// removeAutostartUserUnit disables + deletes the per-domain boot-autostart user
// unit, if present. Idempotent — silent when there is nothing to remove.
func removeAutostartUserUnit(domainName string) {
	unitName := autostartUnitName(domainName)
	_ = exec.Command("systemctl", "--user", "disable", unitName).Run()
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	unitPath := filepath.Join(cfgDir, "systemd", "user", unitName)
	if _, statErr := os.Stat(unitPath); statErr == nil {
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		fmt.Fprintf(os.Stderr, "Removed boot-autostart user unit %s\n", unitName)
	}
}

// lingerEnabled reports whether systemd user lingering is already on for
// the given user, so we don't shell out to enable it redundantly.
func lingerEnabled(username string) bool {
	out, err := exec.Command("loginctl", "show-user", username, "--property=Linger").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "Linger=yes"
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
	Image           string `arg:"" help:"Image name"`
	Ram             string `long:"ram" help:"Override RAM size (e.g. 4G, 8192M)"`
	Cpus            int    `long:"cpus" help:"Override CPU count"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	SshKey          string `long:"ssh-key" default:"auto" help:"SSH public key: path to .pub file, 'auto' (default ~/.ssh key), 'generate', or 'none'"`
	AutoDetectFlags `embed:""`
}

func (c *VmCreateCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Best-effort: start the libvirt user-session daemon before backend
	// probe. Many fresh-user setups have virtqemud.service installed but
	// not started, which silently falls libvirt → qemu in resolveVmBackend
	// when backend is "auto", and produces a hard error when backend is
	// "libvirt". Auto-starting it gives a frictionless first-VM experience
	// without masking real problems: if the unit doesn't exist (libvirt
	// truly not installed), this is a no-op and the downstream gate
	// surfaces the actual issue.
	startLibvirtUserSession()

	// --- New kind:vm entity path (D1, D4, D12) ---
	// Resolve the kind:vm entity FIRST so its `backend:` pin (when set)
	// overrides the global vm.backend setting BEFORE backend resolution —
	// the documented "pin backend: libvirt so the auto→qemu fallback can't
	// mask a missing daemon" behavior. (VmSpec.Backend was previously
	// absent, so the pin was silently dropped; now it is honored.)
	dir, _ := os.Getwd()
	var spec *VmSpec
	if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf.VM != nil {
		spec = uf.VM[c.Image]
	}
	backend, err := resolveVmBackend(vmConfiguredBackend(c.Image, rt.VmBackend))
	if err != nil {
		return err
	}
	if spec != nil {
		// VmSpec-driven create pipeline: RenderDomain for libvirt,
		// RenderQemuArgv for qemu. Uses output/qcow2/{disk,seed} produced
		// by `ov vm build` (the cloud_image branch of vm_build.go).
		return c.runVmSpecCreate(c.Image, spec, backend)
	}

	// Reached here = image is not a `kind: vm` entity, AND the legacy
	// ImageConfig.Vm / OCI LabelVm fallback was removed in the VM
	// hard-cutover. Tell the user what to do.
	_ = rt
	_ = backend
	return fmt.Errorf(
		"VM %q has no kind:vm entity in vm.yml.\n"+
			"  Declare one (optionally paired with a bootc image), e.g.:\n"+
			"      vm:\n"+
			"        %s-bootc:\n"+
			"          source: {kind: bootc, image: %s}\n",
		c.Image, c.Image, c.Image)
}

func (c *VmCreateCmd) createLibvirt(name, qcow2, ram string, cpus, sshPort int, ports []string, sshPubKey string) error {
	ramMB := parseRAMtoMB(ram)

	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
	}
	gpu := detected.GPU
	if gpu {
		fmt.Fprintf(os.Stderr, "Warning: GPU passthrough for libvirt VMs requires manual --host-device configuration\n")
	}

	var smbiosCreds []string
	if sshPubKey != "" {
		smbiosCreds = append(smbiosCreds, SmbiosCredForRootSSH(sshPubKey))
		fmt.Fprintf(os.Stderr, "Injecting SSH key via SMBIOS credential\n")
	}

	xmlStr := buildDomainXML(name, qcow2, ramMB, cpus, sshPort, ports, gpu, smbiosCreds...)

	conn, err := connectLibvirt("")
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

func (c *VmCreateCmd) createQemu(name, qcow2, ram string, cpus, sshPort int, ports []string, sshPubKey string) error {
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
		"-display", "none",
		"-daemonize",
		"-pidfile", filepath.Join(stateDir, "qemu.pid"),
	}

	// SSH key injection via systemd credentials (SMBIOS type 11)
	if sshPubKey != "" {
		cred := SmbiosCredForRootSSH(sshPubKey)
		args = append(args, "-smbios", fmt.Sprintf("type=11,value=%s", cred))
		fmt.Fprintf(os.Stderr, "Injecting SSH key via SMBIOS credential\n")
	}

	// Port forwarding: SSH mapping comes from image.yml `vm.ssh_port`
	// (default 2222) — published ports from the image labels follow.
	hostfwds := fmt.Sprintf("hostfwd=tcp::%d-:22", sshPort)
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
	fmt.Fprintf(os.Stderr, "SSH: ssh -p 2222 root@localhost\n")
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

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Image, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "libvirt":
		conn, err := connectLibvirt("")
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

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Image, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "libvirt":
		conn, err := connectLibvirt("")
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

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Image, rt.VmBackend))
	if err != nil {
		return err
	}

	name := vmName(c.Image, c.Instance)

	switch backend {
	case "libvirt":
		conn, err := connectLibvirt("")
		if err != nil {
			return err
		}
		defer conn.Close()

		dom, err := conn.lookupDomain(name)
		if err != nil {
			return fmt.Errorf("VM %s not found: %w", name, err)
		}

		// Stop if running — gracefully (flush the guest filesystem, incl. the
		// in-guest podman overlay store), forcing only if it won't power off.
		conn.gracefulStopDomain(dom)

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

	// Remove any boot-autostart user unit (the inverse of ensureBootAutostartPrereqs),
	// so a destroyed VM doesn't leave a unit that fails at boot. Idempotent.
	removeAutostartUserUnit(name)

	// Remove the managed ssh-config Host stanza (the inverse of what
	// `ov vm create` published). The libvirt/qemu domain `name` is
	// already the prefixed form ("ov-<image>" via vmName()), which IS
	// the alias — we use it directly without re-prefixing.
	if home, herr := os.UserHomeDir(); herr == nil {
		remaining, rerr := RemoveVmSshStanza(home, name)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", rerr)
		}
		if remaining == 0 {
			if rerr := RemoveSshConfigInclude(home); rerr != nil {
				fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", rerr)
			}
		}
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
	All          bool `short:"a" long:"all" help:"Show all VMs including stopped"`
	CleanOrphans bool `long:"clean-orphans" help:"Detect and undefine orphan libvirt domains (defined but no qcow2 backing or state dir)"`
}

func (c *VmListCmd) Run() error {
	if c.CleanOrphans {
		return c.runCleanOrphans()
	}

	// Backend-agnostic listing — probe BOTH libvirt and QEMU and merge.
	// Each probe is informational; a failure in one doesn't fail the
	// whole command. Pre-fix behavior was to bail when the configured
	// backend's probe failed, hiding running VMs in the OTHER backend.
	type vmRow struct {
		Name    string
		Backend string
		State   string
	}
	var rows []vmRow
	var probeNotes []string

	// libvirt probe
	if conn, err := connectLibvirt(""); err == nil {
		defer conn.Close()
		domains, derr := conn.listOvDomains()
		if derr != nil {
			probeNotes = append(probeNotes, fmt.Sprintf("(libvirt: listing failed: %v)", derr))
		} else {
			for _, d := range domains {
				rows = append(rows, vmRow{Name: d.Name, Backend: "libvirt", State: d.State})
			}
		}
	} else {
		probeNotes = append(probeNotes, fmt.Sprintf("(libvirt session daemon not reachable: %v)", err))
	}

	// QEMU pidfile scan
	if dir, err := vmDir(); err == nil {
		entries, derr := os.ReadDir(dir)
		if derr == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				name := entry.Name()
				pidFile := filepath.Join(dir, name, "qemu.pid")
				state := "stopped"
				alive := false
				if data, err := os.ReadFile(pidFile); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							if err := proc.Signal(syscall.Signal(0)); err == nil {
								state = "running"
								alive = true
							}
						}
					}
				}
				// Skip QEMU rows that duplicate a libvirt-listed name —
				// libvirt is authoritative when both backends know about
				// the same domain.
				duplicate := false
				for _, existing := range rows {
					if existing.Name == name {
						duplicate = true
						break
					}
				}
				if duplicate {
					continue
				}
				if !c.All && !alive {
					continue
				}
				rows = append(rows, vmRow{Name: name, Backend: "qemu", State: state})
			}
		}
	}

	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "No VMs found")
		for _, note := range probeNotes {
			fmt.Fprintln(os.Stderr, note)
		}
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tBACKEND\tSTATE")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Name, r.Backend, r.State)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, note := range probeNotes {
		fmt.Fprintln(os.Stderr, note)
	}
	return nil
}

// runCleanOrphans detects orphan libvirt domains and undefines them.
// A domain is "orphan" when:
//  1. Defined in libvirt
//  2. State == shut off (not running)
//  3. Either: backing qcow2 doesn't exist, OR no matching state dir.
//
// Active (running) domains are never touched. Cleanup runs
// DomainUndefineFlags(libvirt.DomainUndefineNvram) and removes the
// per-VM state directory.
func (c *VmListCmd) runCleanOrphans() error {
	conn, err := connectLibvirt("")
	if err != nil {
		return fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close()
	domains, err := conn.listOvDomains()
	if err != nil {
		return fmt.Errorf("listing domains: %w", err)
	}
	stateRoot, err := vmDir()
	if err != nil {
		return err
	}
	var orphans []string
	for _, d := range domains {
		if d.State == "running" {
			continue
		}
		stateDir := filepath.Join(stateRoot, d.Name)
		_, statErr := os.Stat(stateDir)
		if statErr == nil {
			continue // state dir present → not an orphan
		}
		orphans = append(orphans, d.Name)
	}
	if len(orphans) == 0 {
		fmt.Println("no orphan libvirt domains")
		return nil
	}
	for _, name := range orphans {
		dom, derr := conn.lookupDomain(name)
		if derr != nil {
			fmt.Fprintf(os.Stderr, "warning: lookup %s: %v\n", name, derr)
			continue
		}
		if uerr := conn.l.DomainUndefineFlags(dom, libvirt.DomainUndefineNvram); uerr != nil {
			fmt.Fprintf(os.Stderr, "warning: undefine %s: %v\n", name, uerr)
			continue
		}
		fmt.Printf("undefined orphan: %s\n", name)
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

	backend, err := resolveVmBackend(vmConfiguredBackend(c.Image, rt.VmBackend))
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
// generateDir is the directory where generated keypairs are stored (only used for "generate").
func resolveSSHPubKey(flag, generateDir string) (string, error) {
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
		if err := os.MkdirAll(generateDir, 0755); err != nil {
			return "", err
		}
		pubkey, err := generateSSHKeypair(generateDir)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "Generated SSH keypair in %s\n", generateDir)
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

// vmSSHKeyDir returns the directory for storing VM SSH keypairs.
func vmSSHKeyDir(name string) (string, error) {
	dir, err := vmDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// containerSSHKeyDir returns the directory for storing container SSH keypairs.
func containerSSHKeyDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "ov", "ssh", name), nil
}

// generateSSHKeypair creates an ed25519 keypair in the given directory.
// Returns the public key in authorized_keys format. Idempotent: when
// the .pub file already exists in dir, the existing public key is
// read and returned without generating a new pair (so multiple VM
// lifecycle calls — build, create, start — use the same identity).
func generateSSHKeypair(dir string) (string, error) {
	pubPath := filepath.Join(dir, "id_ed25519.pub")
	if existing, err := os.ReadFile(pubPath); err == nil {
		return strings.TrimSpace(string(existing)), nil
	}

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
	User     string   `short:"l" long:"user" default:"root" help:"SSH username"`
	Args     []string `arg:"" optional:"" help:"Additional SSH arguments or command"`
}

func (c *VmSshCmd) Run() error {
	// Resolve SSH port from the kind:vm entity in vm.yml.
	// (Legacy OCI LabelVm lookup was removed in the VM hard-cutover.)
	if c.Port == 2222 {
		if dir, derr := os.Getwd(); derr == nil {
			if uf, ok, ufErr := LoadUnified(dir); ufErr == nil && ok && uf.VM != nil {
				if spec, hit := uf.VM[c.Image]; hit && spec.SSH != nil && spec.SSH.Port != 0 {
					c.Port = spec.SSH.Port
				}
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
