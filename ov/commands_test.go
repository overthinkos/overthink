package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageConfigSetupCmd_DirectModeError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_AUTO_ENABLE")
	// Explicitly force direct mode (default is now auto → quadlet when podman+systemctl present)
	os.Setenv("OV_RUN_MODE", "direct")
	defer os.Unsetenv("OV_RUN_MODE")

	cmd := &ImageConfigSetupCmd{Image: "fedora-test"}
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error for config in direct mode")
	}
	if !strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("error should mention run_mode=quadlet, got: %v", err)
	}
}

func TestImageConfigRemoveCmd_DirectModeError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	// Explicitly force direct mode (default is now auto → quadlet when podman+systemctl present)
	os.Setenv("OV_RUN_MODE", "direct")
	defer os.Unsetenv("OV_RUN_MODE")

	cmd := &ImageConfigRemoveCmd{Image: "fedora-test"}
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error for config remove in direct mode")
	}
	if !strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("error should mention run_mode=quadlet, got: %v", err)
	}
}
