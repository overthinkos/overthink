package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	libvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
	"golang.org/x/crypto/ssh"
)

// domainStateRunning is the libvirt domain state for a running VM.
const domainStateRunning = libvirt.DomainRunning

// libvirtConn wraps a go-libvirt connection to the session daemon.
// When the URI is qemu+ssh://, Tunnel holds the SSH client that
// forwards the remote virtqemud socket; Close tears everything down.
type libvirtConn struct {
	l      *libvirt.Libvirt
	tunnel *SSHTunnel // non-nil when connected via qemu+ssh://
	uri    LibvirtURI
}

// connectLibvirt connects to a libvirt session daemon — local by
// default, or remote when the URI is qemu+ssh://host/session.
//
// Empty uri is equivalent to "qemu:///session" (local). Local mode
// dials the virtqemud UNIX socket under $XDG_RUNTIME_DIR/libvirt/.
// Remote mode opens an SSH connection, discovers the remote user's
// virtqemud socket path over that SSH channel, forwards the socket
// into a local net.Conn, and speaks libvirt RPC through it.
//
// Uses ConnectToURI(qemu:///session) in all cases — the URI here is
// what the daemon connects to, not the transport. Modern libvirt
// ships per-driver modular daemons (virtqemud, virtnetworkd, …) and
// the session-scoped virtqemud only accepts /session URIs.
func connectLibvirt(uri string) (*libvirtConn, error) {
	parsed, err := ParseLibvirtURI(uri)
	if err != nil {
		return nil, err
	}
	if parsed.IsLocal() {
		return connectLocalLibvirtSession(parsed)
	}
	return connectRemoteLibvirtSession(parsed)
}

// connectLocalLibvirtSession dials the local virtqemud UNIX socket.
//
// Best-effort starts virtqemud.service (with libvirtd.service as a
// legacy fallback) before dialing — modular libvirt's `--timeout=120`
// causes the daemon to auto-exit after 120 s of idle, so consecutive
// `charly check libvirt …` invocations spaced wider than that find the
// socket gone. systemctl auto-restart on socket activation usually
// covers this, but on hosts without socket activation (no
// virtqemud.socket unit) the daemon stays down. Auto-starting here
// makes `charly check libvirt` self-healing on idle-timeout. See the
// 2026-05-06 R10 follow-up RCA.
func connectLocalLibvirtSession(parsed LibvirtURI) (*libvirtConn, error) {
	startLibvirtUserSession()
	sockPath := libvirtSessionSocket()
	c, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to libvirt session socket %s: %w", sockPath, err)
	}
	l := libvirt.NewWithDialer(dialers.NewAlreadyConnected(c))
	if err := l.ConnectToURI(libvirt.QEMUSession); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("libvirt handshake failed: %w", err)
	}
	return &libvirtConn{l: l, uri: parsed}, nil
}

// connectRemoteLibvirtSession opens an SSH connection and forwards
// the remote virtqemud session socket. Socket path is discovered by
// running `id -u` over the SSH channel (remote $XDG_RUNTIME_DIR may
// not match the connecting user's UID if id remapping is in play,
// so using `id -u` is the robust choice).
func connectRemoteLibvirtSession(parsed LibvirtURI) (*libvirtConn, error) {
	tunnel, err := NewSSHTunnel(parsed.Remote)
	if err != nil {
		return nil, fmt.Errorf("ssh to %s: %w", parsed.Remote, err)
	}
	sockPath, err := remoteVirtqemudSocketPath(tunnel.client)
	if err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("discovering remote virtqemud socket: %w", err)
	}
	conn, err := tunnel.client.Dial("unix", sockPath)
	if err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("dialing remote socket %s via ssh: %w", sockPath, err)
	}
	l := libvirt.NewWithDialer(dialers.NewAlreadyConnected(conn))
	if err := l.ConnectToURI(libvirt.QEMUSession); err != nil {
		_ = conn.Close()
		_ = tunnel.Close()
		return nil, fmt.Errorf("libvirt handshake over ssh failed: %w", err)
	}
	return &libvirtConn{l: l, tunnel: tunnel, uri: parsed}, nil
}

// Close disconnects from libvirt, and from SSH if the connection was
// remote.
func (c *libvirtConn) Close() error {
	err := c.l.Disconnect()
	if c.tunnel != nil {
		if terr := c.tunnel.Close(); terr != nil && err == nil {
			err = terr
		}
	}
	return err
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

// startDomain starts a defined domain. Before calling libvirt's
// DomainCreate, pre-creates any missing parent directories for
// <listen type='socket'/> graphics sockets — libvirt 12.x on Arch
// does not create `~/.config/libvirt/qemu/lib/domain-<id>-<name>/`
// in time for the QEMU bind(2) call, and QEMU fails with
// "bind: No such file or directory". Pre-creating is idempotent.
func (c *libvirtConn) startDomain(dom libvirt.Domain) error {
	if err := ensureDomainSocketDirs(c.l, dom); err != nil {
		return fmt.Errorf("preparing socket dirs: %w", err)
	}
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

// gracefulStopDomain requests an ACPI/agent shutdown and waits (up to the
// config StopGrace) for the domain to power off, forcing a destroy only if
// it will not stop in time. A graceful stop lets the guest flush its
// filesystems — notably the in-guest podman OVERLAY STORE: a forced
// DomainDestroy of a busy guest can leave a layer's diff dir half-written, so a
// qcow2 disk REUSED across an `charly update` recreate would then carry a torn
// image that fails `podman run` with `…/storage/overlay/<hash>: no such file`.
// No-op when the domain is already stopped or absent.
func (c *libvirtConn) gracefulStopDomain(dom libvirt.Domain) {
	if state, err := c.domainState(dom); err != nil || state != domainStateRunning {
		return
	}
	if err := c.shutdownDomain(dom); err != nil {
		// ACPI/agent request rejected (no acpid, no guest agent) — force now.
		_ = c.destroyDomain(dom)
		return
	}
	// StopGate (poll.go): wait up to the config StopGrace for the domain to
	// power off, polling its state — replaces the fixed 60s magic deadline the
	// census flagged as marginal under 8+ concurrent destroys (a forced destroy
	// of a still-flushing guest can tear the in-guest podman overlay store).
	cfg := loadedReadiness().StopGate("graceful-stop domain")
	if err := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		state, serr := c.domainState(dom)
		return serr != nil || state != domainStateRunning, 0, nil
	}); err != nil {
		_ = c.destroyDomain(dom) // did not power off within StopGrace — force
	}
}

// undefineDomain removes the domain definition.
// Note: removeStorage is handled by the caller (file deletion), not via libvirt flags,
// since libvirt's storage wipe only works with managed storage pools.
func (c *libvirtConn) undefineDomain(dom libvirt.Domain, _ bool) error {
	return c.l.DomainUndefineFlags(dom, libvirt.DomainUndefineNvram)
}

// defineAndStartDomain defines a domain from XML and starts it.
// Between define and start, pre-creates any missing parent dirs for
// <listen type='socket'/> sockets (libvirt 12.x Arch bug — see
// startDomain comment).
func (c *libvirtConn) defineAndStartDomain(xmlStr string) error {
	dom, err := c.l.DomainDefineXML(xmlStr)
	if err != nil {
		return fmt.Errorf("defining domain: %w", err)
	}
	if err := ensureDomainSocketDirs(c.l, dom); err != nil {
		return fmt.Errorf("preparing socket dirs: %w", err)
	}
	if err := c.l.DomainCreate(dom); err != nil {
		return fmt.Errorf("starting domain: %w", err)
	}
	return nil
}

// ensureDomainSocketDirs reads the (possibly libvirt-populated)
// domain XML, finds every <graphics> listener with type='socket'
// and a `socket=` path, and creates the parent directory of each
// with 0700 if it doesn't exist. Idempotent.
//
// Rationale: libvirt 12.2 on Arch (and likely other rolling distros)
// does not reliably pre-create
// `~/.config/libvirt/qemu/lib/domain-<id>-<name>/` before handing
// off to QEMU, which then fails bind(2) on the SPICE socket. We
// shoulder that responsibility here.
func ensureDomainSocketDirs(l *libvirt.Libvirt, dom libvirt.Domain) error {
	xmlStr, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return fmt.Errorf("reading domain XML: %w", err)
	}
	paths := extractGraphicsSocketPaths(xmlStr)
	paths = append(paths, extractChannelSocketPaths(xmlStr)...)
	for _, p := range paths {
		dir := filepath.Dir(p)
		if dir == "" || dir == "." || dir == "/" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// extractChannelSocketPaths finds `<channel type='unix'><source path='…'/></channel>`
// paths in a libvirt domain XML. Same string-search approach as
// extractGraphicsSocketPaths — looks for any `<source>` whose
// containing element is a unix-type channel.
//
// Rationale: the qemu-guest-agent channel binds a unix socket; if
// the parent directory doesn't exist (common when authors compose
// the path with templating like {{.VmStateDir}}/qga.sock and the
// VM state dir was just created), QEMU's bind(2) fails. Mirroring
// the existing graphics-socket pre-create logic.
func extractChannelSocketPaths(xmlStr string) []string {
	var out []string
	remaining := xmlStr
	for {
		i := strings.Index(remaining, "<channel")
		if i < 0 {
			return out
		}
		// Slice the channel element body to its closing tag.
		end := strings.Index(remaining[i:], "</channel>")
		if end < 0 {
			return out
		}
		body := remaining[i : i+end]
		remaining = remaining[i+end:]
		if !strings.Contains(body, `type='unix'`) && !strings.Contains(body, `type="unix"`) {
			continue
		}
		// Look for <source path='…'/> (or path="…").
		for _, q := range []string{"path='", `path="`} {
			_, after, ok := strings.Cut(body, q)
			if !ok {
				continue
			}
			rest := after
			ei := strings.IndexAny(rest, `'"`)
			if ei < 0 {
				continue
			}
			out = append(out, rest[:ei])
			break
		}
	}
}

// extractGraphicsSocketPaths finds `<listen type='socket' socket='…'/>`
// paths in a libvirt domain XML. String-search rather than a full XML
// parse — keeps the dependency surface small and doesn't crash on
// any edge shapes libvirt might emit.
func extractGraphicsSocketPaths(xmlStr string) []string {
	var out []string
	remaining := xmlStr
	for {
		i := strings.Index(remaining, "<listen")
		if i < 0 {
			return out
		}
		end := strings.Index(remaining[i:], "/>")
		if end < 0 {
			end = strings.Index(remaining[i:], ">")
			if end < 0 {
				return out
			}
		}
		tag := remaining[i : i+end]
		remaining = remaining[i+end:]
		if !strings.Contains(tag, `type='socket'`) && !strings.Contains(tag, `type="socket"`) {
			continue
		}
		// Look for socket='…' or socket="…"
		for _, q := range []string{"socket='", `socket="`} {
			_, after, ok := strings.Cut(tag, q)
			if !ok {
				continue
			}
			rest := after
			ei := strings.IndexAny(rest, `'"`)
			if ei < 0 {
				continue
			}
			out = append(out, rest[:ei])
			break
		}
	}
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

// setDomainAutostart toggles libvirt's per-domain autostart flag. The
// flag is a libvirt domain property (not part of the domain XML), so it
// survives DomainDefineXML re-definitions; we re-assert it on create
// anyway. For qemu:///session the flag only triggers at host boot when
// the user session lingers — see ensureBootAutostartPrereqs.
func (c *libvirtConn) setDomainAutostart(name string, on bool) error {
	dom, err := c.lookupDomain(name)
	if err != nil {
		return fmt.Errorf("looking up domain %s: %w", name, err)
	}
	flag := int32(0)
	if on {
		flag = 1
	}
	if err := c.l.DomainSetAutostart(dom, flag); err != nil {
		return fmt.Errorf("setting autostart on %s: %w", name, err)
	}
	return nil
}

// listCharlyDomains returns all domains with the "charly-" prefix.
func (c *libvirtConn) listCharlyDomains() ([]domainInfo, error) {
	flags := libvirt.ConnectListDomainsActive | libvirt.ConnectListDomainsInactive
	domains, _, err := c.l.ConnectListAllDomains(1, flags)
	if err != nil {
		return nil, err
	}

	var results []domainInfo
	for _, dom := range domains {
		name := dom.Name
		if !strings.HasPrefix(name, "charly-") {
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

// remoteVirtqemudSocketPath discovers the remote user's session
// virtqemud socket path via the SSH connection. Probes (in order):
//  1. $XDG_RUNTIME_DIR/libvirt/virtqemud-sock (modular libvirt ≥ 8)
//  2. /run/user/$(id -u)/libvirt/virtqemud-sock
//  3. $XDG_RUNTIME_DIR/libvirt/libvirt-sock (legacy monolithic)
//
// Returns the first path that exists on the remote host.
func remoteVirtqemudSocketPath(client *ssh.Client) (string, error) {
	// Single command that prints the first existing candidate. Cheaper
	// than three separate round-trips.
	script := `
set -e
for p in "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/libvirt/virtqemud-sock" \
         "/run/user/$(id -u)/libvirt/virtqemud-sock" \
         "${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/libvirt/libvirt-sock"; do
  if [ -S "$p" ]; then
    printf "%s" "$p"
    exit 0
  fi
done
echo "no libvirt session socket found" >&2
exit 1
`
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close() //nolint:errcheck
	out, err := session.Output(script)
	if err != nil {
		return "", fmt.Errorf("probing remote socket path: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("remote returned empty socket path")
	}
	return path, nil
}

// libvirtSessionSocket returns the path to the user's libvirt session
// socket. Modern libvirt (≥ 8.0) uses per-driver modular daemons with
// separate sockets (virtqemud-sock); legacy libvirt (< 8.0) uses the
// monolithic libvirt-sock. Probe the modular socket first because
// that's what every current distro ships; fall back to the legacy
// path on older systems.
func libvirtSessionSocket() string {
	picked, _ := libvirtSessionSocketWithProbes()
	return picked
}

// libvirtSessionSocketWithProbes returns both the picked socket path
// AND the full list of paths attempted, so callers (resolveVmBackend
// in particular) can format helpful error messages that show every
// path probed instead of just the fallback. The picked path is empty
// when none of the probed paths exists; in that case the fallback
// path is still returned (it's the path the caller would attempt to
// dial), but the second return surfaces the full probe trail.
func libvirtSessionSocketWithProbes() (picked string, probed []string) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	libvirtDir := filepath.Join(dir, "libvirt")

	// Probe order: modular (virtqemud) first — standard on libvirt
	// ≥ 8.0 — then legacy monolithic socket as fallback.
	modular := filepath.Join(libvirtDir, "virtqemud-sock")
	legacy := filepath.Join(libvirtDir, "libvirt-sock")
	probed = []string{modular, legacy}

	if _, err := os.Stat(modular); err == nil {
		return modular, probed
	}
	if _, err := os.Stat(legacy); err == nil {
		return legacy, probed
	}
	// Neither exists. Return the legacy path as the caller's last
	// resort dial target; the probe trail in `probed` shows what
	// was checked.
	return legacy, probed
}
