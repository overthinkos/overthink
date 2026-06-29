package main

import (
	"context"
)

// rebootStepProvider is the `Reboot` InstallStep IR provider — skipped on OCI/local,
// executed on VM. Extracted into its OWN file as the externalizable dedicated-provider
// pattern (Phase 3). An InstallStep is pure IR (never a user-authored input), so it is
// schema-less and does not fit the schema-carrying RegisterBuiltinPluginUnit path; it
// self-registers via registerDedicatedBuiltin below and is INTENTIONALLY absent from
// both the builtinProviderInstances slice and the `providers:` manifest, yet dispatches
// identically through providerRegistry.ResolveStep. Each Emit* preserves its venue's
// EXACT prior behaviour (behavior-preserving).
type rebootStepProvider struct{ builtinStepBase }

func (rebootStepProvider) Reserved() string { return string(StepKindReboot) }
func (rebootStepProvider) EmitOCI(_ *OCITarget, _ InstallStep, _ *InstallPlan) error {
	return nil // no machine to reboot during an image build
}
func (rebootStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, _ *InstallPlan, opts EmitOpts, _ *CandyRecord) error {
	return t.execReboot(ctx, step.(*RebootStep), opts)
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(rebootStepProvider{})
