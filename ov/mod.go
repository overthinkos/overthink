package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParsedRef represents a parsed remote reference with version.
// Works for both layer refs and image refs.
// Format: @host/org/repo/name:version
type ParsedRef struct {
	Raw        string // original string, e.g. "@github.com/org/repo/name:v1.0.0"
	RepoPath   string // e.g. "github.com/org/repo"
	Name       string // e.g. "name" (layer name or image name)
	Version    string // e.g. "v1.0.0"
}

// StripVersion removes the :version suffix from a remote ref.
// For non-remote refs (no @ prefix), returns (ref, "").
// e.g. "@github.com/org/repo/name:v1.0.0" -> ("@github.com/org/repo/name", "v1.0.0")
func StripVersion(ref string) (string, string) {
	if !strings.HasPrefix(ref, "@") {
		return ref, ""
	}
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		return ref[:idx], ref[idx+1:]
	}
	return ref, ""
}

// IsRemoteLayerRef returns true if a layer reference is a remote ref (starts with @)
func IsRemoteLayerRef(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// IsRemoteImageRef returns true if a ref looks like a remote image reference (starts with @)
func IsRemoteImageRef(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// ParseRemoteRef parses a remote reference into repo path, name, and version.
// e.g. "@github.com/org/repo/name:v1.0.0" -> ParsedRef{RepoPath: "github.com/org/repo", Name: "name", Version: "v1.0.0"}
func ParseRemoteRef(ref string) *ParsedRef {
	raw := ref

	// Strip @ prefix
	ref = strings.TrimPrefix(ref, "@")

	// Split version
	version := ""
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}

	// Split into repo path and name (last segment)
	repoPath, name := splitRepoAndName(ref)

	return &ParsedRef{
		Raw:      raw,
		RepoPath: repoPath,
		Name:     name,
		Version:  version,
	}
}

// splitRepoAndName splits "github.com/org/repo/name" into ("github.com/org/repo", "name")
func splitRepoAndName(ref string) (repoPath string, name string) {
	lastSlash := strings.LastIndex(ref, "/")
	if lastSlash == -1 {
		return "", ref
	}
	return ref[:lastSlash], ref[lastSlash+1:]
}

// BareRef returns the layer map key for a remote ref (without @ prefix and without :version).
// e.g. "@github.com/org/repo/name:v1.0.0" -> "github.com/org/repo/name"
func BareRef(ref string) string {
	bare, _ := StripVersion(ref)
	return strings.TrimPrefix(bare, "@")
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

// ModuleCachePath returns the cache path for a specific repo version.
// e.g. ~/.cache/ov/mod/github.com/org/repo@v1.0.0/
func ModuleCachePath(repoPath, version string) (string, error) {
	cacheDir, err := ModuleCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, repoPath+"@"+version), nil
}

// IsModuleCached checks if a repo version is already in the cache
func IsModuleCached(repoPath, version string) (bool, error) {
	cachePath, err := ModuleCachePath(repoPath, version)
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

// EnsureModuleDownloaded downloads the module if not already cached.
// Returns the cache path.
func EnsureModuleDownloaded(repoPath, version string) (string, error) {
	cached, err := IsModuleCached(repoPath, version)
	if err != nil {
		return "", err
	}
	if cached {
		path, err := ModuleCachePath(repoPath, version)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return DownloadModule(repoPath, version)
}

// CollectRemoteRefs collects all unique remote refs (repo+version pairs) from
// images.yml layer lists and layer.yml depends/layers fields.
// Returns a map of repoPath -> version.
func CollectRemoteRefs(cfg *Config, layers map[string]*Layer) (map[string]string, error) {
	repos := make(map[string]string) // repoPath -> version

	addRef := func(ref, source string) error {
		if !IsRemoteLayerRef(ref) {
			return nil
		}
		parsed := ParseRemoteRef(ref)
		version := parsed.Version
		if version == "" {
			// No version specified -- resolve latest git tag
			if existing, ok := repos[parsed.RepoPath]; ok {
				// Already resolved for this repo, reuse
				version = existing
			} else {
				repoURL := ModuleGitURL(parsed.RepoPath)
				tag, err := GitLatestTag(repoURL)
				if err != nil {
					return fmt.Errorf("%s: cannot resolve version for %s: %w", source, parsed.RepoPath, err)
				}
				version = tag
				fmt.Fprintf(os.Stderr, "Resolved @%s -> %s\n", parsed.RepoPath, version)
			}
		}
		if existing, ok := repos[parsed.RepoPath]; ok && existing != version {
			return fmt.Errorf("version conflict for repo %s: %s vs %s (from %s)", parsed.RepoPath, existing, version, source)
		}
		repos[parsed.RepoPath] = version
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

	// Scan layer.yml depends and layers: fields
	for layerName, layer := range layers {
		for _, dep := range layer.RawDepends {
			if err := addRef(dep, fmt.Sprintf("layer %s depends", layerName)); err != nil {
				return nil, err
			}
		}
		for _, ref := range layer.RawIncludedLayers {
			if err := addRef(ref, fmt.Sprintf("layer %s layers", layerName)); err != nil {
				return nil, err
			}
		}
	}

	return repos, nil
}
