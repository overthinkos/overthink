package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModFile represents the layers.mod manifest
type ModFile struct {
	Module  string       `yaml:"module"`
	Require []ModRequire `yaml:"require,omitempty"`
	Replace []ModReplace `yaml:"replace,omitempty"`
}

// ModRequire represents a required module dependency
type ModRequire struct {
	Module  string `yaml:"module"`
	Version string `yaml:"version"`
}

// ModReplace represents a local replacement for a module
type ModReplace struct {
	Module string `yaml:"module"`
	Path   string `yaml:"path"`
}

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

// ParseModFile reads and parses a layers.mod file
func ParseModFile(dir string) (*ModFile, error) {
	path := filepath.Join(dir, "layers.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading layers.mod: %w", err)
	}

	var mf ModFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parsing layers.mod: %w", err)
	}
	return &mf, nil
}

// WriteModFile writes a layers.mod file
func WriteModFile(dir string, mf *ModFile) error {
	data, err := yaml.Marshal(mf)
	if err != nil {
		return fmt.Errorf("marshaling layers.mod: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "layers.mod"), data, 0644)
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

// IsRemoteLayerRef returns true if a layer reference is a fully-qualified remote path
// (contains at least two slashes, e.g. "github.com/org/repo/layer-name")
func IsRemoteLayerRef(ref string) bool {
	return strings.Count(ref, "/") >= 3
}

// SplitRemoteLayerRef splits a remote layer reference into module path and layer name.
// e.g. "github.com/overthinkos/ml-layers/cuda" -> ("github.com/overthinkos/ml-layers", "cuda")
func SplitRemoteLayerRef(ref string) (modulePath string, layerName string) {
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash == -1 {
		return "", ref
	}
	return ref[:lastSlash], ref[lastSlash+1:]
}

// FindReplace returns the replace entry for a module, or nil if not replaced
func (mf *ModFile) FindReplace(modulePath string) *ModReplace {
	for i := range mf.Replace {
		if mf.Replace[i].Module == modulePath {
			return &mf.Replace[i]
		}
	}
	return nil
}

// FindRequire returns the require entry for a module, or nil if not found
func (mf *ModFile) FindRequire(modulePath string) *ModRequire {
	for i := range mf.Require {
		if mf.Require[i].Module == modulePath {
			return &mf.Require[i]
		}
	}
	return nil
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
