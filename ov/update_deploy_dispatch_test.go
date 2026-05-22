package main

import (
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
