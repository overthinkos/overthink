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
)

// TunnelPort represents a single port to tunnel with its protocol and access scope.
type TunnelPort struct {
	Port        int    // Tailscale HTTPS listen port (must be valid serve port)
	BackendPort int    // Localhost backend port (0 means same as Port)
	Protocol    string // backend scheme: "http", "https", "https+insecure", "tcp", "tls-terminated-tcp", "ssh", "rdp", "smb"
	Public      bool   // true = internet-accessible, false = private (tailnet-only)
	Hostname    string // cloudflare: per-port hostname (from map form)
}

// backend returns the localhost backend port, defaulting to Port if BackendPort is zero.
func (tp TunnelPort) backend() int {
	if tp.BackendPort != 0 {
		return tp.BackendPort
	}
	return tp.Port
}

// TunnelConfig is the resolved, ready-to-execute tunnel configuration.
type TunnelConfig struct {
	Provider   string       // "tailscale" or "cloudflare"
	TunnelName string       // cloudflare: tunnel name
	Hostname   string       // cloudflare: default hostname (from image dns field)
	BoxName    string       // for PID file naming
	Ports      []TunnelPort // all tunneled ports with access scope
}

// schemeTarget returns the backend URL for a given scheme and port.
func schemeTarget(scheme string, port int) string {
	switch scheme {
	case "tcp", "tls-terminated-tcp":
		return fmt.Sprintf("tcp://127.0.0.1:%d", port)
	default:
		return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
	}
}

// tailscaleFlag returns the tailscale serve/funnel flag for a scheme.
// Returns "--https", "--tcp", or "--tls-terminated-tcp".
func tailscaleFlag(scheme string) string {
	switch scheme {
	case "tcp":
		return "--tcp"
	case "tls-terminated-tcp":
		return "--tls-terminated-tcp"
	default:
		return "--https"
	}
}

// isTCPFamily returns true for schemes that use TCP-style forwarding (not HTTP proxy).
func isTCPFamily(scheme string) bool {
	switch scheme {
	case "tcp", "tls-terminated-tcp":
		return true
	default:
		return false
	}
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
	if tp.Protocol == "udp" {
		fmt.Fprintf(os.Stderr, "Warning: port %d (UDP) cannot be tunneled — tailscale funnel only supports TCP/HTTPS. UDP traffic works directly between tailnet nodes.\n", tp.Port)
		return nil
	}
	port := strconv.Itoa(tp.Port)
	target := schemeTarget(tp.Protocol, tp.backend())
	flag := tailscaleFlag(tp.Protocol)
	cmd := exec.Command("tailscale", "funnel", "--bg", flag+"="+port, target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("public tunnel on port %s failed: %w", port, err)
	}
	fmt.Fprintf(os.Stderr, "Port %s: public (internet-accessible)\n", port)
	return nil
}

func tailscalePublicOneStop(tp TunnelPort) error {
	if tp.Protocol == "udp" {
		return nil // UDP ports are not tunneled
	}
	port := strconv.Itoa(tp.Port)
	flag := tailscaleFlag(tp.Protocol)
	cmd := exec.Command("tailscale", "funnel", flag+"="+port, "off")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("public tunnel stop on port %s failed: %w", port, err)
	}
	fmt.Fprintf(os.Stderr, "Port %s: public tunnel disabled\n", port)
	return nil
}

// --- Tailscale Private (tailnet-only via serve) ---

func tailscalePrivateOneStart(tp TunnelPort) error {
	if tp.Protocol == "udp" {
		fmt.Fprintf(os.Stderr, "Warning: port %d (UDP) cannot be tunneled — tailscale serve only supports TCP/HTTPS. UDP traffic works directly between tailnet nodes.\n", tp.Port)
		return nil
	}
	port := strconv.Itoa(tp.Port)
	target := schemeTarget(tp.Protocol, tp.backend())
	flag := tailscaleFlag(tp.Protocol)
	cmd := exec.Command("tailscale", "serve", "--bg", flag+"="+port, target)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("private tunnel on port %s failed: %w", port, err)
	}
	proto := "https"
	if isTCPFamily(tp.Protocol) {
		proto = "tcp"
	}
	fmt.Fprintf(os.Stderr, "Port %s: private (tailnet-only, %s)\n", port, proto)
	return nil
}

func tailscalePrivateOneStop(tp TunnelPort) error {
	if tp.Protocol == "udp" {
		return nil // UDP ports are not tunneled
	}
	port := strconv.Itoa(tp.Port)
	flag := tailscaleFlag(tp.Protocol)
	cmd := exec.Command("tailscale", "serve", flag+"="+port, "off")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("private tunnel stop on port %s failed: %w", port, err)
	}
	proto := "https"
	if isTCPFamily(tp.Protocol) {
		proto = "tcp"
	}
	fmt.Fprintf(os.Stderr, "Port %s: private tunnel disabled (%s)\n", port, proto)
	return nil
}

// --- Cloudflare Tunnel ---

// tunnelConfigDir returns ~/.config/charly/tunnels/
func tunnelConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "charly", "tunnels"), nil
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

// cloudflareTunnelSetup creates the tunnel, writes config YAML, and routes DNS.
// Called by charly config (quadlet mode) for setup-only, and by cloudflareTunnelStart (direct mode).
// Returns the tunnel name and config file path.
func cloudflareTunnelSetup(cfg TunnelConfig) (tunnelName, configPath string, err error) {
	name := cfg.TunnelName

	// 1. Check if tunnel already exists
	uuid, err := findCloudflaredTunnel(name)
	if err != nil {
		return "", "", err
	}

	// 2. Create tunnel if it doesn't exist
	if uuid == "" {
		uuid, err = createCloudflaredTunnel(name)
		if err != nil {
			return "", "", err
		}
	}

	// 3. Find credentials file
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("determining home directory: %w", err)
	}
	credsFile := filepath.Join(home, ".cloudflared", uuid+".json")
	if _, err := os.Stat(credsFile); err != nil {
		return "", "", fmt.Errorf("credentials file not found at %s (run 'cloudflared tunnel login' first): %w", credsFile, err)
	}

	// 4. Write config file
	configDir, err := tunnelConfigDir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", "", fmt.Errorf("creating tunnel config dir: %w", err)
	}

	cfgPath, err := tunnelConfigPath(name)
	if err != nil {
		return "", "", err
	}

	// Build ingress rules from public ports
	var ingress strings.Builder
	for _, tp := range cfg.Ports {
		if tp.Protocol == "udp" {
			fmt.Fprintf(os.Stderr, "Warning: port %d (UDP) skipped in Cloudflare tunnel — cloudflared only supports HTTP/WebSocket\n", tp.Port)
			continue
		}
		hostname := tp.Hostname
		if hostname == "" {
			hostname = cfg.Hostname // fallback to image dns
		}
		fmt.Fprintf(&ingress, "  - hostname: %s\n    service: %s://localhost:%d\n", hostname, tp.Protocol, tp.Port)
	}
	ingress.WriteString("  - service: http_status:404\n")

	configContent := fmt.Sprintf("tunnel: %s\ncredentials-file: %s\ningress:\n%s", uuid, credsFile, ingress.String())

	if err := os.WriteFile(cfgPath, []byte(configContent), 0600); err != nil {
		return "", "", fmt.Errorf("writing tunnel config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote tunnel config %s\n", cfgPath)

	// 5. Route DNS for each hostname (idempotent)
	hostnames := collectUniqueHostnames(cfg)
	for _, h := range hostnames {
		fmt.Fprintf(os.Stderr, "Routing DNS %s → tunnel %s\n", h, name)
		dnsCmd := exec.Command("cloudflared", "tunnel", "route", "dns", "--overwrite-dns", name, h)
		dnsCmd.Stdout = os.Stderr
		dnsCmd.Stderr = os.Stderr
		if err := dnsCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: DNS route for %s failed (may already exist): %v\n", h, err)
		}
	}

	return name, cfgPath, nil
}

// cloudflareTunnelStart sets up the tunnel (create, config, DNS) then starts the cloudflared process.
// Used in direct mode. In quadlet mode, charly config calls cloudflareTunnelSetup and the systemd service runs cloudflared.
func cloudflareTunnelStart(cfg TunnelConfig) error {
	name, cfgPath, err := cloudflareTunnelSetup(cfg)
	if err != nil {
		return err
	}

	// Start cloudflared in background
	cmd := exec.Command("cloudflared", "tunnel", "--config", cfgPath, "run", name)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	// Write PID file
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
		_ = os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	// Send SIGTERM
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		// Process may already be dead
		fmt.Fprintf(os.Stderr, "Warning: could not signal PID %d: %v\n", pid, err)
	} else {
		fmt.Fprintf(os.Stderr, "Stopped cloudflared tunnel %s (PID %d)\n", name, pid)
	}

	_ = os.Remove(pidPath)
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
	if _, after, ok := strings.Cut(outputStr, "with id "); ok {
		uuid := strings.TrimSpace(after)
		// UUID may have trailing newline or text
		if nlIdx := strings.IndexAny(uuid, "\n\r "); nlIdx != -1 {
			uuid = uuid[:nlIdx]
		}
		return uuid, nil
	}

	return "", fmt.Errorf("could not parse tunnel UUID from output: %s", outputStr)
}

// parseHostPorts extracts host-side ports from image port mappings via the
// canonical ParsePortMapping. Unparseable entries are reported on stderr —
// silent skipping was the root cause of an unrelated bug where tunnel rules
// vanished without a diagnostic.
func parseHostPorts(boxPorts []string) []int {
	var result []int
	for _, mapping := range boxPorts {
		p, ok := ParsePortMapping(mapping)
		if !ok {
			fmt.Fprintf(os.Stderr,
				"Warning: ignoring unparseable port mapping %q (expected forms: \"P\", \"H:C\", \"IP:H:C\")\n",
				mapping)
			continue
		}
		result = append(result, p.Host)
	}
	return result
}

// buildPortMapping builds a host→container port map from image port mappings.
// Same loud-failure policy as parseHostPorts — see comment above.
func buildPortMapping(boxPorts []string) map[int]int {
	m := make(map[int]int, len(boxPorts))
	for _, mapping := range boxPorts {
		p, ok := ParsePortMapping(mapping)
		if !ok {
			fmt.Fprintf(os.Stderr,
				"Warning: ignoring unparseable port mapping %q (expected forms: \"P\", \"H:C\", \"IP:H:C\")\n",
				mapping)
			continue
		}
		m[p.Host] = p.Container
	}
	return m
}

// resolveProto returns the backend scheme for a container port, defaulting to "http".
func resolveProto(containerPort int, portProtos map[int]string) string {
	if portProtos != nil {
		if pp, ok := portProtos[containerPort]; ok {
			return pp
		}
	}
	return "http"
}

// ResolveTunnelConfig resolves a TunnelYAML into a TunnelConfig with defaults applied.
// portProtos maps container port -> protocol ("http" or "tcp") from candy PortSpec data.
// boxPorts is the list of image port mappings (e.g. "18789:18789", "443:18789").
func ResolveTunnelConfig(t *TunnelYAML, boxName string, dns string, _ map[string]*Candy, _ []string, portProtos map[int]string, boxPorts []string) *TunnelConfig {
	if t == nil {
		return nil
	}

	cfg := &TunnelConfig{
		Provider: t.Provider,
		BoxName:  boxName,
	}

	hostPorts := parseHostPorts(boxPorts)
	hostToContainer := buildPortMapping(boxPorts)

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
			Port:        hp,
			BackendPort: hp,
			Protocol:    proto,
			Public:      publicSet[hp],
			Hostname:    publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "charly-" + boxName
		}
		cfg.Hostname = dns
	}

	return cfg
}

// TunnelConfigFromMetadata creates a TunnelConfig from image label metadata.
// Unlike ResolveTunnelConfig, this doesn't need candy access since the tunnel
// configuration is already stored in the label.
func TunnelConfigFromMetadata(meta *BoxMetadata) *TunnelConfig {
	if meta == nil || meta.Tunnel == nil {
		return nil
	}

	t := meta.Tunnel
	cfg := &TunnelConfig{
		Provider: t.Provider,
		BoxName:  meta.Box,
	}

	hostPorts := parseHostPorts(meta.Port)
	hostToContainer := buildPortMapping(meta.Port)

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
		proto := resolveProto(cp, meta.PortProto)
		cfg.Ports = append(cfg.Ports, TunnelPort{
			Port:        hp,
			BackendPort: hp,
			Protocol:    proto,
			Public:      publicSet[hp],
			Hostname:    publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "charly-" + meta.Box
		}
		cfg.Hostname = meta.DNS
	}

	return cfg
}
