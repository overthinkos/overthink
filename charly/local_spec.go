package main

import (
	"os"
)

// LocalSpec is a reusable candy-stack template applied directly to a
// Linux filesystem (target:local deployments). Unlike pod/vm/k8s which
// wrap an image, a local deployment is defined entirely by its candy
// stack + install options + env — there's no OCI artifact backing it.
// A kind:local entry lets multiple deployments share the same profile
// (e.g., a "developer workstation" stack reused across machines).
//
// A target:local deployment MAY reference a template via
// `local: <name>` on kind:deployment OR inline `add_candy:` directly.
// Both are valid; templates are for reuse.
type LocalSpec struct {
	// Candy is the ordered candy stack applied to the host filesystem.
	// Required (use `candy: []` for an explicit stub placeholder; an
	// empty list emits a load-time WARNING but is permitted to support
	// staged template name reservation).
	Candy []string `yaml:"candy"`

	// InstallOpts are default install-time gates. CLI flags / deployment
	// overrides merge on top via InstallOptsConfig.ApplyTo (3-tier
	// precedence: CLI > deployment > template).
	InstallOpts *InstallOptsConfig `yaml:"install_opts,omitempty"`

	// Env are environment variables set in the user's shell profile when
	// the local install applies. Same format as BoxConfig.Env:
	// []string{"KEY=VALUE", ...}. Deployment env adds to / overrides on
	// key collision (deployment wins).
	Env []string `yaml:"env,omitempty"`

	// Description carries the Gherkin-shaped self-description (Feature/
	// Narrative/Tag/Scenario). Replaces the retired info:/status: scalar
	// fields. The status word lives in Description.Tag — walk the tag
	// list looking for "working"/"testing"/"broken" via descriptionStatus.
	Description *Description `yaml:"description,omitempty"`

	// Scenario carries optional target-specific acceptance scenarios (Op
	// steps). Candy and box scenarios propagate automatically.
	Scenario []Scenario `yaml:"scenario,omitempty"`

	// Note: there is NO image-fetch surface on a kind:local template.
	// Deploys apply candies (host packages + configs) only; container
	// images required for `charly check run` / `charly check live` are ensured by the
	// check preflight (see charly/check_image_preflight.go), sourced from the
	// score's `target_image:` + scenario `pod:` declarations. The
	// previous template-level `images:` field was removed in the
	// 2026-05 deploy-fetch-narrowing cutover; legacy YAML carrying it
	// hard-errors at validate time with a pointer to
	// `charly migrate`.
}

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns nil when the project has no charly.yml, no `local:` map,
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
	// Namespace-aware via the single resolver: a bare name hits this project's
	// `local:` map exactly as before, while a qualified `local: <ns>.<tmpl>`
	// ref descends into the imported namespace. resolveLocalRef tolerates a nil
	// Local map, so the previous explicit nil-guard is no longer needed.
	spec, _ := uf.ProjectConfig().resolveLocalRef(name)
	return spec
}

// Force os import use — findLocalSpec doesn't reach for it but the
// import is kept stable for the package layout.
var _ = os.Getwd
