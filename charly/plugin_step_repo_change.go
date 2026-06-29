package main

// repoChangeStepProvider is the `RepoChange` InstallStep IR provider, extracted into its
// OWN file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is
// pure IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Its sole in-proc venue is now EmitOCI (the
// pod-overlay add_candy: Containerfile synthesis); target:vm externalized into
// candy/plugin-deploy-vm, so the deploy-venue behaviour (gates + ReverseOp collection) now
// lives in the out-of-process kit.WalkPlans.
type repoChangeStepProvider struct{ builtinStepBase }

func (repoChangeStepProvider) Reserved() string { return string(StepKindRepoChange) }
func (repoChangeStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitRepoChange(step.(*RepoChangeStep))
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(repoChangeStepProvider{})
