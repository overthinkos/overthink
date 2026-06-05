package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestResolveSidecarNames covers the helper that the post-2026-05-16
// commands.go sidecar sweep depends on. Pre-fix the sweep used
// HasPrefix(name, "ov-<image>-") which over-matched Pattern-A
// instance quadlets and unrelated bases sharing the prefix
// (jupyter ⇒ over-matched jupyter-pod and jupyter/concurrency-test).
// The new sweep enumerates EXACT sidecar names from deploy.yml; this
// test pins the enumeration contract.
func TestResolveSidecarNames(t *testing.T) {
	tests := []struct {
		name       string
		deployYAML string
		image      string
		instance   string
		want       []string
	}{
		{
			name: "no entry — returns nil",
			deployYAML: `deploy:
  other:
    target: pod
    box: other
`,
			image:    "missing",
			instance: "",
			want:     nil,
		},
		{
			name: "entry without sidecars — returns nil",
			deployYAML: `deploy:
  foo:
    target: pod
    box: foo
`,
			image:    "foo",
			instance: "",
			want:     nil,
		},
		{
			name: "entry with one sidecar — single-name slice",
			deployYAML: `deploy:
  foo:
    target: pod
    box: foo
    sidecar:
      tailscale: {}
`,
			image:    "foo",
			instance: "",
			want:     []string{"tailscale"},
		},
		{
			name: "entry with multiple sidecars — sorted",
			deployYAML: `deploy:
  foo:
    target: pod
    box: foo
    sidecar:
      vault: {}
      tailscale: {}
`,
			image:    "foo",
			instance: "",
			want:     []string{"tailscale", "vault"},
		},
		{
			name: "Pattern-A instance entry with sidecar",
			deployYAML: `deploy:
  foo/inst1:
    target: pod
    box: foo
    sidecar:
      tailscale: {}
`,
			image:    "foo",
			instance: "inst1",
			want:     []string{"tailscale"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
				t.Fatalf("creating ov config dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "ov", "deploy.yml"), []byte(tc.deployYAML), 0600); err != nil {
				t.Fatalf("writing deploy.yml: %v", err)
			}

			got := resolveSidecarNames(tc.image, tc.instance)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveSidecarNames(%q, %q) = %v; want %v", tc.image, tc.instance, got, tc.want)
			}
		})
	}
}
