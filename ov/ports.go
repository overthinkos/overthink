package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// PortConflict describes a host port that is already in use.
type PortConflict struct {
	HostPort  int
	ContPort  int
	Owner     string // container name, or "unknown"
	OwnerType string // "ov-container", "container", "host-process"
}

// ParseHostPort extracts the host port from a mapping like "8000:8000" or "8000".
func ParseHostPort(mapping string) (int, error) {
	parts := strings.SplitN(mapping, ":", 2)
	return strconv.Atoi(parts[0])
}

// ParseContainerPort extracts the container port from a mapping like "8000:9000" or "8000".
func ParseContainerPort(mapping string) (int, error) {
	parts := strings.SplitN(mapping, ":", 2)
	if len(parts) == 2 {
		return strconv.Atoi(parts[1])
	}
	return strconv.Atoi(parts[0])
}

// CheckPortAvailability tests whether each host port can be bound.
// Returns a list of conflicts for ports that are already in use.
func CheckPortAvailability(ports []string, bindAddr string, engine string) []PortConflict {
	var conflicts []PortConflict
	for _, mapping := range ports {
		hostPort, err := ParseHostPort(mapping)
		if err != nil {
			continue
		}
		contPort, _ := ParseContainerPort(mapping)

		addr := fmt.Sprintf("%s:%d", bindAddr, hostPort)
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
		contPort, err := strconv.Atoi(parts[1])
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
			result[i] = fmt.Sprintf("%d:%d", newHost, contPort)
		} else {
			result[i] = mapping
		}
	}
	return result, nil
}

// SavePortOverride writes port overrides to deploy.yml for persistence.
func SavePortOverride(image string, ports []string) error {
	path, err := DeployConfigPath()
	if err != nil {
		return fmt.Errorf("determining deploy config path: %w", err)
	}

	dc, _ := LoadDeployConfig()
	if dc == nil {
		dc = &DeployConfig{Images: make(map[string]DeployImageConfig)}
	}
	if dc.Images == nil {
		dc.Images = make(map[string]DeployImageConfig)
	}

	overlay := dc.Images[image]
	overlay.Ports = ports
	dc.Images[image] = overlay

	data, err := yaml.Marshal(dc)
	if err != nil {
		return fmt.Errorf("marshaling deploy config: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
