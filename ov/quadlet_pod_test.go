package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeneratePodQuadlet(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:   "my-app",
		Ports:       []string{"443:18789", "8080:8080"},
		BindAddress: "127.0.0.1",
		Network:     "ov",
	}

	content := generatePodQuadlet(cfg)

	// Check pod name
	if !strings.Contains(content, "PodName=ov-my-app") {
		t.Error("missing PodName")
	}

	// Dual networking: pod stays on bridge network
	if !strings.Contains(content, "Network=ov") {
		t.Error("missing Network=ov (dual networking: pod must stay on bridge)")
	}

	// Check port mappings moved to pod
	if !strings.Contains(content, "PodmanArgs=-p 127.0.0.1:443:18789") {
		t.Error("missing port mapping 443:18789")
	}
	if !strings.Contains(content, "PodmanArgs=-p 127.0.0.1:8080:8080") {
		t.Error("missing port mapping 8080:8080")
	}

	// Check install section
	if !strings.Contains(content, "WantedBy=default.target") {
		t.Error("missing WantedBy")
	}
}

func TestGeneratePodQuadlet_Instance(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "my-app",
		Instance:  "staging",
		Ports:     []string{"443:18789"},
	}

	content := generatePodQuadlet(cfg)
	if !strings.Contains(content, "PodName=ov-my-app-staging") {
		t.Error("missing instance in PodName")
	}
}

func TestGeneratePodQuadlet_ShmSize(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "my-app",
		Network:   "ov",
		Security:  SecurityConfig{ShmSize: "1g"},
	}
	content := generatePodQuadlet(cfg)
	if !strings.Contains(content, "PodmanArgs=--shm-size=1g") {
		t.Error("pod must have --shm-size when ShmSize is set (infra container owns /dev/shm)")
	}
}

func TestGenerateQuadlet_PodMode_NoShmSize(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:  "my-app",
		ImageRef:   "ghcr.io/overthinkos/my-app:latest",
		Home:       "/home/user",
		PodName:    "ov-my-app",
		Security:   SecurityConfig{ShmSize: "1g"},
		Entrypoint: []string{"sleep", "infinity"},
	}
	content := generateQuadlet(cfg)
	if strings.Contains(content, "ShmSize=") {
		t.Error("ShmSize must NOT be on container in pod mode (infra container owns /dev/shm)")
	}
}

func TestGenerateSidecarQuadlet(t *testing.T) {
	sc := ResolvedSidecar{
		Name:  "tailscale",
		Image: "ghcr.io/tailscale/tailscale:latest",
		Env: map[string]string{
			"TS_STATE_DIR":  "/var/lib/tailscale",
			"TS_USERSPACE":  "false",
			"TS_ACCEPT_DNS": "true",
		},
		Volumes: []VolumeMount{
			{VolumeName: "ov-my-app-tailscale-state", ContainerPath: "/var/lib/tailscale"},
		},
		Secrets: []CollectedSecret{
			{Name: "ov-my-app-tailscale-ts-authkey", Env: "TS_AUTHKEY"},
		},
		Security: SecurityConfig{
			CapAdd:  []string{"NET_ADMIN", "SYS_MODULE"},
			Devices: []string{"/dev/net/tun:/dev/net/tun"},
		},
	}

	content := generateSidecarQuadlet(sc, "ov-my-app")

	// Check pod reference
	if !strings.Contains(content, "Pod=ov-my-app.pod") {
		t.Error("missing Pod directive")
	}

	// Check container name
	if !strings.Contains(content, "ContainerName=ov-my-app-tailscale") {
		t.Error("missing ContainerName")
	}

	// Check image
	if !strings.Contains(content, "Image=ghcr.io/tailscale/tailscale:latest") {
		t.Error("missing Image")
	}

	// Check volume
	if !strings.Contains(content, "Volume=ov-my-app-tailscale-state:/var/lib/tailscale") {
		t.Error("missing volume")
	}

	// Check env vars (sorted)
	if !strings.Contains(content, "Environment=TS_ACCEPT_DNS=true") {
		t.Error("missing TS_ACCEPT_DNS")
	}
	if !strings.Contains(content, "Environment=TS_STATE_DIR=/var/lib/tailscale") {
		t.Error("missing TS_STATE_DIR")
	}
	if !strings.Contains(content, "Environment=TS_USERSPACE=false") {
		t.Error("missing TS_USERSPACE")
	}

	// Check secret
	if !strings.Contains(content, "Secret=ov-my-app-tailscale-ts-authkey,type=env,target=TS_AUTHKEY") {
		t.Error("missing secret")
	}

	// Check capabilities
	if !strings.Contains(content, "AddCapability=NET_ADMIN") {
		t.Error("missing NET_ADMIN capability")
	}
	if !strings.Contains(content, "AddCapability=SYS_MODULE") {
		t.Error("missing SYS_MODULE capability")
	}

	// Check device
	if !strings.Contains(content, "AddDevice=/dev/net/tun:/dev/net/tun") {
		t.Error("missing tun device")
	}

	// No TS_SERVE_CONFIG when tunnel is nil
	if strings.Contains(content, "TS_SERVE_CONFIG") {
		t.Error("should not have TS_SERVE_CONFIG without tunnel")
	}
}

func TestGenerateSidecarQuadlet_NoAutoServeConfig(t *testing.T) {
	// Sidecar should NOT auto-inject TS_SERVE_CONFIG even when tunnel is configured.
	// The host tunnel handles port exposure; the sidecar handles routing only.
	sc := ResolvedSidecar{
		Name:  "tailscale",
		Image: "ts:latest",
	}

	content := generateSidecarQuadlet(sc, "ov-my-app")

	if strings.Contains(content, "TS_SERVE_CONFIG") {
		t.Error("sidecar should NOT have TS_SERVE_CONFIG (host tunnel handles port exposure)")
	}
}

func TestGenerateQuadlet_PodMode(t *testing.T) {
	cfg := QuadletConfig{
		ImageName: "my-app",
		ImageRef:  "ghcr.io/overthinkos/my-app:latest",
		Home:      "/home/user",
		Ports:     []string{"443:18789"},
		Volumes: []VolumeMount{
			{VolumeName: "ov-my-app-data", ContainerPath: "/opt/data"},
		},
		BindAddress: "127.0.0.1",
		Network:     "ov",
		PodName:     "ov-my-app",
		Entrypoint:  []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
	}

	content := generateQuadlet(cfg)

	// Should have Pod= directive
	if !strings.Contains(content, "Pod=ov-my-app.pod") {
		t.Error("missing Pod directive in pod mode")
	}

	// Should NOT have PublishPort (moved to pod)
	if strings.Contains(content, "PublishPort=") {
		t.Error("PublishPort should not be present in pod mode (ports go on the pod)")
	}

	// Should NOT have Network (pod handles networking)
	if strings.Contains(content, "Network=") {
		t.Error("Network should not be present in pod mode (pod handles networking)")
	}

	// Should still have volumes and other directives
	if !strings.Contains(content, "Volume=ov-my-app-data:/opt/data") {
		t.Error("missing volume in pod mode")
	}
}

func TestGenerateQuadlet_NonPodMode(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:   "my-app",
		ImageRef:    "ghcr.io/overthinkos/my-app:latest",
		Home:        "/home/user",
		Ports:       []string{"443:18789"},
		BindAddress: "127.0.0.1",
		Network:     "ov",
		Entrypoint:  []string{"sleep", "infinity"},
	}

	content := generateQuadlet(cfg)

	// Should NOT have Pod= directive
	if strings.Contains(content, "Pod=") {
		t.Error("Pod should not be present in non-pod mode")
	}

	// Should have PublishPort
	if !strings.Contains(content, "PublishPort=") {
		t.Error("missing PublishPort in non-pod mode")
	}

	// Should have Network
	if !strings.Contains(content, "Network=ov") {
		t.Error("missing Network in non-pod mode")
	}
}

func TestGenerateQuadlet_HostTunnelWithSidecar(t *testing.T) {
	// Host-based tailscale serve should be present EVEN when a sidecar exists.
	// The host tunnel handles port exposure on the host's tailnet;
	// the sidecar handles exit node routing — they are independent.
	cfg := QuadletConfig{
		ImageName:   "my-app",
		ImageRef:    "ghcr.io/overthinkos/my-app:latest",
		Home:        "/home/user",
		Ports:       []string{"443:18789"},
		BindAddress: "127.0.0.1",
		PodName:     "ov-my-app",
		Entrypoint:  []string{"sleep", "infinity"},
		Tunnel: &TunnelConfig{
			Provider: "tailscale",
			Ports: []TunnelPort{
				{Port: 443, Protocol: "http", Public: false},
			},
		},
		Sidecars: []ResolvedSidecar{
			{Name: "tailscale", Image: "ts:latest"},
		},
	}

	content := generateQuadlet(cfg)

	// Host tunnel commands MUST be present (dual networking)
	if !strings.Contains(content, "ExecStartPost=tailscale serve") {
		t.Error("host tailscale serve should be present even with sidecar (dual networking)")
	}
	if !strings.Contains(content, "ExecStopPost=-tailscale serve") {
		t.Error("host tailscale serve cleanup should be present")
	}
}

func TestGenerateTailscaleServeConfig(t *testing.T) {
	tunnel := &TunnelConfig{
		Provider: "tailscale",
		Ports: []TunnelPort{
			{Port: 443, BackendPort: 18789, Protocol: "http", Public: true},
			{Port: 8443, BackendPort: 8443, Protocol: "https", Public: false},
		},
	}

	data, err := GenerateTailscaleServeConfig(tunnel)
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("expected non-nil serve config")
	}

	var cfg TailscaleServeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	// Check TCP entries
	if _, ok := cfg.TCP["443"]; !ok {
		t.Error("missing TCP 443")
	}
	if _, ok := cfg.TCP["8443"]; !ok {
		t.Error("missing TCP 8443")
	}

	// Check Web entries
	webKey443 := "${TS_CERT_DOMAIN}:443"
	if web, ok := cfg.Web[webKey443]; !ok {
		t.Error("missing Web entry for 443")
	} else {
		handler := web.Handlers["/"]
		if handler.Proxy != "http://127.0.0.1:18789" {
			t.Errorf("proxy = %q, want http://127.0.0.1:18789", handler.Proxy)
		}
	}

	webKey8443 := "${TS_CERT_DOMAIN}:8443"
	if web, ok := cfg.Web[webKey8443]; !ok {
		t.Error("missing Web entry for 8443")
	} else {
		handler := web.Handlers["/"]
		if handler.Proxy != "https://127.0.0.1:8443" {
			t.Errorf("proxy = %q, want https://127.0.0.1:8443", handler.Proxy)
		}
	}

	// Check AllowFunnel
	if !cfg.AllowFunnel[webKey443] {
		t.Error("port 443 should have AllowFunnel=true (public)")
	}
	if cfg.AllowFunnel[webKey8443] {
		t.Error("port 8443 should have AllowFunnel=false (private)")
	}
}

func TestGenerateTailscaleServeConfig_TCPOnly(t *testing.T) {
	tunnel := &TunnelConfig{
		Provider: "tailscale",
		Ports: []TunnelPort{
			{Port: 5432, Protocol: "tcp", Public: false},
		},
	}

	data, err := GenerateTailscaleServeConfig(tunnel)
	if err != nil {
		t.Fatal(err)
	}
	// TCP-only ports don't produce a serve config (TS_SERVE_CONFIG doesn't support raw TCP)
	if data != nil {
		t.Error("TCP-only tunnel should produce nil serve config")
	}
}

func TestGenerateTailscaleServeConfig_Nil(t *testing.T) {
	data, err := GenerateTailscaleServeConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if data != nil {
		t.Error("nil tunnel should produce nil serve config")
	}
}

func TestPodQuadletFilename(t *testing.T) {
	if got := podQuadletFilename("my-app"); got != "ov-my-app.pod" {
		t.Errorf("got %q, want ov-my-app.pod", got)
	}
}

func TestSidecarQuadletFilename(t *testing.T) {
	if got := sidecarQuadletFilename("my-app", "tailscale"); got != "ov-my-app-tailscale.container" {
		t.Errorf("got %q, want ov-my-app-tailscale.container", got)
	}
}
