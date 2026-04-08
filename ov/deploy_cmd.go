package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployCmd manages deploy.yml deployment overrides.
type DeployCmd struct {
	Show   DeployShowCmd   `cmd:"" help:"Show current deploy.yml overrides"`
	Export DeployExportCmd `cmd:"" help:"Export effective config as deploy.yml"`
	Import DeployImportCmd `cmd:"" help:"Import deploy.yml file(s) into config"`
	Reset  DeployResetCmd  `cmd:"" help:"Remove deploy.yml overrides"`
	Status DeployStatusCmd `cmd:"" help:"Show sync status between deploy.yml and quadlet files"`
	Path   DeployPathCmd   `cmd:"" help:"Print deploy.yml file path"`
}

// DeployShowCmd displays the current deploy.yml content.
type DeployShowCmd struct {
	Image    string `arg:"" optional:"" help:"Show overrides for a specific image"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *DeployShowCmd) Run() error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Images) == 0 {
		fmt.Println("No deploy.yml configured")
		return nil
	}

	if c.Image != "" {
		key := deployKey(c.Image, c.Instance)
		entry, ok := dc.Images[key]
		if !ok {
			fmt.Printf("No overrides for image %q\n", key)
			return nil
		}
		// Print just this image's config
		out := &DeployConfig{Images: map[string]DeployImageConfig{key: entry}}
		return marshalToStdout(out)
	}

	return marshalToStdout(dc)
}

// DeployExportCmd exports the current effective runtime configuration.
type DeployExportCmd struct {
	Images []string `arg:"" optional:"" help:"Images to export (default: all with overrides)"`
	Output string   `short:"o" help:"Write to file instead of stdout"`
	All    bool     `help:"Export all enabled images with all runtime fields"`
}

func (c *DeployExportCmd) Run() error {
	if c.All {
		return c.exportAll()
	}
	return c.exportOverrides()
}

func (c *DeployExportCmd) exportAll() error {
	dir, _ := os.Getwd()
	cfg, err := LoadConfigRaw(dir)
	if err != nil {
		return fmt.Errorf("loading images.yml: %w", err)
	}
	dc := ExportAllImages(cfg)
	if len(c.Images) > 0 {
		dc = filterDeployImages(dc, c.Images)
	}
	return c.output(dc)
}

func (c *DeployExportCmd) exportOverrides() error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Images) == 0 {
		fmt.Fprintln(os.Stderr, "No deploy.yml overrides to export")
		return nil
	}
	if len(c.Images) > 0 {
		dc = filterDeployImages(dc, c.Images)
	}
	return c.output(dc)
}

func (c *DeployExportCmd) output(dc *DeployConfig) error {
	if c.Output != "" {
		data, err := yaml.Marshal(dc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(c.Output, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", c.Output, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", c.Output)
		return nil
	}
	return marshalToStdout(dc)
}

// DeployImportCmd loads deploy.yml file(s) into ~/.config/ov/deploy.yml.
type DeployImportCmd struct {
	Files   []string `arg:"" help:"Deploy YAML files to import (merged left-to-right)"`
	Replace bool     `help:"Replace entire deploy.yml instead of merging with existing"`
	Image   string   `long:"image" help:"Import only this image's config"`
}

func (c *DeployImportCmd) Run() error {
	// Load input files
	var inputs []*DeployConfig
	for _, f := range c.Files {
		dc, err := LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	// Start with existing or empty
	var base *DeployConfig
	if !c.Replace {
		existing, err := LoadDeployConfig()
		if err != nil {
			return err
		}
		base = existing
	}
	if base == nil {
		base = &DeployConfig{Images: make(map[string]DeployImageConfig)}
	}

	// Merge input files left-to-right
	merged := MergeDeployConfigs(append([]*DeployConfig{base}, inputs...)...)

	// Filter to single image if requested
	if c.Image != "" {
		entry, ok := merged.Images[c.Image]
		if !ok {
			return fmt.Errorf("image %q not found in input files", c.Image)
		}
		// Preserve other images from existing config, replace only the target
		if !c.Replace {
			existing, _ := LoadDeployConfig()
			if existing != nil {
				existing.Images[c.Image] = entry
				merged = existing
			} else {
				merged = &DeployConfig{Images: map[string]DeployImageConfig{c.Image: entry}}
			}
		} else {
			merged = &DeployConfig{Images: map[string]DeployImageConfig{c.Image: entry}}
		}
	}

	if err := SaveDeployConfig(merged); err != nil {
		return err
	}

	path, _ := DeployConfigPath()
	fmt.Fprintf(os.Stderr, "Imported %d file(s) into %s\n", len(c.Files), path)
	return nil
}

// DeployResetCmd removes deploy.yml overrides.
type DeployResetCmd struct {
	Image    string `arg:"" optional:"" help:"Image to reset (omit to clear all)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *DeployResetCmd) Run() error {
	if c.Image == "" {
		// Clear entire deploy.yml
		path, err := DeployConfigPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No deploy.yml to remove")
				return nil
			}
			return err
		}
		fmt.Println("Removed deploy.yml")
		return nil
	}

	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}
	if dc == nil {
		fmt.Printf("No overrides for image %q\n", c.Image)
		return nil
	}

	key := deployKey(c.Image, c.Instance)
	if _, ok := dc.Images[key]; !ok {
		fmt.Printf("No overrides for image %q\n", key)
		return nil
	}

	RemoveImageDeploy(dc, key)

	if len(dc.Images) == 0 {
		// No images left — remove the file
		path, _ := DeployConfigPath()
		os.Remove(path)
		fmt.Printf("Removed overrides for %q (deploy.yml now empty, removed)\n", key)
		return nil
	}

	if err := SaveDeployConfig(dc); err != nil {
		return err
	}
	fmt.Printf("Removed overrides for %q\n", key)
	return nil
}

// DeployPathCmd prints the deploy.yml file path.
type DeployPathCmd struct{}

func (c *DeployPathCmd) Run() error {
	path, err := DeployConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// DeployStatusCmd shows sync status between deploy.yml and quadlet files.
type DeployStatusCmd struct{}

func (c *DeployStatusCmd) Run() error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return err
	}

	// Enumerate quadlet files
	qdir, qdirErr := quadletDir()
	quadletImages := make(map[string]bool)
	if qdirErr == nil {
		entries, readErr := os.ReadDir(qdir)
		if readErr == nil {
			for _, e := range entries {
				name := e.Name()
				if len(name) > 3 && name[:3] == "ov-" && len(name) > 10 && name[len(name)-10:] == ".container" {
					imageName := name[3 : len(name)-10]
					quadletImages[imageName] = true
				}
			}
		}
	}

	// Map deploy keys to quadlet stems for cross-referencing
	// e.g., "selkies-desktop/foo" → quadlet stem "selkies-desktop-foo"
	deployToStem := make(map[string]string) // deploy key → quadlet stem
	stemToDeploy := make(map[string]string) // quadlet stem → deploy key
	if dc != nil {
		for key := range dc.Images {
			img, inst := parseDeployKey(key)
			stem := strings.TrimPrefix(containerNameInstance(img, inst), "ov-")
			deployToStem[key] = stem
			stemToDeploy[stem] = key
		}
	}

	if len(deployToStem) == 0 && len(quadletImages) == 0 {
		fmt.Println("No deploy.yml entries and no quadlet files found")
		return nil
	}

	// Stale deploy.yml entries (no quadlet)
	for key, stem := range deployToStem {
		if !quadletImages[stem] {
			fmt.Printf("%-40s deploy.yml: yes  quadlet: no   (stale config)\n", key)
		}
	}
	// Both exist or quadlet only
	for stem := range quadletImages {
		if key, ok := stemToDeploy[stem]; ok {
			fmt.Printf("%-40s deploy.yml: yes  quadlet: yes  (ok)\n", key)
		} else {
			fmt.Printf("%-40s deploy.yml: no   quadlet: yes  (no overrides)\n", stem)
		}
	}

	return nil
}

// --- helpers ---

func marshalToStdout(dc *DeployConfig) error {
	data, err := yaml.Marshal(dc)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func filterDeployImages(dc *DeployConfig, names []string) *DeployConfig {
	filtered := &DeployConfig{Images: make(map[string]DeployImageConfig)}
	for _, name := range names {
		if entry, ok := dc.Images[name]; ok {
			filtered.Images[name] = entry
		}
	}
	return filtered
}
