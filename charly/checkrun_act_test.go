package main

import (
	"context"
	"strings"
	"testing"
)

// The act renderer produces the create/configure command for each
// state-provision verb, and declines (ok=false) for observe-only verbs.
func TestRenderProvisionScript(t *testing.T) {
	tr := true
	cases := []struct {
		name     string
		op       Op
		verb     string
		wantOK   bool
		contains string
	}{
		{"package", Op{Package: "redis", Do: "act"}, "package", true, "install"},
		{"service", Op{Service: "sshd", Do: "act"}, "service", true, "enable --now"},
		{"file-content", Op{File: "/etc/x.conf", Content: "hi\n", Mode: "0644", Do: "act"}, "file", true, "chmod '0644'"},
		{"user", Op{User: "bob", Do: "act"}, "user", true, "useradd"},
		{"group", Op{Group: "devs", Do: "act"}, "group", true, "groupadd"},
		{"kernel-param", Op{KernelParam: "vm.swappiness", Value: MatcherList{{Op: "equals", Value: "10"}}, Do: "act"}, "kernel-param", true, "sysctl -w 'vm.swappiness'='10'"},
		// An observe-only verb has no act form → ok=false (falls to the probe handler).
		{"port-observe", Op{Port: 80, Listening: &tr}, "port", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := renderProvisionScript(&c.op, c.verb, nil)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (script=%q)", ok, c.wantOK, got)
			}
			if c.wantOK && !strings.Contains(got, c.contains) {
				t.Errorf("script %q does not contain %q", got, c.contains)
			}
		})
	}
}

// A do:instruct op (the agent verb) routes to the bound grader, threading the
// instruction text; with no grader it is an advisory skip (not a fail).
func TestGradeInstruct_RoutesToGrader(t *testing.T) {
	r := NewRunner(nil, nil, RunModeLive)
	g := &stubGrader{pass: true}
	r.Grader = g
	res := r.gradeInstruct(context.Background(), &Op{Agent: "confirm the dashboard is populated"})
	if res.Status != TestPass {
		t.Fatalf("instruct with a passing grader should pass, got %+v", res)
	}
	if g.calls != 1 || g.lastReq.Text != "confirm the dashboard is populated" {
		t.Errorf("grader not called with the agent instruction: calls=%d req=%+v", g.calls, g.lastReq)
	}

	// No grader bound → advisory skip, never a fail.
	r2 := NewRunner(nil, nil, RunModeLive)
	res2 := r2.gradeInstruct(context.Background(), &Op{Agent: "x"})
	if res2.Status != TestSkip {
		t.Errorf("instruct with no grader should skip (advisory), got %v", res2.Status)
	}
}

// The agent verb resolves to do:instruct via the VerbCatalog default, so a bare
// agent op dispatches to the grader (not the unknown-verb fallthrough).
func TestAgentVerb_DefaultsToInstruct(t *testing.T) {
	op := Op{Agent: "do a complex manual setup"}
	if op.EffectiveDo() != DoInstruct {
		t.Errorf("agent verb EffectiveDo = %q, want instruct", op.EffectiveDo())
	}
	// And it is NOT a pending/prose step (it carries the agent verb).
	if (&Step{Op: op}).IsPending() {
		t.Errorf("an agent: step must not be classified as pending prose")
	}
}
