package main

import (
	"strings"
	"testing"
)

// deploy_node_test.go — tests for DeploymentNode tree walking and
// dotted-path resolution.

func makeTree() map[string]DeploymentNode {
	return map[string]DeploymentNode{
		"stack": {
			Target: "container",
			Nested: map[string]*DeploymentNode{
				"web": {
					Target: "container",
					Nested: map[string]*DeploymentNode{
						"db": {Target: "host"},
					},
				},
				"worker": {Target: "host"},
			},
		},
		"arch": {
			Target:   "vm",
			Vm: "arch",
		},
	}
}

func TestWalkPreOrder_RootThenChildren(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := root.WalkPreOrder("stack", func(path string, node *DeploymentNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack", "stack.web", "stack.web.db", "stack.worker"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestWalkPostOrder_ChildrenThenRoot(t *testing.T) {
	tree := makeTree()
	root := tree["stack"]
	var paths []string
	err := root.WalkPostOrder("stack", func(path string, node *DeploymentNode) error {
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"stack.web.db", "stack.web", "stack.worker", "stack"}
	if !equalSlices(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestResolveNodePath_FindsNested(t *testing.T) {
	tree := makeTree()
	node, ancestors, err := ResolveNodePath(tree, "stack.web.db")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if node.Target != "host" {
		t.Errorf("resolved target = %q, want host", node.Target)
	}
	if len(ancestors) != 2 {
		t.Errorf("ancestors len = %d, want 2", len(ancestors))
	}
}

func TestResolveNodePath_MissingSegment(t *testing.T) {
	tree := makeTree()
	_, _, err := ResolveNodePath(tree, "stack.missing.db")
	if err == nil {
		t.Fatal("expected error for missing segment")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error to name the missing segment, got %v", err)
	}
}

func TestResolveNodePath_EmptyPath(t *testing.T) {
	tree := makeTree()
	_, _, err := ResolveNodePath(tree, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestResolveNodePath_MalformedDots(t *testing.T) {
	tree := makeTree()
	for _, bad := range []string{"stack.", ".stack", "stack..web"} {
		if _, _, err := ResolveNodePath(tree, bad); err == nil {
			t.Errorf("expected error for malformed path %q", bad)
		}
	}
}

func TestValidateDeploymentTree_RejectsDotInName(t *testing.T) {
	section := &DeploymentsSection{
		Images: map[string]DeploymentNode{
			"bad.name": {Target: "host"},
		},
	}
	err := validateDeploymentTree(section)
	if err == nil {
		t.Fatal("expected error for '.' in deployment name")
	}
	if !strings.Contains(err.Error(), "'.'") {
		t.Errorf("error should cite the reserved character, got %v", err)
	}
}

func TestValidateDeploymentTree_RejectsArchCloudBase(t *testing.T) {
	section := &DeploymentsSection{
		Images: map[string]DeploymentNode{
			"arch-cloud-base": {Target: "vm"},
		},
	}
	err := validateDeploymentTree(section)
	if err == nil {
		t.Fatal("expected error for legacy arch-cloud-base name")
	}
}

func TestSortedChildKeys_Deterministic(t *testing.T) {
	kids := map[string]*DeploymentNode{"z": {}, "a": {}, "m": {}}
	got := sortedNestedKeys(kids)
	if !equalSlices(got, []string{"a", "m", "z"}) {
		t.Errorf("got %v, want [a m z]", got)
	}
}

func TestHasChildren(t *testing.T) {
	empty := &DeploymentNode{}
	if empty.HasChildren() {
		t.Error("empty node should not report HasChildren")
	}
	withKids := &DeploymentNode{Nested: map[string]*DeploymentNode{"k": {}}}
	if !withKids.HasChildren() {
		t.Error("node with children should report HasChildren")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
