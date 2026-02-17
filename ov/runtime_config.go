package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// RuntimeConfig represents the user-level runtime configuration (~/.config/ov/config.yml)
type RuntimeConfig struct {
	Engine     EngineConfig `yaml:"engine"`
	RunMode    string       `yaml:"run_mode,omitempty"`
	AutoEnable *bool        `yaml:"auto_enable,omitempty"`
}

// EngineConfig specifies which container engine to use
type EngineConfig struct {
	Build string `yaml:"build,omitempty"`
	Run   string `yaml:"run,omitempty"`
}

// ResolvedRuntime holds the fully resolved runtime configuration
type ResolvedRuntime struct {
	BuildEngine string // "docker" or "podman"
	RunEngine   string // "docker" or "podman"
	RunMode     string // "direct" or "quadlet"
	AutoEnable  bool   // auto-enable quadlet on first start
}

// RuntimeConfigPath returns the path to the user's runtime config file.
var RuntimeConfigPath = defaultRuntimeConfigPath

func defaultRuntimeConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "ov", "config.yml"), nil
}

// LoadRuntimeConfig reads the runtime config file. Returns zero-value config if missing.
func LoadRuntimeConfig() (*RuntimeConfig, error) {
	path, err := RuntimeConfigPath()
	if err != nil {
		return &RuntimeConfig{}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RuntimeConfig{}, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var cfg RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveRuntimeConfig writes the runtime config file, creating directories as needed.
func SaveRuntimeConfig(cfg *RuntimeConfig) error {
	path, err := RuntimeConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// ResolveRuntime resolves the runtime configuration: env vars > config file > defaults.
func ResolveRuntime() (*ResolvedRuntime, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}

	rt := &ResolvedRuntime{
		BuildEngine: resolveValue(os.Getenv("OV_BUILD_ENGINE"), cfg.Engine.Build, "docker"),
		RunEngine:   resolveValue(os.Getenv("OV_RUN_ENGINE"), cfg.Engine.Run, "docker"),
		RunMode:     resolveValue(os.Getenv("OV_RUN_MODE"), cfg.RunMode, "direct"),
		AutoEnable:  resolveAutoEnable(os.Getenv("OV_AUTO_ENABLE"), cfg.AutoEnable),
	}

	if err := validateEngine(rt.BuildEngine, "engine.build"); err != nil {
		return nil, err
	}
	if err := validateEngine(rt.RunEngine, "engine.run"); err != nil {
		return nil, err
	}
	if err := validateRunMode(rt.RunMode); err != nil {
		return nil, err
	}

	if rt.RunMode == "quadlet" && rt.RunEngine != "podman" {
		fmt.Fprintf(os.Stderr, "Warning: run_mode=quadlet requires podman; engine.run=%s\n", rt.RunEngine)
	}

	return rt, nil
}

// resolveValue returns the first non-empty value from the chain.
func resolveValue(envVal, cfgVal, defaultVal string) string {
	if envVal != "" {
		return envVal
	}
	if cfgVal != "" {
		return cfgVal
	}
	return defaultVal
}

func validateEngine(value, field string) error {
	if value != "docker" && value != "podman" {
		return fmt.Errorf("%s must be \"docker\" or \"podman\", got %q", field, value)
	}
	return nil
}

func validateRunMode(value string) error {
	if value != "direct" && value != "quadlet" {
		return fmt.Errorf("run_mode must be \"direct\" or \"quadlet\", got %q", value)
	}
	return nil
}

func resolveAutoEnable(envVal string, cfgVal *bool) bool {
	if envVal != "" {
		return envVal == "true" || envVal == "1"
	}
	if cfgVal != nil {
		return *cfgVal
	}
	return false
}

// GetConfigValue returns the value for a dot-notation key from the config file.
func GetConfigValue(key string) (string, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return "", err
	}

	switch key {
	case "engine.build":
		return cfg.Engine.Build, nil
	case "engine.run":
		return cfg.Engine.Run, nil
	case "run_mode":
		return cfg.RunMode, nil
	case "auto_enable":
		if cfg.AutoEnable != nil {
			if *cfg.AutoEnable {
				return "true", nil
			}
			return "false", nil
		}
		return "", nil
	default:
		return "", fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, run_mode, auto_enable)", key)
	}
}

// SetConfigValue sets a value for a dot-notation key in the config file.
func SetConfigValue(key, value string) error {
	// Validate value before writing
	switch key {
	case "engine.build", "engine.run":
		if err := validateEngine(value, key); err != nil {
			return err
		}
	case "run_mode":
		if err := validateRunMode(value); err != nil {
			return err
		}
	case "auto_enable":
		if value != "true" && value != "false" {
			return fmt.Errorf("auto_enable must be \"true\" or \"false\", got %q", value)
		}
	default:
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, run_mode, auto_enable)", key)
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}

	switch key {
	case "engine.build":
		cfg.Engine.Build = value
	case "engine.run":
		cfg.Engine.Run = value
	case "run_mode":
		cfg.RunMode = value
	case "auto_enable":
		b := value == "true"
		cfg.AutoEnable = &b
	}

	return SaveRuntimeConfig(cfg)
}

// ResetConfigValue removes a key from the config file (reverts to default).
// If key is empty, resets the entire config.
func ResetConfigValue(key string) error {
	if key == "" {
		// Reset entire config
		return SaveRuntimeConfig(&RuntimeConfig{})
	}

	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}

	switch key {
	case "engine.build":
		cfg.Engine.Build = ""
	case "engine.run":
		cfg.Engine.Run = ""
	case "run_mode":
		cfg.RunMode = ""
	case "auto_enable":
		cfg.AutoEnable = nil
	default:
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, run_mode, auto_enable)", key)
	}

	return SaveRuntimeConfig(cfg)
}

// configKeySource describes where a config value comes from.
type configKeySource struct {
	Key      string
	Value    string
	Source   string // "env", "config", "default"
}

// ListConfigValues returns all config keys with their resolved values and sources.
func ListConfigValues() ([]configKeySource, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}

	resolve := func(key, envName, cfgVal, defaultVal string) configKeySource {
		envVal := os.Getenv(envName)
		if envVal != "" {
			return configKeySource{Key: key, Value: envVal, Source: "env (" + envName + ")"}
		}
		if cfgVal != "" {
			return configKeySource{Key: key, Value: cfgVal, Source: "config"}
		}
		return configKeySource{Key: key, Value: defaultVal, Source: "default"}
	}

	// Resolve auto_enable separately since it's a bool pointer
	autoEnableEntry := func() configKeySource {
		envVal := os.Getenv("OV_AUTO_ENABLE")
		if envVal != "" {
			resolved := "false"
			if envVal == "true" || envVal == "1" {
				resolved = "true"
			}
			return configKeySource{Key: "auto_enable", Value: resolved, Source: "env (OV_AUTO_ENABLE)"}
		}
		if cfg.AutoEnable != nil {
			val := "false"
			if *cfg.AutoEnable {
				val = "true"
			}
			return configKeySource{Key: "auto_enable", Value: val, Source: "config"}
		}
		return configKeySource{Key: "auto_enable", Value: "false", Source: "default"}
	}

	return []configKeySource{
		resolve("engine.build", "OV_BUILD_ENGINE", cfg.Engine.Build, "docker"),
		resolve("engine.run", "OV_RUN_ENGINE", cfg.Engine.Run, "docker"),
		resolve("run_mode", "OV_RUN_MODE", cfg.RunMode, "direct"),
		autoEnableEntry(),
	}, nil
}
