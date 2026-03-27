package main

import (
	"os"
	"path/filepath"
	"testing"
)

// mockStore implements CredentialStore for testing.
type mockStore struct {
	name    string
	data    map[string]map[string]string // service -> key -> value
	setErr  error
}

func newMockStore(name string) *mockStore {
	return &mockStore{name: name, data: make(map[string]map[string]string)}
}

func (m *mockStore) Get(service, key string) (string, error) {
	if svc, ok := m.data[service]; ok {
		return svc[key], nil
	}
	return "", nil
}

func (m *mockStore) Set(service, key, value string) error {
	if m.setErr != nil {
		return m.setErr
	}
	if m.data[service] == nil {
		m.data[service] = make(map[string]string)
	}
	m.data[service][key] = value
	return nil
}

func (m *mockStore) Delete(service, key string) error {
	if svc, ok := m.data[service]; ok {
		delete(svc, key)
	}
	return nil
}

func (m *mockStore) List(service string) ([]string, error) {
	var keys []string
	if svc, ok := m.data[service]; ok {
		for k := range svc {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (m *mockStore) Name() string {
	return m.name
}

func TestResolveCredentialEnvOverride(t *testing.T) {
	t.Setenv("TEST_CRED_ENV", "from-env")

	val, source := ResolveCredential("TEST_CRED_ENV", "ov/test", "key1", "fallback")
	if val != "from-env" || source != "env" {
		t.Errorf("expected (from-env, env), got (%s, %s)", val, source)
	}
}

func TestResolveCredentialDefault(t *testing.T) {
	// Ensure no env var set
	os.Unsetenv("TEST_CRED_NONE")

	// Use config backend (no keyring in tests) with empty config
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()
	resetDefaultStore()
	defer resetDefaultStore()

	t.Setenv("OV_SECRET_BACKEND", "config")

	val, source := ResolveCredential("TEST_CRED_NONE", "ov/test", "nonexistent", "default-val")
	if val != "default-val" || source != "default" {
		t.Errorf("expected (default-val, default), got (%s, %s)", val, source)
	}
}

func TestConfigFileStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	store := &ConfigFileStore{}

	// Set and Get for each service type
	tests := []struct {
		service string
		key     string
		value   string
	}{
		{CredServiceVNC, "my-image", "vncpass123"},
	}

	for _, tt := range tests {
		if err := store.Set(tt.service, tt.key, tt.value); err != nil {
			t.Fatalf("Set(%s, %s): %v", tt.service, tt.key, err)
		}
		got, err := store.Get(tt.service, tt.key)
		if err != nil {
			t.Fatalf("Get(%s, %s): %v", tt.service, tt.key, err)
		}
		if got != tt.value {
			t.Errorf("Get(%s, %s) = %q, want %q", tt.service, tt.key, got, tt.value)
		}
	}

	// List
	keys, err := store.List(CredServiceVNC)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "my-image" {
		t.Errorf("List(ov/vnc) = %v, want [my-image]", keys)
	}

	// Delete
	if err := store.Delete(CredServiceVNC, "my-image"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := store.Get(CredServiceVNC, "my-image")
	if got != "" {
		t.Errorf("Get after Delete = %q, want empty", got)
	}
}

func TestConfigFileStoreName(t *testing.T) {
	store := &ConfigFileStore{}
	if store.Name() != "config" {
		t.Errorf("Name() = %q, want %q", store.Name(), "config")
	}
}

func TestHasPlaintextCredentials(t *testing.T) {
	cfg := &RuntimeConfig{
		VncPasswords: map[string]string{"img1": "pw1"},
	}
	if n := HasPlaintextCredentials(cfg); n != 1 {
		t.Errorf("HasPlaintextCredentials = %d, want 1", n)
	}
}

func TestPlaintextCredentialEntries(t *testing.T) {
	cfg := &RuntimeConfig{
		VncPasswords: map[string]string{"img1": "pw1"},
	}
	entries := PlaintextCredentialEntries(cfg)
	if len(entries) != 1 {
		t.Fatalf("PlaintextCredentialEntries len = %d, want 1", len(entries))
	}
	if entries[0].Service != CredServiceVNC || entries[0].Key != "img1" || entries[0].Value != "pw1" {
		t.Errorf("unexpected entry: %+v", entries[0])
	}
}

func TestResolveSecretBackendEnv(t *testing.T) {
	t.Setenv("OV_SECRET_BACKEND", "keyring")
	if got := resolveSecretBackend(); got != "keyring" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "keyring")
	}
}

func TestResolveSecretBackendConfig(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	// Write config with secret_backend
	cfg := &RuntimeConfig{SecretBackend: "config"}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig: %v", err)
	}

	os.Unsetenv("OV_SECRET_BACKEND")
	if got := resolveSecretBackend(); got != "config" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "config")
	}
}

func TestResolveSecretBackendDefault(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(dir, "config.yml"), nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	os.Unsetenv("OV_SECRET_BACKEND")
	if got := resolveSecretBackend(); got != "auto" {
		t.Errorf("resolveSecretBackend() = %q, want %q", got, "auto")
	}
}
