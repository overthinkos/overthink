// CUE schema for the `vm` kind. #Vm validates ONE value of the `vm:` map
// (VmSpec). Hardware/firmware/backend/network/ssh invariants constrained;
// `libvirt:` (LibvirtDomain, 30+ sub-types) and `cloud_init:` (VmCloudInit) are
// OPEN passthrough for now (tightened later). Shared #Step from _common.cue.

#Vm: {
	source: #VmSource

	disk_size?: string
	ram?:       string
	cpu?:       int & >=1 // yaml key is singular `cpu` (VmSpec.Cpus yaml:"cpu")
	machine?:   "q35" | "virt" | "i440fx" | "pc"
	firmware?:  *"bios" | "uefi-insecure" | "uefi-secure"

	backend:   *"auto" | "libvirt" | "qemu"
	autostart: *false | true
	// autostart:true requires the libvirt backend (qemu has no persistent daemon).
	if autostart {
		backend: "auto" | "libvirt"
	}

	network?: #VmNetwork
	ssh?:     #VmSSH
	cloud_init?: {...} // OPEN passthrough (VmCloudInit)
	libvirt?: {...}    // OPEN passthrough (LibvirtDomain)

	plan?: [...#Step]
	snapshot?: [...#VmSnapshot]
	...
}

// 5-way discriminated union on source.kind; each arm pins kind, requires its
// fields, and forbids cross-branch fields via _|_ (collapses under Concrete).
#VmSource:
	{
		kind:       "cloud_image"
		url:        string & !=""
		checksum?:  #VmChecksum
		cache?:     string
		base_user?: string
		box?:         _|_
		transport?:   _|_
		rootfs?:      _|_
		root_size?:   _|_
		kernel_args?: _|_
		...
	} | {
		kind:        "bootc"
		box:         string & !=""
		transport?:  "registry" | "containers-storage" | "oci" | "oci-archive"
		rootfs?:     "ext4" | "xfs" | "btrfs"
		root_size?:  string
		kernel_args?: string
		url?:      _|_
		checksum?: _|_
		cache?:    _|_
		...
	} | {
		kind:              "clone"
		from_vm:           string & !=""
		from_snapshot:     string & !=""
		cloud_init_clean?: bool
		url?:          _|_
		box?:          _|_
		libvirt_name?: _|_
		disk_path?:    _|_
		disk_format?:  _|_
		...
	} | {
		kind:            "imported"
		libvirt_name:    string & !=""
		disk_path:       string & !=""
		disk_format:     "qcow2" | "raw"
		adopted_at?:     string
		last_synced_at?: string
		url?:           _|_
		box?:           _|_
		from_vm?:       _|_
		from_snapshot?: _|_
		...
	} | {
		kind:               "bootstrap"
		builder:            string & !=""
		distro:             string & !=""
		builder_image?:     string
		rootfs?:            "ext4" | "xfs" | "btrfs"
		root_size?:         string
		kernel_args?:       string
		package?: [...string]
		bootstrap_arch?:    string
		bootstrap_variant?: string
		url?:       _|_
		box?:       _|_
		transport?: _|_
		...
	}

#VmChecksum: {
	type?:  "sha256"
	value?: string & =~"^[0-9a-fA-F]{64}$"
	...
}

#VmNetwork: {
	model?: string
	mode:   *"user" | "bridge" | "nat" | "network"
	bridge?: string
	mac?:   string
	port_forwards?: [...(string & =~":")]
	if mode == "bridge" {
		bridge: string & !=""
	}
	...
}

#VmSSH: {
	user?:          string
	port?:          int & >=0 & <=65535
	port_auto?:     bool
	key_source?:    *"auto" | "generate" | "none" | (string & =~"^/")
	key_injection?: #VmKeyInjection
	...
}

#VmKeyInjection: {
	smbios?:     "auto" | "enabled" | "disabled"
	cloud_init?: "auto" | "enabled" | "disabled"
	...
}

#VmSnapshot: {
	name:         string & !=""
	description?: string
	mode?:        *"external" | "internal"
	quiesce?:     bool
	from?:        string
	...
}
