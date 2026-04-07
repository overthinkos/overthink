package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed sidecar.yml
var embeddedSidecarYAML []byte

// SidecarDef is a sidecar container template (embedded in the ov binary or per-image override in deploy.yml).
type SidecarDef struct {
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Image       string            `yaml:"image,omitempty" json:"image,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets     []SidecarSecret   `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Volumes     []SidecarVolume   `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	Security    *SecurityConfig   `yaml:"security,omitempty" json:"security,omitempty"`
}

// SidecarSecret is a credential store secret injected as an env var into the sidecar.
type SidecarSecret struct {
	Name        string `yaml:"name" json:"name"`                                   // credential store key / podman secret name suffix
	Env         string `yaml:"env" json:"env"`                                     // target env var in container
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // human-readable description
}

// SidecarVolume is a named volume for a sidecar container.
type SidecarVolume struct {
	Name string `yaml:"name" json:"name"` // volume name suffix (full: ov-<image>-<sidecar>-<name>)
	Path string `yaml:"path" json:"path"` // container mount path
}

// SidecarConfig is the parsed sidecar.yml structure.
type SidecarConfig struct {
	Sidecars map[string]SidecarDef `yaml:"sidecars"`
}

// ResolvedSidecar is a fully resolved sidecar ready for quadlet generation.
type ResolvedSidecar struct {
	Name     string            // sidecar key (e.g., "tailscale")
	Image    string            // resolved OCI image ref
	Env      map[string]string // merged env vars (sorted for deterministic output)
	Secrets  []CollectedSecret // provisioned podman secrets
	Volumes  []VolumeMount     // resolved named volumes
	Security SecurityConfig    // merged security config
}

// LoadEmbeddedSidecarConfig parses the sidecar templates compiled into the ov binary.
func LoadEmbeddedSidecarConfig() (*SidecarConfig, error) {
	var cfg SidecarConfig
	if err := yaml.Unmarshal(embeddedSidecarYAML, &cfg); err != nil {
		return nil, fmt.Errorf("parsing embedded sidecar.yml: %w", err)
	}
	return &cfg, nil
}

// MergeSidecars merges sidecar definitions from base into overlay.
// For each sidecar name:
//   - image: overlay replaces if non-empty
//   - env: map merge (overlay keys win, base keys preserved)
//   - secrets: overlay replaces entirely
//   - volumes: overlay replaces entirely
//   - security: overlay replaces entirely
//   - description: overlay replaces if non-empty
//
// Sidecars in base but not overlay are inherited.
// Sidecars in overlay but not base are added.
func MergeSidecars(base, overlay map[string]SidecarDef) map[string]SidecarDef {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	if len(base) == 0 {
		return overlay
	}
	if len(overlay) == 0 {
		result := make(map[string]SidecarDef, len(base))
		for k, v := range base {
			result[k] = v
		}
		return result
	}

	result := make(map[string]SidecarDef, len(base)+len(overlay))

	for name, baseDef := range base {
		if overlayDef, ok := overlay[name]; ok {
			result[name] = mergeSingleSidecar(baseDef, overlayDef)
		} else {
			result[name] = baseDef
		}
	}

	for name, overlayDef := range overlay {
		if _, ok := base[name]; !ok {
			result[name] = overlayDef
		}
	}

	return result
}

func mergeSingleSidecar(base, overlay SidecarDef) SidecarDef {
	merged := base

	if overlay.Description != "" {
		merged.Description = overlay.Description
	}
	if overlay.Image != "" {
		merged.Image = overlay.Image
	}
	if overlay.Secrets != nil {
		merged.Secrets = overlay.Secrets
	}
	if overlay.Volumes != nil {
		merged.Volumes = overlay.Volumes
	}
	if overlay.Security != nil {
		merged.Security = overlay.Security
	}

	if len(overlay.Env) > 0 {
		mergedEnv := make(map[string]string, len(base.Env)+len(overlay.Env))
		for k, v := range base.Env {
			mergedEnv[k] = v
		}
		for k, v := range overlay.Env {
			mergedEnv[k] = v
		}
		merged.Env = mergedEnv
	}

	return merged
}

// ResolveSidecars resolves sidecar definitions into generation-ready configs.
// Resolves volume names (ov-<image>-<sidecar>-<vol>) and collects secrets.
func ResolveSidecars(defs map[string]SidecarDef, imageName, instance string) []ResolvedSidecar {
	if len(defs) == 0 {
		return nil
	}

	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	var resolved []ResolvedSidecar
	for _, name := range names {
		def := defs[name]
		sc := ResolvedSidecar{
			Name:  name,
			Image: def.Image,
			Env:   def.Env,
		}

		if def.Security != nil {
			sc.Security = *def.Security
		}

		for _, v := range def.Volumes {
			volName := sidecarVolumeName(imageName, name, v.Name)
			if instance != "" {
				volName = sidecarVolumeName(imageName+"-"+instance, name, v.Name)
			}
			sc.Volumes = append(sc.Volumes, VolumeMount{
				VolumeName:    volName,
				ContainerPath: v.Path,
			})
		}

		for _, s := range def.Secrets {
			secretName := sidecarSecretName(imageName, name, s.Name)
			if instance != "" {
				secretName = sidecarSecretName(imageName+"-"+instance, name, s.Name)
			}
			sc.Secrets = append(sc.Secrets, CollectedSecret{
				Name:       secretName,
				Env:        s.Env,
				SecretName: s.Name,
			})
		}

		resolved = append(resolved, sc)
	}

	return resolved
}

// ResolveSidecarsForConfig merges embedded templates with deploy.yml overrides.
// Only sidecars referenced in deploySidecars are included.
func ResolveSidecarsForConfig(deploySidecars map[string]SidecarDef) (map[string]SidecarDef, error) {
	if len(deploySidecars) == 0 {
		return nil, nil
	}

	embedded, err := LoadEmbeddedSidecarConfig()
	if err != nil {
		return nil, err
	}

	var templates map[string]SidecarDef
	if embedded != nil {
		templates = embedded.Sidecars
	}

	// Merge: embedded templates + deploy.yml overrides
	merged := MergeSidecars(templates, deploySidecars)

	// Filter: only keep sidecars that are referenced in deploy.yml
	filtered := make(map[string]SidecarDef, len(deploySidecars))
	for name := range deploySidecars {
		if def, ok := merged[name]; ok {
			filtered[name] = def
		}
	}

	if len(filtered) == 0 {
		return nil, nil
	}
	return filtered, nil
}

// SidecarEnvKeys returns all env var keys defined by attached sidecars.
// Used to route CLI -e flags to sidecars vs the app container.
func SidecarEnvKeys(sidecars map[string]SidecarDef) map[string]string {
	keys := make(map[string]string) // env key -> sidecar name
	for scName, sc := range sidecars {
		for k := range sc.Env {
			keys[k] = scName
		}
		for _, s := range sc.Secrets {
			if s.Env != "" {
				keys[s.Env] = scName
			}
		}
	}
	// Also include well-known TS_ prefix for tailscale sidecar
	if _, ok := sidecars["tailscale"]; ok {
		for _, k := range []string{"TS_HOSTNAME", "TS_EXTRA_ARGS", "TS_TAILSCALED_EXTRA_ARGS", "TS_DEBUG_FIREWALL_MODE", "TS_ROUTES", "TS_SERVE_CONFIG", "TS_LOGIN_SERVER"} {
			keys[k] = "tailscale"
		}
	}
	return keys
}

// --- Naming helpers ---

func sidecarVolumeName(imageName, sidecarName, volumeName string) string {
	return fmt.Sprintf("ov-%s-%s-%s", imageName, sidecarName, volumeName)
}

func sidecarSecretName(imageName, sidecarName, secretName string) string {
	return fmt.Sprintf("ov-%s-%s-%s", imageName, sidecarName, secretName)
}

func SidecarContainerName(imageName, sidecarName string) string {
	return containerName(imageName) + "-" + sidecarName
}

func SidecarContainerNameInstance(imageName, instance, sidecarName string) string {
	return containerNameInstance(imageName, instance) + "-" + sidecarName
}

func PodName(imageName string) string {
	return containerName(imageName)
}

func PodNameInstance(imageName, instance string) string {
	return containerNameInstance(imageName, instance)
}

func HasTailscaleSidecar(sidecars map[string]SidecarDef) bool {
	_, ok := sidecars["tailscale"]
	return ok
}

func sidecarConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "ov", "sidecar"), nil
}

func SortedSidecarEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var result []string
	for _, k := range keys {
		result = append(result, k+"="+env[k])
	}
	return result
}

func IsSidecarEnvQuotable(val string) bool {
	return strings.ContainsAny(val, `"{}[] `)
}
