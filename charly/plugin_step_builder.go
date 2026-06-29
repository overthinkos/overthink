package main

import (
	"context"
)

// builderStepProvider is the `Builder` InstallStep IR provider, extracted into its OWN
// file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is pure
// IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving): the VM venue collects no ReverseOp (matches the switch).
type builderStepProvider struct{ builtinStepBase }

func (builderStepProvider) Reserved() string { return string(StepKindBuilder) }
func (builderStepProvider) EmitOCI(t *OCITarget, step InstallStep, plan *InstallPlan) error {
	return t.emitBuilder(step.(*BuilderStep), plan)
}
func (builderStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, _ *CandyRecord) error {
	return t.execBuilder(ctx, step.(*BuilderStep), plan, opts) // no ReverseOp (matches the VM switch)
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(builderStepProvider{})
