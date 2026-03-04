package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ModCmd groups module management subcommands
type ModCmd struct {
	Init     ModInitCmd     `cmd:"" help:"Create layers.mod with module path from git remote"`
	Get      ModGetCmd      `cmd:"" help:"Add/update a module dependency"`
	Remove   ModRemoveCmd   `cmd:"" help:"Remove a module dependency"`
	Download ModDownloadCmd `cmd:"" help:"Download all required modules to cache"`
	Tidy     ModTidyCmd     `cmd:"" help:"Remove unused requires, add missing ones"`
	Verify   ModVerifyCmd   `cmd:"" help:"Verify cached modules against layers.lock hashes"`
	Update   ModUpdateCmd   `cmd:"" help:"Update module(s) to latest version"`
	List     ModListCmd     `cmd:"" help:"List modules with versions and their layers"`
}

// ModInitCmd creates a layers.mod file
type ModInitCmd struct{}

func (c *ModInitCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Check if layers.mod already exists
	if fileExists(dir + "/layers.mod") {
		return fmt.Errorf("layers.mod already exists")
	}

	// Detect module path from git remote
	modulePath := ""
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		remote := strings.TrimSpace(string(out))
		modulePath = gitRemoteToModulePath(remote)
	}

	if modulePath == "" {
		return fmt.Errorf("could not detect module path from git remote; create layers.mod manually")
	}

	mf := &ModFile{
		Module: modulePath,
	}

	if err := WriteModFile(dir, mf); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Created layers.mod with module: %s\n", modulePath)
	return nil
}

// ModGetCmd adds or updates a module dependency
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
	version := "latest"
	if len(parts) == 2 {
		version = parts[1]
	}

	// Load or create layers.mod
	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found (run 'ov mod init' first)")
	}

	// If version is "latest", resolve to the default branch
	if version == "latest" {
		version = "main" // default; will be resolved to commit via git
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

	// Update or add require entry
	found := false
	for i := range mf.Require {
		if mf.Require[i].Module == modulePath {
			mf.Require[i].Version = version
			found = true
			break
		}
	}
	if !found {
		mf.Require = append(mf.Require, ModRequire{
			Module:  modulePath,
			Version: version,
		})
	}

	if err := WriteModFile(dir, mf); err != nil {
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

	// Update or add lock entry
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
	return nil
}

// ModRemoveCmd removes a module dependency
type ModRemoveCmd struct {
	Module string `arg:"" help:"Module path to remove"`
}

func (c *ModRemoveCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found")
	}

	// Remove from requires
	var newReqs []ModRequire
	found := false
	for _, req := range mf.Require {
		if req.Module == c.Module {
			found = true
		} else {
			newReqs = append(newReqs, req)
		}
	}
	if !found {
		return fmt.Errorf("module %q not found in layers.mod", c.Module)
	}
	mf.Require = newReqs

	// Remove from replaces
	var newReplaces []ModReplace
	for _, rep := range mf.Replace {
		if rep.Module != c.Module {
			newReplaces = append(newReplaces, rep)
		}
	}
	mf.Replace = newReplaces

	if err := WriteModFile(dir, mf); err != nil {
		return err
	}

	// Remove from lock file
	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf != nil {
		var newModules []LockModule
		for _, lm := range lf.Modules {
			if lm.Module != c.Module {
				newModules = append(newModules, lm)
			}
		}
		lf.Modules = newModules
		if err := WriteLockFile(dir, lf); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Removed %s\n", c.Module)
	return nil
}

// ModDownloadCmd downloads all required modules
type ModDownloadCmd struct{}

func (c *ModDownloadCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found")
	}

	for _, req := range mf.Require {
		// Skip replaced modules
		if mf.FindReplace(req.Module) != nil {
			fmt.Fprintf(os.Stderr, "Skipping %s (replaced by local path)\n", req.Module)
			continue
		}

		cached, err := IsModuleCached(req.Module, req.Version)
		if err != nil {
			return err
		}
		if cached {
			fmt.Fprintf(os.Stderr, "Already cached: %s@%s\n", req.Module, req.Version)
			continue
		}

		if _, err := DownloadModule(req.Module, req.Version); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "All modules downloaded\n")
	return nil
}

// ModTidyCmd removes unused requires and adds missing ones
type ModTidyCmd struct{}

func (c *ModTidyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found")
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Find all modules referenced by images
	usedModules := CollectRequiredModules(cfg)

	// Also scan layer dependencies for cross-module refs
	layers, err := ScanAllLayers(dir)
	if err != nil {
		return err
	}
	for _, layer := range layers {
		for _, dep := range layer.Depends {
			if IsRemoteLayerRef(dep) {
				modPath, _ := SplitRemoteLayerRef(dep)
				usedModules[modPath] = true
			}
		}
	}

	// Remove unused requires
	var kept []ModRequire
	for _, req := range mf.Require {
		if usedModules[req.Module] {
			kept = append(kept, req)
		} else {
			fmt.Fprintf(os.Stderr, "Removed unused: %s\n", req.Module)
		}
	}

	// Check for missing requires
	for modPath := range usedModules {
		found := false
		for _, req := range kept {
			if req.Module == modPath {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "Warning: module %s is referenced but not in layers.mod (run 'ov mod get %s@<version>')\n", modPath, modPath)
		}
	}

	mf.Require = kept
	if err := WriteModFile(dir, mf); err != nil {
		return err
	}

	// Clean up lock file too
	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf != nil {
		var keptModules []LockModule
		for _, lm := range lf.Modules {
			if usedModules[lm.Module] {
				keptModules = append(keptModules, lm)
			}
		}
		lf.Modules = keptModules
		if err := WriteLockFile(dir, lf); err != nil {
			return err
		}
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

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}

	allOK := true
	for _, lm := range lf.Modules {
		// Skip replaced modules
		if mf != nil && mf.FindReplace(lm.Module) != nil {
			fmt.Fprintf(os.Stderr, "Skipping %s (replaced by local path)\n", lm.Module)
			continue
		}

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

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found")
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}
	if lf == nil {
		lf = &LockFile{}
	}

	for i := range mf.Require {
		req := &mf.Require[i]
		if c.Module != "" && req.Module != c.Module {
			continue
		}

		// Skip replaced modules
		if mf.FindReplace(req.Module) != nil {
			continue
		}

		// Re-resolve the version to get latest commit
		repoURL := ModuleGitURL(req.Module)
		commit, err := GitResolveRef(repoURL, req.Version)
		if err != nil {
			return fmt.Errorf("resolving %s@%s: %w", req.Module, req.Version, err)
		}

		// Remove old cache if commit changed
		if lm := lf.FindLockModule(req.Module); lm != nil && lm.Commit != commit {
			oldCache, _ := ModuleCachePath(req.Module, lm.Version)
			os.RemoveAll(oldCache)
		}

		// Download (re-download if needed)
		cachePath, err := ModuleCachePath(req.Module, req.Version)
		if err != nil {
			return err
		}
		os.RemoveAll(cachePath) // force re-download

		cachePath, err = DownloadModule(req.Module, req.Version)
		if err != nil {
			return err
		}

		// Update lock
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
			if lf.Modules[j].Module == req.Module {
				lf.Modules[j].Commit = commit
				lf.Modules[j].Hash = hash
				lf.Modules[j].Layers = layerNames
				lockFound = true
				break
			}
		}
		if !lockFound {
			lf.Modules = append(lf.Modules, LockModule{
				Module:  req.Module,
				Version: req.Version,
				Commit:  commit,
				Hash:    hash,
				Layers:  layerNames,
			})
		}

		fmt.Fprintf(os.Stderr, "Updated %s@%s -> %s\n", req.Module, req.Version, commit[:12])
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

	mf, err := ParseModFile(dir)
	if err != nil {
		return err
	}
	if mf == nil {
		return fmt.Errorf("no layers.mod found")
	}

	lf, err := ParseLockFile(dir)
	if err != nil {
		return err
	}

	for _, req := range mf.Require {
		replaced := ""
		if rep := mf.FindReplace(req.Module); rep != nil {
			replaced = fmt.Sprintf(" => %s", rep.Path)
		}

		var layers string
		if lf != nil {
			if lm := lf.FindLockModule(req.Module); lm != nil {
				layers = strings.Join(lm.Layers, ", ")
			}
		}

		if layers != "" {
			fmt.Printf("%s@%s%s [%s]\n", req.Module, req.Version, replaced, layers)
		} else {
			fmt.Printf("%s@%s%s\n", req.Module, req.Version, replaced)
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
