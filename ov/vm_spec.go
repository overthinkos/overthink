package main

// VmSpec is the canonical VM configuration shape. Consumed by both the
// cloud-image path (`source.kind: cloud_image`) and the bootc path
// (`source.kind: bootc`). Every field except Source, CloudInit, and the
// Source-branch-specific subfields applies equally to both source kinds —
// that's the parity guarantee: anything configurable for cloud_image VMs
// is configurable for bootc VMs, and vice versa.
//
// See the approved plan D1/D12 for the full rationale.
type VmSpec struct {
	// Source is a discriminated union: exactly one of the two branches
	// (cloud_image or bootc) must be populated, matching Source.Kind.
	Source VmSource `yaml:"source"`

	// --- Hardware (both source kinds) ---

	DiskSize string `yaml:"disk_size,omitempty"` // e.g. "20G", "10 GiB"
	Ram      string `yaml:"ram,omitempty"`       // e.g. "4G", "8192M"
	Cpus     int    `yaml:"cpus,omitempty"`      // e.g. 2
	Machine  string `yaml:"machine,omitempty"`   // q35 | virt (arm64) | i440fx — default: host-native
	Firmware string `yaml:"firmware,omitempty"`  // bios | uefi-insecure | uefi-secure — default: bios

	// --- Network (structured; replaces old VmConfig.Network string tag) ---

	Network *VmNetwork `yaml:"network,omitempty"`

	// --- SSH + key injection ---

	SSH *VmSSH `yaml:"ssh,omitempty"`

	// --- Cloud-init (structured intent; rendered at run time) ---

	// Only meaningful when Source.Kind == "cloud_image", or when
	// Source.Kind == "bootc" AND the bootc image includes the
	// `cloud-init` layer AND SSH.KeyInjection.CloudInit is enabled.
	CloudInit *VmCloudInit `yaml:"cloud_init,omitempty"`

	// --- Fully-generic libvirt / qemu configuration ---

	Libvirt *LibvirtDomain `yaml:"libvirt,omitempty"`

	// --- Target-specific tests (optional; default empty) ---
	//
	// Layer tests and image tests propagate automatically via the existing
	// composition machinery. These slots are ONLY for tests genuinely
	// specific to the VM template (e.g., checking a cloud-init runcmd
	// took effect, probing a libvirt device).
	Tests       []Check `yaml:"tests,omitempty"`
	DeployTests []Check `yaml:"deploy_tests,omitempty"`
}

// Note: per /ov-dev:disposable, disposability is a DEPLOY property and
// lives exclusively on DeploymentNode. The former `VmSpec.Disposable`
// / `VmSpec.Lifecycle` fields were removed in the schema-v3 cutover —
// the `ov rebuild <vm-name>` authorization reads from the
// DeploymentNode(s) that reference this VM via `vm_source:` (see
// rebuild.go:vmDisposableFromDeployments). `ov migrate deploy-v3`
// moves any residual flags on a user's on-disk configs to the
// matching deployment entries.

// VmSource is the discriminated-union source for a VM disk image.
// Kind selects the active branch — "cloud_image" uses URL/Checksum/Cache;
// "bootc" uses Image/Transport/Rootfs/RootSize/KernelArgs.
type VmSource struct {
	// Kind discriminates the two branches. Must be "cloud_image" or "bootc".
	Kind string `yaml:"kind"`

	// --- Cloud-image branch ---

	// URL is the HTTPS location of a pre-built qcow2 cloud image.
	URL string `yaml:"url,omitempty"`

	// Checksum integrity for the fetched URL. If Value is empty and
	// Type is sha256 (the default), the fetcher attempts to auto-resolve
	// a sidecar file (<url>.SHA256 / .sha256 / .sha256sum).
	Checksum VmChecksum `yaml:"checksum,omitempty"`

	// Cache is the directory where fetched qcow2 images are stored.
	// Content-addressed by sha256(url). Supports resumable downloads.
	// Default: ~/.cache/ov/vm-images/
	Cache string `yaml:"cache,omitempty"`

	// BaseUser is the upstream cloud image's pre-existing user account
	// that ov adopts (mirrors the container-side `base_user:` +
	// `user_policy: adopt` pattern). When set:
	//   - The cloud-init renderer emits a merge-by-name entry
	//     (`users: [default, {name: <base_user>, ssh_authorized_keys:
	//     […]}]`) so cloud-init appends the SSH pubkey to the existing
	//     account's authorized_keys WITHOUT recreating the user,
	//     touching its shell, or rewriting /etc/sudoers.
	//   - spec.ssh.user defaults to this value, so `ov vm ssh <vm>`
	//     connects as the upstream's account.
	// Common values: "arch" (Arch cloud image), "ubuntu" (Ubuntu),
	// "fedora" (Fedora cloud), "debian" (Debian), "cloud-user" (CentOS).
	// Leave empty only if the image has no default account — then
	// you MUST declare a custom user in spec.cloud_init.users with
	// full sudo/groups/shell fields.
	BaseUser string `yaml:"base_user,omitempty"`

	// --- Bootc branch ---

	// Image is the kind:image entry name (or full OCI tag) that this
	// bootc VM was built from. `bootc install to-disk` reads the image
	// by ref during VM disk construction.
	Image string `yaml:"image,omitempty"`

	// Transport controls how `bootc install` reads the container image:
	//   registry            — pull from the image's registry
	//   containers-storage  — read from local rootful podman storage
	//   oci                 — read an OCI directory layout
	//   oci-archive         — read an OCI tarball
	Transport string `yaml:"transport,omitempty"`

	// Rootfs is the root filesystem type for the bootc install.
	// One of: ext4, xfs, btrfs. Default: ext4.
	Rootfs string `yaml:"rootfs,omitempty"`

	// RootSize caps the root partition at a fixed size (leaving the
	// remaining DiskSize unpartitioned). Empty means "fill DiskSize".
	RootSize string `yaml:"root_size,omitempty"`

	// KernelArgs are additional kernel command-line parameters appended
	// via `bootc install --karg <...>`.
	KernelArgs string `yaml:"kernel_args,omitempty"`
}

// VmChecksum is the integrity check for a VmSource URL fetch.
type VmChecksum struct {
	Type  string `yaml:"type,omitempty"`  // sha256 (only supported algorithm for now)
	Value string `yaml:"value,omitempty"` // hex digest; empty → auto-fetch <url>.SHA256 sidecar
}

// VmNetwork is the structured network configuration for a VM. Replaces
// the legacy VmConfig.Network string tag.
type VmNetwork struct {
	// Model is the virtio device model: virtio-net-pci (default),
	// e1000, rtl8139, etc.
	Model string `yaml:"model,omitempty"`

	// Mode is the network mode:
	//   user   — QEMU user-mode networking with host port forwards (default)
	//   bridge — attach to a named Linux bridge
	//   nat    — libvirt-managed NAT (libvirt backend only)
	Mode string `yaml:"mode,omitempty"`

	// Bridge is the name of the Linux bridge when Mode == "bridge".
	Bridge string `yaml:"bridge,omitempty"`

	// MAC pins the interface MAC address. Empty → auto-generated stable
	// MAC derived from the VM name.
	MAC string `yaml:"mac,omitempty"`

	// PortForwards are additional host:guest TCP port mappings on top of
	// the SSH port forward (which is auto-added from VmSSH.Port → 22).
	// Format: "host:guest", e.g. "8080:80".
	PortForwards []string `yaml:"port_forwards,omitempty"`
}

// VmSSH configures SSH access to the guest: the user created at
// provisioning time, the host-side SSH port forward, key source, and
// per-channel key-injection toggles.
type VmSSH struct {
	// User is the guest user to create (cloud-image source) or use
	// (bootc source). Defaults:
	//   cloud_image → "ov"
	//   bootc       → "root"
	User string `yaml:"user,omitempty"`

	// Port is the host port forwarded to guest :22. Default: 2222.
	Port int `yaml:"port,omitempty"`

	// KeySource controls which SSH public key gets injected into the
	// guest. Values:
	//   auto          — use the first ~/.ssh/*.pub found (default)
	//   generate      — create a new ed25519 keypair in ~/.local/share/ov/vm/<vm>/
	//   none          — inject no key
	//   <absolute_path> — read the specified .pub file
	KeySource string `yaml:"key_source,omitempty"`

	// KeyInjection controls the injection channels. When nil, per-source-kind
	// auto-defaults apply (see D13):
	//   cloud_image → {smbios: enabled, cloud_init: enabled}  (additive)
	//   bootc       → {smbios: enabled, cloud_init: disabled}
	KeyInjection *VmKeyInjection `yaml:"key_injection,omitempty"`
}

// VmKeyInjection toggles the SSH key delivery channels. Values are
// tri-state strings: "auto" (apply default), "enabled", or "disabled".
// The two channels are additive — having both on is the safe default
// for cloud_image VMs and there is no duplication cost at the guest.
type VmKeyInjection struct {
	// SMBIOS injects the SSH key via SMBIOS type 11 OEM strings.
	// The guest's systemd-ssh-generator (systemd ≥ v250) materializes
	// the pubkey into ~<user>/.ssh/authorized_keys at boot.
	SMBIOS string `yaml:"smbios,omitempty"`

	// CloudInit embeds the SSH key in the rendered user-data under
	// `users:[...].ssh_authorized_keys`. Only has effect when a seed
	// ISO is emitted (always for cloud_image; optional for bootc).
	CloudInit string `yaml:"cloud_init,omitempty"`
}
