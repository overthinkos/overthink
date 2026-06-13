package main

import (
	"strings"
	"testing"
)

// The act renderer produces the create/configure command for each
// state-provision verb, and declines (ok=false) for observe-only verbs.
// Act-ness is the enclosing step's `run:` keyword (intentDo=act); the renderer
// itself keys on the verb, so the Op carries no `do:` field anymore.
func TestRenderProvisionScript(t *testing.T) {
	tr := true
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
		{"group", Op{Group: "devs"}, "group", true, "groupadd"},
		{"kernel-param", Op{KernelParam: "vm.swappiness", Value: MatcherList{{Op: "equals", Value: "10"}}}, "kernel-param", true, "sysctl -w 'vm.swappiness'='10'"},
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
