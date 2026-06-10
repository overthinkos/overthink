package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// --- parseVerdict --------------------------------------------------------

func TestParseVerdict_Plain(t *testing.T) {
	pass, ev, ok := parseVerdict("some reasoning here\n" + `{"verdict":"pass","evidence":"the port answered PONG"}`)
	if !ok || !pass || ev != "the port answered PONG" {
		t.Fatalf("got pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

func TestParseVerdict_Fail(t *testing.T) {
	pass, _, ok := parseVerdict(`{"verdict":"fail","evidence":"connection refused"}`)
	if !ok || pass {
		t.Fatalf("got pass=%v ok=%v, want fail", pass, ok)
	}
}

func TestParseVerdict_StreamJSON(t *testing.T) {
	out := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":"checking..."}` + "\n" +
		`{"type":"result","subtype":"success","result":"I probed it. {\"verdict\":\"pass\",\"evidence\":\"ok\"}"}`
	pass, ev, ok := parseVerdict(out)
	if !ok || !pass || ev != "ok" {
		t.Fatalf("stream-json: pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

func TestParseVerdict_NoVerdict(t *testing.T) {
	if _, _, ok := parseVerdict("just prose, no json object at all"); ok {
		t.Fatal("expected no parseable verdict")
	}
}

func TestParseVerdict_LastWins(t *testing.T) {
	// An earlier illustrative example must not beat the final real verdict.
	out := `for example {"verdict":"fail"} but actually {"verdict":"pass","evidence":"done"}`
	pass, ev, ok := parseVerdict(out)
	if !ok || !pass || ev != "done" {
		t.Fatalf("last-wins: pass=%v ev=%q ok=%v", pass, ev, ok)
	}
}

// --- RunAIOnce -----------------------------------------------------------

func TestRunAIOnce_CapturesStdout(t *testing.T) {
	ai := &AIConfig{Command: []string{"sh", "-c", `echo '{"verdict":"pass","evidence":"ok"}'`}}
	out, _, err := RunAIOnce(context.Background(), ai, "ignored", 10*time.Second)
	if err != nil {
		t.Fatalf("RunAIOnce: %v", err)
	}
	if pass, _, ok := parseVerdict(out); !ok || !pass {
		t.Fatalf("verdict not parsed from %q", out)
	}
}

func TestRunAIOnce_SubstitutesPrompt(t *testing.T) {
	ai := &AIConfig{Command: []string{"printf", "%s", "${PROMPT}"}}
	out, _, err := RunAIOnce(context.Background(), ai, "HELLO-PROMPT-TOKEN", 10*time.Second)
	if err != nil {
		t.Fatalf("RunAIOnce: %v", err)
	}
	if !strings.Contains(out, "HELLO-PROMPT-TOKEN") {
		t.Fatalf("${PROMPT} not substituted into argv: %q", out)
	}
}

func TestRunAIOnce_Timeout(t *testing.T) {
	ai := &AIConfig{Command: []string{"sleep", "10"}}
	_, _, err := RunAIOnce(context.Background(), ai, "x", 150*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestRunAIOnce_NoCommand(t *testing.T) {
	if _, _, err := RunAIOnce(context.Background(), &AIConfig{}, "x", time.Second); err == nil {
		t.Fatal("expected error for an ai entry with no command")
	}
}

// --- AgentGrader ---------------------------------------------------------

func TestAgentGrader_GradeFail(t *testing.T) {
	ai := &AIConfig{Command: []string{"sh", "-c", `echo '{"verdict":"fail","evidence":"port closed"}'`}}
	g := &AgentGrader{AI: ai, Target: "eval-pod"}
	res := g.Grade(context.Background(), GraderRequest{Keyword: "Then", Text: "the port answers"})
	if res.Status != TestFail {
		t.Fatalf("want TestFail, got %v", res.Status)
	}
	if !strings.Contains(res.Message, "port closed") {
		t.Fatalf("evidence not surfaced: %q", res.Message)
	}
}

func TestAgentGrader_UnparseableIsFail(t *testing.T) {
	ai := &AIConfig{Command: []string{"sh", "-c", `echo "I have no idea"`}}
	g := &AgentGrader{AI: ai, Target: "eval-pod"}
	res := g.Grade(context.Background(), GraderRequest{Keyword: "Then", Text: "x"})
	if res.Status != TestFail {
		t.Fatalf("unparseable grader output must FAIL (never silent pass), got %v", res.Status)
	}
}

// --- grader dispatch through RunScenarios --------------------------------

type stubGrader struct {
	pass    bool
	calls   int
	lastReq GraderRequest
}

func (g *stubGrader) Grade(_ context.Context, req GraderRequest) EvalResult {
	g.calls++
	g.lastReq = req
	st := TestFail
	if g.pass {
		st = TestPass
	}
	return EvalResult{Status: st, Verb: "agent", Message: "stub"}
}

func proseScenarioSet() *LabelDescriptionSet {
	return &LabelDescriptionSet{
		Candy: []LabeledDescription{{
			Origin: "candy:x",
			Description: Description{
				Feature:   "the gizmo works",
				Narrative: "as an operator...",
				Scenario: []Scenario{{
					Name: "gizmo-prose",
					Step: []Step{{Then: "the gizmo responds"}},
				}},
			},
		}},
	}
}

func TestRunScenarios_GraderDispatchPass(t *testing.T) {
	g := &stubGrader{pass: true}
	r := NewRunner(nil, nil, RunModeLive)
	r.Grader = g
	res := RunScenarios(context.Background(), r, proseScenarioSet(), nil, false)
	if len(res) != 1 || res[0].Status != TestPass {
		t.Fatalf("graded scenario should pass, got %+v", res)
	}
	if g.calls != 1 {
		t.Fatalf("grader called %d times, want 1", g.calls)
	}
	// Goal + scenario context threaded into the grader.
	if g.lastReq.Feature != "the gizmo works" || g.lastReq.Scenario != "gizmo-prose" || g.lastReq.Text != "the gizmo responds" {
		t.Fatalf("grader request context not threaded: %+v", g.lastReq)
	}
}

func TestRunScenarios_GraderDispatchFail(t *testing.T) {
	r := NewRunner(nil, nil, RunModeLive)
	r.Grader = &stubGrader{pass: false}
	res := RunScenarios(context.Background(), r, proseScenarioSet(), nil, false)
	if len(res) != 1 || res[0].Status != TestFail {
		t.Fatalf("a failing grader must fail the scenario, got %+v", res)
	}
}

func TestRunScenarios_NoGrader_ProseSkips(t *testing.T) {
	r := NewRunner(nil, nil, RunModeLive) // no grader
	res := RunScenarios(context.Background(), r, proseScenarioSet(), nil, false)
	if len(res) != 1 {
		t.Fatalf("want 1 scenario, got %d", len(res))
	}
	if res[0].Status == TestFail {
		t.Fatalf("prose step without a grader must NOT fail (advisory skip), got %v", res[0].Status)
	}
	if res[0].Pending != 1 {
		t.Fatalf("prose step without a grader should be pending=1, got %d", res[0].Pending)
	}
}

// --- scenarioFailCount ---------------------------------------------------

func TestScenarioFailCount(t *testing.T) {
	in := []ScenarioResult{
		{Status: TestPass}, {Status: TestFail}, {Status: TestSkip}, {Status: TestFail},
	}
	if got := scenarioFailCount(in); got != 2 {
		t.Fatalf("scenarioFailCount = %d, want 2", got)
	}
}

// --- buildGraderPrompt ---------------------------------------------------

// TestBuildGraderPrompt_PillarName is the eval-coverage gate for the pillar
// rename: the grader's system prompt names the pillar, and it must read
// "Agent Driven Evaluation" — never the retired "Agent Driven Development".
// This assertion FAILS on the pre-rename string and PASSES after.
func TestBuildGraderPrompt_PillarName(t *testing.T) {
	prompt := buildGraderPrompt(GraderRequest{Keyword: "Then", Text: "the port answers"}, "eval-pod", "")
	if !strings.Contains(prompt, "Agent Driven Evaluation") {
		t.Fatalf("grader prompt must name the pillar 'Agent Driven Evaluation'; got:\n%s", prompt)
	}
	if strings.Contains(prompt, "Agent Driven Development") {
		t.Fatalf("grader prompt still names the retired pillar 'Agent Driven Development':\n%s", prompt)
	}
}
