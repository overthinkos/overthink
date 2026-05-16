package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveDeployState_AbortOnInvalidExistingFile pins the post-2026-05-16
// data-loss fix: when LoadDeployConfig returns an error (e.g. because
// the file fails validateDeployRequiresImage), saveDeployState MUST
// ABORT and leave the file byte-identical — not silently construct a
// fresh empty config and truncate the on-disk file.
//
// Pre-fix reproduction: `ov deploy add archlinux archlinux --disposable`
// against a deploy.yml whose pre-existing entries lacked the required
// `image:` field destroyed the entire file's content (provides section,
// other deploy entries) and wrote only the new disposable: true marker.
func TestSaveDeployState_AbortOnInvalidExistingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-existing deploy.yml that fails validateDeployRequiresImage —
	// `legacy-entry` is target:pod but lacks the required `image:`.
	initialYAML := `provides:
    env:
        - name: SOME_URL
          value: http://example/api
          source: legacy-entry
deploy:
    legacy-entry:
        target: pod
    another-entry:
        target: pod
        image: another
`
	path := filepath.Join(dir, "ov", "deploy.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	initialBytes, _ := os.ReadFile(path)

	// Attempt to write the disposable flag for a brand-new entry. With
	// the pre-fix code, this would call LoadDeployConfig() → err →
	// discarded → dc = empty → entry.Disposable = true → SaveDeployConfig
	// truncates the file. With the post-fix code, the load error
	// propagates and saveDeployState aborts before any write.
	saveDeployState("newimage", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Image:         "newimage",
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
// validator on the next load and bricks every subsequent `ov` invocation.
func TestSaveDeployState_PersistsImageAndTargetForNewEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `deploy:
    existing-deploy:
        target: pod
        image: existing-image
`
	path := filepath.Join(dir, "ov", "deploy.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	saveDeployState("newimage", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Image:         "newimage",
		Target:        "pod",
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if dc == nil {
		t.Fatal("nil DeployConfig after reload")
	}

	if _, ok := dc.Deploy["existing-deploy"]; !ok {
		t.Error("existing-deploy entry was lost (merge failure)")
	}

	newEntry, ok := dc.Deploy["newimage"]
	if !ok {
		t.Fatal("newimage entry not added")
	}
	if newEntry.Image != "newimage" {
		t.Errorf("Image not persisted on new entry: got %q want %q", newEntry.Image, "newimage")
	}
	if newEntry.Target != "pod" {
		t.Errorf("Target not persisted on new entry: got %q want %q", newEntry.Target, "pod")
	}
	if !newEntry.Disposable {
		t.Error("Disposable not persisted on new entry")
	}
}

// TestSaveDeployState_DoesNotClobberExistingImageTarget pins the
// "only set when entry doesn't already declare" semantics: if a
// pre-existing entry already has image:/target:, a saveDeployState
// call with different Image/Target values MUST leave the existing
// values alone (operator authority over agent re-derivation).
func TestSaveDeployState_DoesNotClobberExistingImageTarget(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initialYAML := `deploy:
    existing:
        target: pod
        image: pinned-image-ref:1.2.3
`
	path := filepath.Join(dir, "ov", "deploy.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	saveDeployState("existing", "", SaveDeployStateInput{
		SetDisposable: true,
		Disposable:    true,
		Image:         "would-clobber",
		Target:        "vm",
	})

	dc, err := LoadDeployConfig()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	entry := dc.Deploy["existing"]
	if entry.Image != "pinned-image-ref:1.2.3" {
		t.Errorf("Image clobbered: got %q want %q", entry.Image, "pinned-image-ref:1.2.3")
	}
	if entry.Target != "pod" {
		t.Errorf("Target clobbered: got %q want %q", entry.Target, "pod")
	}
	if !entry.Disposable {
		t.Error("Disposable not applied (this field SHOULD update)")
	}
}

// TestSaveDeployConfig_AtomicWriteSurvivesIntermediateFailure pins the
// tempfile + rename atomic-write guarantee: if the marshal step succeeds
// but the rename step fails (simulated by making the target path a
// directory), the prior on-disk file MUST remain intact.
//
// We can't easily inject a failure into os.Rename in a unit test, so
// this test exercises the happy path's atomicity properties (file mode,
// no .tmp leftovers) as a regression guard for the implementation shape.
func TestSaveDeployConfig_AtomicWriteLeavesNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ov"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dc := &DeployConfig{Deploy: map[string]DeploymentNode{
		"foo": {Target: "pod", Image: "foo"},
	}}
	if err := SaveDeployConfig(dc); err != nil {
		t.Fatalf("SaveDeployConfig: %v", err)
	}
	// No .tmp leftovers in the config dir.
	entries, err := os.ReadDir(filepath.Join(dir, "ov"))
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
	info, err := os.Stat(filepath.Join(dir, "ov", "deploy.yml"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("file mode = %o; want 0600", info.Mode().Perm())
	}
}
