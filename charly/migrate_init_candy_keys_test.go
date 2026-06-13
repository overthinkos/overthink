package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateInitCandyKeys proves the init-system vocabulary keys are rewritten
// layer_field→candy_field / layer_file→candy_file / depends_layer→depends_candy
// (at every depth within the init: subtree), a coincidental same-named key OUTSIDE
// init: is left untouched, and the step is idempotent.
func TestMigrateInitCandyKeys(t *testing.T) {
	dir := t.TempDir()
	src := `version: 2026.164.0004
init:
    supervisord:
        layer_field:
            - service
        depends_layer: supervisord
        model: fragment_assembly
    systemd:
        layer_field:
            - service
        layer_file:
            - '*.service'
candy:
    web:
        layer_field: not-an-init-key
`
	path := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := MigrateInitCandyKeys(dir, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(changed) == 0 {
		t.Fatalf("expected a change, got none")
	}

	got, _ := os.ReadFile(path)
	s := string(got)

	// All three init keys rewritten.
	for _, want := range []string{"candy_field:", "candy_file:", "depends_candy:"} {
		if !strings.Contains(s, want) {
			t.Errorf("init key rewrite missing %q:\n%s", want, s)
		}
	}
	// No old init key spelling survives inside the init: subtree.
	initSec := s[strings.Index(s, "init:"):strings.Index(s, "candy:")]
	for _, old := range []string{"layer_field:", "layer_file:", "depends_layer:"} {
		if strings.Contains(initSec, old) {
			t.Errorf("old init key %q survived inside init::\n%s", old, initSec)
		}
	}
	// The coincidental candy.web.layer_field OUTSIDE init: must be untouched (scope).
	candySec := s[strings.Index(s, "candy:"):]
	if !strings.Contains(candySec, "layer_field: not-an-init-key") {
		t.Errorf("a layer_field key OUTSIDE init: must NOT be renamed (scope leak):\n%s", candySec)
	}

	// Idempotency: a second run is a no-op.
	changed2, err := MigrateInitCandyKeys(dir, false)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if len(changed2) != 0 {
		t.Errorf("second run should be a no-op, changed: %v", changed2)
	}
}
