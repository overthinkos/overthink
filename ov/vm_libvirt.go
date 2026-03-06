package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
)

// domainStateRunning is the libvirt domain state for a running VM.
const domainStateRunning = libvirt.DomainRunning

// libvirtConn wraps a go-libvirt connection to the session daemon.
type libvirtConn struct {
	l *libvirt.Libvirt
}

// connectLibvirt connects to the user's libvirt session socket.
func connectLibvirt() (*libvirtConn, error) {
	sockPath := libvirtSessionSocket()
	c, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt session socket %s: %w", sockPath, err)
	}
	l := libvirt.New(c)
	if err := l.Connect(); err != nil {
		c.Close()
		return nil, fmt.Errorf("libvirt handshake failed: %w", err)
	}
	return &libvirtConn{l: l}, nil
}

// Close disconnects from libvirt.
func (c *libvirtConn) Close() error {
	return c.l.Disconnect()
}

// lookupDomain finds a domain by name.
func (c *libvirtConn) lookupDomain(name string) (libvirt.Domain, error) {
	return c.l.DomainLookupByName(name)
}

// domainState returns the current state of a domain.
func (c *libvirtConn) domainState(dom libvirt.Domain) (libvirt.DomainState, error) {
	state, _, err := c.l.DomainGetState(dom, 0)
	if err != nil {
		return 0, err
	}
	return libvirt.DomainState(state), nil
}

// startDomain starts a defined domain.
func (c *libvirtConn) startDomain(dom libvirt.Domain) error {
	return c.l.DomainCreate(dom)
}

// shutdownDomain requests a graceful shutdown.
func (c *libvirtConn) shutdownDomain(dom libvirt.Domain) error {
	return c.l.DomainShutdown(dom)
}

// destroyDomain forces immediate stop.
func (c *libvirtConn) destroyDomain(dom libvirt.Domain) error {
	return c.l.DomainDestroy(dom)
}

// undefineDomain removes the domain definition.
// Note: removeStorage is handled by the caller (file deletion), not via libvirt flags,
// since libvirt's storage wipe only works with managed storage pools.
func (c *libvirtConn) undefineDomain(dom libvirt.Domain, removeStorage bool) error {
	return c.l.DomainUndefineFlags(dom, libvirt.DomainUndefineNvram)
}

// defineAndStartDomain defines a domain from XML and starts it.
func (c *libvirtConn) defineAndStartDomain(xmlStr string) error {
	dom, err := c.l.DomainDefineXML(xmlStr)
	if err != nil {
		return fmt.Errorf("defining domain: %w", err)
	}
	if err := c.l.DomainCreate(dom); err != nil {
		return fmt.Errorf("starting domain: %w", err)
	}
	return nil
}

// getDomainXML returns the XML description of a domain.
func (c *libvirtConn) getDomainXML(dom libvirt.Domain) (string, error) {
	return c.l.DomainGetXMLDesc(dom, 0)
}

// redefineDomain redefines a domain from XML string.
func (c *libvirtConn) redefineDomain(xmlStr string) error {
	_, err := c.l.DomainDefineXML(xmlStr)
	return err
}

// listOvDomains returns all domains with the "ov-" prefix.
func (c *libvirtConn) listOvDomains() ([]domainInfo, error) {
	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := c.l.ConnectListAllDomains(1, flags)
	if err != nil {
		return nil, err
	}

	var results []domainInfo
	for _, dom := range domains {
		name := dom.Name
		if !strings.HasPrefix(name, "ov-") {
			continue
		}
		state, stateErr := c.domainState(dom)
		stateStr := "unknown"
		if stateErr == nil {
			stateStr = domainStateString(state)
		}
		results = append(results, domainInfo{Name: name, State: stateStr})
	}
	return results, nil
}

type domainInfo struct {
	Name  string
	State string
}

func domainStateString(state libvirt.DomainState) string {
	switch state {
	case libvirt.DomainRunning:
		return "running"
	case libvirt.DomainShutoff:
		return "shut off"
	case libvirt.DomainPaused:
		return "paused"
	case libvirt.DomainShutdown:
		return "shutting down"
	case libvirt.DomainCrashed:
		return "crashed"
	case libvirt.DomainPmsuspended:
		return "suspended"
	default:
		return "unknown"
	}
}

// libvirtSessionSocket returns the path to the user's libvirt session socket.
func libvirtSessionSocket() string {
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		return filepath.Join(runtimeDir, "libvirt", "libvirt-sock")
	}
	return fmt.Sprintf("/run/user/%d/libvirt/libvirt-sock", os.Getuid())
}

// buildDomainXML constructs a minimal libvirt domain XML for a VM.
func buildDomainXML(name, qcow2 string, ramMB, cpus int, ports []string, gpu bool, smbiosCredentials ...string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='MiB'>%d</memory>
  <vcpu>%d</vcpu>
  <os>
    <type arch='x86_64' machine='q35'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'/>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='%s'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <interface type='user'>
      <model type='virtio'/>
`, name, ramMB, cpus, qcow2))

	// Port forwards
	b.WriteString("      <portForward proto='tcp'>\n")
	b.WriteString("        <range start='22' to='2222'/>\n")
	for _, p := range ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) == 2 {
			b.WriteString(fmt.Sprintf("        <range start='%s' to='%s'/>\n", parts[1], parts[0]))
		}
	}
	b.WriteString("      </portForward>\n")
	b.WriteString("    </interface>\n")

	// Serial console
	b.WriteString(`    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
`)

	if gpu {
		b.WriteString("    <!-- GPU passthrough requires manual --host-device configuration -->\n")
	}

	b.WriteString("  </devices>\n")

	// SMBIOS credentials for systemd (SSH keys, etc.)
	if len(smbiosCredentials) > 0 {
		b.WriteString("  <sysinfo type='smbios'>\n")
		b.WriteString("    <oemStrings>\n")
		for _, cred := range smbiosCredentials {
			b.WriteString(fmt.Sprintf("      <entry>%s</entry>\n", cred))
		}
		b.WriteString("    </oemStrings>\n")
		b.WriteString("  </sysinfo>\n")
	}

	b.WriteString("</domain>\n")
	return b.String()
}
