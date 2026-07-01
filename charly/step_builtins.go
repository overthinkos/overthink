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

// rebootStepProvider (StepKindReboot) lives in plugin_step_reboot.go.

// externalPluginStepProvider (StepKindExternalPlugin) lives in plugin_step_external.go.

// The plugin-served build-emit kinds have NO in-proc StepProvider: their BUILD-emit externalized to
// the compiled-in class:step plugin candy/plugin-installstep (served over OpEmit). OCITarget.emitStep
// routes them by pluginEmitStepWords (provider_step.go); their DEPLOY leg is unchanged
// (charly/plugin/kit.WalkPlans renders them from the step view). Two sub-categories:
//   - The seven PURE kinds (C1.1) — File, ShellHook, ShellSnippet, ServicePackaged, ServiceCustom,
//     RepoChange, ApkInstall — whose fragment the plugin formats directly from the step VIEW.
//     apk-install declares Emits=false (no build fragment — the android deploy preresolver reads it).
//   - The HOST-COUPLED SystemPackages (C1.2) + Builder (C1.3) + LocalPkgInstall (C1.4) + Op (C1.5)
//     kinds — their OpEmit calls back the host's "step-emit" host-builder for a render they cannot do
//     across the process boundary (SystemPackages needs the DistroDef-format templates; Builder needs
//     the multi-stage buildStageContext + RenderTemplate engine; LocalPkgInstall needs the host
//     localpkg build engine renderLocalPkgImageInstall → buildLocalPkgOnHost + host-dir staging; Op
//     needs the RICHEST Generator.emitTasks per-verb render pipeline — COPY staging, op coalescing).
//     See step_emit_hostbuild.go (stepEmitSystemPackages, stepEmitBuilder, stepEmitLocalPkgInstall,
//     stepEmitOp). Their DEPLOY legs (SystemPackages/Builder/LocalPkgInstall host-engine via
//     RunHostStep → renderHostPackageCommand / runVenueBuilderStep / execLocalPkgInstall; Op the
//     act-OpStep resolveProvisionScript / renderOpCommand path) are likewise unchanged.
