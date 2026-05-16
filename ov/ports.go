package main

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// PortConflict describes a host port that is already in use.
type PortConflict struct {
	HostPort  int
	ContPort  int
	Owner     string // container name, or "unknown"
	OwnerType string // "ov-container", "container", "host-process"
}

// stripPortSuffix removes /tcp or /udp protocol suffix from a port string.
// "47998/udp" -> "47998", "udp"; "8000" -> "8000", ""
func stripPortSuffix(s string) (string, string) {
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

// ParsedPortMapping describes the four possible shapes podman accepts:
//
//	"P"               -> {Host: P, Container: P}
//	"H:C"             -> {Host: H, Container: C}
//	"IP:H:C"          -> {Host: H, Container: C, BindAddr: "IP"}
//	"[v6]:H:C"        -> {Host: H, Container: C, BindAddr: "[v6]"}
//
// Any of those forms may carry a /tcp or /udp suffix on the trailing port.
type ParsedPortMapping struct {
	BindAddr  string // explicit bind prefix if present (e.g. "127.0.0.1" or "[::1]"); empty otherwise
	Host      int
	Container int
	Protocol  string // "udp" / "tcp" / "" — extracted from /udp or /tcp suffix
}

// ParsePortMapping is the canonical port-mapping parser.
//
// Returns ok=false on unparseable input. Callers that want a loud failure
// (warning logged, port skipped) should branch on ok.
//
// All in-tree port handling routes through this — ParseHostPort,
// ParseContainerPort, parseHostPorts (tunnel.go), buildPortMapping (tunnel.go),
// and localizePort (shell.go) — so a single fix here covers every site that
// would otherwise mis-handle the IP:H:C form.
func ParsePortMapping(mapping string) (ParsedPortMapping, bool) {
	clean, proto := stripPortSuffix(mapping)
	parts := splitMappingParts(clean)
	var bindAddr, hostStr, contStr string
	switch len(parts) {
	case 1: // "P"
		hostStr = parts[0]
		contStr = parts[0]
	case 2: // "H:C"
		hostStr = parts[0]
		contStr = parts[1]
	case 3: // "IP:H:C"
		bindAddr = parts[0]
		hostStr = parts[1]
		contStr = parts[2]
	default:
		return ParsedPortMapping{}, false
	}
	host, err1 := strconv.Atoi(hostStr)
	cont, err2 := strconv.Atoi(contStr)
	if err1 != nil || err2 != nil {
		return ParsedPortMapping{}, false
	}
	if host <= 0 || host > 65535 || cont <= 0 || cont > 65535 {
		return ParsedPortMapping{}, false
	}
	return ParsedPortMapping{
		BindAddr:  bindAddr,
		Host:      host,
		Container: cont,
		Protocol:  proto,
	}, true
}

// splitMappingParts splits a port mapping while honoring an IPv6 bracket
// prefix as a single token (so "[::1]:8080:80" -> ["[::1]", "8080", "80"]).
func splitMappingParts(s string) []string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			head := s[:i+1]
			tail := strings.TrimPrefix(s[i+1:], ":")
			if tail == "" {
				return []string{head}
			}
			return append([]string{head}, strings.Split(tail, ":")...)
		}
	}
	return strings.Split(s, ":")
}

// FormatPortMapping is the inverse of ParsePortMapping. Empty bindAddr / proto
// are omitted; trailing-zero / equal ports collapse to canonical short forms
// that podman accepts.
func FormatPortMapping(p ParsedPortMapping) string {
	suffix := ""
	if p.Protocol != "" {
		suffix = "/" + p.Protocol
	}
	core := fmt.Sprintf("%d:%d", p.Host, p.Container)
	if p.BindAddr != "" {
		return p.BindAddr + ":" + core + suffix
	}
	return core + suffix
}

// ParseHostPort extracts the host port from a mapping. Accepts every form
// ParsePortMapping does, including the IP:H:C bind-address form.
func ParseHostPort(mapping string) (int, error) {
	p, ok := ParsePortMapping(mapping)
	if !ok {
		return 0, fmt.Errorf("invalid port mapping %q", mapping)
	}
	return p.Host, nil
}

// ParseContainerPort extracts the container port from a mapping. Accepts every
// form ParsePortMapping does, including the IP:H:C bind-address form.
func ParseContainerPort(mapping string) (int, error) {
	p, ok := ParsePortMapping(mapping)
	if !ok {
		return 0, fmt.Errorf("invalid port mapping %q", mapping)
	}
	return p.Container, nil
}

// CheckPortAvailability tests whether each host port can be bound.
// Returns a list of conflicts for ports that are already in use.
// Detects /udp suffix and uses UDP bind check accordingly.
func CheckPortAvailability(ports []string, bindAddr string, engine string) []PortConflict {
	var conflicts []PortConflict
	for _, mapping := range ports {
		hostPort, err := ParseHostPort(mapping)
		if err != nil {
			continue
		}
		contPort, _ := ParseContainerPort(mapping)

		addr := fmt.Sprintf("%s:%d", bindAddr, hostPort)

		if strings.HasSuffix(mapping, "/udp") {
			conn, err := net.ListenPacket("udp", addr)
			if err != nil {
				owner, ownerType := FindPortOwner(hostPort, engine)
				conflicts = append(conflicts, PortConflict{
					HostPort:  hostPort,
					ContPort:  contPort,
					Owner:     owner,
					OwnerType: ownerType,
				})
			} else {
				conn.Close()
			}
		} else {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				owner, ownerType := FindPortOwner(hostPort, engine)
				conflicts = append(conflicts, PortConflict{
					HostPort:  hostPort,
					ContPort:  contPort,
					Owner:     owner,
					OwnerType: ownerType,
				})
			} else {
				ln.Close()
			}
		}
	}
	return conflicts
}

// FindPortOwner checks running containers to identify what is using a port.
func FindPortOwner(port int, engine string) (owner string, ownerType string) {
	binary := EngineBinary(engine)
	portStr := strconv.Itoa(port)

	cmd := exec.Command(binary, "ps", "--format", "{{.Names}} {{.Ports}}")
	out, err := cmd.Output()
	if err != nil {
		return "unknown", "host-process"
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, ":"+portStr+"->") || strings.Contains(line, ":"+portStr+"/") {
			name := strings.Fields(line)[0]
			if strings.HasPrefix(name, "ov-") {
				return name, "ov-container"
			}
			return name, "container"
		}
	}
	return "unknown", "host-process"
}

// FormatPortConflicts produces a user-friendly error message with remediation suggestions.
func FormatPortConflicts(conflicts []PortConflict, image string) string {
	var b strings.Builder
	for _, c := range conflicts {
		fmt.Fprintf(&b, "\n  Port %d is in use", c.HostPort)
		if c.Owner != "unknown" {
			fmt.Fprintf(&b, " by container %q", c.Owner)
		}
		b.WriteString("\n")

		switch c.OwnerType {
		case "ov-container":
			// Extract image name from "ov-<image>" or "ov-<image>-<instance>"
			ovImage := strings.TrimPrefix(c.Owner, "ov-")
			fmt.Fprintf(&b, "    Fix: ov stop %s\n", ovImage)
		case "container":
			fmt.Fprintf(&b, "    Fix: podman stop %s\n", c.Owner)
		default:
			fmt.Fprintf(&b, "    Fix: find and stop the process using port %d\n", c.HostPort)
		}

		fmt.Fprintf(&b, "    Or remap: ov start %s --port %d:%d\n", image, c.HostPort+1, c.ContPort)
	}
	return b.String()
}

// ApplyPortOverrides modifies port mappings based on --port flags.
// Each override is "newHost:containerPort". It replaces the host port
// for the matching container port in the ports list.
// Preserves protocol suffixes like /udp.
func ApplyPortOverrides(ports []string, overrides []string) ([]string, error) {
	// Parse overrides into container→host map
	overrideMap := make(map[int]int)
	for _, o := range overrides {
		parts := strings.SplitN(o, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid port override %q: expected host:container (e.g., 5901:5900)", o)
		}
		newHost, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid host port in override %q: %w", o, err)
		}
		clean, _ := stripPortSuffix(parts[1])
		contPort, err := strconv.Atoi(clean)
		if err != nil {
			return nil, fmt.Errorf("invalid container port in override %q: %w", o, err)
		}
		overrideMap[contPort] = newHost
	}

	// Apply overrides
	result := make([]string, len(ports))
	for i, mapping := range ports {
		contPort, err := ParseContainerPort(mapping)
		if err != nil {
			result[i] = mapping
			continue
		}
		if newHost, ok := overrideMap[contPort]; ok {
			// Preserve protocol suffix (/udp, /tcp)
			suffix := ""
			if strings.HasSuffix(mapping, "/udp") {
				suffix = "/udp"
			} else if strings.HasSuffix(mapping, "/tcp") {
				suffix = "/tcp"
			}
			result[i] = fmt.Sprintf("%d:%d%s", newHost, contPort, suffix)
		} else {
			result[i] = mapping
		}
	}
	return result, nil
}

// SavePortOverride writes port overrides to deploy.yml for persistence.
func SavePortOverride(image, instance string, ports []string) error {
	dc, err := loadDeployConfigForWrite("SavePortOverride")
	if err != nil {
		return err
	}

	key := deployKey(image, instance)
	overlay := dc.Deploy[key]
	overlay.Port = ports
	dc.Deploy[key] = overlay

	return SaveDeployConfig(dc)
}

// containerPortsFromMappings extracts the container-side port number from
// each mapping. "auto" sentinels are skipped (they have no container port
// to extract — they ARE the request to allocate one). Unparseable entries
// are silently dropped (the loud-skip warning lives in CheckPortAvailability).
func containerPortsFromMappings(mappings []string) ([]int, error) {
	if len(mappings) == 0 {
		return nil, nil
	}
	result := make([]int, 0, len(mappings))
	for _, m := range mappings {
		if IsAutoPort(m) {
			continue
		}
		cp, err := ParseContainerPort(m)
		if err != nil {
			continue
		}
		result = append(result, cp)
	}
	return result, nil
}

// IsAutoPort reports whether a port-list entry is the literal "auto" sentinel.
// Authors write `port: [auto]` (or `port: [auto, "8443:443"]` to mix
// auto-allocation with explicit pins) in deploy.yml.
func IsAutoPort(mapping string) bool {
	return strings.TrimSpace(mapping) == "auto"
}

// HasAutoPort reports whether any entry in the list is the auto sentinel.
func HasAutoPort(ports []string) bool {
	for _, p := range ports {
		if IsAutoPort(p) {
			return true
		}
	}
	return false
}

// AllocateAutoPorts probes free TCP host ports — one per container port,
// in declaration order. The `occupied` set names host ports already in
// use by other deployments (so two `port: [auto]` deploys on the same
// host don't collide). Returned mappings have BindAddr="" (default host
// bind) and Protocol="tcp". Each successful allocation is recorded back
// into `occupied` so subsequent calls in the same DeployConfig pass see
// the reservation.
//
// Free-port discovery uses the same net.Listen("tcp","127.0.0.1:0") +
// immediate-close pattern already used by ssh_tunnel.go:78 and
// vnc_vm.go:163 — the OS picks an ephemeral port, we close, and the
// caller binds in the (small) window before the OS reassigns.
func AllocateAutoPorts(containerPorts []int, occupied map[int]bool) ([]ParsedPortMapping, error) {
	if len(containerPorts) == 0 {
		return nil, nil
	}
	if occupied == nil {
		occupied = map[int]bool{}
	}
	result := make([]ParsedPortMapping, 0, len(containerPorts))
	for _, cp := range containerPorts {
		if cp <= 0 || cp > 65535 {
			return nil, fmt.Errorf("AllocateAutoPorts: invalid container port %d", cp)
		}
		var host int
		for attempt := 0; attempt < 32; attempt++ {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, fmt.Errorf("AllocateAutoPorts: probe failed for container port %d: %w", cp, err)
			}
			candidate := ln.Addr().(*net.TCPAddr).Port
			_ = ln.Close()
			if !occupied[candidate] {
				host = candidate
				break
			}
		}
		if host == 0 {
			return nil, fmt.Errorf("AllocateAutoPorts: exhausted 32 attempts for container port %d", cp)
		}
		occupied[host] = true
		result = append(result, ParsedPortMapping{Host: host, Container: cp})
	}
	return result, nil
}

// ExpandAutoPorts replaces every "auto" sentinel in the list with concrete
// `host:container` pairs allocated from containerPorts (one allocation per
// container port, in declaration order). Explicit mappings pass through
// unchanged AND their host ports are added to the occupied set so the
// allocator avoids collisions.
//
// Multiple "auto" sentinels in one list collapse to a single expansion at
// the first sentinel's position (so `port: [auto, auto]` is treated the
// same as `port: [auto]`).
//
// Returns the expanded list and a flag indicating whether expansion
// happened (caller can persist the result back to deploy.yml as
// `resolved_port:`).
func ExpandAutoPorts(ports []string, containerPorts []int, occupied map[int]bool) ([]string, bool, error) {
	if !HasAutoPort(ports) {
		return ports, false, nil
	}
	if occupied == nil {
		occupied = map[int]bool{}
	}
	// Reserve explicit host ports first so auto-allocation can't collide.
	for _, p := range ports {
		if IsAutoPort(p) {
			continue
		}
		if h, err := ParseHostPort(p); err == nil {
			occupied[h] = true
		}
	}
	allocs, err := AllocateAutoPorts(containerPorts, occupied)
	if err != nil {
		return nil, false, err
	}
	result := make([]string, 0, len(ports)+len(allocs))
	consumed := false
	for _, p := range ports {
		if IsAutoPort(p) {
			if consumed {
				continue
			}
			for _, a := range allocs {
				result = append(result, FormatPortMapping(a))
			}
			consumed = true
			continue
		}
		result = append(result, p)
	}
	return result, true, nil
}
