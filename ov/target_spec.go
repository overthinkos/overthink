package main

// TargetSpec is the ov authoring shape for a Calamares-style install target —
// the assemblage of modules, branding, and execution sequence that produces
// one installer experience (`settings.conf` projected as YAML).
//
// `kind: target` is the third Calamares-aligned kind alongside `kind: group`
// and `kind: module`. The lexical overlap with the deployment-entry `target:`
// scalar field (e.g. `target: pod`) is accepted: different YAML positions,
// no parser collision.
//
// The future Calamares emitter (out of scope for this cutover) reads
// TargetSpec to write `/etc/calamares/settings.conf` + `/etc/calamares/branding/`.
type TargetSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Calamares settings.conf core fields.
	ModulesSearch []string             `yaml:"modules-search,omitempty" json:"modules-search,omitempty"`
	Instances     []TargetInstance     `yaml:"instance,omitempty" json:"instances,omitempty"`
	Sequence      *TargetSequence      `yaml:"sequence,omitempty" json:"sequence,omitempty"`
	Branding      string               `yaml:"branding,omitempty" json:"branding,omitempty"`
	PromptInstall bool                 `yaml:"prompt-install,omitempty" json:"prompt-install,omitempty"`
	DontChroot    bool                 `yaml:"dont-chroot,omitempty" json:"dont-chroot,omitempty"`
	OemSetup      bool                 `yaml:"oem-setup,omitempty" json:"oem-setup,omitempty"`
	DisableCancel bool                 `yaml:"disable-cancel,omitempty" json:"disable-cancel,omitempty"`

	// ov extensions: bind groups and images to the target.
	Group []string `yaml:"group,omitempty" json:"group,omitempty"` // group names from the unified file
	Image []string `yaml:"image,omitempty" json:"image,omitempty"` // image names from the unified file
}

// TargetInstance mirrors Calamares' `instances:` entries — multiple
// configurations of the same module under different IDs.
type TargetInstance struct {
	ID     string `yaml:"id" json:"id"`
	Module string `yaml:"module" json:"module"`
	Config string `yaml:"config,omitempty" json:"config,omitempty"`
}

// TargetSequence carries Calamares' show / exec module-name lists.
// Phase order in Calamares: show modules collect user input → exec modules
// run during installation.
type TargetSequence struct {
	Show []string `yaml:"show,omitempty" json:"show,omitempty"`
	Exec []string `yaml:"exec,omitempty" json:"exec,omitempty"`
}

// TargetDoc wraps a single TargetSpec with an explicit Name — the standalone
// `kind: target` form.
type TargetDoc struct {
	Name       string `yaml:"name"`
	TargetSpec `yaml:",inline"`
}
