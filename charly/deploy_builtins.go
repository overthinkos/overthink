package main

// The built-in deploy targets as DeployTargetProviders. Each constructs its
// UnifiedDeployTarget unchanged — the migration is behavior-preserving; the
// ResolveTarget dispatch switch is replaced by providerRegistry.ResolveDeploy.

// Every built-in deploy target now lives in its OWN dedicated file as the
// externalizable dedicated-provider pattern (Phase 3); each self-registers via
// registerDedicatedBuiltin and is therefore absent from both the
// builtinProviderInstances slice and the `providers:` manifest:

// localTarget (the `local` deploy target) lives in plugin_deploy_local.go.

// vmTarget (the `vm` deploy target) lives in plugin_deploy_vm.go.

// podTarget (the `pod` deploy target) lives in plugin_deploy_pod.go.

// k8sTarget (the `k8s` deploy target) lives in plugin_deploy_k8s.go.

// android has NO in-proc deploy target — it is an EXTERNAL deploy substrate (F1),
// served out-of-process by candy/plugin-adb (deploy:android). ResolveTarget routes
// `target: android` to externalDeployTarget; the host-side device-endpoint resolution
// + apk-spec collection live in android_deploy_preresolve.go.
