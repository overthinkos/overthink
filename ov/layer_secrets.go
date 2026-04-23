package main

// layer_secrets.go — resolver for layer.yml `secret_requires:` and
// `secret_accepts:` when a layer is being applied via host / vm / ssh deploy
// targets (the non-container install-plan flow).
//
// Container targets have their own path (ProvisionPodmanSecrets, see
// secrets.go) that mounts secrets as podman secrets / env at container-run
// time. That path runs AFTER build and does not inject env into the layer's
// setup.sh. For install-plan-based targets, the setup scripts run directly
// on the deploy target, so the credential-store value must be resolved on the
// operator side and passed through as env on the step.
//
// This file provides a small, substrate-neutral helper that takes a Layer
// and returns the resolved env map. Caller responsibility: merge into the
// emitted TaskStep's task.env at plan-build time (ov/deploy_add_cmd.go —
// Task #6), or prepend as a shell prelude in the heredoc renderer.

import (
	"fmt"
	"strings"
)

// ResolveLayerSecrets walks the layer's secret_requires + secret_accepts
// and resolves each via the credential store. Returns the env map, plus a
// list of REQUIRED secrets that failed to resolve (empty = everything is
// satisfied). Callers should fail the deploy when the missing list is
// non-empty — per R1, missing required secrets are a hard error, never a
// silent skip.
//
// The Key field on an EnvDependency follows the format "<service>/<key>"
// and must start with "ov/" (enforced by validate.go). When Key is empty,
// the default lookup is service="ov/secret", key=Name.
func ResolveLayerSecrets(layer *Layer) (env map[string]string, missingRequired []string) {
	env = map[string]string{}
	if layer == nil {
		return env, nil
	}

	if layer.HasSecretRequires {
		for _, dep := range layer.SecretRequires() {
			val := resolveEnvDepValue(dep)
			if val == "" {
				missingRequired = append(missingRequired, dep.Name)
				continue
			}
			env[dep.Name] = val
		}
	}

	if layer.HasSecretAccepts {
		for _, dep := range layer.SecretAccepts() {
			val := resolveEnvDepValue(dep)
			if val == "" {
				// Accepts are optional — fall back to the declared default
				// if one is set. Absence is not an error.
				if dep.Default != "" {
					env[dep.Name] = dep.Default
				}
				continue
			}
			env[dep.Name] = val
		}
	}

	return env, missingRequired
}

// resolveEnvDepValue looks up one EnvDependency against the credential store.
// Key format: "<service>/<key>" (must start with "ov/" per validate.go);
// default lookup is service="ov/secret", key=dep.Name.
func resolveEnvDepValue(dep EnvDependency) string {
	service, key := "ov/secret", dep.Name
	if dep.Key != "" {
		if idx := strings.LastIndex(dep.Key, "/"); idx > 0 {
			service = dep.Key[:idx]
			key = dep.Key[idx+1:]
		}
	}
	// Pass dep.Name as envVar so the operator can provide the value
	// via `export K3S_CLUSTER_TOKEN=…` before invoking deploy — matches
	// how ResolveCredential is used elsewhere in ov. The credential
	// store is the preferred source (audit trail), but a plain env var
	// override is honored for CI scripts, one-shot demos, and Task #12
	// verification without forcing kdbx setup.
	val, _ := ResolveCredential(dep.Name, service, key, "")
	return val
}

// ResolveSecretsForLayers is the batch variant used when multiple layers in
// a single deploy share secret_requires — their resolution results merge
// into one env map, with layer-order precedence (later layers win on
// duplicate names, matching the existing generate.go `secretRequiresMap`
// semantics in the label-emission path).
func ResolveSecretsForLayers(layers []*Layer) (env map[string]string, missingRequired []string) {
	env = map[string]string{}
	seenMissing := map[string]bool{}
	for _, l := range layers {
		perLayer, missing := ResolveLayerSecrets(l)
		for k, v := range perLayer {
			env[k] = v
		}
		for _, name := range missing {
			if !seenMissing[name] {
				seenMissing[name] = true
				missingRequired = append(missingRequired, name)
			}
		}
	}
	return env, missingRequired
}

// FormatMissingSecretsError produces a human-readable error message naming
// every missing required secret, with the remediation hint pointing at
// `ov secrets set`. Used by callers that bail out when
// ResolveLayerSecrets returns a non-empty missingRequired list.
func FormatMissingSecretsError(missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	var hints []string
	for _, name := range missing {
		hints = append(hints, fmt.Sprintf("  ov secrets set ov/secret/%s <value>", name))
	}
	return fmt.Errorf(
		"missing required secret(s): %s\n\nResolve each by storing in the credential store:\n%s",
		strings.Join(missing, ", "), strings.Join(hints, "\n"),
	)
}

// LayersForPlans reloads the layer map and returns the ordered *Layer
// slice covered by the given plans (both LayersIncluded for image-level
// plans and per-plan Layer for layer-only plans). Used by deploy-add to
// call ResolveSecretsForLayers + RetrieveLayerArtifacts.
func LayersForPlans(plans []*InstallPlan, dir string, cfg *Config) ([]*Layer, error) {
	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ordered []*Layer
	pick := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		if l, ok := layers[name]; ok {
			ordered = append(ordered, l)
		}
	}
	for _, p := range plans {
		for _, name := range p.LayersIncluded {
			pick(name)
		}
		pick(p.Layer)
	}
	return ordered, nil
}

// InjectSecretsIntoPlans merges the resolved secret env map into every
// TaskStep's task.Env across the supplied plans. Existing task.Env keys
// are preserved (layer-declared env takes precedence over a credential-
// store collision — a deliberate choice so an author can explicitly pin
// a value they control). Called from deploy_add_cmd after
// ResolveLayerSecrets and before target.Emit so the heredoc renderer
// sees the values as regular env exports.
func InjectSecretsIntoPlans(plans []*InstallPlan, env map[string]string) {
	if len(env) == 0 {
		return
	}
	for _, p := range plans {
		for _, step := range p.Steps {
			ts, ok := step.(*TaskStep)
			if !ok || ts.Task == nil {
				continue
			}
			if ts.Task.Env == nil {
				ts.Task.Env = map[string]string{}
			}
			for k, v := range env {
				if _, alreadySet := ts.Task.Env[k]; alreadySet {
					continue
				}
				ts.Task.Env[k] = v
			}
		}
	}
}
