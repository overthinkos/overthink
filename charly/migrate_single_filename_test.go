package main

import (
	migrate "github.com/overthinkos/overthink/candy/plugin-migrate"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateSingleFilename_FullProject exercises the whole single-filename
// migration: box files split into box/<name>/charly.yml, candy manifests rename,
// per-kind files fold into charly.yml, the default-matching build.yml import is
// dropped + the file deleted, and discover: is rewritten to [box, candy].
func TestMigrateSingleFilename_FullProject(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: 2026.159.1912
import:
  - box.yml
  - base.yml
  - vm.yml
  - build.yml
discover:
  - path: candy
    recursive: true
    manifest: candy.yml
defaults:
  registry: ghcr.io/example
`)
	writeFixture(t, dir, "box.yml", `version: 2026.159.1912
box:
  alpha:
    base: fedora
    candy: [foo]
  beta:
    base: fedora
`)
	writeFixture(t, dir, "base.yml", `version: 2026.143.0844
box:
  arch:
    version: 2026.144.1443
    base: quay.io/archlinux/archlinux:base
    distro: [arch]
`)
	writeFixture(t, dir, "vm.yml", `version: 2026.159.1912
vm:
  test-vm:
    ram: 4G
`)
	writeFixture(t, dir, "candy/foo/candy.yml", `candy:
  name: foo
  version: 2026.144.1443
`)
	// build.yml whose build vocabulary matches the embedded default → dropped +
	// deleted (semantic compare: the embedded charly.yml parses to the same
	// distro/builder/init/resource maps). The frozen legacy YAML fixture is the
	// exact YAML form the embedded charly.yml is data-equivalent to.
	legacyVocab, rerr := os.ReadFile("testdata/embedded_legacy.yml")
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.yml"), legacyVocab, 0o644); err != nil {
		t.Fatal(err)
	}

	// The build.yml-matches-embedded-vocab verdict is HOST-PRELIFTED by core (the
	// migrator no longer parses it); compute it here exactly as the in-core shim's
	// prelift does, then drive the prelift-aware migrator.
	matchesEmbed := localBuildMatchesEmbeddedVocab(legacyVocab)
	if _, err := migrate.MigrateSingleFilenameWithEmbed(dir, "", false, matchesEmbed); err != nil {
		t.Fatalf("migrate.MigrateSingleFilenameWithEmbed: %v", err)
	}
	// unified-node is the next forward chain step: it rewrites every kind-keyed
	// entity (the split box: docs + the folded root vm:) into the name-first
	// node-form the loader requires.
	if _, err := migrate.MigrateUnifiedNode(dir, false); err != nil {
		t.Fatalf("unified-node: %v", err)
	}
	// box-to-candy (EDGE-INHERIT cutover D): the `box:` KIND merged INTO `candy:`,
	// so every node-form `<name>: {box: …}` IMAGE becomes `<name>: {candy: …}` (the
	// base:/from: marker keeps it an image). Without this step the migrated boxes
	// carry the removed `box:` discriminator and the loader rejects them.
	if _, err := migrate.MigrateBoxToCandy(&MigrateContext{Dir: dir}); err != nil {
		t.Fatalf("box-to-candy: %v", err)
	}
	// calver-schema is the chain's final step; it re-stamps the root to HEAD so the
	// migrated tree loads (single-filename itself does no version bump).
	if _, err := migrate.MigrateCalverSchema(dir, "", LatestSchemaVersion(), false); err != nil {
		t.Fatalf("calver-schema: %v", err)
	}

	// Boxes split into per-box dirs (node-form `<name>: {candy: …}` image docs).
	for _, name := range []string{"alpha", "beta", "arch"} {
		p := filepath.Join(dir, "box", name, "charly.yml")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("box/%s/charly.yml missing: %v", name, err)
			continue
		}
		if !strings.Contains(string(data), "candy:") || !strings.Contains(string(data), name+":") {
			t.Errorf("box/%s/charly.yml not a name-first node-form candy: image doc:\n%s", name, data)
		}
	}
	// Box files deleted.
	for _, f := range []string{"box.yml", "base.yml", "vm.yml", "build.yml"} {
		if fileExists(filepath.Join(dir, f)) {
			t.Errorf("%s should have been removed", f)
		}
	}
	// Candy manifest renamed.
	if !fileExists(filepath.Join(dir, "candy", "foo", "charly.yml")) {
		t.Error("candy/foo/charly.yml missing")
	}
	if fileExists(filepath.Join(dir, "candy", "foo", "candy.yml")) {
		t.Error("candy/foo/candy.yml should have been renamed")
	}
	// Root charly.yml: vm folded, build.yml + per-kind imports dropped, discover rewritten.
	root, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	rs := string(root)
	if !strings.Contains(rs, "vm:") {
		t.Errorf("vm: not folded into charly.yml:\n%s", rs)
	}
	for _, dropped := range []string{"box.yml", "base.yml", "vm.yml", "build.yml"} {
		if strings.Contains(rs, "- "+dropped) {
			t.Errorf("import still references %s:\n%s", dropped, rs)
		}
	}
	if !strings.Contains(rs, "path: box") || !strings.Contains(rs, "path: candy") {
		t.Errorf("discover not rewritten to [box, candy]:\n%s", rs)
	}

	// The migrated tree LOADS + discovers both boxes and candies.
	uf, present, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified after migrate: %v", err)
	}
	if !present {
		t.Fatal("charly.yml not present after migrate")
	}
	for _, name := range []string{"alpha", "beta", "arch"} {
		if _, ok := uf.Box[name]; !ok {
			t.Errorf("discovered box %q missing after migrate", name)
		}
	}
	if _, ok := uf.Candy["foo"]; !ok {
		t.Error("discovered candy foo missing after migrate")
	}

	// Idempotency: a second run changes nothing.
	changed, err := migrate.MigrateSingleFilename(dir, "", false)
	if err != nil {
		t.Fatalf("second migrate.MigrateSingleFilename: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("second run not idempotent: %v", changed)
	}
}

// TestMigrateSingleFilename_CandyOnlyDiscoverPreserved proves the migration is
// idempotent on a project that deliberately discovers ONLY candy (it owns no
// boxes — e.g. the main repo after the box inversion). rewriteDiscover must NOT
// clobber the already-single-filename candy-only discover back to [box, candy].
func TestMigrateSingleFilename_CandyOnlyDiscoverPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: 2026.164.0004
discover:
  - path: candy
    recursive: true
defaults:
  registry: ghcr.io/example
`)
	writeFixture(t, dir, "candy/foo/charly.yml", `candy:
  name: foo
  version: 2026.144.1443
`)

	changed, err := migrate.MigrateSingleFilename(dir, "", false)
	if err != nil {
		t.Fatalf("migrate.MigrateSingleFilename: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("candy-only project is not a no-op (rewriteDiscover clobbered the discover?): %v", changed)
	}
	root, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	rs := string(root)
	if strings.Contains(rs, "path: box") {
		t.Errorf("candy-only discover was clobbered to include a box path:\n%s", rs)
	}
	if !strings.Contains(rs, "path: candy") {
		t.Errorf("candy discover path was lost:\n%s", rs)
	}
}

// TestMigrateSingleFilename_InlineBoxSplit covers the bootc case: an inline box:
// map in charly.yml is split into box/<name>/charly.yml and removed from the root.
func TestMigrateSingleFilename_InlineBoxSplit(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: 2026.159.1912
discover:
  - path: candy
    manifest: candy.yml
box:
  inlinebox:
    base: quay.io/fedora/fedora:43
    distro: [fedora:43, fedora]
`)
	if _, err := migrate.MigrateSingleFilename(dir, "", false); err != nil {
		t.Fatalf("migrate.MigrateSingleFilename: %v", err)
	}
	if !fileExists(filepath.Join(dir, "box", "inlinebox", "charly.yml")) {
		t.Error("box/inlinebox/charly.yml missing (inline split failed)")
	}
	root, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	// The inline box: map must be gone from the root (only the discover box spec
	// path may mention "box").
	if strings.Contains(string(root), "inlinebox") {
		t.Errorf("inline box not removed from charly.yml:\n%s", root)
	}
}

// TestMigrateSingleFilename_CustomBuildYmlKept verifies a CUSTOMIZED build.yml
// (not byte-matching the embedded default) is LEFT imported — it overrides the
// embed and dropping it would lose the override.
func TestMigrateSingleFilename_CustomBuildYmlKept(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", `version: 2026.159.1912
import:
  - build.yml
discover:
  - path: candy
    manifest: candy.yml
`)
	customVocab := `distro:
  mydistro:
    bootstrap:
      install_cmd: custom install
`
	writeFixture(t, dir, "build.yml", customVocab)
	// Host-prelift the verdict (a customized vocab → false → kept), as the shim does.
	matchesEmbed := localBuildMatchesEmbeddedVocab([]byte(customVocab))
	if _, err := migrate.MigrateSingleFilenameWithEmbed(dir, "", false, matchesEmbed); err != nil {
		t.Fatalf("migrate.MigrateSingleFilenameWithEmbed: %v", err)
	}
	if !fileExists(filepath.Join(dir, "build.yml")) {
		t.Error("customized build.yml was wrongly deleted")
	}
	root, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	if !strings.Contains(string(root), "build.yml") {
		t.Errorf("customized build.yml import was wrongly dropped:\n%s", root)
	}
}
