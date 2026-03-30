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
    dns: "app.example.com"
    acme_email: "test@example.com"
    tunnel:
      provider: cloudflare
      public: [8080]
    volumes:
      - name: data
        type: encrypted
        path: "~/.myapp"
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
	if img.DNS != "app.example.com" {
		t.Errorf("DNS = %q, want app.example.com", img.DNS)
	}
	if img.AcmeEmail != "test@example.com" {
		t.Errorf("AcmeEmail = %q, want test@example.com", img.AcmeEmail)
	}
	if img.Tunnel == nil || img.Tunnel.Provider != "cloudflare" {
		t.Errorf("Tunnel = %+v, want cloudflare provider", img.Tunnel)
	}
	if len(img.Volumes) != 1 || img.Volumes[0].Name != "data" {
		t.Errorf("Volumes = %+v, want 1 volume named data", img.Volumes)
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
				DNS:   "old.example.com",
				Ports:  []string{"80:80"},
			},
		},
	}
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"myapp": {
				DNS:      "new.example.com",
				AcmeEmail: "admin@example.com",
				Ports:     []string{"8080:8080"},
			},
		},
	}

	MergeDeployOverlay(cfg, dc)

	img := cfg.Images["myapp"]
	if img.DNS != "new.example.com" {
		t.Errorf("DNS = %q, want new.example.com", img.DNS)
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
			"unknown": {DNS: "test.example.com"},
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
			"myapp": {Layers: []string{"svc"}, DNS: "original.com"},
		},
	}

	MergeDeployOverlay(cfg, nil)

	if cfg.Images["myapp"].DNS != "original.com" {
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
				Tunnel: &TunnelYAML{Provider: "cloudflare", Public: PortScope{Ports: []int{8080}}},
			},
		},
	}

	MergeDeployOverlay(cfg, dc)

	img := cfg.Images["myapp"]
	if img.Tunnel == nil || img.Tunnel.Provider != "cloudflare" {
		t.Errorf("Tunnel = %+v, want cloudflare provider", img.Tunnel)
	}
	if img.Tunnel != nil && (len(img.Tunnel.Public.Ports) != 1 || img.Tunnel.Public.Ports[0] != 8080) {
		t.Errorf("Tunnel.Public.Ports = %v, want [8080]", img.Tunnel.Public.Ports)
	}
}

func TestResolveVolumeBacking(t *testing.T) {
	labelVolumes := []VolumeMount{
		{VolumeName: "ov-myapp-data", ContainerPath: "/home/user/.myapp"},
		{VolumeName: "ov-myapp-cache", ContainerPath: "/home/user/.myapp/cache"},
	}
	deployVolumes := []DeployVolumeConfig{
		{Name: "data", Type: "bind", Host: "/mnt/nas/data"},
	}

	volumes, binds := ResolveVolumeBacking("myapp", labelVolumes, deployVolumes, "/home/user", "/enc", "/vol")

	// "cache" should remain a named volume
	if len(volumes) != 1 || volumes[0].VolumeName != "ov-myapp-cache" {
		t.Errorf("volumes = %+v, want 1 named volume for cache", volumes)
	}
	// "data" should be a bind mount
	if len(binds) != 1 || binds[0].Name != "data" || binds[0].HostPath != "/mnt/nas/data" {
		t.Errorf("binds = %+v, want 1 bind mount for data with host /mnt/nas/data", binds)
	}
}

func TestResolveVolumeBackingAutoPath(t *testing.T) {
	labelVolumes := []VolumeMount{
		{VolumeName: "ov-myapp-data", ContainerPath: "/home/user/.myapp"},
	}
	deployVolumes := []DeployVolumeConfig{
		{Name: "data", Type: "bind"}, // no host path → auto
	}

	_, binds := ResolveVolumeBacking("myapp", labelVolumes, deployVolumes, "/home/user", "/enc", "/vol")

	if len(binds) != 1 || binds[0].HostPath != "/vol/myapp/data" {
		t.Errorf("binds = %+v, want auto path /vol/myapp/data", binds)
	}
}

func TestResolveVolumeBackingEncrypted(t *testing.T) {
	labelVolumes := []VolumeMount{
		{VolumeName: "ov-myapp-secrets", ContainerPath: "/home/user/.secrets"},
	}
	deployVolumes := []DeployVolumeConfig{
		{Name: "secrets", Type: "encrypted"},
	}

	volumes, binds := ResolveVolumeBacking("myapp", labelVolumes, deployVolumes, "/home/user", "/enc", "/vol")

	if len(volumes) != 0 {
		t.Errorf("volumes = %+v, want 0 named volumes", volumes)
	}
	if len(binds) != 1 || !binds[0].Encrypted {
		t.Errorf("binds = %+v, want 1 encrypted bind mount", binds)
	}
	if binds[0].HostPath != "/enc/ov-myapp-secrets/plain" {
		t.Errorf("HostPath = %q, want /enc/ov-myapp-secrets/plain", binds[0].HostPath)
	}
}
