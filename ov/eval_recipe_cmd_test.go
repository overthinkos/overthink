package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout to a buffer for the duration of fn,
// then restores. Used by tests that exercise printRecipeText/TAP since
// those functions write directly to stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// TestPrintRecipeTAP_HeaderAndPlan asserts the TAP v13 prologue.
func TestPrintRecipeTAP_HeaderAndPlan(t *testing.T) {
	res := &EvalRunResults{
		Scenario: []ScenarioEvalResult{
			{Name: "alpha", Origin: "pod:a", Status: "pass"},
			{Name: "beta", Origin: "pod:b", Status: "pass"},
		},
	}
	out := captureStdout(t, func() { printRecipeTAP(res) })
	if !strings.HasPrefix(out, "TAP version 13\n") {
		t.Errorf("missing TAP v13 header. Got:\n%s", out)
	}
	if !strings.Contains(out, "1..2\n") {
		t.Errorf("missing plan line `1..2`. Got:\n%s", out)
	}
}

// TestPrintRecipeTAP_PassFailSkip exercises the three status branches.
func TestPrintRecipeTAP_PassFailSkip(t *testing.T) {
	res := &EvalRunResults{
		Scenario: []ScenarioEvalResult{
			{Name: "p", Origin: "pod:x", Status: "pass"},
			{Name: "s", Origin: "pod:x", Status: "skip"},
			{
				Name:   "f",
				Origin: "pod:x",
				Status: "fail",
				Step: []StepEvalResult{
					{Status: "pass", Verb: "command", Text: "earlier ok step"},
					{
						Status: "fail",
						Verb:   "command",
						Text:   "the broken assertion",
						StepID: "desc:pod:x:0:1",
					},
				},
			},
		},
	}
	out := captureStdout(t, func() { printRecipeTAP(res) })

	checks := []string{
		"ok 1 - p\n",
		"ok 2 - s # SKIP\n",
		"not ok 3 - f\n",
		// YAML diagnostic block — emitted only on failure
		"  ---\n",
		`  origin: "pod:x"`,
		"  failed_steps:\n",
		`    - text: "the broken assertion"`,
		`      verb: "command"`,
		`      step_id: "desc:pod:x:0:1"`,
		"  ...\n",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\n--- actual ---\n%s", want, out)
		}
	}
	// Passing/skipping scenarios MUST NOT carry diagnostic blocks
	// (YAML `---` only follows the not-ok line in our output).
	idxNotOk := strings.Index(out, "not ok 3")
	if idxNotOk == -1 {
		t.Fatalf("expected `not ok 3` line: %s", out)
	}
	if strings.Index(out, "  ---") < idxNotOk {
		t.Errorf("YAML diagnostic block appeared before the not-ok line: %s", out)
	}
}

// TestPrintRecipeTAP_NilSafe asserts we don't panic on nil input.
func TestPrintRecipeTAP_NilSafe(t *testing.T) {
	out := captureStdout(t, func() { printRecipeTAP(nil) })
	if !strings.Contains(out, "TAP version 13\n") {
		t.Errorf("nil result should still emit TAP header. Got:\n%s", out)
	}
	if !strings.Contains(out, "1..0\n") {
		t.Errorf("nil result should emit `1..0` plan. Got:\n%s", out)
	}
}
