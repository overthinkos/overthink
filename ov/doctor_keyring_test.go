package main

import (
	"strings"
	"testing"
)

// TestCheckKeyringHealth_NoBus verifies that checkKeyringHealth returns nil
// (skips) when there's no session bus available. The "Secret backend"
// check above it already covers that case, so duplicating would be noise.
func TestCheckKeyringHealth_NoBus(t *testing.T) {
	// Force newSSClient to fail by pointing DBUS_SESSION_BUS_ADDRESS at a
	// non-existent socket.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent-dbus-socket-for-test")
	results := checkKeyringHealth()
	if results != nil {
		t.Errorf("expected nil (skip), got %+v", results)
	}
}

// TestCheckKeyringIndexConsistency_EmptyIndex verifies that the consistency
// check returns nil when the shadow index is empty — no indexed keys means
// nothing to verify.
func TestCheckKeyringIndexConsistency_EmptyIndex(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return dir + "/config.yml", nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	results := checkKeyringIndexConsistency()
	if results != nil {
		t.Errorf("expected nil for empty index, got %+v", results)
	}
}

// TestCheckKeyringIndexConsistency_NoBus verifies that the consistency
// check returns nil when there's no session bus.
func TestCheckKeyringIndexConsistency_NoBus(t *testing.T) {
	dir := t.TempDir()
	RuntimeConfigPath = func() (string, error) {
		return dir + "/config.yml", nil
	}
	defer func() { RuntimeConfigPath = defaultRuntimeConfigPath }()

	// Seed a shadow index entry so the early-empty return doesn't fire.
	cfg := &RuntimeConfig{
		KeyringKeys: []string{"ov/enc/testimg"},
	}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("seeding config: %v", err)
	}

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent-dbus-socket-for-test")
	results := checkKeyringIndexConsistency()
	if results != nil {
		t.Errorf("expected nil (no bus → skip), got %+v", results)
	}
}

// TestCheckKeyringHealth_ReportsBrokenCollection_Integration runs against
// the REAL session bus if one is available. On this dev host it reports
// the broken /atrawog KeePassXC stub. Skips if no session bus or no
// collections. This is a soft check — it documents the expected output
// shape without enforcing specific paths.
func TestCheckKeyringHealth_ReportsBrokenCollection_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test — run with go test (no -short)")
	}
	c, err := newSSClient()
	if err != nil {
		t.Skipf("no session bus available: %v", err)
	}
	defer c.close()
	paths, err := c.collections()
	if err != nil {
		t.Skipf("cannot list collections: %v", err)
	}
	if len(paths) == 0 {
		t.Skip("no collections on this host")
	}

	results := checkKeyringHealth()
	if len(results) == 0 {
		t.Fatal("expected at least one result when collections exist")
	}
	r := results[0]
	if r.Name != "Secret Service collections" {
		t.Errorf("name = %q, want 'Secret Service collections'", r.Name)
	}
	// Status is CheckOK or CheckWarning depending on the host's current
	// state; both are valid. Just check the result has a meaningful detail.
	if r.Status != CheckOK && r.Status != CheckWarning {
		t.Errorf("status = %v, want CheckOK or CheckWarning", r.Status)
	}
	if r.Status == CheckWarning && !strings.Contains(r.Detail, "Broken") {
		t.Errorf("warning result should mention 'Broken', got detail: %q", r.Detail)
	}
	t.Logf("checkKeyringHealth result: status=%v version=%q detail=%q",
		r.Status, r.Version, r.Detail)
}
