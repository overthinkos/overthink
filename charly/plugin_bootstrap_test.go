package main

import (
	"context"
	"strings"
	"testing"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
)

// TestBootstrapPhase_ExampleEnumeratedAndNoOp proves F9 (phase machinery + bootstrap set): the
// compiled-in candy/plugin-example-bootstrap declares Phase=="bootstrap", so providersInPhase
// enumerates it, and runBootstrapPhase invokes its OpBootstrap returning the config UNCHANGED (the
// no-op). The phase flag travels over Describe (buildUnitInProc) onto the inprocProvider.
func TestBootstrapPhase_ExampleEnumeratedAndNoOp(t *testing.T) {
	found := false
	for _, p := range providerRegistry.providersInPhase(sdk.PhaseBootstrap) {
		if p.Reserved() == "examplebootstrap" {
			found = true
		}
	}
	if !found {
		t.Fatal("compiled-in examplebootstrap not enumerated in PhaseBootstrap (phase flag / pluginsgen / compiled_plugins)")
	}
	in := []byte("version: " + LatestSchemaVersion().String() + "\nfoo: bar\n")
	out := runBootstrapPhase(in)
	if string(out) != string(in) {
		t.Fatalf("no-op bootstrap mutated the config: %q -> %q", in, out)
	}
}

// zzFakeBootstrapProvider is a test bootstrap provider that TRANSFORMS the config — proving
// runBootstrapPhase APPLIES a bootstrap plugin's returned bytes (the migrate M15 path), not just
// the no-op. Unique word so it never collides with a real provider.
type zzFakeBootstrapProvider struct{}

func (zzFakeBootstrapProvider) Reserved() string         { return "zzfakebootstrap" }
func (zzFakeBootstrapProvider) Class() ProviderClass     { return ClassVerb }
func (zzFakeBootstrapProvider) pluginPhase() string      { return sdk.PhaseBootstrap }
func (zzFakeBootstrapProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	out, err := marshalJSON(map[string]string{"config": "TRANSFORMED-BY-BOOTSTRAP"})
	if err != nil {
		return nil, err
	}
	return &Result{JSON: out}, nil
}

// TestBootstrapPhase_TransformApplied proves the bootstrap pre-pass APPLIES a bootstrap plugin's
// returned (transformed) bytes — the migrate enabler. Uses runBootstrapPhaseWith with a fixed
// provider list (NOT the global registry), so the transforming fake cannot pollute the hot-path
// runBootstrapPhase that every other test's LoadUnified runs.
func TestBootstrapPhase_TransformApplied(t *testing.T) {
	if phaseOfProvider(zzFakeBootstrapProvider{}) != sdk.PhaseBootstrap {
		t.Fatal("fake bootstrap provider not classified PhaseBootstrap")
	}
	out := runBootstrapPhaseWith([]byte("version: 1\n"), []Provider{zzFakeBootstrapProvider{}})
	if !strings.Contains(string(out), "TRANSFORMED-BY-BOOTSTRAP") {
		t.Fatalf("runBootstrapPhaseWith did not apply the bootstrap transform: %q", out)
	}
}
