package main

import "testing"

// newVenueTestUF builds a small UnifiedFile covering every venue class the
// shared eval-verb resolver must distinguish.
func newVenueTestUF() *UnifiedFile {
	return &UnifiedFile{
		VM: map[string]*VmSpec{
			"cachyos-gpu": {}, // bare kind:vm entity
		},
		Deploy: map[string]DeploymentNode{
			"web-pod":     {Target: "pod"},
			"k3s-vm":      {Target: "vm", Vm: "k3s-vm-entity"},
			"bare-vm-dep": {Target: "vm"}, // target:vm with no explicit Vm → falls back to key
			"my-local":    {Target: "local"},
			"remote-host": {Target: "local", Host: "user@box"},
		},
	}
}

func TestEvalVmTarget(t *testing.T) {
	uf := newVenueTestUF()
	cases := []struct {
		name   string
		wantVM string
		wantOK bool
	}{
		{"cachyos-gpu", "cachyos-gpu", true},    // kind:vm entity
		{"k3s-vm", "k3s-vm-entity", true},       // target:vm deploy → entry.Vm
		{"bare-vm-dep", "bare-vm-dep", true},    // target:vm, no Vm → key fallback
		{"k3s-vm.inner", "k3s-vm-entity", true}, // dotted root is target:vm
		{"web-pod", "", false},                  // pod is not a VM
		{"my-local", "", false},                 // local is not a VM
		{"nonexistent", "", false},              // unknown
	}
	for _, tc := range cases {
		gotVM, gotOK := evalVmTarget(uf, tc.name)
		if gotOK != tc.wantOK || gotVM != tc.wantVM {
			t.Errorf("evalVmTarget(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotVM, gotOK, tc.wantVM, tc.wantOK)
		}
	}
}

func TestEvalVmTargetNilUF(t *testing.T) {
	if vm, ok := evalVmTarget(nil, "anything"); ok || vm != "" {
		t.Errorf("evalVmTarget(nil, …) = (%q, %v), want (\"\", false)", vm, ok)
	}
}

func TestEvalLocalTarget(t *testing.T) {
	uf := newVenueTestUF()
	cases := []struct {
		name   string
		wantOK bool
		host   string
	}{
		{"my-local", true, ""},            // host:local (default shell)
		{"remote-host", true, "user@box"}, // host:<remote> (ssh)
		{"my-local.child", true, ""},      // dotted root is target:local
		{"web-pod", false, ""},            // pod is not local
		{"cachyos-gpu", false, ""},        // vm entity is not a local deploy
		{"k3s-vm", false, ""},             // target:vm is not local
	}
	for _, tc := range cases {
		node, gotOK := evalLocalTarget(uf, tc.name)
		if gotOK != tc.wantOK {
			t.Errorf("evalLocalTarget(%q) ok = %v, want %v", tc.name, gotOK, tc.wantOK)
			continue
		}
		if gotOK && node.Host != tc.host {
			t.Errorf("evalLocalTarget(%q) node.Host = %q, want %q", tc.name, node.Host, tc.host)
		}
	}
}

func TestEvalLocalTargetNilUF(t *testing.T) {
	if _, ok := evalLocalTarget(nil, "anything"); ok {
		t.Errorf("evalLocalTarget(nil, …) ok = true, want false")
	}
}

// TestResolveEvalVenueLocalDot verifies the "." fast-path returns a host venue
// without touching the project config (the in-guest delegation target).
func TestResolveEvalVenueLocalDot(t *testing.T) {
	v, err := resolveEvalVenue(".", "")
	if err != nil {
		t.Fatalf("resolveEvalVenue(\".\") error: %v", err)
	}
	if v.Kind != "host" {
		t.Errorf("resolveEvalVenue(\".\").Kind = %q, want host", v.Kind)
	}
	if _, ok := v.Exec.(ShellExecutor); !ok {
		t.Errorf("resolveEvalVenue(\".\").Exec = %T, want ShellExecutor", v.Exec)
	}
	if v.IsContainer() {
		t.Errorf("resolveEvalVenue(\".\").IsContainer() = true, want false")
	}
}
