package main

// The built-in builders as BuilderProviders. Each carries its UNCHANGED reverse
// (teardown) + stage-context logic — the migration is behavior-preserving; only
// the BuilderStep.Reverse + collectBuilderContext switches are replaced by
// providerRegistry.ResolveBuilder. renderBuilderScript stays data-driven.

// Every built-in builder now lives in its OWN dedicated file as the externalizable
// dedicated-provider pattern (Phase 3); each self-registers via
// registerDedicatedBuiltin and is therefore absent from both the
// builtinProviderInstances slice and the `providers:` manifest:

// aurBuilder (the `aur` builder) lives in plugin_builder_aur.go.

// pixiBuilder (the `pixi` builder) lives in plugin_builder_pixi.go.

// cargoBuilder (the `cargo` builder) lives in plugin_builder_cargo.go.

// npmBuilder (the `npm` builder) lives in plugin_builder_npm.go.
