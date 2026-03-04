package main

import (
	"fmt"
	"os"
	"strings"
)

// ModCmd groups module management subcommands
type ModCmd struct {
	Get      ModGetCmd      `cmd:"" help:"Download a module and update layers.lock"`
	Download ModDownloadCmd `cmd:"" help:"Download all required modules to cache"`
	Tidy     ModTidyCmd     `cmd:"" help:"Remove unused lock entries"`
	Verify   ModVerifyCmd   `cmd:"" help:"Verify cached modules against layers.lock hashes"`
	Update   ModUpdateCmd   `cmd:"" help:"Update module(s) to latest version"`
	List     ModListCmd     `cmd:"" help:"List modules with versions and their layers"`
}

// ModGetCmd downloads a module and updates layers.lock
type ModGetCmd struct {
	ModuleVersion string `arg:"" help:"Module path with optional version (module@version)"`
}

func (c *ModGetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Parse module@version
	parts := strings.SplitN(c.ModuleVersion, "@", 2)
	modulePath := parts[0]
	version := "main"
	if len(parts) == 2 {
		version = parts[1]
	}

	// Resolve ref to commit
	repoURL := ModuleGitURL(modulePath)
	commit, err := GitResolveRef(repoURL, version)
	if err != nil {
		return fmt.Errorf("resolving %s@%s: %w", modulePath, version, err)
	}

	// Download module
	cachePath, err := DownloadModule(modulePath, version)
	if err != nil {
		return err
	}

	// Discover layers in the module
	layerNames, err := DiscoverModuleLayers(cachePath)
	if err != nil {
		return err
	}

	// Compute hash
	hash, err := ComputeModuleHash(cachePath)
	if err != nil {
		return err
	}

	// Update lock file
	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf == nil {
		lf = &LockFile{}
	}

	lockFound := false
	for i := range lf.Modules {
		if lf.Modules[i].Module == modulePath {
			lf.Modules[i].Version = version
			lf.Modules[i].Commit = commit
			lf.Modules[i].Hash = hash
			lf.Modules[i].Layers = layerNames
			lockFound = true
			break
		}
	}
	if !lockFound {
		lf.Modules = append(lf.Modules, LockModule{
			Module:  modulePath,
			Version: version,
			Commit:  commit,
			Hash:    hash,
			Layers:  layerNames,
		})
	}

	if err := WriteLockFile(dir, lf); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Added %s@%s (%d layers: %s)\n", modulePath, version, len(layerNames), strings.Join(layerNames, ", "))
	fmt.Fprintf(os.Stderr, "Use refs like %s/<layer>@%s in layer.yml depends or images.yml layers\n", modulePath, version)
	return nil
}

// ModDownloadCmd downloads all required modules
type ModDownloadCmd struct{}

func (c *ModDownloadCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Collect modules from inline @version refs
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr != nil {
		return fmt.Errorf("cannot load images.yml: %w", cfgErr)
	}
	localLayers, err := ScanLayers(dir)
	if err != nil {
		return err
	}
	versions, err := CollectRequiredModulesVersioned(cfg, localLayers)
	if err != nil {
		return err
	}

	if len(versions) == 0 {
		fmt.Fprintf(os.Stderr, "No modules to download\n")
		return nil
	}

	for modPath, version := range versions {
		cached, err := IsModuleCached(modPath, version)
		if err != nil {
			return err
		}
		if cached {
			fmt.Fprintf(os.Stderr, "Already cached: %s@%s\n", modPath, version)
			continue
		}

		if _, err := DownloadModule(modPath, version); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "All modules downloaded\n")
	return nil
}

// ModTidyCmd removes unused lock entries
type ModTidyCmd struct{}

func (c *ModTidyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	localLayers, err := ScanLayers(dir)
	if err != nil {
		return err
	}
	versions, err := CollectRequiredModulesVersioned(cfg, localLayers)
	if err != nil {
		return err
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf == nil {
		fmt.Fprintf(os.Stderr, "No layers.lock found, nothing to tidy\n")
		return nil
	}

	var kept []LockModule
	for _, lm := range lf.Modules {
		if _, ok := versions[lm.Module]; ok {
			kept = append(kept, lm)
		} else {
			fmt.Fprintf(os.Stderr, "Removed unused: %s\n", lm.Module)
		}
	}

	// Check for missing lock entries
	for modPath := range versions {
		found := false
		for _, lm := range kept {
			if lm.Module == modPath {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Warning: module %s is referenced but not in layers.lock (run 'ov mod get %s@<version>')\n", modPath, modPath)
		}
	}

	lf.Modules = kept
	if err := WriteLockFile(dir, lf); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Tidy complete\n")
	return nil
}

// ModVerifyCmd verifies cached modules against layers.lock hashes
type ModVerifyCmd struct{}

func (c *ModVerifyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf == nil {
		return fmt.Errorf("no layers.lock found")
	}

	allOK := true
	for _, lm := range lf.Modules {
		cachePath, err := ModuleCachePath(lm.Module, lm.Version)
		if err != nil {
			return err
		}

		if _, err := os.Stat(cachePath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "MISSING: %s@%s (not in cache)\n", lm.Module, lm.Version)
			allOK = false
			continue
		}

		hash, err := ComputeModuleHash(cachePath)
		if err != nil {
			return err
		}

		if hash != lm.Hash {
			fmt.Fprintf(os.Stderr, "MISMATCH: %s@%s (expected %s, got %s)\n", lm.Module, lm.Version, lm.Hash, hash)
			allOK = false
		} else {
			fmt.Fprintf(os.Stderr, "OK: %s@%s\n", lm.Module, lm.Version)
		}
	}

	if !allOK {
		return fmt.Errorf("verification failed")
	}
	fmt.Fprintf(os.Stderr, "All modules verified\n")
	return nil
}

// ModUpdateCmd updates module(s) to latest version
type ModUpdateCmd struct {
	Module string `arg:"" optional:"" help:"Module to update (all if omitted)"`
}

func (c *ModUpdateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Collect modules from inline refs
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr != nil {
		return fmt.Errorf("cannot load images.yml: %w", cfgErr)
	}
	localLayers, err := ScanLayers(dir)
	if err != nil {
		return err
	}
	versions, err := CollectRequiredModulesVersioned(cfg, localLayers)
	if err != nil {
		return err
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf == nil {
		lf = &LockFile{}
	}

	for modPath, version := range versions {
		if c.Module != "" && modPath != c.Module {
			continue
		}

		repoURL := ModuleGitURL(modPath)
		commit, err := GitResolveRef(repoURL, version)
		if err != nil {
			return fmt.Errorf("resolving %s@%s: %w", modPath, version, err)
		}

		// Remove old cache if commit changed
		if lm := lf.FindLockModule(modPath); lm != nil && lm.Commit != commit {
			oldCache, _ := ModuleCachePath(modPath, lm.Version)
			os.RemoveAll(oldCache)
		}

		cachePath, err := ModuleCachePath(modPath, version)
		if err != nil {
			return err
		}
		os.RemoveAll(cachePath)

		cachePath, err = DownloadModule(modPath, version)
		if err != nil {
			return err
		}

		layerNames, err := DiscoverModuleLayers(cachePath)
		if err != nil {
			return err
		}
		hash, err := ComputeModuleHash(cachePath)
		if err != nil {
			return err
		}

		lockFound := false
		for j := range lf.Modules {
			if lf.Modules[j].Module == modPath {
				lf.Modules[j].Commit = commit
				lf.Modules[j].Hash = hash
				lf.Modules[j].Layers = layerNames
				lockFound = true
				break
			}
		}
		if !lockFound {
			lf.Modules = append(lf.Modules, LockModule{
				Module:  modPath,
				Version: version,
				Commit:  commit,
				Hash:    hash,
				Layers:  layerNames,
			})
		}

		fmt.Fprintf(os.Stderr, "Updated %s@%s -> %s\n", modPath, version, commit[:12])
	}

	return WriteLockFile(dir, lf)
}

// ModListCmd lists modules with versions and their layers
type ModListCmd struct{}

func (c *ModListCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, cfgErr := LoadConfig(dir)
	if cfgErr != nil {
		return fmt.Errorf("cannot load images.yml: %w", cfgErr)
	}
	localLayers, err := ScanLayers(dir)
	if err != nil {
		return err
	}
	versions, err := CollectRequiredModulesVersioned(cfg, localLayers)
	if err != nil {
		return err
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}

	for modPath, version := range versions {
		var layers string
		if lf != nil {
			if lm := lf.FindLockModule(modPath); lm != nil {
				layers = strings.Join(lm.Layers, ", ")
			}
		}
		if layers != "" {
			fmt.Printf("%s@%s [%s]\n", modPath, version, layers)
		} else {
			fmt.Printf("%s@%s\n", modPath, version)
		}
	}
	return nil
}

// gitRemoteToModulePath converts a git remote URL to a module path.
// e.g. "https://github.com/overthinkos/overthink.git" -> "github.com/overthinkos/overthink"
// e.g. "git@github.com:overthinkos/overthink.git" -> "github.com/overthinkos/overthink"
func gitRemoteToModulePath(remote string) string {
	remote = strings.TrimSpace(remote)

	// Handle SSH URLs (git@github.com:org/repo.git)
	if strings.HasPrefix(remote, "git@") {
		remote = strings.TrimPrefix(remote, "git@")
		remote = strings.Replace(remote, ":", "/", 1)
	}

	// Handle HTTPS URLs
	remote = strings.TrimPrefix(remote, "https://")
	remote = strings.TrimPrefix(remote, "http://")

	// Remove .git suffix
	remote = strings.TrimSuffix(remote, ".git")

	return remote
}
