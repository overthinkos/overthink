package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LockFile represents the layers.lock file
type LockFile struct {
	Modules []LockModule `yaml:"modules,omitempty"`
}

// LockModule represents a locked module entry
type LockModule struct {
	Module  string   `yaml:"module"`
	Version string   `yaml:"version"`
	Commit  string   `yaml:"commit"`
	Hash    string   `yaml:"hash"`
	Layers  []string `yaml:"layers"`
}

// ModuleManifest represents a module.yml file in a remote module
type ModuleManifest struct {
	Module string `yaml:"module"`
}

// ParsedRef represents a parsed remote reference with optional version.
// Works for both layer refs and image refs.
type ParsedRef struct {
	Raw        string // original string, e.g. "github.com/org/repo/name@v1.0.0"
	ModulePath string // e.g. "github.com/org/repo"
	Name       string // e.g. "name" (layer name or image name)
	Version    string // e.g. "v1.0.0" (empty if not specified)
}

// StripVersion removes the @version suffix from a ref.
// e.g. "github.com/org/repo/name@v1.0.0" -> ("github.com/org/repo/name", "v1.0.0")
// If no @ is present, returns (ref, "").
func StripVersion(ref string) (string, string) {
	if idx := strings.LastIndex(ref, "@"); idx != -1 {
		return ref[:idx], ref[idx+1:]
	}
	return ref, ""
}

// ParseRemoteRef parses a remote reference into module path, name, and optional version.
// e.g. "github.com/org/repo/name@v1.0.0" -> ParsedRef{ModulePath: "github.com/org/repo", Name: "name", Version: "v1.0.0"}
func ParseRemoteRef(ref string) *ParsedRef {
	bare, version := StripVersion(ref)
	modPath, name := SplitRemoteLayerRef(bare)
	return &ParsedRef{
		Raw:        ref,
		ModulePath: modPath,
		Name:       name,
		Version:    version,
	}
}

// IsRemoteLayerRef returns true if a layer reference is a fully-qualified remote path
// (contains at least three slashes, e.g. "github.com/org/repo/layer-name" or "github.com/org/repo/layer@v1")
func IsRemoteLayerRef(ref string) bool {
	bare, _ := StripVersion(ref)
	return strings.Count(bare, "/") >= 3
}

// IsRemoteImageRef returns true if a ref looks like a remote image reference.
// Same structural rule as IsRemoteLayerRef (3+ slashes after stripping version).
func IsRemoteImageRef(ref string) bool {
	return IsRemoteLayerRef(ref)
}

// SplitRemoteLayerRef splits a remote layer reference into module path and layer name.
// Strips any @version suffix before splitting.
// e.g. "github.com/overthinkos/ml-layers/cuda@v1" -> ("github.com/overthinkos/ml-layers", "cuda")
func SplitRemoteLayerRef(ref string) (modulePath string, layerName string) {
	bare, _ := StripVersion(ref)
	lastSlash := strings.LastIndex(bare, "/")
	if lastSlash == -1 {
		return "", bare
	}
	return bare[:lastSlash], bare[lastSlash+1:]
}

// ParseLockFile reads and parses a layers.lock file
func ParseLockFile(dir string) (*LockFile, error) {
	path := filepath.Join(dir, "layers.lock")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading layers.lock: %w", err)
	}

	var lf LockFile
	if err := yaml.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parsing layers.lock: %w", err)
	}
	return &lf, nil
}

// WriteLockFile writes a layers.lock file
func WriteLockFile(dir string, lf *LockFile) error {
	header := "# layers.lock (generated -- do not edit)\n"
	data, err := yaml.Marshal(lf)
	if err != nil {
		return fmt.Errorf("marshaling layers.lock: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "layers.lock"), []byte(header+string(data)), 0644)
}

// ParseModuleManifest reads and parses a module.yml file
func ParseModuleManifest(dir string) (*ModuleManifest, error) {
	path := filepath.Join(dir, "module.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading module.yml: %w", err)
	}

	var mm ModuleManifest
	if err := yaml.Unmarshal(data, &mm); err != nil {
		return nil, fmt.Errorf("parsing module.yml: %w", err)
	}
	return &mm, nil
}

// FindLockModule returns the lock entry for a module, or nil if not found
func (lf *LockFile) FindLockModule(modulePath string) *LockModule {
	for i := range lf.Modules {
		if lf.Modules[i].Module == modulePath {
			return &lf.Modules[i]
		}
	}
	return nil
}

// ModuleCacheDir returns the cache directory for modules.
// Uses $OV_MODULE_CACHE env var if set, otherwise ~/.cache/ov/mod/
func ModuleCacheDir() (string, error) {
	if envDir := os.Getenv("OV_MODULE_CACHE"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "ov", "mod"), nil
}

// ModuleCachePath returns the cache path for a specific module version.
// e.g. ~/.cache/ov/mod/github.com/overthinkos/ml-layers@v1.0.0/
func ModuleCachePath(modulePath, version string) (string, error) {
	cacheDir, err := ModuleCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, modulePath+"@"+version), nil
}

// IsModuleCached checks if a module version is already in the cache
func IsModuleCached(modulePath, version string) (bool, error) {
	cachePath, err := ModuleCachePath(modulePath, version)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CollectRequiredModules scans image layers in a config to find which modules are needed.
// Returns a set of module paths referenced by remote layer refs.
func CollectRequiredModules(cfg *Config) map[string]bool {
	modules := make(map[string]bool)
	for _, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for _, layerRef := range img.Layers {
			if IsRemoteLayerRef(layerRef) {
				modPath, _ := SplitRemoteLayerRef(layerRef)
				modules[modPath] = true
			}
		}
	}
	return modules
}

// CollectRequiredModulesVersioned scans image layers and layer depends for remote refs
// with @version suffixes. Returns module path -> version map.
// Errors if the same module is referenced with different versions.
func CollectRequiredModulesVersioned(cfg *Config, layers map[string]*Layer) (map[string]string, error) {
	modules := make(map[string]string) // modulePath -> version

	addRef := func(ref, source string) error {
		if !IsRemoteLayerRef(ref) {
			return nil
		}
		parsed := ParseRemoteRef(ref)
		if parsed.Version == "" {
			return nil
		}
		if existing, ok := modules[parsed.ModulePath]; ok && existing != parsed.Version {
			return fmt.Errorf("version conflict for module %s: %s vs %s (from %s)", parsed.ModulePath, existing, parsed.Version, source)
		}
		modules[parsed.ModulePath] = parsed.Version
		return nil
	}

	// Scan images.yml layer references
	if cfg != nil {
		for imgName, img := range cfg.Images {
			if !img.IsEnabled() {
				continue
			}
			for _, layerRef := range img.Layers {
				if err := addRef(layerRef, fmt.Sprintf("images.yml image %s", imgName)); err != nil {
					return nil, err
				}
			}
		}
	}

	// Scan layer.yml depends (use RawDepends to get @version info)
	for layerName, layer := range layers {
		deps := layer.RawDepends
		if len(deps) == 0 {
			deps = layer.Depends // fallback for layers without RawDepends
		}
		for _, dep := range deps {
			if err := addRef(dep, fmt.Sprintf("layer %s depends", layerName)); err != nil {
				return nil, err
			}
		}
	}

	return modules, nil
}
