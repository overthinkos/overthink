package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

const legacyCandyDoc = `candy:
  name: redis
  version: "2026.150.0000"
  description: in-memory store
  status: working
  candy: [supervisord]
  require: [python]
  env:
    REDIS_DATA: /var/lib/redis
  package: [redis, redis-cli]
  service:
    - name: redis
      exec: /usr/bin/redis-server
  plan:
    - check: the binary exists
      id: redis-bin
      file: /usr/bin/redis-server
`

// TestMigrateUnifiedNode_CandyRoundTrip proves the forward migration produces
// node-form that the loader decodes into EXACTLY the CandyYAML the legacy
// kind-first decode produces — and that the migration is idempotent.
func TestMigrateUnifiedNode_CandyRoundTrip(t *testing.T) {
	// Legacy decode: the candy VALUE → CandyYAML (the "want").
	var legacyDoc yaml.Node
	if err := yaml.Unmarshal([]byte(legacyCandyDoc), &legacyDoc); err != nil {
		t.Fatal(err)
	}
	candyVal := mapValue(mappingRoot(&legacyDoc), "candy")
	if candyVal == nil {
		t.Fatal("legacy doc has no candy: key")
	}
	var want CandyYAML
	if err := decodeEntityViaCUE(candyVal, reflect.TypeOf(CandyYAML{}), &want, "legacy"); err != nil {
		t.Fatalf("legacy decode: %v", err)
	}

	// Migrate the legacy doc to node-form.
	var mig yaml.Node
	if err := yaml.Unmarshal([]byte(legacyCandyDoc), &mig); err != nil {
		t.Fatal(err)
	}
	if !migrateUnifiedNodeDoc(&mig) {
		t.Fatal("migration reported no change on a legacy candy doc")
	}

	// The migrated node-form must load into the same CandyYAML.
	_, nodes, err := parseNodeTree(&mig)
	if err != nil {
		t.Fatalf("parse migrated node-form: %v", err)
	}
	if len(nodes) != 1 || nodes[0].name != "redis" || nodes[0].disc != "candy" {
		t.Fatalf("migrated to %d nodes; first=%+v", len(nodes), nodes[0])
	}
	_, ic, err := buildCandy(nodes[0])
	if err != nil {
		t.Fatalf("buildCandy on migrated node: %v", err)
	}
	if !reflect.DeepEqual(ic.CandyYAML, want) {
		t.Fatalf("migrated candy != legacy candy\n migrated: %#v\n legacy:   %#v", ic.CandyYAML, want)
	}

	// Idempotency: migrating the already-node-form doc is a no-op.
	if migrateUnifiedNodeDoc(&mig) {
		t.Error("migration not idempotent — changed an already-node-form doc")
	}
}

// TestMigrateUnifiedNode_ProjectLoads migrates a legacy project (a pod deploy
// workload + a disposable pod bed carrying a sub-entity member + a standalone
// vm:) and proves the migrated node-form LOADS into the right structures: a pod
// deployment, a disposable pod bed whose member folds to a Nested pod-in-pod,
// and a standalone VM template. A disposable bundle IS a check bed, so it must
// carry its own workload target (a box) — a box-less disposable group has no
// inferred target and is rejected by validateCheckBeds. Because the bed carries
// a box, its sub-entity children deploy INSIDE its venue → Nested (a box-less
// group's children would be alongside Peers; see buildBundleNode).
func TestMigrateUnifiedNode_ProjectLoads(t *testing.T) {
	dir := t.TempDir()
	legacy := `version: "` + latestSchemaVersion.String() + `"
deploy:
  web:
    target: pod
    box: coder
  shop:
    target: pod
    box: shop-app
    disposable: true
    nested:
      chrome:
        target: pod
        box: chrome-headless
vm:
  pg:
    source: {kind: cloud_image, url: "http://example/img"}
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateUnifiedNode(dir, false); err != nil {
		t.Fatalf("MigrateUnifiedNode: %v", err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified migrated project: %v", err)
	}
	web, ok := uf.Bundle["web"]
	if !ok || web.Target != "pod" || web.Box != "coder" {
		t.Errorf("web deployment wrong: ok=%v %+v", ok, web)
	}
	shop, ok := uf.Bundle["shop"]
	if !ok || shop.Disposable == nil || !*shop.Disposable || shop.Children["chrome"] == nil {
		t.Errorf("shop bed wrong: ok=%v disposable=%v nested=%v", ok, shop.Disposable, shop.Children)
	}
	if uf.VM["pg"] == nil {
		t.Errorf("standalone vm pg not loaded")
	}
}

// TestMigrateUnifiedNode_CrossKindNameCollision reproduces the cachyos migration
// bug: the kind-keyed format legally reused one name across SEPARATE maps (a
// `deploy:` bundle + a `local:` template both named `charly-cachyos`, a `deploy:`
// bundle + a `vm:` template both named `cachyos-gpu`), but a node-form document's
// top-level names are GLOBALLY UNIQUE. The buggy migrator emitted two
// `charly-cachyos:` top-level keys, each with an identically-named `…-env` child;
// CUE unified them and the env lists (length 1 vs 2) conflicted. The fix keeps the
// bundle's bare name, renames the colliding template `<name>-<kind>`, and rewrites
// the bundle's cross-ref. This test FAILS against the un-fixed migrator (duplicate
// top-level key + LoadUnified CUE conflict) and PASSES after it.
func TestMigrateUnifiedNode_CrossKindNameCollision(t *testing.T) {
	dir := t.TempDir()
	legacy := `version: "` + latestSchemaVersion.String() + `"
deploy:
  charly-cachyos:
    target: local
    local: charly-cachyos
    host: local
    disposable: true
    env: [EDITOR=nvim]
  cachyos-gpu:
    target: vm
    vm: cachyos-gpu
    env: [EDITOR=nvim, PAGER=less]
local:
  charly-cachyos:
    description: CachyOS workstation profile
    candy: [wheel-nopasswd]
    env: [EDITOR=nvim, PAGER=less]
vm:
  cachyos-gpu:
    source: {kind: cloud_image, url: "http://example/img"}
`
	path := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateUnifiedNode(dir, false); err != nil {
		t.Fatalf("MigrateUnifiedNode: %v", err)
	}

	// 1) The migrated document must carry NO duplicate top-level node names.
	migrated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var migDoc yaml.Node
	if err := yaml.Unmarshal(migrated, &migDoc); err != nil {
		t.Fatalf("parse migrated yaml: %v", err)
	}
	root := mappingRoot(&migDoc)
	if root == nil {
		t.Fatal("migrated doc has no mapping root")
	}
	seen := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		seen[root.Content[i].Value]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("duplicate top-level node %q appears %d times after migration\n--- migrated ---\n%s", name, n, migrated)
		}
	}
	// The user-facing bundle keeps the bare name; the colliding templates are
	// renamed `<name>-<kind>`.
	for _, want := range []string{"charly-cachyos", "cachyos-gpu", "charly-cachyos-local", "cachyos-gpu-vm"} {
		if seen[want] != 1 {
			t.Errorf("expected exactly one top-level %q, got %d\n--- migrated ---\n%s", want, seen[want], migrated)
		}
	}

	// 2) The migrated project loads (no CUE conflict) with the cross-refs rewritten
	// to the renamed templates and both env lists preserved on distinct entities.
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified migrated project (cross-kind collision unresolved?): %v", err)
	}
	bundle, ok := uf.Bundle["charly-cachyos"]
	if !ok {
		t.Fatalf("deploy bundle charly-cachyos missing; deploys=%v", collisionKeys(uf.Bundle))
	}
	if bundle.Local != "charly-cachyos-local" {
		t.Errorf("bundle cross-ref not rewritten: got local=%q want %q", bundle.Local, "charly-cachyos-local")
	}
	if !reflect.DeepEqual(bundle.Env, []string{"EDITOR=nvim"}) {
		t.Errorf("bundle env clobbered by unification: got %v want [EDITOR=nvim]", bundle.Env)
	}
	tmpl, ok := uf.Local["charly-cachyos-local"]
	if !ok {
		t.Fatalf("renamed local template charly-cachyos-local missing; locals=%v", collisionKeys(uf.Local))
	}
	if !reflect.DeepEqual(tmpl.Env, []string{"EDITOR=nvim", "PAGER=less"}) {
		t.Errorf("template env clobbered by unification: got %v want [EDITOR=nvim PAGER=less]", tmpl.Env)
	}
	gpu, ok := uf.Bundle["cachyos-gpu"]
	if !ok || gpu.Vm != "cachyos-gpu-vm" {
		t.Errorf("cachyos-gpu bundle cross-ref not rewritten: ok=%v vm=%q want cachyos-gpu-vm", ok, gpu.Vm)
	}
	if uf.VM["cachyos-gpu-vm"] == nil {
		t.Errorf("renamed vm template cachyos-gpu-vm missing; vms=%v", collisionKeys(uf.VM))
	}

	// 3) Re-migrating the already-node-form project is a no-op (idempotent).
	if _, err := MigrateUnifiedNode(dir, false); err != nil {
		t.Fatalf("second MigrateUnifiedNode: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(migrated) {
		t.Errorf("migration not idempotent on the resolved node-form\n--- first ---\n%s\n--- second ---\n%s", migrated, again)
	}
}

// collisionKeys returns the keys of a map[string]V for failure messages.
func collisionKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// TestMigrateUnifiedNode_TemplatelessLocalKeepsDisc proves the venue-from-position
// regression fix: a legacy nested `target: local` deploy with NO cross-ref (an
// add_candy-only overlay, the check-arch-vm.arch-host shape) must migrate to a
// `local:` discriminator, NOT a bare `bundle:`. Dropping `target: local` under
// `bundle:` would leave a group node (empty target) that classifyTarget defaults
// to a pod — routing a guest-shell check to `podman exec` (podman: command not
// found in the VM guest). A cross-ref'd root (`target: vm` + `vm: arch`) still
// migrates to `bundle:` (target inferred from the cross-ref).
func TestMigrateUnifiedNode_TemplatelessLocalKeepsDisc(t *testing.T) {
	legacy := `version: "` + latestSchemaVersion.String() + `"
check:
  check-arch-vm:
    target: vm
    vm: arch
    disposable: true
    nested:
      arch-host:
        target: local
        disposable: true
        add_candy: ['@github.com/overthinkos/overthink/candy/direnv:v2026.166.1222']
        plan:
          - check: command=direnv --version
            id: ah-direnv-version
            command: "direnv --version"
            context: [runtime]
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(legacy), &doc); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if !migrateUnifiedNodeDoc(&doc) {
		t.Fatal("migrateUnifiedNodeDoc reported no change")
	}
	root := rootMappingNode(&doc)
	if root == nil {
		t.Fatal("migrated doc has no mapping root")
	}
	bedVal := findMappingValue(root, "check-arch-vm")
	if bedVal == nil {
		t.Fatal("check-arch-vm missing after migration")
	}
	// The cross-ref'd VM root keeps `bundle:` (vm cross-ref infers the target).
	if findMappingValue(bedVal, "bundle") == nil {
		t.Errorf("check-arch-vm should carry a `bundle:` disc (cross-ref vm: arch); first keys: %v", firstKeysOf(bedVal))
	}
	// The template-less nested local child must carry `local:`, never `bundle:`.
	childVal := findMappingValue(bedVal, "arch-host")
	if childVal == nil {
		t.Fatalf("nested child arch-host missing; check-arch-vm keys: %v", firstKeysOf(bedVal))
	}
	if findMappingValue(childVal, "bundle") != nil {
		t.Errorf("nested arch-host migrated to `bundle:` (group→pod regression); want `local:`; keys: %v", firstKeysOf(childVal))
	}
	if findMappingValue(childVal, "local") == nil {
		t.Errorf("nested arch-host should carry a `local:` disc; keys: %v", firstKeysOf(childVal))
	}
}

// firstKeysOf returns the mapping keys of a yaml mapping node (for assertion
// messages).
func firstKeysOf(m *yaml.Node) []string {
	var ks []string
	if m == nil {
		return ks
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		ks = append(ks, m.Content[i].Value)
	}
	return ks
}
