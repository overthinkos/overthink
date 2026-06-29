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

// android and k8s have NO in-proc deploy target — they are EXTERNAL deploy
// substrates (F1), served out-of-process by candy/plugin-adb (deploy:android) /
// candy/plugin-kube (deploy:k8s). ResolveTarget routes `target: android` /
// `target: k8s` to externalDeployTarget; the host-side substrate preresolution
// (device-endpoint + apk specs; cluster template + Capabilities → the generated
// Kustomize tree) lives in android_deploy_preresolve.go / k8s_deploy_preresolve.go.
