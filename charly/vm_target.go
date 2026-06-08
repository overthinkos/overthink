package main

// Shared VM target resolution for `charly eval spice` and `charly eval libvirt`.
//
// ResolveVmTarget opens a session-scoped libvirt connection, finds
// the running domain whose name matches the vm.yml entity, and
// parses its live XML via libvirtxml. Callers get:
//
//   - A libvirt connection they can use for further RPCs
//     (DomainScreenshot, DomainSendKey, QEMUDomainAgentCommand, etc.).
//   - The parsed libvirtxml.Domain — cheap field lookups for graphics
//     settings, device enumeration, etc.
//   - Convenience methods: SpiceAddress(), AgentReachable().
//
// Error taxonomy (surfaces the same wording to both commands):
//   - Unknown vm-name: "no vm.yml entity named <name>; known: …"
//   - Stopped domain: "domain <dom> is not running; start with
//     `charly vm start <name>`"
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
	Conn    *libvirtConn       // shared connection wrapper
	Domain  libvirt.Domain     // libvirt handle
	XML     *libvirtxml.Domain // parsed live XML
	Spec    *VmSpec            // vm.yml entity
	VmName  string             // vm.yml key
	DomName string             // libvirt domain name (typically "charly-<vmName>")
	Uri     string             // libvirt URI used to resolve this target (empty = local)
}

// ResolveVmTarget opens a libvirt connection (local by default or
// remote when uri is qemu+ssh://…) and resolves the running domain
// for a vm.yml entity. Caller must Close() the returned target.
//
// The domain-name convention matches `charly vm start`: "charly-<vmName>".
// For entity names already prefixed with "charly-" (rare), the prefix is
// not doubled. Pass uri == "" for the default local qemu:///session.
func ResolveVmTarget(vmName, uri string) (*VmTarget, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	// Load the vm.yml entity.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ok || uf.VM == nil {
		return nil, fmt.Errorf("no kind:vm entities declared in charly.yml")
	}
	// Schema v4: callers may pass either a kind:vm entity name directly
	// (e.g. "arch") or a kind:deployment name with target:vm (e.g.
	// "arch-vm") whose Vm field points at the actual entity.
	spec, present := uf.VM[vmName]
	if !present && uf.Deploy != nil {
		if entry, ok := uf.Deploy[vmName]; ok && entry.Target == "vm" && entry.Vm != "" {
			if s, okSpec := uf.VM[entry.Vm]; okSpec {
				spec = s
				vmName = entry.Vm
				present = true
			}
		}
	}
	if !present {
		known := make([]string, 0, len(uf.VM))
		for k := range uf.VM {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("no vm.yml entity named %q; known: %s", vmName, strings.Join(known, ", "))
	}

	// Open libvirt (local or remote per uri).
	conn, err := connectLibvirt(uri)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}

	// Find the domain.
	domName := vmDomainNameFor(vmName)
	dom, err := conn.lookupDomain(domName)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("domain %q not found; start with `charly vm start %s`: %w", domName, vmName, err)
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
		Uri:     uri,
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
		return fmt.Errorf("domain %s is not running; start with `charly vm start %s`", t.DomName, t.VmName)
	}
	return nil
}

// DisplayEndpoint describes how to reach one graphics channel
// (SPICE or VNC) of a running VM. Callers that want a raw net.Conn
// should pass this to Dial().
//
// IsSocket / SocketPath are set when the resolved <listen> element
// is `<listen type='socket'/>`. Host / Port are set for TCP-exposed
// listeners. The two are mutually exclusive — libvirt picks the
// first listener when there are several on one <graphics>, but ov
// always prefers the socket when present (matches virt-manager's
// auto-forwarding behavior).
//
// TunnelNeeded is true when the VmTarget was resolved over a remote
// libvirt URI (qemu+ssh://…); callers must open an SSH-forwarded
// local endpoint via ov/ssh_tunnel.go before dialing.
type DisplayEndpoint struct {
	Kind         string // "spice" | "vnc"
	IsSocket     bool
	SocketPath   string
	Host         string
	Port         int
	Password     string
	TunnelNeeded bool
}

// SpiceEndpoint walks the domain XML and returns the SPICE graphics
// endpoint (socket or TCP) with tunneling requirements annotated.
//
// Errors:
//   - no <graphics type='spice'/> in domain
//   - graphics present but no listener has resolved (port==0 for TCP,
//     or libvirt hasn't populated a socket= attribute yet)
func (t *VmTarget) SpiceEndpoint() (DisplayEndpoint, error) {
	if t.XML == nil || t.XML.Devices == nil {
		return DisplayEndpoint{}, fmt.Errorf("no devices in domain XML for %s", t.DomName)
	}
	for _, g := range t.XML.Devices.Graphics {
		if g.Spice == nil {
			continue
		}
		s := g.Spice
		ep := DisplayEndpoint{
			Kind:         "spice",
			Password:     s.Passwd,
			TunnelNeeded: t.Uri != "",
		}
		// Prefer socket listeners — that's what virt-manager and our
		// CLI want on remote hypervisors.
		for _, l := range s.Listeners {
			if l.Socket != nil && l.Socket.Socket != "" {
				ep.IsSocket = true
				ep.SocketPath = l.Socket.Socket
				return ep, nil
			}
		}
		// Fall back to TCP listener.
		ep.Port = s.Port
		ep.Host = "127.0.0.1"
		for _, l := range s.Listeners {
			if l.Address != nil && l.Address.Address != "" {
				ep.Host = l.Address.Address
				break
			}
		}
		if ep.Port == 0 {
			return DisplayEndpoint{}, fmt.Errorf("SPICE port not yet assigned for %s; domain may still be starting up (or socket listener has no resolved path yet)", t.DomName)
		}
		return ep, nil
	}
	return DisplayEndpoint{}, fmt.Errorf("VM %s has no SPICE graphics device declared in vm.yml", t.VmName)
}

// VncEndpoint is the VNC counterpart of SpiceEndpoint.
func (t *VmTarget) VncEndpoint() (DisplayEndpoint, error) {
	if t.XML == nil || t.XML.Devices == nil {
		return DisplayEndpoint{}, fmt.Errorf("no devices in domain XML for %s", t.DomName)
	}
	for _, g := range t.XML.Devices.Graphics {
		if g.VNC == nil {
			continue
		}
		v := g.VNC
		ep := DisplayEndpoint{
			Kind:         "vnc",
			Password:     v.Passwd,
			TunnelNeeded: t.Uri != "",
		}
		for _, l := range v.Listeners {
			if l.Socket != nil && l.Socket.Socket != "" {
				ep.IsSocket = true
				ep.SocketPath = l.Socket.Socket
				return ep, nil
			}
		}
		ep.Port = v.Port
		ep.Host = "127.0.0.1"
		for _, l := range v.Listeners {
			if l.Address != nil && l.Address.Address != "" {
				ep.Host = l.Address.Address
				break
			}
		}
		if ep.Host == "127.0.0.1" && v.Listen != "" {
			ep.Host = v.Listen
		}
		if ep.Port == 0 {
			return DisplayEndpoint{}, fmt.Errorf("VNC port not yet assigned for %s", t.DomName)
		}
		return ep, nil
	}
	return DisplayEndpoint{}, fmt.Errorf("VM %s has no VNC graphics device declared in vm.yml", t.VmName)
}

// SpiceAddress returns the TCP form of the SPICE endpoint — provided
// for existing callers that don't understand socket listeners. Use
// SpiceEndpoint() for new code. Returns an error if the endpoint is
// socket-only (no TCP fallback).
func (t *VmTarget) SpiceAddress() (host string, port int, passwd string, err error) {
	ep, err := t.SpiceEndpoint()
	if err != nil {
		return "", 0, "", err
	}
	if ep.IsSocket {
		return "", 0, "", fmt.Errorf("VM %s SPICE listens on UNIX socket %s; TCP address not available — use SpiceEndpoint()", t.VmName, ep.SocketPath)
	}
	return ep.Host, ep.Port, ep.Password, nil
}

// VncAddress is the TCP-only counterpart of SpiceAddress.
func (t *VmTarget) VncAddress() (host string, port int, passwd string, err error) {
	ep, err := t.VncEndpoint()
	if err != nil {
		return "", 0, "", err
	}
	if ep.IsSocket {
		return "", 0, "", fmt.Errorf("VM %s VNC listens on UNIX socket %s; TCP address not available — use VncEndpoint()", t.VmName, ep.SocketPath)
	}
	return ep.Host, ep.Port, ep.Password, nil
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
// vm.yml entity. Matches `charly vm start`'s naming.
func vmDomainNameFor(vmName string) string {
	if strings.HasPrefix(vmName, "charly-") {
		return vmName
	}
	return "charly-" + vmName
}
