package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestDeployConfigLookup_NilSafe pins the post-2026-05-16 cleanup of
// the call sites that previously wrote
//
//	dc := loadDeployConfigForRead("...")
//	if dc != nil {
//	    if entry, ok := dc.Deploy[deployKey(image, instance)]; ok { ... }
//	}
//
// using nil-safe Lookup/LookupKey methods. The contract: nil receiver
// returns (zero, false) so callers can chain
// `loadDeployConfigForRead(...).Lookup(image, instance)` without a
// separate nil check.
func TestDeployConfigLookup_NilSafe(t *testing.T) {
	var dc *BundleConfig // nil
	if entry, ok := dc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
	if entry, ok := dc.LookupKey("foo"); ok {
		t.Errorf("LookupKey on nil dc returned ok=true entry=%+v; want (zero, false)", entry)
	}
}

// TestDeployConfigLookup_PresentAndAbsent pins the basic Lookup
// contract: present entries return (entry, true); absent entries and
// nil deploy map return (zero, false). Instance form is keyed via
// deployKey (image/instance); LookupKey takes the raw deploy.yml key.
func TestDeployConfigLookup_PresentAndAbsent(t *testing.T) {
	dc := &BundleConfig{Bundle: map[string]BundleNode{
		"foo":       {Target: "pod", Box: "foo"},
		"foo/inst1": {Target: "pod", Box: "foo"},
		"vm:arch":   {Target: "vm"},
	}}

	// Lookup (image, instance) form.
	if entry, ok := dc.Lookup("foo", ""); !ok || entry.Box != "foo" {
		t.Errorf("Lookup(foo, \"\") = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("foo", "inst1"); !ok || entry.Box != "foo" {
		t.Errorf("Lookup(foo, inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.Lookup("missing", ""); ok {
		t.Errorf("Lookup(missing, \"\") = (%+v, %v); want absent", entry, ok)
	}

	// LookupKey (raw deploy.yml key) form.
	if entry, ok := dc.LookupKey("foo/inst1"); !ok || entry.Box != "foo" {
		t.Errorf("LookupKey(foo/inst1) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("vm:arch"); !ok || entry.Target != "vm" {
		t.Errorf("LookupKey(vm:arch) = (%+v, %v); want present", entry, ok)
	}
	if entry, ok := dc.LookupKey("missing"); ok {
		t.Errorf("LookupKey(missing) = (%+v, %v); want absent", entry, ok)
	}

	// Empty / nil-map dc returns (zero, false).
	emptyDc := &BundleConfig{}
	if entry, ok := emptyDc.Lookup("foo", ""); ok {
		t.Errorf("Lookup on empty dc returned ok=true entry=%+v", entry)
	}
}

// TestSaveDeployState_AbortOnInvalidExistingFile pins the post-2026-05-16
// data-loss fix: when LoadBundleConfig returns an error (e.g. because
// the file fails validateDeployRequiresBox), saveDeployState MUST
// ABORT and leave the file byte-identical — not silently construct a
// fresh empty config and truncate the on-disk file.
//
// Pre-fix reproduction: `charly bundle add arch arch --disposable`
// against a deploy.yml whose pre-existing entries lacked the required
// `box:` field destroyed the entire file's content (provides section,
// other deploy entries) and wrote only the new disposable: true marker.
func TestSaveDeployState_AbortOnInvalidExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing deploy.yml that fails validateDeployRequiresBox —
	// `legacy-entry` is target:pod but lacks the required `box:`.
	initialYAML := `version: 2026.169.0004
provides:
    env:
        - name: SOME_URL
          value: http://example/api
          source: legacy-entry
deploy:
    legacy-entry:
        target: pod
    another-entry:
        target: pod
        box: another
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	initialBytes, _ := os.ReadFile(path)

	// Attempt to write the disposable flag for a brand-new entry. With
	// the pre-fix code, this would call LoadBundleConfig() → err →
	// discarded → dc = empty → entry.Disposable = true → SaveBundleConfig
	// truncates the file. With the post-fix code, the load error
	// propagates and saveDeployState aborts before any write.
	saveDeployState("newimage", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	})

	afterBytes, _ := os.ReadFile(path)
	if !bytes.Equal(initialBytes, afterBytes) {
		t.Errorf("saveDeployState mutated deploy.yml despite load-time validation error\n--- before ---\n%s\n--- after ---\n%s",
			initialBytes, afterBytes)
	}
}

// TestSaveDeployState_PersistsImageAndTargetForNewEntry pins the
// post-2026-05-16 require-image plumbing: when the caller passes
// Image/Target on a brand-new entry, both must land in deploy.yml
// alongside Disposable. Without this, the entry fails the require-image
// validator on the next load and bricks every subsequent `charly` invocation.
func TestSaveDeployState_PersistsImageAndTargetForNewEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.169.0004
existing-deploy:
    bundle:
        box: existing-image
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	saveDeployState("newimage", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "newimage",
		Target:        "pod",
	})

	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if dc == nil {
		t.Fatal("nil BundleConfig after reload")
	}

	if _, ok := dc.Bundle["existing-deploy"]; !ok {
		t.Error("existing-deploy entry was lost (merge failure)")
	}

	newEntry, ok := dc.Bundle["newimage"]
	if !ok {
		t.Fatal("newimage entry not added")
	}
	if newEntry.Box != "newimage" {
		t.Errorf("Image not persisted on new entry: got %q want %q", newEntry.Box, "newimage")
	}
	if newEntry.Target != "pod" {
		t.Errorf("Target not persisted on new entry: got %q want %q", newEntry.Target, "pod")
	}
	if newEntry.Disposable == nil || !*newEntry.Disposable {
		t.Error("Disposable not persisted on new entry")
	}
}

// TestSaveDeployState_DoesNotClobberExistingImageTarget pins the
// "only set when entry doesn't already declare" semantics: if a
// pre-existing entry already has box:/target:, a saveDeployState
// call with different Image/Target values MUST leave the existing
// values alone (operator authority over agent re-derivation).
func TestSaveDeployState_DoesNotClobberExistingImageTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `version: 2026.169.0004
existing:
    bundle:
        box: pinned-image-ref:1.2.3
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	saveDeployState("existing", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Box:           "would-clobber",
		Target:        "vm",
	})

	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	entry := dc.Bundle["existing"]
	if entry.Box != "pinned-image-ref:1.2.3" {
		t.Errorf("Image clobbered: got %q want %q", entry.Box, "pinned-image-ref:1.2.3")
	}
	if entry.Target != "pod" {
		t.Errorf("Target clobbered: got %q want %q", entry.Target, "pod")
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("Disposable not applied (this field SHOULD update)")
	}
}

// TestSaveBundleConfig_AtomicWriteSurvivesIntermediateFailure pins the
// tempfile + rename atomic-write guarantee: if the marshal step succeeds
// but the rename step fails (simulated by making the target path a
// directory), the prior on-disk file MUST remain intact.
//
// We can't easily inject a failure into os.Rename in a unit test, so
// this test exercises the happy path's atomicity properties (file mode,
// no .tmp leftovers) as a regression guard for the implementation shape.
func TestSaveBundleConfig_AtomicWriteLeavesNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dc := &BundleConfig{Bundle: map[string]BundleNode{
		"foo": {Target: "pod", Box: "foo"},
	}}
	if err := SaveBundleConfig(dc); err != nil {
		t.Fatalf("SaveBundleConfig: %v", err)
	}
	// No .tmp leftovers in the config dir.
	entries, err := os.ReadDir(filepath.Join(dir, "charly"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || (len(e.Name()) > 4 && e.Name()[:4] == ".dep") {
			if e.Name() != "deploy.yml" {
				t.Errorf("leftover tempfile: %s", e.Name())
			}
		}
	}
	// File mode is 0600 (matches the original os.WriteFile(0600) contract).
	info, err := os.Stat(filepath.Join(dir, "charly", "charly.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}

// TestBundleNode_DisposableFalseRoundTrip pins the *bool Disposable
// fix: an operator's explicit `disposable: false` must survive YAML
// unmarshal → re-marshal. With the prior `Disposable bool` +
// `omitempty` declaration, `false` was indistinguishable from "absent"
// at marshal time so the explicit lockdown intent was silently erased
// on the next saveDeployState. With *bool, nil=absent and &false=
// explicit lockdown both round-trip faithfully.
func TestBundleNode_DisposableFalseRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := `version: 2026.169.0004
locked-pod:
    bundle:
        box: foo
        disposable: false
open-pod:
    bundle:
        box: bar
        disposable: true
bare-pod:
    bundle:
        box: baz
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if dc == nil {
		t.Fatal("LoadBundleConfig returned nil")
	}
	locked := dc.Bundle["locked-pod"]
	if locked.Disposable == nil {
		t.Fatal("locked-pod: explicit `disposable: false` parsed as nil; should be &false")
	}
	if *locked.Disposable {
		t.Errorf("locked-pod: disposable = %v, want false", *locked.Disposable)
	}
	if locked.IsDisposable() {
		t.Error("locked-pod.IsDisposable() returned true despite explicit disposable: false")
	}

	open := dc.Bundle["open-pod"]
	if open.Disposable == nil || !*open.Disposable {
		t.Errorf("open-pod: disposable = %v, want &true", open.Disposable)
	}
	if !open.IsDisposable() {
		t.Error("open-pod.IsDisposable() returned false despite explicit disposable: true")
	}

	bare := dc.Bundle["bare-pod"]
	if bare.Disposable != nil {
		t.Errorf("bare-pod: disposable = %v, want nil (field absent in source)", bare.Disposable)
	}
	if bare.IsDisposable() {
		t.Error("bare-pod.IsDisposable() returned true for absent disposable field")
	}

	if err := SaveBundleConfig(dc); err != nil {
		t.Fatalf("save: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if !bytes.Contains(out, []byte("disposable: false")) {
		t.Errorf("re-serialized deploy.yml dropped explicit `disposable: false`:\n%s", string(out))
	}
	if !bytes.Contains(out, []byte("disposable: true")) {
		t.Errorf("re-serialized deploy.yml dropped explicit `disposable: true`:\n%s", string(out))
	}
}

// TestRemoveVmDeployEntry_SelectiveAndIdempotent pins the deploy-lifecycle
// cleanup primitive that `charly vm destroy` (vm.go) and `charly bundle del vm:<name>`
// (unified_targets_vm.go) rely on to remove a VM's deploy.yml entry on teardown
// — the inverse of the saveVmDeployState written on add. It proves the two
// load-bearing properties of the fix:
//
//  1. SELECTIVE removal — removing `vm:k3s-vm` strips ONLY that entry; sibling
//     VM entries (incl. a running, preemptible operator workstation) and pod
//     entries survive untouched. This is the operator-safety property: a
//     disposable bed's teardown can never collateral-remove the workstation.
//  2. IDEMPOTENCY — a second removal of the already-gone entry returns nil and
//     leaves the file valid + siblings intact. This is the config-layer half of
//     the "a config whose libvirt domain is already destroyed is STILL cleaned"
//     behavior (the other half being vm.go's now-non-fatal lookupDomain miss).
//
// Without the fix, `charly vm destroy` never called removeVmDeployEntry, so a
// disposable check-bed VM entry lingered in deploy.yml after every bed run.
func TestRemoveVmDeployEntry_SelectiveAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Seed: the disposable bed VM to remove, plus a running preemptible
	// operator workstation and an unrelated pod deploy that must both survive.
	initialYAML := `version: 2026.169.0004
vm:k3s-vm:
    bundle:
        vm: k3s-vm
        vm_state:
            ssh_port: 38067
            ssh_user: arch
vm:cachyos-gpu:
    bundle:
        vm: cachyos-gpu
    vm:cachyos-gpu-preemptible:
        preemptible:
            holds:
                - nvidia-gpu
web-app:
    bundle:
        box: web-app
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// (1) Selective removal of the disposable bed VM.
	if err := removeVmDeployEntry("vm:k3s-vm"); err != nil {
		t.Fatalf("removeVmDeployEntry(vm:k3s-vm): %v", err)
	}
	dc, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after removal: %v", err)
	}
	if _, ok := dc.LookupKey("vm:k3s-vm"); ok {
		t.Error("vm:k3s-vm still present after removeVmDeployEntry — entry not removed")
	}
	if _, ok := dc.LookupKey("vm:cachyos-gpu"); !ok {
		t.Error("vm:cachyos-gpu (operator workstation) was collateral-removed — selective-removal property violated")
	}
	if _, ok := dc.LookupKey("web-app"); !ok {
		t.Error("web-app pod deploy was collateral-removed — selective-removal property violated")
	}

	// (2) Idempotency: removing the already-gone entry is a clean no-op.
	if err := removeVmDeployEntry("vm:k3s-vm"); err != nil {
		t.Fatalf("idempotent re-removal of vm:k3s-vm errored: %v", err)
	}
	dc2, err := LoadBundleConfig()
	if err != nil {
		t.Fatalf("reload after idempotent re-removal: %v", err)
	}
	if _, ok := dc2.LookupKey("vm:cachyos-gpu"); !ok {
		t.Error("vm:cachyos-gpu disappeared after idempotent re-removal")
	}
	if _, ok := dc2.LookupKey("web-app"); !ok {
		t.Error("web-app disappeared after idempotent re-removal")
	}
}
