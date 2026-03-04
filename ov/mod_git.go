package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// GitResolveRef resolves a git reference (tag, branch, or commit) to a full commit hash.
// Uses git ls-remote for tags/branches; for commit hashes, validates length and returns as-is.
func GitResolveRef(repoURL string, ref string) (string, error) {
	// If ref looks like a full commit hash (40 hex chars), return as-is
	if len(ref) == 40 && isHex(ref) {
		return ref, nil
	}

	// Try ls-remote to resolve tags and branches
	cmd := exec.Command("git", "ls-remote", repoURL, ref, "refs/tags/"+ref, "refs/heads/"+ref)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", repoURL, ref, err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			commit := parts[0]
			refName := parts[1]
			// Prefer exact tag match, then branch
			if refName == "refs/tags/"+ref || refName == "refs/heads/"+ref || refName == ref {
				return commit, nil
			}
		}
	}

	// Check for peeled tag (annotated tags show as refs/tags/v1.0.0^{})
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.HasSuffix(parts[1], "^{}") {
			return parts[0], nil
		}
	}

	// If nothing matched but ref is a short hex, it might be a short commit
	if len(ref) >= 7 && isHex(ref) {
		return ref, nil
	}

	return "", fmt.Errorf("could not resolve ref %q in %s", ref, repoURL)
}

// GitClone clones a git repository at a specific ref into the target directory.
// Uses shallow clone for efficiency.
func GitClone(repoURL string, ref string, commit string, targetDir string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	// Clone with depth 1 at the specific ref
	// First try as a tag/branch name
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", ref, repoURL, targetDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// If that fails, do a full clone and checkout the commit
		os.RemoveAll(targetDir) // clean up partial clone
		return gitCloneByCommit(repoURL, commit, targetDir)
	}

	return nil
}

// gitCloneByCommit clones a repo and checks out a specific commit
func gitCloneByCommit(repoURL string, commit string, targetDir string) error {
	// Init bare repo, fetch specific commit, checkout
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	cmds := [][]string{
		{"git", "init"},
		{"git", "remote", "add", "origin", repoURL},
		{"git", "fetch", "--depth", "1", "origin", commit},
		{"git", "checkout", "FETCH_HEAD"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = targetDir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(targetDir) // clean up on failure
			return fmt.Errorf("git %s: %w", strings.Join(args[1:], " "), err)
		}
	}

	return nil
}

// ModuleGitURL converts a module path to a git clone URL.
// e.g. "github.com/overthinkos/ml-layers" -> "https://github.com/overthinkos/ml-layers.git"
func ModuleGitURL(modulePath string) string {
	return "https://" + modulePath + ".git"
}

// ComputeModuleHash computes a SHA-256 hash of a module's layers/ directory contents.
// This is used to verify cache integrity in layers.lock.
func ComputeModuleHash(moduleDir string) (string, error) {
	layersDir := filepath.Join(moduleDir, "layers")
	if _, err := os.Stat(layersDir); os.IsNotExist(err) {
		return "", fmt.Errorf("no layers/ directory in module %s", moduleDir)
	}

	h := sha256.New()

	// Walk files deterministically (sorted)
	var files []string
	err := filepath.WalkDir(layersDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(layersDir, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking layers directory: %w", err)
	}

	sort.Strings(files)

	for _, rel := range files {
		// Write filename to hash
		h.Write([]byte(rel))
		// Write file contents to hash
		data, err := os.ReadFile(filepath.Join(layersDir, rel))
		if err != nil {
			return "", err
		}
		h.Write(data)
	}

	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

// DownloadModule downloads a module to the cache.
// Returns the cache path where the module was stored.
func DownloadModule(modulePath string, version string) (string, error) {
	repoURL := ModuleGitURL(modulePath)

	// Resolve the ref to a commit hash
	commit, err := GitResolveRef(repoURL, version)
	if err != nil {
		return "", fmt.Errorf("resolving %s@%s: %w", modulePath, version, err)
	}

	cachePath, err := ModuleCachePath(modulePath, version)
	if err != nil {
		return "", err
	}

	// Check if already cached
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}

	fmt.Fprintf(os.Stderr, "Downloading %s@%s...\n", modulePath, version)

	// Clone into cache
	if err := GitClone(repoURL, version, commit, cachePath); err != nil {
		return "", fmt.Errorf("downloading %s@%s: %w", modulePath, version, err)
	}

	// Remove .git directory to save space (cache is read-only)
	os.RemoveAll(filepath.Join(cachePath, ".git"))

	return cachePath, nil
}

// DiscoverModuleLayers returns the list of layer names in a module directory
func DiscoverModuleLayers(moduleDir string) ([]string, error) {
	layersDir := filepath.Join(moduleDir, "layers")
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// isHex returns true if s contains only hexadecimal characters
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return len(s) > 0
}
