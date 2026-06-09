package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldLayer creates a new layer directory with placeholder files
func ScaffoldLayer(dir string, name string) error {
	layerDir := filepath.Join(dir, DefaultCandyDir, name)

	// Check if layer already exists
	if _, err := os.Stat(layerDir); err == nil {
		return fmt.Errorf("layer %q already exists at %s", name, layerDir)
	}

	// Create layer directory
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		return fmt.Errorf("creating layer directory: %w", err)
	}

	// Create a placeholder candy manifest in the canonical kind-keyed form,
	// named via the single configurable default (UnifiedFileName).
	layerYml := filepath.Join(layerDir, UnifiedFileName)
	layerContent := fmt.Sprintf("# %s candy config\ncandy:\n  rpm:\n    packages:\n      # Add RPM packages here\n", name)
	if err := os.WriteFile(layerYml, []byte(layerContent), 0644); err != nil {
		return fmt.Errorf("creating %s: %w", UnifiedFileName, err)
	}

	fmt.Printf("Created layer at %s\n", layerDir)
	fmt.Println("Files created:")
	fmt.Println("  charly.yml - Candy config (rpm/deb packages, require, env, ports, route, service)")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")

	return nil
}
