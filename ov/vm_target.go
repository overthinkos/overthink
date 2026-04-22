package main

// Shared VM target resolution for `ov test spice` and `ov test libvirt`.
//
// ResolveVmTarget opens a session-scoped libvirt connection, finds
// the running domain whose name matches the vms.yml entity, and
// parses its live XML via libvirtxml. Callers get:
//
//   - A libvirt connection they can use for further RPCs
//     (DomainScreenshot, DomainSendKey, QEMUDomainAgentCommand, etc.).
//   - The parsed libvirtxml.Domain — cheap field lookups for graphics
//     settings, device enumeration, etc.
//   - Convenience methods: SpiceAddress(), AgentReachable().
//
// Error taxonomy (surfaces the same wording to both commands):
//   - Unknown vm-name: "no vms.yml entity named <name>; known: …"
//   - Stopped domain: "domain <dom> is not running; start with
//     `ov vm start <name>`"
//   - No graphics stanza of matching type: "VM <name> has no <kind>
//     graphics device" (SpiceAddress specifically).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	libvirtxml "libvirt.org/go/libvirtxml"
)

// VmTarget holds an open libvirt connection to a running VM plus its
// parsed runtime XML. Callers are responsible for calling Close.
type VmTarget struct {
	Conn    *libvirtConn      // shared connection wrapper
	Domain  libvirt.Domain    // libvirt handle
	XML     *libvirtxml.Domain // parsed live XML
	Spec    *VmSpec           // vms.yml entity
	VmName  string            // vms.yml key
	DomName string            // libvirt domain name (typically "ov-<vmName>")
}

// ResolveVmTarget opens a libvirt session connection and resolves the
// running domain for a vms.yml entity. The caller must call Close()
// on the returned target.
//
// The domain-name convention matches `ov vm start`: "ov-<vmName>".
// For entity names already prefixed with "ov-" (rare), the prefix is
// not doubled.
func ResolveVmTarget(vmName string) (*VmTarget, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	// Load the vms.yml entity.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !ok || uf.VMs == nil {
		return nil, fmt.Errorf("no kind:vm entities declared in overthink.yml")
	}
	spec, present := uf.VMs[vmName]
	if !present {
		known := make([]string, 0, len(uf.VMs))
		for k := range uf.VMs {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("no vms.yml entity named %q; known: %s", vmName, strings.Join(known, ", "))
	}

	// Open libvirt.
	conn, err := connectLibvirt()
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}

	// Find the domain.
	domName := vmDomainNameFor(vmName)
	dom, err := conn.lookupDomain(domName)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("domain %q not found; start with `ov vm start %s`: %w", domName, vmName, err)
	}

	// Parse live XML.
	xmlStr, err := conn.getDomainXML(dom)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("getting XML for %s: %w", domName, err)
	}
	parsed := &libvirtxml.Domain{}
	if err := parsed.Unmarshal(xmlStr); err != nil {
		conn.Close()
		return nil, fmt.Errorf("parsing XML for %s: %w", domName, err)
	}

	return &VmTarget{
		Conn:    conn,
		Domain:  dom,
		XML:     parsed,
		Spec:    spec,
		VmName:  vmName,
		DomName: domName,
	}, nil
}

// Close releases the libvirt connection.
func (t *VmTarget) Close() error {
	if t == nil || t.Conn == nil {
		return nil
	}
	return t.Conn.Close()
}

// Running checks that the target domain is in the "running" state.
func (t *VmTarget) Running() (bool, error) {
	state, err := t.Conn.domainState(t.Domain)
	if err != nil {
		return false, err
	}
	return state == libvirt.DomainRunning, nil
}

// EnsureRunning returns an error if the domain is not running.
func (t *VmTarget) EnsureRunning() error {
	ok, err := t.Running()
	if err != nil {
		return fmt.Errorf("checking domain state: %w", err)
	}
	if !ok {
		return fmt.Errorf("domain %s is not running; start with `ov vm start %s`", t.DomName, t.VmName)
	}
	return nil
}

// SpiceAddress extracts the SPICE listen host, port, and password
// from the runtime domain XML. autoport=yes is transparent — running
// XML always has the assigned port inline.
//
// Errors:
//   - no <graphics type='spice'/> in domain: "no SPICE graphics device
//     declared"
//   - graphics present but autoport unresolved (port==0): "SPICE port
//     not yet assigned; domain may still be starting up"
func (t *VmTarget) SpiceAddress() (host string, port int, passwd string, err error) {
	if t.XML == nil || t.XML.Devices == nil {
		return "", 0, "", fmt.Errorf("no devices in domain XML for %s", t.DomName)
	}
	for _, g := range t.XML.Devices.Graphics {
		if g.Spice == nil {
			continue
		}
		s := g.Spice
		port = s.Port
		passwd = s.Passwd
		host = "127.0.0.1"
		if len(s.Listeners) > 0 && s.Listeners[0].Address != nil {
			if a := s.Listeners[0].Address.Address; a != "" {
				host = a
			}
		}
		if port == 0 {
			return "", 0, "", fmt.Errorf("SPICE port not yet assigned for %s; domain may still be starting up", t.DomName)
		}
		return host, port, passwd, nil
	}
	return "", 0, "", fmt.Errorf("VM %s has no SPICE graphics device declared in vms.yml", t.VmName)
}

// VncAddress extracts the VNC listen host, port, and password from
// the runtime domain XML — mirror of SpiceAddress for VMs that use
// VNC graphics instead.
func (t *VmTarget) VncAddress() (host string, port int, passwd string, err error) {
	if t.XML == nil || t.XML.Devices == nil {
		return "", 0, "", fmt.Errorf("no devices in domain XML for %s", t.DomName)
	}
	for _, g := range t.XML.Devices.Graphics {
		if g.VNC == nil {
			continue
		}
		v := g.VNC
		port = v.Port
		passwd = v.Passwd
		host = "127.0.0.1"
		if len(v.Listeners) > 0 && v.Listeners[0].Address != nil {
			if a := v.Listeners[0].Address.Address; a != "" {
				host = a
			}
		}
		if v.Listen != "" {
			host = v.Listen
		}
		if port == 0 {
			return "", 0, "", fmt.Errorf("VNC port not yet assigned for %s", t.DomName)
		}
		return host, port, passwd, nil
	}
	return "", 0, "", fmt.Errorf("VM %s has no VNC graphics device declared in vms.yml", t.VmName)
}

// AgentReachable probes qemu-guest-agent with a guest-ping command.
// Returns true if the agent responds within the timeout. Useful as a
// cheap pre-flight check before `guest exec`/`guest info`/etc.
func (t *VmTarget) AgentReachable(timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = ctx
	req := map[string]any{"execute": "guest-ping"}
	buf, err := json.Marshal(req)
	if err != nil {
		return false
	}
	// go-libvirt exposes QEMUDomainAgentCommand which talks to QGA.
	// Timeout is in seconds (int32).
	_, err = t.Conn.l.QEMUDomainAgentCommand(t.Domain, string(buf), int32(timeout.Seconds()), 0)
	return err == nil
}

// vmDomainNameFor returns the libvirt domain name convention for a
// vms.yml entity. Matches `ov vm start`'s naming.
func vmDomainNameFor(vmName string) string {
	if strings.HasPrefix(vmName, "ov-") {
		return vmName
	}
	return "ov-" + vmName
}
