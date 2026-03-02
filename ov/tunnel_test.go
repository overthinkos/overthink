package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTunnelYAMLUnmarshalBareString(t *testing.T) {
	tests := []struct {
		input    string
		provider string
	}{
		{"tailscale", "tailscale"},
		{"cloudflare", "cloudflare"},
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
			if tunnel.Port != 0 {
				t.Errorf("Port = %d, want 0", tunnel.Port)
			}
		})
	}
}

func TestTunnelYAMLUnmarshalExpanded(t *testing.T) {
	input := `
provider: cloudflare
port: 3001
tunnel: my-tunnel
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", tunnel.Provider)
	}
	if tunnel.Port != 3001 {
		t.Errorf("Port = %d, want 3001", tunnel.Port)
	}
	if tunnel.Tunnel != "my-tunnel" {
		t.Errorf("Tunnel = %q, want my-tunnel", tunnel.Tunnel)
	}
}

func TestTunnelYAMLUnmarshalTailscale(t *testing.T) {
	input := `
provider: tailscale
https: 8443
port: 9090
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", tunnel.Provider)
	}
	if tunnel.HTTPS != 8443 {
		t.Errorf("HTTPS = %d, want 8443", tunnel.HTTPS)
	}
	if tunnel.Port != 9090 {
		t.Errorf("Port = %d, want 9090", tunnel.Port)
	}
}

func TestTunnelYAMLInImageConfig(t *testing.T) {
	input := `
defaults:
  registry: ghcr.io/test
images:
  myapp:
    base: "fedora:43"
    tunnel: cloudflare
    fqdn: "app.example.com"
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
}

func TestTunnelYAMLExpandedInImageConfig(t *testing.T) {
	input := `
images:
  myapp:
    base: "fedora:43"
    tunnel:
      provider: tailscale
      port: 8080
      https: 10000
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
	if img.Tunnel.Port != 8080 {
		t.Errorf("Port = %d, want 8080", img.Tunnel.Port)
	}
	if img.Tunnel.HTTPS != 10000 {
		t.Errorf("HTTPS = %d, want 10000", img.Tunnel.HTTPS)
	}
}

func TestValidFunnelPorts(t *testing.T) {
	valid := []int{443, 8443, 10000}
	invalid := []int{80, 8080, 3000, 0, 65535}

	for _, p := range valid {
		if !ValidFunnelPorts[p] {
			t.Errorf("port %d should be valid", p)
		}
	}
	for _, p := range invalid {
		if ValidFunnelPorts[p] {
			t.Errorf("port %d should be invalid", p)
		}
	}
}

func TestResolveTunnelConfigTailscaleDefaults(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale"}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil)

	if cfg.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", cfg.Provider)
	}
	if cfg.HTTPS != 443 {
		t.Errorf("HTTPS = %d, want 443 (default)", cfg.HTTPS)
	}
	if cfg.ImageName != "myapp" {
		t.Errorf("ImageName = %q, want myapp", cfg.ImageName)
	}
}

func TestResolveTunnelConfigCloudflareDefaults(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "cloudflare"}
	cfg := ResolveTunnelConfig(tunnel, "immich", "im.example.com", nil, nil)

	if cfg.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", cfg.Provider)
	}
	if cfg.TunnelName != "ov-immich" {
		t.Errorf("TunnelName = %q, want ov-immich", cfg.TunnelName)
	}
	if cfg.Hostname != "im.example.com" {
		t.Errorf("Hostname = %q, want im.example.com", cfg.Hostname)
	}
}

func TestResolveTunnelConfigCloudflareCustomTunnel(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "cloudflare", Tunnel: "my-tunnel", Port: 3001}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "app.example.com", nil, nil)

	if cfg.TunnelName != "my-tunnel" {
		t.Errorf("TunnelName = %q, want my-tunnel", cfg.TunnelName)
	}
	if cfg.Port != 3001 {
		t.Errorf("Port = %d, want 3001", cfg.Port)
	}
}

func TestResolveTunnelConfigNil(t *testing.T) {
	cfg := ResolveTunnelConfig(nil, "myapp", "", nil, nil)
	if cfg != nil {
		t.Error("expected nil for nil TunnelYAML")
	}
}

func TestResolveTunnelConfigPortFromRoute(t *testing.T) {
	layers := map[string]*Layer{
		"traefik": {Name: "traefik", HasRoute: false},
		"immich": {
			Name:     "immich",
			HasRoute: true,
			route: &RouteConfig{
				Host: "immich.localhost",
				Port: "3001",
			},
		},
	}
	layerNames := []string{"traefik", "immich"}

	tunnel := &TunnelYAML{Provider: "cloudflare"}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "app.example.com", layers, layerNames)

	if cfg.Port != 3001 {
		t.Errorf("Port = %d, want 3001 (from route)", cfg.Port)
	}
}

func TestConfigResolveTunnel(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{},
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				FQDN:   "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Port: 3001},
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test")
	if err != nil {
		t.Fatalf("ResolveImage error: %v", err)
	}
	if resolved.Tunnel == nil {
		t.Fatal("expected Tunnel to be non-nil")
	}
	if resolved.Tunnel.Provider != "cloudflare" {
		t.Errorf("Provider = %q, want cloudflare", resolved.Tunnel.Provider)
	}
	if resolved.Tunnel.Port != 3001 {
		t.Errorf("Port = %d, want 3001", resolved.Tunnel.Port)
	}
}

func TestConfigResolveTunnelFromDefaults(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Tunnel: &TunnelYAML{Provider: "tailscale"},
		},
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test")
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
		Images: map[string]ImageConfig{
			"myapp": {
				Base:   "fedora:43",
				Layers: []string{},
			},
		},
	}

	resolved, err := cfg.ResolveImage("myapp", "test")
	if err != nil {
		t.Fatalf("ResolveImage error: %v", err)
	}
	if resolved.Tunnel != nil {
		t.Error("expected Tunnel to be nil when not configured")
	}
}

func TestValidateTunnelInvalidProvider(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "wireguard"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
	if !strings.Contains(err.Error(), "tunnel provider must be") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelInvalidFunnelPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", Funnel: true, HTTPS: 8080},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for invalid funnel port")
	}
	if !strings.Contains(err.Error(), "tunnel https must be 443, 8443, or 10000 for funnel") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelCloudflareMissingFQDN(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "cloudflare"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for missing fqdn")
	}
	if !strings.Contains(err.Error(), "requires fqdn") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelCloudflareInvalidTunnelName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				FQDN:   "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Tunnel: "-bad-name"},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
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
				Tunnel: &TunnelYAML{Provider: "tailscale", HTTPS: 443, Port: 8080},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
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
				FQDN:   "app.example.com",
				Tunnel: &TunnelYAML{Provider: "cloudflare", Port: 3001},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	// Should have no tunnel errors
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "tunnel") {
			t.Errorf("unexpected tunnel error: %v", err)
		}
	}
}

func TestValidateTunnelInvalidPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", Port: 70000},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(err.Error(), "tunnel port must be 1-65535") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTunnelYAMLUnmarshalFunnel(t *testing.T) {
	input := `
provider: tailscale
funnel: true
port: 8080
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Provider != "tailscale" {
		t.Errorf("Provider = %q, want tailscale", tunnel.Provider)
	}
	if !tunnel.Funnel {
		t.Error("Funnel = false, want true")
	}
	if tunnel.Port != 8080 {
		t.Errorf("Port = %d, want 8080", tunnel.Port)
	}
}

func TestTunnelYAMLUnmarshalServeDefault(t *testing.T) {
	// When funnel is not set, it defaults to false (serve mode)
	input := `
provider: tailscale
port: 2283
`
	var tunnel TunnelYAML
	if err := yaml.Unmarshal([]byte(input), &tunnel); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if tunnel.Funnel {
		t.Error("Funnel = true, want false (serve is default)")
	}
}

func TestResolveTunnelConfigFunnelFlag(t *testing.T) {
	tunnel := &TunnelYAML{Provider: "tailscale", Funnel: true}
	cfg := ResolveTunnelConfig(tunnel, "myapp", "", nil, nil)

	if !cfg.Funnel {
		t.Error("Funnel = false, want true")
	}

	// Serve mode (default)
	tunnel2 := &TunnelYAML{Provider: "tailscale"}
	cfg2 := ResolveTunnelConfig(tunnel2, "myapp", "", nil, nil)

	if cfg2.Funnel {
		t.Error("Funnel = true, want false (serve is default)")
	}
}

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

func TestValidateTunnelServeInvalidPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				// Serve mode (funnel: false, the default) with port outside allowed range
				Tunnel: &TunnelYAML{Provider: "tailscale", HTTPS: 2000},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for invalid serve port")
	}
	if !strings.Contains(err.Error(), "for serve") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTunnelServeValidPort(t *testing.T) {
	// Port 8080 is valid for serve (3000-10000 range) but invalid for funnel
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "tailscale", HTTPS: 8080},
				Layers: []string{},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	// Should have no tunnel errors
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "tunnel") {
			t.Errorf("unexpected tunnel error: %v", err)
		}
	}
}

func TestValidateTunnelHTTPSPortConflict(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantErr   bool
		errSubstr string
	}{
		{
			name: "two tailscale images default port 443",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"app-a": {
						Tunnel: &TunnelYAML{Provider: "tailscale"},
						Layers: []string{},
					},
					"app-b": {
						Tunnel: &TunnelYAML{Provider: "tailscale"},
						Layers: []string{},
					},
				},
			},
			wantErr:   true,
			errSubstr: "both use tailscale tunnel https port 443",
		},
		{
			name: "two tailscale images different https ports",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"app-a": {
						Tunnel: &TunnelYAML{Provider: "tailscale", HTTPS: 443},
						Layers: []string{},
					},
					"app-b": {
						Tunnel: &TunnelYAML{Provider: "tailscale", HTTPS: 8443},
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
						Tunnel: &TunnelYAML{Provider: "tailscale"},
						Layers: []string{},
					},
					"app-b": {
						FQDN:   "app.example.com",
						Tunnel: &TunnelYAML{Provider: "cloudflare"},
						Layers: []string{},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "inherited tailscale from defaults conflicts",
			cfg: &Config{
				Defaults: ImageConfig{
					Tunnel: &TunnelYAML{Provider: "tailscale"},
				},
				Images: map[string]ImageConfig{
					"app-a": {
						Layers: []string{},
					},
					"app-b": {
						Layers: []string{},
					},
				},
			},
			wantErr:   true,
			errSubstr: "both use tailscale tunnel https port 443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layers := map[string]*Layer{}
			err := Validate(tt.cfg, layers)
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
					if strings.Contains(errStr, "tunnel https port") {
						t.Errorf("unexpected tunnel port conflict error: %v", err)
					}
				}
			}
		})
	}
}
