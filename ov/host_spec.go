package main

// HostSpec is a reusable host-deployment template. Unlike pod/vm/k8s which
// wrap an image, a host deployment is defined entirely by its layer stack
// + install options + env — there's no OCI artifact backing it. A
// kind:host entry lets multiple host deployments share the same profile
// (e.g., a common "developer workstation" stack reused across machines).
//
// A host deployment MAY reference a template via `host: <name>` OR inline
// `layers:` directly on kind:deployment. Both are valid; templates are
// for reuse.
type HostSpec struct {
	// Layers is the ordered layer stack applied to the host filesystem.
	// Required. Deployment `add_layers:` is appended (not replaced) — this
	// is the one list-field asymmetry across target kinds (see plan's
	// override semantics table).
	Layers []string `yaml:"layers"`

	// InstallOpts are default install-time gates. CLI flags / deployment
	// overrides merge on top via InstallOptsConfig.ApplyTo.
	InstallOpts *InstallOptsConfig `yaml:"install_opts,omitempty"`

	// Env are environment variables set in the user's shell profile when
	// the host install applies. Same format as ImageConfig.Env:
	// []string{"KEY=VALUE", ...}. Deployment env adds to / overrides.
	Env []string `yaml:"env,omitempty"`

	// Tests / DeployTests are optional target-specific checks (default
	// empty). Layer tests and per-deployment tests propagate
	// automatically.
	Eval        []Check `yaml:"eval,omitempty"`
	DeployEval  []Check `yaml:"deploy_eval,omitempty"`
}
