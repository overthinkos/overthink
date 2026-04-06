package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// --- PortScope YAML Unmarshaling ---

func TestPortScopeUnmarshalAll(t *testing.T) {
	var ps PortScope
	if err := yaml.Unmarshal([]byte(`all`), &ps); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !ps.All {
		t.Error("All = false, want true")
	}
}

func TestPortScopeUnmarshalList(t *testing.T) {
	var ps PortScope
	if err := yaml.Unmarshal([]byte("[443, 8443]"), &ps); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	want := []int{443, 8443}
	if !reflect.DeepEqual(ps.Ports, want) {
		t.Errorf("Ports = %v, want %v", ps.Ports, want)
	}
}

func TestPortScopeUnmarshalMap(t *testing.T) {
	input := `
18789: "app.example.com"
9222: "chrome.example.com"
`
	var ps PortScope
	if err := yaml.Unmarshal([]byte(input), &ps); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if ps.PortMap[18789] != "app.example.com" {
		t.Errorf("PortMap[18789] = %q, want app.example.com", ps.PortMap[18789])
	}
	if ps.PortMap[9222] != "chrome.example.com" {
		t.Errorf("PortMap[9222] = %q, want chrome.example.com", ps.PortMap[9222])
	}
}

func TestPortScopeIsZero(t *testing.T) {
	var ps PortScope
	if !ps.IsZero() {
		t.Error("zero PortScope.IsZero() = false, want true")
	}
	ps.All = true
	if ps.IsZero() {
		t.Error("All=true PortScope.IsZero() = true, want false")
	}
}

// --- PortScope JSON Round-Trip ---

func TestPortScopeJSONRoundTripAll(t *testing.T) {
	orig := PortScope{All: true}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) != `"all"` {
		t.Errorf("Marshal = %s, want \"all\"", data)
	}
	var decoded PortScope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !decoded.All {
		t.Error("round-trip All = false, want true")
	}
}

func TestPortScopeJSONRoundTripList(t *testing.T) {
	orig := PortScope{Ports: []int{443, 8443}}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var decoded PortScope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !reflect.DeepEqual(decoded.Ports, orig.Ports) {
		t.Errorf("round-trip Ports = %v, want %v", decoded.Ports, orig.Ports)
	}
}

func TestPortScopeJSONRoundTripMap(t *testing.T) {
	orig := PortScope{PortMap: map[int]string{18789: "app.example.com"}}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var decoded PortScope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if decoded.PortMap[18789] != "app.example.com" {
		t.Errorf("round-trip PortMap[18789] = %q, want app.example.com", decoded.PortMap[18789])
	}
}

func TestPortScopeJSONNull(t *testing.T) {
	var ps PortScope
	data, err := json.Marshal(ps)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("zero PortScope Marshal = %s, want null", data)
	}
}

// --- TunnelYAML Bare String Defaults ---

func TestTunnelYAMLUnmarshalBareString(t *testing.T) {
	tests := []struct {
		input       string
		provider    string
		wantPublic  bool
		wantPrivate bool
	}{
		{"tailscale", "tailscale", false, true},  // tailscale default: all ports private
		{"cloudflare", "cloudflare", true, false}, // cloudflare default: all ports public
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			var tunnel TunnelYAML
			if err := yaml.Unmarshal([]byte(tt.input), &tunnel); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tt.input, err)
			}
			if tunnel.Provider != tt.provider {
				t.Errorf("Provider = %q, want %q", tunnel.Provider, tt.provider)
			}
			if tunnel.Public.All != tt.wantPublic {
				t.Errorf("Public.All = %v, want %v", tunnel.Public.All, tt.wantPublic)
			}
			if tunnel.Private.All != tt.wantPrivate {
				t.Errorf("Private.All = %v, want %v", tunnel.Private.All, tt.wantPrivate)
			}
		})
	}
}

// --- TunnelYAML Expanded Form ---

func TestTunnelYAMLUnmarshalExpanded(t *testing.T) {
	input := `
provider: cloudflare
tunnel: my-tunnel
public:
  18789: "app.example.com"
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", tunnel.Provider)
	}
	if tunnel.Tunnel != "my-tunnel" {
		t.Errorf("Tunnel = %q, want my-tunnel", tunnel.Tunnel)
	}
	if tunnel.Public.PortMap[18789] != "app.example.com" {
		t.Errorf("Public.PortMap[18789] = %q, want app.example.com", tunnel.Public.PortMap[18789])
	}
}

func TestTunnelYAMLUnmarshalTailscalePublicPrivate(t *testing.T) {
	input := `
provider: tailscale
public: [443]
private: all
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", tunnel.Provider)
	}
	if len(tunnel.Public.Ports) != 1 || tunnel.Public.Ports[0] != 443 {
		t.Errorf("Public.Ports = %v, want [443]", tunnel.Public.Ports)
	}
	if !tunnel.Private.All {
		t.Error("Private.All = false, want true")
	}
}

// --- TunnelYAML in ImageConfig ---

func TestTunnelYAMLInImageConfig(t *testing.T) {
	input := `
defaults:
  registry: ghcr.io/test
images:
  myapp:
    base: "fedora:43"
    tunnel: cloudflare
    dns: "app.example.com"
    layers: []
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	img := cfg.Images["myapp"]
	if img.Tunnel == nil {
		t.Fatal("expected Tunnel to be non-nil")
	}
	if img.Tunnel.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", img.Tunnel.Provider)
	}
	if !img.Tunnel.Public.All {
		t.Error("Public.All = false, want true (cloudflare bare string default)")
	}
}

func TestTunnelYAMLExpandedInImageConfig(t *testing.T) {
	input := `
images:
  myapp:
    base: "fedora:43"
    tunnel:
      provider: tailscale
      public: [443]
      private: [8080]
    layers: []
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	img := cfg.Images["myapp"]
	if img.Tunnel == nil {
		t.Fatal("expected Tunnel to be non-nil")
	}
	if img.Tunnel.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", img.Tunnel.Provider)
	}
	if len(img.Tunnel.Public.Ports) != 1 || img.Tunnel.Public.Ports[0] != 443 {
		t.Errorf("Public.Ports = %v, want [443]", img.Tunnel.Public.Ports)
	}
	if len(img.Tunnel.Private.Ports) != 1 || img.Tunnel.Private.Ports[0] != 8080 {
		t.Errorf("Private.Ports = %v, want [8080]", img.Tunnel.Private.Ports)
	}
}

// --- ValidPublicPorts ---

func TestValidPublicPorts(t *testing.T) {
	valid := []int{443, 8443, 10000}
	invalid := []int{80, 8080, 3000, 0, 65535}

	for _, p := range valid {
		if !ValidPublicPorts[p] {
			t.Errorf("port %d should be valid", p)
		}
	}
	for _, p := range invalid {
		if ValidPublicPorts[p] {
			t.Errorf("port %d should be invalid", p)
		}
	}
}

// --- ResolveTunnelConfig ---

func TestResolveTunnelConfigTailscalePrivateAll(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	imagePorts := []string{"8080:8080", "9090:9090"}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, nil, imagePorts)

	if cfg.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", cfg.Provider)
	}
	if cfg.ImageName != "myapp" {
		t.Errorf("ImageName = %q, want myapp", cfg.ImageName)
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(cfg.Ports))
	}
	for _, p := range cfg.Ports {
		if p.Public {
			t.Errorf("port %d should be private", p.Port)
		}
	}
}

func TestResolveTunnelConfigCloudflareDefaults(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "cloudflare", Public: PortScope{All: true}}
	imagePorts := []string{"3001:3001"}
	cfg := ResolveTunnelConfig(tunnel, "immich", "im.example.com", nil, nil, nil, imagePorts)

	if cfg.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", cfg.Provider)
	}
	if cfg.TunnelName != "ov-immich" {
		t.Errorf("TunnelName = %q, want ov-immich", cfg.TunnelName)
	}
	if cfg.Hostname != "im.example.com" {
		t.Errorf("Hostname = %q, want im.example.com", cfg.Hostname)
	}
	if len(cfg.Ports) != 1 || !cfg.Ports[0].Public {
		t.Errorf("Ports = %+v, want [{Port:3001 Public:true}]", cfg.Ports)
	}
}

func TestResolveTunnelConfigCloudflareCustomTunnel(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "cloudflare", Tunnel: "my-tunnel", Public: PortScope{Ports: []int{3001}}}
	imagePorts := []string{"3001:3001"}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "app.example.com", nil, nil, nil, imagePorts)

	if cfg.TunnelName != "my-tunnel" {
		t.Errorf("TunnelName = %q, want my-tunnel", cfg.TunnelName)
	}
	if len(cfg.Ports) != 1 || cfg.Ports[0].Port != 3001 {
		t.Errorf("Ports = %+v, want [{Port:3001}]", cfg.Ports)
	}
}

func TestResolveTunnelConfigNil(t *testing.T) {
	cfg := ResolveTunnelConfig(nil, "myapp", "", nil, nil, nil, nil)
	if cfg != nil {
		t.Error("expected nil for nil TunnelYAML")
	}
}

func TestResolveTunnelConfigPublicAndPrivate(t *testing.T) {
	tunnel := &TunnelYAML{
		Provider: "tailscale",
		Public:   PortScope{Ports: []int{443}},
		Private:  PortScope{All: true},
	}
	imagePorts := []string{"443:18789", "5900:5900", "9222:9222"}
	portProtos := map[int]string{5900: "tcp"}

	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, portProtos, imagePorts)

	if len(cfg.Ports) != 3 {
		t.Fatalf("got %d ports, want 3", len(cfg.Ports))
	}

	// Port 443 should be public
	if !cfg.Ports[0].Public || cfg.Ports[0].Port != 443 {
		t.Errorf("Ports[0] = %+v, want {Port:443 Public:true}", cfg.Ports[0])
	}
	// Port 5900 should be private, tcp
	if cfg.Ports[1].Public || cfg.Ports[1].Protocol != "tcp" {
		t.Errorf("Ports[1] = %+v, want {Port:5900 Private tcp}", cfg.Ports[1])
	}
	// Port 9222 should be private, http
	if cfg.Ports[2].Public || cfg.Ports[2].Protocol != "http" {
		t.Errorf("Ports[2] = %+v, want {Port:9222 Private http}", cfg.Ports[2])
	}
}

func TestResolveTunnelConfigCloudflarePortMap(t *testing.T) {
	tunnel := &TunnelYAML{
		Provider: "cloudflare",
		Tunnel:   "my-tunnel",
		Public:   PortScope{PortMap: map[int]string{18789: "app.example.com", 9222: "chrome.example.com"}},
	}
	imagePorts := []string{"18789:18789", "9222:9222"}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, nil, imagePorts)

	if len(cfg.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(cfg.Ports))
	}
	// Check that hostnames are carried through
	foundApp := false
	foundChrome := false
	for _, p := range cfg.Ports {
		if p.Port == 18789 && p.Hostname == "app.example.com" {
			foundApp = true
		}
		if p.Port == 9222 && p.Hostname == "chrome.example.com" {
			foundChrome = true
		}
	}
	if !foundApp {
		t.Error("missing port 18789 with hostname app.example.com")
	}
	if !foundChrome {
		t.Error("missing port 9222 with hostname chrome.example.com")
	}
}

// --- Config ResolveImage Tunnel ---

func TestConfigResolveTunnel(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				DNS:    "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Public: PortScope{Ports: []int{3001}}},
				Ports:  []string{"3001:3001"},
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage error: %v", err)
	}
	if resolved.Tunnel == nil {
		t.Fatal("expected Tunnel to be non-nil")
	}
	if resolved.Tunnel.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", resolved.Tunnel.Provider)
	}
	if len(resolved.Tunnel.Ports) != 1 || resolved.Tunnel.Ports[0].Port != 3001 {
		t.Errorf("Ports = %+v, want [{Port:3001}]", resolved.Tunnel.Ports)
	}
}

func TestConfigResolveTunnelFromDefaults(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Build:  BuildFormats{"rpm"},
			Tunnel: &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}},
		},
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				Ports:  []string{"8080:8080"},
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage error: %v", err)
	}
	if resolved.Tunnel == nil {
		t.Fatal("expected Tunnel to be inherited from defaults")
	}
	if resolved.Tunnel.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", resolved.Tunnel.Provider)
	}
}

func TestConfigResolveTunnelNil(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Build: BuildFormats{"rpm"}},
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test", "")
	if err != nil {
		t.Fatalf("ResolveImage error: %v", err)
	}
	if resolved.Tunnel != nil {
		t.Error("expected Tunnel to be nil when not configured")
	}
}

// --- Validation ---

func TestValidateTunnelInvalidProvider(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "wireguard", Private: PortScope{All: true}},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
	if !strings.Contains(err.Error(), "tunnel provider must be") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelMustSpecifyScope(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for tunnel with no public/private scope")
	}
	if !strings.Contains(err.Error(), "tunnel must specify public, private, or both") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelBothAllConflict(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{All: true}, Private: PortScope{All: true}},
				Ports:  []string{"443:443"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for both public: all and private: all")
	}
	if !strings.Contains(err.Error(), "cannot have both public: all and private: all") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelCloudflarePrivateError(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				DNS:    "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Private: PortScope{All: true}},
				Ports:  []string{"8080:8080"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for cloudflare with private ports")
	}
	if !strings.Contains(err.Error(), "cloudflare tunnels are always public") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelCloudflareMissingDNS(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "cloudflare", Public: PortScope{Ports: []int{8080}}},
				Ports:  []string{"8080:8080"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for missing dns")
	}
	if !strings.Contains(err.Error(), "requires dns") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelCloudflareInvalidTunnelName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				DNS:    "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Tunnel: "-bad-name", Public: PortScope{Ports: []int{8080}}},
				Ports:  []string{"8080:8080"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for invalid tunnel name")
	}
	if !strings.Contains(err.Error(), "tunnel name must match") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelValidTailscale(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{443}}, Private: PortScope{Ports: []int{8080}}},
				Ports:  []string{"443:443", "8080:8080"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	// Should have no tunnel errors (there may be other validation errors like missing layers)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "tunnel") {
			t.Errorf("unexpected tunnel error: %v", err)
		}
	}
}

func TestValidateTunnelValidCloudflare(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				DNS:    "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Public: PortScope{Ports: []int{3001}}},
				Ports:  []string{"3001:3001"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	// Should have no tunnel errors
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "tunnel") {
			t.Errorf("unexpected tunnel error: %v", err)
		}
	}
}

func TestValidateTunnelTailscaleInvalidPublicPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{8080}}},
				Ports:  []string{"8080:8080"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers, "")
	if err == nil {
		t.Fatal("expected error for invalid public port")
	}
	if !strings.Contains(err.Error(), "tailscale public port 8080 must be 443, 8443, or 10000") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- isValidServePort ---

func TestIsValidServePort(t *testing.T) {
	valid := []int{80, 443, 3000, 3001, 5000, 8080, 8443, 10000, 4443, 5432, 6443}
	invalid := []int{0, 79, 81, 442, 444, 2999, 10001, 65535}

	for _, p := range valid {
		if !isValidServePort(p) {
			t.Errorf("port %d should be valid for serve", p)
		}
	}
	for _, p := range invalid {
		if isValidServePort(p) {
			t.Errorf("port %d should be invalid for serve", p)
		}
	}
}

// --- Cross-Image Port Conflict ---

func TestValidateTunnelPublicPortConflict(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantErr   bool
		errSubstr string
	}{
		{
			name: "two tailscale images same public port",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"app-a": {
						Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{443}}},
						Ports:  []string{"443:443"},
						Layers: []string{},
					},
					"app-b": {
						Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{443}}},
						Ports:  []string{"443:443"},
						Layers: []string{},
					},
				},
			},
			wantErr:   true,
			errSubstr: "tailscale public port 443 used by multiple images",
		},
		{
			name: "two tailscale images different public ports",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"app-a": {
						Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{443}}},
						Ports:  []string{"443:443"},
						Layers: []string{},
					},
					"app-b": {
						Tunnel: &TunnelYAML{Provider: "tailscale", Public: PortScope{Ports: []int{8443}}},
						Ports:  []string{"8443:8443"},
						Layers: []string{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "tailscale and cloudflare no conflict",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"app-a": {
						Tunnel: &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}},
						Ports:  []string{"8080:8080"},
						Layers: []string{},
					},
					"app-b": {
						DNS:    "app.example.com",
						Tunnel: &TunnelYAML{Provider: "cloudflare", Public: PortScope{Ports: []int{3001}}},
						Ports:  []string{"3001:3001"},
						Layers: []string{},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layers := map[string]*Layer{}
			err := Validate(tt.cfg, layers, "")
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected validation error")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					errStr := err.Error()
					if strings.Contains(errStr, "tunnel") && strings.Contains(errStr, "conflict") {
						t.Errorf("unexpected tunnel conflict error: %v", err)
					}
				}
			}
		})
	}
}

// --- PortSpec ---

func TestPortSpecUnmarshalInt(t *testing.T) {
	input := `
- 8080
- 9090
`
	var specs []PortSpec
	if err := yaml.Unmarshal([]byte(input), &specs); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].Port != 8080 || specs[0].Protocol != "http" {
		t.Errorf("specs[0] = %+v, want {Port:8080 Protocol:http}", specs[0])
	}
	if specs[1].Port != 9090 || specs[1].Protocol != "http" {
		t.Errorf("specs[1] = %+v, want {Port:9090 Protocol:http}", specs[1])
	}
}

func TestPortSpecUnmarshalTCP(t *testing.T) {
	input := `
- tcp:5900
`
	var specs []PortSpec
	if err := yaml.Unmarshal([]byte(input), &specs); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Port != 5900 || specs[0].Protocol != "tcp" {
		t.Errorf("specs[0] = %+v, want {Port:5900 Protocol:tcp}", specs[0])
	}
}

func TestPortSpecUnmarshalStringNumber(t *testing.T) {
	input := `
- "5432"
`
	var specs []PortSpec
	if err := yaml.Unmarshal([]byte(input), &specs); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if specs[0].Port != 5432 || specs[0].Protocol != "http" {
		t.Errorf("specs[0] = %+v, want {Port:5432 Protocol:http}", specs[0])
	}
}

func TestPortSpecMixed(t *testing.T) {
	input := `
- 18789
- tcp:5900
- 9222
`
	var specs []PortSpec
	if err := yaml.Unmarshal([]byte(input), &specs); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	want := []PortSpec{
		{Port: 18789, Protocol: "http"},
		{Port: 5900, Protocol: "tcp"},
		{Port: 9222, Protocol: "http"},
	}
	if !reflect.DeepEqual(specs, want) {
		t.Errorf("got %+v, want %+v", specs, want)
	}
}

// --- ResolveTunnelConfig with Protocols ---

func TestResolveTunnelConfigWithProtocols(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	portProtos := map[int]string{5900: "tcp"}
	imagePorts := []string{"18789:18789", "5900:5900", "9222:9222"}

	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, portProtos, imagePorts)

	if len(cfg.Ports) != 3 {
		t.Fatalf("got %d ports, want 3", len(cfg.Ports))
	}

	want := []TunnelPort{
		{Port: 18789, BackendPort: 18789, Protocol: "http", Public: false},
		{Port: 5900, BackendPort: 5900, Protocol: "tcp", Public: false},
		{Port: 9222, BackendPort: 9222, Protocol: "http", Public: false},
	}
	if !reflect.DeepEqual(cfg.Ports, want) {
		t.Errorf("Ports = %+v, want %+v", cfg.Ports, want)
	}
}

// --- TunnelConfigFromMetadata ---

func TestTunnelConfigFromMetadataPublicPrivate(t *testing.T) {
	meta := &ImageMetadata{
		Image: "test-app",
		Tunnel: &TunnelYAML{
			Provider: "tailscale",
			Public:   PortScope{Ports: []int{443}},
			Private:  PortScope{All: true},
		},
		Ports:      []string{"443:18789", "5900:5900"},
		PortProtos: map[int]string{5900: "tcp"},
	}

	cfg := TunnelConfigFromMetadata(meta)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(cfg.Ports))
	}
	// Port 443 should be public
	if !cfg.Ports[0].Public || cfg.Ports[0].Port != 443 {
		t.Errorf("Ports[0] = %+v, want {Port:443 Public:true}", cfg.Ports[0])
	}
	// Port 5900 should be private, tcp
	if cfg.Ports[1].Public || cfg.Ports[1].Protocol != "tcp" {
		t.Errorf("Ports[1] = %+v, want {Port:5900 Private tcp}", cfg.Ports[1])
	}
}

// --- collectPortProtos ---

func TestResolveTunnelConfigPreservesPort(t *testing.T) {
	// Port 2283 is preserved as-is for Tailscale serve (tailnet-only).
	// The isValidServePort restriction only applies to funnel (public).
	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	imagePorts := []string{"2283:2283"}
	cfg := ResolveTunnelConfig(tunnel, "immich-ml", "", nil, nil, nil, imagePorts)

	if len(cfg.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(cfg.Ports))
	}
	tp := cfg.Ports[0]
	if tp.Port != 2283 {
		t.Errorf("Port = %d, want 2283 (no remap for serve)", tp.Port)
	}
	if tp.BackendPort != 2283 {
		t.Errorf("BackendPort = %d, want 2283", tp.BackendPort)
	}
	if tp.backend() != 2283 {
		t.Errorf("backend() = %d, want 2283", tp.backend())
	}
}

func TestTunnelPortBackendDefault(t *testing.T) {
	// When BackendPort is 0, backend() should return Port.
	tp := TunnelPort{Port: 443}
	if tp.backend() != 443 {
		t.Errorf("backend() = %d, want 443 (default to Port when BackendPort=0)", tp.backend())
	}
	// When BackendPort is set, backend() should return BackendPort.
	tp = TunnelPort{Port: 443, BackendPort: 2283}
	if tp.backend() != 2283 {
		t.Errorf("backend() = %d, want 2283", tp.backend())
	}
}

func TestCollectPortProtos(t *testing.T) {
	layers := map[string]*Layer{
		"wayvnc": {
			Name:      "wayvnc",
			HasPorts:  true,
			ports:     []string{"5900"},
			portSpecs: []PortSpec{{Port: 5900, Protocol: "tcp"}},
		},
		"openclaw": {
			Name:      "openclaw",
			HasPorts:  true,
			ports:     []string{"18789"},
			portSpecs: []PortSpec{{Port: 18789, Protocol: "http"}},
		},
	}
	layerNames := []string{"openclaw", "wayvnc"}

	protos := collectPortProtos(layers, layerNames)
	if len(protos) != 1 {
		t.Fatalf("got %d protos, want 1 (only non-http)", len(protos))
	}
	if protos[5900] != "tcp" {
		t.Errorf("protos[5900] = %q, want tcp", protos[5900])
	}
}

// --- Backend scheme helpers ---

func TestSchemeTarget(t *testing.T) {
	tests := []struct {
		scheme string
		port   int
		want   string
	}{
		{"http", 3000, "http://127.0.0.1:3000"},
		{"https", 8443, "https://127.0.0.1:8443"},
		{"https+insecure", 3000, "https+insecure://127.0.0.1:3000"},
		{"tcp", 5900, "tcp://127.0.0.1:5900"},
		{"tls-terminated-tcp", 22, "tcp://127.0.0.1:22"},
		{"ssh", 22, "ssh://127.0.0.1:22"},
		{"rdp", 3389, "rdp://127.0.0.1:3389"},
	}
	for _, tt := range tests {
		got := schemeTarget(tt.scheme, tt.port)
		if got != tt.want {
			t.Errorf("schemeTarget(%q, %d) = %q, want %q", tt.scheme, tt.port, got, tt.want)
		}
	}
}

func TestTailscaleFlag(t *testing.T) {
	tests := []struct {
		scheme string
		want   string
	}{
		{"http", "--https"},
		{"https", "--https"},
		{"https+insecure", "--https"},
		{"tcp", "--tcp"},
		{"tls-terminated-tcp", "--tls-terminated-tcp"},
	}
	for _, tt := range tests {
		got := tailscaleFlag(tt.scheme)
		if got != tt.want {
			t.Errorf("tailscaleFlag(%q) = %q, want %q", tt.scheme, got, tt.want)
		}
	}
}

func TestIsTCPFamily(t *testing.T) {
	if !isTCPFamily("tcp") {
		t.Error("tcp should be TCP family")
	}
	if !isTCPFamily("tls-terminated-tcp") {
		t.Error("tls-terminated-tcp should be TCP family")
	}
	if isTCPFamily("http") {
		t.Error("http should not be TCP family")
	}
	if isTCPFamily("https") {
		t.Error("https should not be TCP family")
	}
	if isTCPFamily("https+insecure") {
		t.Error("https+insecure should not be TCP family")
	}
	if isTCPFamily("ssh") {
		t.Error("ssh should not be TCP family")
	}
}

func TestResolveTunnelConfigWithHTTPSInsecure(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	portProtos := map[int]string{3000: "https+insecure"}
	imagePorts := []string{"3000:3000", "8888:8888"}

	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, portProtos, imagePorts)

	if len(cfg.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(cfg.Ports))
	}
	if cfg.Ports[0].Protocol != "https+insecure" {
		t.Errorf("Port 3000 protocol = %q, want https+insecure", cfg.Ports[0].Protocol)
	}
	if cfg.Ports[1].Protocol != "http" {
		t.Errorf("Port 8888 protocol = %q, want http", cfg.Ports[1].Protocol)
	}
}

func TestResolveTunnelConfigWithTLSTerminatedTCP(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	portProtos := map[int]string{22: "tls-terminated-tcp"}
	imagePorts := []string{"22:22"}

	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, portProtos, imagePorts)

	if len(cfg.Ports) != 1 {
		t.Fatalf("got %d ports, want 1", len(cfg.Ports))
	}
	if cfg.Ports[0].Protocol != "tls-terminated-tcp" {
		t.Errorf("Port 22 protocol = %q, want tls-terminated-tcp", cfg.Ports[0].Protocol)
	}
}

func TestResolveTunnelConfigCloudflareSchemes(t *testing.T) {
	tunnel := &TunnelYAML{
		Provider: "cloudflare",
		Tunnel:   "test-tunnel",
		Public:   PortScope{PortMap: map[int]string{443: "app.example.com", 22: "ssh.example.com"}},
	}
	portProtos := map[int]string{22: "ssh"}
	imagePorts := []string{"443:443", "22:22"}

	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil, portProtos, imagePorts)

	if len(cfg.Ports) != 2 {
		t.Fatalf("got %d ports, want 2", len(cfg.Ports))
	}
	if cfg.Ports[0].Protocol != "http" {
		t.Errorf("Port 443 protocol = %q, want http", cfg.Ports[0].Protocol)
	}
	if cfg.Ports[1].Protocol != "ssh" {
		t.Errorf("Port 22 protocol = %q, want ssh", cfg.Ports[1].Protocol)
	}
}
