package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bcWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bcRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func bcAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("expected %s to be absent", path)
	}
}

func TestMigrateBoxCandyRename(t *testing.T) {
	dir := t.TempDir()
	bcWrite(t, filepath.Join(dir, "overthink.yml"), `version: "2026.155.1801"
import:
    - image.yml
    - eval.yml
discover:
    layer:
        - path: layers
          recursive: true
`)
	bcWrite(t, filepath.Join(dir, "image.yml"), `image:
    eval-pod:
        base: fedora
        layer:
            - supervisord
`)
	bcWrite(t, filepath.Join(dir, "eval.yml"), `eval:
    eval-pod:
        target: pod
        image: eval-pod
`)
	// Nested-collision layer: a `layer:` deps list inside the `layer:` wrapper.
	bcWrite(t, filepath.Join(dir, "layers", "chrome-cdp", "layer.yml"), `layer:
    name: chrome-cdp
    version: "2026.155.1801"
    layer:
        - chrome-devtools-mcp
    image_default: keepme
`)

	changed, err := MigrateBoxCandyRename(dir, "", false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(changed) == 0 {
		t.Fatal("expected changes")
	}

	// Files + directory renamed.
	if _, err := os.Stat(filepath.Join(dir, "box.yml")); err != nil {
		t.Error("box.yml missing")
	}
	bcAbsent(t, filepath.Join(dir, "image.yml"))
	if _, err := os.Stat(filepath.Join(dir, "candy", "chrome-cdp", "candy.yml")); err != nil {
		t.Error("candy/chrome-cdp/candy.yml missing")
	}
	bcAbsent(t, filepath.Join(dir, "layers"))

	// Collision: the candy file carries BOTH a root `candy:` wrapper and a
	// nested `candy:` deps list; the compound key image_default is untouched.
	candy := bcRead(t, filepath.Join(dir, "candy", "chrome-cdp", "candy.yml"))
	if strings.Count(candy, "candy:") < 2 {
		t.Errorf("expected wrapper + deps candy: keys, got:\n%s", candy)
	}
	if strings.Contains(candy, "layer:") {
		t.Errorf("layer: not fully renamed:\n%s", candy)
	}
	if !strings.Contains(candy, "image_default: keepme") {
		t.Errorf("compound key image_default wrongly renamed:\n%s", candy)
	}

	// image: -> box: (map) and nested layer: deps -> candy:.
	box := bcRead(t, filepath.Join(dir, "box.yml"))
	if !strings.Contains(box, "box:") || strings.Contains(box, "image:") {
		t.Errorf("image: -> box: failed:\n%s", box)
	}
	if !strings.Contains(box, "candy:") {
		t.Errorf("layer: deps -> candy: failed in box:\n%s", box)
	}

	// image: selector -> box: on the pod bed.
	ev := bcRead(t, filepath.Join(dir, "eval.yml"))
	if !strings.Contains(ev, "box: eval-pod") {
		t.Errorf("eval selector image: -> box: failed:\n%s", ev)
	}

	// import path image.yml -> box.yml; discover layer: -> candy: + path layers -> candy.
	ot := bcRead(t, filepath.Join(dir, "overthink.yml"))
	if !strings.Contains(ot, "box.yml") {
		t.Errorf("import image.yml -> box.yml failed:\n%s", ot)
	}
	if !strings.Contains(ot, "path: candy") {
		t.Errorf("discover path layers -> candy failed:\n%s", ot)
	}

	// Idempotent: a second run changes nothing.
	changed2, err := MigrateBoxCandyRename(dir, "", false)
	if err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if len(changed2) != 0 {
		t.Errorf("expected idempotent no-op, got: %v", changed2)
	}
}
