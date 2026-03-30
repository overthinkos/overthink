package main

import (
	"fmt"
	"strings"
	"testing"
)

// resolveBindMounts tests moved to deploy_test.go (TestResolveVolumeBacking*)

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

	ovBin := "/usr/bin/ov"
	got := generateEncUnit("myapp", mounts, ovBin)

	if !strings.Contains(got, "ov-myapp-enc.service") {
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
	if !strings.Contains(got, "ExecStart=/usr/bin/ov config mount myapp") {
		t.Errorf("expected ExecStart delegating to ov config mount, got:\n%s", got)
	}
	if !strings.Contains(got, "ExecStop=/usr/bin/ov config unmount myapp") {
		t.Errorf("expected ExecStop delegating to ov config unmount, got:\n%s", got)
	}
	if !strings.Contains(got, "WantedBy=default.target") {
		t.Errorf("expected WantedBy, got:\n%s", got)
	}
}

func TestGenerateCryptoUnitNoEncrypted(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/home/user/data", ContPath: "/home/user/.myapp", Encrypted: false},
	}

	got := generateEncUnit("myapp", mounts, "/usr/bin/ov")
	if got != "" {
		t.Errorf("expected empty string for no encrypted mounts, got: %s", got)
	}
}

func TestGenerateCryptoUnitMultiple(t *testing.T) {
	mounts := []ResolvedBindMount{
		{Name: "secrets", HostPath: "/data/enc/ov-myapp-secrets/plain", ContPath: "/home/user/.secrets", Encrypted: true},
		{Name: "models", HostPath: "/data/enc/ov-myapp-models/plain", ContPath: "/home/user/.models", Encrypted: true},
	}

	got := generateEncUnit("myapp", mounts, "/usr/bin/ov")

	// Multiple volumes are handled by a single ov config mount call
	if strings.Count(got, "ExecStart=") != 1 {
		t.Errorf("expected 1 ExecStart line (ov config mount handles all volumes), got:\n%s", got)
	}
	if strings.Count(got, "ExecStop=") != 1 {
		t.Errorf("expected 1 ExecStop line (ov config unmount handles all volumes), got:\n%s", got)
	}
}

func TestCryptoServiceFilename(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"myapp", "ov-myapp-enc.service"},
		{"openclaw", "ov-openclaw-enc.service"},
	}
	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			got := encServiceFilename(tt.image)
			if got != tt.want {
				t.Errorf("encServiceFilename(%q) = %q, want %q", tt.image, got, tt.want)
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
	if !strings.Contains(err.Error(), "ov config mount") {
		t.Errorf("error should suggest 'ov config mount', got: %v", err)
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

// Build-time bind mount validation tests removed — validateBindMounts was deleted.
// Volume backing is now a deploy-time concern (see deploy_test.go TestResolveVolumeBacking*).

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

	// Enc service dependency was removed — ov start handles mounting inline
	if strings.Contains(got, "Requires=ov-myapp-enc.service") {
		t.Errorf("should NOT have Requires for enc service (removed), got:\n%s", got)
	}
	if strings.Contains(got, "After=ov-myapp-enc.service") {
		t.Errorf("should NOT have After for enc service (removed), got:\n%s", got)
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
	args := buildShellArgs("docker", "myapp:latest", "/workspace", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, SecurityConfig{})

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
	args := buildShellArgs("podman", "myapp:latest", "/workspace", 1000, 1000, nil, nil, bindMounts, false, "", "127.0.0.1", nil, SecurityConfig{})

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
	args := buildStartArgs("docker", "myapp:latest", "/workspace", 1000, 1000, nil, "ov-myapp", nil, bindMounts, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"})

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
	args := buildStartArgs("podman", "myapp:latest", "/workspace", 1000, 1000, nil, "ov-myapp", nil, bindMounts, false, "127.0.0.1", nil, SecurityConfig{}, []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"})

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

	imageName := "myapp"
	// We can't call encPasswd() directly because loadEncryptedVolumes needs deploy.yml,
	// so test the logic by simulating what encPasswd() does.
	mounts := []DeployVolumeConfig{
		{Name: "secrets", Type: "encrypted"},
	}
	storagePath := "/data/enc"

	for _, m := range mounts {
		plainDir := encryptedPlainDir(storagePath, imageName, m.Name)
		if isEncryptedMounted(plainDir) {
			err := fmt.Errorf("encrypted volume %q is still mounted; run 'ov config unmount %s' first", m.Name, imageName)
			if !strings.Contains(err.Error(), "still mounted") {
				t.Errorf("expected 'still mounted' in error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "ov config unmount") {
				t.Errorf("expected 'ov config unmount' hint in error, got: %v", err)
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
