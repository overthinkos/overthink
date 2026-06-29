package main

// shellHookStepProvider is the `ShellHook` InstallStep IR provider, extracted into its OWN
// file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is pure
// IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Its sole in-proc venue is now EmitOCI (the
// pod-overlay add_candy: Containerfile synthesis); target:vm externalized into
// candy/plugin-deploy-vm, so the deploy-venue behaviour (gates + ReverseOp collection) now
// lives in the out-of-process kit.WalkPlans.
type shellHookStepProvider struct{ builtinStepBase }

func (shellHookStepProvider) Reserved() string { return string(StepKindShellHook) }
func (shellHookStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitShellHook(step.(*ShellHookStep))
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(shellHookStepProvider{})
