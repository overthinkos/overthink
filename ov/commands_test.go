package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnableCmd_DirectModeError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_RUN_MODE")
	os.Unsetenv("OV_AUTO_ENABLE")

	// run_mode defaults to "direct"
	cmd := &EnableCmd{Image: "fedora-test"}
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error for enable in direct mode")
	}
	if !strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("error should mention run_mode=quadlet, got: %v", err)
	}
}

func TestDisableCmd_DirectModeError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_RUN_MODE")

	cmd := &DisableCmd{Image: "fedora-test"}
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error for disable in direct mode")
	}
	if !strings.Contains(err.Error(), "run_mode=quadlet") {
		t.Errorf("error should mention run_mode=quadlet, got: %v", err)
	}
}
