package main

// cross_kind_name_test.go — locks in the cross-kind name reuse policy under
// the unified node-form model. The boundary moved with the node-form cutover:
//
//   - ACROSS SEPARATE discovered documents (files), the SAME identifier (e.g.
//     `redis`) MAY name a box in one file AND a candy in another — they are
//     distinct documents that register into distinct internal maps.
//   - WITHIN ONE document, every top-level node name is GLOBALLY UNIQUE — a
//     box `x` and a local/vm/candy `x` both flatten to the one top-level YAML
//     key `x: {…}`, so they COLLIDE: yaml merges the two `x:` mappings into a
//     single node carrying two entity discriminators, which the closed #NodeDoc
//     schema rejects (a node has exactly one discriminator). The loader fails.
//
// charly verbs still disambiguate cross-FILE reuse by command context:
// `charly box build redis` reaches the box document, the candy resolver reaches
// the candy directory.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCrossKindNameReuse_LoaderAcceptsAllKinds — the SAME name `redis` names a
// box in box/redis/charly.yml AND a candy in candy/redis/charly.yml. Because
// they are SEPARATE discovered documents, LoadUnified accepts the reuse and
// registers each into its own map. Within ONE document, however, a duplicate
// top-level node name collides and the loader rejects it.
func TestCrossKindNameReuse_LoaderAcceptsAllKinds(t *testing.T) {
	// --- Cross-FILE reuse: accepted. ---
	dir := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(dir, "charly.yml"), `version: 2026.173.1742
defaults:
  registry: ghcr.io/example
discover:
  - path: box
    recursive: true
  - path: candy
    recursive: true
`)
	// A box named `redis` in its own discovered document (node-form).
	must(filepath.Join(dir, "box", "redis", "charly.yml"), `redis:
  candy:
    base: fedora
`)
	// A candy ALSO named `redis` in a SEPARATE discovered document. Node-form:
	// only SCALARS live in the `candy:` value — its package collection and each
	// plan step are CHILD nodes (`redis-package:` / `redis-step-N:`).
	must(filepath.Join(dir, "candy", "redis", "charly.yml"), `redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
  redis-package:
    package:
      - redis
  redis-step-0:
    check: the binary exists
    file: /usr/bin/redis-server
`)

	uf, ok, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified rejected cross-FILE name reuse: %v", err)
	}
	if !ok || uf == nil {
		t.Fatal("LoadUnified returned ok=false")
	}
	cfg := uf.ProjectConfig()
	if _, present := cfg.Box["redis"]; !present {
		t.Errorf("box.redis missing; boxes present: %v", boxConfigKeys(cfg))
	}
	cands, err := uf.ProjectCandies(dir)
	if err != nil {
		t.Fatalf("ProjectCandies: %v", err)
	}
	if cands["redis"] == nil {
		t.Errorf("candy.redis missing; got %d candies", len(cands))
	}

	// --- Within ONE document: duplicate top-level name rejected. ---
	dir2 := t.TempDir()
	dupDoc := `version: 2026.173.1742
redis:
  candy:
    base: fedora
redis:
  local:
    candy: [redis]
`
	if err := os.WriteFile(filepath.Join(dir2, "charly.yml"), []byte(dupDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadUnified(dir2); err == nil {
		t.Fatal("LoadUnified accepted a duplicate top-level node name within one document; want a collision error")
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
			cfgYAML: `version: 2026.173.1742
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
			cfgYAML: `version: 2026.173.1742
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
			cfgYAML: `version: 2026.173.1742
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
