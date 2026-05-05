package main

// cross_kind_name_test.go — locks in the cross-kind name reuse policy
// introduced 2026-05. The same identifier (e.g. cachyos-dx) MAY exist
// simultaneously across multiple namespaces:
//
//   - layer (under layers/<name>/)
//   - image: entry
//   - pod: entry
//   - vm: entry
//   - k8s: entry
//   - local: entry
//   - deployment: entry
//
// The unified loader does NOT enforce global uniqueness across these
// namespaces — uniqueness is scoped to each kind. ov verbs disambiguate
// by command context: `ov image build cachyos-dx` reaches into the
// image: map, `ov vm create cachyos-dx` reaches into the vm: map, and
// so on.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossKindNameReuse_LoaderAcceptsAllKinds — write an overthink.yml
// with the SAME identifier `cachyos-dx` under every kind-keyed map, plus
// a layer at layers/cachyos-dx/. LoadUnified must accept it without a
// uniqueness error.
func TestCrossKindNameReuse_LoaderAcceptsAllKinds(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "layers", "cachyos-dx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layers", "cachyos-dx", "layer.yml"),
		[]byte("rpm:\n  packages: [example]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overthink := `version: 4
defaults:
  registry: ghcr.io/example
  build: [rpm]

image:
  cachyos-dx:
    base: fedora
    layers: [cachyos-dx]

pod:
  cachyos-dx:
    image: cachyos-dx

vm:
  cachyos-dx:
    source:
      kind: cloud_image
      url: https://example.invalid/img.qcow2

local:
  cachyos-dx:
    layers: [cachyos-dx]

deployment:
  cachyos-dx:
    target: local
    local: cachyos-dx
    host: local
`
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(overthink), 0o644); err != nil {
		t.Fatal(err)
	}

	uf, ok, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified rejected cross-kind name reuse: %v", err)
	}
	if !ok || uf == nil {
		t.Fatal("LoadUnified returned ok=false")
	}
	// Every kind-keyed map must contain the shared name.
	if _, present := uf.Images["cachyos-dx"]; !present {
		t.Error("image.cachyos-dx missing")
	}
	if _, present := uf.Pod["cachyos-dx"]; !present {
		t.Error("pod.cachyos-dx missing")
	}
	if _, present := uf.VM["cachyos-dx"]; !present {
		t.Error("vm.cachyos-dx missing")
	}
	if _, present := uf.Local["cachyos-dx"]; !present {
		t.Error("local.cachyos-dx missing")
	}
	if uf.Deployments == nil {
		t.Fatal("deployments section missing")
	}
	if _, present := uf.Deployments.Images["cachyos-dx"]; !present {
		t.Error("deployment.cachyos-dx missing")
	}
}

// TestCrossKindNameReuse_QcDeploymentKeyRejected — the load-time hard
// error for residual `qc:` deployment keys, paired with the migration
// command remediation hint.
func TestCrossKindNameReuse_QcDeploymentKeyRejected(t *testing.T) {
	dir := t.TempDir()
	overthink := `version: 4
deployment:
  qc:
    target: local
    host: local
    local: cachyos-dx
`
	if err := os.WriteFile(filepath.Join(dir, "overthink.yml"), []byte(overthink), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("expected load-time error for residual `qc:` key, got nil")
	}
	if got := err.Error(); !crossKindContains(got, "qc-rename") {
		t.Errorf("error message must point at `ov migrate qc-rename`, got: %q", got)
	}
}

// TestMigrateQcRename_Idempotent — running the migration twice produces
// byte-identical output on the second pass, and a `qc:` entry becomes
// `cachyos-dx:`. The migration is opportunistic per file (missing files
// are not errors) and rewrites a deliberate set of comment idioms.
func TestMigrateQcRename_Idempotent(t *testing.T) {
	dir := t.TempDir()
	deployYml := `# Top-level comment
deployment:
    # qc — this CachyOS workstation
    qc:
        target: local
        local: cachyos-dx
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(deployYml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateQcRename(dir, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !crossKindContains(string(got), "cachyos-dx:") {
		t.Errorf("expected qc → cachyos-dx rename; got:\n%s", got)
	}
	if crossKindContains(string(got), "\n    qc:\n") {
		t.Errorf("residual `qc:` deployment key after rename:\n%s", got)
	}
	first := string(got)

	// Second run — should be byte-identical (idempotent).
	if _, err := MigrateQcRename(dir, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != first {
		t.Errorf("idempotency violated; first run:\n%s\n\nsecond run:\n%s", first, got2)
	}
}

// crossKindContains is a tiny local substring helper used only by this
// test file. The `contains` symbol is taken by ov/registry.go.
func crossKindContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
