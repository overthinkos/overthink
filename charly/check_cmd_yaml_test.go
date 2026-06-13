package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestEmitImageTestYAML_RoundTripsThroughParseCharlyTestOutput is the
// load-bearing invariant: whatever `charly check box --format yaml`
// emits MUST parse cleanly via ParseCharlyTestOutput. Without this,
// the benchmark scorer would silently mis-parse and classify
// scenarios wrong.
func TestEmitImageTestYAML_RoundTripsThroughParseCharlyTestOutput(t *testing.T) {
	scenarios := []ScenarioResult{
		{
			Origin:     "candy:sshd",
			ScenarioID: "desc:candy:sshd:0",
			Name:       "SSH server reachable",
			Tag:        []string{"smoke"},
			Status:     TestPass,
			Pending:    0,
			Step: []StepResult{
				{
					Keyword: "given",
					Text:    "sshd is installed",
					StepID:  "desc:candy:sshd:0:0",
					Result:  CheckResult{Verb: "package", Status: TestPass},
				},
				{
					Keyword: "when",
					Text:    "connecting",
					StepID:  "desc:candy:sshd:0:1",
					Result:  CheckResult{Verb: "port", Status: TestPass},
				},
			},
		},
		{
			Origin:     "candy:foo",
			ScenarioID: "desc:candy:foo:0",
			Name:       "Foo service runs",
			Status:     TestFail,
			Pending:    1,
			Step: []StepResult{
				{
					Keyword: "then",
					Text:    "pending step",
					StepID:  "desc:candy:foo:0:0",
					Result:  CheckResult{Verb: "", Status: TestSkip},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ovbench/test:charly-fedora", "", scenarios, nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	// Sanity: the emitted YAML looks right.
	out := buf.String()
	if !strings.Contains(out, "box: ovbench/test:charly-fedora") {
		t.Errorf("missing box line: %q", out)
	}
	if !strings.Contains(out, "mode: box") {
		t.Errorf("missing mode: %q", out)
	}

	// Round-trip through the benchmark parser.
	parsed, err := ParseCharlyTestOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCharlyTestOutput failed on emitted YAML: %v\n%s", err, out)
	}
	if parsed.Box != "ovbench/test:charly-fedora" {
		t.Errorf("parsed box: %q", parsed.Box)
	}
	if parsed.Mode != "box" {
		t.Errorf("parsed mode: %q", parsed.Mode)
	}
	if len(parsed.Scenario) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(parsed.Scenario))
	}
	if parsed.Scenario[0].ID != "desc:candy:sshd:0" {
		t.Errorf("scenario[0].ID: %q", parsed.Scenario[0].ID)
	}
	if parsed.Scenario[0].Status != "pass" {
		t.Errorf("scenario[0].Status: %q", parsed.Scenario[0].Status)
	}
	if parsed.Scenario[1].Status != "fail" {
		t.Errorf("scenario[1].Status: %q", parsed.Scenario[1].Status)
	}
	if parsed.Scenario[1].PendingSteps != 1 {
		t.Errorf("scenario[1].PendingSteps: %d", parsed.Scenario[1].PendingSteps)
	}
	// Summary derivation (producer set totals).
	if parsed.Summary.Total != 2 || parsed.Summary.Pass != 1 || parsed.Summary.Fail != 1 {
		t.Errorf("summary: %+v", parsed.Summary)
	}
	// Pending step flag propagates.
	foo := parsed.Scenario[1]
	if len(foo.Step) != 1 {
		t.Fatalf("foo.Step: %d", len(foo.Step))
	}
	if !foo.Step[0].Pending {
		t.Errorf("step with no verb should have Pending=true; got %+v", foo.Step[0])
	}
}

func TestEmitImageTestYAML_LiveContainerMode(t *testing.T) {
	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ref:tag", "charly-fedora-coder", nil, nil); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(buf.String(), "mode: run") {
		t.Errorf("live container should emit mode: run; got %q", buf.String())
	}
}

func TestEmitImageTestYAML_EmptyScenariosEmitsUsableYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := emitImageTestYAML(&buf, "ref:tag", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCharlyTestOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if parsed.Summary.Total != 0 {
		t.Errorf("empty should yield total=0: %+v", parsed.Summary)
	}
}
