package main

import "testing"

// TestDedicatedProviders_ResolveAndDispatch proves the externalizable
// dedicated-provider pattern: a schema-less IR provider extracted into its own file —
// step:Reboot (plugin_step_reboot.go) — still registers into the SAME providerRegistry and
// dispatches identically, even though it is INTENTIONALLY absent from both
// builtinProviderInstances and the `providers:` manifest (it self-registers from a package-var
// initializer via registerDedicatedBuiltin). The test fails if the dedicated registration
// regresses (provider missing) or if the typed dispatch adapter is lost. (deploy:local was once
// such a dedicated builtin; it has since externalized into candy/plugin-deploy-local — NO in-proc
// provider, asserted by TestReservedWordRegistry_DeployBijection. The four builders
// (cargo/npm/pixi/aur) likewise externalized — NO in-proc provider, asserted by
// TestExternalizedBuilders_NoInProcProvider below.)
func TestDedicatedProviders_ResolveAndDispatch(t *testing.T) {
	// step:Reboot — resolves to a StepProvider (the per-venue Emit* dispatch).
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

	// The dedicated provider is intentionally ABSENT from the manifest-driven instance supply
	// — the defining property of the externalizable pattern.
	byKey := builtinInstanceMap()
	if _, in := byKey[provKey(ClassStep, string(StepKindReboot))]; in {
		t.Fatalf("step:Reboot is still in builtinProviderInstances — must self-register from its dedicated file instead")
	}
	manifest := parseEmbeddedProviderManifest()
	for _, w := range manifest[string(ClassStep)] {
		if w == string(StepKindReboot) {
			t.Fatalf("step:Reboot is still in the providers: manifest — a dedicated provider must not be listed there")
		}
	}
}

// TestExternalizedBuilders_NoInProcProvider proves the four detection-builders (cargo/npm/pixi/aur)
// are EXTERNAL out-of-process plugin candies with NO compiled-in BuilderProvider — the builder
// analogue of TestReservedWordRegistry_DeployBijection. At process start (before any plugin
// connects at load time) the registry resolves NONE of them, and each is recorded in the
// externalizedBuilders set with a serving plugin candy in externalBuilderPlugins. A regression
// that re-introduced an in-proc builtin builder would resolve here and fail.
func TestExternalizedBuilders_NoInProcProvider(t *testing.T) {
	byKey := builtinInstanceMap()
	manifest := parseEmbeddedProviderManifest()
	for _, word := range []string{"cargo", "npm", "pixi", "aur"} {
		if !externalizedBuilders[word] {
			t.Fatalf("builder %q must be in externalizedBuilders (single source of truth)", word)
		}
		if _, ok := externalBuilderPlugins[word]; !ok {
			t.Fatalf("builder %q must name its serving plugin candy in externalBuilderPlugins", word)
		}
		if _, ok := providerRegistry.resolve(ClassBuilder, word); ok {
			t.Fatalf("builder %q resolves to an in-proc provider at process start — it must be external (connected only at plugin-load time)", word)
		}
		if _, in := byKey[provKey(ClassBuilder, word)]; in {
			t.Fatalf("builder %q is in builtinProviderInstances — an externalized builder has no compiled-in provider", word)
		}
		for _, w := range manifest[string(ClassBuilder)] {
			if w == word {
				t.Fatalf("builder %q is in the providers: manifest — an externalized builder has no compiled-in provider", word)
			}
		}
		if ref, ok := externalBuilderPluginRef(word); !ok || ref == "" {
			t.Fatalf("builder %q must produce a canonical plugin ref for submodule auto-inject", word)
		}
	}
}

// TestDedicatedProviders_BulkStepResolveAndAbsent proves how every InstallStep kind is SERVED
// after the C1.1 build-emit externalization. The HOST-COUPLED / host-engine kinds (everything
// NOT in pluginEmitStepWords) each live in their OWN dedicated plugin_step_<name>.go file,
// self-register via registerDedicatedBuiltin, and are absent from both builtinProviderInstances
// and the `providers:` manifest — yet resolve through the SAME providerRegistry as a typed
// StepProvider. The seven PURE kinds in pluginEmitStepWords have NO in-proc StepProvider: their
// build-emit is served by the compiled-in class:step plugin candy/plugin-installstep, resolved by
// its lowercase word with a declared StepContract. The step bijection gate in init() checks the
// same split (a missing provider panics at startup). The test fails if any registration regresses.
func TestDedicatedProviders_BulkStepResolveAndAbsent(t *testing.T) {
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

	for _, kind := range allStepKinds {
		// The seven PURE kinds' build-emit externalized to candy/plugin-installstep: NO in-proc
		// StepProvider; served by a compiled-in class:step plugin (lowercase word, StepContract).
		if word, isPlugin := pluginEmitStepWords[kind]; isPlugin {
			if _, ok := stepProviderFor(kind); ok {
				t.Fatalf("step:%s still has an in-proc StepProvider — its build-emit externalized to candy/plugin-installstep", kind)
			}
			prov, ok := providerRegistry.resolve(ClassStep, word)
			if !ok {
				t.Fatalf("step:%s externalized build-emit word %q not registered (candy/plugin-installstep not compiled in?)", kind, word)
			}
			if _, ok := prov.(stepContractCarrier); !ok {
				t.Fatalf("step:%s class:step provider %q declares no StepContract", kind, word)
			}
			continue
		}
		// Every other kind resolves to a dedicated in-proc StepProvider, absent from the
		// manifest-driven instance supply.
		sp, ok := stepProviderFor(kind)
		if !ok {
			t.Fatalf("stepProviderFor(%q) not resolved — dedicated self-registration regressed", kind)
		}
		if sp.Reserved() != string(kind) {
			t.Fatalf("step:%s Reserved() = %q, want %q", kind, sp.Reserved(), kind)
		}
		if sp.Class() != ClassStep {
			t.Fatalf("step:%s Class() = %q, want %q", kind, sp.Class(), ClassStep)
		}
		if _, in := byKey[provKey(ClassStep, string(kind))]; in {
			t.Fatalf("step:%s is still in builtinProviderInstances — must self-register from its dedicated file", kind)
		}
		if inManifest(ClassStep, string(kind)) {
			t.Fatalf("step:%s is still in the providers: manifest — a dedicated provider must not be listed there", kind)
		}
	}

	// The `providers:` manifest carries NO step entries at all — the in-proc step providers are
	// dedicated builtins and the seven externalized kinds are compiled-in plugin candies.
	if len(manifest[string(ClassStep)]) != 0 {
		t.Fatalf("providers: manifest step list = %v, want empty (no manifest-driven step instances)", manifest[string(ClassStep)])
	}
}
