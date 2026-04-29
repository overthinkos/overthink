package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// vm_import.go — VM adoption.
//
// Reads a libvirt domain's XML, maps it onto a VmSpec with
// source.kind: imported, and persists a kind:vm declaration to vm.yml.
//
// Imported VMs are tracked by ov for lifecycle (start/stop/ssh/console)
// and snapshot operations, but `ov vm build` is a no-op (the disk is
// externally managed). `ov vm clone --from <imported>@<snap>` works
// transparently because clone uses the snapshot's external file as
// backing.

// libvirtDomainXMLForImport is the subset of libvirt domain XML that
// import maps onto VmSpec fields.
type libvirtDomainXMLForImport struct {
	XMLName  xml.Name                  `xml:"domain"`
	Type     string                    `xml:"type,attr"`
	Name     string                    `xml:"name"`
	UUID     string                    `xml:"uuid"`
	Memory   libvirtMemoryForImport    `xml:"memory"`
	Vcpu     libvirtVcpuForImport      `xml:"vcpu"`
	OS       libvirtOSForImport        `xml:"os"`
	Devices  libvirtDevicesForImport   `xml:"devices"`
}

type libvirtMemoryForImport struct {
	Unit  string `xml:"unit,attr"`
	Value int64  `xml:",chardata"`
}

type libvirtVcpuForImport struct {
	Value int `xml:",chardata"`
}

type libvirtOSForImport struct {
	Type    libvirtOSTypeForImport     `xml:"type"`
	Loader  *libvirtOSLoaderForImport  `xml:"loader,omitempty"`
}

type libvirtOSTypeForImport struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr"`
	Value   string `xml:",chardata"`
}

type libvirtOSLoaderForImport struct {
	Type     string `xml:"type,attr"`
	ReadOnly string `xml:"readonly,attr"`
	Secure   string `xml:"secure,attr"`
	Path     string `xml:",chardata"`
}

type libvirtDevicesForImport struct {
	Disks      []libvirtDiskForImport      `xml:"disk"`
	Interfaces []libvirtInterfaceForImport `xml:"interface"`
}

type libvirtDiskForImport struct {
	Type   string `xml:"type,attr"`
	Device string `xml:"device,attr"`
	Driver struct {
		Name string `xml:"name,attr"`
		Type string `xml:"type,attr"`
	} `xml:"driver"`
	Source struct {
		File string `xml:"file,attr"`
	} `xml:"source"`
	Target struct {
		Dev string `xml:"dev,attr"`
		Bus string `xml:"bus,attr"`
	} `xml:"target"`
}

type libvirtInterfaceForImport struct {
	Type string `xml:"type,attr"`
}

// ImportFromLibvirt reads the domain XML for `domainName`, maps it
// onto a VmSpec, and returns the (name, spec) pair ready to be written
// into vm.yml. The targetName argument lets the caller override the
// vm.yml entry key (default: domain name).
func ImportFromLibvirt(domainName, targetName string) (string, *VmSpec, error) {
	if domainName == "" {
		return "", nil, fmt.Errorf("import: domain name is required")
	}

	conn, err := connectLibvirt(libvirtSessionURI)
	if err != nil {
		return "", nil, fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close()

	dom, err := conn.lookupDomain(domainName)
	if err != nil {
		return "", nil, fmt.Errorf("looking up domain %q: %w", domainName, err)
	}
	xmlStr, err := conn.l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return "", nil, fmt.Errorf("reading domain XML for %q: %w", domainName, err)
	}

	var parsed libvirtDomainXMLForImport
	if err := xml.Unmarshal([]byte(xmlStr), &parsed); err != nil {
		return "", nil, fmt.Errorf("parsing domain XML: %w", err)
	}

	spec := &VmSpec{}
	mapMemoryToSpec(parsed.Memory, spec)
	mapVcpuToSpec(parsed.Vcpu, spec)
	mapOSToSpec(parsed.OS, spec)
	mapNetworkToSpec(parsed.Devices.Interfaces, spec)
	primaryDisk := mapDiskToSpec(parsed.Devices.Disks)

	now := time.Now().UTC().Format(time.RFC3339)
	spec.Source = VmSource{
		Kind:        "imported",
		LibvirtName: parsed.Name,
		DiskPath:    primaryDisk.Path,
		DiskFormat:  primaryDisk.Format,
		AdoptedAt:   now,
	}

	name := targetName
	if name == "" {
		// Strip any "ov-" prefix to avoid double-prefixing on subsequent
		// commands (which add their own ov- prefix).
		name = strings.TrimPrefix(parsed.Name, "ov-")
	}
	if name == "" {
		name = parsed.Name
	}
	return name, spec, nil
}

type primaryDiskInfo struct {
	Path   string
	Format string
	Bus    string
}

// mapDiskToSpec returns the path + format of the first <disk
// device='disk'> entry. Snapshots/cdroms are skipped. ov tracks one
// primary disk per VM in V1.
func mapDiskToSpec(disks []libvirtDiskForImport) primaryDiskInfo {
	for _, d := range disks {
		if d.Device != "disk" && d.Device != "" {
			continue
		}
		if d.Type != "file" && d.Type != "" {
			continue
		}
		return primaryDiskInfo{
			Path:   d.Source.File,
			Format: strings.ToLower(d.Driver.Type),
			Bus:    d.Target.Bus,
		}
	}
	return primaryDiskInfo{}
}

func mapMemoryToSpec(m libvirtMemoryForImport, spec *VmSpec) {
	// Convert to a human-readable size with the unit attribute (KiB,
	// MiB, GiB). Default unit is KiB if missing.
	unit := strings.ToLower(m.Unit)
	if unit == "" {
		unit = "kib"
	}
	bytesPerUnit := int64(1024) // KiB
	switch unit {
	case "kib", "kb":
		bytesPerUnit = 1024
	case "mib", "mb":
		bytesPerUnit = 1024 * 1024
	case "gib", "gb":
		bytesPerUnit = 1024 * 1024 * 1024
	case "b":
		bytesPerUnit = 1
	}
	totalBytes := m.Value * bytesPerUnit
	gib := totalBytes / (1024 * 1024 * 1024)
	if gib > 0 && totalBytes%(1024*1024*1024) == 0 {
		spec.Ram = fmt.Sprintf("%dG", gib)
		return
	}
	mib := totalBytes / (1024 * 1024)
	spec.Ram = fmt.Sprintf("%dM", mib)
}

func mapVcpuToSpec(v libvirtVcpuForImport, spec *VmSpec) {
	if v.Value > 0 {
		spec.Cpus = v.Value
	}
}

func mapOSToSpec(os libvirtOSForImport, spec *VmSpec) {
	if os.Type.Machine != "" {
		spec.Machine = os.Type.Machine
	}
	if os.Loader == nil {
		spec.Firmware = "bios"
		return
	}
	// UEFI: distinguish secure-boot-capable from insecure based on
	// loader path heuristics (libvirt records OVMF_CODE.fd vs
	// OVMF_CODE.secboot.fd).
	path := strings.ToLower(os.Loader.Path)
	switch {
	case strings.Contains(path, "secboot"):
		spec.Firmware = "uefi-secure"
	case os.Loader.Secure == "yes":
		spec.Firmware = "uefi-secure"
	default:
		spec.Firmware = "uefi-insecure"
	}
}

func mapNetworkToSpec(ifaces []libvirtInterfaceForImport, spec *VmSpec) {
	if len(ifaces) == 0 {
		return
	}
	t := strings.ToLower(ifaces[0].Type)
	switch t {
	case "user":
		spec.Network = &VmNetwork{Mode: "user"}
	case "bridge":
		spec.Network = &VmNetwork{Mode: "bridge"}
	case "network":
		spec.Network = &VmNetwork{Mode: "nat"}
	}
}

// ListUnmanagedDomains returns libvirt domains that are NOT recorded
// in ov's vm.yml (not yet imported).
func ListUnmanagedDomains() ([]string, error) {
	conn, err := connectLibvirt(libvirtSessionURI)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}
	defer conn.Close()

	domains, _, err := conn.l.ConnectListAllDomains(1, 0)
	if err != nil {
		return nil, fmt.Errorf("listing domains: %w", err)
	}

	managed, _ := loadManagedVmSet()
	var out []string
	for _, d := range domains {
		// Filter ov-managed domains (those with the ov- prefix that
		// also appear in vm.yml).
		ovName := strings.TrimPrefix(d.Name, "ov-")
		if managed[ovName] || managed[d.Name] {
			continue
		}
		out = append(out, d.Name)
	}
	return out, nil
}

// loadManagedVmSet returns the set of VM names recorded in the
// project's vm.yml (or overthink.yml). Returns an empty set on any
// error; ListUnmanagedDomains tolerates missing config gracefully.
func loadManagedVmSet() (map[string]bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return map[string]bool{}, err
	}
	for _, fn := range []string{"vm.yml", "overthink.yml"} {
		path := filepath.Join(cwd, fn)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Vm map[string]any `yaml:"vm"`
		}
		_ = yaml.Unmarshal(data, &doc)
		out := map[string]bool{}
		for k := range doc.Vm {
			out[k] = true
		}
		return out, nil
	}
	return map[string]bool{}, nil
}

// WriteVmImportDeclaration persists a kind:vm declaration with
// source.kind: imported into vm.yml (or overthink.yml).
func WriteVmImportDeclaration(name string, spec *VmSpec) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(cwd, "vm.yml")
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			alt := filepath.Join(cwd, "overthink.yml")
			if _, err2 := os.Stat(alt); err2 == nil {
				target = alt
			} else {
				return fmt.Errorf("neither vm.yml nor overthink.yml found in %s; run `ov new project` first or create vm.yml manually", cwd)
			}
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("reading %s: %w", target, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", target, err)
	}
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top-level YAML is not a mapping", target)
	}
	topMap := root.Content[0]
	vmMap := findOrCreateMapEntry(topMap, "vm")
	if alreadyHas(vmMap, name) {
		return fmt.Errorf("%s: vm entry %q already exists", target, name)
	}
	entry := buildImportedVmNode(spec)
	vmMap.Content = append(vmMap.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name},
		entry,
	)
	out, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("marshaling YAML: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	return os.Rename(tmp, target)
}

// buildImportedVmNode constructs the YAML mapping for an imported VM
// spec.
func buildImportedVmNode(spec *VmSpec) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	source := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addStrPair(source, "kind", "imported")
	addStrPair(source, "libvirt_name", spec.Source.LibvirtName)
	if spec.Source.DiskPath != "" {
		addStrPair(source, "disk_path", spec.Source.DiskPath)
	}
	if spec.Source.DiskFormat != "" {
		addStrPair(source, "disk_format", spec.Source.DiskFormat)
	}
	if spec.Source.AdoptedAt != "" {
		addStrPair(source, "adopted_at", spec.Source.AdoptedAt)
	}
	n.Content = append(n.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "source"},
		source,
	)
	if spec.Ram != "" {
		addStrPair(n, "ram", spec.Ram)
	}
	if spec.Cpus > 0 {
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "cpus"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", spec.Cpus)},
		)
	}
	if spec.Machine != "" {
		addStrPair(n, "machine", spec.Machine)
	}
	if spec.Firmware != "" {
		addStrPair(n, "firmware", spec.Firmware)
	}
	if spec.Network != nil && spec.Network.Mode != "" {
		netNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		addStrPair(netNode, "mode", spec.Network.Mode)
		n.Content = append(n.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "network"},
			netNode,
		)
	}
	return n
}
