package main

// GroupSpec is the ov authoring shape for a Calamares-style "package group"
// (the netinstall.yaml entry shape). A group is a named, hierarchical bundle
// of packages with selection-state metadata, optionally referencing an ov
// layer for install logic via the `requires:` list.
//
// All Calamares group fields appear at the top level so a Calamares parser
// reading an ov group.yml sees a faithful netinstall group. ov-specific
// extensions (`distros:` for per-distro overrides, `requires:` for layer
// dependencies) sit alongside Calamares fields and are silently ignored by
// Calamares' YAML parser.
type GroupSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Calamares-shaped flat package list (the canonical surface).
	Packages []PackageItem `yaml:"packages,omitempty" json:"packages,omitempty"`

	// ov extension: per-distro overrides.
	Distros map[string]*DistroPackages `yaml:"distros,omitempty" json:"distros,omitempty"`

	// Calamares selection-state metadata.
	Hidden       bool   `yaml:"hidden,omitempty" json:"hidden,omitempty"`
	Selected     bool   `yaml:"selected,omitempty" json:"selected,omitempty"`
	Critical     bool   `yaml:"critical,omitempty" json:"critical,omitempty"`
	Immutable    bool   `yaml:"immutable,omitempty" json:"immutable,omitempty"`
	Expanded     bool   `yaml:"expanded,omitempty" json:"expanded,omitempty"`
	NonCheckable bool   `yaml:"noncheckable,omitempty" json:"noncheckable,omitempty"`
	PreInstall   string `yaml:"pre_install,omitempty" json:"pre_install,omitempty"`
	PostInstall  string `yaml:"post_install,omitempty" json:"post_install,omitempty"`
	Source       string `yaml:"source,omitempty" json:"source,omitempty"`

	// Recursive: subgroups can be inline or named refs to other groups
	// declared in the unified file. Polymorphic decoding TBD; for now
	// inline-only is supported (mirrors Calamares' usual netinstall.yaml).
	Subgroups []*GroupSpec `yaml:"subgroups,omitempty" json:"subgroups,omitempty"`

	// ov extension: group dependencies (other groups that must be installed
	// first). Calamares groups have no analogous field; modules use
	// `requiredModules`. We use the shorter `requires:` for ov consistency.
	Requires []string `yaml:"requires,omitempty" json:"requires,omitempty"`
}

// GroupDoc wraps a single GroupSpec with an explicit Name — the standalone
// `kind: group` form. Bundles concatenated via YAML --- separators are
// supported the same way as LayerDoc / VmDoc.
type GroupDoc struct {
	Name      string `yaml:"name"`
	GroupSpec `yaml:",inline"`
}
