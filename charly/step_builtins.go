package main

// The built-in InstallStep kinds' in-proc providers. The ONE remaining in-proc venue is EmitOCI
// (the pod-overlay add_candy: Containerfile synthesis). Both target:local AND target:vm
// externalized (into candy/plugin-deploy-local / candy/plugin-deploy-vm), so the in-proc
// per-deploy-step walk (the former EmitLocal/EmitVM) is gone — the deploy-venue behaviour (gates +
// ReverseOp collection) now lives in the out-of-process kit.WalkPlans.

// The HOST-COUPLED / host-engine step providers each live in their OWN dedicated file as the
// externalizable dedicated-provider pattern; each self-registers via registerDedicatedBuiltin and is
// therefore absent from both the builtinProviderInstances slice and the `providers:` manifest, yet
// dispatches identically through providerRegistry.ResolveStep:

// builderStepProvider (StepKindBuilder) lives in plugin_step_builder.go.

// opStepProvider (StepKindOp) lives in plugin_step_op.go.

// localPkgInstallStepProvider (StepKindLocalPkgInstall) lives in plugin_step_local_pkg_install.go.

// rebootStepProvider (StepKindReboot) lives in plugin_step_reboot.go.

// externalPluginStepProvider (StepKindExternalPlugin) lives in plugin_step_external.go.

// The plugin-served build-emit kinds have NO in-proc StepProvider: their BUILD-emit externalized to
// the compiled-in class:step plugin candy/plugin-installstep (served over OpEmit). OCITarget.emitStep
// routes them by pluginEmitStepWords (provider_step.go); their DEPLOY leg is unchanged
// (charly/plugin/kit.WalkPlans renders them from the step view). Two sub-categories:
//   - The seven PURE kinds (C1.1) — File, ShellHook, ShellSnippet, ServicePackaged, ServiceCustom,
//     RepoChange, ApkInstall — whose fragment the plugin formats directly from the step VIEW.
//     apk-install declares Emits=false (no build fragment — the android deploy preresolver reads it).
//   - The HOST-COUPLED SystemPackages kind (C1.2) — its OpEmit calls back the host's "step-emit"
//     host-builder for the DistroDef-format-template render (step_emit_hostbuild.go), which cannot
//     cross the process boundary. Its DEPLOY leg (host-engine, RunHostStep → renderHostPackageCommand)
//     is likewise unchanged.
