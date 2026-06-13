package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateRecipeSectionValues proves the recipe-from kind/scope VALUES are
// rewritten layer→candy / image→box, while a builder `kind: layer` and a
// check-level `scope: build|deploy` (NOT under a `from:` sequence) are left
// untouched, and the step is idempotent.
func TestMigrateRecipeSectionValues(t *testing.T) {
	dir := t.TempDir()
	src := `version: 2026.164.0002
recipe:
    r:
        from:
            - kind: layer
              name: sshd
              pod: p
            - kind: image
              name: fedora.x
              pod: p
              scope: [image]
            - kind: pod
              name: somepod
              pod: p
candy:
    web:
        eval:
            - id: x
              scope: build
            - id: y
              scope: deploy
builder:
    custom:
        kind: layer
`
	path := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	changed, err := MigrateRecipeSectionValues(dir, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(changed) == 0 {
		t.Fatalf("expected a change, got none")
	}

	got, _ := os.ReadFile(path)
	s := string(got)

	// Recipe from.kind + scope rewritten.
	if !strings.Contains(s, "kind: candy") {
		t.Errorf("recipe from.kind layer→candy not applied:\n%s", s)
	}
	if strings.Contains(s, "name: fedora.x") && !strings.Contains(s, "scope: [box]") {
		t.Errorf("recipe from.scope [image]→[box] not applied:\n%s", s)
	}
	// `kind: image` (the box recipe-from) → `kind: box`.
	if strings.Contains(s, "kind: image") {
		t.Errorf("recipe from.kind image→box not applied (kind: image remains):\n%s", s)
	}
	// pod recipe-from untouched.
	if !strings.Contains(s, "kind: pod") {
		t.Errorf("recipe from.kind pod must be untouched:\n%s", s)
	}
	// Builder kind: layer must NOT change (not under from:).
	bidx := strings.Index(s, "builder:")
	if bidx < 0 || !strings.Contains(s[bidx:], "kind: layer") {
		t.Errorf("builder kind: layer must be untouched:\n%s", s)
	}
	// Check-level scope: build / deploy must NOT change.
	if !strings.Contains(s, "scope: build") || !strings.Contains(s, "scope: deploy") {
		t.Errorf("check-level scope: build/deploy must be untouched:\n%s", s)
	}

	// Idempotency: a second run is a no-op.
	changed2, err := MigrateRecipeSectionValues(dir, false)
	if err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if len(changed2) != 0 {
		t.Errorf("second run should be a no-op, changed: %v", changed2)
	}
}
