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

// TestDedicatedProviders_BulkResolveAndAbsent proves the Phase 3 BULK extraction: every
// remaining deploy-target (pod/vm/k8s/android) and builder (pixi/npm/aur) now lives in
// its OWN dedicated plugin_<class>_<name>.go file, self-registers via
// registerDedicatedBuiltin, and is INTENTIONALLY absent from both
// builtinProviderInstances and the `providers:` manifest — yet still resolves through
// the SAME providerRegistry and dispatches identically (the deploy-target bijection gate
// still sees them registered; builders have no gate, so the resolve IS the wiring proof).
func TestDedicatedProviders_BulkResolveAndAbsent(t *testing.T) {
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	inManifest := func(class ProviderClass, word string) bool {
		for _, w := range manifest[string(class)] {
			if w == word {
				return true
			}
		}
		return false
	}

	// Deploy targets: each resolves to a DeployTargetProvider that constructs the
	// expected UnifiedDeployTarget (behavior-preserving), and is absent from slice+manifest.
	wantTarget := map[string]func(UnifiedDeployTarget) bool{
		"pod":     func(t UnifiedDeployTarget) bool { _, ok := t.(*PodUnifiedTarget); return ok },
		"vm":      func(t UnifiedDeployTarget) bool { _, ok := t.(*VmUnifiedTarget); return ok },
		"k8s":     func(t UnifiedDeployTarget) bool { _, ok := t.(*K8sUnifiedTarget); return ok },
		"android": func(t UnifiedDeployTarget) bool { _, ok := t.(*AndroidUnifiedTarget); return ok },
	}
	for _, word := range []string{"pod", "vm", "k8s", "android"} {
		dp, ok := providerRegistry.ResolveDeploy(word)
		if !ok {
			t.Fatalf("ResolveDeploy(%q) not registered — dedicated self-registration regressed", word)
		}
		dtp, ok := dp.(DeployTargetProvider)
		if !ok {
			t.Fatalf("deploy:%s resolved but is not a DeployTargetProvider (got %T)", word, dp)
		}
		tgt, err := dtp.ResolveTarget(&BundleNode{}, "demo")
		if err != nil {
			t.Fatalf("deploy:%s ResolveTarget: %v", word, err)
		}
		if !wantTarget[word](tgt) {
			t.Fatalf("deploy:%s ResolveTarget = %T, unexpected UnifiedDeployTarget type", word, tgt)
		}
		if _, in := byKey[provKey(ClassDeployTarget, word)]; in {
			t.Fatalf("deploy:%s is still in builtinProviderInstances — must self-register from its dedicated file", word)
		}
		if inManifest(ClassDeployTarget, word) {
			t.Fatalf("deploy:%s is still in the providers: manifest — a dedicated provider must not be listed there", word)
		}
	}

	// Builders: each resolves to a BuilderProvider and is absent from slice+manifest.
	for _, word := range []string{"pixi", "npm", "aur"} {
		bp, ok := builderProviderFor(word)
		if !ok {
			t.Fatalf("builderProviderFor(%q) not resolved — dedicated self-registration regressed", word)
		}
		if bp.Reserved() != word {
			t.Fatalf("builder:%s Reserved() = %q, want %q", word, bp.Reserved(), word)
		}
		if bp.Class() != ClassBuilder {
			t.Fatalf("builder:%s Class() = %q, want %q", word, bp.Class(), ClassBuilder)
		}
		if _, in := byKey[provKey(ClassBuilder, word)]; in {
			t.Fatalf("builder:%s is still in builtinProviderInstances — must self-register from its dedicated file", word)
		}
		if inManifest(ClassBuilder, word) {
			t.Fatalf("builder:%s is still in the providers: manifest — a dedicated provider must not be listed there", word)
		}
	}

	// aur additionally carries the optional BuilderStager half (host staging dir) — all
	// of its methods moved with it.
	st, ok := builderStagerFor("aur")
	if !ok {
		t.Fatal("builderStagerFor(\"aur\") not resolved — aur must still implement BuilderStager")
	}
	if st.StagingMount() != "/tmp/aur-pkgs" {
		t.Fatalf("aur StagingMount() = %q, want %q", st.StagingMount(), "/tmp/aur-pkgs")
	}
}
