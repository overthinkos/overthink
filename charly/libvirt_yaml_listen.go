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

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

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
	Type    string `yaml:"type,omitempty"`
	Address string `yaml:"address,omitempty"`
	Network string `yaml:"network,omitempty"`
	Socket  string `yaml:"socket,omitempty"`
}

// LibvirtGraphicsListeners is the YAML-shaped list of listeners for
// one <graphics> element. Custom UnmarshalYAML accepts scalar / map /
// list forms (see file doc).
type LibvirtGraphicsListeners []LibvirtGraphicsListen

// UnmarshalYAML accepts:
//   - scalar string → one address listener
//   - mapping       → one listener (type inferred from Address if
//     unset: "address" when Address non-empty, else
//     required explicit type)
//   - sequence      → list of listeners (each a mapping)
func (ll *LibvirtGraphicsListeners) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			*ll = nil
			return nil
		}
		*ll = LibvirtGraphicsListeners{{Type: "address", Address: value.Value}}
		return nil
	case yaml.MappingNode:
		var one LibvirtGraphicsListen
		if err := value.Decode(&one); err != nil {
			return fmt.Errorf("graphics listen (mapping): %w", err)
		}
		if one.Type == "" {
			switch {
			case one.Address != "":
				one.Type = "address"
			case one.Network != "":
				one.Type = "network"
			default:
				return fmt.Errorf("graphics listen: mapping must set `type:` (socket|address|network) or `address:`")
			}
		}
		*ll = LibvirtGraphicsListeners{one}
		return nil
	case yaml.SequenceNode:
		var list []LibvirtGraphicsListen
		if err := value.Decode(&list); err != nil {
			return fmt.Errorf("graphics listen (sequence): %w", err)
		}
		for i := range list {
			if list[i].Type == "" {
				switch {
				case list[i].Address != "":
					list[i].Type = "address"
				case list[i].Network != "":
					list[i].Type = "network"
				default:
					return fmt.Errorf("graphics listen[%d]: missing `type:` and no inferrable default", i)
				}
			}
		}
		*ll = LibvirtGraphicsListeners(list)
		return nil
	}
	return fmt.Errorf("graphics listen: unexpected YAML kind %d (want scalar, mapping, or sequence)", value.Kind)
}

// MarshalYAML preserves the shorthand: a single address listener
// marshals as a scalar; everything else marshals as a sequence.
// Keeps re-marshaled vm.yml readable.
func (ll LibvirtGraphicsListeners) MarshalYAML() (any, error) { //nolint:unparam // error return kept for interface/API stability
	if len(ll) == 1 {
		l := ll[0]
		if l.Type == "address" && l.Socket == "" && l.Network == "" {
			return l.Address, nil
		}
	}
	return []LibvirtGraphicsListen(ll), nil
}

// First returns the first listener, or the zero value if empty.
func (ll LibvirtGraphicsListeners) First() LibvirtGraphicsListen {
	if len(ll) == 0 {
		return LibvirtGraphicsListen{}
	}
	return ll[0]
}
