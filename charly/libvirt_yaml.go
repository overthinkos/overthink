package main

// LibvirtDomain is the opencharly YAML-facing shape for the libvirt
// <domain> configuration (and the applicable-subset QEMU argv).
//
// vm.yml authors write this struct as the `libvirt:` stanza under a
// kind:vm entity. At render time it converts to a libvirtxml.Domain
// via ToLibvirtXML (see libvirt_yaml_bridge.go) and is marshaled to
// the XML libvirt actually consumes.
//
// The YAML shape is preserved verbatim from the prior LibvirtConfig
// type — existing vm.yml files load unchanged. The rename reflects
// the post-cutover architecture: the opencharly YAML layer is a
// translation facade over libvirtxml, not an independent schema.
//
// Raw-XML escape hatches:
//   - Snippets: runtime-injected via InjectLibvirtXML (post-define).
//     Preserved from the prior design for candy-level `libvirt:`
//     composition — candies contribute XML fragments that are not
//     known until the box is composed.
//   - XMLPassthrough: declarative verbatim libvirt XML fragments
//     merged into the rendered domain at ToLibvirtXML time (Rule 6
//     of the YAML↔XML mapping table). Read directly from vm.yml
//     and baked into the domain XML before libvirtxml.Marshal.
type LibvirtDomain struct {
	// Snippets are raw XML strings classified by the existing
	// isDeviceElement helper: device-scoped elements are injected
	// into <devices>, domain-scoped elements before </domain>.
	// Deduplicated by exact string match. Composed by the candy
	// machinery (CollectLibvirtSnippets); NOT a user-authored
	// vm.yml field.
	Snippets []string `yaml:"snippets,omitempty"`

	// XMLPassthrough accepts one or more verbatim libvirt XML
	// fragments. Each fragment is parsed by libvirtxml.Unmarshal
	// at render time and merged into the canonical domain XML at
	// the correct parent element (determined by the fragment's
	// root element: <launchSecurity> into <domain>, <vsock> into
	// <devices>, etc.). Lets users reach every libvirt feature
	// without waiting for a first-class YAML field to land.
	XMLPassthrough string `yaml:"xml_passthrough,omitempty"`

	// --- Structured declarative fields ---

	Features       *LibvirtFeatures       `yaml:"features,omitempty"`
	CPU            *LibvirtCPU            `yaml:"cpu,omitempty"`
	Clock          *LibvirtClock          `yaml:"clock,omitempty"`
	MemoryBacking  *LibvirtMemoryBacking  `yaml:"memory_backing,omitempty"`
	MemTune        *LibvirtMemTune        `yaml:"memtune,omitempty"`
	NUMATune       *LibvirtNUMATune       `yaml:"numatune,omitempty"`
	CPUTune        *LibvirtCPUTune        `yaml:"cputune,omitempty"`
	IOThreads      int                    `yaml:"iothreads,omitempty"`
	Devices        *LibvirtDevices        `yaml:"devices,omitempty"`
	SecLabel       *LibvirtSecLabel       `yaml:"seclabel,omitempty"`
	LaunchSecurity *LibvirtLaunchSecurity `yaml:"launch_security,omitempty"`
	Resource       *LibvirtResource       `yaml:"resource,omitempty"`
	SysInfo        *LibvirtSysInfo        `yaml:"sysinfo,omitempty"`
}

// LibvirtFeatures toggles hypervisor-level features. Nil-pointer means
// "renderer default": {ACPI: true, APIC: true}.
type LibvirtFeatures struct {
	ACPI   *bool          `yaml:"acpi,omitempty"`
	APIC   *bool          `yaml:"apic,omitempty"`
	PAE    *bool          `yaml:"pae,omitempty"`
	SMM    *bool          `yaml:"smm,omitempty"` // required for UEFI secure boot (D17)
	HAP    *bool          `yaml:"hap,omitempty"`
	VMPort *bool          `yaml:"vmport,omitempty"`
	PMU    *bool          `yaml:"pmu,omitempty"`
	HyperV *LibvirtHyperV `yaml:"hyperv,omitempty"`
	KVM    *LibvirtKVM    `yaml:"kvm,omitempty"`
	IBS    string         `yaml:"ibs,omitempty"`
}

// LibvirtHyperV toggles Hyper-V enlightenments (Windows guest perf).
type LibvirtHyperV struct {
	Relaxed         string            `yaml:"relaxed,omitempty"` // "on" | "off"
	VAPIC           string            `yaml:"vapic,omitempty"`
	Spinlocks       *LibvirtSpinlocks `yaml:"spinlocks,omitempty"`
	VPIndex         string            `yaml:"vpindex,omitempty"`
	Runtime         string            `yaml:"runtime,omitempty"`
	Synic           string            `yaml:"synic,omitempty"`
	STimer          string            `yaml:"stimer,omitempty"`
	Reset           string            `yaml:"reset,omitempty"`
	VendorID        *LibvirtVendorID  `yaml:"vendor_id,omitempty"`
	Frequencies     string            `yaml:"frequencies,omitempty"`
	Reenlightenment string            `yaml:"reenlightenment,omitempty"`
	TLBFlush        string            `yaml:"tlbflush,omitempty"`
	IPI             string            `yaml:"ipi,omitempty"`
	EVMCS           string            `yaml:"evmcs,omitempty"`
}

type LibvirtSpinlocks struct {
	State   string `yaml:"state,omitempty"` // "on" | "off"
	Retries int    `yaml:"retries,omitempty"`
}

type LibvirtVendorID struct {
	State string `yaml:"state,omitempty"`
	Value string `yaml:"value,omitempty"`
}

// LibvirtKVM toggles KVM paravirt features (e.g. hidden flag).
type LibvirtKVM struct {
	Hidden        string `yaml:"hidden,omitempty"` // "on" | "off"
	HintDedicated string `yaml:"hint_dedicated,omitempty"`
	PollControl   string `yaml:"poll_control,omitempty"`
	PVIPI         string `yaml:"pv_ipi,omitempty"`
	DirtyRingSize int    `yaml:"dirty_ring_size,omitempty"`
}

// LibvirtCPU is the CPU configuration. The renderer applies D16
// defaults (mode: host-passthrough, check: none, +vmx/+svm auto-added
// per host vendor for nested virt) when this struct is nil.
type LibvirtCPU struct {
	Mode       string              `yaml:"mode,omitempty"`       // host-passthrough (default) | host-model | custom
	Model      string              `yaml:"model,omitempty"`      // required when Mode == custom
	Check      string              `yaml:"check,omitempty"`      // none (default) | partial | full
	Migratable string              `yaml:"migratable,omitempty"` // "on" | "off"
	Topology   *LibvirtCPUTopology `yaml:"topology,omitempty"`
	Features   []LibvirtCPUFeature `yaml:"features,omitempty"`
	Cache      *LibvirtCPUCache    `yaml:"cache,omitempty"`
	NUMA       []LibvirtNUMACell   `yaml:"numa,omitempty"`
}

type LibvirtCPUTopology struct {
	Sockets int `yaml:"sockets,omitempty"`
	Dies    int `yaml:"dies,omitempty"`
	Cores   int `yaml:"cores,omitempty"`
	Threads int `yaml:"threads,omitempty"`
}

type LibvirtCPUFeature struct {
	Policy string `yaml:"policy"` // force | require | optional | disable | forbid
	Name   string `yaml:"name"`
}

type LibvirtCPUCache struct {
	Mode  string `yaml:"mode,omitempty"`  // emulate | passthrough | disable
	Level int    `yaml:"level,omitempty"` // cache level (1, 2, 3)
}

type LibvirtNUMACell struct {
	ID        int    `yaml:"id,omitempty"`
	CPUs      string `yaml:"cpus,omitempty"`   // "0-3"
	Memory    string `yaml:"memory,omitempty"` // "2048"
	Unit      string `yaml:"unit,omitempty"`   // "MiB"
	MemAccess string `yaml:"memaccess,omitempty"`
}

// LibvirtClock configures guest timekeeping.
type LibvirtClock struct {
	Offset     string         `yaml:"offset,omitempty"`     // utc | localtime | timezone | variable | absolute
	Timezone   string         `yaml:"timezone,omitempty"`   // when Offset == timezone
	Adjustment string         `yaml:"adjustment,omitempty"` // when Offset == variable
	Basis      string         `yaml:"basis,omitempty"`      // when Offset == variable: "utc" | "localtime"
	Timers     []LibvirtTimer `yaml:"timers,omitempty"`
}

type LibvirtTimer struct {
	Name       string `yaml:"name"`                 // rtc | pit | hpet | tsc | kvmclock | hypervclock | armvtimer
	Present    string `yaml:"present,omitempty"`    // "yes" | "no"
	Track      string `yaml:"track,omitempty"`      // boot | guest | wall | realtime
	TickPolicy string `yaml:"tickpolicy,omitempty"` // delay | catchup | merge | discard
	Frequency  int    `yaml:"frequency,omitempty"`
	Mode       string `yaml:"mode,omitempty"` // auto | native | emulate | paravirt | smpsafe
}

// LibvirtMemoryBacking configures backing store for guest memory.
type LibvirtMemoryBacking struct {
	Hugepages    *LibvirtHugepages `yaml:"hugepages,omitempty"`
	NoSharepages *bool             `yaml:"nosharepages,omitempty"`
	Locked       *bool             `yaml:"locked,omitempty"`
	Source       string            `yaml:"source,omitempty"`     // file | anonymous | memfd
	Access       string            `yaml:"access,omitempty"`     // shared | private
	Allocation   string            `yaml:"allocation,omitempty"` // immediate | ondemand
	Discard      *bool             `yaml:"discard,omitempty"`
}

type LibvirtHugepages struct {
	Size    string `yaml:"size,omitempty"` // "2M" | "1G"
	NodeSet string `yaml:"nodeset,omitempty"`
}

// LibvirtMemTune provides memory limit hints to the hypervisor.
type LibvirtMemTune struct {
	HardLimit     string `yaml:"hard_limit,omitempty"` // e.g. "8G"
	SoftLimit     string `yaml:"soft_limit,omitempty"`
	SwapHardLimit string `yaml:"swap_hard_limit,omitempty"`
	MinGuarantee  string `yaml:"min_guarantee,omitempty"`
}

// LibvirtNUMATune pins guest memory to host NUMA nodes.
type LibvirtNUMATune struct {
	Memory   *LibvirtNUMAMemory `yaml:"memory,omitempty"`
	MemNodes []LibvirtMemnode   `yaml:"memnodes,omitempty"`
}

type LibvirtNUMAMemory struct {
	Mode      string `yaml:"mode,omitempty"`      // strict | preferred | interleave | restrictive
	Nodeset   string `yaml:"nodeset,omitempty"`   // "0-1"
	Placement string `yaml:"placement,omitempty"` // static | auto
}

type LibvirtMemnode struct {
	CellID  int    `yaml:"cellid,omitempty"`
	Mode    string `yaml:"mode,omitempty"`
	Nodeset string `yaml:"nodeset,omitempty"`
}

// LibvirtCPUTune configures guest vCPU scheduling + pinning.
type LibvirtCPUTune struct {
	Shares         int                  `yaml:"shares,omitempty"`
	Period         int                  `yaml:"period,omitempty"`
	Quota          int                  `yaml:"quota,omitempty"`
	GlobalPeriod   int                  `yaml:"global_period,omitempty"`
	GlobalQuota    int                  `yaml:"global_quota,omitempty"`
	EmulatorPeriod int                  `yaml:"emulator_period,omitempty"`
	EmulatorQuota  int                  `yaml:"emulator_quota,omitempty"`
	IOThreadPeriod int                  `yaml:"iothread_period,omitempty"`
	IOThreadQuota  int                  `yaml:"iothread_quota,omitempty"`
	VCPUPin        []LibvirtVCPUPin     `yaml:"vcpupin,omitempty"`
	EmulatorPin    *LibvirtEmulatorPin  `yaml:"emulatorpin,omitempty"`
	IOThreadPin    []LibvirtIOThreadPin `yaml:"iothreadpin,omitempty"`
}

type LibvirtVCPUPin struct {
	VCPU   int    `yaml:"vcpu"`
	CPUSet string `yaml:"cpuset"` // "0-3" | "0,2,4"
}

type LibvirtEmulatorPin struct {
	CPUSet string `yaml:"cpuset"`
}

type LibvirtIOThreadPin struct {
	IOThread int    `yaml:"iothread"`
	CPUSet   string `yaml:"cpuset"`
}

// LibvirtDevices is the big one: everything under the libvirt <devices>
// element. Empty lists are valid — the renderer always synthesizes the
// root disk + ssh interface from VmSpec runtime parameters separately;
// these are additions on top.
type LibvirtDevices struct {
	Emulator    string              `yaml:"emulator,omitempty"`   // path to qemu binary (e.g. /usr/bin/qemu-system-x86_64)
	Disks       []LibvirtDisk       `yaml:"disks,omitempty"`      // additional disks beyond the root qcow2
	Interfaces  []LibvirtInterface  `yaml:"interfaces,omitempty"` // additional NICs beyond user-mode SSH-forwarded
	Channels    []LibvirtChannel    `yaml:"channels,omitempty"`   // qemu-guest-agent, spice-webdav
	Serial      []LibvirtSerial     `yaml:"serial,omitempty"`
	Console     []LibvirtConsole    `yaml:"console,omitempty"`
	Parallel    []LibvirtParallel   `yaml:"parallel,omitempty"`
	Graphics    []LibvirtGraphics   `yaml:"graphics,omitempty"` // vnc, spice, rdp
	Video       []LibvirtVideo      `yaml:"video,omitempty"`
	Audio       []LibvirtAudio      `yaml:"audio,omitempty"`
	Sound       []LibvirtSound      `yaml:"sound,omitempty"`
	Inputs      []LibvirtInput      `yaml:"inputs,omitempty"`
	USB         []LibvirtUSB        `yaml:"usb,omitempty"`
	RedirDev    []LibvirtRedirDev   `yaml:"redirdev,omitempty"`
	Hostdevs    []LibvirtHostdev    `yaml:"hostdevs,omitempty"`    // PCI / USB / SCSI passthrough
	Filesystems []LibvirtFilesystem `yaml:"filesystems,omitempty"` // virtiofs / 9p host↔guest shares
	RNG         []LibvirtRNG        `yaml:"rng,omitempty"`
	TPM         []LibvirtTPM        `yaml:"tpm,omitempty"`
	Watchdog    []LibvirtWatchdog   `yaml:"watchdog,omitempty"`
	MemBalloon  *LibvirtMemBalloon  `yaml:"memballoon,omitempty"`
	Shmem       []LibvirtShmem      `yaml:"shmem,omitempty"`
	IOMMU       *LibvirtIOMMU       `yaml:"iommu,omitempty"`
	Vsock       *LibvirtVsock       `yaml:"vsock,omitempty"`
	Panic       []LibvirtPanic      `yaml:"panic,omitempty"`
	Smartcard   []LibvirtSmartcard  `yaml:"smartcard,omitempty"`
	Hub         []LibvirtHub        `yaml:"hub,omitempty"`
}

// --- Per-device structs ---

type LibvirtDisk struct {
	Type     string            `yaml:"type,omitempty"`   // file | block | network | volume
	Device   string            `yaml:"device,omitempty"` // disk | cdrom | floppy | lun
	Source   map[string]string `yaml:"source,omitempty"` // {file: /path} | {dev: /dev/…} | {pool, volume} etc.
	Target   map[string]string `yaml:"target,omitempty"` // {dev: vda, bus: virtio}
	Driver   map[string]string `yaml:"driver,omitempty"` // {name: qemu, type: qcow2, cache: none, io: native}
	Readonly *bool             `yaml:"readonly,omitempty"`
	Serial   string            `yaml:"serial,omitempty"`
	WWN      string            `yaml:"wwn,omitempty"`
	Boot     int               `yaml:"boot,omitempty"` // boot order
}

type LibvirtInterface struct {
	Type         string               `yaml:"type,omitempty"`   // user | bridge | network | direct
	Source       map[string]string    `yaml:"source,omitempty"` // {bridge: virbr0} | {network: default}
	Model        string               `yaml:"model,omitempty"`  // virtio (virtio-net-pci) | e1000 | rtl8139
	MAC          string               `yaml:"mac,omitempty"`
	MTU          int                  `yaml:"mtu,omitempty"`
	Driver       map[string]string    `yaml:"driver,omitempty"`
	Boot         int                  `yaml:"boot,omitempty"`
	PortForwards []LibvirtPortForward `yaml:"port_forwards,omitempty"` // for type: user
}

type LibvirtPortForward struct {
	Proto string `yaml:"proto,omitempty"` // tcp | udp
	Start int    `yaml:"start"`
	To    int    `yaml:"to,omitempty"`
}

type LibvirtChannel struct {
	Type   string `yaml:"type,omitempty"` // virtio | spicevmc | unix | pty
	Name   string `yaml:"name,omitempty"` // e.g. org.qemu.guest_agent.0
	Path   string `yaml:"path,omitempty"`
	Source string `yaml:"source,omitempty"`
}

type LibvirtSerial struct {
	Type   string            `yaml:"type,omitempty"` // pty | tcp | file | unix | dev
	Source map[string]string `yaml:"source,omitempty"`
	Target map[string]string `yaml:"target,omitempty"`
}

type LibvirtConsole struct {
	Type   string            `yaml:"type,omitempty"`
	Target map[string]string `yaml:"target,omitempty"`
}

type LibvirtParallel struct {
	Type   string            `yaml:"type,omitempty"`
	Source map[string]string `yaml:"source,omitempty"`
	Target map[string]string `yaml:"target,omitempty"`
}

type LibvirtGraphics struct {
	Type     string                   `yaml:"type"`               // vnc | spice | rdp | sdl
	Port     int                      `yaml:"port,omitempty"`     // -1 → autoport
	AutoPort string                   `yaml:"autoport,omitempty"` // "yes" | "no"
	Listen   LibvirtGraphicsListeners `yaml:"listen,omitempty"`   // scalar / map / list — see libvirt_yaml_listen.go
	Passwd   string                   `yaml:"passwd,omitempty"`
	Keymap   string                   `yaml:"keymap,omitempty"`
	GL       string                   `yaml:"gl,omitempty"` // "yes" | "no" (for 3D accel)
}

type LibvirtVideo struct {
	Model   string `yaml:"model"`          // virtio | vga | cirrus | qxl | bochs | ramfb | none
	VRAM    int    `yaml:"vram,omitempty"` // kilobytes
	Heads   int    `yaml:"heads,omitempty"`
	Accel3D *bool  `yaml:"accel3d,omitempty"`
	Primary *bool  `yaml:"primary,omitempty"`
}

type LibvirtAudio struct {
	Type string `yaml:"type,omitempty"` // none | spice | oss | pulse | alsa | coreaudio
	ID   int    `yaml:"id,omitempty"`
}

type LibvirtSound struct {
	Model string `yaml:"model"` // ac97 | ich6 | ich9 | usb | virtio
}

type LibvirtInput struct {
	Type string `yaml:"type"`          // tablet | mouse | keyboard
	Bus  string `yaml:"bus,omitempty"` // ps2 | usb | virtio
}

type LibvirtUSB struct {
	Model string `yaml:"model,omitempty"`
	Port  int    `yaml:"port,omitempty"`
}

type LibvirtRedirDev struct {
	Bus  string `yaml:"bus,omitempty"`  // usb
	Type string `yaml:"type,omitempty"` // spicevmc | tcp
}

type LibvirtHostdev struct {
	Type    string            `yaml:"type"`              // pci | usb | scsi | mdev
	Mode    string            `yaml:"mode,omitempty"`    // subsystem | capabilities
	Managed string            `yaml:"managed,omitempty"` // "yes" | "no"
	Source  map[string]string `yaml:"source"`            // {domain, bus, slot, function} | {vendor, product} | …
	ROM     map[string]string `yaml:"rom,omitempty"`
	Driver  map[string]string `yaml:"driver,omitempty"`
}

type LibvirtFilesystem struct {
	Type       string            `yaml:"type,omitempty"`       // mount (default) | block | file | template | ram
	Driver     string            `yaml:"driver,omitempty"`     // virtiofs | 9p | path
	AccessMode string            `yaml:"accessmode,omitempty"` // passthrough | mapped | squash
	Source     string            `yaml:"source"`               // host path
	Target     string            `yaml:"target"`               // guest mount tag
	Readonly   *bool             `yaml:"readonly,omitempty"`
	Binary     map[string]string `yaml:"binary,omitempty"` // virtiofsd knobs
}

type LibvirtRNG struct {
	Model   string            `yaml:"model,omitempty"`   // virtio (default)
	Backend string            `yaml:"backend,omitempty"` // /dev/urandom | /dev/random | builtin | egd
	Rate    map[string]string `yaml:"rate,omitempty"`    // {period, bytes}
}

type LibvirtTPM struct {
	Model   string            `yaml:"model,omitempty"`   // tpm-tis | tpm-crb | tpm-spapr
	Backend map[string]string `yaml:"backend,omitempty"` // {type: emulator, version: "2.0"} | {type: passthrough, path: ...}
}

type LibvirtWatchdog struct {
	Model  string `yaml:"model"`            // i6300esb | ib700 | diag288 | itco
	Action string `yaml:"action,omitempty"` // reset | shutdown | poweroff | pause | none | dump | inject-nmi
}

type LibvirtMemBalloon struct {
	Model       string         `yaml:"model"` // virtio | none
	Autodeflate string         `yaml:"autodeflate,omitempty"`
	Stats       map[string]int `yaml:"stats,omitempty"` // {period: 5}
}

type LibvirtShmem struct {
	Name   string            `yaml:"name"`
	Role   string            `yaml:"role,omitempty"`
	Model  map[string]string `yaml:"model,omitempty"` // {type: ivshmem-plain | ivshmem-doorbell}
	Size   string            `yaml:"size,omitempty"`
	Server map[string]string `yaml:"server,omitempty"`
}

type LibvirtIOMMU struct {
	Model  string            `yaml:"model"` // intel | smmuv3 | virtio
	Driver map[string]string `yaml:"driver,omitempty"`
}

type LibvirtVsock struct {
	Model string            `yaml:"model,omitempty"` // virtio
	CID   map[string]string `yaml:"cid,omitempty"`   // {auto: "yes"} or {address: "3"}
}

type LibvirtPanic struct {
	Model   string            `yaml:"model,omitempty"` // isa | pseries | hyperv | s390 | pvpanic
	Address map[string]string `yaml:"address,omitempty"`
}

type LibvirtSmartcard struct {
	Mode string `yaml:"mode,omitempty"` // host | host-certificates | passthrough
	Type string `yaml:"type,omitempty"`
}

type LibvirtHub struct {
	Type string `yaml:"type"` // usb
}

// LibvirtSecLabel maps to <seclabel>. Commonly used with SELinux.
type LibvirtSecLabel struct {
	Type       string `yaml:"type,omitempty"`    // dynamic | static | none
	Model      string `yaml:"model,omitempty"`   // selinux | dac | apparmor
	Relabel    string `yaml:"relabel,omitempty"` // "yes" | "no"
	Label      string `yaml:"label,omitempty"`
	BaseLabel  string `yaml:"baselabel,omitempty"`
	ImageLabel string `yaml:"imagelabel,omitempty"`
}

// LibvirtLaunchSecurity covers confidential VMs (AMD SEV/SEV-ES/SEV-SNP, Intel TDX).
type LibvirtLaunchSecurity struct {
	Type            string `yaml:"type,omitempty"` // sev | sev-es | sev-snp | tdx
	CBitPos         int    `yaml:"cbitpos,omitempty"`
	ReducedPhysBits int    `yaml:"reduced_phys_bits,omitempty"`
	Policy          string `yaml:"policy,omitempty"` // hex string
	DhCert          string `yaml:"dh_cert,omitempty"`
	Session         string `yaml:"session,omitempty"`
	KernelHashes    string `yaml:"kernel_hashes,omitempty"` // "yes" | "no"
}

// LibvirtResource is the cgroup partition (rare; mainly for libvirt users
// who run VMs under a named cgroup).
type LibvirtResource struct {
	Partition    string            `yaml:"partition,omitempty"`
	FibreChannel map[string]string `yaml:"fibrechannel,omitempty"`
}

// LibvirtSysInfo is <sysinfo>. Used for SMBIOS credential injection
// (SSH-key-via-SMBIOS channel in D13) and guest BIOS identification.
type LibvirtSysInfo struct {
	Type       string              `yaml:"type,omitempty"` // smbios (default) | fwcfg
	BIOS       map[string]string   `yaml:"bios,omitempty"`
	System     map[string]string   `yaml:"system,omitempty"`
	BaseBoard  []map[string]string `yaml:"baseboard,omitempty"`
	Chassis    map[string]string   `yaml:"chassis,omitempty"`
	Processor  []map[string]string `yaml:"processor,omitempty"`
	OEMStrings []string            `yaml:"oem_strings,omitempty"` // renderer populates this for SMBIOS SSH-key injection
}
