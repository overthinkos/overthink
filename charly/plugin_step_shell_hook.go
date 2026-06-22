package main

import (
	"context"
	"time"
)

// shellHookStepProvider is the `ShellHook` InstallStep IR provider, extracted into its OWN
// file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is pure
// IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving): the VM venue collects ReverseOps inline.
type shellHookStepProvider struct{ builtinStepBase }

func (shellHookStepProvider) Reserved() string { return string(StepKindShellHook) }
func (shellHookStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitShellHook(step.(*ShellHookStep))
}
func (shellHookStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord, start time.Time) error {
	return t.execShellHook(step.(*ShellHookStep), plan, opts, rec, start)
}
func (shellHookStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, plan *InstallPlan, opts EmitOpts, rec *CandyRecord) error {
	s := step.(*ShellHookStep)
	if err := t.execShellHook(ctx, s, plan, opts); err != nil {
		return err
	}
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(shellHookStepProvider{})
