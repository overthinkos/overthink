package main

// Structured <listen> support for LibvirtGraphics.
//
// libvirt's <graphics> element may carry one or more <listen> children.
// The YAML surface accepts three interchangeable shapes for the
// `listen:` field under each graphics entry:
//
//   # (a) scalar address — single <listen type='address' address='.'/>
//   listen: 127.0.0.1
//
//   # (b) single map — explicit type control (socket / address / network)
//   listen:
//     type: socket
//
//   # (c) list — multiple listeners on one <graphics>
//   listen:
//     - type: socket
//     - type: address
//       address: 127.0.0.1
//
// All three unmarshal into LibvirtGraphicsListeners (a list of
// LibvirtGraphicsListen). The bridge in libvirt_yaml_bridge.go
// iterates the list and emits one <listen> element per entry.

// LibvirtGraphicsListen is one <listen> child of a <graphics> element.
//
// Type controls which concrete listener libvirt creates:
//   - "address": <listen type='address' address='<Address>'/>
//     TCP listener bound to the given address.
//   - "socket":  <listen type='socket' [socket='<Socket>']/>
//     UNIX-socket listener. When Socket is empty libvirt auto-allocates
//     a path under $XDG_RUNTIME_DIR/libvirt/qemu/. Preferred for
//     remote GUI clients (virt-manager auto-forwards UNIX sockets over
//     the qemu+ssh:// RPC channel; no external tunnel required).
//   - "network": <listen type='network' network='<Network>'/>
//     Use a libvirt network's address (rare in session mode).
type LibvirtGraphicsListen struct {
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Address string `yaml:"address,omitempty" json:"address,omitempty"`
	Network string `yaml:"network,omitempty" json:"network,omitempty"`
	Socket  string `yaml:"socket,omitempty" json:"socket,omitempty"`
}

// LibvirtGraphicsListeners is the YAML-shaped list of listeners for
// one <graphics> element. Custom UnmarshalYAML accepts scalar / map /
// list forms (see file doc).
type LibvirtGraphicsListeners []LibvirtGraphicsListen

// First returns the first listener, or the zero value if empty.
func (ll LibvirtGraphicsListeners) First() LibvirtGraphicsListen {
	if len(ll) == 0 {
		return LibvirtGraphicsListen{}
	}
	return ll[0]
}
