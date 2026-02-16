package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldLayer creates a new layer directory with placeholder files
func ScaffoldLayer(dir string, name string) error {
	layerDir := filepath.Join(dir, "layers", name)

	// Check if layer already exists
	if _, err := os.Stat(layerDir); err == nil {
		return fmt.Errorf("layer %q already exists at %s", name, layerDir)
	}

	// Create layer directory
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		return fmt.Errorf("creating layer directory: %w", err)
	}

	// Create a placeholder layer.yml
	layerYml := filepath.Join(layerDir, "layer.yml")
	layerContent := fmt.Sprintf("# %s layer config\nrpm:\n  packages:\n    # Add RPM packages here\n", name)
	if err := os.WriteFile(layerYml, []byte(layerContent), 0644); err != nil {
		return fmt.Errorf("creating layer.yml: %w", err)
	}

	fmt.Printf("Created layer at %s\n", layerDir)
	fmt.Println("Files created:")
	fmt.Println("  layer.yml - Layer config (rpm/deb packages, depends, env, ports, route, service)")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")

	return nil
}
