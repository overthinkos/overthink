package main

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PackageItem is a single package entry. Polymorphic between bare scalar
// (`- nginx`) and object form (`- {name: nginx, description: open-source build}`).
// Calamares-shaped: matches the package entries in `netinstall.yaml`.
//
// The bare-scalar shorthand resolves into Name only; the object form populates
// both Name and Description. JSON encoding always emits the object form so
// labels round-trip cleanly.
type PackageItem struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// UnmarshalYAML accepts both scalar and mapping forms.
func (p *PackageItem) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		p.Name = node.Value
		p.Description = ""
		return nil
	}
	if node.Kind == yaml.MappingNode {
		var raw struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		if err := node.Decode(&raw); err != nil {
			return err
		}
		p.Name = raw.Name
		p.Description = raw.Description
		return nil
	}
	return fmt.Errorf("invalid package entry: expected scalar or mapping, got %v", node.Kind)
}

// MarshalYAML emits the bare-scalar shorthand when only Name is set, otherwise
// the object form. Keeps migrated layer.yml files concise where the long form
// adds no value.
func (p PackageItem) MarshalYAML() (interface{}, error) {
	if p.Description == "" {
		return p.Name, nil
	}
	return struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description,omitempty"`
	}{p.Name, p.Description}, nil
}

// AURPackages is the per-distro AUR sub-block under `distros.arch.aur`.
// One ov-specific extension to the otherwise-flat Calamares package shape:
// AUR sources need a separate manifest because they are built via yay in a
// builder stage (not pacman directly).
type AURPackages struct {
	Package []PackageItem `yaml:"package,omitempty" json:"package,omitempty"`
	Options []string      `yaml:"option,omitempty" json:"options,omitempty"`
	// Replaces lists distro-repo packages whose file paths conflict
	// with the AUR build artifact. Each entry is removed via
	// `pacman -Rs --noconfirm <pkg>` BEFORE the AUR `pacman -U`
	// install on host (`target: local`) deploys. Idempotent — entries
	// not currently installed are silently skipped. Required when the
	// AUR build owns paths also owned by an Arch repo package (e.g.
	// `visual-studio-code-bin` and `code` both own /usr/bin/code).
	// OCI image builds ignore this field — fresh rootfs has no
	// conflicting package.
	Replaces []string `yaml:"replace,omitempty" json:"replaces,omitempty"`
}

// DistroPackages carries per-distro package overrides plus format-specific
// extras (copr, repos, options, exclude, modules) inherited from the legacy
// per-format / per-distro-tag sections.
//
// The map key on `LayerYAML.Distros` / `GroupSpec.Distros` identifies the
// distro (e.g. `fedora`, `arch`, `debian`, `ubuntu`) or a versioned
// variant (`debian-13`, `ubuntu-24.04`).
type DistroPackages struct {
	Package []PackageItem    `yaml:"package,omitempty" json:"package,omitempty"`
	Copr    []string         `yaml:"copr,omitempty" json:"copr,omitempty"`       // fedora-only
	Repo    []map[string]any `yaml:"repo,omitempty" json:"repo,omitempty"`       // free-form per-distro repo blocks
	Exclude []string         `yaml:"exclude,omitempty" json:"exclude,omitempty"` // package excludes
	Options []string         `yaml:"option,omitempty" json:"options,omitempty"`  // extra installer flags
	Module  []string         `yaml:"module,omitempty" json:"module,omitempty"`   // dnf module enable
	AUR     *AURPackages     `yaml:"aur,omitempty" json:"aur,omitempty"`         // arch-only

	// Raw captures the entire YAML map for template rendering. Populated by
	// the migrator and the parser in lockstep so install templates that read
	// fields outside the typed surface (a custom `keys:` block, etc.) still
	// see the original data.
	Raw map[string]interface{} `yaml:"-" json:"-"`
}

// PackageNames returns just the names from a PackageItem list, in order.
// Convenience for places that only need the install-target list.
func PackageNames(items []PackageItem) []string {
	out := make([]string, 0, len(items))
	for _, p := range items {
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}
	return out
}

// PackageItemsFromStrings constructs a PackageItem slice from bare names.
// Used by the migrator when collapsing legacy format sections that only
// carried `packages: [name1, name2]`.
func PackageItemsFromStrings(names []string) []PackageItem {
	out := make([]PackageItem, 0, len(names))
	for _, n := range names {
		if n != "" {
			out = append(out, PackageItem{Name: n})
		}
	}
	return out
}
