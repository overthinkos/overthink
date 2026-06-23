package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// spice_preresolve.go is the HOST-side half of the externalized `spice` verb. The
// out-of-process candy/plugin-spice provider speaks the SPICE wire but owns NO
// go-libvirt — so the host does the vm.yml → libvirt-domain → live-XML →
// <graphics type='spice'> resolution (ResolveVmTarget + SpiceEndpoint, vm_target.go,
// which stays in core for the VM machinery) and opens any qemu+ssh:// side tunnel
// itself, handing the plugin a plain DIALABLE endpoint via the CheckEnv. This is the
// spice analogue of preresolveKubeCluster (k8s_config.go): the plugin cannot reach
// core's project loader / libvirt, so the host pre-resolves before marshaling.

// SpiceEnv is the host-resolved, DIALABLE SPICE endpoint shipped to the
// out-of-process candy/plugin-spice provider via CheckEnv.Spice. Exactly one of
// Socket / Address is set — the host prefers the UNIX socket (the charly-managed-VM
// default after the socket-listen cutover); for a remote qemu+ssh:// VM it opens the
// side tunnel and fills the FORWARDED local address. The plugin just dials this.
type SpiceEnv struct {
	Address  string `json:"address"`  // "host:port" for a TCP listener (or forwarded-local TCP)
	Socket   string `json:"socket"`   // UNIX socket path (or forwarded-local socket)
	Password string `json:"password"` // SPICE ticket; empty = AUTH_NONE
}

// preresolveSpiceEndpoint resolves a `spice:` op's target VM (r.Box) to a dialable
// SPICE endpoint host-side. Returns:
//   - env:     the resolved endpoint (nil for a non-spice op or no VM context — the
//     plugin's own no-endpoint skip then fires);
//   - cleanup: closes any opened SSH tunnel (ALWAYS non-nil — defer it unconditionally);
//   - early:   a pre-dispatch CheckResult to return immediately — a SKIP when the VM
//     declares no SPICE device (the host-side analogue of the former
//     vmDisplayDeviceAbsent subprocess-stderr skip) or a FAIL when resolution
//     errored; nil to proceed to dispatch.
//
// The cleanup must outlive the plugin's Invoke (the tunnel carries the live SPICE
// connection), so invokeVerbProvider defers it across the Invoke call.
func (r *Runner) preresolveSpiceEndpoint(c *Op) (env *SpiceEnv, cleanup func(), early *CheckResult) {
	noop := func() {}
	// Non-spice op, or no VM context (r.Box empty) → nothing to resolve; the plugin's
	// own box-mode / no-endpoint skip handles the degenerate cases.
	if c.Spice == "" || r.Box == "" {
		return nil, noop, nil
	}
	// The declarative verb honours CHARLY_LIBVIRT_URI for a remote hypervisor, exactly
	// as the former `charly check spice` subprocess did (its --uri flag carried that env).
	uri := os.Getenv("CHARLY_LIBVIRT_URI")
	t, err := ResolveVmTarget(r.Box, uri)
	if err != nil {
		res := failf(c, "spice: %s: %v", c.Spice, err)
		return nil, noop, &res
	}
	ep, epErr := t.SpiceEndpoint()
	tunnelTarget := t.Uri
	_ = t.Close() // read the endpoint, then release the resolver's libvirt connection
	if epErr != nil {
		// "VM <name> has no SPICE graphics device declared in vm.yml" → N/A SKIP (the
		// SPICE-less cachyos-gpu operator vs the SPICE-having check bed); any other
		// resolver error is a real FAIL.
		if strings.Contains(epErr.Error(), noVmDisplayDeviceErr) {
			res := skipf(c, fmt.Sprintf("spice %s — N/A: deployment has no SPICE graphics device (SPICE-less GPU desktop)", c.Spice))
			return nil, noop, &res
		}
		res := failf(c, "spice: %s: %v", c.Spice, epErr)
		return nil, noop, &res
	}

	// Local endpoint — hand the plugin the direct address, no tunnel.
	if !ep.TunnelNeeded {
		se := &SpiceEnv{Password: ep.Password}
		if ep.IsSocket {
			se.Socket = ep.SocketPath
		} else {
			se.Address = fmt.Sprintf("%s:%d", ep.Host, ep.Port)
		}
		return se, noop, nil
	}

	// Remote (qemu+ssh://) — open an SSH tunnel forwarding the remote endpoint to a
	// local address and hand the plugin the forwarded address; the tunnel is torn down
	// (cleanup) after Invoke returns. Preserved from the former dialSpiceEndpoint; the
	// R10 bed is LOCAL, so this path is preserve-but-not-bed-exercised.
	parsed, perr := ParseLibvirtURI(tunnelTarget)
	if perr != nil {
		res := failf(c, "spice: %s: %v", c.Spice, perr)
		return nil, noop, &res
	}
	tunnel, terr := NewSSHTunnel(parsed.Remote)
	if terr != nil {
		res := failf(c, "spice: %s: ssh tunnel to %s: %v", c.Spice, parsed.Remote, terr)
		return nil, noop, &res
	}
	tunnelCleanup := func() { _ = tunnel.Close() }
	if ep.IsSocket {
		localSock, _, ferr := tunnel.ForwardUnix(context.Background(), ep.SocketPath)
		if ferr != nil {
			tunnelCleanup()
			res := failf(c, "spice: %s: forwarding remote socket %s: %v", c.Spice, ep.SocketPath, ferr)
			return nil, noop, &res
		}
		return &SpiceEnv{Socket: localSock, Password: ep.Password}, tunnelCleanup, nil
	}
	localAddr, _, ferr := tunnel.ForwardTCP(context.Background(), ep.Host, ep.Port)
	if ferr != nil {
		tunnelCleanup()
		res := failf(c, "spice: %s: forwarding remote TCP %s:%d: %v", c.Spice, ep.Host, ep.Port, ferr)
		return nil, noop, &res
	}
	return &SpiceEnv{Address: localAddr, Password: ep.Password}, tunnelCleanup, nil
}
