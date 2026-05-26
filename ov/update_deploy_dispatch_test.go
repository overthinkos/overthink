package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveUpdateDeployNode guards the 2026-05 fix for `ov update <base>
// -i <instance>`: the deploy lookup must compose the full deploy key
// (deployKey(image, instance)) so an instance-only `<base>/<instance>`
// entry resolves. Before the fix the dispatcher looked up the bare base
// name and failed with `no deploy named "<base>"`.
func TestResolveUpdateDeployNode(t *testing.T) {
	tree := map[string]DeploymentNode{
		"foo/bar": {Target: "pod", Image: "foo"},
		"baz":     {Target: "pod", Image: "baz"},
		// Nested topology — exercises the dotted-path walk, which must keep
		// working because deployKey returns a dotted name unchanged when
		// instance is empty.
		"stack": {
			Target: "vm",
			Nested: map[string]*DeploymentNode{
				"web": {Target: "pod", Image: "web"},
			},
		},
	}

	t.Run("instance key resolves", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "foo", "bar")
		if err != nil {
			t.Fatalf("instance lookup failed: %v", err)
		}
		if node.Image != "foo" {
			t.Errorf("got Image %q, want foo", node.Image)
		}
	})

	t.Run("bare name resolves", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "baz", "")
		if err != nil {
			t.Fatalf("bare lookup failed: %v", err)
		}
		if node.Image != "baz" {
			t.Errorf("got Image %q, want baz", node.Image)
		}
	})

	t.Run("dotted nested path still walks", func(t *testing.T) {
		node, err := resolveUpdateDeployNode(tree, "stack.web", "")
		if err != nil {
			t.Fatalf("nested lookup failed: %v", err)
		}
		if node.Image != "web" {
			t.Errorf("got Image %q, want web", node.Image)
		}
	})

	t.Run("regression: bare base must NOT resolve an instance-only entry", func(t *testing.T) {
		// This is the exact bug — `ov update foo -i bar` previously looked
		// up bare "foo" and found nothing. The fix uses deployKey, so a
		// bare-base lookup (instance "") correctly does NOT match foo/bar.
		_, err := resolveUpdateDeployNode(tree, "foo", "")
		if err == nil {
			t.Fatal("bare base 'foo' must not resolve when only foo/bar exists")
		}
	})

	t.Run("missing instance error reports the full key", func(t *testing.T) {
		_, err := resolveUpdateDeployNode(tree, "foo", "missing")
		if err == nil {
			t.Fatal("expected error for missing instance key")
		}
		if !strings.Contains(err.Error(), "foo/missing") {
			t.Errorf("error %q should name the full key foo/missing", err.Error())
		}
	})
}

// TestCheckUpdateDisposable guards the 2026-05-26 disposable-enforcement
// fix on `ov update`. Before the fix, `ov update <image> -i <instance>`
// destroyed + recreated the deploy without checking the disposable flag,
// silently bypassing the operator's lockdown. After the fix the dispatch
// refuses with a remediation message that mirrors /ov-internals:
// disposable's sample refusal text.
func TestCheckUpdateDisposable(t *testing.T) {
	tDisposable := boolPtr(true)
	fDisposable := boolPtr(false)
	cases := []struct {
		name     string
		node     *DeploymentNode
		image    string
		instance string
		wantErr  bool
		want     []string // substrings expected in the error message
	}{
		{
			name:    "explicit disposable true is allowed",
			node:    &DeploymentNode{Disposable: tDisposable},
			image:   "ok-pod",
			wantErr: false,
		},
		{
			name:    "ephemeral implies disposable (no error)",
			node:    &DeploymentNode{Ephemeral: &EphemeralLifetime{}},
			image:   "scratch-pod",
			wantErr: false,
		},
		{
			name:    "absent disposable refuses",
			node:    &DeploymentNode{},
			image:   "prod-api",
			wantErr: true,
			want:    []string{"prod-api", "is not marked", "disposable: true", "lifecycle: (unset)"},
		},
		{
			name:    "explicit disposable: false refuses",
			node:    &DeploymentNode{Disposable: fDisposable, Lifecycle: "prod"},
			image:   "locked-api",
			wantErr: true,
			want:    []string{"locked-api", "lifecycle: prod"},
		},
		{
			name:     "instance form includes the slash key",
			node:     &DeploymentNode{Disposable: fDisposable},
			image:    "versa",
			instance: "ecovoyage",
			wantErr:  true,
			want:     []string{"versa/ecovoyage", "ov deploy add versa/ecovoyage"},
		},
		{
			name:    "lifecycle dev alone does NOT authorize",
			node:    &DeploymentNode{Lifecycle: "dev"},
			image:   "dev-bench",
			wantErr: true,
			want:    []string{"dev-bench", "lifecycle: dev", "lifecycle tags alone do NOT authorize"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUpdateDisposable(tc.node, tc.image, tc.instance)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected refusal, got nil")
				}
				for _, sub := range tc.want {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q missing substring %q", err.Error(), sub)
					}
				}
			} else if err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}

// TestExtractQuadletImageLine guards the 2026-05-26 cross-pollution
// fix on updateAllDeployedQuadlets. The function preserves the
// operator-chosen Image= line on a sibling deploy when an unrelated
// `ov update <bed>` triggers a cross-deploy env refresh; the test
// covers the happy path (Image= present), the absent-Image= path
// (caller falls back to fresh resolution), and the missing-file path
// (caller falls back).
func TestExtractQuadletImageLine(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{
			name: "Image= present at top of [Container] block",
			content: `[Unit]
Description=x

[Container]
Image=ghcr.io/overthinkos/versa:2026.135.1326
ContainerName=ov-versa-ecovoyage
`,
			want: "ghcr.io/overthinkos/versa:2026.135.1326",
		},
		{
			name: "Image= with sidecar Pod= directive (still finds the right line)",
			content: `[Container]
Pod=ov-versa.pod
Image=ghcr.io/tailscale/tailscale:latest
ContainerName=ov-versa-tailscale
`,
			want: "ghcr.io/tailscale/tailscale:latest",
		},
		{
			name: "no Image= line returns empty without error (caller falls back)",
			content: `[Unit]
Description=missing-image

[Container]
ContainerName=ov-broken
`,
			want: "",
		},
		{
			name:    "missing file returns error",
			content: "", // signal: don't create file
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "test-"+tc.name+".container")
			if tc.content != "" || tc.name != "missing file returns error" {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			got, err := extractQuadletImageLine(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("imageRef = %q, want %q", got, tc.want)
			}
		})
	}
}
