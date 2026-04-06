package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployConfig represents per-machine deployment overrides (~/.config/ov/deploy.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
type DeployConfig struct {
	Env               []string                    `yaml:"env,omitempty"`                // global env vars injected into all containers
	ServiceEnvSources map[string]string           `yaml:"service_env_sources,omitempty"` // tracks which image injected each global env var (key -> image name)
	Images            map[string]DeployImageConfig `yaml:"images"`
}

// DeployImageConfig holds deployment-specific overrides for a single image.
type DeployImageConfig struct {
	Version    string               `yaml:"version,omitempty"`
	Status     string               `yaml:"status,omitempty"`
	Info       string               `yaml:"info,omitempty"`
	Tunnel     *TunnelYAML          `yaml:"tunnel,omitempty"`
	DNS        string               `yaml:"dns,omitempty"`
	AcmeEmail  string               `yaml:"acme_email,omitempty"`
	Volumes    []DeployVolumeConfig `yaml:"volumes,omitempty"`
	Ports      []string             `yaml:"ports,omitempty"`
	Env        []string             `yaml:"env,omitempty"`
	EnvFile    string               `yaml:"env_file,omitempty"`
	Security   *SecurityConfig      `yaml:"security,omitempty"`
	Network    string               `yaml:"network,omitempty"`
	Engine     string               `yaml:"engine,omitempty"`
	Secrets         []DeploySecretConfig `yaml:"secrets,omitempty"`
	ForwardGpgAgent *bool                `yaml:"forward_gpg_agent,omitempty"` // Override global forward_gpg_agent per image
	ForwardSshAgent *bool                `yaml:"forward_ssh_agent,omitempty"` // Override global forward_ssh_agent per image
}

// DeployVolumeConfig overrides the backing for a layer-declared volume.
type DeployVolumeConfig struct {
	Name       string `yaml:"name"`                    // matches layer volume name
	Type       string `yaml:"type,omitempty"`           // "volume" (default), "bind", "encrypted"
	Host       string `yaml:"host,omitempty"`           // explicit host path (bind type only, optional)
	Path       string `yaml:"path,omitempty"`           // container path (only for deploy-only volumes not in any layer)
	DataSeeded bool   `yaml:"data_seeded,omitempty"`    // tracks if data was provisioned from image
	DataSource string `yaml:"data_source,omitempty"`    // image:tag that provided the data
}

// DeploySecretConfig overrides or provides a secret for deployment.
type DeploySecretConfig struct {
	Name   string `yaml:"name"`              // matches layer secret name
	Source string `yaml:"source,omitempty"`   // "keyring" (default), "env:VAR", "file:/path"
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

		if overlay.Version != "" {
			img.Version = overlay.Version
		}
		if overlay.Status != "" {
			img.Status = overlay.Status
		}
		if overlay.Info != "" {
			img.Info = overlay.Info
		}
		if overlay.Tunnel != nil {
			img.Tunnel = overlay.Tunnel
		}
		if overlay.DNS != "" {
			img.DNS = overlay.DNS
		}
		if overlay.AcmeEmail != "" {
			img.AcmeEmail = overlay.AcmeEmail
		}
		if overlay.Ports != nil {
			img.Ports = overlay.Ports
		}
		if overlay.Env != nil {
			img.Env = overlay.Env
		}
		if overlay.EnvFile != "" {
			img.EnvFile = overlay.EnvFile
		}
		if overlay.Security != nil {
			img.Security = overlay.Security
		}
		if overlay.Network != "" {
			img.Network = overlay.Network
		}
		if overlay.Engine != "" {
			img.Engine = overlay.Engine
		}

		cfg.Images[name] = img
	}
}

// MergeDeployOntoMetadata applies deploy.yml overrides onto label-derived metadata.
// Same field-level replace semantics as MergeDeployOverlay.
func MergeDeployOntoMetadata(meta *ImageMetadata, dc *DeployConfig) {
	if dc == nil || dc.Images == nil || meta == nil {
		return
	}

	overlay, ok := dc.Images[meta.Image]
	if !ok {
		return
	}

	if overlay.Status != "" {
		meta.Status = overlay.Status
	}
	if overlay.Info != "" {
		meta.Info = overlay.Info
	}
	if overlay.Tunnel != nil {
		meta.Tunnel = overlay.Tunnel
	}
	if overlay.DNS != "" {
		meta.DNS = overlay.DNS
	}
	if overlay.AcmeEmail != "" {
		meta.AcmeEmail = overlay.AcmeEmail
	}
	if overlay.Ports != nil {
		meta.Ports = overlay.Ports
	}
	if overlay.Env != nil {
		meta.Env = overlay.Env
	}
	if overlay.Security != nil {
		meta.Security = *overlay.Security
	}
	if overlay.Network != "" {
		meta.Network = overlay.Network
	}
	if overlay.Engine != "" {
		meta.Engine = overlay.Engine
	}
	// Merge deploy.yml secrets onto image label secrets
	if overlay.Secrets != nil {
		deployByName := make(map[string]DeploySecretConfig, len(overlay.Secrets))
		for _, ds := range overlay.Secrets {
			deployByName[ds.Name] = ds
		}
		// Override matching secrets from image labels with deploy.yml source config
		for i, ls := range meta.Secrets {
			if _, ok := deployByName[ls.Name]; ok {
				// Deploy.yml provides this secret — keep the label entry
				// (the source override is used at provisioning time, not in the label)
				_ = i
			}
		}
		// Add deploy-only secrets that aren't in the image labels
		for _, ds := range overlay.Secrets {
			found := false
			for _, ls := range meta.Secrets {
				if ls.Name == ds.Name {
					found = true
					break
				}
			}
			if !found {
				meta.Secrets = append(meta.Secrets, LabelSecret{
					Name:   ds.Name,
					Target: "/run/secrets/" + ds.Name,
				})
			}
		}
	}
}

// ResolveVolumeBacking splits image volumes into named volumes and bind mounts
// based on deploy.yml volume configuration.
// Volumes without a deploy override remain as named volumes.
// Volumes with type=bind or type=encrypted become ResolvedBindMount.
// Deploy-only volumes (with Path set, not in labels) are also supported.
func ResolveVolumeBacking(imageName string, labelVolumes []VolumeMount, deployVolumes []DeployVolumeConfig, home string, encStoragePath string, volumesPath string) ([]VolumeMount, []ResolvedBindMount) {
	// Index deploy volume configs by name
	deployByName := make(map[string]DeployVolumeConfig, len(deployVolumes))
	for _, dv := range deployVolumes {
		deployByName[dv.Name] = dv
	}

	// Track which deploy entries matched a label volume
	matched := make(map[string]bool)

	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount

	for _, vol := range labelVolumes {
		// Extract short name from "ov-<image>-<name>"
		shortName := strings.TrimPrefix(vol.VolumeName, "ov-"+imageName+"-")

		dv, hasOverride := deployByName[shortName]
		if hasOverride {
			matched[shortName] = true
		}

		if hasOverride && (dv.Type == "bind" || dv.Type == "encrypted") {
			var hostPath string
			if dv.Type == "encrypted" {
				if dv.Host != "" {
					// Explicit per-volume path: /path/{cipher,plain}
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					// Global default: <encStoragePath>/ov-<image>-<name>/{cipher,plain}
					hostPath = encryptedPlainDir(encStoragePath, imageName, shortName)
				}
			} else if dv.Host != "" {
				hostPath = expandHostHome(dv.Host)
			} else {
				// Auto path: <volumesPath>/<image>/<name>
				hostPath = filepath.Join(volumesPath, imageName, shortName)
			}
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      shortName,
				HostPath:  hostPath,
				ContPath:  vol.ContainerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			// Default: keep as named volume
			volumes = append(volumes, vol)
		}
	}

	// Add deploy-only volumes (not in any layer, must have Path)
	for _, dv := range deployVolumes {
		if matched[dv.Name] || dv.Path == "" {
			continue
		}
		containerPath := ExpandPath(dv.Path, home)
		if dv.Type == "bind" || dv.Type == "encrypted" {
			var hostPath string
			if dv.Type == "encrypted" {
				if dv.Host != "" {
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					hostPath = encryptedPlainDir(encStoragePath, imageName, dv.Name)
				}
			} else if dv.Host != "" {
				hostPath = expandHostHome(dv.Host)
			} else {
				hostPath = filepath.Join(volumesPath, imageName, dv.Name)
			}
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      dv.Name,
				HostPath:  hostPath,
				ContPath:  containerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			volumes = append(volumes, VolumeMount{
				VolumeName:    "ov-" + imageName + "-" + dv.Name,
				ContainerPath: containerPath,
			})
		}
	}

	return volumes, bindMounts
}

// LoadDeployFile reads a deploy.yml from an arbitrary path.
func LoadDeployFile(path string) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var dc DeployConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &dc, nil
}

// SaveDeployConfig writes a DeployConfig to the standard deploy.yml path.
func SaveDeployConfig(dc *DeployConfig) error {
	path, err := DeployConfigPath()
	if err != nil {
		return fmt.Errorf("determining deploy config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := yaml.Marshal(dc)
	if err != nil {
		return fmt.Errorf("marshaling deploy config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// MergeDeployConfigs merges multiple DeployConfigs left-to-right.
// Later configs take precedence (field-level replace per image).
func MergeDeployConfigs(configs ...*DeployConfig) *DeployConfig {
	result := &DeployConfig{Images: make(map[string]DeployImageConfig)}
	for _, dc := range configs {
		if dc == nil || dc.Images == nil {
			continue
		}
		for name, overlay := range dc.Images {
			existing := result.Images[name]
			if overlay.Tunnel != nil {
				existing.Tunnel = overlay.Tunnel
			}
			if overlay.DNS != "" {
				existing.DNS = overlay.DNS
			}
			if overlay.AcmeEmail != "" {
				existing.AcmeEmail = overlay.AcmeEmail
			}
			if overlay.Volumes != nil {
				existing.Volumes = overlay.Volumes
			}
			if overlay.Ports != nil {
				existing.Ports = overlay.Ports
			}
			if overlay.Env != nil {
				existing.Env = overlay.Env
			}
			if overlay.EnvFile != "" {
				existing.EnvFile = overlay.EnvFile
			}
			if overlay.Security != nil {
				existing.Security = overlay.Security
			}
			if overlay.Network != "" {
				existing.Network = overlay.Network
			}
			if overlay.Engine != "" {
				existing.Engine = overlay.Engine
			}
			result.Images[name] = existing
		}
	}
	return result
}

// RemoveImageDeploy removes an image's entry from a deploy config.
func RemoveImageDeploy(dc *DeployConfig, imageName string) {
	if dc != nil && dc.Images != nil {
		delete(dc.Images, imageName)
	}
}

// cleanDeployEntry removes an image's entry from deploy.yml (best-effort).
// Also removes global service env vars that were injected by this image.
// If deploy.yml becomes empty after removal, the file is deleted.
func cleanDeployEntry(imageName string) {
	dc, err := LoadDeployConfig()
	if err != nil || dc == nil {
		return
	}

	hasImage := false
	if _, ok := dc.Images[imageName]; ok {
		hasImage = true
		RemoveImageDeploy(dc, imageName)
	}

	// Remove global service env vars injected by this image
	removedEnv := false
	if dc.ServiceEnvSources != nil {
		var keysToRemove []string
		for key, source := range dc.ServiceEnvSources {
			if source == imageName {
				keysToRemove = append(keysToRemove, key)
			}
		}
		for _, key := range keysToRemove {
			dc.Env = removeEnvByKey(dc.Env, key)
			delete(dc.ServiceEnvSources, key)
			removedEnv = true
			fmt.Fprintf(os.Stderr, "Removed service env: %s\n", key)
		}
		if len(dc.ServiceEnvSources) == 0 {
			dc.ServiceEnvSources = nil
		}
	}

	if !hasImage && !removedEnv {
		return
	}

	if len(dc.Images) == 0 && len(dc.Env) == 0 {
		if path, pathErr := DeployConfigPath(); pathErr == nil {
			os.Remove(path)
		}
	} else if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not clean deploy.yml: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaned deploy.yml entry for %s\n", imageName)
}

// appendOrReplaceEnv adds or replaces an env var entry (KEY=VALUE) in a slice.
// If the key already exists, the value is replaced in-place.
func appendOrReplaceEnv(envs []string, entry string) []string {
	key := envKey(entry)
	for i, e := range envs {
		if envKey(e) == key {
			envs[i] = entry
			return envs
		}
	}
	return append(envs, entry)
}

// removeEnvByKey removes all env vars with the given key from a slice.
func removeEnvByKey(envs []string, key string) []string {
	var result []string
	for _, e := range envs {
		if envKey(e) != key {
			result = append(result, e)
		}
	}
	return result
}

// envKey extracts the KEY part from a KEY=VALUE string.
func envKey(entry string) string {
	if idx := strings.IndexByte(entry, '='); idx >= 0 {
		return entry[:idx]
	}
	return entry
}

// filterOwnServiceEnv returns global env vars excluding those injected by imageName.
func filterOwnServiceEnv(globalEnv []string, sources map[string]string, imageName string) []string {
	if len(sources) == 0 || imageName == "" {
		return globalEnv
	}
	var result []string
	for _, e := range globalEnv {
		if sources[envKey(e)] != imageName {
			result = append(result, e)
		}
	}
	return result
}

// SaveDeployStateInput holds the deployment parameters to persist.
type SaveDeployStateInput struct {
	Ports     []string
	Env       []string
	EnvFile   string
	Network   string
	Security  *SecurityConfig
	Volumes   []DeployVolumeConfig
}

// saveDeployState persists deployment parameters to deploy.yml (best-effort).
// Merges onto any existing entry to preserve fields from ov deploy import.
func saveDeployState(imageName string, input SaveDeployStateInput) {
	dc, _ := LoadDeployConfig()
	if dc == nil {
		dc = &DeployConfig{Images: make(map[string]DeployImageConfig)}
	}
	entry := dc.Images[imageName] // preserve existing fields (tunnel, volumes, etc.)
	if input.Volumes != nil {
		entry.Volumes = input.Volumes
	}
	if input.Ports != nil {
		entry.Ports = input.Ports
	}
	if len(input.Env) > 0 {
		entry.Env = input.Env
	}
	if input.EnvFile != "" {
		entry.EnvFile = input.EnvFile
	}
	if input.Network != "" {
		entry.Network = input.Network
	}
	if input.Security != nil {
		entry.Security = input.Security
	}
	dc.Images[imageName] = entry
	if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to deploy.yml: %v\n", err)
	}
}

// ExportAllImages exports all runtime-relevant fields for all enabled images in a Config.
func ExportAllImages(cfg *Config) *DeployConfig {
	dc := &DeployConfig{Images: make(map[string]DeployImageConfig)}
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		entry := DeployImageConfig{
			Version:   img.Version,
			Status:    img.Status,
			Info:      img.Info,
			Ports:     img.Ports,
			Tunnel:    img.Tunnel,
			DNS:       img.DNS,
			AcmeEmail: img.AcmeEmail,
			Env:       img.Env,
			EnvFile:   img.EnvFile,
			Security:  img.Security,
			Network:   img.Network,
			Engine:    img.Engine,
		}
		// Only include if at least one field is set
		if entry.Version != "" || entry.Status != "" || entry.Info != "" ||
			entry.Ports != nil || entry.Tunnel != nil || entry.DNS != "" ||
			entry.AcmeEmail != "" || entry.Env != nil ||
			entry.EnvFile != "" || entry.Security != nil || entry.Network != "" ||
			entry.Engine != "" {
			dc.Images[name] = entry
		}
	}
	return dc
}

