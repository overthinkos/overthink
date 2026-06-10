package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldCandy creates a new candy directory with placeholder files
func ScaffoldCandy(dir string, name string) error {
	candyDir := filepath.Join(dir, DefaultCandyDir, name)

	// Check if candy already exists
	if _, err := os.Stat(candyDir); err == nil {
		return fmt.Errorf("layer %q already exists at %s", name, candyDir)
	}

	// Create candy directory
	if err := os.MkdirAll(candyDir, 0755); err != nil {
		return fmt.Errorf("creating layer directory: %w", err)
	}

	// Create a placeholder candy manifest in the canonical kind-keyed form,
	// named via the single configurable default (UnifiedFileName).
	candyYml := filepath.Join(candyDir, UnifiedFileName)
	candyContent := fmt.Sprintf("# %s candy config\ncandy:\n  rpm:\n    packages:\n      # Add RPM packages here\n", name)
	if err := os.WriteFile(candyYml, []byte(candyContent), 0644); err != nil {
		return fmt.Errorf("creating %s: %w", UnifiedFileName, err)
	}

	fmt.Printf("Created layer at %s\n", candyDir)
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
