package main

import (
	"context"
)

// localPkgInstallStepProvider is the `LocalPkgInstall` InstallStep IR provider, extracted
// into its OWN file as the externalizable dedicated-provider pattern (Phase 3). An
// InstallStep is pure IR (never a user-authored input), so it is schema-less and does not
// fit the schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving).
type localPkgInstallStepProvider struct{ builtinStepBase }

func (localPkgInstallStepProvider) Reserved() string { return string(StepKindLocalPkgInstall) }
func (localPkgInstallStepProvider) EmitOCI(t *OCITarget, step InstallStep, _ *InstallPlan) error {
	return t.emitLocalPkgInstall(step.(*LocalPkgInstallStep))
}
func (localPkgInstallStepProvider) EmitVM(t *VmDeployTarget, ctx context.Context, step InstallStep, _ *InstallPlan, opts EmitOpts, _ *CandyRecord) error {
	s := step.(*LocalPkgInstallStep)
	return execLocalPkgInstall(ctx, t.Exec, s, venueHasPkgManager(ctx, t.Exec, s.LocalPkg, opts), "vm:"+t.VMName, opts)
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(localPkgInstallStepProvider{})
