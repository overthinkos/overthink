package main

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestKdbx(t *testing.T) (store *KdbxStore, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")

	// Override password prompt
	origAsk := kdbxAskPassword
	kdbxAskPassword = func() (string, error) { return "testpass", nil }

	if err := CreateKdbxDatabase(dbPath, "testpass"); err != nil {
		t.Fatalf("CreateKdbxDatabase: %v", err)
	}

	store = &KdbxStore{path: dbPath, cachedPass: "testpass"}
	store.passOnce.Do(func() {}) // mark password as already resolved

	return store, func() { kdbxAskPassword = origAsk }
}

func TestKdbxStore_Roundtrip(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	if err := store.Set(CredServiceVNC, "my-image", "secret123"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := store.Get(CredServiceVNC, "my-image")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "secret123" {
		t.Errorf("Get = %q, want %q", val, "secret123")
	}
}

func TestKdbxStore_GetNotFound(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	val, err := store.Get(CredServiceVNC, "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "" {
		t.Errorf("Get = %q, want empty", val)
	}
}

func TestKdbxStore_Delete(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	store.Set(CredServiceVNC, "to-delete", "val")
	if err := store.Delete(CredServiceVNC, "to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	val, _ := store.Get(CredServiceVNC, "to-delete")
	if val != "" {
		t.Errorf("Get after Delete = %q, want empty", val)
	}
}

func TestKdbxStore_List(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	store.Set(CredServiceVNC, "image-b", "pw1")
	store.Set(CredServiceVNC, "image-a", "pw2")

	keys, err := store.List(CredServiceVNC)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("List len = %d, want 2", len(keys))
	}
	// Should be sorted
	if keys[0] != "image-a" || keys[1] != "image-b" {
		t.Errorf("List = %v, want [image-a, image-b]", keys)
	}
}

func TestKdbxStore_GroupCreation(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	// Custom service should auto-create groups
	if err := store.Set("ov/custom-service", "my-key", "myval"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, err := store.Get("ov/custom-service", "my-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val != "myval" {
		t.Errorf("Get = %q, want %q", val, "myval")
	}
}

func TestKdbxStore_Overwrite(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	store.Set(CredServiceVNC, "my-image", "first")
	store.Set(CredServiceVNC, "my-image", "second")

	val, _ := store.Get(CredServiceVNC, "my-image")
	if val != "second" {
		t.Errorf("Get = %q, want %q", val, "second")
	}
}

func TestKdbxStore_MultipleServices(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	store.Set(CredServiceVNC, "img", "vnc-pass")
	store.Set(CredServiceSunshineUser, "img", "sun-user")
	store.Set(CredServiceSunshinePassword, "img", "sun-pass")

	v1, _ := store.Get(CredServiceVNC, "img")
	v2, _ := store.Get(CredServiceSunshineUser, "img")
	v3, _ := store.Get(CredServiceSunshinePassword, "img")

	if v1 != "vnc-pass" {
		t.Errorf("vnc = %q", v1)
	}
	if v2 != "sun-user" {
		t.Errorf("sunshine-user = %q", v2)
	}
	if v3 != "sun-pass" {
		t.Errorf("sunshine-password = %q", v3)
	}
}

func TestKdbxStore_Name(t *testing.T) {
	store := &KdbxStore{}
	if store.Name() != "kdbx" {
		t.Errorf("Name() = %q, want %q", store.Name(), "kdbx")
	}
}

func TestKdbxStore_Probe(t *testing.T) {
	store, cleanup := setupTestKdbx(t)
	defer cleanup()

	if err := store.Probe(); err != nil {
		t.Errorf("Probe on existing file: %v", err)
	}

	missing := &KdbxStore{path: "/nonexistent/path.kdbx"}
	if err := missing.Probe(); err == nil {
		t.Error("Probe on missing file should fail")
	}

	empty := &KdbxStore{}
	if err := empty.Probe(); err == nil {
		t.Error("Probe with no path should fail")
	}
}

func TestDefaultCredentialStore_Kdbx(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	configPath := filepath.Join(dir, "config.yml")

	origAsk := kdbxAskPassword
	kdbxAskPassword = func() (string, error) { return "testpass", nil }
	defer func() { kdbxAskPassword = origAsk }()

	CreateKdbxDatabase(dbPath, "testpass")

	orig := RuntimeConfigPath
	RuntimeConfigPath = func() (string, error) { return configPath, nil }
	defer func() { RuntimeConfigPath = orig }()
	resetDefaultStore()
	defer resetDefaultStore()

	t.Setenv("OV_SECRET_BACKEND", "kdbx")
	t.Setenv("OV_KDBX_PATH", dbPath)

	store := DefaultCredentialStore()
	if store.Name() != "kdbx" {
		t.Errorf("DefaultCredentialStore().Name() = %q, want %q", store.Name(), "kdbx")
	}
}

func TestAutoDetection_KdbxFallback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	configPath := filepath.Join(dir, "config.yml")

	origAsk := kdbxAskPassword
	kdbxAskPassword = func() (string, error) { return "testpass", nil }
	defer func() { kdbxAskPassword = origAsk }()

	CreateKdbxDatabase(dbPath, "testpass")

	orig := RuntimeConfigPath
	RuntimeConfigPath = func() (string, error) { return configPath, nil }
	defer func() { RuntimeConfigPath = orig }()
	resetDefaultStore()
	defer resetDefaultStore()

	// Auto mode with kdbx configured but no keyring
	os.Unsetenv("OV_SECRET_BACKEND")
	t.Setenv("OV_KDBX_PATH", dbPath)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "") // disable keyring

	store := DefaultCredentialStore()
	// Should be either "kdbx" (if keyring probe fails) or "keyring" (if running on a desktop)
	// We can't force the keyring to fail in unit tests, so just verify it doesn't crash
	if store == nil {
		t.Fatal("DefaultCredentialStore() returned nil")
	}
	t.Logf("Auto-detected backend: %s", store.Name())
}
