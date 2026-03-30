package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

var configPermWarningOnce sync.Once

// RuntimeConfig represents the user-level runtime configuration (~/.config/ov/config.yml)
type RuntimeConfig struct {
	Engine               EngineConfig      `yaml:"engine"`
	RunMode              string            `yaml:"run_mode,omitempty"`
	AutoEnable           *bool             `yaml:"auto_enable,omitempty"`
	BindAddress          string            `yaml:"bind_address,omitempty"`
	EncryptedStoragePath string            `yaml:"encrypted_storage_path,omitempty"`
	SecretBackend        string            `yaml:"secret_backend,omitempty"`     // "auto", "keyring", "kdbx", "config"
	SecretsKdbxPath      string            `yaml:"secrets_kdbx_path,omitempty"`  // Path to .kdbx database file
	SecretsKdbxKeyFile   string            `yaml:"secrets_kdbx_key_file,omitempty"` // Optional key file for .kdbx
	Vm                   RuntimeVmConfig   `yaml:"vm,omitempty"`
	VncPasswords         map[string]string `yaml:"vnc_passwords,omitempty"`      // VNC passwords keyed by image[-instance]
	KeyringKeys          []string          `yaml:"keyring_keys,omitempty"`       // Shadow index: names of keys stored in keyring (no values)
}

// RuntimeVmConfig holds user-level VM defaults
type RuntimeVmConfig struct {
	Backend   string `yaml:"backend,omitempty"`   // "auto", "libvirt", "qemu"
	DiskSize  string `yaml:"disk_size,omitempty"` // default disk size
	RootSize  string `yaml:"root_size,omitempty"` // root partition size
	Ram       string `yaml:"ram,omitempty"`       // default RAM
	Cpus      int    `yaml:"cpus,omitempty"`      // default CPU count
	Rootfs    string `yaml:"rootfs,omitempty"`    // root filesystem type
	Transport string `yaml:"transport,omitempty"` // image transport (registry, containers-storage)
}

// EngineConfig specifies which container engine to use
type EngineConfig struct {
	Build   string `yaml:"build,omitempty"`
	Run     string `yaml:"run,omitempty"`
	Rootful string `yaml:"rootful,omitempty"` // "auto", "machine", "sudo", "native"
}

// ResolvedRuntime holds the fully resolved runtime configuration
type ResolvedRuntime struct {
	BuildEngine          string // "docker" or "podman"
	RunEngine            string // "docker" or "podman"
	Rootful              string // "auto", "machine", "sudo", "native"
	RunMode              string // "direct" or "quadlet"
	AutoEnable           bool   // auto-enable quadlet on first start
	BindAddress          string // "127.0.0.1" or "0.0.0.0"
	EncryptedStoragePath string // path for gocryptfs encrypted storage
	VmBackend            string // "auto", "libvirt", or "qemu"
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

	// Warn once per session if config file has overly permissive permissions
	if info, statErr := os.Stat(path); statErr == nil {
		perm := info.Mode().Perm()
		if perm&0077 != 0 {
			configPermWarningOnce.Do(func() {
				fmt.Fprintf(os.Stderr, "WARNING: %s has permissions %04o (accessible by other users).\n", path, perm)
				fmt.Fprintf(os.Stderr, "This file may contain plaintext credentials.\n")
				fmt.Fprintf(os.Stderr, "Run: chmod 600 %s\n", path)
			})
		}
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

	return os.WriteFile(path, data, 0600)
}

// ResolveRuntime resolves the runtime configuration: env vars > config file > defaults.
func ResolveRuntime() (*ResolvedRuntime, error) {
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return nil, err
	}

	rt := &ResolvedRuntime{
		BuildEngine:          resolveValue(os.Getenv("OV_BUILD_ENGINE"), cfg.Engine.Build, "auto"),
		RunEngine:            resolveValue(os.Getenv("OV_RUN_ENGINE"), cfg.Engine.Run, "auto"),
		Rootful:              resolveValue(os.Getenv("OV_ENGINE_ROOTFUL"), cfg.Engine.Rootful, "auto"),
		RunMode:              resolveValue(os.Getenv("OV_RUN_MODE"), cfg.RunMode, "auto"),
		AutoEnable:           resolveAutoEnable(os.Getenv("OV_AUTO_ENABLE"), cfg.AutoEnable),
		BindAddress:          resolveValue(os.Getenv("OV_BIND_ADDRESS"), cfg.BindAddress, "127.0.0.1"),
		EncryptedStoragePath: resolveEncryptedStoragePath(os.Getenv("OV_ENCRYPTED_STORAGE_PATH"), cfg.EncryptedStoragePath),
		VmBackend:            resolveValue(os.Getenv("OV_VM_BACKEND"), cfg.Vm.Backend, "auto"),
	}

	// Auto-detect engines
	var detectErr error
	if rt.BuildEngine == "auto" {
		rt.BuildEngine, detectErr = detectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.build: %w", detectErr)
		}
	}
	if rt.RunEngine == "auto" {
		rt.RunEngine, detectErr = detectEngine()
		if detectErr != nil {
			return nil, fmt.Errorf("engine.run: %w", detectErr)
		}
	}

	// Auto-detect run mode: default to quadlet when podman + systemd are present
	if rt.RunMode == "auto" {
		rt.RunMode = detectRunMode(rt.RunEngine)
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
	if err := validateBindAddress(rt.BindAddress); err != nil {
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

// detectEngine auto-detects the container engine: prefers podman, falls back to docker.
func detectEngine() (string, error) {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman", nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker", nil
	}
	return "", fmt.Errorf("no container engine found (install podman or docker)")
}

func validateEngine(value, field string) error {
	if value != "docker" && value != "podman" {
		return fmt.Errorf("%s must be \"docker\" or \"podman\", got %q", field, value)
	}
	return nil
}

func validateRunMode(value string) error {
	if value != "auto" && value != "direct" && value != "quadlet" {
		return fmt.Errorf("run_mode must be \"auto\", \"direct\", or \"quadlet\", got %q", value)
	}
	return nil
}

// detectRunMode returns "quadlet" when podman and systemd are present, otherwise "direct".
func detectRunMode(runEngine string) string {
	if runEngine == "podman" {
		if _, err := exec.LookPath("systemctl"); err == nil {
			return "quadlet"
		}
	}
	return "direct"
}

func validateBindAddress(value string) error {
	if value != "127.0.0.1" && value != "0.0.0.0" {
		return fmt.Errorf("bind_address must be \"127.0.0.1\" or \"0.0.0.0\", got %q", value)
	}
	return nil
}

func resolveEncryptedStoragePath(envVal, cfgVal string) string {
	if envVal != "" {
		return expandHostHome(envVal)
	}
	if cfgVal != "" {
		return expandHostHome(cfgVal)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "ov", "encrypted")
	}
	return filepath.Join(home, ".local", "share", "ov", "encrypted")
}

// expandHostHome expands ~ and $HOME in a path using the actual user's home directory.
func expandHostHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		return home + path[1:]
	}
	if path == "~" {
		return home
	}
	path = strings.ReplaceAll(path, "$HOME", home)
	return path
}

func resolveAutoEnable(envVal string, cfgVal *bool) bool {
	if envVal != "" {
		return envVal == "true" || envVal == "1"
	}
	if cfgVal != nil {
		return *cfgVal
	}
	return true
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
	case "engine.rootful":
		return cfg.Engine.Rootful, nil
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
	case "bind_address":
		return cfg.BindAddress, nil
	case "encrypted_storage_path":
		return cfg.EncryptedStoragePath, nil
	case "secret_backend":
		return cfg.SecretBackend, nil
	case "secrets.kdbx_path":
		return cfg.SecretsKdbxPath, nil
	case "secrets.kdbx_key_file":
		return cfg.SecretsKdbxKeyFile, nil
	case "vm.backend":
		return cfg.Vm.Backend, nil
	case "vm.disk_size":
		return cfg.Vm.DiskSize, nil
	case "vm.ram":
		return cfg.Vm.Ram, nil
	case "vm.cpus":
		if cfg.Vm.Cpus > 0 {
			return fmt.Sprintf("%d", cfg.Vm.Cpus), nil
		}
		return "", nil
	case "vm.rootfs":
		return cfg.Vm.Rootfs, nil
	case "vm.root_size":
		return cfg.Vm.RootSize, nil
	case "vm.transport":
		return cfg.Vm.Transport, nil
	default:
		if strings.HasPrefix(key, "vnc.password.") {
			name := strings.TrimPrefix(key, "vnc.password.")
			val, source := ResolveCredential("", CredServiceVNC, name, "")
			if source == "locked" {
				return "<LOCKED>", nil
			}
			// In auto mode, config file fallback may return empty while the
			// keyring holds the actual value but is locked. Check shadow index.
			if val == "" && GetKeyringState() == KeyringLocked {
				kr := &KeyringStore{}
				if keys, err := kr.List(CredServiceVNC); err == nil {
					for _, k := range keys {
						if k == name {
							return "<LOCKED>", nil
						}
					}
				}
			}
			return val, nil
		}
		return "", fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, secret_backend, secrets.kdbx_path, secrets.kdbx_key_file, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
	}
}

// SetConfigValue sets a value for a dot-notation key in the config file.
func SetConfigValue(key, value string) error {
	// Validate value before writing
	switch key {
	case "engine.build", "engine.run":
		if value != "auto" {
			if err := validateEngine(value, key); err != nil {
				return fmt.Errorf("%s must be \"auto\", \"docker\", or \"podman\", got %q", key, value)
			}
		}
	case "engine.rootful":
		if value != "auto" && value != "machine" && value != "sudo" && value != "native" {
			return fmt.Errorf("engine.rootful must be \"auto\", \"machine\", \"sudo\", or \"native\", got %q", value)
		}
	case "run_mode":
		if err := validateRunMode(value); err != nil {
			return err
		}
	case "auto_enable":
		if value != "true" && value != "false" {
			return fmt.Errorf("auto_enable must be \"true\" or \"false\", got %q", value)
		}
	case "bind_address":
		if err := validateBindAddress(value); err != nil {
			return err
		}
	case "encrypted_storage_path":
		// Any non-empty path is valid
	case "secret_backend":
		if value != "auto" && value != "keyring" && value != "kdbx" && value != "config" {
			return fmt.Errorf("secret_backend must be \"auto\", \"keyring\", \"kdbx\", or \"config\", got %q", value)
		}
	case "secrets.kdbx_path":
		// Any non-empty path is valid
	case "secrets.kdbx_key_file":
		// Any non-empty path is valid
	case "vm.backend":
		if value != "auto" && value != "libvirt" && value != "qemu" {
			return fmt.Errorf("vm.backend must be \"auto\", \"libvirt\", or \"qemu\", got %q", value)
		}
	case "vm.disk_size":
		// Any non-empty size string is valid (e.g. "10 GiB", "20G")
	case "vm.ram":
		// Any non-empty size string is valid (e.g. "4G", "8192M")
	case "vm.cpus":
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("vm.cpus must be an integer, got %q", value)
		}
	case "vm.rootfs":
		if value != "ext4" && value != "xfs" && value != "btrfs" {
			return fmt.Errorf("vm.rootfs must be \"ext4\", \"xfs\", or \"btrfs\", got %q", value)
		}
	case "vm.root_size":
		// Any non-empty size string is valid (e.g. "10G", "5120M")
	case "vm.transport":
		valid := map[string]bool{"registry": true, "containers-storage": true, "oci": true, "oci-archive": true}
		if !valid[value] {
			return fmt.Errorf("vm.transport must be \"registry\", \"containers-storage\", \"oci\", or \"oci-archive\", got %q", value)
		}
	default:
		if strings.HasPrefix(key, "vnc.password.") {
			// VNC passwords are free-form strings, no validation needed.
			break
		}
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
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
	case "engine.rootful":
		cfg.Engine.Rootful = value
	case "run_mode":
		cfg.RunMode = value
	case "auto_enable":
		b := value == "true"
		cfg.AutoEnable = &b
	case "bind_address":
		cfg.BindAddress = value
	case "encrypted_storage_path":
		cfg.EncryptedStoragePath = value
	case "secret_backend":
		cfg.SecretBackend = value
		// Reset cached default store so the new backend takes effect
		resetDefaultStore()
	case "secrets.kdbx_path":
		cfg.SecretsKdbxPath = value
	case "secrets.kdbx_key_file":
		cfg.SecretsKdbxKeyFile = value
	case "vm.backend":
		cfg.Vm.Backend = value
	case "vm.disk_size":
		cfg.Vm.DiskSize = value
	case "vm.root_size":
		cfg.Vm.RootSize = value
	case "vm.ram":
		cfg.Vm.Ram = value
	case "vm.cpus":
		cpus, _ := strconv.Atoi(value)
		cfg.Vm.Cpus = cpus
	case "vm.rootfs":
		cfg.Vm.Rootfs = value
	case "vm.transport":
		cfg.Vm.Transport = value
	default:
		// Credential keys go through the credential store
		if strings.HasPrefix(key, "vnc.password.") {
			name := strings.TrimPrefix(key, "vnc.password.")
			return DefaultCredentialStore().Set(CredServiceVNC, name, value)
		}
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
	case "engine.rootful":
		cfg.Engine.Rootful = ""
	case "run_mode":
		cfg.RunMode = ""
	case "auto_enable":
		cfg.AutoEnable = nil
	case "bind_address":
		cfg.BindAddress = ""
	case "encrypted_storage_path":
		cfg.EncryptedStoragePath = ""
	case "secret_backend":
		cfg.SecretBackend = ""
		resetDefaultStore()
	case "secrets.kdbx_path":
		cfg.SecretsKdbxPath = ""
	case "secrets.kdbx_key_file":
		cfg.SecretsKdbxKeyFile = ""
	case "vm.backend":
		cfg.Vm.Backend = ""
	case "vm.disk_size":
		cfg.Vm.DiskSize = ""
	case "vm.ram":
		cfg.Vm.Ram = ""
	case "vm.cpus":
		cfg.Vm.Cpus = 0
	case "vm.rootfs":
		cfg.Vm.Rootfs = ""
	case "vm.root_size":
		cfg.Vm.RootSize = ""
	case "vm.transport":
		cfg.Vm.Transport = ""
	default:
		// Credential keys: delete from credential store
		if strings.HasPrefix(key, "vnc.password.") {
			name := strings.TrimPrefix(key, "vnc.password.")
			return DefaultCredentialStore().Delete(CredServiceVNC, name)
		}
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, engine.rootful, run_mode, auto_enable, bind_address, encrypted_storage_path, secret_backend, vm.backend, vm.disk_size, vm.root_size, vm.ram, vm.cpus, vm.rootfs, vm.transport, vnc.password.<image>)", key)
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
			source := "env (" + envName + ")"
			if DotenvLoaded(envName) {
				source = "env (.env)"
			}
			return configKeySource{Key: key, Value: envVal, Source: source}
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
			source := "env (OV_AUTO_ENABLE)"
			if DotenvLoaded("OV_AUTO_ENABLE") {
				source = "env (.env)"
			}
			return configKeySource{Key: "auto_enable", Value: resolved, Source: source}
		}
		if cfg.AutoEnable != nil {
			val := "false"
			if *cfg.AutoEnable {
				val = "true"
			}
			return configKeySource{Key: "auto_enable", Value: val, Source: "config"}
		}
		return configKeySource{Key: "auto_enable", Value: "true", Source: "default"}
	}

	// Resolve encrypted_storage_path default
	defaultStoragePath := resolveEncryptedStoragePath("", "")

	// Resolve vm.cpus separately since it's an int
	vmCpusEntry := func() configKeySource {
		envVal := os.Getenv("OV_VM_CPUS")
		if envVal != "" {
			source := "env (OV_VM_CPUS)"
			if DotenvLoaded("OV_VM_CPUS") {
				source = "env (.env)"
			}
			return configKeySource{Key: "vm.cpus", Value: envVal, Source: source}
		}
		if cfg.Vm.Cpus > 0 {
			return configKeySource{Key: "vm.cpus", Value: fmt.Sprintf("%d", cfg.Vm.Cpus), Source: "config"}
		}
		return configKeySource{Key: "vm.cpus", Value: "2", Source: "default"}
	}

	return []configKeySource{
		resolve("engine.build", "OV_BUILD_ENGINE", cfg.Engine.Build, "auto"),
		resolve("engine.run", "OV_RUN_ENGINE", cfg.Engine.Run, "auto"),
		resolve("engine.rootful", "OV_ENGINE_ROOTFUL", cfg.Engine.Rootful, "auto"),
		resolve("run_mode", "OV_RUN_MODE", cfg.RunMode, "auto"),
		autoEnableEntry(),
		resolve("bind_address", "OV_BIND_ADDRESS", cfg.BindAddress, "127.0.0.1"),
		resolve("encrypted_storage_path", "OV_ENCRYPTED_STORAGE_PATH", cfg.EncryptedStoragePath, defaultStoragePath),
		resolve("secret_backend", "OV_SECRET_BACKEND", cfg.SecretBackend, "auto"),
		resolve("secrets.kdbx_path", "OV_KDBX_PATH", cfg.SecretsKdbxPath, ""),
		resolve("secrets.kdbx_key_file", "OV_KDBX_KEY_FILE", cfg.SecretsKdbxKeyFile, ""),
		resolve("vm.backend", "OV_VM_BACKEND", cfg.Vm.Backend, "auto"),
		resolve("vm.disk_size", "OV_VM_DISK_SIZE", cfg.Vm.DiskSize, "10 GiB"),
		resolve("vm.root_size", "OV_VM_ROOT_SIZE", cfg.Vm.RootSize, ""),
		resolve("vm.ram", "OV_VM_RAM", cfg.Vm.Ram, "4G"),
		vmCpusEntry(),
		resolve("vm.rootfs", "OV_VM_ROOTFS", cfg.Vm.Rootfs, "ext4"),
		resolve("vm.transport", "OV_VM_TRANSPORT", cfg.Vm.Transport, ""),
	}, nil
}
