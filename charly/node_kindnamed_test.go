package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNodeForm_KindNamedEntity is the regression test for an entity NAMED after a
// reserved kind keyword (the real case: a box named `k8s`). The migration flattens
// `box: {k8s: …}` to the name-first `k8s: {box: …}`, where the top-level key `k8s`
// collides with the `k8s` kind keyword. Two loader/validate sites must handle it:
//
//   - applyDiscoveredManifest routed by firstKindKey, which returns `k8s` for the
//     node-form value → it mis-decoded the box as a k8s-kind entity (`box` as a
//     string ref). The fix routes via classifyDoc (docShapeNode wins).
//   - validateVocabularyCollections (the LEGACY root-shape collection validator)
//     read top-level `k8s:` as the k8s collection and validated the `box` child
//     against #K8s. The fix skips node-form files (isNodeFormFile).
//
// Without either fix this test FAILS (load error / validation error).
func TestNodeForm_KindNamedEntity(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(dir, "charly.yml"), `version: "`+latestSchemaVersion.String()+`"
discover:
  - box
`)
	// A box NAMED after the `k8s` kind keyword, in node-form.
	must(filepath.Join(dir, "box", "k8s", "charly.yml"), "k8s:\n  box:\n    base: fedora\n")

	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified rejected a box named after a kind keyword: %v", err)
	}
	b, ok := uf.Box["k8s"]
	if !ok {
		t.Fatalf("box named 'k8s' not loaded as a box; boxes=%v", boxKeys(uf.Box))
	}
	if b.Base != "fedora" {
		t.Errorf("k8s box base = %q, want fedora (misdecoded as a k8s-kind entity?)", b.Base)
	}

	// The legacy root-shape collection validator must SKIP this node-form file
	// (classifyDoc → docShapeNode), or it validates the `box` child against #K8s.
	data, _ := os.ReadFile(filepath.Join(dir, "box", "k8s", "charly.yml"))
	if !isNodeFormFile(data) {
		t.Error("kind-named node-form file not recognized as node-form — the legacy collection validator would misvalidate it")
	}
}
