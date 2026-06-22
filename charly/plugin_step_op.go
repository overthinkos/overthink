package main

import (
	"context"
	"time"
)

// opStepProvider is the `Op` (task) InstallStep IR provider, extracted into its OWN file
// as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is pure IR
// (never a user-authored input), so it is schema-less and does not fit the schema-carrying
// RegisterBuiltinPluginUnit path; it self-registers via registerDedicatedBuiltin below and
// is INTENTIONALLY absent from both the builtinProviderInstances slice and the `providers:`
// manifest, yet dispatches identically through providerRegistry.ResolveStep. Each Emit*
// preserves its venue's EXACT prior behaviour (behavior-preserving): the VM venue collects
// ReverseOps inline.
type opStepProvider struct{ builtinStepBase }

func (opStepProvider) Reserved() string { return string(StepKindOp) }
func (opStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitOp(step.(*OpStep))
}
func (opStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execOp(step.(*OpStep), plan, opts, rec, start)
}
func (opStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*OpStep)
	if err := t.execOp(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(opStepProvider{})
