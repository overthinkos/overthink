package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DoctorCmd checks host dependencies and reports status.
type DoctorCmd struct {
	JSON bool `long:"json" help:"Output as JSON"`
}

// DoctorCheckStatus represents the result of a single dependency check.
type DoctorCheckStatus int

const (
	CheckOK      DoctorCheckStatus = iota // installed and working
	CheckMissing                          // not found
	CheckWarning                          // found but with caveats
	CheckInfo                             // informational (hardware, not a dep)
	CheckAbsent                           // hardware not present (neutral)
)

// DoctorCheckResult holds the outcome of a single check.
type DoctorCheckResult struct {
	Name        string            `json:"name"`
	Status      DoctorCheckStatus `json:"status"`
	Version     string            `json:"version,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	InstallHint string            `json:"install_hint,omitempty"`
}

// CheckGroup organizes checks by feature area.
type CheckGroup struct {
	Name     string              `json:"name"`
	Required bool                `json:"required"`
	OrLogic  bool                `json:"or_logic,omitempty"` // at least one check must pass
	Checks   []DoctorCheckResult `json:"checks"`
}

// HardwareInfo holds device detection results for JSON output.
type HardwareInfo struct {
	GPU            bool         `json:"gpu"`
	AMDGPU         bool         `json:"amd_gpu"`
	AMDGFXVersion  string       `json:"amd_gfx_version,omitempty"`
	GPUFlags       []string     `json:"gpu_flags"`
	Devices        []DeviceInfo `json:"devices"`
	ContainerFlags []string     `json:"container_flags"`
}

// DeviceInfo describes a single detected/absent device.
type DeviceInfo struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description"`
	Present     bool   `json:"present"`
}

// DoctorOutput is the JSON output structure.
type DoctorOutput struct {
	System   Distro       `json:"system"`
	Groups   []CheckGroup `json:"groups"`
	Hardware HardwareInfo `json:"hardware"`
	Summary  struct {
		Installed int `json:"installed"`
		Missing   int `json:"missing"`
		Warnings  int `json:"warnings"`
		Devices   int `json:"devices"`
	} `json:"summary"`
}

func (c *DoctorCmd) Run() error {
	distro := detectDistro()
	groups := runDoctorChecks(distro)
	hardware := runHardwareChecks(distro)

	if c.JSON {
		return c.printJSON(distro, groups, hardware)
	}
	return c.printHuman(distro, groups, hardware)
}

// runDoctorChecks runs all dependency checks and returns grouped results.
func runDoctorChecks(distro Distro) []CheckGroup {
	groups := []CheckGroup{
		{
			Name:     "Container Engine (required -- at least one)",
			Required: true,
			OrLogic:  true,
			Checks: []DoctorCheckResult{
				checkBinary("docker", distro),
				checkBinary("podman", distro),
			},
		},
		{
			Name:     "Build Infrastructure (recommended)",
			Required: false,
			Checks:   buildInfraChecks(distro),
		},
		{
			Name:     "Service Management (quadlet mode)",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("systemctl", distro),
				checkQuadletPodman(distro),
			},
		},
		{
			Name:     "Virtual Machines",
			Required: false,
			Checks:   vmChecks(distro),
		},
		{
			Name:     "VFIO / GPU passthrough",
			Required: false,
			Checks:   vfioChecks(),
		},
		{
			Name:     "Encrypted Storage",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("gocryptfs", distro),
				checkBinary("fusermount3", distro),
				checkBinary("systemd-ask-password", distro),
			},
		},
		{
			Name:     "Secret Storage",
			Required: false,
			Checks:   secretStorageChecks(),
		},
		{
			Name:     "Tunnels",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("tailscale", distro),
				checkBinary("cloudflared", distro),
			},
		},
		{
			Name:     "Merge & Registry",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("skopeo", distro),
			},
		},
		{
			Name:     "Shell & TTY",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("script", distro),
			},
		},
	}

	// Only show podman machine group if podman is installed
	if _, err := exec_LookPath("podman"); err == nil {
		groups = append(groups, CheckGroup{
			Name:     "Podman Machine",
			Required: false,
			Checks: []DoctorCheckResult{
				checkGvproxyDoctor(distro),
			},
		})
	}

	return groups
}

func buildInfraChecks(distro Distro) []DoctorCheckResult {
	checks := []DoctorCheckResult{
		checkGo(),
		checkBinary("git", distro),
	}
	// Only check buildx if docker is available
	if _, err := exec_LookPath("docker"); err == nil {
		checks = append(checks, checkBuildxBuilder())
	}
	return checks
}

func vmChecks(distro Distro) []DoctorCheckResult {
	qemuBin := qemuSystemBinary()
	checks := []DoctorCheckResult{
		checkBinary(qemuBin, distro),
		checkBinary("qemu-img", distro),
		checkVirtiofsd(distro),
		checkBinary("virsh", distro),
		checkBinary("ssh", distro),
		checkLibvirtSocket(),
	}
	return checks
}

// vfioChecks reports host readiness for VFIO GPU passthrough, reusing the same
// DetectVFIO probe that `charly vm gpu` consumes.
func vfioChecks() []DoctorCheckResult {
	rep := DetectVFIO()
	var checks []DoctorCheckResult

	if rep.IOMMUEnabled {
		detail := "enabled"
		if rep.IOMMUKind != "" {
			detail = rep.IOMMUKind + " — enabled"
		}
		checks = append(checks, DoctorCheckResult{Name: "IOMMU", Status: CheckOK, Detail: detail})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "IOMMU",
			Status:      CheckWarning,
			Detail:      "not enabled (/sys/kernel/iommu_groups empty)",
			InstallHint: "add intel_iommu=on or amd_iommu=on plus iommu=pt to the kernel cmdline, then reboot",
		})
	}

	if vfioPciAvailable() {
		checks = append(checks, DoctorCheckResult{Name: "vfio-pci driver", Status: CheckOK})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "vfio-pci driver",
			Status:      CheckWarning,
			Detail:      "not loaded",
			InstallHint: "sudo modprobe vfio-pci (libvirt managed='yes' loads it on VM start)",
		})
	}

	// memlock — VFIO pins all guest RAM; a rootless session needs a high limit.
	if _, hard := MemlockLimitBytes(); memlockUnlimited(hard) {
		checks = append(checks, DoctorCheckResult{Name: "memlock limit", Status: CheckOK, Detail: "unlimited"})
	} else if hard >= 16<<30 {
		checks = append(checks, DoctorCheckResult{Name: "memlock limit", Status: CheckOK, Detail: fmt.Sprintf("%d MiB", hard>>20)})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "memlock limit",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("%d MiB — too low for GPU passthrough (needs >= guest RAM)", hard>>20),
			InstallHint: "raise RLIMIT_MEMLOCK for the libvirt session (limits.d 'hard memlock unlimited' + re-login)",
		})
	}

	if len(rep.GPUs) == 0 {
		checks = append(checks, DoctorCheckResult{Name: "passthrough GPU", Status: CheckAbsent, Detail: "none detected"})
		return checks
	}
	for _, g := range rep.GPUs {
		grp := "no IOMMU group"
		access := ""
		if g.IOMMUGroup >= 0 {
			grp = fmt.Sprintf("group %d", g.IOMMUGroup)
			if VfioGroupAccessible(g.IOMMUGroup) {
				access = ", /dev/vfio rw"
			} else {
				access = fmt.Sprintf(", /dev/vfio/%d NOT accessible (charly udev install)", g.IOMMUGroup)
			}
		}
		drv := g.Driver
		if drv == "" {
			drv = "unbound"
		}
		checks = append(checks, DoctorCheckResult{
			Name:   g.Addr,
			Status: CheckInfo,
			Detail: fmt.Sprintf("%s:%s %s — driver=%s, %s%s", trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel, drv, grp, access),
		})
	}
	return checks
}

// checkVirtiofsd checks for virtiofsd which may be installed outside PATH.
// On Arch Linux it installs to /usr/lib/virtiofsd, on RHEL to /usr/libexec/virtiofsd.
func checkVirtiofsd(distro Distro) DoctorCheckResult {
	if path, err := exec_LookPath("virtiofsd"); err == nil {
		return DoctorCheckResult{
			Name:   "virtiofsd",
			Status: CheckOK,
			Detail: path,
		}
	}
	// Check non-PATH locations where distros install virtiofsd
	for _, path := range []string{"/usr/lib/virtiofsd", "/usr/libexec/virtiofsd"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return DoctorCheckResult{
				Name:   "virtiofsd",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return DoctorCheckResult{
		Name:        "virtiofsd",
		Status:      CheckMissing,
		Detail:      "not found",
		InstallHint: distro.installHint("virtiofsd"),
	}
}

// checkBinary checks if a binary exists in PATH and tries to get its version.
func checkBinary(name string, distro Distro) DoctorCheckResult {
	path, err := exec_LookPath(name)
	if err != nil {
		return DoctorCheckResult{
			Name:        name,
			Status:      CheckMissing,
			Detail:      "not found",
			InstallHint: distro.installHint(name),
		}
	}
	version := getBinaryVersion(name)
	return DoctorCheckResult{
		Name:    name,
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkGo checks for Go and validates the version.
func checkGo() DoctorCheckResult {
	path, err := exec_LookPath("go")
	if err != nil {
		return DoctorCheckResult{
			Name:        "go",
			Status:      CheckMissing,
			Detail:      "not found (required to build charly from source)",
			InstallHint: "install Go 1.25.3+",
		}
	}
	version := getBinaryVersion("go")
	return DoctorCheckResult{
		Name:    "go",
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkQuadletPodman checks if podman is available for quadlet mode.
func checkQuadletPodman(distro Distro) DoctorCheckResult {
	if _, err := exec_LookPath("podman"); err != nil {
		return DoctorCheckResult{
			Name:        "podman (for quadlet)",
			Status:      CheckWarning,
			Detail:      "quadlet mode requires podman",
			InstallHint: distro.installHint("podman"),
		}
	}
	return DoctorCheckResult{
		Name:   "podman (for quadlet)",
		Status: CheckOK,
		Detail: "available",
	}
}

// checkBuildxBuilder checks if Docker buildx is available.
func checkBuildxBuilder() DoctorCheckResult {
	cmd := exec.Command("docker", "buildx", "version")
	out, err := cmd.Output()
	if err != nil {
		return DoctorCheckResult{
			Name:        "docker buildx",
			Status:      CheckMissing,
			Detail:      "not available",
			InstallHint: "install docker-buildx",
		}
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return DoctorCheckResult{
		Name:    "docker buildx",
		Status:  CheckOK,
		Version: version,
	}
}

// checkLibvirtSocket checks if the libvirt session socket exists.
func checkLibvirtSocket() DoctorCheckResult {
	sockPath := libvirtSessionSocket()
	if _, err := os.Stat(sockPath); err == nil {
		return DoctorCheckResult{
			Name:   "libvirt session socket",
			Status: CheckOK,
			Detail: sockPath,
		}
	}

	// Determine the best hint based on what's available
	hint := "systemctl --user enable --now libvirtd.socket"

	// On Arch/modern distros, user-level libvirtd.socket may not exist.
	// Check if system-level virtqemud is available instead.
	distro := detectDistro()
	if distro.ID == "arch" {
		hint = "sudo systemctl enable --now virtqemud.socket && sudo usermod -aG libvirt $USER"
	}

	return DoctorCheckResult{
		Name:        "libvirt session socket",
		Status:      CheckMissing,
		Detail:      sockPath,
		InstallHint: hint,
	}
}

// checkGvproxyDoctor checks gvproxy availability (same logic as checkGvproxy in machine.go).
func checkGvproxyDoctor(distro Distro) DoctorCheckResult {
	if _, err := exec_LookPath("gvproxy"); err == nil {
		path, _ := exec_LookPath("gvproxy")
		return DoctorCheckResult{
			Name:   "gvproxy",
			Status: CheckOK,
			Detail: path,
		}
	}
	for _, path := range []string{"/usr/libexec/podman/gvproxy", "/usr/local/libexec/podman/gvproxy", "/usr/lib/podman/gvproxy"} {
		if _, err := os.Stat(path); err == nil {
			return DoctorCheckResult{
				Name:   "gvproxy",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return DoctorCheckResult{
		Name:        "gvproxy",
		Status:      CheckMissing,
		Detail:      "not found",
		InstallHint: distro.installHint("gvproxy"),
	}
}

// getBinaryVersion tries to get the version string from a binary.
func getBinaryVersion(name string) string {
	cmd := exec.Command(name, "--version")
	out, err := cmd.Output()
	if err != nil {
		// Some tools use "version" instead of "--version"
		cmd = exec.Command(name, "version")
		out, err = cmd.Output()
		if err != nil {
			return ""
		}
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// deviceDescriptions maps device patterns to human-readable descriptions.
// deviceDescriptions maps a host device path to a human description for `charly doctor`'s
// hardware section, read from the device_descriptions directive in the embedded charly.yml
// (Phase 4: data moved out of Go) via the shared minimal decoder. Panics if the directive
// is empty/malformed (a build-time invariant, never a runtime input).
var deviceDescriptions = parseEmbeddedDeviceDescriptions()

func parseEmbeddedDeviceDescriptions() map[string]string {
	var doc struct {
		DeviceDescriptions map[string]string `yaml:"device_descriptions"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.DeviceDescriptions) == 0 {
		panic("doctor: embedded charly.yml has no device_descriptions: directive")
	}
	return doc.DeviceDescriptions
}

// runHardwareChecks probes for GPU and devices, matching DetectHostDevices() behavior.
func runHardwareChecks(_ Distro) HardwareInfo {
	hw := HardwareInfo{}

	// GPU detection (same as DetectGPU)
	hw.GPU = DetectGPU()
	if hw.GPU {
		// Determine which engine is configured to show appropriate flags
		if _, err := exec_LookPath("podman"); err == nil {
			hw.GPUFlags = GPURunArgs("podman")
		} else if _, err := exec_LookPath("docker"); err == nil {
			hw.GPUFlags = GPURunArgs("docker")
		}
		hw.ContainerFlags = append(hw.ContainerFlags, hw.GPUFlags...)
	}

	// AMD GPU detection
	hw.AMDGPU = DetectAMDGPU()
	if hw.AMDGPU {
		hw.AMDGFXVersion = detectAMDGFXVersion()
		hw.ContainerFlags = append(hw.ContainerFlags, "--group-add", "keep-groups")
	}

	// Device pattern probing (same patterns as devicePatterns in devices.go)
	for _, pattern := range devicePatterns {
		desc := deviceDescriptions[pattern]
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			for _, m := range matches {
				hw.Devices = append(hw.Devices, DeviceInfo{
					Pattern:     pattern,
					Path:        m,
					Description: desc,
					Present:     true,
				})
				hw.ContainerFlags = append(hw.ContainerFlags, "--device", m)
			}
		} else {
			// Show the pattern itself for absent devices
			hw.Devices = append(hw.Devices, DeviceInfo{
				Pattern:     pattern,
				Path:        pattern,
				Description: desc,
				Present:     false,
			})
		}
	}

	return hw
}

func (c *DoctorCmd) printHuman(distro Distro, groups []CheckGroup, hw HardwareInfo) error {
	fmt.Println("charly doctor")
	fmt.Println("=========")
	fmt.Printf("System: %s (%s)\n", distro.Name, managerShort(distro.Manager))
	fmt.Println()

	installed, missing, warnings := 0, 0, 0
	requiredFailed := false

	for _, g := range groups {
		groupStatus := groupStatusSymbol(g)
		fmt.Printf("[%s] %s\n", groupStatus, g.Name)

		for _, ch := range g.Checks {
			symbol, line := formatCheck(ch)
			fmt.Printf("  [%s] %s\n", symbol, line)
			switch ch.Status {
			case CheckOK:
				installed++
			case CheckMissing:
				missing++
				if g.Required && !g.OrLogic {
					requiredFailed = true
				}
			case CheckWarning:
				warnings++
			}
		}

		// For OR-logic groups, check if at least one passed
		if g.Required && g.OrLogic {
			anyOK := false
			for _, ch := range g.Checks {
				if ch.Status == CheckOK {
					anyOK = true
					break
				}
			}
			if !anyOK {
				requiredFailed = true
			}
		}

		fmt.Println()
	}

	// Hardware section
	deviceCount := 0
	fmt.Println("[OK] Hardware & Auto-Detected Devices")
	if hw.GPU {
		fmt.Printf("  [+] NVIDIA GPU -- detected (%s)\n", strings.Join(hw.GPUFlags, " "))
	} else {
		fmt.Println("  [ ] NVIDIA GPU -- not detected")
	}
	if hw.AMDGPU {
		label := "detected (--group-add keep-groups)"
		if hw.AMDGFXVersion != "" {
			label = fmt.Sprintf("detected gfx %s (--group-add keep-groups)", hw.AMDGFXVersion)
		}
		fmt.Printf("  [+] AMD GPU -- %s\n", label)
	} else {
		fmt.Println("  [ ] AMD GPU -- not detected")
	}

	for _, d := range hw.Devices {
		if d.Present {
			fmt.Printf("  [+] %s -- %s\n", d.Path, d.Description)
			deviceCount++
		} else {
			fmt.Printf("  [ ] %s -- not present\n", d.Path)
		}
	}

	if hw.GPU {
		deviceCount++
	}
	if hw.AMDGPU {
		deviceCount++
	}

	fmt.Println()
	if len(hw.ContainerFlags) > 0 {
		fmt.Printf("  Containers will receive: %s\n", strings.Join(hw.ContainerFlags, " "))
	} else {
		fmt.Println("  No devices will be passed to containers")
	}
	fmt.Println("  Disable with: --no-autodetect")
	fmt.Println()

	fmt.Printf("Summary: %d found, %d missing, %d warnings, %d devices detected\n",
		installed, missing, warnings, deviceCount)

	if requiredFailed {
		return fmt.Errorf("required dependencies missing")
	}
	return nil
}

func (c *DoctorCmd) printJSON(distro Distro, groups []CheckGroup, hw HardwareInfo) error {
	output := DoctorOutput{
		System:   distro,
		Groups:   groups,
		Hardware: hw,
	}

	deviceCount := 0
	if hw.GPU {
		deviceCount++
	}
	if hw.AMDGPU {
		deviceCount++
	}
	for _, d := range hw.Devices {
		if d.Present {
			deviceCount++
		}
	}

	for _, g := range groups {
		for _, ch := range g.Checks {
			switch ch.Status {
			case CheckOK:
				output.Summary.Installed++
			case CheckMissing:
				output.Summary.Missing++
			case CheckWarning:
				output.Summary.Warnings++
			}
		}
	}
	output.Summary.Devices = deviceCount

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func groupStatusSymbol(g CheckGroup) string {
	if g.OrLogic {
		// At least one must pass
		for _, ch := range g.Checks {
			if ch.Status == CheckOK {
				return "OK"
			}
		}
		if g.Required {
			return "!!"
		}
		return "--"
	}

	allOK := true
	anyOK := false
	for _, ch := range g.Checks {
		if ch.Status != CheckOK {
			allOK = false
		} else {
			anyOK = true
		}
	}
	if allOK {
		return "OK"
	}
	if g.Required {
		return "!!"
	}
	if anyOK {
		return "!!"
	}
	return "--"
}

func formatCheck(ch DoctorCheckResult) (string, string) {
	switch ch.Status {
	case CheckOK:
		parts := []string{ch.Name}
		if ch.Version != "" {
			parts = append(parts, "--", ch.Version)
		}
		if ch.Detail != "" && ch.Version == "" {
			parts = append(parts, "--", ch.Detail)
		}
		return "+", strings.Join(parts, " ")
	case CheckMissing:
		line := ch.Name + " -- " + ch.Detail
		if ch.InstallHint != "" {
			line += " (" + ch.InstallHint + ")"
		}
		return "-", line
	case CheckWarning:
		line := ch.Name + " -- " + ch.Detail
		if ch.InstallHint != "" {
			line += " (" + ch.InstallHint + ")"
		}
		return "!", line
	default:
		return " ", ch.Name
	}
}

func managerShort(manager string) string {
	if manager == "" {
		return "unknown package manager"
	}
	return manager
}

// secretStorageChecks returns checks for the credential/secret storage subsystem.
func secretStorageChecks() []DoctorCheckResult {
	var checks []DoctorCheckResult

	// Check 1: Secret backend availability
	kr := &KeyringStore{}
	if err := kr.Probe(); err == nil {
		checks = append(checks, DoctorCheckResult{
			Name:    "Secret backend",
			Status:  CheckOK,
			Version: "system keyring",
		})
	} else {
		state := GetKeyringState()
		backend := resolveSecretBackend()
		switch {
		case state == KeyringLocked:
			checks = append(checks, DoctorCheckResult{
				Name:        "Secret backend",
				Status:      CheckWarning,
				Version:     "system keyring (LOCKED)",
				Detail:      "keyring is locked — credentials unavailable until unlocked",
				InstallHint: "Unlock your keyring, or run: charly config set secret_backend config",
			})
		case backend == "config":
			checks = append(checks, DoctorCheckResult{
				Name:    "Secret backend",
				Status:  CheckOK,
				Version: "config file (explicit)",
			})
		default:
			checks = append(checks, DoctorCheckResult{
				Name:        "Secret backend",
				Status:      CheckWarning,
				Detail:      "config file (no keyring available)",
				InstallHint: "Install gnome-keyring or keepassxc for secure credential storage, or run: charly config set secret_backend config",
			})
		}
	}

	// Check 2: Config file permissions
	configPath, err := RuntimeConfigPath()
	if err == nil {
		if info, statErr := os.Stat(configPath); statErr == nil {
			perm := info.Mode().Perm()
			if perm&0077 == 0 {
				checks = append(checks, DoctorCheckResult{
					Name:    "Config permissions",
					Status:  CheckOK,
					Version: fmt.Sprintf("%04o", perm),
				})
			} else {
				checks = append(checks, DoctorCheckResult{
					Name:        "Config permissions",
					Status:      CheckWarning,
					Detail:      fmt.Sprintf("%04o (world-readable)", perm),
					InstallHint: fmt.Sprintf("Run: chmod 600 %s", configPath),
				})
			}
		}
	}

	// Check 3: Plaintext credentials count
	cfg, err := LoadRuntimeConfig()
	if err == nil {
		count := HasPlaintextCredentials(cfg)
		if count == 0 {
			checks = append(checks, DoctorCheckResult{
				Name:    "Plaintext credentials",
				Status:  CheckOK,
				Version: "0",
			})
		} else {
			checks = append(checks, DoctorCheckResult{
				Name:        "Plaintext credentials",
				Status:      CheckWarning,
				Detail:      fmt.Sprintf("%d in config.yml", count),
				InstallHint: "Run: charly config migrate-secrets --dry-run",
			})
		}
	}

	// Check 4: Secret Service collection health (defect G).
	// Iterates the secret service provider's collections and flags any that
	// fail on property reads. This catches the class of bug where
	// KeePassXC's FdoSecrets plugin advertises a stub collection that routes
	// I/O errors for every method call — the real credentials are in a
	// sibling collection but the default alias points at the stub.
	checks = append(checks, checkKeyringHealth()...)

	// Check 5: Keyring shadow index consistency (defect G + H).
	// Cross-checks the config.yml KeyringKeys shadow index against reality:
	// every indexed key should actually be retrievable via the secret service.
	// If not, the index is out of sync and should be pruned.
	checks = append(checks, checkKeyringIndexConsistency()...)

	return checks
}

// checkKeyringHealth probes each Secret Service collection and reports
// healthy/broken counts. Returns one DoctorCheckResult per distinguishable state.
// Skips silently (returns nil) if there's no session bus or no collections
// — those cases are already handled by "Secret backend" above.
func checkKeyringHealth() []DoctorCheckResult {
	c, err := newSSClient()
	if err != nil {
		// No session bus — already covered by "Secret backend" check. Skip.
		return nil
	}
	defer c.close()

	paths, err := c.collections()
	if err != nil {
		return []DoctorCheckResult{{
			Name:        "Secret Service collections",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("cannot list collections: %v", err),
			InstallHint: "Check that your Secret Service provider (gnome-keyring, keepassxc) is running correctly",
		}}
	}
	if len(paths) == 0 {
		// Also already covered upstream. Skip.
		return nil
	}

	var healthy, broken []string
	for _, p := range paths {
		if err := c.isCollectionHealthy(p); err != nil {
			broken = append(broken, string(p))
		} else {
			label := c.collectionLabel(p)
			if label != "" {
				healthy = append(healthy, fmt.Sprintf("%q", label))
			} else {
				healthy = append(healthy, string(p))
			}
		}
	}

	if len(broken) == 0 {
		return []DoctorCheckResult{{
			Name:    "Secret Service collections",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d healthy", len(healthy)),
			Detail:  strings.Join(healthy, ", "),
		}}
	}
	return []DoctorCheckResult{{
		Name:   "Secret Service collections",
		Status: CheckWarning,
		Version: fmt.Sprintf("%d healthy + %d broken",
			len(healthy), len(broken)),
		Detail: fmt.Sprintf(
			"charly will iterate and skip broken. Broken: %s. Healthy: %s",
			strings.Join(broken, ", "),
			strings.Join(healthy, ", ")),
		InstallHint: "Consider cleaning stale entries in your Secret Service provider (e.g. KeePassXC → Tools → Settings → Secret Service Integration → Exposed Databases)",
	}}
}

// checkKeyringIndexConsistency cross-checks the config.yml KeyringKeys
// shadow index against what the secret service can actually return. Entries
// in the index that are NOT present in any collection indicate that the
// keyring has drifted — the user may need to re-store those credentials or
// prune the stale index entries.
func checkKeyringIndexConsistency() []DoctorCheckResult {
	cfg, err := LoadRuntimeConfig()
	if err != nil || len(cfg.KeyringKeys) == 0 {
		return nil
	}
	c, err := newSSClient()
	if err != nil {
		// No session bus — can't check. Not a failure, just skip.
		return nil
	}
	defer c.close()

	var missing []string
	for _, entry := range cfg.KeyringKeys {
		// Index entries are stored as "<service>/<key>" where <service> may
		// contain slashes (e.g. "charly/enc/immich-ml" = service:"charly/enc",
		// key:"immich-ml"). Reuse the canonical split from credential_config.
		service, key := parseCompositeKey(entry)
		if service == "" || key == "" {
			continue
		}
		_, _, ferr := c.findItemAnyCollection(service, key, cfg.KeyringCollectionLabel)
		if ferr != nil && errors.Is(ferr, ErrSSNotFound) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return []DoctorCheckResult{{
			Name:    "Keyring index consistency",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d/%d", len(cfg.KeyringKeys), len(cfg.KeyringKeys)),
		}}
	}
	return []DoctorCheckResult{{
		Name:    "Keyring index consistency",
		Status:  CheckWarning,
		Version: fmt.Sprintf("%d indexed, %d missing", len(cfg.KeyringKeys), len(missing)),
		Detail: fmt.Sprintf(
			"indexed but not found in any collection: %s",
			strings.Join(missing, ", ")),
		InstallHint: "Re-store with `charly secrets set <service> <key>` or prune stale index entries",
	}}
}
