package main

import (
	"context"
	"testing"
)

// TestRelocatedHTTPVerb_DispatchesViaKit proves the `http` check verb — relocated to
// candy/plugin-http (a compiled-in kit candy) — dispatches through the providerRegistry
// as a CheckVerbProvider (the kitVerbAdapter passing the live *Runner as a
// kit.CheckContext) and runs its relocated request logic. Exercises the deterministic
// ModeBox curl path (fakeExecutor): curl reports the status code, an optional body read
// follows; a status match passes, a mismatch fails.
func TestRelocatedHTTPVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("http")
	if !ok {
		t.Fatal("http verb not registered — compiled-in kit candy (candy/plugin-http) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("http provider is not a CheckVerbProvider: %T", prov)
	}

	// ModeBox: curl writes "200"; status:200 expected → pass.
	feOK := &fakeExecutor{responses: []fakeResponse{
		{matchPrefix: "curl", stdout: "200", exit: 0},
		{matchPrefix: "cat /tmp/.charly-test-body", stdout: "hello", exit: 0},
	}}
	res := cv.RunVerb(context.Background(), &Runner{Exec: feOK, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"http": "http://svc/", "status": 200}})
	if res.Status != TestPass {
		t.Fatalf("curl 200 + status:200: want pass, got %v: %s", res.Status, res.Message)
	}

	// ModeBox: curl writes "503"; status:200 expected → fail.
	feBad := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "curl", stdout: "503", exit: 0}}}
	res2 := cv.RunVerb(context.Background(), &Runner{Exec: feBad, Mode: RunModeBox},
		&Op{PluginInput: map[string]any{"http": "http://svc/", "status": 200}})
	if res2.Status != TestFail {
		t.Fatalf("curl 503 + status:200: want fail, got %v: %s", res2.Status, res2.Message)
	}
}
