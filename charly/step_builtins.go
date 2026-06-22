package main

// The built-in InstallStep kinds as StepProviders. Each Emit* method preserves
// its venue's EXACT prior switch-case behaviour (the per-target emitX/execX
// handlers are unchanged); only the three type-switches + a dead never-wired
// step-walker are gone (C4). The VM venue collects ReverseOps + applies gate checks
// inline, so each EmitVM mirrors its case (which steps append s.Reverse(), which gate,
// which skip).

// Every built-in InstallStep provider now lives in its OWN dedicated file as the
// externalizable dedicated-provider pattern (Phase 3); each self-registers via
// registerDedicatedBuiltin and is therefore absent from both the
// builtinProviderInstances slice and the `providers:` manifest, yet dispatches
// identically through providerRegistry.ResolveStep:

// systemPackagesStepProvider (StepKindSystemPackages) lives in plugin_step_system_packages.go.

// builderStepProvider (StepKindBuilder) lives in plugin_step_builder.go.

// opStepProvider (StepKindOp) lives in plugin_step_op.go.

// fileStepProvider (StepKindFile) lives in plugin_step_file.go.

// servicePackagedStepProvider (StepKindServicePackaged) lives in plugin_step_service_packaged.go.

// serviceCustomStepProvider (StepKindServiceCustom) lives in plugin_step_service_custom.go.

// shellHookStepProvider (StepKindShellHook) lives in plugin_step_shell_hook.go.

// shellSnippetStepProvider (StepKindShellSnippet) lives in plugin_step_shell_snippet.go.

// repoChangeStepProvider (StepKindRepoChange) lives in plugin_step_repo_change.go.

// apkInstallStepProvider (StepKindApkInstall) lives in plugin_step_apk_install.go.

// localPkgInstallStepProvider (StepKindLocalPkgInstall) lives in plugin_step_local_pkg_install.go.

// rebootStepProvider (StepKindReboot) lives in plugin_step_reboot.go.
