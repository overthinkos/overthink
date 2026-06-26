package main

import (
	"strings"
	"testing"
)

// TestReadinessPluginEnv_RoundTrips proves FU-7: the host's RESOLVED readiness, emitted
// as CHARLY_READINESS_* env (readinessPluginEnv) and re-read through readinessResolve (the
// SAME path the out-of-process vm plugin runs), yields the identical bounds. That round-trip
// is how the plugin — which cannot LoadUnified to see the project's defaults.readiness —
// inherits the host-resolved readiness via its spawn env.
func TestReadinessPluginEnv_RoundTrips(t *testing.T) {
	want := loadedReadiness()

	env := readinessPluginEnv()
	if len(env) != len(readinessSpecs) {
		t.Fatalf("readinessPluginEnv: emitted %d entries, want %d (one per readiness field)", len(env), len(readinessSpecs))
	}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, "CHARLY_READINESS_") {
			t.Fatalf("malformed readiness env entry %q", kv)
		}
		t.Setenv(k, v)
	}

	// nil config → readinessResolve draws purely from the CHARLY_READINESS_* env we just set,
	// exactly as the plugin's readinessResolve(nil) does in its spawned process.
	got, err := readinessResolve(nil)
	if err != nil {
		t.Fatalf("re-resolve from emitted env: %v", err)
	}
	if got != want {
		t.Fatalf("readiness did not round-trip through CHARLY_READINESS_* env:\n want %+v\n got  %+v", want, got)
	}
}
