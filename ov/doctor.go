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

// CheckStatus represents the result of a single dependency check.
type CheckStatus int

const (
	CheckOK      CheckStatus = iota // installed and working
	CheckMissing                    // not found
	CheckWarning                    // found but with caveats
	CheckInfo                       // informational (hardware, not a dep)
	CheckAbsent                     // hardware not present (neutral)
)

// CheckResult holds the outcome of a single check.
type CheckResult struct {
	Name        string      `json:"name"`
	Status      CheckStatus `json:"status"`
	Version     string      `json:"version,omitempty"`
	Detail      string      `json:"detail,omitempty"`
	InstallHint string      `json:"install_hint,omitempty"`
}

// CheckGroup organizes checks by feature area.
type CheckGroup struct {
	Name     string        `json:"name"`
	Required bool          `json:"required"`
	OrLogic  bool          `json:"or_logic,omitempty"` // at least one check must pass
	Checks   []CheckResult `json:"checks"`
}

// HardwareInfo holds device detection results for JSON output.
type HardwareInfo struct {
	GPU            bool           `json:"gpu"`
	AMDGPU         bool           `json:"amd_gpu"`
	AMDGFXVersion  string         `json:"amd_gfx_version,omitempty"`
	GPUFlags       []string       `json:"gpu_flags"`
	Devices        []DeviceInfo   `json:"devices"`
	ContainerFlags []string       `json:"container_flags"`
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
			Checks: []CheckResult{
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
			Checks: []CheckResult{
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
			Name:     "Encrypted Storage",
			Required: false,
			Checks: []CheckResult{
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
			Checks: []CheckResult{
				checkBinary("tailscale", distro),
				checkBinary("cloudflared", distro),
			},
		},
		{
			Name:     "Merge & Registry",
			Required: false,
			Checks: []CheckResult{
				checkBinary("skopeo", distro),
			},
		},
		{
			Name:     "Shell & TTY",
			Required: false,
			Checks: []CheckResult{
				checkBinary("script", distro),
			},
		},
	}

	// Only show podman machine group if podman is installed
	if _, err := exec_LookPath("podman"); err == nil {
		groups = append(groups, CheckGroup{
			Name:     "Podman Machine",
			Required: false,
			Checks: []CheckResult{
				checkGvproxyDoctor(distro),
			},
		})
	}

	return groups
}

func buildInfraChecks(distro Distro) []CheckResult {
	checks := []CheckResult{
		checkGo(),
		checkBinary("git", distro),
	}
	// Only check buildx if docker is available
	if _, err := exec_LookPath("docker"); err == nil {
		checks = append(checks, checkBuildxBuilder())
	}
	return checks
}

func vmChecks(distro Distro) []CheckResult {
	qemuBin := qemuSystemBinary()
	checks := []CheckResult{
		checkBinary(qemuBin, distro),
		checkBinary("qemu-img", distro),
		checkVirtiofsd(distro),
		checkBinary("virsh", distro),
		checkBinary("ssh", distro),
		checkLibvirtSocket(),
	}
	return checks
}

// checkVirtiofsd checks for virtiofsd which may be installed outside PATH.
// On Arch Linux it installs to /usr/lib/virtiofsd, on RHEL to /usr/libexec/virtiofsd.
func checkVirtiofsd(distro Distro) CheckResult {
	if path, err := exec_LookPath("virtiofsd"); err == nil {
		return CheckResult{
			Name:    "virtiofsd",
			Status:  CheckOK,
			Detail:  path,
		}
	}
	// Check non-PATH locations where distros install virtiofsd
	for _, path := range []string{"/usr/lib/virtiofsd", "/usr/libexec/virtiofsd"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return CheckResult{
				Name:   "virtiofsd",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return CheckResult{
		Name:        "virtiofsd",
		Status:      CheckMissing,
		Detail:      "not found",
		InstallHint: distro.installHint("virtiofsd"),
	}
}

// checkBinary checks if a binary exists in PATH and tries to get its version.
func checkBinary(name string, distro Distro) CheckResult {
	path, err := exec_LookPath(name)
	if err != nil {
		return CheckResult{
			Name:        name,
			Status:      CheckMissing,
			Detail:      "not found",
			InstallHint: distro.installHint(name),
		}
	}
	version := getBinaryVersion(name)
	return CheckResult{
		Name:    name,
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkGo checks for Go and validates the version.
func checkGo() CheckResult {
	path, err := exec_LookPath("go")
	if err != nil {
		return CheckResult{
			Name:        "go",
			Status:      CheckMissing,
			Detail:      "not found (required to build ov from source)",
			InstallHint: "install Go 1.25.3+",
		}
	}
	version := getBinaryVersion("go")
	return CheckResult{
		Name:    "go",
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkQuadletPodman checks if podman is available for quadlet mode.
func checkQuadletPodman(distro Distro) CheckResult {
	if _, err := exec_LookPath("podman"); err != nil {
		return CheckResult{
			Name:        "podman (for quadlet)",
			Status:      CheckWarning,
			Detail:      "quadlet mode requires podman",
			InstallHint: distro.installHint("podman"),
		}
	}
	return CheckResult{
		Name:   "podman (for quadlet)",
		Status: CheckOK,
		Detail: "available",
	}
}

// checkBuildxBuilder checks if Docker buildx is available.
func checkBuildxBuilder() CheckResult {
	cmd := exec.Command("docker", "buildx", "version")
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:        "docker buildx",
			Status:      CheckMissing,
			Detail:      "not available",
			InstallHint: "install docker-buildx",
		}
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return CheckResult{
		Name:    "docker buildx",
		Status:  CheckOK,
		Version: version,
	}
}

// checkLibvirtSocket checks if the libvirt session socket exists.
func checkLibvirtSocket() CheckResult {
	sockPath := libvirtSessionSocket()
	if _, err := os.Stat(sockPath); err == nil {
		return CheckResult{
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

	return CheckResult{
		Name:        "libvirt session socket",
		Status:      CheckMissing,
		Detail:      sockPath,
		InstallHint: hint,
	}
}

// checkGvproxyDoctor checks gvproxy availability (same logic as checkGvproxy in machine.go).
func checkGvproxyDoctor(distro Distro) CheckResult {
	if _, err := exec_LookPath("gvproxy"); err == nil {
		path, _ := exec_LookPath("gvproxy")
		return CheckResult{
			Name:   "gvproxy",
			Status: CheckOK,
			Detail: path,
		}
	}
	for _, path := range []string{"/usr/libexec/podman/gvproxy", "/usr/local/libexec/podman/gvproxy", "/usr/lib/podman/gvproxy"} {
		if _, err := os.Stat(path); err == nil {
			return CheckResult{
				Name:   "gvproxy",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return CheckResult{
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
var deviceDescriptions = map[string]string{
	"/dev/dri/renderD*": "GPU render node",
	"/dev/kfd":          "AMD Kernel Fusion Driver (ROCm compute)",
	"/dev/kvm":          "KVM virtualization",
	"/dev/vhost-net":    "vhost network acceleration",
	"/dev/vhost-vsock":  "VM socket communication",
	"/dev/fuse":         "FUSE filesystem",
	"/dev/net/tun":      "TUN/TAP network device",
	"/dev/hwrng":        "hardware random number generator",
}

// runHardwareChecks probes for GPU and devices, matching DetectHostDevices() behavior.
func runHardwareChecks(distro Distro) HardwareInfo {
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
	fmt.Println("ov doctor")
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

func formatCheck(ch CheckResult) (string, string) {
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
func secretStorageChecks() []CheckResult {
	var checks []CheckResult

	// Check 1: Secret backend availability
	kr := &KeyringStore{}
	if err := kr.Probe(); err == nil {
		checks = append(checks, CheckResult{
			Name:    "Secret backend",
			Status:  CheckOK,
			Version: "system keyring",
		})
	} else {
		state := GetKeyringState()
		backend := resolveSecretBackend()
		if state == KeyringLocked {
			checks = append(checks, CheckResult{
				Name:        "Secret backend",
				Status:      CheckWarning,
				Version:     "system keyring (LOCKED)",
				Detail:      "keyring is locked — credentials unavailable until unlocked",
				InstallHint: "Unlock your keyring, or run: ov config set secret_backend config",
			})
		} else if backend == "config" {
			checks = append(checks, CheckResult{
				Name:    "Secret backend",
				Status:  CheckOK,
				Version: "config file (explicit)",
			})
		} else {
			checks = append(checks, CheckResult{
				Name:        "Secret backend",
				Status:      CheckWarning,
				Detail:      "config file (no keyring available)",
				InstallHint: "Install gnome-keyring or keepassxc for secure credential storage, or run: ov config set secret_backend config",
			})
		}
	}

	// Check 2: Config file permissions
	configPath, err := RuntimeConfigPath()
	if err == nil {
		if info, statErr := os.Stat(configPath); statErr == nil {
			perm := info.Mode().Perm()
			if perm&0077 == 0 {
				checks = append(checks, CheckResult{
					Name:    "Config permissions",
					Status:  CheckOK,
					Version: fmt.Sprintf("%04o", perm),
				})
			} else {
				checks = append(checks, CheckResult{
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
			checks = append(checks, CheckResult{
				Name:    "Plaintext credentials",
				Status:  CheckOK,
				Version: "0",
			})
		} else {
			checks = append(checks, CheckResult{
				Name:        "Plaintext credentials",
				Status:      CheckWarning,
				Detail:      fmt.Sprintf("%d in config.yml", count),
				InstallHint: "Run: ov config migrate-secrets --dry-run",
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
// healthy/broken counts. Returns one CheckResult per distinguishable state.
// Skips silently (returns nil) if there's no session bus or no collections
// — those cases are already handled by "Secret backend" above.
func checkKeyringHealth() []CheckResult {
	c, err := newSSClient()
	if err != nil {
		// No session bus — already covered by "Secret backend" check. Skip.
		return nil
	}
	defer c.close()

	paths, err := c.collections()
	if err != nil {
		return []CheckResult{{
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
		return []CheckResult{{
			Name:    "Secret Service collections",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d healthy", len(healthy)),
			Detail:  strings.Join(healthy, ", "),
		}}
	}
	return []CheckResult{{
		Name:   "Secret Service collections",
		Status: CheckWarning,
		Version: fmt.Sprintf("%d healthy + %d broken",
			len(healthy), len(broken)),
		Detail: fmt.Sprintf(
			"ov will iterate and skip broken. Broken: %s. Healthy: %s",
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
func checkKeyringIndexConsistency() []CheckResult {
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
		// contain slashes (e.g. "ov/enc/immich-ml" = service:"ov/enc",
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
		return []CheckResult{{
			Name:    "Keyring index consistency",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d/%d", len(cfg.KeyringKeys), len(cfg.KeyringKeys)),
		}}
	}
	return []CheckResult{{
		Name:    "Keyring index consistency",
		Status:  CheckWarning,
		Version: fmt.Sprintf("%d indexed, %d missing", len(cfg.KeyringKeys), len(missing)),
		Detail: fmt.Sprintf(
			"indexed but not found in any collection: %s",
			strings.Join(missing, ", ")),
		InstallHint: "Re-store with `ov secrets set <service> <key>` or prune stale index entries",
	}}
}
