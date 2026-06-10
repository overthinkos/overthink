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
	Cpus     int    `yaml:"cpu,omitempty"`       // e.g. 2 (YAML key `cpu` — native VmSpec key, singular per the field-singular cutover; rendered to libvirt <vcpu>)
	Machine  string `yaml:"machine,omitempty"`   // q35 | virt (arm64) | i440fx — default: host-native
	Firmware string `yaml:"firmware,omitempty"`  // bios | uefi-insecure | uefi-secure — default: bios

	// Backend pins the VM backend for THIS entity (auto | libvirt | qemu),
	// overriding the global vm.backend setting. Honored by VmCreateCmd.Run.
	// Previously this field was MISSING, so a `backend:` key on a vm entity
	// was silently dropped and the pin never took effect (the documented
	// "pin backend: libvirt so the auto→qemu fallback can't mask a missing
	// daemon" behavior was a no-op until this field was added).
	Backend string `yaml:"backend,omitempty"`

	// Autostart makes libvirt start this domain when the session daemon
	// starts. For qemu:///session that only fires at host boot when the
	// user's systemd instance is lingering, so VmCreateCmd also enables
	// linger + the virtqemud user socket (see ensureBootAutostartPrereqs).
	// Requires the libvirt backend — rejected with backend: qemu, which
	// has no persistent daemon to honor it.
	Autostart bool `yaml:"autostart,omitempty"`

	// --- Network (structured; replaces old VmConfig.Network string tag) ---

	Network *VmNetwork `yaml:"network,omitempty"`

	// --- SSH + key injection ---

	SSH *VmSSH `yaml:"ssh,omitempty"`

	// --- Cloud-init (structured intent; rendered at run time) ---

	// Only meaningful when Source.Kind == "cloud_image", or when
	// Source.Kind == "bootc" AND the bootc image includes the
	// `cloud-init` candy AND SSH.KeyInjection.CloudInit is enabled.
	CloudInit *VmCloudInit `yaml:"cloud_init,omitempty"`

	// --- Fully-generic libvirt / qemu configuration ---

	Libvirt *LibvirtDomain `yaml:"libvirt,omitempty"`

	// --- Target-specific tests (optional; default empty) ---
	//
	// Candy tests and box tests propagate automatically via the existing
	// composition machinery. These slots are ONLY for tests genuinely
	// specific to the VM template (e.g., checking a cloud-init runcmd
	// took effect, probing a libvirt device).
	Eval       []Check `yaml:"eval,omitempty"`
	DeployEval []Check `yaml:"deploy_eval,omitempty"`

	// --- Declarative snapshot intent (optional; default empty) ---
	//
	// Documents which named snapshots THIS template expects to have. Read
	// by `charly vm snapshot list <vm>` to flag missing-but-expected snapshots.
	// Actual existing snapshots live in registry.json under
	// ~/.local/share/charly/vm/charly-<vm>/snapshots/registry.json — the
	// declarative list is intent, not inventory.
	Snapshots []VmSnapshotDecl `yaml:"snapshot,omitempty"`
}

// VmSnapshotDecl is one entry in the declarative `vm.<name>.snapshots`
// list. Records intent — this template is expected to have a snapshot
// of the given name and mode. The actual existence is tracked in the
// per-VM registry.json. The `From` field is forward-looking: V1 builds
// snapshot chains implicitly (whichever is current at create-time
// becomes the parent); V2 may honor explicit chaining.
type VmSnapshotDecl struct {
	// Name uniquely identifies the snapshot within this VM (registry key).
	Name string `yaml:"name"`

	// Description is an optional human-facing note about what the snapshot
	// captures. Persisted into registry.json.meta.
	Description string `yaml:"description,omitempty"`

	// Mode is "external" (default; clone-friendly, separate qcow2 file)
	// or "internal" (embedded in the qcow2 via qemu-img snapshot). See
	// /charly-vm:vm "snapshot modes" for the tradeoff matrix.
	Mode string `yaml:"mode,omitempty"`

	// Quiesce, when true, instructs the snapshot creation path to flush
	// guest state via guest-agent fsfreeze before snapshotting (with
	// libvirt's plain freeze as fallback when qemu-guest-agent is absent).
	Quiesce bool `yaml:"quiesce,omitempty"`

	// From names the parent snapshot in a multi-tier chain. RESERVED for
	// V2; V1 builds chains implicitly at create-time. Setting this in V1
	// produces a one-line warning but is not enforced.
	From string `yaml:"from,omitempty"`
}

// Note: per /charly-internals:disposable, disposability is a DEPLOY property and
// lives exclusively on DeploymentNode. `VmSpec.Disposable`
// / `VmSpec.Lifecycle` are not VmSpec fields —
// the `charly update <vm-name>` authorization reads from the
// DeploymentNode(s) that reference this VM via `vm:` (see
// run_subcommand.go:vmDisposableFromDeployments). `charly migrate`
// moves any residual flags on a user's on-disk configs to the
// matching deployment entries.

// VmSource is the discriminated-union source for a VM disk image.
// Kind selects the active branch:
//   - "cloud_image" — fetch a pre-built qcow2 from URL/Checksum/Cache
//   - "bootc"       — `bootc install to-disk` from a kind:image entry
//   - "clone"       — qcow2 backing-chain overlay on another VM's snapshot
//   - "imported"    — adopt an externally-managed VM (virsh-defined,
//     virt-manager-created, etc.); charly tracks lifecycle
//     but does not rebuild the disk
type VmSource struct {
	// Kind discriminates the branches. Must be "cloud_image", "bootc",
	// "clone", or "imported".
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
	// Default: ~/.cache/charly/vm-images/
	Cache string `yaml:"cache,omitempty"`

	// BaseUser is the upstream cloud image's pre-existing user account
	// that charly adopts (mirrors the container-side `base_user:` +
	// `user_policy: adopt` pattern). When set:
	//   - The cloud-init renderer emits a merge-by-name entry
	//     (`users: [default, {name: <base_user>, ssh_authorized_keys:
	//     […]}]`) so cloud-init appends the SSH pubkey to the existing
	//     account's authorized_keys WITHOUT recreating the user,
	//     touching its shell, or rewriting /etc/sudoers.
	//   - spec.ssh.user defaults to this value, so `charly vm ssh <vm>`
	//     connects as the upstream's account.
	// Common values: "arch" (Arch cloud image), "ubuntu" (Ubuntu),
	// "fedora" (Fedora cloud), "debian" (Debian), "cloud-user" (CentOS).
	// Leave empty only if the image has no default account — then
	// you MUST declare a custom user in spec.cloud_init.users with
	// full sudo/groups/shell fields.
	BaseUser string `yaml:"base_user,omitempty"`

	// --- Bootc branch ---

	// Box is the kind:image entry name (or full OCI tag) that this
	// bootc VM was built from. `bootc install to-disk` reads the image
	// by ref during VM disk construction.
	Box string `yaml:"box,omitempty"`

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

	// --- Clone branch (Kind == "clone") ---
	//
	// FromVm names the parent kind:vm entity whose snapshot disk this
	// clone backs onto. The parent VM must exist and be addressable in
	// the same project. Required when Kind == "clone".
	FromVm string `yaml:"from_vm,omitempty"`

	// FromSnapshot names the snapshot on FromVm to use as the backing
	// disk for the clone. The snapshot must exist (created via
	// `charly vm snapshot create <FromVm> <FromSnapshot>`). Required when
	// Kind == "clone". When the named snapshot is mode=internal, the
	// build path auto-promotes via `qemu-img convert` before creating
	// the overlay, with a one-line stderr note.
	FromSnapshot string `yaml:"from_snapshot,omitempty"`

	// CloudInitClean, when true, injects a `runcmd: cloud-init clean
	// --machine-id --logs` entry into the clone's user-data so the
	// guest regenerates its machine-id and SSH host keys on first boot.
	// Default false to avoid surprising existing workflows. Recommended
	// true for clones that will run alongside the parent on the same
	// network.
	CloudInitClean bool `yaml:"cloud_init_clean,omitempty"`

	// --- Imported branch (Kind == "imported") ---
	//
	// LibvirtName is the libvirt domain name for the externally-managed
	// VM. Often differs from charly's `charly-<vm>` prefixed naming convention,
	// because adoption preserves the upstream tool's name. Required when
	// Kind == "imported".
	LibvirtName string `yaml:"libvirt_name,omitempty"`

	// DiskPath is the absolute path to the disk image file as recorded
	// in the libvirt domain's `<disk source file=/>` element. charly
	// commands consult this for snapshot operations and clone-backing
	// resolution; charly NEVER overwrites this file. Required when
	// Kind == "imported".
	DiskPath string `yaml:"disk_path,omitempty"`

	// DiskFormat is the qemu image format declared by the libvirt
	// domain (`<driver type=/>`): typically "qcow2" or "raw".
	// Required when Kind == "imported".
	DiskFormat string `yaml:"disk_format,omitempty"`

	// AdoptedAt is the ISO8601 timestamp when `charly vm import` first
	// recorded this entry. Informational; aids audit trails.
	AdoptedAt string `yaml:"adopted_at,omitempty"`

	// LastSyncedAt is the ISO8601 timestamp of the most recent
	// `charly vm import --update` invocation. Empty when the entry has
	// never been re-synced (still matches the original adoption).
	LastSyncedAt string `yaml:"last_synced_at,omitempty"`

	// --- Bootstrap branch (Kind == "bootstrap") ---
	//
	// Builder names a kind:bootstrap builder declared in build.yml
	// (e.g. "pacstrap", "debootstrap", "alpine-bootstrap"). The builder
	// definition supplies the rootfs-creation template; the Distro field
	// supplies the per-distro config (base packages, keyring init,
	// repos, bootloader install).
	Builder string `yaml:"builder,omitempty"`

	// BuilderImage is the OCI image ref of the privileged builder used
	// to host the rootfs creation step. Typically points at a
	// project-internal image with arch-install-scripts / debootstrap /
	// apk pre-installed (e.g. arch-pacstrap-builder). Required for
	// privileged bootstrap builders.
	BuilderImage string `yaml:"builder_image,omitempty"`

	// Distro selects the DistroDef in build.yml whose Pacstrap /
	// Debootstrap / AlpineBootstrap / Bootloader sub-blocks drive the
	// bootstrap and bootloader install. Examples: "arch", "cachyos",
	// "debian", "ubuntu", "alpine".
	Distro string `yaml:"distro,omitempty"`

	// Package is the per-VM additional package list passed to the
	// bootstrap command alongside the distro's base packages.
	Package []string `yaml:"package,omitempty"`

	// BootstrapArch picks the target architecture for bootstrap (mostly
	// relevant for debootstrap which needs `--arch=amd64`/`arm64`).
	// Defaults to host arch when empty.
	BootstrapArch string `yaml:"bootstrap_arch,omitempty"`

	// BootstrapVariant is debootstrap's `--variant=` argument
	// (minbase|buildd|fakechroot|...). Defaults to "minbase".
	BootstrapVariant string `yaml:"bootstrap_variant,omitempty"`
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
	//   cloud_image → "charly"
	//   bootc       → "root"
	User string `yaml:"user,omitempty"`

	// Port is the host port forwarded to guest :22. Default: 2222.
	Port int `yaml:"port,omitempty"`

	// PortAuto, when true, auto-allocates a free host port for the SSH
	// forward at `charly vm create` and persists it in vm_state.ssh_port (reused
	// on rebuild — idempotent). Mutually exclusive with Port. Lets concurrent
	// VM beds avoid fixed host-port collisions, mirroring the pod path's
	// `port: [auto]`.
	PortAuto bool `yaml:"port_auto,omitempty"`

	// KeySource controls which SSH public key gets injected into the
	// guest. Values:
	//   auto          — use the first ~/.ssh/*.pub found (default)
	//   generate      — create a new ed25519 keypair in ~/.local/share/charly/vm/<vm>/
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
