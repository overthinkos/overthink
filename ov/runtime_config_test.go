package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfig_Missing(t *testing.T) {
	// Point to a non-existent path
	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()

	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "nonexistent", "config.yml"), nil
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		t.Fatalf("expected nil error for missing config, got: %v", err)
	}
	if cfg.Engine.Build != "" || cfg.Engine.Run != "" || cfg.RunMode != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestSaveAndLoadRuntimeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	cfg := &RuntimeConfig{
		Engine:  EngineConfig{Build: "podman", Run: "docker"},
		RunMode: "quadlet",
	}
	if err := SaveRuntimeConfig(cfg); err != nil {
		t.Fatalf("SaveRuntimeConfig() error: %v", err)
	}

	loaded, err := LoadRuntimeConfig()
	if err != nil {
		t.Fatalf("LoadRuntimeConfig() error: %v", err)
	}
	if loaded.Engine.Build != "podman" {
		t.Errorf("Engine.Build = %q, want %q", loaded.Engine.Build, "podman")
	}
	if loaded.Engine.Run != "docker" {
		t.Errorf("Engine.Run = %q, want %q", loaded.Engine.Run, "docker")
	}
	if loaded.RunMode != "quadlet" {
		t.Errorf("RunMode = %q, want %q", loaded.RunMode, "quadlet")
	}
}

func TestResolveRuntime_Defaults(t *testing.T) {
	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	// Ensure env vars are clear
	for _, key := range []string{"OV_BUILD_ENGINE", "OV_RUN_ENGINE", "OV_RUN_MODE"} {
		os.Unsetenv(key)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BuildEngine != "docker" {
		t.Errorf("BuildEngine = %q, want %q", rt.BuildEngine, "docker")
	}
	if rt.RunEngine != "docker" {
		t.Errorf("RunEngine = %q, want %q", rt.RunEngine, "docker")
	}
	if rt.RunMode != "direct" {
		t.Errorf("RunMode = %q, want %q", rt.RunMode, "direct")
	}
}

func TestResolveRuntime_EnvOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	// Write config with podman
	cfg := &RuntimeConfig{Engine: EngineConfig{Build: "podman"}}
	SaveRuntimeConfig(cfg)

	// Set env to override
	os.Setenv("OV_BUILD_ENGINE", "docker")
	defer os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_RUN_MODE")

	rt, err := ResolveRuntime()
	if err != nil {
		t.Fatalf("ResolveRuntime() error: %v", err)
	}
	if rt.BuildEngine != "docker" {
		t.Errorf("BuildEngine = %q, want %q (env should override config)", rt.BuildEngine, "docker")
	}
}

func TestResolveRuntime_InvalidEngine(t *testing.T) {
	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	os.Setenv("OV_BUILD_ENGINE", "containerd")
	defer os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_RUN_MODE")

	_, err := ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid engine")
	}
}

func TestResolveRuntime_InvalidRunMode(t *testing.T) {
	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Setenv("OV_RUN_MODE", "swarm")
	defer os.Unsetenv("OV_RUN_MODE")

	_, err := ResolveRuntime()
	if err == nil {
		t.Error("expected error for invalid run_mode")
	}
}

func TestSetConfigValue_Validates(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	err := SetConfigValue("engine.build", "containerd")
	if err == nil {
		t.Error("expected error for invalid engine value")
	}

	err = SetConfigValue("run_mode", "swarm")
	if err == nil {
		t.Error("expected error for invalid run_mode value")
	}

	err = SetConfigValue("engine.build", "podman")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := GetConfigValue("engine.build")
	if err != nil {
		t.Fatalf("GetConfigValue() error: %v", err)
	}
	if val != "podman" {
		t.Errorf("GetConfigValue() = %q, want %q", val, "podman")
	}
}

func TestResetConfigValue(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	SetConfigValue("engine.build", "podman")
	ResetConfigValue("engine.build")

	val, _ := GetConfigValue("engine.build")
	if val != "" {
		t.Errorf("after reset, GetConfigValue() = %q, want empty", val)
	}
}

func TestResetConfigValue_All(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	SetConfigValue("engine.build", "podman")
	SetConfigValue("run_mode", "quadlet")
	ResetConfigValue("")

	cfg, _ := LoadRuntimeConfig()
	if cfg.Engine.Build != "" || cfg.RunMode != "" {
		t.Errorf("after full reset, config should be empty, got %+v", cfg)
	}
}

func TestListConfigValues(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yml")

	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) { return configPath, nil }

	os.Unsetenv("OV_BUILD_ENGINE")
	os.Unsetenv("OV_RUN_ENGINE")
	os.Unsetenv("OV_RUN_MODE")

	SetConfigValue("engine.build", "podman")

	vals, err := ListConfigValues()
	if err != nil {
		t.Fatalf("ListConfigValues() error: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 values, got %d", len(vals))
	}

	// engine.build should come from config
	if vals[0].Key != "engine.build" || vals[0].Value != "podman" || vals[0].Source != "config" {
		t.Errorf("engine.build entry: %+v", vals[0])
	}
	// engine.run should be default
	if vals[1].Key != "engine.run" || vals[1].Value != "docker" || vals[1].Source != "default" {
		t.Errorf("engine.run entry: %+v", vals[1])
	}
}

func TestGetConfigValue_UnknownKey(t *testing.T) {
	orig := RuntimeConfigPath
	defer func() { RuntimeConfigPath = orig }()
	RuntimeConfigPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "config.yml"), nil
	}

	_, err := GetConfigValue("foo.bar")
	if err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestResolveValue(t *testing.T) {
	tests := []struct {
		env, cfg, def, want string
	}{
		{"podman", "docker", "docker", "podman"},
		{"", "podman", "docker", "podman"},
		{"", "", "docker", "docker"},
	}
	for _, tt := range tests {
		got := resolveValue(tt.env, tt.cfg, tt.def)
		if got != tt.want {
			t.Errorf("resolveValue(%q, %q, %q) = %q, want %q", tt.env, tt.cfg, tt.def, got, tt.want)
		}
	}
}
