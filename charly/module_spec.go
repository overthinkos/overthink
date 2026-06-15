package main

// ModuleSpec is the charly authoring shape for a Calamares-style installer module
// (`module.desc` projected as YAML). Authors typically won't write these by
// hand short-term; the kind exists so that future Calamares-import lands
// cleanly without inventing new vocabulary.
//
// `requiredModules` keeps Calamares' camelCase intentionally — it is the only
// field name that round-trips verbatim through a future emitter. Everywhere
// else, charly stays snake_case.
type ModuleSpec struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // charly extension; Calamares module.desc has no description

	Type      string `yaml:"type,omitempty" json:"type,omitempty"`           // job | view
	Interface string `yaml:"interface,omitempty" json:"interface,omitempty"` // qtplugin | python | process

	Load    string   `yaml:"load,omitempty" json:"load,omitempty"`       // shared library name (qtplugin)
	Script  string   `yaml:"script,omitempty" json:"script,omitempty"`   // python entry point
	Command []string `yaml:"command,omitempty" json:"command,omitempty"` // process module command

	RequiredModules []string `yaml:"requiredModules,omitempty" json:"requiredModules,omitempty"` // camelCase outlier — Calamares wire form
	Weight          int      `yaml:"weight,omitempty" json:"weight,omitempty"`
	NoConfig        bool     `yaml:"noconfig,omitempty" json:"noconfig,omitempty"`
	Emergency       bool     `yaml:"emergency,omitempty" json:"emergency,omitempty"`
	Timeout         int      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Chroot          bool     `yaml:"chroot,omitempty" json:"chroot,omitempty"`
}

// ModuleDoc wraps a single ModuleSpec with an explicit Name — the standalone
// `kind: module` form.
type ModuleDoc struct {
	Name       string `yaml:"name" json:"name"`
	ModuleSpec `yaml:",inline"`
}
