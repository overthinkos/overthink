package main

import (
	"context"
	"strings"
	"testing"
)

// nestedRecordingExec is a DeployExecutor that records the RunUser scripts
// deployNestedPodsInGuest issues (the in-guest `ov deploy from-image` calls) and
// the PutFile destinations (the host-ov delivery), so the test can assert the
// nested-pod-in-VM orchestration without a real guest.
type nestedRecordingExec struct {
	userScripts []string
	putDests    []string // remote paths the host ov binary was delivered to
}

func (e *nestedRecordingExec) Venue() string                                     { return "nested-rec://test" }
func (e *nestedRecordingExec) RunSystem(context.Context, string, EmitOpts) error { return nil }
func (e *nestedRecordingExec) RunUser(_ context.Context, script string, _ EmitOpts) error {
	e.userScripts = append(e.userScripts, script)
	return nil
}
func (e *nestedRecordingExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *nestedRecordingExec) PutFile(_ context.Context, _, remotePath string, _ uint32, _ bool, _ EmitOpts) error {
	e.putDests = append(e.putDests, remotePath)
	return nil
}
func (e *nestedRecordingExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *nestedRecordingExec) RunCapture(context.Context, string) (string, string, int, error) {
	return "", "", 0, nil
}
func (e *nestedRecordingExec) Kind() string { return "nested-rec" }
func (e *nestedRecordingExec) ResolveHome(context.Context, string) (string, error) {
	return "/home/guest", nil
}

// TestDeployNestedPodsInGuest_DeploysOnlyPodChildren proves the nested-pod-in-VM
// capability's deploy orchestration: each nested target:pod child is built on the
// host, cp-image'd into the guest as localhost/ov-<key>:latest, and brought up via
// the guest's own project-free `ov deploy from-image <ref> <key>` as a PERSISTENT
// (lingering) quadlet — in sorted order — while non-pod children (android/k8s) and
// image-less entries are skipped. Without the capability the helper does not exist
// / does nothing and these assertions fail; this is the eval-coverage gate for the
// Go side of Cutover 2 (the live bed proves it end-to-end on the GPU VM).
func TestDeployNestedPodsInGuest_DeploysOnlyPodChildren(t *testing.T) {
	// Stub the child-process boundary: record build / vm-cp-image argv, no real ov.
	var ovCalls [][]string
	orig := runOvSubcommand
	runOvSubcommand = func(args ...string) error {
		ovCalls = append(ovCalls, append([]string(nil), args...))
		return nil
	}
	defer func() { runOvSubcommand = orig }()

	// Stamp the host ov identity. The nested from-image delegation ALWAYS runs
	// the HOST's own ov (delivered to /tmp/ov-<calver> via putHostOvInGuest),
	// never the guest's PATH ov — the host binary is the from-image authority,
	// and a /tmp path can't shadow the guest's pacman /usr/bin/ov.
	savedVer := BuildCalVer
	defer func() { BuildCalVer = savedVer }()
	BuildCalVer = "2026.154.943"

	exec := &nestedRecordingExec{}
	node := &DeploymentNode{
		Nested: map[string]*DeploymentNode{
			"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			"alpha-pod":   {Target: "", Image: "alpha-img"},               // default target == pod
			"droid":       {Target: "android", Image: "android-emulator"}, // skipped (not in-guest)
			"empty":       {Target: "pod"},                                // skipped (no image)
		},
	}

	if err := deployNestedPodsInGuest("cachyos-gpu-vm", node, exec, EmitOpts{}); err != nil {
		t.Fatalf("deployNestedPodsInGuest: %v", err)
	}

	// Two pod children processed (alpha-pod, selkies-kde — sorted); each issues an
	// image-build + a vm-cp-image → 4 ov subcommands, in this exact order. The
	// cp-image carries --rootless so the image lands in the guest USER's podman
	// storage, which the --user from-image quadlet below reads.
	wantOv := [][]string{
		{"box", "build", "alpha-img"},
		{"vm", "cp-image", "cachyos-gpu-vm", "alpha-img", "--as", "localhost/ov-alpha-pod:latest", "--rootless"},
		{"box", "build", "selkies-kde-nvidia"},
		{"vm", "cp-image", "cachyos-gpu-vm", "selkies-kde-nvidia", "--as", "localhost/ov-selkies-kde:latest", "--rootless"},
	}
	if len(ovCalls) != len(wantOv) {
		t.Fatalf("expected %d ov subcommands (build+cp-image × 2 pod children), got %d: %v",
			len(wantOv), len(ovCalls), ovCalls)
	}
	for i, want := range wantOv {
		if strings.Join(ovCalls[i], " ") != strings.Join(want, " ") {
			t.Errorf("ov call %d = %v, want %v", i, ovCalls[i], want)
		}
	}

	// The host ov was delivered into the guest at the explicit /tmp/ov-<calver>
	// path (NOT shadowing the guest's pacman /usr/bin/ov) before any from-image
	// deploy. One delivery for the whole batch (same guest venue).
	wantOvPath := "/tmp/ov-2026.154.943"
	if len(exec.putDests) == 0 {
		t.Fatalf("host ov was never delivered into the guest (no PutFile)")
	}
	deliveredHostOv := false
	for _, d := range exec.putDests {
		if d == wantOvPath {
			deliveredHostOv = true
		}
	}
	if !deliveredHostOv {
		t.Errorf("host ov not delivered to %s; PutFile dests = %v", wantOvPath, exec.putDests)
	}

	// Two in-guest from-image deploys (the persistent quadlets), sorted. Each
	// invokes the delivered /tmp host ov by EXPLICIT PATH (the from-image
	// authority), never the guest's PATH ov.
	if len(exec.userScripts) != 2 {
		t.Fatalf("expected 2 in-guest from-image deploys, got %d: %v", len(exec.userScripts), exec.userScripts)
	}
	if !strings.Contains(exec.userScripts[0], wantOvPath+" deploy from-image localhost/ov-alpha-pod:latest alpha-pod") {
		t.Errorf("script[0] missing alpha-pod from-image deploy via host /tmp ov: %q", exec.userScripts[0])
	}
	if !strings.Contains(exec.userScripts[1], wantOvPath+" deploy from-image localhost/ov-selkies-kde:latest selkies-kde") {
		t.Errorf("script[1] missing selkies-kde from-image deploy via host /tmp ov: %q", exec.userScripts[1])
	}
	// Lingering is enabled so the --user quadlet auto-starts at boot — the
	// persistence property the bed's fresh-rebuild leg (guest reboot) proves.
	for i, s := range exec.userScripts {
		if !strings.Contains(s, "loginctl enable-linger") {
			t.Errorf("script[%d] missing enable-linger (persistence): %q", i, s)
		}
	}

	// The skipped children leave no trace anywhere.
	var allOv string
	for _, c := range ovCalls {
		allOv += strings.Join(c, " ") + "\n"
	}
	if strings.Contains(allOv, "android-emulator") {
		t.Error("android child must NOT be built/loaded as an in-guest pod")
	}
	joinedScripts := strings.Join(exec.userScripts, "\n")
	if strings.Contains(joinedScripts, "droid") || strings.Contains(joinedScripts, "empty") {
		t.Error("non-pod / image-less children must be skipped")
	}
}

// TestDeployNestedPodsInGuest_NoNested is the no-op guard: a nil node or a node
// with no nested children must touch nothing (no build, no cp-image, no deploy).
func TestDeployNestedPodsInGuest_NoNested(t *testing.T) {
	ovCalls := 0
	orig := runOvSubcommand
	runOvSubcommand = func(args ...string) error { ovCalls++; return nil }
	defer func() { runOvSubcommand = orig }()

	exec := &nestedRecordingExec{}
	if err := deployNestedPodsInGuest("vm", nil, exec, EmitOpts{}); err != nil {
		t.Fatalf("nil node: %v", err)
	}
	if err := deployNestedPodsInGuest("vm", &DeploymentNode{}, exec, EmitOpts{}); err != nil {
		t.Fatalf("empty nested: %v", err)
	}
	if ovCalls != 0 || len(exec.userScripts) != 0 {
		t.Errorf("no-op expected, got %d ov calls + %d guest deploys", ovCalls, len(exec.userScripts))
	}
}

// TestDeriveDeploymentName covers the default-name derivation the source-less
// `ov deploy from-image <ref>` (pod + k8s) uses when no explicit name is given:
// strip the tag, take the last path component.
func TestDeriveDeploymentName(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"ghcr.io/overthinkos/selkies-kde-nvidia:2026.153.1026", "selkies-kde-nvidia"},
		{"localhost/ov-selkies-kde:latest", "ov-selkies-kde"},
		{"selkies-kde-nvidia", "selkies-kde-nvidia"},
		{"docker.io/library/redis:7", "redis"},
	}
	for _, c := range cases {
		if got := deriveDeploymentName(c.ref); got != c.want {
			t.Errorf("deriveDeploymentName(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay locks the merge
// invariant the VM target's nested-pod deploy relies on: a project VM deploy
// that declares a `nested:` target:pod child, overlaid by a per-host operator
// entry that carries its OWN per-host fields but NO `nested:` block, MUST keep
// the project's nested child after merge. This is exactly the operator
// workstation shape (~/.config/ov/deploy.yml's cachyos-gpu has
// target/vm/preemptible but no nested:) that surfaced the failure: a whole-node
// re-read of the operator deploy.yml (operator clobbering project) would drop
// nested: and silently skip deployNestedPodsInGuest. VmUnifiedTarget.Add
// consumes this merged node directly. The eval-bed keys (no operator overlay)
// were never affected — which is why the bug hid behind a green pod bed. The
// end-to-end consumption proof is the live `ov eval live cachyos-gpu.selkies-kde`
// R10.
func TestMergeDeployConfigs_VMNestedSurvivesNestedlessOverlay(t *testing.T) {
	project := &DeployConfig{Deploy: map[string]DeploymentNode{
		"cachyos-gpu": {
			Target: "vm",
			Vm:     "cachyos-gpu",
			Nested: map[string]*DeploymentNode{
				"selkies-kde": {Target: "pod", Image: "selkies-kde-nvidia"},
			},
		},
	}}
	// Operator per-host overlay: per-host field set, NO nested: block.
	operator := &DeployConfig{Deploy: map[string]DeploymentNode{
		"cachyos-gpu": {
			Target:    "vm",
			Vm:        "cachyos-gpu",
			Lifecycle: "prod",
		},
	}}

	merged := MergeDeployConfigs(project, operator)
	node := merged.Deploy["cachyos-gpu"]

	// The operator overlay's non-zero field won (proves the overlay DID merge,
	// not that we merely read the project node)...
	if node.Lifecycle != "prod" {
		t.Errorf("operator Lifecycle not merged: got %q, want prod", node.Lifecycle)
	}
	// ...AND the project's nested child PASSED THROUGH the nestedless overlay.
	// A whole-node replace (the old re-read bug shape) would drop it here.
	if len(node.Nested) != 1 || node.Nested["selkies-kde"] == nil {
		t.Fatalf("project nested: dropped by nestedless operator overlay: %#v", node.Nested)
	}
	if got := node.Nested["selkies-kde"].Image; got != "selkies-kde-nvidia" {
		t.Errorf("nested child box: got %q, want selkies-kde-nvidia", got)
	}
}
