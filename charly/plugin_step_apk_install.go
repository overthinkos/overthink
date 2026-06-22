package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// apkInstallStepProvider is the `ApkInstall` InstallStep IR provider, extracted into its
// OWN file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is
// pure IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. Each Emit* preserves its venue's EXACT prior
// behaviour (behavior-preserving): only a kind:android device executes it, so every venue
// here records a skip.
type apkInstallStepProvider struct{ builtinStepBase }

func (apkInstallStepProvider) Reserved() string { return string(StepKindApkInstall) }
func (apkInstallStepProvider) EmitOCI(_ *OCITarget, _ InstallStep, _ *InstallPlan) error {
	// No device at image-build time; the deploy-time AndroidDeployTarget runs it.
	return nil
}
func (apkInstallStepProvider) EmitLocal(t *LocalDeployTarget, step InstallStep, _ *InstallPlan, _ EmitOpts, rec *CandyRecord, start time.Time) error {
	s := step.(*ApkInstallStep)
	t.noteStep(rec, StepKindApkInstall, s.Scope(), VenueSkip,
		fmt.Sprintf("candy=%s skipped: apk installs only on a kind:android device", s.CandyName), start)
	return nil
}
func (apkInstallStepProvider) EmitVM(_ *VmDeployTarget, _ context.Context, step InstallStep, _ *InstallPlan, _ EmitOpts, _ *CandyRecord) error {
	s := step.(*ApkInstallStep)
	fmt.Fprintf(os.Stderr, "VmDeployTarget: skipping apk install (candy=%s) — apk installs only on a kind:android device\n", s.CandyName)
	return nil
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(apkInstallStepProvider{})
