// CUE schema for the `vm` kind. #Vm validates ONE value of the `vm:` map
// (VmSpec). FULLY MODELED + CLOSED: every VmSpec field, the 5-arm #VmSource
// union, the structured #VmCloudInit (cloud_init_types.go) and the ~54-subtype
// #LibvirtDomain tree (libvirt_yaml.go) are modeled and CLOSED — an unknown
// key is a typo. Genuine passthroughs stay typed-open: libvirt.snippets /
// libvirt.xml_passthrough (raw XML), cloud_init.extra (raw cloud-config),
// cloud_init.network.ethernets (cloud-init network-config v2), and every
// libvirt map[string]string Go field as `{[string]: string}`.
//
// Cross-rules mirror libvirt_validate.go: firmware:uefi-secure ⇒ machine≠i440fx,
// ssh.port ⊕ ssh.port_auto, cpu.mode:custom ⇒ model required, hostdev pci ⇒ hex
// source domain/bus/slot/function. Shared #Step from _common.cue (R3).

#Vm: {
	source: #VmSource

	disk_size?: string
	ram?:       string
	cpu?:       int & >=1 // yaml key is singular `cpu` (VmSpec.Cpus yaml:"cpu")
	machine?:   "q35" | "virt" | "i440fx" | "pc"
	// firmware is REQUIRED-with-default (not optional): an if-guard can only
	// reference a field that always resolves to a concrete value, and an
	// OPTIONAL field — even one carrying a default — errors with "cannot
	// reference optional field" when absent. Required-with-default materializes
	// "bios" on omission (matching the Go empty→bios behavior) AND stays
	// referenceable by the uefi-secure cross-rule below.
	firmware: *"bios" | "uefi-insecure" | "uefi-secure"

	backend:   *"auto" | "libvirt" | "qemu"
	autostart: *false | true
	// autostart:true requires the libvirt backend (qemu has no persistent daemon).
	if autostart {
		backend: "auto" | "libvirt"
	}
	// Secure Boot needs Q35 SMM — i440fx can't supply it (libvirt_validate.go).
	// machine stays OPTIONAL (Go allows empty machine with uefi-secure), so the
	// constraint is `machine?: !=…`, not `machine: !=…` (the latter would force
	// machine present and false-reject the common omit-machine uefi-secure case).
	if firmware == "uefi-secure" {
		machine?: !="i440fx"
	}

	network?:    #VmNetwork
	ssh?:        #VmSSH
	cloud_init?: #VmCloudInit
	libvirt?:    #LibvirtDomain

	plan?: [...#Step]
	snapshot?: [...#VmSnapshot]
}

// 5-way discriminated union on source.kind; each arm pins kind, requires its
// fields, forbids cross-branch fields via _|_, and is CLOSED (no trailing `...`)
// so an unmodeled key is a typo.
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
	}

#VmChecksum: {
	type?:  "sha256"
	value?: string & =~"^[0-9a-fA-F]{64}$"
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
}

#VmSSH: {
	user?:          string
	port?:          int & >=0 & <=65535
	port_auto?:     bool
	key_source?:    *"auto" | "generate" | "none" | (string & =~"^/")
	key_injection?: #VmKeyInjection
	// port and port_auto are mutually exclusive (libvirt_validate.go: PortAuto &&
	// Port>0 is an error): port_auto is false/absent OR port is ≤0/absent. The
	// disjunction keeps the struct CLOSED — an embedded matchN would open it.
} & ({port_auto?: false} | {port?: int & <=0})

#VmKeyInjection: {
	smbios?:     "auto" | "enabled" | "disabled"
	cloud_init?: "auto" | "enabled" | "disabled"
}

#VmSnapshot: {
	name:         string & !=""
	description?: string
	mode?:        *"external" | "internal"
	quiesce?:     bool
	from?:        string
}

// ---------------------------------------------------------------------------
// cloud_init: VmCloudInit (cloud_init_types.go). CLOSED. Genuine passthroughs:
// extra (raw cloud-config string) and network.ethernets (network-config v2,
// map[string]map[string]any → {[string]: {[string]: _}}).
// ---------------------------------------------------------------------------
#VmCloudInit: {
	hostname?: string
	timezone?: string
	locale?:   string
	users?: [...#VmCloudInitUser]
	package?: [...string]
	runcmd?: [...string]
	bootcmd?: [...string]
	write_files?: [...#VmCloudInitFile]
	network?:        #VmCloudInitNetwork
	mirrors?:        #VmCloudInitMirrors
	charly_install?: #VmCharlyInstall
	extra?:          string // raw cloud-config YAML escape hatch (verbatim passthrough)
}

#VmCloudInitUser: {
	name: string & !="" // VmCloudInitUser.Name yaml:"name" — required
	sudo?: bool
	groups?: [...string]
	shell?:       string
	lock_passwd?: bool
}

#VmCloudInitFile: {
	path:     string & !="" // VmCloudInitFile.Path yaml:"path" — required
	content?: string
	owner?:   string
	perms?:   string // cloud-init perms, e.g. "0644" — no Go validator, kept plain
	encoding?: string // "" | b64 | gz | gz+b64 — no Go validator, kept plain
}

#VmCloudInitNetwork: {
	version?: int
	// network-config v2 map[string]map[string]any — typed-open passthrough.
	ethernets?: {[string]: {[string]: _}}
}

#VmCloudInitMirrors: {
	apt?: [...string]
	dnf?: [...string]
	pacman?: [...string]
}

#VmCharlyInstall: {
	// VmCharlyInstall has ONLY `strategy` (the vm-spec skill's url/checksum are
	// STALE — the Go struct dropped them). auto: scp host binary post-boot;
	// scp: explicit form; skip: user-managed.
	strategy?: *"auto" | "scp" | "skip"
}

// ---------------------------------------------------------------------------
// libvirt: LibvirtDomain (libvirt_yaml.go). CLOSED, every sub-type modeled as a
// #Libvirt<Name> def. Genuine passthroughs stay typed (NOT blanket `{...}`):
// snippets/xml_passthrough (raw XML) and every map[string]string Go field as
// `{[string]: string}`.
// ---------------------------------------------------------------------------
#LibvirtDomain: {
	snippets?: [...string]   // raw XML strings (candy-composed) — typed passthrough
	xml_passthrough?: string // verbatim libvirt XML fragment — typed passthrough
	features?:        #LibvirtFeatures
	cpu?:             #LibvirtCPU
	clock?:           #LibvirtClock
	memory_backing?:  #LibvirtMemoryBacking
	memtune?:         #LibvirtMemTune
	numatune?:        #LibvirtNUMATune
	cputune?:         #LibvirtCPUTune
	iothreads?:       int
	devices?:         #LibvirtDevices
	seclabel?:        #LibvirtSecLabel
	launch_security?: #LibvirtLaunchSecurity
	resource?:        #LibvirtResource
	sysinfo?:         #LibvirtSysInfo
}

#LibvirtFeatures: {
	acpi?:   bool
	apic?:   bool
	pae?:    bool
	smm?:    bool
	hap?:    bool
	vmport?: bool
	pmu?:    bool
	hyperv?: #LibvirtHyperV
	kvm?:    #LibvirtKVM
	ibs?:    string
}

// HyperV enlightenment toggles — all "on"/"off"-ish strings; no Go validator,
// kept plain string to avoid false-rejecting valid libvirt values.
#LibvirtHyperV: {
	relaxed?:         string
	vapic?:           string
	spinlocks?:       #LibvirtSpinlocks
	vpindex?:         string
	runtime?:         string
	synic?:           string
	stimer?:          string
	reset?:           string
	vendor_id?:       #LibvirtVendorID
	frequencies?:     string
	reenlightenment?: string
	tlbflush?:        string
	ipi?:             string
	evmcs?:           string
}

#LibvirtSpinlocks: {
	state?:   string
	retries?: int
}

#LibvirtVendorID: {
	state?: string
	value?: string
}

#LibvirtKVM: {
	hidden?:          string
	hint_dedicated?:  string
	poll_control?:    string
	pv_ipi?:          string
	dirty_ring_size?: int
}

#LibvirtCPU: {
	// mode is REQUIRED-with-default (renderer default host-passthrough) so the
	// custom⇒model if-guard below can reference it (optional fields error when
	// absent). #LibvirtCPU only instantiates when `cpu:` is present.
	mode: *"host-passthrough" | "host-model" | "custom"
	model?:      string
	check?:      string // none|partial|full — no Go validator, kept plain
	migratable?: string // on|off — no Go validator, kept plain
	topology?:   #LibvirtCPUTopology
	features?: [...#LibvirtCPUFeature]
	cache?: #LibvirtCPUCache
	numa?: [...#LibvirtNUMACell]
	// custom mode requires model (libvirt_validate.go).
	if mode == "custom" {
		model: string & !=""
	}
}

#LibvirtCPUTopology: {
	sockets?: int
	dies?:    int
	cores?:   int
	threads?: int
}

#LibvirtCPUFeature: {
	policy?: "force" | "require" | "optional" | "disable" | "forbid"
	name:    string & !="" // LibvirtCPUFeature.Name yaml:"name" — required
}

#LibvirtCPUCache: {
	mode?:  string // emulate|passthrough|disable — no Go validator, kept plain
	level?: int
}

#LibvirtNUMACell: {
	id?:        int
	cpus?:      string
	memory?:    string
	unit?:      string
	memaccess?: string
}

#LibvirtClock: {
	offset?:     "utc" | "localtime" | "timezone" | "variable" | "absolute"
	timezone?:   string
	adjustment?: string
	basis?:      string
	timers?: [...#LibvirtTimer]
}

#LibvirtTimer: {
	name:        string & !="" // LibvirtTimer.Name yaml:"name" — required
	present?:    string
	track?:      string
	tickpolicy?: string
	frequency?:  int
	mode?:       string
}

#LibvirtMemoryBacking: {
	hugepages?:    #LibvirtHugepages
	nosharepages?: bool
	locked?:       bool
	source?:       string // file|anonymous|memfd — no Go validator, kept plain
	access?:       string // shared|private — no Go validator, kept plain
	allocation?:   string // immediate|ondemand — no Go validator, kept plain
	discard?:      bool
}

#LibvirtHugepages: {
	size?:    string
	nodeset?: string
}

#LibvirtMemTune: {
	hard_limit?:      string
	soft_limit?:      string
	swap_hard_limit?: string
	min_guarantee?:   string
}

#LibvirtNUMATune: {
	memory?: #LibvirtNUMAMemory
	memnodes?: [...#LibvirtMemnode]
}

#LibvirtNUMAMemory: {
	mode?:      string
	nodeset?:   string
	placement?: string
}

#LibvirtMemnode: {
	cellid?:  int
	mode?:    string
	nodeset?: string
}

#LibvirtCPUTune: {
	shares?:          int
	period?:          int
	quota?:           int
	global_period?:   int
	global_quota?:    int
	emulator_period?: int
	emulator_quota?:  int
	iothread_period?: int
	iothread_quota?:  int
	vcpupin?: [...#LibvirtVCPUPin]
	emulatorpin?: #LibvirtEmulatorPin
	iothreadpin?: [...#LibvirtIOThreadPin]
}

#LibvirtVCPUPin: {
	vcpu:   int            // LibvirtVCPUPin.VCPU yaml:"vcpu" — required
	cpuset: string & !=""  // LibvirtVCPUPin.CPUSet yaml:"cpuset" — required
}

#LibvirtEmulatorPin: {
	cpuset: string & !="" // required
}

#LibvirtIOThreadPin: {
	iothread: int           // required
	cpuset:   string & !="" // required
}

#LibvirtDevices: {
	emulator?: string
	disks?: [...#LibvirtDisk]
	interfaces?: [...#LibvirtInterface]
	channels?: [...#LibvirtChannel]
	serial?: [...#LibvirtSerial]
	console?: [...#LibvirtConsole]
	parallel?: [...#LibvirtParallel]
	graphics?: [...#LibvirtGraphics]
	video?: [...#LibvirtVideo]
	audio?: [...#LibvirtAudio]
	sound?: [...#LibvirtSound]
	inputs?: [...#LibvirtInput]
	usb?: [...#LibvirtUSB]
	redirdev?: [...#LibvirtRedirDev]
	hostdevs?: [...#LibvirtHostdev]
	filesystems?: [...#LibvirtFilesystem]
	rng?: [...#LibvirtRNG]
	tpm?: [...#LibvirtTPM]
	watchdog?: [...#LibvirtWatchdog]
	memballoon?: #LibvirtMemBalloon
	shmem?: [...#LibvirtShmem]
	iommu?: #LibvirtIOMMU
	vsock?: #LibvirtVsock
	panic?: [...#LibvirtPanic]
	smartcard?: [...#LibvirtSmartcard]
	hub?: [...#LibvirtHub]
}

#LibvirtDisk: {
	type?:   string // file|block|network|volume — no Go validator, kept plain
	device?: string
	source?: {[string]: string}
	target?: {[string]: string}
	driver?: {[string]: string}
	readonly?: bool
	serial?:   string
	wwn?:      string
	boot?:     int
}

#LibvirtInterface: {
	type?:   string
	source?: {[string]: string}
	model?:  string
	mac?:    string
	mtu?:    int
	driver?: {[string]: string}
	boot?:   int
	port_forwards?: [...#LibvirtPortForward]
}

#LibvirtPortForward: {
	proto?: string
	start:  int // LibvirtPortForward.Start yaml:"start" — required
	to?:    int
}

#LibvirtChannel: {
	type?:   string
	name?:   string
	path?:   string
	source?: string // LibvirtChannel.Source is a scalar string (not a map)
}

#LibvirtSerial: {
	type?:   string
	source?: {[string]: string}
	target?: {[string]: string}
}

#LibvirtConsole: {
	type?:   string
	target?: {[string]: string}
}

#LibvirtParallel: {
	type?:   string
	source?: {[string]: string}
	target?: {[string]: string}
}

#LibvirtGraphics: {
	type:      "vnc" | "spice" | "rdp" | "sdl" | "egl-headless" // required (libvirt_validate.go)
	port?:     int
	autoport?: string
	listen?:   #LibvirtListen
	passwd?:   string
	keymap?:   string
	gl?:       string
}

// LibvirtGraphicsListeners union: scalar address | single map | list of maps.
#LibvirtListen: string | #LibvirtListenOne | [...#LibvirtListenOne]
#LibvirtListenOne: {
	type?:    string
	address?: string
	network?: string
	socket?:  string
}

#LibvirtVideo: {
	model:    string & !="" // LibvirtVideo.Model required (libvirt_validate.go); "none" is valid
	vram?:    int
	heads?:   int
	accel3d?: bool
	primary?: bool
}

#LibvirtAudio: {
	type?: string
	id?:   int
}

#LibvirtSound: {
	model: string & !="" // LibvirtSound.Model yaml:"model" — required
}

#LibvirtInput: {
	type: string & !="" // LibvirtInput.Type yaml:"type" — required
	bus?: string
}

#LibvirtUSB: {
	model?: string
	port?:  int
}

#LibvirtRedirDev: {
	bus?:  string
	type?: string
}

// PCI source address component: 0x-hex OR bare decimal (hexUintPtr accepts both).
#LibvirtPCIHex: string & =~"^(0[xX][0-9a-fA-F]+|[0-9]+)$"

#LibvirtHostdev: {
	type:     "pci" | "usb" | "scsi" | "mdev" // required (libvirt_validate.go)
	mode?:    string
	managed?: "yes" | "no"
	source: {[string]: string} // LibvirtHostdev.Source yaml:"source" — required typed map
	rom?:    {[string]: string}
	driver?: {[string]: string}
	// PCI passthrough requires hex source domain/bus/slot/function
	// (libvirt_validate.go); a malformed address silently drops <source>.
	if type == "pci" {
		source: {
			domain:   #LibvirtPCIHex
			bus:      #LibvirtPCIHex
			slot:     #LibvirtPCIHex
			function: #LibvirtPCIHex
			...
		}
	}
}

#LibvirtFilesystem: {
	type?:       string
	driver?:     "virtiofs" | "9p" | "path"          // libvirt_validate.go
	accessmode?: "passthrough" | "mapped" | "squash" // libvirt_validate.go
	source: string & !="" // required (host path)
	target: string & !="" // required (guest mount tag)
	readonly?: bool
	binary?: {[string]: string}
}

#LibvirtRNG: {
	model?:   string
	backend?: string
	rate?: {[string]: string}
}

#LibvirtTPM: {
	model?: string
	backend?: {[string]: string}
}

#LibvirtWatchdog: {
	model:   string & !="" // LibvirtWatchdog.Model yaml:"model" — required
	action?: string
}

#LibvirtMemBalloon: {
	model:        string & !="" // LibvirtMemBalloon.Model yaml:"model" — required
	autodeflate?: string
	stats?: {[string]: int}
}

#LibvirtShmem: {
	name:  string & !="" // LibvirtShmem.Name yaml:"name" — required
	role?: string
	model?: {[string]: string}
	size?:  string
	server?: {[string]: string}
}

#LibvirtIOMMU: {
	model: string & !="" // LibvirtIOMMU.Model yaml:"model" — required
	driver?: {[string]: string}
}

#LibvirtVsock: {
	model?: string
	cid?: {[string]: string}
}

#LibvirtPanic: {
	model?: string
	address?: {[string]: string}
}

#LibvirtSmartcard: {
	mode?: string
	type?: string
}

#LibvirtHub: {
	type: string & !="" // LibvirtHub.Type yaml:"type" — required
}

#LibvirtSecLabel: {
	type?:       string
	model?:      string
	relabel?:    string
	label?:      string
	baselabel?:  string
	imagelabel?: string
}

#LibvirtLaunchSecurity: {
	type?:              "sev" | "sev-es" | "sev-snp" | "tdx" // libvirt_validate.go
	cbitpos?:           int
	reduced_phys_bits?: int
	policy?:            string
	dh_cert?:           string
	session?:           string
	kernel_hashes?:     string
}

#LibvirtResource: {
	partition?: string
	fibrechannel?: {[string]: string}
}

#LibvirtSysInfo: {
	type?: string
	bios?: {[string]: string}
	system?: {[string]: string}
	baseboard?: [...{[string]: string}]
	chassis?: {[string]: string}
	processor?: [...{[string]: string}]
	oem_strings?: [...string]
}
