package main

// podTarget is the `pod` deploy-target IR provider (the default substrate), extracted
// into its OWN file as the externalizable dedicated-provider pattern (Phase 3). A deploy
// target derives its word from cross-refs (`target:` is not user-authored), so it carries
// NO authored plugin_input and NO CUE schema — it therefore does not fit the
// schema-carrying RegisterBuiltinPluginUnit path (registerPluginUnitSchema rejects an
// empty schema). Instead it self-registers via registerDedicatedBuiltin below, and is
// INTENTIONALLY absent from both the shared builtinProviderInstances slice and the
// `providers:` manifest, yet dispatches identically through providerRegistry.ResolveDeploy.
// Its UnifiedDeployTarget construction is unchanged (behavior-preserving).
type podTarget struct{ builtinDeployBase }

func (podTarget) Reserved() string { return "pod" }
func (podTarget) ResolveTarget(node *BundleNode, name string) (UnifiedDeployTarget, error) {
	// BaseImageRef is the image the rebuild's build/check steps target; node.Image is
	// the charly.yml `box:` field (Rebuild falls back to NodeName when empty).
	return &PodUnifiedTarget{NodeName: name, BaseImageRef: node.Image}, nil
}

// Self-register at package-var init (runs before any init(), so the per-class deploy
// bijection gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(podTarget{})
