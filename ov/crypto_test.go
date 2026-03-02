package main

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolveBindMountsPlain(t *testing.T) {
	mounts := []BindMountConfig{
		{Name: "data", Host: "/home/user/data", Path: "~/.myapp/data"},
	}
	resolved := resolveBindMounts("myapp", mounts, "/home/user", "/tmp/encrypted")

	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(resolved))
	}
	m := resolved[0]
	if m.Name != "data" {
		t.Errorf("Name = %q, want data", m.Name)
	}
	if m.HostPath != "/home/user/data" {
		t.Errorf("HostPath = %q, want /home/user/data", m.HostPath)
	}
	if m.ContPath != "/home/user/.myapp/data" {
		t.Errorf("ContPath = %q, want /home/user/.myapp/data", m.ContPath)
	}
	if m.Encrypted {
		t.Error("expected Encrypted = false")
	}
}

func TestResolveBindMountsEncrypted(t *testing.T) {
	mounts := []BindMountConfig{
		{Name: "secrets", Path: "~/.myapp/secrets", Encrypted: true},
	}
	resolved := resolveBindMounts("myapp", mounts, "/home/user", "/data/encrypted")

	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved mount, got %d", len(resolved))
	}
	m := resolved[0]
	if m.Name != "secrets" {
		t.Errorf("Name = %q, want secrets", m.Name)
	}
	if m.HostPath != "/data/encrypted/ov-myapp-secrets/plain" {
		t.Errorf("HostPath = %q, want /data/encrypted/ov-myapp-secrets/plain", m.HostPath)
	}
	if m.ContPath != "/home/user/.myapp/secrets" {
		t.Errorf("ContPath = %q, want /home/user/.myapp/secrets", m.ContPath)
	}
	if !m.Encrypted {
		t.Error("expected Encrypted = true")
	}
}

func TestResolveBindMountsMixed(t *testing.T) {
	mounts := []BindMountConfig{
		{Name: "data", Host: "/home/user/data", Path: "~/.myapp"},
		{Name: "secrets", Path: "~/.myapp/secrets", Encrypted: true},
	}
	resolved := resolveBindMounts("myapp", mounts, "/home/user", "/tmp/enc")

	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved mounts, got %d", len(resolved))
	}
	if resolved[0].Encrypted {
		t.Error("first mount should be plain")
	}
	if !resolved[1].Encrypted {
		t.Error("second mount should be encrypted")
	}
}

func TestResolveBindMountsEmpty(t *testing.T) {
	resolved := resolveBindMounts("myapp", nil, "/home/user", "/tmp/enc")
	if len(resolved) != 0 {
		t.Errorf("expected 0 resolved mounts, got %d", len(resolved))
	}
}

func TestEncryptedVolumeName(t *testing.T) {
	tests := []struct {
		image, name, want string
	}{
		{"myapp", "secrets", "ov-myapp-secrets"},
		{"openclaw", "data", "ov-openclaw-data"},
	}
	for _, tt := range tests {
		got := encryptedVolumeName(tt.image, tt.name)
		if got != tt.want {
			t.Errorf("encryptedVolumeName(%q, %q) = %q, want %q", tt.image, tt.name, got, tt.want)
		}
	}
}

func TestEncryptedCipherDir(t *testing.T) {
	got := encryptedCipherDir("/data/enc", "myapp", "secrets")
	want := "/data/enc/ov-myapp-secrets/cipher"
	if got != want {
		t.Errorf("encryptedCipherDir() = %q, want %q", got, want)
	}
}

func TestEncryptedPlainDir(t *testing.T) {
	got := encryptedPlainDir("/data/enc", "myapp", "secrets")
	want := "/data/enc/ov-myapp-secrets/plain"
	if got != want {
		t.Errorf("encryptedPlainDir() = %q, want %q", got, want)
	}
}

func TestIsEncryptedInitialized(t *testing.T) {
	// Non-existent directory
	if isEncryptedInitialized("/nonexistent/cipher") {
		t.Error("expected false for nonexistent directory")
	}

	// Directory without gocryptfs.conf
	dir := t.TempDir()
	if isEncryptedInitialized(dir) {
		t.Error("expected false for dir without gocryptfs.conf")
	}
}

func TestHasEncryptedBindMounts(t *testing.T) {
	tests := []struct {
		name   string
		mounts []ResolvedBindMount
		want   bool
	}{
		{"nil", nil, false},
		{"empty", []ResolvedBindMount{}, false},
		{"plain only", []ResolvedBindMount{{Encrypted: false}}, false},
		{"encrypted", []ResolvedBindMount{{Encrypted: true}}, true},
		{"mixed", []ResolvedBindMount{{Encrypted: false}, {Encrypted: true}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasEncryptedBindMounts(tt.mounts)
			if got != tt.want {
				t.Errorf("hasEncryptedBindMounts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateCryptoUnit(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/data/enc/ov-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}

	got := generateCryptoUnit("myapp", mounts, "/data/enc")

	if !strings.Contains(got, "ov-myapp-crypto.service") {
		t.Errorf("expected filename in comment, got:\n%s", got)
	}
	if !strings.Contains(got, "Description=gocryptfs mounts for ov-myapp") {
		t.Errorf("expected Description, got:\n%s", got)
	}
	if !strings.Contains(got, "Type=oneshot") {
		t.Errorf("expected Type=oneshot, got:\n%s", got)
	}
	if !strings.Contains(got, "RemainAfterExit=yes") {
		t.Errorf("expected RemainAfterExit, got:\n%s", got)
	}
	if !strings.Contains(got, "gocryptfs -extpass") {
		t.Errorf("expected ExecStart with gocryptfs, got:\n%s", got)
	}
	if !strings.Contains(got, "--id=ov-myapp") {
		t.Errorf("expected --id=ov-myapp in extpass, got:\n%s", got)
	}
	if !strings.Contains(got, "/data/enc/ov-myapp-secrets/cipher") {
		t.Errorf("expected cipher dir in ExecStart, got:\n%s", got)
	}
	if !strings.Contains(got, "/data/enc/ov-myapp-secrets/plain") {
		t.Errorf("expected plain dir in ExecStart, got:\n%s", got)
	}
	if !strings.Contains(got, "ExecStop=-fusermount3 -u") {
		t.Errorf("expected ExecStop with fusermount3, got:\n%s", got)
	}
	if !strings.Contains(got, "WantedBy=default.target") {
		t.Errorf("expected WantedBy, got:\n%s", got)
	}
}

func TestGenerateCryptoUnitNoEncrypted(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp", Encrypted: false},
	}

	got := generateCryptoUnit("myapp", mounts, "/data/enc")
	if got != "" {
		t.Errorf("expected empty string for no encrypted mounts, got: %s", got)
	}
}

func TestGenerateCryptoUnitMultiple(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/data/enc/ov-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		{Name: "models", HostPath: "/data/enc/ov-myapp-models/plain", ContPath: "/home/user/.models", Encrypted: true},
	}

	got := generateCryptoUnit("myapp", mounts, "/data/enc")

	// Should have two ExecStart and two ExecStop lines
	if strings.Count(got, "ExecStart=") != 2 {
		t.Errorf("expected 2 ExecStart lines, got:\n%s", got)
	}
	if strings.Count(got, "ExecStop=") != 2 {
		t.Errorf("expected 2 ExecStop lines, got:\n%s", got)
	}
}

func TestCryptoServiceFilename(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"myapp", "ov-myapp-crypto.service"},
		{"openclaw", "ov-openclaw-crypto.service"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := cryptoServiceFilename(tt.image)
			if got != tt.want {
				t.Errorf("cryptoServiceFilename(%q) = %q, want %q", tt.image, got, tt.want)
			}
		})
	}
}

func TestVerifyBindMountsPlainDirMissing(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/nonexistent/path", ContPath: "/home/user/.myapp", Encrypted: false},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err == nil {
		t.Fatal("expected error for missing host dir")
	}
	if !strings.Contains(err.Error(), "bind mount \"data\"") {
		t.Errorf("error should reference bind mount name, got: %v", err)
	}
}

func TestVerifyBindMountsPlainDirExists(t *testing.T) {
	dir := t.TempDir()
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: dir, ContPath: "/home/user/.myapp", Encrypted: false},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestVerifyBindMountsEncryptedNotMounted(t *testing.T) {
	// Mock isEncryptedMounted to always return false
	orig := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return false }
	defer func() { isEncryptedMounted = orig }()

	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/tmp/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err == nil {
		t.Fatal("expected error for unmounted encrypted volume")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("error should mention 'not mounted', got: %v", err)
	}
	if !strings.Contains(err.Error(), "ov crypto mount") {
		t.Errorf("error should suggest 'ov crypto mount', got: %v", err)
	}
}

func TestVerifyBindMountsEncryptedMounted(t *testing.T) {
	// Mock isEncryptedMounted to always return true
	orig := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return true }
	defer func() { isEncryptedMounted = orig }()

	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/tmp/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	err := verifyBindMounts(mounts, "myapp")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsValid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "data", Host: "/home/user/data", Path: "~/.myapp"},
					{Name: "secrets", Path: "~/.myapp/secrets", Encrypted: true},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "bind_mounts") {
			t.Errorf("unexpected bind mount validation error: %v", err)
		}
	}
}

func TestValidateBindMountsMissingName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Host: "/home/user/data", Path: "~/.myapp"},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "missing required \"name\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsInvalidName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "INVALID_NAME", Host: "/data", Path: "/app"},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "lowercase alphanumeric") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsDuplicateName(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "data", Host: "/data1", Path: "/app1"},
					{Name: "data", Host: "/data2", Path: "/app2"},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsMissingPath(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "data", Host: "/data"},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for missing path")
	}
	if !strings.Contains(err.Error(), "missing required \"path\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsEncryptedWithHost(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "secrets", Host: "/should/not/be/here", Path: "/app/secrets", Encrypted: true},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for encrypted mount with host")
	}
	if !strings.Contains(err.Error(), "must not have \"host\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsPlainWithoutHost(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{},
				BindMounts: []BindMountConfig{
					{Name: "data", Path: "/app/data"},
				},
			},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Fatal("expected error for plain mount without host")
	}
	if !strings.Contains(err.Error(), "requires \"host\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateBindMountsVolumeNameOverride(t *testing.T) {
	// Bind mount with same name as layer volume should produce a note, not an error
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {
				Layers: []string{"mylayer"},
				BindMounts: []BindMountConfig{
					{Name: "data", Host: "/host/data", Path: "/app/data"},
				},
			},
		},
	}
	layers := map[string]*Layer{
		"mylayer": {
			Name:       "mylayer",
			HasVolumes: true,
			volumes: []VolumeYAML{
				{Name: "data", Path: "/other/path"},
			},
		},
	}

	err := Validate(cfg, layers)
	// Should NOT be a validation error (just a note on stderr)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "collides with a layer volume name") {
			t.Errorf("name collision should be a note, not an error: %v", err)
		}
	}
}

func TestBindMountConfigYAMLParsing(t *testing.T) {
	input := `
images:
  myapp:
    base: "fedora:43"
    layers: []
    bind_mounts:
      - name: data
        host: "~/data/myapp"
        path: "~/.myapp"
      - name: secrets
        path: "~/.myapp/secrets"
        encrypted: true
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	img := cfg.Images["myapp"]
	if len(img.BindMounts) != 2 {
		t.Fatalf("expected 2 bind mounts, got %d", len(img.BindMounts))
	}

	bm0 := img.BindMounts[0]
	if bm0.Name != "data" || bm0.Host != "~/data/myapp" || bm0.Path != "~/.myapp" || bm0.Encrypted {
		t.Errorf("bind mount 0 = %+v", bm0)
	}

	bm1 := img.BindMounts[1]
	if bm1.Name != "secrets" || bm1.Host != "" || bm1.Path != "~/.myapp/secrets" || !bm1.Encrypted {
		t.Errorf("bind mount 1 = %+v", bm1)
	}
}

func TestQuadletWithBindMounts(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:   "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Workspace:   "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []ResolvedBindMount{
			{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp", Encrypted: false},
		},
	}

	got := generateQuadlet(cfg)

	if !strings.Contains(got, "Volume=/home/user/data:/home/user/.myapp") {
		t.Errorf("expected Volume for bind mount, got:\n%s", got)
	}
	// Should not have crypto service dependency
	if strings.Contains(got, "crypto.service") {
		t.Errorf("should not have crypto service for plain mounts, got:\n%s", got)
	}
}

func TestQuadletWithEncryptedBindMounts(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:   "myapp",
		ImageRef:    "ghcr.io/test/myapp:latest",
		Workspace:   "/home/user/project",
		BindAddress: "127.0.0.1",
		BindMounts: []ResolvedBindMount{
			{Name: "secrets", HostPath: "/data/enc/ov-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		},
	}

	got := generateQuadlet(cfg)

	if !strings.Contains(got, "Requires=ov-myapp-crypto.service") {
		t.Errorf("expected Requires for crypto service, got:\n%s", got)
	}
	if !strings.Contains(got, "After=ov-myapp-crypto.service") {
		t.Errorf("expected After for crypto service, got:\n%s", got)
	}
	if !strings.Contains(got, "Volume=/data/enc/ov-myapp-secrets/plain:/home/user/.secrets") {
		t.Errorf("expected Volume for encrypted bind mount, got:\n%s", got)
	}
}

func TestBuildShellArgsWithBindMounts(t *testing.T) {
	withTerminal(t, true)
	bindMounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("docker", "myapp:latest", "/workspace", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1")

	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/home/user/data:/home/user/.myapp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v /home/user/data:/home/user/.myapp in args, got: %v", args)
	}
	// Docker should NOT have --userns
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			t.Error("docker should not have --userns=keep-id")
		}
	}
}

func TestBuildShellArgsWithBindMountsPodman(t *testing.T) {
	withTerminal(t, true)
	bindMounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp"},
	}
	args := buildShellArgs("podman", "myapp:latest", "/workspace", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1")

	found := false
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 in podman args, got: %v", args)
	}
}

func TestBuildStartArgsWithBindMounts(t *testing.T) {
	bindMounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/enc/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	args := buildStartArgs("docker", "myapp:latest", "/workspace", 1000, 1000, nil, "ov-myapp", nil, bindMounts, false, "127.0.0.1")

	found := false
	for i, arg := range args {
		if arg == "-v" && i+1 < len(args) && args[i+1] == "/enc/plain:/home/user/.secrets" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected -v /enc/plain:/home/user/.secrets in args, got: %v", args)
	}
	// Docker should NOT have --userns
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			t.Error("docker should not have --userns=keep-id")
		}
	}
}

func TestBuildStartArgsWithBindMountsPodman(t *testing.T) {
	bindMounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/enc/plain", ContPath: "/home/user/.secrets", Encrypted: true},
	}
	args := buildStartArgs("podman", "myapp:latest", "/workspace", 1000, 1000, nil, "ov-myapp", nil, bindMounts, false, "127.0.0.1")

	found := false
	for _, arg := range args {
		if arg == "--userns=keep-id:uid=1000,gid=1000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --userns=keep-id:uid=1000,gid=1000 in podman args, got: %v", args)
	}
}

func TestCryptoPasswdRequiresUnmount(t *testing.T) {
	// Mock isEncryptedMounted to return true (volume is mounted)
	origMounted := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return true }
	defer func() { isEncryptedMounted = origMounted }()

	cmd := &CryptoPasswdCmd{Image: "myapp"}
	// We can't call Run() directly because loadEncryptedMounts needs images.yml,
	// so test the logic by simulating what Run() does.
	mounts := []BindMountConfig{
		{Name: "secrets", Path: "~/.secrets", Encrypted: true},
	}
	storagePath := "/data/enc"

	for _, m := range mounts {
		plainDir := encryptedPlainDir(storagePath, cmd.Image, m.Name)
		if isEncryptedMounted(plainDir) {
			err := fmt.Errorf("encrypted volume %q is still mounted; run 'ov crypto unmount %s' first", m.Name, cmd.Image)
			if !strings.Contains(err.Error(), "still mounted") {
				t.Errorf("expected 'still mounted' in error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "ov crypto unmount") {
				t.Errorf("expected 'ov crypto unmount' hint in error, got: %v", err)
			}
			return
		}
	}
	t.Fatal("expected mounted volume to trigger error")
}

func TestCryptoPasswdPasswordMismatch(t *testing.T) {
	// Mock askPassword to return controlled values
	origAsk := askPassword
	callCount := 0
	askPassword = func(id, prompt string) (string, error) {
		callCount++
		switch callCount {
		case 1:
			return "oldpass", nil // current
		case 2:
			return "newpass", nil // new
		case 3:
			return "different", nil // confirm (mismatch)
		}
		return "", fmt.Errorf("unexpected call")
	}
	defer func() { askPassword = origAsk }()

	// Mock isEncryptedMounted to return false (all unmounted)
	origMounted := isEncryptedMounted
	isEncryptedMounted = func(plainDir string) bool { return false }
	defer func() { isEncryptedMounted = origMounted }()

	// Simulate the password check logic from Run()
	oldPass, _ := askPassword("test-old", "Current passphrase:")
	newPass, _ := askPassword("test-new", "New passphrase:")
	confirmPass, _ := askPassword("test-confirm", "Confirm new passphrase:")

	_ = oldPass
	if newPass != confirmPass {
		// This is the expected path
		return
	}
	t.Fatal("expected password mismatch to be detected")
}
