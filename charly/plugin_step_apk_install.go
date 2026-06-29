package main

// apkInstallStepProvider is the `ApkInstall` InstallStep IR provider, extracted into its
// OWN file as the externalizable dedicated-provider pattern (Phase 3). An InstallStep is
// pure IR (never a user-authored input), so it is schema-less and does not fit the
// schema-carrying RegisterBuiltinPluginUnit path; it self-registers via
// registerDedicatedBuiltin below and is INTENTIONALLY absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches identically
// through providerRegistry.ResolveStep. NO DeployTarget executes an ApkInstallStep: the
// android substrate is EXTERNAL (F1), so its host-side preresolver (collectAndroidInstalls,
// android_deploy_preresolve.go) READS this step to collect the apk install specs and ships
// them to the deploy:android plugin (candy/plugin-adb), which drives the device. Every Emit*
// venue here therefore records a skip — the step is provenance the preresolver consumes,
// never executed in-line by a DeployTarget.
type apkInstallStepProvider struct{ builtinStepBase }

func (apkInstallStepProvider) Reserved() string { return string(StepKindApkInstall) }
func (apkInstallStepProvider) EmitOCI(_ *OCITarget, _ InstallStep, _ *InstallPlan) error {
	// No device at image-build time; the android deploy preresolver reads this step
	// host-side at deploy and the deploy:android plugin installs the apps.
	return nil
}

// Self-register at package-var init (before any init(), so the per-class step bijection
// gate in registry_bootstrap.go observes it without a cross-init race).
var _ = registerDedicatedBuiltin(apkInstallStepProvider{})
