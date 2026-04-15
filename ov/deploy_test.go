package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestAppendOrReplaceEnv(t *testing.T) {
	envs := []string{"A=1", "B=2"}

	// Append new key
	envs = appendOrReplaceEnv(envs, "C=3")
	if !reflect.DeepEqual(envs, []string{"A=1", "B=2", "C=3"}) {
		t.Errorf("got %v, want [A=1 B=2 C=3]", envs)
	}

	// Replace existing key
	envs = appendOrReplaceEnv(envs, "B=new")
	if !reflect.DeepEqual(envs, []string{"A=1", "B=new", "C=3"}) {
		t.Errorf("got %v, want [A=1 B=new C=3]", envs)
	}
}

func TestGlobalEnvForImage(t *testing.T) {
	dc := &DeployConfig{
		Provides: &ProvidesConfig{
			Env: []EnvProvidesEntry{
				{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama"},
				{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
			},
			MCP: []MCPProvidesEntry{
				{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Transport: "http", Source: "jupyter"},
			},
		},
	}

	// Pod-aware: ollama's own env_provides are included with localhost rewrite
	got := dc.GlobalEnvForImage("ollama", "ov-ollama", nil)
	// Should have PGHOST, OLLAMA_HOST (rewritten to localhost), and MCP
	foundPG := false
	foundOllama := false
	ollamaValue := ""
	foundMCP := false
	for _, e := range got {
		if envKey(e) == "PGHOST" {
			foundPG = true
		}
		if envKey(e) == "OLLAMA_HOST" {
			foundOllama = true
			ollamaValue = e[len("OLLAMA_HOST="):]
		}
		if envKey(e) == "OV_MCP_SERVERS" {
			foundMCP = true
		}
	}
	if !foundPG {
		t.Error("PGHOST should be in globalEnv for ollama")
	}
	if !foundOllama {
		t.Error("OLLAMA_HOST should be pod-aware (included with localhost) for ollama")
	}
	if ollamaValue != "http://localhost:11434" {
		t.Errorf("OLLAMA_HOST should be rewritten to localhost, got %q", ollamaValue)
	}
	if !foundMCP {
		t.Error("OV_MCP_SERVERS should be injected for ollama")
	}

	// Nil DeployConfig returns nil
	var nilDC *DeployConfig
	if got := nilDC.GlobalEnvForImage("test", "ov-test", nil); got != nil {
		t.Errorf("nil DC should return nil, got %v", got)
	}
}

// TestGlobalEnvForImageNoSelfInjectionWithoutAccepts pins the fixed semantic:
// a producer does NOT automatically consume its own env_provides. Before this fix,
// GlobalEnvForImage had an explicit bypass that unconditionally injected same-image
// entries (source == consumer) into the producer's own quadlet, skipping the
// env_accepts filter. That bypass clobbered the ollama layer's own
// `env: OLLAMA_HOST=0.0.0.0` declaration with the rewritten self-provide
// `OLLAMA_HOST=http://localhost:11434`, breaking rootlessport forwarding.
//
// After the fix: same-image entries are filtered by env_accepts uniformly with
// cross-image entries. A producer that does not declare `env_accepts:` for a var
// does NOT receive that var in its own env — its layer's own `env:` declaration
// (baked as Dockerfile ENV) stays authoritative.
func TestGlobalEnvForImageNoSelfInjectionWithoutAccepts(t *testing.T) {
	dc := &DeployConfig{
		Provides: &ProvidesConfig{
			Env: []EnvProvidesEntry{
				{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama"},
				{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
			},
		},
	}

	// Ollama deploys with NO env_accepts (correct producer-only layer config).
	// Its own OLLAMA_HOST provides entry MUST NOT be injected into its own env.
	empty := map[string]bool{}
	got := dc.GlobalEnvForImage("ollama", "ov-ollama", empty)

	for _, e := range got {
		switch envKey(e) {
		case "OLLAMA_HOST":
			t.Errorf("ollama should NOT receive its own OLLAMA_HOST env_provides "+
				"(producer is not a self-consumer without explicit env_accepts), got %q", e)
		case "PGHOST":
			t.Errorf("ollama should NOT receive PGHOST (not in env_accepts), got %q", e)
		}
	}

	// Openwebui declares env_accepts: [OLLAMA_HOST] — should receive the cross-image
	// URL with NO rewrite (openwebui is not the producer, so no localhost rewrite applies).
	accepts := map[string]bool{"OLLAMA_HOST": true}
	got = dc.GlobalEnvForImage("openwebui", "ov-openwebui", accepts)
	want := "OLLAMA_HOST=http://ov-ollama:11434"
	found := false
	for _, e := range got {
		if e == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("openwebui should receive %q, got %v", want, got)
	}

	// PGHOST should still NOT reach openwebui (not in its acceptedEnv set).
	for _, e := range got {
		if envKey(e) == "PGHOST" {
			t.Errorf("openwebui should NOT receive PGHOST (not in env_accepts), got %q", e)
		}
	}
}

func TestDeployConfigProvidesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")

	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) { return path, nil }
	defer func() { DeployConfigPath = orig }()

	dc := &DeployConfig{
		Provides: &ProvidesConfig{
			Env: []EnvProvidesEntry{
				{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama"},
				{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
			},
			MCP: []MCPProvidesEntry{
				{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Transport: "http", Source: "jupyter"},
			},
		},
		Images: map[string]DeployImageConfig{
			"ollama": {Ports: []string{"11434:11434"}},
		},
	}

	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveDeployConfig: %v", err)
	}

	loaded, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	if len(loaded.Provides.Env) != 2 {
		t.Errorf("Provides.Env = %v, want 2 entries", loaded.Provides.Env)
	}
	if len(loaded.Provides.MCP) != 1 {
		t.Errorf("Provides.MCP = %v, want 1 entry", loaded.Provides.MCP)
	}
}

func TestCleanDeployEntryRemovesProvides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")

	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) { return path, nil }
	defer func() { DeployConfigPath = orig }()

	dc := &DeployConfig{
		Provides: &ProvidesConfig{
			Env: []EnvProvidesEntry{
				{Name: "OLLAMA_HOST", Value: "http://ov-ollama:11434", Source: "ollama"},
				{Name: "PGHOST", Value: "ov-postgresql", Source: "postgresql"},
			},
			MCP: []MCPProvidesEntry{
				{Name: "jupyter", URL: "http://ov-jupyter:8888/mcp", Transport: "http", Source: "jupyter"},
			},
		},
		Images: map[string]DeployImageConfig{
			"ollama":     {Ports: []string{"11434:11434"}},
			"postgresql": {Ports: []string{"5432:5432"}},
			"jupyter":    {Ports: []string{"8888:8888"}},
		},
	}
	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveDeployConfig: %v", err)
	}

	// Remove ollama — should clean up OLLAMA_HOST from provides.env
	cleanDeployEntry("ollama", "")

	loaded, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	// OLLAMA_HOST should be gone, PGHOST should remain
	if len(loaded.Provides.Env) != 1 || loaded.Provides.Env[0].Name != "PGHOST" {
		t.Errorf("Provides.Env after cleanup = %v, want only PGHOST", loaded.Provides.Env)
	}
	// MCP should be untouched
	if len(loaded.Provides.MCP) != 1 {
		t.Errorf("Provides.MCP should be untouched, got %v", loaded.Provides.MCP)
	}
	if _, ok := loaded.Images["ollama"]; ok {
		t.Error("ollama image should be removed")
	}

	// Remove jupyter — should clean up MCP entry
	cleanDeployEntry("jupyter", "")

	loaded2, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}
	if loaded2.Provides != nil && len(loaded2.Provides.MCP) > 0 {
		t.Errorf("Provides.MCP after jupyter cleanup should be empty, got %v", loaded2.Provides.MCP)
	}
}

func TestResolveVolumeBackingEncryptedWithHost(t *testing.T) {
	labelVolumes := []VolumeMount{
		{VolumeName: "ov-myapp-library", ContainerPath: "/home/user/.immich/library"},
	}
	deployVolumes := []DeployVolumeConfig{
		{Name: "library", Type: "encrypted", Host: "/data/immich/library"},
	}

	volumes, binds := ResolveVolumeBacking("myapp", labelVolumes, deployVolumes, "/home/user", "/enc", "/vol")

	if len(volumes) != 0 {
		t.Errorf("volumes = %+v, want 0 named volumes", volumes)
	}
	if len(binds) != 1 || !binds[0].Encrypted {
		t.Errorf("binds = %+v, want 1 encrypted bind mount", binds)
	}
	// Explicit Host path is used directly (no ov-image-name prefix)
	if binds[0].HostPath != "/data/immich/library/plain" {
		t.Errorf("HostPath = %q, want /data/immich/library/plain", binds[0].HostPath)
	}
}

func TestDeployKey(t *testing.T) {
	tests := []struct {
		image, instance string
		want            string
	}{
		{"selkies-desktop", "", "selkies-desktop"},
		{"selkies-desktop", "31.58.9.4", "selkies-desktop/31.58.9.4"},
		{"my-app", "foo", "my-app/foo"},
	}
	for _, tt := range tests {
		got := deployKey(tt.image, tt.instance)
		if got != tt.want {
			t.Errorf("deployKey(%q, %q) = %q, want %q", tt.image, tt.instance, got, tt.want)
		}
	}
}

func TestParseDeployKey(t *testing.T) {
	tests := []struct {
		key          string
		wantImage    string
		wantInstance string
	}{
		{"selkies-desktop", "selkies-desktop", ""},
		{"selkies-desktop/31.58.9.4", "selkies-desktop", "31.58.9.4"},
		{"my-app/foo", "my-app", "foo"},
	}
	for _, tt := range tests {
		img, inst := parseDeployKey(tt.key)
		if img != tt.wantImage || inst != tt.wantInstance {
			t.Errorf("parseDeployKey(%q) = (%q, %q), want (%q, %q)", tt.key, img, inst, tt.wantImage, tt.wantInstance)
		}
	}
}

func TestDeployKeyRoundTrip(t *testing.T) {
	pairs := [][2]string{
		{"selkies-desktop", ""},
		{"selkies-desktop", "31.58.9.4"},
		{"my-app", "test"},
	}
	for _, p := range pairs {
		key := deployKey(p[0], p[1])
		img, inst := parseDeployKey(key)
		if img != p[0] || inst != p[1] {
			t.Errorf("round-trip failed: deployKey(%q, %q) = %q → parseDeployKey → (%q, %q)", p[0], p[1], key, img, inst)
		}
	}
}

func TestIsSameBaseImage(t *testing.T) {
	tests := []struct {
		source, imageName string
		want              bool
	}{
		{"selkies-desktop", "selkies-desktop", true},
		{"selkies-desktop/foo", "selkies-desktop", true},
		{"selkies-desktop", "other-image", false},
		{"selkies-desktop/foo", "other-image", false},
	}
	for _, tt := range tests {
		got := isSameBaseImage(tt.source, tt.imageName)
		if got != tt.want {
			t.Errorf("isSameBaseImage(%q, %q) = %v, want %v", tt.source, tt.imageName, got, tt.want)
		}
	}
}

func TestSaveDeployStateInstance(t *testing.T) {
	tmp := t.TempDir()
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(tmp, "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	// Save base
	saveDeployState("selkies-desktop", "", SaveDeployStateInput{
		Ports: []string{"3000:3000"},
	})
	// Save instance
	saveDeployState("selkies-desktop", "31.58.9.4", SaveDeployStateInput{
		Ports: []string{"3001:3000"},
		Env:   []string{"HTTP_PROXY=http://31.58.9.4:6077"},
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	// Verify base entry
	base, ok := dc.Images["selkies-desktop"]
	if !ok {
		t.Fatal("base entry missing")
	}
	if len(base.Ports) != 1 || base.Ports[0] != "3000:3000" {
		t.Errorf("base.Ports = %v, want [3000:3000]", base.Ports)
	}

	// Verify instance entry
	inst, ok := dc.Images["selkies-desktop/31.58.9.4"]
	if !ok {
		t.Fatal("instance entry missing")
	}
	if len(inst.Ports) != 1 || inst.Ports[0] != "3001:3000" {
		t.Errorf("inst.Ports = %v, want [3001:3000]", inst.Ports)
	}
	if len(inst.Env) != 1 || inst.Env[0] != "HTTP_PROXY=http://31.58.9.4:6077" {
		t.Errorf("inst.Env = %v, want [HTTP_PROXY=http://31.58.9.4:6077]", inst.Env)
	}
}

func TestCleanDeployEntryInstance(t *testing.T) {
	tmp := t.TempDir()
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(tmp, "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	// Set up both base and instance
	dc := &DeployConfig{
		Images: map[string]DeployImageConfig{
			"selkies-desktop":           {Ports: []string{"3000:3000"}},
			"selkies-desktop/31.58.9.4": {Ports: []string{"3001:3000"}, Env: []string{"HTTP_PROXY=x"}},
		},
	}
	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveDeployConfig: %v", err)
	}

	// Remove instance — base should survive
	cleanDeployEntry("selkies-desktop", "31.58.9.4")

	loaded, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	if _, ok := loaded.Images["selkies-desktop/31.58.9.4"]; ok {
		t.Error("instance entry should be removed")
	}
	if _, ok := loaded.Images["selkies-desktop"]; !ok {
		t.Error("base entry should survive")
	}
}

func TestMergeEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		newVars  []string
		want     []string
	}{
		{
			name:     "add new key",
			existing: []string{"SSH_KEY=abc"},
			newVars:  []string{"HTTP_PROXY=http://1.2.3.4:8080"},
			want:     []string{"SSH_KEY=abc", "HTTP_PROXY=http://1.2.3.4:8080"},
		},
		{
			name:     "override existing key",
			existing: []string{"HTTP_PROXY=http://old:1234", "SSH_KEY=abc"},
			newVars:  []string{"HTTP_PROXY=http://new:5678"},
			want:     []string{"HTTP_PROXY=http://new:5678", "SSH_KEY=abc"},
		},
		{
			name:     "mixed add and override",
			existing: []string{"SSH_KEY=abc", "FOO=bar"},
			newVars:  []string{"FOO=baz", "HTTP_PROXY=http://1.2.3.4:8080"},
			want:     []string{"SSH_KEY=abc", "FOO=baz", "HTTP_PROXY=http://1.2.3.4:8080"},
		},
		{
			name:     "empty existing",
			existing: nil,
			newVars:  []string{"HTTP_PROXY=http://1.2.3.4:8080"},
			want:     []string{"HTTP_PROXY=http://1.2.3.4:8080"},
		},
		{
			name:     "empty new preserves existing",
			existing: []string{"SSH_KEY=abc"},
			newVars:  nil,
			want:     []string{"SSH_KEY=abc"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeEnvVars(tt.existing, tt.newVars)
			if len(got) != len(tt.want) {
				t.Fatalf("mergeEnvVars() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mergeEnvVars()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSaveDeployStateEnvMerge(t *testing.T) {
	tmp := t.TempDir()
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(tmp, "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	// Initial config with SSH key
	saveDeployState("test-image", "inst1", SaveDeployStateInput{
		Env: []string{"SSH_AUTHORIZED_KEYS=ssh-ed25519 AAAA", "EXISTING=keep"},
	})

	// Add proxy vars — should merge, not replace
	saveDeployState("test-image", "inst1", SaveDeployStateInput{
		Env: []string{"HTTP_PROXY=http://1.2.3.4:8080", "HTTPS_PROXY=http://1.2.3.4:8080"},
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	entry := dc.Images["test-image/inst1"]
	// SSH key and EXISTING should be preserved
	envMap := make(map[string]string)
	for _, kv := range entry.Env {
		parts := strings.SplitN(kv, "=", 2)
		envMap[parts[0]] = parts[1]
	}

	if v, ok := envMap["SSH_AUTHORIZED_KEYS"]; !ok || v != "ssh-ed25519 AAAA" {
		t.Errorf("SSH_AUTHORIZED_KEYS lost after merge: env = %v", entry.Env)
	}
	if v, ok := envMap["EXISTING"]; !ok || v != "keep" {
		t.Errorf("EXISTING lost after merge: env = %v", entry.Env)
	}
	if v, ok := envMap["HTTP_PROXY"]; !ok || v != "http://1.2.3.4:8080" {
		t.Errorf("HTTP_PROXY not added: env = %v", entry.Env)
	}
	if v, ok := envMap["HTTPS_PROXY"]; !ok || v != "http://1.2.3.4:8080" {
		t.Errorf("HTTPS_PROXY not added: env = %v", entry.Env)
	}
}

func TestSaveDeployStateEnvClean(t *testing.T) {
	tmp := t.TempDir()
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(tmp, "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	// Initial config with SSH key
	saveDeployState("test-image", "inst1", SaveDeployStateInput{
		Env: []string{"SSH_AUTHORIZED_KEYS=ssh-ed25519 AAAA", "OLD_VAR=remove-me"},
	})

	// Clean replace — should drop everything not in the new list
	saveDeployState("test-image", "inst1", SaveDeployStateInput{
		Env:      []string{"HTTP_PROXY=http://1.2.3.4:8080"},
		CleanEnv: true,
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	entry := dc.Images["test-image/inst1"]
	if len(entry.Env) != 1 || entry.Env[0] != "HTTP_PROXY=http://1.2.3.4:8080" {
		t.Errorf("CleanEnv should replace env list: got %v", entry.Env)
	}
}

func TestSaveDeployStateTunnel(t *testing.T) {
	tmp := t.TempDir()
	orig := DeployConfigPath
	DeployConfigPath = func() (string, error) {
		return filepath.Join(tmp, "deploy.yml"), nil
	}
	defer func() { DeployConfigPath = orig }()

	tunnel := &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}
	saveDeployState("selkies-desktop", "1.2.3.4", SaveDeployStateInput{
		Ports:  []string{"3001:3000"},
		Tunnel: tunnel,
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("LoadDeployConfig: %v", err)
	}

	entry := dc.Images["selkies-desktop/1.2.3.4"]
	if entry.Tunnel == nil {
		t.Fatal("Tunnel not persisted to deploy.yml")
	}
	if entry.Tunnel.Provider != "tailscale" {
		t.Errorf("Tunnel.Provider = %q, want tailscale", entry.Tunnel.Provider)
	}
	if !entry.Tunnel.Private.All {
		t.Error("Tunnel.Private.All = false, want true")
	}

	// Verify tunnel survives subsequent saves without tunnel field
	saveDeployState("selkies-desktop", "1.2.3.4", SaveDeployStateInput{
		Env: []string{"HTTP_PROXY=http://1.2.3.4:8080"},
	})

	dc2, _ := LoadDeployConfig()
	entry2 := dc2.Images["selkies-desktop/1.2.3.4"]
	if entry2.Tunnel == nil {
		t.Fatal("Tunnel lost after save without tunnel field")
	}
	if entry2.Tunnel.Provider != "tailscale" {
		t.Errorf("Tunnel.Provider = %q after re-save, want tailscale", entry2.Tunnel.Provider)
	}
}
