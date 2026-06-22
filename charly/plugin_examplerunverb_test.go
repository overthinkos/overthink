package main

import (
	"context"
	"strings"
	"testing"
)

// TestRunPluginVerb_CheckVerbProviderKeepsRunner proves the dispatch fix: a builtin
// plugin unit whose provider is a CheckVerbProvider (examplerunverb) is dispatched
// IN-PROCESS via RunVerb — carrying the live *Runner — NOT through the out-of-proc
// Invoke envelope (whose builtinVerbBase stub errors "in-process only"). The pass
// message must contain the authored marker (proving plugin_input round-trips into
// RunVerb), which the Invoke path could never produce here.
func TestRunPluginVerb_CheckVerbProviderKeepsRunner(t *testing.T) {
	r := &Runner{Mode: RunModeBox}

	op := &Op{Plugin: "examplerunverb", PluginInput: map[string]any{"marker": "hi"}}
	res := r.runPluginVerb(context.Background(), op)
	if res.Status != TestPass {
		t.Fatalf("examplerunverb status=%v msg=%q, want pass (RunVerb ran with the *Runner, not the Invoke stub)", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "hi") {
		t.Fatalf("examplerunverb message=%q, want it to contain the authored marker %q (plugin_input round-trip via RunVerb)", res.Message, "hi")
	}
	// The message also echoes a fact read off the live *Runner (the run mode) — an
	// out-of-proc Invoke could never reach it, so its presence corroborates that the
	// CheckVerbProvider dispatch kept the executor rather than marshalling the Op.
	if !strings.Contains(res.Message, "mode=box") {
		t.Fatalf("examplerunverb message=%q, want it to echo the live runner's mode (proves RunVerb held the *Runner)", res.Message)
	}
}
