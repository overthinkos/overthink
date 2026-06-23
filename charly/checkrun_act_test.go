package main

import (
	"context"
	"strings"
	"testing"
)

// The act renderer produces the create/configure command for each
// state-provision verb, and declines (ok=false) for observe-only verbs.
// Act-ness is the enclosing step's `run:` keyword (intentDo=act); the verb
// provider's ProvisionActor renders it, so the Op carries no `do:` field anymore.
//
// actScriptForTest resolves a verb to its provider and renders the
// act script via ProvisionActor — the same path runProvisionAct takes (C1b).
func actScriptForTest(op *Op, verb string, distros []string) (string, bool) {
	prov, ok := providerRegistry.ResolveVerb(verb)
	if !ok {
		return "", false
	}
	actor, ok := prov.(ProvisionActor)
	if !ok {
		return "", false
	}
	return actor.RenderProvisionScript(op, distros)
}

func TestRenderProvisionScript(t *testing.T) {
	cases := []struct {
		name     string
		op       Op
		verb     string
		wantOK   bool
		contains string
	}{
		{"package", Op{Package: "redis"}, "package", true, "install"},
		{"service", Op{Service: "sshd"}, "service", true, "enable --now"},
		{"file-content", Op{File: "/etc/x.conf", Content: "hi\n", Mode: "0644"}, "file", true, "chmod '0644'"},
		{"user", Op{User: "bob"}, "user", true, "useradd"},
		// unix_group is the FIRST extracted state-provision verb: a builtin plugin unit
		// whose provider is a ProvisionActor reading plugin_input (not the removed
		// Op.UnixGroup/Op.GID fields).
		{"unix_group", Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "devs"}}, "unix_group", true, "groupadd"},
		{"kernel-param", Op{KernelParam: "vm.swappiness", Value: MatcherList{{Op: "equals", Value: "10"}}}, "kernel-param", true, "sysctl -w 'vm.swappiness'='10'"},
		// An observe-only verb has no act form → ok=false (falls to the probe handler).
		// addr is now a builtin plugin verb (authored plugin: addr + plugin_input); its
		// provider is a CheckVerbProvider, not a ProvisionActor, so it still has no act form.
		{"addr-observe", Op{Plugin: "addr", PluginInput: map[string]any{"addr": "127.0.0.1:80", "reachable": true}}, "addr", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := actScriptForTest(&c.op, c.verb, nil)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (script=%q)", ok, c.wantOK, got)
			}
			if c.wantOK && !strings.Contains(got, c.contains) {
				t.Errorf("script %q does not contain %q", got, c.contains)
			}
		})
	}
}

// TestRunProvisionActDispatch exercises the FULL act path end-to-end (C1b): the
// verb resolves to its provider, ProvisionActor renders the script, and the
// rendered script runs via the executor — pass on exit 0, fail on non-zero, and
// ok=false (fall through) for a verb with no act form.
func TestRunProvisionActDispatch(t *testing.T) {
	ctx := context.Background()

	// file is a ProvisionActor → renders `mkdir … && touch …`, execs, passes.
	fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "mkdir", exit: 0}}}
	r := &Runner{Exec: fe, Mode: RunModeLive}
	res, ok := r.runProvisionAct(ctx, &Op{File: "/tmp/x"}, "file")
	if !ok {
		t.Fatalf("runProvisionAct(file) ok=false, want true (file is a ProvisionActor)")
	}
	if res.Status != TestPass {
		t.Fatalf("runProvisionAct(file) status=%v, want TestPass; msg=%q", res.Status, res.Message)
	}
	if len(fe.calls) != 1 || !strings.Contains(fe.calls[0], "mkdir") {
		t.Fatalf("expected the rendered script to execute once; calls=%v", fe.calls)
	}

	// addr has no act form → ok=false (caller falls through to the probe handler). addr is
	// now a builtin plugin verb; its CheckVerbProvider is not a ProvisionActor.
	if _, ok := r.runProvisionAct(ctx, &Op{Plugin: "addr", PluginInput: map[string]any{"addr": "127.0.0.1:80"}}, "addr"); ok {
		t.Fatalf("runProvisionAct(addr) ok=true, want false (no act form)")
	}

	// An unknown verb (no provider) → ok=false.
	if _, ok := r.runProvisionAct(ctx, &Op{}, "no-such-verb"); ok {
		t.Fatalf("runProvisionAct(unknown) ok=true, want false")
	}

	// Non-zero exit → fail.
	feFail := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "mkdir", exit: 1, stderr: "boom"}}}
	rFail := &Runner{Exec: feFail, Mode: RunModeLive}
	res2, ok := rFail.runProvisionAct(ctx, &Op{File: "/tmp/y"}, "file")
	if !ok || res2.Status != TestFail {
		t.Fatalf("runProvisionAct(file, exit 1) = (status=%v, ok=%v), want (TestFail, true)", res2.Status, ok)
	}
}
