package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadUnified_NodeForm proves the loader parses a unified node-form charly.yml
// end-to-end: classifyDoc → docShapeNode → validate-before-execute (#NodeDoc) →
// normalizeNodeInto → the projected UnifiedFile maps. Candy + box + a bundle group
// with two alongside pod members + an inline cross-member check.
func TestLoadUnified_NodeForm(t *testing.T) {
	dir := t.TempDir()
	doc := `version: "` + latestSchemaVersion.String() + `"
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
  redis-step-0:
    check: the binary exists
    file: /usr/bin/redis-server
coder:
  box:
    base: fedora
  coder-candy:
    candy: [redis]
shop:
  bundle: {}
  web:
    bundle:
      box: coder
    web-step-0:
      check: web reaches the cache
      command: "redis-cli -h ${HOST:cache} ping"
  cache:
    bundle:
      box: coder
`
	if err := os.WriteFile(filepath.Join(dir, UnifiedFileName), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified node-form: %v", err)
	}
	if uf.Candy["redis"] == nil {
		t.Errorf("candy redis not loaded; candies=%v", mapKeys(uf.Candy))
	} else if uf.Candy["redis"].Version != "2026.150.0000" {
		t.Errorf("candy redis version = %q", uf.Candy["redis"].Version)
	}
	if _, ok := uf.Box["coder"]; !ok {
		t.Errorf("box coder not loaded; boxes=%v", boxKeys(uf.Box))
	} else if uf.Box["coder"].Base != "fedora" {
		t.Errorf("box coder base = %q", uf.Box["coder"].Base)
	}
	shop, ok := uf.Bundle["shop"]
	if !ok {
		t.Fatalf("bundle shop not loaded; deploys=%v", deployKeys(uf.Bundle))
	}
	if len(shop.Members) != 2 || shop.Members["web"] == nil || shop.Members["cache"] == nil {
		t.Fatalf("shop members wrong: %v", deployKeys2(shop.Members))
	}
	if shop.Members["web"].Box != "coder" {
		t.Errorf("web member box=%q, want coder", shop.Members["web"].Box)
	}
	// Post-cutover: flattenBundleVenues HOISTS the member's step into the root
	// bundle Plan, stamping venue from tree position, and CLEARS the member's own
	// Plan. So the web member's step now lives in shop.Plan with venue "web".
	if len(shop.Members["web"].Plan) != 0 {
		t.Errorf("web member Plan should be cleared after hoist, got %d", len(shop.Members["web"].Plan))
	}
	foundWebVenue := false
	for _, s := range shop.Plan {
		if s.venue == "web" {
			foundWebVenue = true
		}
	}
	if !foundWebVenue {
		t.Errorf("expected a hoisted step with venue %q in shop.Plan", "web")
	}
}

func mapKeys(m map[string]*InlineCandy) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func boxKeys(m map[string]BoxConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func deployKeys(m map[string]BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
func deployKeys2(m map[string]*BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
