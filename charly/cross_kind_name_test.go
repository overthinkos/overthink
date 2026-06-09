package main

// cross_kind_name_test.go — locks in the cross-kind name reuse policy
// introduced 2026-05. The same identifier (e.g. charly-cachyos) MAY exist
// simultaneously across multiple namespaces:
//
//   - layer (under layers/<name>/)
//   - box: entry
//   - pod: entry
//   - vm: entry
//   - k8s: entry
//   - local: entry
//   - deployment: entry
//
// The unified loader does NOT enforce global uniqueness across these
// namespaces — uniqueness is scoped to each kind. charly verbs disambiguate
// by command context: `charly box build charly-cachyos` reaches into the
// box: map, `charly vm create charly-cachyos` reaches into the vm: map, and
// so on.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossKindNameReuse_LoaderAcceptsAllKinds — write an charly.yml
// with the SAME identifier `charly-cachyos` under every kind-keyed map, plus
// a layer at layers/charly-cachyos/. LoadUnified must accept it without a
// uniqueness error.
func TestCrossKindNameReuse_LoaderAcceptsAllKinds(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "layers", "charly-cachyos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layers", "charly-cachyos", "candy.yml"),
		[]byte("rpm:\n  packages: [example]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgYAML := `version: 2026.160.1301
defaults:
  registry: ghcr.io/example
  build: [rpm]

box:
  charly-cachyos:
    base: fedora
    candy: [charly-cachyos]

pod:
  charly-cachyos:
    box: charly-cachyos

vm:
  charly-cachyos:
    source:
      kind: cloud_image
      url: https://example.invalid/img.qcow2

local:
  charly-cachyos:
    candy: [charly-cachyos]

deploy:
  charly-cachyos:
    target: local
    local: charly-cachyos
    host: local
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(cfgYAML), 0o644); err != nil {
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
	if _, present := uf.Image["charly-cachyos"]; !present {
		t.Error("image.charly-cachyos missing")
	}
	if _, present := uf.Pod["charly-cachyos"]; !present {
		t.Error("pod.charly-cachyos missing")
	}
	if _, present := uf.VM["charly-cachyos"]; !present {
		t.Error("vm.charly-cachyos missing")
	}
	if _, present := uf.Local["charly-cachyos"]; !present {
		t.Error("local.charly-cachyos missing")
	}
	if uf.Deploy == nil {
		t.Fatal("deployments section missing")
	}
	if _, present := uf.Deploy["charly-cachyos"]; !present {
		t.Error("deployment.charly-cachyos missing")
	}
}

// TestCrossKindNameReuse_RetiredKeysRejected — the load-time hard
// errors for the THREE retired CachyOS-deployment keys, all pointing
// at the consolidated migration command:
//   - deployment.qc           (pre-2026-05-05 form)
//   - deployment.cachyos-dx   (post-2026-05-05, pre-2026-05 polymorphism cutover form)
//   - local.cachyos-dx        (kind:local namespace; same vintage)
func TestCrossKindNameReuse_RetiredKeysRejected(t *testing.T) {
	cases := []struct {
		name     string
		cfgYAML  string
		mustHint string
	}{
		{
			name: "deployment.qc",
			cfgYAML: `version: 2026.160.1301
deploy:
  qc:
    target: local
    host: local
    local: charly-cachyos
`,
			mustHint: "charly migrate",
		},
		{
			name: "deployment.cachyos-dx",
			cfgYAML: `version: 2026.160.1301
deploy:
  cachyos-dx:
    target: local
    host: local
    local: charly-cachyos
`,
			mustHint: "charly migrate",
		},
		{
			name: "local.cachyos-dx",
			cfgYAML: `version: 2026.160.1301
local:
  cachyos-dx:
    candy: [example]
`,
			mustHint: "charly migrate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(tc.cfgYAML), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := LoadUnified(dir)
			if err == nil {
				t.Fatalf("expected load-time error for retired key %s, got nil", tc.name)
			}
			if got := err.Error(); !crossKindContains(got, tc.mustHint) {
				t.Errorf("error message must point at %q, got: %q", tc.mustHint, got)
			}
		})
	}
}

// TestMigrateCharlyCachyos_Idempotent — running the consolidated migration
// twice produces byte-identical output on the second pass. The
// migration handles BOTH legacy keys (qc, cachyos-dx) AND moves the
// matching kind:local template name. Migration is opportunistic per
// file (missing files are not errors).
func TestMigrateCharlyCachyos_Idempotent(t *testing.T) {
	dir := t.TempDir()
	deployYml := `# Top-level comment
deploy:
    # qc — this CachyOS workstation
    qc:
        target: local
        local: cachyos-dx

    # cachyos-dx — second-stage legacy form
    cachyos-dx:
        target: local
        local: cachyos-dx
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(deployYml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateCharlyCachyos(dir, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !crossKindContains(string(got), "charly-cachyos:") {
		t.Errorf("expected → charly-cachyos rename; got:\n%s", got)
	}
	if crossKindContains(string(got), "\n    qc:\n") {
		t.Errorf("residual `qc:` deployment key after rename:\n%s", got)
	}
	if crossKindContains(string(got), "\n    cachyos-dx:\n") {
		t.Errorf("residual `cachyos-dx:` deployment key after rename:\n%s", got)
	}
	if crossKindContains(string(got), "local: cachyos-dx") {
		t.Errorf("residual `local: cachyos-dx` cross-reference after rename:\n%s", got)
	}
	first := string(got)

	// Second run — should be byte-identical (idempotent).
	if _, err := MigrateCharlyCachyos(dir, false); err != nil {
		t.Fatalf("second run: %v", err)
	}
	got2, _ := os.ReadFile(path)
	if string(got2) != first {
		t.Errorf("idempotency violated; first run:\n%s\n\nsecond run:\n%s", first, got2)
	}
}

// crossKindContains is a tiny local substring helper used only by this
// test file. The `contains` symbol is taken by charly/registry.go.
func crossKindContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
