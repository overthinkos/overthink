package main

// layer_secrets.go — resolver for the candy manifest `secret_requires:` and
// `secret_accepts:` when a layer is being applied via host / vm / ssh deploy
// targets (the non-container install-plan flow).
//
// Container targets have their own path (ProvisionPodmanSecrets, see
// secrets.go) that mounts secrets as podman secrets / env at container-run
// time. That path runs AFTER build and does not inject env into the layer's
// build-time tasks. For install-plan-based targets, the layer's tasks run
// directly on the deploy target, so the credential-store value must be
// resolved on the operator side and passed through as env on the step.
//
// Resolution policy (post 2026-05-06 cutover): `secret_requires:` entries
// auto-generate a 32-byte hex token via DefaultCredentialStore.Set when
// missing everywhere (env + store). `secret_accepts:` entries fall back to
// dep.Default when missing, never auto-generate. The auto-generation is
// race-free across multiple layers declaring the same secret because
// DefaultCredentialStore is cached via sync.Once and the first caller's
// Set is visible to the second caller's ResolveCredential.

import (
	"strings"
)

// ensureLayerSecret resolves a secret_requires/secret_accepts EnvDependency
// against the credential store. For required deps that miss everywhere
// (env, store), generates a 32-byte hex token, persists via
// DefaultCredentialStore, and returns the new value. For optional deps
// that miss, returns "" with source classification from ResolveCredential
// so the caller can fall back to dep.Default if set.
//
// The Key field on an EnvDependency follows the format "<service>/<key>"
// and must start with "charly/" (enforced by validate.go). When Key is empty,
// the default lookup is service="charly/secret", key=Name.
//
// Race-free across multiple layers declaring the same secret: the first
// caller's store.Set lands in the active backend (keyring/config
// fallback per credential_store.go DefaultCredentialStore); the second
// caller's ResolveCredential reads the persisted value. All callers in
// one process share the cached singleton.
func ensureLayerSecret(dep EnvDependency, required bool) (val, source string) {
	service, key := "charly/secret", dep.Name
	if dep.Key != "" {
		if idx := strings.LastIndex(dep.Key, "/"); idx > 0 {
			service = dep.Key[:idx]
			key = dep.Key[idx+1:]
		}
	}
	// Pass dep.Name as envVar so an operator can override the persisted
	// value via `export K3S_CLUSTER_TOKEN=…` before invoking deploy
	// (matches the ResolveCredential pattern used elsewhere).
	val, source = ResolveCredential(dep.Name, service, key, "")
	if val != "" {
		return val, source
	}
	if !required {
		return "", source
	}
	return generateAndStoreSecret(service, key)
}

// ResolveLayerSecret walks the layer's secret_requires + secret_accepts
// and resolves each via the credential store. Required entries that miss
// everywhere auto-generate a 32-byte hex token (see ensureLayerSecret).
// Optional `secret_accepts:` entries that miss fall back to dep.Default.
//
// Returns the env map; never returns an error. The auto-generate policy
// guarantees every `secret_requires:` resolves to a non-empty value.
func ResolveLayerSecret(layer *Layer) map[string]string {
	env := map[string]string{}
	if layer == nil {
		return env
	}

	if layer.HasSecretRequires() {
		for _, dep := range layer.SecretRequire() {
			val, _ := ensureLayerSecret(dep, true)
			env[dep.Name] = val
		}
	}

	if layer.HasSecretAccepts() {
		for _, dep := range layer.SecretAccept() {
			val, _ := ensureLayerSecret(dep, false)
			if val == "" && dep.Default != "" {
				env[dep.Name] = dep.Default
				continue
			}
			if val != "" {
				env[dep.Name] = val
			}
		}
	}

	return env
}

// ResolveSecretForLayer is the batch variant used when multiple layers in
// a single deploy share secret_requires — their resolution results merge
// into one env map, with layer-order precedence (later layers win on
// duplicate names, matching the existing generate.go `secretRequiresMap`
// semantics in the label-emission path).
func ResolveSecretForLayer(layers []*Layer) map[string]string {
	env := map[string]string{}
	for _, l := range layers {
		for k, v := range ResolveLayerSecret(l) {
			env[k] = v
		}
	}
	return env
}

// LayerForPlan reloads the layer map and returns the ordered *Layer
// slice covered by the given plans (both LayersIncluded for image-level
// plans and per-plan Layer for layer-only plans). Used by deploy-add to
// call ResolveSecretForLayer + RetrieveLayerArtifacts.
func LayerForPlan(plans []*InstallPlan, dir string, cfg *Config) ([]*Layer, error) {
	layers, err := ScanAllLayerWithConfig(dir, cfg)
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
// ResolveLayerSecret and before target.Emit so the heredoc renderer
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
