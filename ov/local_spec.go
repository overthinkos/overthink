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

	// Images is the list of container images this template needs
	// available in local podman storage before tests can run. Each
	// entry is an identifier resolvable via the same three input
	// forms `ov image pull` accepts:
	//
	//   - Short name (e.g. "eval-target") — resolved via cfg.Images
	//     to a registry ref at deploy time.
	//   - Fully-qualified registry ref (e.g.
	//     "ghcr.io/overthinkos/eval-target:2026.124.1253") — passed
	//     through unchanged.
	//   - Remote project ref (e.g.
	//     "@github.com/overthinkos/overthink/eval-target:latest") —
	//     resolves the repo, reads its image.yml, then pulls the
	//     declared registry ref.
	//
	// At deploy time, LocalDeployTarget runs an ensure-images
	// pre-pass: for each entry, short-circuit when LocalImageExists,
	// else try `ov image pull <ref>`, else fall back to
	// `bin/ov image build <name>` if the image is buildable from this
	// project. See compileImagesSteps in install_build.go for the IR
	// emission and execEnsureImage in deploy_target_local.go for the
	// runtime path. 2026-05 cutover.
	Images []string `yaml:"images,omitempty"`
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
