package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// TunnelYAML supports both bare string and expanded form in images.yml:
//
//	tunnel: tailscale
//	tunnel: cloudflare
//	tunnel:
//	  provider: cloudflare
//	  port: 3001
type TunnelYAML struct {
	Provider string `yaml:"provider" json:"provider"`
	Port     int    `yaml:"port,omitempty" json:"port,omitempty"`
	HTTPS    int    `yaml:"https,omitempty" json:"https,omitempty"`    // tailscale only: external HTTPS port
	Funnel   bool   `yaml:"funnel,omitempty" json:"funnel,omitempty"` // tailscale only: true=funnel (public), false=serve (tailnet-private)
	Tunnel   string `yaml:"tunnel,omitempty" json:"tunnel,omitempty"` // cloudflare only: tunnel name
	Ports    string `yaml:"ports,omitempty" json:"ports,omitempty"`   // "all" to expose all image ports
}

// UnmarshalYAML handles bare string ("tailscale"/"cloudflare") or expanded form.
func (t *TunnelYAML) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		t.Provider = value.Value
		return nil
	}
	// Expanded form: decode into an alias to avoid infinite recursion
	type raw TunnelYAML
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*t = TunnelYAML(r)
	return nil
}

// TunnelPort represents a single port to tunnel with its protocol.
type TunnelPort struct {
	Port     int
	Protocol string // "http" or "tcp"
}

// TunnelConfig is the resolved, ready-to-execute tunnel configuration.
type TunnelConfig struct {
	Provider   string // "tailscale" or "cloudflare"
	Port       int    // container port to tunnel to (single-port mode)
	HTTPS      int    // tailscale: external HTTPS port (single-port mode)
	Funnel     bool   // tailscale: true=funnel (public), false=serve (tailnet-private)
	TunnelName string // cloudflare: tunnel name
	Hostname   string // cloudflare: from fqdn
	ImageName  string // for PID file naming
	Ports      []TunnelPort // multiple ports (when tunnel.ports == "all")
}

// collectPortProtos builds a port->protocol map from layer PortSpec data.
func collectPortProtos(layers map[string]*Layer, layerNames []string) map[int]string {
	protos := make(map[int]string)
	for _, name := range layerNames {
		layer, ok := layers[name]
		if !ok {
			continue
		}
		for _, ps := range layer.PortSpecs() {
			if ps.Protocol != "" && ps.Protocol != "http" {
				protos[ps.Port] = ps.Protocol
			}
		}
	}
	if len(protos) == 0 {
		return nil
	}
	return protos
}

// ValidFunnelPorts are the allowed external ports for Tailscale Funnel.
var ValidFunnelPorts = map[int]bool{443: true, 8443: true, 10000: true}

// TunnelStart dispatches to the appropriate provider's start function.
// Package-level var for testability (same pattern as gpu.go DetectGPU).
var TunnelStart = defaultTunnelStart

func defaultTunnelStart(cfg TunnelConfig) error {
	switch cfg.Provider {
	case "tailscale":
		if cfg.Funnel {
			return tailscaleFunnelStart(cfg)
		}
		return tailscaleServeStart(cfg)
	case "cloudflare":
		return cloudflareTunnelStart(cfg)
	default:
		return fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
}

// TunnelStop dispatches to the appropriate provider's stop function.
var TunnelStop = defaultTunnelStop

func defaultTunnelStop(cfg TunnelConfig) error {
	switch cfg.Provider {
	case "tailscale":
		if cfg.Funnel {
			return tailscaleFunnelStop(cfg)
		}
		return tailscaleServeStop(cfg)
	case "cloudflare":
		return cloudflareTunnelStop(cfg)
	default:
		return fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
}

// --- Tailscale Funnel ---

func tailscaleFunnelStart(cfg TunnelConfig) error {
	httpsPort := strconv.Itoa(cfg.HTTPS)
	target := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	cmd := exec.Command("tailscale", "funnel", "--bg", "--https="+httpsPort, target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale funnel start failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Tailscale Funnel enabled on :%s -> %s\n", httpsPort, target)
	return nil
}

func tailscaleFunnelStop(cfg TunnelConfig) error {
	httpsPort := strconv.Itoa(cfg.HTTPS)
	cmd := exec.Command("tailscale", "funnel", httpsPort, "off")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale funnel stop failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Tailscale Funnel disabled on :%s\n", httpsPort)
	return nil
}

// --- Tailscale Serve ---

// ValidServePorts are the allowed external ports for Tailscale Serve.
var ValidServePorts = map[int]bool{
	80: true, 443: true, 3000: true, 3001: true, 3002: true, 3003: true,
	4443: true, 5432: true, 6443: true, 8443: true,
}

// isValidServePort checks if a port is allowed for tailscale serve.
// Allowed: 80, 443, 3000-10000, 4443, 5432, 6443, 8443.
func isValidServePort(port int) bool {
	if port >= 3000 && port <= 10000 {
		return true
	}
	return port == 80 || port == 443 || port == 4443 || port == 5432 || port == 6443 || port == 8443
}

func tailscaleServeStart(cfg TunnelConfig) error {
	// Multi-port mode
	if len(cfg.Ports) > 0 {
		for _, tp := range cfg.Ports {
			if err := tailscaleServeOneStart(tp); err != nil {
				return err
			}
		}
		return nil
	}
	// Single-port mode
	httpsPort := strconv.Itoa(cfg.HTTPS)
	target := fmt.Sprintf("http://127.0.0.1:%d", cfg.Port)
	cmd := exec.Command("tailscale", "serve", "--bg", "--https="+httpsPort, target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale serve start failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Tailscale Serve enabled on :%s -> %s\n", httpsPort, target)
	return nil
}

func tailscaleServeOneStart(tp TunnelPort) error {
	port := strconv.Itoa(tp.Port)
	var cmd *exec.Cmd
	if tp.Protocol == "tcp" {
		target := fmt.Sprintf("tcp://127.0.0.1:%d", tp.Port)
		cmd = exec.Command("tailscale", "serve", "--bg", "--tcp="+port, target)
	} else {
		target := fmt.Sprintf("http://127.0.0.1:%d", tp.Port)
		cmd = exec.Command("tailscale", "serve", "--bg", "--https="+port, target)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale serve start on port %s failed: %w", port, err)
	}
	proto := "HTTPS"
	if tp.Protocol == "tcp" {
		proto = "TCP"
	}
	fmt.Fprintf(os.Stderr, "Tailscale Serve enabled %s :%s\n", proto, port)
	return nil
}

func tailscaleServeStop(cfg TunnelConfig) error {
	// Multi-port mode
	if len(cfg.Ports) > 0 {
		for _, tp := range cfg.Ports {
			if err := tailscaleServeOneStop(tp); err != nil {
				return err
			}
		}
		return nil
	}
	// Single-port mode
	httpsPort := strconv.Itoa(cfg.HTTPS)
	cmd := exec.Command("tailscale", "serve", "--https="+httpsPort, "off")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale serve stop failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Tailscale Serve disabled on :%s\n", httpsPort)
	return nil
}

func tailscaleServeOneStop(tp TunnelPort) error {
	port := strconv.Itoa(tp.Port)
	var cmd *exec.Cmd
	if tp.Protocol == "tcp" {
		cmd = exec.Command("tailscale", "serve", "--tcp="+port, "off")
	} else {
		cmd = exec.Command("tailscale", "serve", "--https="+port, "off")
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tailscale serve stop on port %s failed: %w", port, err)
	}
	proto := "HTTPS"
	if tp.Protocol == "tcp" {
		proto = "TCP"
	}
	fmt.Fprintf(os.Stderr, "Tailscale Serve disabled %s :%s\n", proto, port)
	return nil
}

// --- Cloudflare Tunnel ---

// tunnelConfigDir returns ~/.config/ov/tunnels/
func tunnelConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "ov", "tunnels"), nil
}

// tunnelPIDPath returns the PID file path for a tunnel.
func tunnelPIDPath(name string) (string, error) {
	dir, err := tunnelConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".pid"), nil
}

// tunnelConfigPath returns the cloudflared config file path for a tunnel.
func tunnelConfigPath(name string) (string, error) {
	dir, err := tunnelConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yml"), nil
}

type cloudflaredTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func cloudflareTunnelStart(cfg TunnelConfig) error {
	name := cfg.TunnelName

	// 1. Check if tunnel already exists
	uuid, err := findCloudflaredTunnel(name)
	if err != nil {
		return err
	}

	// 2. Create tunnel if it doesn't exist
	if uuid == "" {
		uuid, err = createCloudflaredTunnel(name)
		if err != nil {
			return err
		}
	}

	// 3. Find credentials file
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}
	credsFile := filepath.Join(home, ".cloudflared", uuid+".json")
	if _, err := os.Stat(credsFile); err != nil {
		return fmt.Errorf("credentials file not found at %s (run 'cloudflared tunnel login' first): %w", credsFile, err)
	}

	// 4. Write config file
	configDir, err := tunnelConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("creating tunnel config dir: %w", err)
	}

	cfgPath, err := tunnelConfigPath(name)
	if err != nil {
		return err
	}

	configContent := fmt.Sprintf(`tunnel: %s
credentials-file: %s
ingress:
  - hostname: %s
    service: http://localhost:%d
  - service: http_status:404
`, uuid, credsFile, cfg.Hostname, cfg.Port)

	if err := os.WriteFile(cfgPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("writing tunnel config: %w", err)
	}

	// 5. Route DNS (idempotent)
	if cfg.Hostname != "" {
		dnsCmd := exec.Command("cloudflared", "tunnel", "route", "dns", name, cfg.Hostname)
		dnsCmd.Stdout = os.Stderr
		dnsCmd.Stderr = os.Stderr
		if err := dnsCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: DNS route for %s failed (may already exist): %v\n", cfg.Hostname, err)
		}
	}

	// 6. Start cloudflared in background
	cmd := exec.Command("cloudflared", "tunnel", "--config", cfgPath, "run", name)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	// 7. Write PID file
	pidPath, err := tunnelPIDPath(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Cloudflare Tunnel %s started (PID %d) -> %s\n", name, cmd.Process.Pid, cfg.Hostname)
	return nil
}

func cloudflareTunnelStop(cfg TunnelConfig) error {
	name := cfg.TunnelName

	pidPath, err := tunnelPIDPath(name)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no PID file, nothing to stop
		}
		return fmt.Errorf("reading PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	// Send SIGTERM
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		// Process may already be dead
		fmt.Fprintf(os.Stderr, "Warning: could not signal PID %d: %v\n", pid, err)
	} else {
		fmt.Fprintf(os.Stderr, "Stopped cloudflared tunnel %s (PID %d)\n", name, pid)
	}

	os.Remove(pidPath)
	return nil
}

func findCloudflaredTunnel(name string) (string, error) {
	cmd := exec.Command("cloudflared", "tunnel", "list", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("listing cloudflare tunnels: %w", err)
	}

	var tunnels []cloudflaredTunnel
	if err := json.Unmarshal(output, &tunnels); err != nil {
		return "", fmt.Errorf("parsing tunnel list: %w", err)
	}

	for _, t := range tunnels {
		if t.Name == name {
			return t.ID, nil
		}
	}
	return "", nil
}

func createCloudflaredTunnel(name string) (string, error) {
	cmd := exec.Command("cloudflared", "tunnel", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating cloudflare tunnel: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	// Parse UUID from output: "Created tunnel <name> with id <uuid>"
	outputStr := string(output)
	if idx := strings.Index(outputStr, "with id "); idx != -1 {
		uuid := strings.TrimSpace(outputStr[idx+len("with id "):])
		// UUID may have trailing newline or text
		if nlIdx := strings.IndexAny(uuid, "\n\r "); nlIdx != -1 {
			uuid = uuid[:nlIdx]
		}
		return uuid, nil
	}

	return "", fmt.Errorf("could not parse tunnel UUID from output: %s", outputStr)
}

// ResolveTunnelConfig resolves a TunnelYAML into a TunnelConfig with defaults applied.
// portProtos maps container port -> protocol ("http" or "tcp") from layer PortSpec data.
// imagePorts is the list of image port mappings (e.g. "18789:18789", "5900").
func ResolveTunnelConfig(t *TunnelYAML, imageName string, fqdn string, layers map[string]*Layer, layerNames []string, portProtos map[int]string, imagePorts []string) *TunnelConfig {
	if t == nil {
		return nil
	}

	cfg := &TunnelConfig{
		Provider:  t.Provider,
		Port:      t.Port,
		ImageName: imageName,
	}

	// Multi-port mode: when ports == "all", populate from image ports
	if t.Ports == "all" && len(imagePorts) > 0 {
		for _, mapping := range imagePorts {
			containerPort := mapping
			// Parse host:container format
			if idx := strings.LastIndex(mapping, ":"); idx != -1 {
				containerPort = mapping[idx+1:]
			}
			p, err := strconv.Atoi(containerPort)
			if err != nil {
				continue
			}
			proto := "http"
			if portProtos != nil {
				if pp, ok := portProtos[p]; ok {
					proto = pp
				}
			}
			cfg.Ports = append(cfg.Ports, TunnelPort{Port: p, Protocol: proto})
		}
	}

	// Default port: first route port from layers (single-port mode only)
	if cfg.Port == 0 && len(cfg.Ports) == 0 && layers != nil {
		for _, ln := range layerNames {
			layer, ok := layers[ln]
			if !ok {
				continue
			}
			if layer.HasRoute {
				route, err := layer.Route()
				if err == nil && route != nil && route.Port != "" {
					if p, err := strconv.Atoi(route.Port); err == nil {
						cfg.Port = p
						break
					}
				}
			}
		}
	}

	switch cfg.Provider {
	case "tailscale":
		cfg.Funnel = t.Funnel
		cfg.HTTPS = t.HTTPS
		if cfg.HTTPS == 0 && len(cfg.Ports) == 0 {
			cfg.HTTPS = 443
		}
	case "cloudflare":
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "ov-" + imageName
		}
		cfg.Hostname = fqdn
	}

	return cfg
}

// TunnelConfigFromMetadata creates a TunnelConfig from image label metadata.
// Unlike ResolveTunnelConfig, this doesn't need layer access since the tunnel port
// is already resolved and stored in the label.
func TunnelConfigFromMetadata(meta *ImageMetadata) *TunnelConfig {
	if meta == nil || meta.Tunnel == nil {
		return nil
	}

	t := meta.Tunnel
	cfg := &TunnelConfig{
		Provider:  t.Provider,
		Port:      t.Port,
		ImageName: meta.Image,
	}

	// Multi-port mode
	if t.Ports == "all" && len(meta.Ports) > 0 {
		for _, mapping := range meta.Ports {
			containerPort := mapping
			if idx := strings.LastIndex(mapping, ":"); idx != -1 {
				containerPort = mapping[idx+1:]
			}
			p, err := strconv.Atoi(containerPort)
			if err != nil {
				continue
			}
			proto := "http"
			if meta.PortProtos != nil {
				if pp, ok := meta.PortProtos[p]; ok {
					proto = pp
				}
			}
			cfg.Ports = append(cfg.Ports, TunnelPort{Port: p, Protocol: proto})
		}
	}

	// If port is still 0 and not multi-port, try first route from label metadata
	if cfg.Port == 0 && len(cfg.Ports) == 0 && len(meta.Routes) > 0 {
		cfg.Port = meta.Routes[0].Port
	}

	switch cfg.Provider {
	case "tailscale":
		cfg.Funnel = t.Funnel
		cfg.HTTPS = t.HTTPS
		if cfg.HTTPS == 0 && len(cfg.Ports) == 0 {
			cfg.HTTPS = 443
		}
	case "cloudflare":
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "ov-" + meta.Image
		}
		cfg.Hostname = meta.FQDN
	}

	return cfg
}
