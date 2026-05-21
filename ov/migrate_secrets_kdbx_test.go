package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kdbxResidualConfig = `engine:
    build: podman
secret_backend: kdbx
secrets_kdbx_path: /home/x/secrets.kdbx
secrets_kdbx_key_file: /home/x/secrets.key
kdbx_cache: true
kdbx_cache_timeout: 7200
`

func TestMigrateDropKdbx_StripsResiduals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(kdbxResidualConfig), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateDropKdbx(path, false); err != nil {
		t.Fatalf("first run: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, residual := range []string{"secret_backend: kdbx", "secrets_kdbx_path", "secrets_kdbx_key_file", "kdbx_cache", "kdbx_cache_timeout"} {
		if strings.Contains(got, residual) {
			t.Errorf("residual %q survived migration:\n%s", residual, got)
		}
	}
	// Unrelated keys are preserved.
	if !strings.Contains(got, "build: podman") {
		t.Errorf("unrelated key engine.build was dropped:\n%s", got)
	}

	// Backup written exactly once.
	baks, _ := filepath.Glob(path + ".bak.*")
	if len(baks) != 1 {
		t.Fatalf("expected 1 backup after first run, got %d", len(baks))
	}

	// Idempotent: a second run is a no-op and writes no further backup.
	if _, err := MigrateDropKdbx(path, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	baks, _ = filepath.Glob(path + ".bak.*")
	if len(baks) != 1 {
		t.Fatalf("expected 1 backup after idempotent second run, got %d", len(baks))
	}
}

func TestMigrateDropKdbx_NoFile(t *testing.T) {
	if _, err := MigrateDropKdbx(filepath.Join(t.TempDir(), "absent.yml"), false); err != nil {
		t.Fatalf("missing file should be a no-op, got: %v", err)
	}
}

func TestMigrateDropKdbx_PreservesLiveSecretBackend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("secret_backend: keyring\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateDropKdbx(path, false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if !strings.Contains(string(out), "secret_backend: keyring") {
		t.Errorf("a live secret_backend value was wrongly removed:\n%s", out)
	}
}

func TestLoadRuntimeConfig_RejectsKdbxResiduals(t *testing.T) {
	cases := map[string]string{
		"secret_backend kdbx": "secret_backend: kdbx\n",
		"secrets_kdbx_path":   "secrets_kdbx_path: /x.kdbx\n",
		"kdbx_cache":          "kdbx_cache: true\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
			defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
			if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRuntimeConfig()
			if err == nil {
				t.Fatalf("expected hard error for residual %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "ov migrate") {
				t.Errorf("error should point at the migration command, got: %v", err)
			}
		})
	}
}

func TestLoadRuntimeConfig_CleanConfigOK(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) { return filepath.Join(dir, "config.yml"), nil }
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("secret_backend: keyring\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRuntimeConfig(); err != nil {
		t.Fatalf("clean config should load, got: %v", err)
	}
}

// TestSecretsCLI_ConfigBackendRoundTrip exercises the retargeted (non-kdbx)
// `ov secrets set` path against the config-file backend and confirms the value
// is retrievable through the active store and enumerable via the list helper.
func TestSecretsCLI_ConfigBackendRoundTrip(t *testing.T) {
	cleanup := setupIsolatedConfigStore(t)
	defer cleanup()

	set := &SecretsSetCmd{Service: "ov/secret", Key: "R10_PROBE", Value: "hello"}
	if err := set.Run(); err != nil {
		t.Fatalf("set: %v", err)
	}

	val, err := DefaultCredentialStore().Get("ov/secret", "R10_PROBE")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != "hello" {
		t.Errorf("round-trip value = %q, want %q", val, "hello")
	}

	names, err := collectCredentialNames()
	if err != nil {
		t.Fatalf("collectCredentialNames: %v", err)
	}
	found := false
	for _, n := range names {
		if n.Service == "ov/secret" && n.Key == "R10_PROBE" {
			found = true
		}
	}
	if !found {
		t.Errorf("collectCredentialNames did not include the stored credential: %+v", names)
	}
}
