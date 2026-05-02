package main

import (
	"os"
)

// LocalSpec is a reusable layer-stack template applied directly to a
// Linux filesystem (target:local deployments). Unlike pod/vm/k8s which
// wrap an image, a local deployment is defined entirely by its layer
// stack + install options + env — there's no OCI artifact backing it.
// A kind:local entry lets multiple deployments share the same profile
// (e.g., a "developer workstation" stack reused across machines).
//
// A target:local deployment MAY reference a template via
// `local: <name>` on kind:deployment OR inline `add_layers:` directly.
// Both are valid; templates are for reuse.
type LocalSpec struct {
	// Layers is the ordered layer stack applied to the host filesystem.
	// Required (use `layers: []` for an explicit stub placeholder; an
	// empty list emits a load-time WARNING but is permitted to support
	// staged template name reservation).
	Layers []string `yaml:"layers"`

	// InstallOpts are default install-time gates. CLI flags / deployment
	// overrides merge on top via InstallOptsConfig.ApplyTo (3-tier
	// precedence: CLI > deployment > template).
	InstallOpts *InstallOptsConfig `yaml:"install_opts,omitempty"`

	// Env are environment variables set in the user's shell profile when
	// the local install applies. Same format as ImageConfig.Env:
	// []string{"KEY=VALUE", ...}. Deployment env adds to / overrides on
	// key collision (deployment wins).
	Env []string `yaml:"env,omitempty"`

	// Description carries the Gherkin-shaped self-description (Feature/
	// Narrative/Tag/Scenario). Replaces the retired info:/status: scalar
	// fields. The status word lives in Description.Tag — walk the tag
	// list looking for "working"/"testing"/"broken" via descriptionStatus.
	Description *Description `yaml:"description,omitempty"`

	// Eval / DeployEval are optional target-specific checks (default
	// empty). Layer tests and per-deployment tests propagate
	// automatically.
	Eval       []Check `yaml:"eval,omitempty"`
	DeployEval []Check `yaml:"deploy_eval,omitempty"`
}

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns nil when the project has no overthink.yml, no `local:` map,
// or no entry by that name. Used by the deploy-add dispatcher to
// resolve a deployment's `local: <template-name>` reference.
func findLocalSpec(dir, name string) *LocalSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	if uf.Local == nil {
		return nil
	}
	return uf.Local[name]
}

// Force os import use — findLocalSpec doesn't reach for it but the
// import is kept stable for the package layout.
var _ = os.Getwd
