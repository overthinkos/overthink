package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sample legacy YAML carrying the kind:local `images:` field. Mirrors
// the shape ov-cachyos used pre-cutover.
const legacyLocalImagesYAML = `version: 4

local:
  ov-cachyos:
    layer:
      - wheel-nopasswd
      - dev-tools
    # 2026-05 cutover (` + "`kind: local`" + ` ` + "`images:`" + ` field).
    images:
      - eval-target
      - openclaw-sway-browser
      - fedora-coder

    install_opts:
      with_services: true
      allow_repo_changes: true
    description:
      feature: CachyOS DX
      tag: [working]
`

func TestRewriteLegacyLocalImages_ReplacesBlockWithCommentFence(t *testing.T) {
	got, n := rewriteLegacyLocalImagesInFile("test.yml", legacyLocalImagesYAML)
	if n != 1 {
		t.Fatalf("expected 1 block rewritten, got %d", n)
	}
	// The list-shaped images: key must be gone.
	if strings.Contains(got, "    images:\n") {
		t.Errorf("post-rewrite still contains list-shaped 'images:' key:\n%s", got)
	}
	// The dated marker must be present.
	if !strings.Contains(got, "deploy-fetch-narrowing cutover") {
		t.Errorf("missing cutover marker:\n%s", got)
	}
	// Original list values preserved as comments.
	for _, expected := range []string{"#   - eval-target", "#   - openclaw-sway-browser", "#   - fedora-coder"} {
		if !strings.Contains(got, expected) {
			t.Errorf("missing migrated comment %q in:\n%s", expected, got)
		}
	}
	// install_opts: must NOT have been collapsed (slice-aliasing
	// regression test — earlier bug ate trailing keys).
	if !strings.Contains(got, "    install_opts:") {
		t.Errorf("install_opts: was eaten by the rewrite:\n%s", got)
	}
	if !strings.Contains(got, "      with_services: true") {
		t.Errorf("install_opts.with_services: was eaten:\n%s", got)
	}
}

func TestRewriteLegacyLocalImages_Idempotent(t *testing.T) {
	once, _ := rewriteLegacyLocalImagesInFile("test.yml", legacyLocalImagesYAML)
	twice, n := rewriteLegacyLocalImagesInFile("test.yml", once)
	if n != 0 {
		t.Errorf("second pass should find 0 blocks, found %d", n)
	}
	if once != twice {
		t.Errorf("idempotency violation: post-second-pass differs from post-first-pass")
	}
}

func TestRewriteLegacyLocalImages_NoMatch(t *testing.T) {
	noImages := `version: 4
local:
  dev:
    layer: [ripgrep]
`
	got, n := rewriteLegacyLocalImagesInFile("test.yml", noImages)
	if n != 0 || got != noImages {
		t.Errorf("file with no images: should be a no-op (n=%d, body changed=%v)", n, got != noImages)
	}
}

func TestScanLegacyLocalImages_IgnoresTopLevelImagesMap(t *testing.T) {
	// Top-level `images:` (image.yml shape) at column 0 must NOT be
	// rewritten — only `local.<name>.images` is in scope.
	imageYML := `version: 4
image:
  fedora-coder:
    enabled: true
`
	blocks := scanLegacyLocalImagesInFile("image.yml", imageYML)
	if len(blocks) != 0 {
		t.Errorf("top-level images: map was incorrectly flagged: %+v", blocks)
	}
}

func TestMigrateLocalImages_WalksAndWrites(t *testing.T) {
	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "local.yml")
	if err := os.WriteFile(yamlPath, []byte(legacyLocalImagesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := MigrateLocalImage(tmp, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != yamlPath {
		t.Errorf("MigrateLocalImage: unexpected change list: %v", changed)
	}
	post, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(post), "    images:\n") {
		t.Errorf("post-migration file still has images: key:\n%s", post)
	}
	// Idempotent on re-run.
	changed2, _ := MigrateLocalImage(tmp, false)
	if len(changed2) != 0 {
		t.Errorf("second migration should be no-op, got changes: %v", changed2)
	}
}
