package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParsedRef represents a parsed remote reference with version.
// Works for both layer refs and image refs.
// Format: @host/org/repo/sub/path:version
type ParsedRef struct {
	Raw      string // original string, e.g. "@github.com/org/repo/layers/name:v1.0.0"
	RepoPath string // e.g. "github.com/org/repo"
	SubPath  string // e.g. "layers/name" (path within repo)
	Name     string // e.g. "name" (last segment)
	Version  string // e.g. "v1.0.0"
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

// ParseRemoteRef parses a remote reference into repo path, sub-path, name, and version.
// e.g. "@github.com/org/repo/layers/name:v1.0.0" -> ParsedRef{RepoPath: "github.com/org/repo", SubPath: "layers/name", Name: "name", Version: "v1.0.0"}
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

	// Split into repo path (first 3 segments) and sub-path (rest)
	repoPath, subPath, name := splitRepoAndSubPath(ref)

	return &ParsedRef{
		Raw:      raw,
		RepoPath: repoPath,
		SubPath:  subPath,
		Name:     name,
		Version:  version,
	}
}

// splitRepoAndSubPath splits a ref into repo path (host/org/repo), sub-path, and name.
// e.g. "github.com/org/repo/layers/name" -> ("github.com/org/repo", "layers/name", "name")
// For short refs like "pixi", returns ("", "", "pixi").
func splitRepoAndSubPath(ref string) (repoPath, subPath, name string) {
	parts := strings.SplitN(ref, "/", 4) // [host, org, repo, sub/path]
	if len(parts) < 4 {
		// Not enough segments for a remote ref — treat as local name
		name = parts[len(parts)-1]
		if len(parts) <= 1 {
			return "", "", name
		}
		return strings.Join(parts, "/"), "", name
	}
	repoPath = strings.Join(parts[:3], "/")
	subPath = parts[3]
	if idx := strings.LastIndex(subPath, "/"); idx != -1 {
		name = subPath[idx+1:]
	} else {
		name = subPath
	}
	return repoPath, subPath, name
}

// BareRef returns the layer map key for a remote ref (without @ prefix and without :version).
// e.g. "@github.com/org/repo/name:v1.0.0" -> "github.com/org/repo/name"
func BareRef(ref string) string {
	bare, _ := StripVersion(ref)
	return strings.TrimPrefix(bare, "@")
}

// RepoCacheDir returns the cache directory for remote repos.
// Uses $OV_REPO_CACHE env var if set, otherwise ~/.cache/ov/repos/
func RepoCacheDir() (string, error) {
	if envDir := os.Getenv("OV_REPO_CACHE"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "ov", "repos"), nil
}

// RepoCachePath returns the cache path for a specific repo version.
// e.g. ~/.cache/ov/repos/github.com/org/repo@v1.0.0/
func RepoCachePath(repoPath, version string) (string, error) {
	cacheDir, err := RepoCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, repoPath+"@"+version), nil
}

// IsRepoCached checks if a repo version is already in the cache
func IsRepoCached(repoPath, version string) (bool, error) {
	cachePath, err := RepoCachePath(repoPath, version)
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

// EnsureRepoDownloaded downloads the repo if not already cached.
// Returns the cache path.
func EnsureRepoDownloaded(repoPath, version string) (string, error) {
	cached, err := IsRepoCached(repoPath, version)
	if err != nil {
		return "", err
	}
	if cached {
		path, err := RepoCachePath(repoPath, version)
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return DownloadRepo(repoPath, version)
}

// RemoteDownload represents a unique (repo, version) pair to download,
// along with the specific bare refs needed from it.
type RemoteDownload struct {
	RepoPath string
	Version  string
	Refs     []string // bare refs to import (e.g. "github.com/org/repo/layers/name")
}

// CollectRemoteRefs collects all unique remote refs from image.yml layer lists
// and layer.yml depends/layers fields. Different layers from the same repo can
// use different versions. Only the same bare ref at conflicting versions is an error.
// Returns a list of RemoteDownload grouped by (repoPath, version).
func CollectRemoteRefs(cfg *Config, layers map[string]*Layer) ([]RemoteDownload, error) {
	// bareRef -> version (for conflict detection)
	refVersions := make(map[string]string)
	// (repoPath, version) -> set of bare refs
	type repoVer struct{ repo, ver string }
	downloads := make(map[repoVer]map[string]bool)
	// Track resolved default branches per repo (to avoid duplicate git queries)
	defaultBranches := make(map[string]string)

	addRef := func(ref, source string) error {
		if !IsRemoteLayerRef(ref) {
			return nil
		}
		parsed := ParseRemoteRef(ref)
		bareRef := BareRef(ref)
		version := parsed.Version
		if version == "" {
			// No version specified -- resolve to default branch
			if branch, ok := defaultBranches[parsed.RepoPath]; ok {
				version = branch
			} else {
				repoURL := RepoGitURL(parsed.RepoPath)
				branch, err := GitDefaultBranch(repoURL)
				if err != nil {
					return fmt.Errorf("%s: cannot resolve default branch for %s: %w", source, parsed.RepoPath, err)
				}
				version = branch
				defaultBranches[parsed.RepoPath] = branch
				fmt.Fprintf(os.Stderr, "Resolved @%s -> %s (default branch)\n", parsed.RepoPath, version)
			}
		}
		// Conflict: same bare ref at different versions
		if existing, ok := refVersions[bareRef]; ok && existing != version {
			return fmt.Errorf("version conflict for %s: %s vs %s (from %s)", bareRef, existing, version, source)
		}
		refVersions[bareRef] = version

		key := repoVer{parsed.RepoPath, version}
		if downloads[key] == nil {
			downloads[key] = make(map[string]bool)
		}
		downloads[key][bareRef] = true
		return nil
	}

	// Scan format_config remote ref from defaults and per-image
	if cfg != nil {
		if ref := cfg.Defaults.FormatConfig; ref != "" {
			if err := addRef(ref, "defaults format_config"); err != nil {
				return nil, err
			}
		}
		for imgName, img := range cfg.Images {
			if !img.IsEnabled() {
				continue
			}
			if ref := img.FormatConfig; ref != "" {
				if err := addRef(ref, fmt.Sprintf("image %s format_config", imgName)); err != nil {
					return nil, err
				}
			}
		}
	}

	// Scan image.yml layer references
	if cfg != nil {
		for imgName, img := range cfg.Images {
			if !img.IsEnabled() {
				continue
			}
			for _, layerRef := range img.Layers {
				if err := addRef(layerRef, fmt.Sprintf("image.yml image %s", imgName)); err != nil {
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

	// Convert to sorted list
	var result []RemoteDownload
	for key, refs := range downloads {
		var refList []string
		for ref := range refs {
			refList = append(refList, ref)
		}
		result = append(result, RemoteDownload{
			RepoPath: key.repo,
			Version:  key.ver,
			Refs:     refList,
		})
	}
	return result, nil
}
