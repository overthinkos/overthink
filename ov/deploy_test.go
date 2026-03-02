package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadDeployConfigMissing(t *testing.T) {
	// Override path to nonexistent file
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig() error = %v", err)
	}
	if dc != nil {
		t.Errorf("expected nil for missing file, got %+v", dc)
	}
}

func TestLoadDeployConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	content := `
images:
  myapp:
    fqdn: "app.example.com"
    acme_email: "test@example.com"
    tunnel:
      provider: cloudflare
      port: 8080
    bind_mounts:
      - name: data
        path: "~/.myapp"
        encrypted: true
    ports:
      - "8080:8080"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) { return path, nil }
	defer func() { DeployConfigPath = orig }()

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig() error = %v", err)
	}
	if dc == nil {
		t.Fatal("expected non-nil config")
	}

	img, ok := dc.Images["myapp"]
	if !ok {
		t.Fatal("expected myapp in deploy config")
	}
	if img.FQDN != "app.example.com" {
		t.Errorf("FQDN = %q, want app.example.com", img.FQDN)
	}
	if img.AcmeEmail != "test@example.com" {
		t.Errorf("AcmeEmail = %q, want test@example.com", img.AcmeEmail)
	}
	if img.Tunnel == nil || img.Tunnel.Provider != "cloudflare" {
		t.Errorf("Tunnel = %+v, want cloudflare provider", img.Tunnel)
	}
	if len(img.BindMounts) != 1 || img.BindMounts[0].Name != "data" {
		t.Errorf("BindMounts = %+v, want 1 mount named data", img.BindMounts)
	}
	if len(img.Ports) != 1 || img.Ports[0] != "8080:8080" {
		t.Errorf("Ports = %v, want [8080:8080]", img.Ports)
	}
}

func TestMergeDeployOverlay(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{"svc"},
				FQDN:   "old.example.com",
				Ports:  []string{"80:80"},
			},
		},
	}
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"myapp": {
				FQDN:      "new.example.com",
				AcmeEmail: "admin@example.com",
				Ports:     []string{"8080:8080"},
			},
		},
	}

	MergeDeployOverlay(cfg, dc)

	img := cfg.Images["myapp"]
	if img.FQDN != "new.example.com" {
		t.Errorf("FQDN = %q, want new.example.com", img.FQDN)
	}
	if img.AcmeEmail != "admin@example.com" {
		t.Errorf("AcmeEmail = %q, want admin@example.com", img.AcmeEmail)
	}
	if !reflect.DeepEqual(img.Ports, []string{"8080:8080"}) {
		t.Errorf("Ports = %v, want [8080:8080]", img.Ports)
	}
	// Layers should be untouched
	if !reflect.DeepEqual(img.Layers, []string{"svc"}) {
		t.Errorf("Layers = %v, should be unchanged", img.Layers)
	}
}

func TestMergeDeployOverlayUnknownImage(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {Layers: []string{"svc"}},
		},
	}
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"unknown": {FQDN: "test.example.com"},
		},
	}

	// Should not panic
	MergeDeployOverlay(cfg, dc)

	// Original config should be unchanged
	if _, ok := cfg.Images["unknown"]; ok {
		t.Error("unknown image should not be added to config")
	}
}

func TestMergeDeployOverlayNil(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {Layers: []string{"svc"}, FQDN: "original.com"},
		},
	}

	MergeDeployOverlay(cfg, nil)

	if cfg.Images["myapp"].FQDN != "original.com" {
		t.Error("nil deploy config should not modify config")
	}
}

func TestMergeDeployOverlayTunnel(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{"svc"},
				Tunnel: &TunnelYAML{Provider: "tailscale"},
			},
		},
	}
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"myapp": {
				Tunnel: &TunnelYAML{Provider: "cloudflare", Port: 8080},
			},
		},
	}

	MergeDeployOverlay(cfg, dc)

	img := cfg.Images["myapp"]
	if img.Tunnel == nil || img.Tunnel.Provider != "cloudflare" || img.Tunnel.Port != 8080 {
		t.Errorf("Tunnel = %+v, want cloudflare:8080", img.Tunnel)
	}
}

func TestMergeDeployOverlayBindMounts(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {Layers: []string{"svc"}},
		},
	}
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"myapp": {
				BindMounts: []BindMountConfig{
					{Name: "data", Path: "~/.myapp", Encrypted: true},
				},
			},
		},
	}

	MergeDeployOverlay(cfg, dc)

	img := cfg.Images["myapp"]
	if len(img.BindMounts) != 1 || img.BindMounts[0].Name != "data" {
		t.Errorf("BindMounts = %+v, want 1 mount named data", img.BindMounts)
	}
}

func TestBindMountNames(t *testing.T) {
	tests := []struct {
		name   string
		mounts []BindMountConfig
		want   map[string]bool
	}{
		{"nil", nil, nil},
		{"empty", []BindMountConfig{}, nil},
		{"one", []BindMountConfig{{Name: "data"}}, map[string]bool{"data": true}},
		{"two", []BindMountConfig{{Name: "data"}, {Name: "cache"}}, map[string]bool{"data": true, "cache": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BindMountNames(tt.mounts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("BindMountNames() = %v, want %v", got, tt.want)
			}
		})
	}
}
