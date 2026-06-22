package main

import "testing"

// TestDedicatedProviders_ResolveAndDispatch proves the externalizable
// dedicated-provider pattern (Phase 3): the three schema-less IR providers extracted
// into their own files — deploy:local (plugin_deploy_local.go), step:Reboot
// (plugin_step_reboot.go), builder:cargo (plugin_builder_cargo.go) — still register
// into the SAME providerRegistry and dispatch identically, even though each is
// INTENTIONALLY absent from both builtinProviderInstances and the `providers:`
// manifest (they self-register from a package-var initializer via
// registerDedicatedBuiltin). The test fails if the dedicated registration regresses
// (provider missing) or if the typed dispatch adapter is lost.
func TestDedicatedProviders_ResolveAndDispatch(t *testing.T) {
	// 1. deploy:local — resolves to a DeployTargetProvider whose ResolveTarget yields
	//    the LocalUnifiedTarget (the unchanged construction).
	dp, ok := providerRegistry.ResolveDeploy("local")
	if !ok {
		t.Fatal("ResolveDeploy(\"local\") not registered — dedicated self-registration regressed")
	}
	dtp, ok := dp.(DeployTargetProvider)
	if !ok {
		t.Fatalf("deploy:local resolved but is not a DeployTargetProvider (got %T)", dp)
	}
	tgt, err := dtp.ResolveTarget(&BundleNode{}, "demo")
	if err != nil {
		t.Fatalf("deploy:local ResolveTarget: %v", err)
	}
	lt, ok := tgt.(*LocalUnifiedTarget)
	if !ok {
		t.Fatalf("deploy:local ResolveTarget = %T, want *LocalUnifiedTarget", tgt)
	}
	if lt.NodeName != "demo" {
		t.Fatalf("deploy:local NodeName = %q, want %q", lt.NodeName, "demo")
	}

	// 2. step:Reboot — resolves to a StepProvider (the per-venue Emit* dispatch).
	sp, ok := stepProviderFor(StepKindReboot)
	if !ok {
		t.Fatal("stepProviderFor(StepKindReboot) not resolved — dedicated self-registration regressed")
	}
	if sp.Reserved() != string(StepKindReboot) {
		t.Fatalf("step provider Reserved() = %q, want %q", sp.Reserved(), StepKindReboot)
	}
	if sp.Class() != ClassStep {
		t.Fatalf("step:Reboot Class() = %q, want %q", sp.Class(), ClassStep)
	}
	// EmitOCI is a no-op (no machine to reboot at image-build) — exercises the
	// registry-dispatched method end-to-end.
	if err := sp.EmitOCI(nil, &RebootStep{CandyName: "k"}, nil); err != nil {
		t.Fatalf("step:Reboot EmitOCI: %v", err)
	}

	// 3. builder:cargo — resolves to a BuilderProvider (no bijection gate exists for
	//    builders, so this resolve IS the wiring proof).
	bp, ok := builderProviderFor("cargo")
	if !ok {
		t.Fatal("builderProviderFor(\"cargo\") not resolved — dedicated self-registration regressed")
	}
	if bp.Reserved() != "cargo" {
		t.Fatalf("builder provider Reserved() = %q, want %q", bp.Reserved(), "cargo")
	}
	if bp.Class() != ClassBuilder {
		t.Fatalf("builder:cargo Class() = %q, want %q", bp.Class(), ClassBuilder)
	}

	// 4. The dedicated providers are intentionally ABSENT from the manifest-driven
	//    instance supply — the defining property of the externalizable pattern.
	byKey := builtinInstanceMap()
	for _, k := range []string{
		provKey(ClassDeployTarget, "local"),
		provKey(ClassStep, string(StepKindReboot)),
		provKey(ClassBuilder, "cargo"),
	} {
		if _, in := byKey[k]; in {
			t.Fatalf("%s is still in builtinProviderInstances — must self-register from its dedicated file instead", k)
		}
	}
	manifest := parseEmbeddedProviderManifest()
	for class, word := range map[ProviderClass]string{
		ClassDeployTarget: "local",
		ClassStep:         string(StepKindReboot),
		ClassBuilder:      "cargo",
	} {
		for _, w := range manifest[string(class)] {
			if w == word {
				t.Fatalf("%s:%s is still in the providers: manifest — a dedicated provider must not be listed there", class, word)
			}
		}
	}
}
