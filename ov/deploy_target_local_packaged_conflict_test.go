package main

// deploy_target_local_packaged_conflict_test.go — unit tests for
// detectPackagedUnitConflict. Verifies the LocalDeployTarget refuses to
// silently override OS-package-shipped systemd units, and that scope=user
// or non-conflicting names pass through cleanly.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withPackagedUnitDirs swaps the lookup paths for the duration of a test
// and restores them on cleanup. Tests use a fixture root under t.TempDir
// to simulate a host with packaged units installed.
func withPackagedUnitDirs(t *testing.T, dirs ...string) {
	t.Helper()
	original := packagedUnitDirs
	packagedUnitDirs = dirs
	t.Cleanup(func() { packagedUnitDirs = original })
}

func TestDetectPackagedUnitConflict_DetectsConflict(t *testing.T) {
	root := t.TempDir()
	usrLib := filepath.Join(root, "usr", "lib", "systemd", "system")
	if err := os.MkdirAll(usrLib, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	packagedPath := filepath.Join(usrLib, "virtqemud.service")
	if err := os.WriteFile(packagedPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withPackagedUnitDirs(t, usrLib, filepath.Join(root, "lib", "systemd", "system"))

	err := detectPackagedUnitConflict(
		"/etc/systemd/system/virtqemud.service",
		ScopeSystem,
		"virtualization",
	)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"virtqemud.service",
		"virtualization",
		packagedPath,
		"use_packaged: virtqemud.service",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q\nfull error: %s", want, msg)
		}
	}
}

func TestDetectPackagedUnitConflict_AllowsCustomNames(t *testing.T) {
	root := t.TempDir()
	usrLib := filepath.Join(root, "usr", "lib", "systemd", "system")
	if err := os.MkdirAll(usrLib, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	withPackagedUnitDirs(t, usrLib, filepath.Join(root, "lib", "systemd", "system"))

	err := detectPackagedUnitConflict(
		"/etc/systemd/system/cdp-proxy.service",
		ScopeSystem,
		"chrome-cdp",
	)
	if err != nil {
		t.Fatalf("unexpected error for non-conflicting custom unit: %v", err)
	}
}

func TestDetectPackagedUnitConflict_SkipsForScopeUser(t *testing.T) {
	root := t.TempDir()
	usrLib := filepath.Join(root, "usr", "lib", "systemd", "system")
	if err := os.MkdirAll(usrLib, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usrLib, "virtqemud.service"), []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withPackagedUnitDirs(t, usrLib, filepath.Join(root, "lib", "systemd", "system"))

	// User-scope writes go to ~/.config/systemd/user/, never collide with
	// packaged system units.
	err := detectPackagedUnitConflict(
		"/home/user/.config/systemd/user/virtqemud.service",
		ScopeUser,
		"virtualization",
	)
	if err != nil {
		t.Fatalf("user-scope must not flag conflict: %v", err)
	}
}

func TestDetectPackagedUnitConflict_DetectsLibFallback(t *testing.T) {
	root := t.TempDir()
	libOnly := filepath.Join(root, "lib", "systemd", "system")
	if err := os.MkdirAll(libOnly, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	packagedPath := filepath.Join(libOnly, "foo.service")
	if err := os.WriteFile(packagedPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	withPackagedUnitDirs(t,
		filepath.Join(root, "usr", "lib", "systemd", "system"), // missing
		libOnly,
	)

	err := detectPackagedUnitConflict(
		"/etc/systemd/system/foo.service",
		ScopeSystem,
		"foo-layer",
	)
	if err == nil {
		t.Fatal("expected conflict error from /lib/systemd/system fallback path, got nil")
	}
	if !strings.Contains(err.Error(), packagedPath) {
		t.Errorf("error should reference fallback path %s; got: %s", packagedPath, err.Error())
	}
}
