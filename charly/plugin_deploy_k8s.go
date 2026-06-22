package main

// k8sTarget is the `k8s` deploy-target IR provider, extracted into its OWN file as the
// externalizable dedicated-provider pattern (Phase 3). A deploy target derives its word
// from cross-refs (`target:` is not user-authored), so it carries NO authored
// plugin_input and NO CUE schema — it therefore does not fit the schema-carrying
// RegisterBuiltinPluginUnit path (registerPluginUnitSchema rejects an empty schema).
// Instead it self-registers via registerDedicatedBuiltin below, and is INTENTIONALLY
// absent from both the shared builtinProviderInstances slice and the `providers:`
// manifest, yet dispatches identically through providerRegistry.ResolveDeploy. Its
// UnifiedDeployTarget construction is unchanged (behavior-preserving).
type k8sTarget struct{ builtinDeployBase }

func (k8sTarget) Reserved() string { return "k8s" }
func (k8sTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &K8sUnifiedTarget{NodeName: name}, nil
}

// Self-register at package-var init (runs before any init(), so the per-class deploy
// bijection gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(k8sTarget{})
