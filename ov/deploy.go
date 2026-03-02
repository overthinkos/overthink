package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DeployConfig represents per-machine deployment overrides (~/.config/ov/deploy.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
type DeployConfig struct {
	Images map[string]DeployImageConfig `yaml:"images"`
}

// DeployImageConfig holds deployment-specific overrides for a single image.
type DeployImageConfig struct {
	Tunnel     *TunnelYAML       `yaml:"tunnel,omitempty"`
	FQDN       string            `yaml:"fqdn,omitempty"`
	AcmeEmail  string            `yaml:"acme_email,omitempty"`
	BindMounts []BindMountConfig `yaml:"bind_mounts,omitempty"`
	Ports      []string          `yaml:"ports,omitempty"`
}

// DeployConfigPath returns the path to the deploy overlay file.
// Package-level var for testability (same pattern as RuntimeConfigPath).
var DeployConfigPath = defaultDeployConfigPath

func defaultDeployConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "ov", "deploy.yml"), nil
}

// LoadDeployConfig reads the deploy overlay file. Returns nil, nil if the file doesn't exist.
func LoadDeployConfig() (*DeployConfig, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var dc DeployConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &dc, nil
}

// MergeDeployOverlay patches cfg.Images in-place with deployment overrides from deploy.yml.
// Field-level replace: deploy.yml value fully replaces images.yml value.
// Unknown images in deploy.yml are silently ignored.
func MergeDeployOverlay(cfg *Config, dc *DeployConfig) {
	if dc == nil || dc.Images == nil {
		return
	}

	for name, overlay := range dc.Images {
		img, ok := cfg.Images[name]
		if !ok {
			continue // silently ignore unknown images
		}

		if overlay.Tunnel != nil {
			img.Tunnel = overlay.Tunnel
		}
		if overlay.FQDN != "" {
			img.FQDN = overlay.FQDN
		}
		if overlay.AcmeEmail != "" {
			img.AcmeEmail = overlay.AcmeEmail
		}
		if overlay.BindMounts != nil {
			img.BindMounts = overlay.BindMounts
		}
		if overlay.Ports != nil {
			img.Ports = overlay.Ports
		}

		cfg.Images[name] = img
	}
}

// BindMountNames returns a set of bind mount names for use as an exclusion filter.
func BindMountNames(mounts []BindMountConfig) map[string]bool {
	if len(mounts) == 0 {
		return nil
	}
	names := make(map[string]bool, len(mounts))
	for _, m := range mounts {
		if m.Name != "" {
			names[m.Name] = true
		}
	}
	return names
}
