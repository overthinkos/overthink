package main

// VmCloudInit is the structured cloud-init intent for a VM. Rendered
// into a NoCloud seed ISO (user-data / meta-data / network-config) at
// `charly vm build` / `charly deploy add vm:…` time by RenderCloudInit.
//
// No raw user_data YAML string — users declare intent here, charly produces
// the cloud-config YAML at run time. The cloud-init candy, network
// setup, and ssh_authorized_keys injection are all handled by the
// renderer; user-supplied fields extend those defaults rather than
// replacing them. See the approved plan D15.
type VmCloudInit struct {
	// Hostname overrides the guest hostname. Default: <vm-name>.
	Hostname string `yaml:"hostname,omitempty" json:"hostname,omitempty"`

	// Timezone (e.g. "UTC", "Europe/Vienna"). Empty → cloud-init default.
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`

	// Locale (e.g. "en_US.UTF-8"). Empty → distro default.
	Locale string `yaml:"locale,omitempty" json:"locale,omitempty"`

	// Users creates additional users at first boot. The renderer
	// automatically injects the VmSSH.User account (with sudo + ssh
	// pubkey) unless that same name is already present here — in which
	// case the renderer appends the pubkey to the user-declared entry.
	Users []VmCloudInitUser `yaml:"users,omitempty" json:"users,omitempty"`

	// Packages to install at first boot via the guest's native package
	// manager. The renderer always prepends {openssh, curl, tar}
	// (deduplicated). Distro-specific package names pass through.
	Package []string `yaml:"package,omitempty" json:"package,omitempty"`

	// RunCmd are commands executed once at first boot. The renderer
	// prepends {systemctl enable --now sshd} (so distro-specific setup
	// can assume sshd is running). Appended after the renderer's
	// prelude.
	RunCmd []string `yaml:"runcmd,omitempty" json:"runcmd,omitempty"`

	// BootCmd are commands executed on every boot, very early in the
	// boot sequence (before RunCmd). Passes through to cloud-init
	// `bootcmd`.
	BootCmd []string `yaml:"bootcmd,omitempty" json:"bootcmd,omitempty"`

	// WriteFiles places files on the guest filesystem at first boot.
	WriteFiles []VmCloudInitFile `yaml:"write_files,omitempty" json:"write_files,omitempty"`

	// Network configures the guest network via cloud-init's
	// network-config v2 (rendered separately from user-data).
	Network *VmCloudInitNetwork `yaml:"network,omitempty" json:"network,omitempty"`

	// Mirrors overrides distro package-manager mirror URLs.
	Mirrors *VmCloudInitMirrors `yaml:"mirrors,omitempty" json:"mirrors,omitempty"`

	// CharlyInstall controls how the charly binary lands in the guest.
	// Nil → strategy: auto (scp post-boot by VmDeployTarget).
	CharlyInstall *VmCharlyInstall `yaml:"charly_install,omitempty" json:"charly_install,omitempty"`

	// Extra is raw cloud-config YAML merged into the rendered
	// user-data after the structured fields. Use only for long-tail
	// cloud-init options the structured schema doesn't cover.
	// Prefer structured fields above — this is an escape hatch, not a
	// recommended route.
	Extra string `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// VmCloudInitUser declares a user account to create at first boot.
// If Name matches VmSSH.User, the renderer appends the ssh pubkey to
// this entry's ssh_authorized_keys (instead of synthesizing its own
// entry). Otherwise the user is created without an ssh pubkey.
type VmCloudInitUser struct {
	Name string `yaml:"name" json:"name"`

	// Sudo: true → "ALL=(ALL) NOPASSWD:ALL". false → no sudo line.
	Sudo bool `yaml:"sudo,omitempty" json:"sudo,omitempty"`

	// Groups to add the user to (e.g. ["wheel"], ["docker", "video"]).
	Groups []string `yaml:"groups,omitempty" json:"groups,omitempty"`

	// Shell path (e.g. "/bin/bash"). Empty → distro default.
	Shell string `yaml:"shell,omitempty" json:"shell,omitempty"`

	// LockPasswd: true → password login disabled (ssh-key-only).
	// Default: true (cloud-init's own default).
	LockPasswd *bool `yaml:"lock_passwd,omitempty" json:"lock_passwd,omitempty"`
}

// VmCloudInitFile is a file placed on the guest filesystem via
// cloud-init's write_files module.
type VmCloudInitFile struct {
	Path     string `yaml:"path" json:"path"`                             // absolute path inside the guest
	Content  string `yaml:"content" json:"content"`                       // file body (plain text)
	Owner    string `yaml:"owner,omitempty" json:"owner,omitempty"`       // e.g. "root:root" (default)
	Perms    string `yaml:"perms,omitempty" json:"perms,omitempty"`       // e.g. "0644" (default)
	Encoding string `yaml:"encoding,omitempty" json:"encoding,omitempty"` // "" | "b64" | "gz" | "gz+b64"
}

// VmCloudInitNetwork is a thin wrapper around cloud-init's
// network-config v2. The renderer emits this as the seed ISO's
// network-config file. Empty → guest defaults to DHCP on every
// virtio-net interface.
type VmCloudInitNetwork struct {
	// Version pins the network-config schema version. Default: 2.
	Version int `yaml:"version,omitempty" json:"version,omitempty"`

	// Ethernets is a map of interface name → config. Pass-through.
	// Example:
	//   ens3: {dhcp4: true}
	//   ens4: {addresses: ["10.0.0.1/24"], gateway4: "10.0.0.254"}
	Ethernets map[string]map[string]any `yaml:"ethernets,omitempty" json:"ethernets,omitempty"`
}

// VmCloudInitMirrors overrides distro package-manager mirror URLs.
// Keyed by package manager. Unset → distro cloud image defaults.
type VmCloudInitMirrors struct {
	APT    []string `yaml:"apt,omitempty" json:"apt,omitempty"`       // Ubuntu/Debian: rewrites /etc/apt/sources.list
	DNF    []string `yaml:"dnf,omitempty" json:"dnf,omitempty"`       // Fedora: writes /etc/yum.repos.d/custom.repo
	Pacman []string `yaml:"pacman,omitempty" json:"pacman,omitempty"` // Arch: rewrites /etc/pacman.d/mirrorlist
}

// VmCharlyInstall controls how the charly binary lands in the guest. charly is
// delivered POST-BOOT by VmDeployTarget (EnsureCharlyInGuest) — never via
// cloud-init. Default strategy is "auto" when the struct is nil.
type VmCharlyInstall struct {
	// Strategy is one of:
	//   auto  — deliver the host charly binary (os.Executable()) into the
	//           guest post-boot via VmDeployTarget, ONLY when the guest's
	//           own charly is absent or older; a current package-managed
	//           guest charly is preferred and never shadowed (default)
	//   scp   — explicit form of auto
	//   skip  — user manages the charly install themselves; VmDeployTarget
	//           verifies presence only
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
}
