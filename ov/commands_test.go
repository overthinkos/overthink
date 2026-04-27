package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImageConfigSetupCmd_DirectModeAllowed — the 2026-04-27 cutover
// added direct-mode support to `ov config`. The pre-cutover version of
// this test (TestImageConfigSetupCmd_DirectModeError) asserted that
// direct mode hard-errored at the run_mode gate; that gate is now a
// switch that accepts both modes. The setup command will still fail
// downstream of the gate (no real image, no podman), but the failure
// must NOT be the "run_mode=quadlet" gate error. Anything else means
// the gate accepted direct mode as expected.
func TestImageConfigSetupCmd_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_AUTO_ENABLE")
	os.Setenv("OV_RUN_MODE", "direct")
	defer os.Unsetenv("OV_RUN_MODE")

	cmd := &ImageConfigSetupCmd{Image: "fedora-test"}
	err := cmd.Run()
	// Some error is expected (image not pulled, etc.) but it must NOT
	// be the run_mode=quadlet gate error.
	if err != nil && strings.Contains(err.Error(), "ov config requires run_mode=quadlet (current") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}

// TestImageConfigRemoveCmd_DirectModeAllowed — same logic for remove.
// Direct-mode remove should NOT hit the run_mode gate; it routes
// through the direct-deploy branch (podman stop + rm + marker cleanup).
func TestImageConfigRemoveCmd_DirectModeAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Setenv("OV_RUN_MODE", "direct")
	defer os.Unsetenv("OV_RUN_MODE")

	cmd := &ImageConfigRemoveCmd{Image: "fedora-test"}
	err := cmd.Run()
	// Direct-mode remove of a non-existent deploy is best-effort —
	// podman rm prints a warning but Run() returns nil. The pre-cutover
	// "run_mode=quadlet required" hard error must NOT fire.
	if err != nil && strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("direct mode should be accepted; got gate error: %v", err)
	}
}
