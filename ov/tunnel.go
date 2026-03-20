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

// PortScope handles three YAML forms for public/private port specification:
//
//	"all"                        → All=true
//	[443, 8443]                  → Ports=[443, 8443]
//	{18789: "host.example.com"}  → PortMap={18789: "host.example.com"}
type PortScope struct {
	All     bool
	Ports   []int
	PortMap map[int]string // port → hostname (cloudflare only)
}

func (p *PortScope) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "all" {
			p.All = true
			return nil
		}
		return fmt.Errorf("expected 'all', port list, or port map, got %q", value.Value)
	case yaml.SequenceNode:
		return value.Decode(&p.Ports)
	case yaml.MappingNode:
		p.PortMap = make(map[int]string)
		return value.Decode(&p.PortMap)
	}
	return fmt.Errorf("unexpected YAML node type for port scope")
}

func (p PortScope) MarshalJSON() ([]byte, error) {
	if p.All {
		return json.Marshal("all")
	}
	if len(p.PortMap) > 0 {
		return json.Marshal(p.PortMap)
	}
	if len(p.Ports) > 0 {
		return json.Marshal(p.Ports)
	}
	return []byte("null"), nil
}

func (p *PortScope) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// Try string "all"
	var s string
	if json.Unmarshal(data, &s) == nil && s == "all" {
		p.All = true
		return nil
	}
	// Try []int
	var ports []int
	if json.Unmarshal(data, &ports) == nil {
		p.Ports = ports
		return nil
	}
	// Try map[string]string (JSON keys are always strings, convert to int)
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err == nil {
		p.PortMap = make(map[int]string, len(raw))
		for k, v := range raw {
			port, err := strconv.Atoi(k)
			if err != nil {
				return fmt.Errorf("invalid port number %q in port map: %w", k, err)
			}
			p.PortMap[port] = v
		}
		return nil
	}
	return fmt.Errorf("cannot unmarshal port scope from JSON: %s", string(data))
}

func (p PortScope) IsZero() bool {
	return !p.All && len(p.Ports) == 0 && len(p.PortMap) == 0
}

// TunnelYAML supports both bare string and expanded form:
//
//	tunnel: tailscale           → all ports private (tailnet-only)
//	tunnel: cloudflare          → all ports public (internet-accessible)
//	tunnel:
//	  provider: tailscale
//	  public: [443]
//	  private: all
type TunnelYAML struct {
	Provider string    `yaml:"provider" json:"provider"`
	Tunnel   string    `yaml:"tunnel,omitempty" json:"tunnel,omitempty"` // cloudflare: tunnel name
	Public   PortScope `yaml:"public,omitempty" json:"public,omitempty"`
	Private  PortScope `yaml:"private,omitempty" json:"private,omitempty"`
}

// UnmarshalYAML handles bare string ("tailscale"/"cloudflare") or expanded form.
func (t *TunnelYAML) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		t.Provider = value.Value
		switch value.Value {
		case "tailscale":
			t.Private = PortScope{All: true} // default: all ports private
		case "cloudflare":
			t.Public = PortScope{All: true} // default: all ports public
		}
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

// TunnelPort represents a single port to tunnel with its protocol and access scope.
type TunnelPort struct {
	Port     int
	Protocol string // "http" or "tcp"
	Public   bool   // true = internet-accessible, false = private (tailnet-only)
	Hostname string // cloudflare: per-port hostname (from map form)
}

// TunnelConfig is the resolved, ready-to-execute tunnel configuration.
type TunnelConfig struct {
	Provider   string       // "tailscale" or "cloudflare"
	TunnelName string       // cloudflare: tunnel name
	Hostname   string       // cloudflare: default hostname (from image dns field)
	ImageName  string       // for PID file naming
	Ports      []TunnelPort // all tunneled ports with access scope
}

// collectPortProtos builds a port->protocol map from layer PortSpec data.
// It resolves the full layer tree (including composing layers) to find all port specs.
func collectPortProtos(layers map[string]*Layer, layerNames []string) map[int]string {
	// Resolve full layer order including sub-layers of composing layers
	allLayers, err := ResolveLayerOrder(layerNames, layers, nil)
	if err != nil {
		// Fall back to direct layer names on error
		allLayers = layerNames
	}

	protos := make(map[int]string)
	for _, name := range allLayers {
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

// ValidPublicPorts are the allowed external ports for Tailscale public access.
var ValidPublicPorts = map[int]bool{443: true, 8443: true, 10000: true}

// TunnelStart dispatches to the appropriate provider's start function.
// Package-level var for testability (same pattern as gpu.go DetectGPU).
var TunnelStart = defaultTunnelStart

func defaultTunnelStart(cfg TunnelConfig) error {
	switch cfg.Provider {
	case "tailscale":
		for _, tp := range cfg.Ports {
			if tp.Public {
				if err := tailscalePublicOneStart(tp); err != nil {
					return err
				}
			} else {
				if err := tailscalePrivateOneStart(tp); err != nil {
					return err
				}
			}
		}
		return nil
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
		for _, tp := range cfg.Ports {
			if tp.Public {
				if err := tailscalePublicOneStop(tp); err != nil {
					return err
				}
			} else {
				if err := tailscalePrivateOneStop(tp); err != nil {
					return err
				}
			}
		}
		return nil
	case "cloudflare":
		return cloudflareTunnelStop(cfg)
	default:
		return fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
}

// --- Tailscale Public (internet-accessible) ---

func tailscalePublicOneStart(tp TunnelPort) error {
	port := strconv.Itoa(tp.Port)
	target := fmt.Sprintf("http://127.0.0.1:%d", tp.Port)
	cmd := exec.Command("tailscale", "funnel", "--bg", "--https="+port, target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("public tunnel on port %s failed: %w", port, err)
	}
	fmt.Fprintf(os.Stderr, "Port %s: public (internet-accessible)\n", port)
	return nil
}

func tailscalePublicOneStop(tp TunnelPort) error {
	port := strconv.Itoa(tp.Port)
	cmd := exec.Command("tailscale", "funnel", port, "off")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("public tunnel stop on port %s failed: %w", port, err)
	}
	fmt.Fprintf(os.Stderr, "Port %s: public tunnel disabled\n", port)
	return nil
}

// --- Tailscale Private (tailnet-only via serve) ---

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

func tailscalePrivateOneStart(tp TunnelPort) error {
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
		return fmt.Errorf("private tunnel on port %s failed: %w", port, err)
	}
	proto := "https"
	if tp.Protocol == "tcp" {
		proto = "tcp"
	}
	fmt.Fprintf(os.Stderr, "Port %s: private (tailnet-only, %s)\n", port, proto)
	return nil
}

func tailscalePrivateOneStop(tp TunnelPort) error {
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
		return fmt.Errorf("private tunnel stop on port %s failed: %w", port, err)
	}
	proto := "https"
	if tp.Protocol == "tcp" {
		proto = "tcp"
	}
	fmt.Fprintf(os.Stderr, "Port %s: private tunnel disabled (%s)\n", port, proto)
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

	// Build ingress rules from public ports
	var ingress strings.Builder
	for _, tp := range cfg.Ports {
		hostname := tp.Hostname
		if hostname == "" {
			hostname = cfg.Hostname // fallback to image dns
		}
		ingress.WriteString(fmt.Sprintf("  - hostname: %s\n    service: http://localhost:%d\n", hostname, tp.Port))
	}
	ingress.WriteString("  - service: http_status:404\n")

	configContent := fmt.Sprintf("tunnel: %s\ncredentials-file: %s\ningress:\n%s", uuid, credsFile, ingress.String())

	if err := os.WriteFile(cfgPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("writing tunnel config: %w", err)
	}

	// 5. Route DNS for each hostname (idempotent)
	hostnames := collectUniqueHostnames(cfg)
	for _, h := range hostnames {
		dnsCmd := exec.Command("cloudflared", "tunnel", "route", "dns", name, h)
		dnsCmd.Stdout = os.Stderr
		dnsCmd.Stderr = os.Stderr
		if err := dnsCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: DNS route for %s failed (may already exist): %v\n", h, err)
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

	fmt.Fprintf(os.Stderr, "Cloudflare Tunnel %s started (PID %d)\n", name, cmd.Process.Pid)
	return nil
}

// collectUniqueHostnames returns deduplicated hostnames from tunnel config ports.
func collectUniqueHostnames(cfg TunnelConfig) []string {
	seen := make(map[string]bool)
	var result []string
	for _, tp := range cfg.Ports {
		h := tp.Hostname
		if h == "" {
			h = cfg.Hostname
		}
		if h != "" && !seen[h] {
			seen[h] = true
			result = append(result, h)
		}
	}
	return result
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

// parseHostPorts extracts host-side ports from image port mappings.
// For "443:18789" returns 443. For "5900" returns 5900.
func parseHostPorts(imagePorts []string) []int {
	var result []int
	for _, mapping := range imagePorts {
		hostPort := mapping
		if idx := strings.Index(mapping, ":"); idx != -1 {
			hostPort = mapping[:idx]
		}
		p, err := strconv.Atoi(hostPort)
		if err != nil {
			continue
		}
		result = append(result, p)
	}
	return result
}

// buildPortMapping builds a host→container port map from image port mappings.
// For "443:18789" maps 443→18789. For "5900" maps 5900→5900.
func buildPortMapping(imagePorts []string) map[int]int {
	m := make(map[int]int, len(imagePorts))
	for _, mapping := range imagePorts {
		if idx := strings.Index(mapping, ":"); idx != -1 {
			host, err1 := strconv.Atoi(mapping[:idx])
			container, err2 := strconv.Atoi(mapping[idx+1:])
			if err1 == nil && err2 == nil {
				m[host] = container
			}
		} else {
			p, err := strconv.Atoi(mapping)
			if err == nil {
				m[p] = p
			}
		}
	}
	return m
}

// resolveProto returns the protocol for a container port, defaulting to "http".
func resolveProto(containerPort int, portProtos map[int]string) string {
	if portProtos != nil {
		if pp, ok := portProtos[containerPort]; ok {
			return pp
		}
	}
	return "http"
}

// ResolveTunnelConfig resolves a TunnelYAML into a TunnelConfig with defaults applied.
// portProtos maps container port -> protocol ("http" or "tcp") from layer PortSpec data.
// imagePorts is the list of image port mappings (e.g. "18789:18789", "443:18789").
func ResolveTunnelConfig(t *TunnelYAML, imageName string, dns string, layers map[string]*Layer, layerNames []string, portProtos map[int]string, imagePorts []string) *TunnelConfig {
	if t == nil {
		return nil
	}

	cfg := &TunnelConfig{
		Provider:  t.Provider,
		ImageName: imageName,
	}

	hostPorts := parseHostPorts(imagePorts)
	hostToContainer := buildPortMapping(imagePorts)

	// Determine public set
	publicSet := make(map[int]bool)
	publicHostnames := make(map[int]string)
	if t.Public.All {
		for _, p := range hostPorts {
			publicSet[p] = true
		}
	}
	for _, p := range t.Public.Ports {
		publicSet[p] = true
	}
	for p, h := range t.Public.PortMap {
		publicSet[p] = true
		publicHostnames[p] = h
	}

	// Determine private set ("all" means all remaining ports not already public)
	privateSet := make(map[int]bool)
	if t.Private.All {
		for _, p := range hostPorts {
			if !publicSet[p] {
				privateSet[p] = true
			}
		}
	}
	for _, p := range t.Private.Ports {
		privateSet[p] = true
	}

	// Build TunnelPort slice (ordered by image port order)
	for _, hp := range hostPorts {
		if !publicSet[hp] && !privateSet[hp] {
			continue // port not tunneled
		}
		cp := hp
		if c, ok := hostToContainer[hp]; ok {
			cp = c
		}
		proto := resolveProto(cp, portProtos)
		cfg.Ports = append(cfg.Ports, TunnelPort{
			Port:     hp,
			Protocol: proto,
			Public:   publicSet[hp],
			Hostname: publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "ov-" + imageName
		}
		cfg.Hostname = dns
	}

	return cfg
}

// TunnelConfigFromMetadata creates a TunnelConfig from image label metadata.
// Unlike ResolveTunnelConfig, this doesn't need layer access since the tunnel
// configuration is already stored in the label.
func TunnelConfigFromMetadata(meta *ImageMetadata) *TunnelConfig {
	if meta == nil || meta.Tunnel == nil {
		return nil
	}

	t := meta.Tunnel
	cfg := &TunnelConfig{
		Provider:  t.Provider,
		ImageName: meta.Image,
	}

	hostPorts := parseHostPorts(meta.Ports)
	hostToContainer := buildPortMapping(meta.Ports)

	// Determine public set
	publicSet := make(map[int]bool)
	publicHostnames := make(map[int]string)
	if t.Public.All {
		for _, p := range hostPorts {
			publicSet[p] = true
		}
	}
	for _, p := range t.Public.Ports {
		publicSet[p] = true
	}
	for p, h := range t.Public.PortMap {
		publicSet[p] = true
		publicHostnames[p] = h
	}

	// Determine private set
	privateSet := make(map[int]bool)
	if t.Private.All {
		for _, p := range hostPorts {
			if !publicSet[p] {
				privateSet[p] = true
			}
		}
	}
	for _, p := range t.Private.Ports {
		privateSet[p] = true
	}

	// Build TunnelPort slice
	for _, hp := range hostPorts {
		if !publicSet[hp] && !privateSet[hp] {
			continue
		}
		cp := hp
		if c, ok := hostToContainer[hp]; ok {
			cp = c
		}
		proto := resolveProto(cp, meta.PortProtos)
		cfg.Ports = append(cfg.Ports, TunnelPort{
			Port:     hp,
			Protocol: proto,
			Public:   publicSet[hp],
			Hostname: publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "ov-" + meta.Image
		}
		cfg.Hostname = meta.DNS
	}

	return cfg
}
