package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppiumSession_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	want := &AppiumSession{
		SessionID: "37e8f3c1-a9b2-4d8e-b6c5-9a4f7c8b1e2d",
		BaseURL:   "http://127.0.0.1:35001/wd/hub",
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		Image:     "android-emulator",
		Instance:  "",
		Caps:      map[string]any{"platformName": "Android"},
	}
	if err := saveAppiumSession(want); err != nil {
		t.Fatalf("saveAppiumSession: %v", err)
	}

	// File should land at the XDG-honoured path with mode 0600.
	path := filepath.Join(dir, "charly", "appium", "sessions", "android-emulator.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("session file perm = %v, want 0600 (session id is a bearer token)", perm)
	}

	got, err := loadAppiumSession("android-emulator", "")
	if err != nil {
		t.Fatalf("loadAppiumSession: %v", err)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, want.SessionID)
	}
	if got.BaseURL != want.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, want.BaseURL)
	}
	if got.Image != want.Image {
		t.Errorf("Image = %q, want %q", got.Image, want.Image)
	}
	if got.Caps["platformName"] != "Android" {
		t.Errorf("Caps round-trip lost platformName: got %v", got.Caps)
	}
}

func TestAppiumSession_InstanceSuffix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	// Two sessions for the same image with different instances must land
	// in different files — instance suffix proves the disambiguation.
	a := &AppiumSession{SessionID: "AAA", BaseURL: "http://x:1/wd/hub", Image: "img", Instance: "i1"}
	b := &AppiumSession{SessionID: "BBB", BaseURL: "http://x:2/wd/hub", Image: "img", Instance: "i2"}
	if err := saveAppiumSession(a); err != nil {
		t.Fatalf("save a: %v", err)
	}
	if err := saveAppiumSession(b); err != nil {
		t.Fatalf("save b: %v", err)
	}
	gotA, _ := loadAppiumSession("img", "i1")
	gotB, _ := loadAppiumSession("img", "i2")
	if gotA == nil || gotA.SessionID != "AAA" {
		t.Errorf("instance i1 round-trip failed: %+v", gotA)
	}
	if gotB == nil || gotB.SessionID != "BBB" {
		t.Errorf("instance i2 round-trip failed: %+v", gotB)
	}
}

func TestAppiumSession_LoadAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	// loadAppiumSession on a missing file returns (nil, nil) so callers
	// can distinguish "no session" from "broken file".
	got, err := loadAppiumSession("nonexistent", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil session for absent file, got %+v", got)
	}
}

func TestAppiumSession_DeleteAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	// deleteAppiumSession on a missing file is a no-op (no error). This
	// makes session-delete idempotent: re-running it on an already-clean
	// state must succeed.
	if err := deleteAppiumSession("nonexistent", ""); err != nil {
		t.Errorf("delete absent should be no-op, got %v", err)
	}
}

func TestAppiumSession_PathRespectsXDG(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", custom)

	path, err := appiumSessionPath("foo", "")
	if err != nil {
		t.Fatalf("appiumSessionPath: %v", err)
	}
	wantPrefix := filepath.Join(custom, "charly", "appium", "sessions")
	if filepath.Dir(path) != wantPrefix {
		t.Errorf("session path = %s; expected to be under XDG_CACHE_HOME=%s", path, wantPrefix)
	}
}
