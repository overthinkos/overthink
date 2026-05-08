package main

import (
	"fmt"
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
	kdbxAskPassword = func(bypassCache bool) (string, error) { return "testpass", nil }

	if err := CreateKdbxDatabase(dbPath, "testpass"); err != nil {
		t.Fatalf("CreateKdbxDatabase: %v", err)
	}

	// cachedPass is set so the first openValidated bypasses the prompt mock
	// and goes straight to the kdbx open.
	store = &KdbxStore{path: dbPath, cachedPass: "testpass"}

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
	store.Set("ov/secret", "img", "secret-val")

	v1, _ := store.Get(CredServiceVNC, "img")
	v2, _ := store.Get("ov/secret", "img")

	if v1 != "vnc-pass" {
		t.Errorf("vnc = %q", v1)
	}
	if v2 != "secret-val" {
		t.Errorf("secret = %q", v2)
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

// TestIsWrongKdbxPassword covers the error-class matcher used to decide
// whether to retry. Substring-based because gokeepasslib does not export a
// typed sentinel; this ensures we don't retry on file-not-found / corruption /
// unrelated I/O errors.
func TestIsWrongKdbxPassword(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"wrong-password verbatim", fmt.Errorf("decoding kdbx: Wrong password? Database integrity check failed"), true},
		{"integrity-only phrasing", fmt.Errorf("Database integrity check failed"), true},
		{"wrong-password-only phrasing", fmt.Errorf("oops Wrong password thing"), true},
		{"file-not-found", fmt.Errorf("opening kdbx: open /nonexistent: no such file or directory"), false},
		{"corruption-non-pwd", fmt.Errorf("decoding kdbx: invalid header magic"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWrongKdbxPassword(tc.err); got != tc.want {
				t.Errorf("isWrongKdbxPassword(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestOpenKdbxWithRetry_TwoWrongThenCorrect simulates the user fat-fingering
// the master password twice and then typing it correctly on the third
// attempt. The kdbxAskPassword mock returns a different value each call, so
// the retry loop's bypassCache discipline is also exercised.
func TestOpenKdbxWithRetry_TwoWrongThenCorrect(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	if err := CreateKdbxDatabase(dbPath, "correct"); err != nil {
		t.Fatalf("CreateKdbxDatabase: %v", err)
	}

	calls := 0
	origAsk := kdbxAskPassword
	defer func() { kdbxAskPassword = origAsk }()
	kdbxAskPassword = func(bypassCache bool) (string, error) {
		calls++
		// First two attempts return wrong; third returns correct.
		// We also assert bypassCache is true on retries.
		switch calls {
		case 1:
			if bypassCache {
				t.Errorf("first attempt should NOT bypassCache, got bypassCache=true")
			}
			return "wrong-1", nil
		case 2:
			if !bypassCache {
				t.Errorf("second attempt SHOULD bypassCache, got bypassCache=false")
			}
			return "wrong-2", nil
		default:
			if !bypassCache {
				t.Errorf("third attempt SHOULD bypassCache, got bypassCache=false")
			}
			return "correct", nil
		}
	}

	db, pw, err := openKdbxWithRetry(dbPath, "")
	if err != nil {
		t.Fatalf("openKdbxWithRetry: %v", err)
	}
	if pw != "correct" {
		t.Errorf("returned password = %q, want %q", pw, "correct")
	}
	if db == nil {
		t.Error("returned db is nil")
	}
	if calls != 3 {
		t.Errorf("kdbxAskPassword called %d times, want 3", calls)
	}
}

// TestOpenKdbxWithRetry_AllWrong asserts we give up after exactly
// kdbxMaxPasswordAttempts (3) failed prompts and surface the wrong-password
// error to the caller.
func TestOpenKdbxWithRetry_AllWrong(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	if err := CreateKdbxDatabase(dbPath, "correct"); err != nil {
		t.Fatalf("CreateKdbxDatabase: %v", err)
	}

	calls := 0
	origAsk := kdbxAskPassword
	defer func() { kdbxAskPassword = origAsk }()
	kdbxAskPassword = func(bypassCache bool) (string, error) {
		calls++
		return "always-wrong", nil
	}

	_, _, err := openKdbxWithRetry(dbPath, "")
	if err == nil {
		t.Fatal("openKdbxWithRetry should have failed after all attempts")
	}
	if !isWrongKdbxPassword(err) {
		t.Errorf("expected wrong-password error, got: %v", err)
	}
	if calls != kdbxMaxPasswordAttempts {
		t.Errorf("kdbxAskPassword called %d times, want %d", calls, kdbxMaxPasswordAttempts)
	}
}

// TestOpenKdbxWithRetry_NonPasswordErrorNoRetry asserts we DO NOT retry on
// errors that aren't wrong-password (file not found, corruption, etc.) — those
// would just fail the same way on every attempt.
func TestOpenKdbxWithRetry_NonPasswordErrorNoRetry(t *testing.T) {
	calls := 0
	origAsk := kdbxAskPassword
	defer func() { kdbxAskPassword = origAsk }()
	kdbxAskPassword = func(bypassCache bool) (string, error) {
		calls++
		return "anything", nil
	}

	_, _, err := openKdbxWithRetry("/nonexistent/path.kdbx", "")
	if err == nil {
		t.Fatal("expected error on missing file")
	}
	if calls != 1 {
		t.Errorf("kdbxAskPassword called %d times, want 1 (no retry on non-password errors)", calls)
	}
}

// TestKdbxStore_OpenValidated_ReusesCachedPassword asserts a successful
// password is cached across calls — we only prompt once per store.
func TestKdbxStore_OpenValidated_ReusesCachedPassword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	if err := CreateKdbxDatabase(dbPath, "correct"); err != nil {
		t.Fatalf("CreateKdbxDatabase: %v", err)
	}

	calls := 0
	origAsk := kdbxAskPassword
	defer func() { kdbxAskPassword = origAsk }()
	kdbxAskPassword = func(bypassCache bool) (string, error) {
		calls++
		return "correct", nil
	}

	store := &KdbxStore{path: dbPath}
	for i := 0; i < 5; i++ {
		if _, err := store.Get("ov/test", "any"); err != nil {
			t.Fatalf("Get %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("kdbxAskPassword called %d times across 5 ops, want 1", calls)
	}
}

func TestDefaultCredentialStore_Kdbx(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.kdbx")
	configPath := filepath.Join(dir, "config.yml")

	origAsk := kdbxAskPassword
	kdbxAskPassword = func(bypassCache bool) (string, error) { return "testpass", nil }
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
	kdbxAskPassword = func(bypassCache bool) (string, error) { return "testpass", nil }
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
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/dev/null/invalid") // disable keyring (empty string causes D-Bus to try defaults and hang)

	store := DefaultCredentialStore()
	// Should be either "kdbx" (if keyring probe fails) or "keyring" (if running on a desktop)
	// We can't force the keyring to fail in unit tests, so just verify it doesn't crash
	if store == nil {
		t.Fatal("DefaultCredentialStore() returned nil")
	}
	t.Logf("Auto-detected backend: %s", store.Name())
}
