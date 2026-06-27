package main

import "testing"

// newVenueTestUF builds a small UnifiedFile covering every venue class the
// shared check-verb resolver must distinguish.
func newVenueTestUF() *UnifiedFile {
	return &UnifiedFile{
		VM: map[string]*VmSpec{
			"cachyos-gpu": {}, // bare kind:vm entity
		},
		Bundle: map[string]BundleNode{
			"web-pod":     {Target: "pod"},
			"k3s-vm":      {Target: "vm", From: "k3s-vm-entity"},
			"bare-vm-dep": {Target: "vm"}, // target:vm with no explicit Vm → falls back to key
			"my-local":    {Target: "local"},
			"remote-host": {Target: "local", Host: "user@box"},
		},
	}
}

func TestCheckVmTarget(t *testing.T) {
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
		gotVM, gotOK := checkVmTarget(uf, tc.name)
		if gotOK != tc.wantOK || gotVM != tc.wantVM {
			t.Errorf("checkVmTarget(%q) = (%q, %v), want (%q, %v)",
				tc.name, gotVM, gotOK, tc.wantVM, tc.wantOK)
		}
	}
}

func TestCheckVmTargetNilUF(t *testing.T) {
	if vm, ok := checkVmTarget(nil, "anything"); ok || vm != "" {
		t.Errorf("checkVmTarget(nil, …) = (%q, %v), want (\"\", false)", vm, ok)
	}
}

func TestCheckLocalTarget(t *testing.T) {
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
		node, gotOK := checkLocalTarget(uf, tc.name)
		if gotOK != tc.wantOK {
			t.Errorf("checkLocalTarget(%q) ok = %v, want %v", tc.name, gotOK, tc.wantOK)
			continue
		}
		if gotOK && node.Host != tc.host {
			t.Errorf("checkLocalTarget(%q) node.Host = %q, want %q", tc.name, node.Host, tc.host)
		}
	}
}

func TestCheckLocalTargetNilUF(t *testing.T) {
	if _, ok := checkLocalTarget(nil, "anything"); ok {
		t.Errorf("checkLocalTarget(nil, …) ok = true, want false")
	}
}

// TestParsePublishedPort covers parsePublishedPort, the shared host "ip:port"
// normalizer behind containerPublishedAddr (the port-protocol verbs' venue
// resolution). It moved here from vnc_test.go when the vnc verb externalized.
func TestParsePublishedPort(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{"standard localhost binding", "127.0.0.1:5900\n", "127.0.0.1:5900", false},
		{"all interfaces binding", "0.0.0.0:5900\n", "127.0.0.1:5900", false},
		{"random high port", "0.0.0.0:49900\n", "127.0.0.1:49900", false},
		{"ipv6 binding", "[::]:5900\n", "127.0.0.1:5900", false},
		{"multiple lines", "0.0.0.0:5900\n[::]:5900\n", "127.0.0.1:5900", false},
		{"no trailing newline", "127.0.0.1:5900", "127.0.0.1:5900", false},
		{"empty output", "", "", true},
		{"only whitespace", "  \n", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePublishedPort(tt.output, 5900)
			if (err != nil) != tt.wantErr {
				t.Errorf("parsePublishedPort() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parsePublishedPort() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResolveCheckVenueLocalDot verifies the "." fast-path returns a host venue
// without touching the project config (the in-guest delegation target).
func TestResolveCheckVenueLocalDot(t *testing.T) {
	v, err := resolveCheckVenue(".", "")
	if err != nil {
		t.Fatalf("resolveCheckVenue(\".\") error: %v", err)
	}
	if v.Kind != "host" {
		t.Errorf("resolveCheckVenue(\".\").Kind = %q, want host", v.Kind)
	}
	if _, ok := v.Exec.(ShellExecutor); !ok {
		t.Errorf("resolveCheckVenue(\".\").Exec = %T, want ShellExecutor", v.Exec)
	}
	if v.IsContainer() {
		t.Errorf("resolveCheckVenue(\".\").IsContainer() = true, want false")
	}
}
