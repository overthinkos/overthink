package main

import (
	"context"
)

// serviceCustomStepProvider is the `ServiceCustom` InstallStep IR provider, extracted into
// its OWN file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep
// is pure IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving): the VM venue gates on opts.WithServices, then collects
// ReverseOps inline.
type serviceCustomStepProvider struct{ builtinStepBase }

func (serviceCustomStepProvider) Reserved() string { return string(StepKindServiceCustom) }
func (serviceCustomStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitServiceCustom(step.(*ServiceCustomStep))
}
func (serviceCustomStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	if !opts.WithServices {
		return nil
	}
	s := step.(*ServiceCustomStep)
	if err := t.execServiceCustom(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(serviceCustomStepProvider{})
