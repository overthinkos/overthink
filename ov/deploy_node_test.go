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
			Target: "vm",
			Vm:     "arch",
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

// TestMergeDeployConfigsLocalCutoverFields locks in the field-level merge for
// the schema-v4 local cutover (kind:host → kind:local) additions: Local, User,
// SSHArgs. Without these, target:local deployments authored in the project
// deploy.yml lost their template ref + ssh overrides whenever resolveTreeRoot
// merged via MergeDeployConfigs(projectDC, localDC), leaving runLocal with an
// empty layer list and a silent no-op install.
func TestMergeDeployConfigsLocalCutoverFields(t *testing.T) {
	project := &DeployConfig{Deployment: map[string]DeploymentNode{
		"qc": {
			Target:  "local",
			Local:   "cachyos-dx",
			Host:    "local",
			User:    "alice",
			SSHArgs: []string{"-o", "ServerAliveInterval=30"},
		},
	}}
	merged := MergeDeployConfigs(project, nil)
	got, ok := merged.Deployment["qc"]
	if !ok {
		t.Fatal("qc dropped by MergeDeployConfigs")
	}
	if got.Local != "cachyos-dx" {
		t.Errorf("Local field lost: got %q want %q", got.Local, "cachyos-dx")
	}
	if got.User != "alice" {
		t.Errorf("User field lost: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ServerAliveInterval=30"}) {
		t.Errorf("SSHArgs field lost: got %v", got.SSHArgs)
	}
	// Per-machine overlay wins on collision (mirrors Host's behavior).
	overlay := &DeployConfig{Deployment: map[string]DeploymentNode{
		"qc": {Local: "ci-runner", User: "bob", SSHArgs: []string{"-o", "ProxyJump=bastion"}},
	}}
	merged = MergeDeployConfigs(project, overlay)
	got = merged.Deployment["qc"]
	if got.Local != "ci-runner" {
		t.Errorf("overlay Local should win: got %q", got.Local)
	}
	if got.User != "bob" {
		t.Errorf("overlay User should win: got %q", got.User)
	}
	if !equalSlices(got.SSHArgs, []string{"-o", "ProxyJump=bastion"}) {
		t.Errorf("overlay SSHArgs should win: got %v", got.SSHArgs)
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
