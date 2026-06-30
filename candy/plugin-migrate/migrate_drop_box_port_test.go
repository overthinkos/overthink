package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateDropBoxPort proves the box-level `port:` field and the `port:
// [auto]` deploy sentinel are removed, while an explicit deploy PIN is preserved
// and a candy `port:` is never touched. Idempotent.
func TestMigrateDropBoxPort(t *testing.T) {
	dir := t.TempDir()

	// A discovered box doc with a retired box-level `port:`.
	boxDir := filepath.Join(dir, "box", "android-emulator")
	if err := os.MkdirAll(boxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	boxYML := "" +
		"box:\n" +
		"    name: android-emulator\n" +
		"    base: selkies-labwc\n" +
		"    port:\n" +
		"        - \"3000:3000\"\n" +
		"        - \"9222:9222\"\n" +
		"    env:\n" +
		"        - EMULATOR_NAME=avd\n"
	boxPath := filepath.Join(boxDir, "charly.yml")
	if err := os.WriteFile(boxPath, []byte(boxYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root charly.yml: an eval bed using `port: [auto]` (dropped) and another
	// using an explicit pin (preserved); plus a candy `port:` that must survive.
	rootYML := "" +
		"version: 2026.164.0004\n" +
		"eval:\n" +
		"    alpha-bed:\n" +
		"        target: pod\n" +
		"        box: web\n" +
		"        port: [auto]\n" +
		"    beta-bed:\n" +
		"        target: pod\n" +
		"        box: web\n" +
		"        port:\n" +
		"            - \"35000:3000\"\n"
	rootPath := filepath.Join(dir, "charly.yml")
	if err := os.WriteFile(rootPath, []byte(rootYML), 0o644); err != nil {
		t.Fatal(err)
	}

	// A candy with a `port:` that MUST be left untouched (candies are the source).
	candyDir := filepath.Join(dir, "candy", "chrome-cdp")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	candyYML := "candy:\n    name: chrome-cdp\n    port:\n        - 9222\n"
	candyPath := filepath.Join(candyDir, "charly.yml")
	if err := os.WriteFile(candyPath, []byte(candyYML), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateDropBoxPort(dir, false)
	if err != nil {
		t.Fatalf("MigrateDropBoxPort() error = %v", err)
	}
	if len(rewritten) != 2 {
		t.Errorf("rewrote %v, want box + root only (candy untouched)", rewritten)
	}

	boxOut, _ := os.ReadFile(boxPath)
	if strings.Contains(string(boxOut), "port:") {
		t.Errorf("box port: not removed:\n%s", boxOut)
	}
	if !strings.Contains(string(boxOut), "EMULATOR_NAME") {
		t.Errorf("box lost unrelated fields:\n%s", boxOut)
	}

	rootOut, _ := os.ReadFile(rootPath)
	if strings.Contains(string(rootOut), "auto") {
		t.Errorf("port: [auto] not removed (no 'auto' should survive):\n%s", rootOut)
	}
	if !strings.Contains(string(rootOut), "35000:3000") {
		t.Errorf("explicit deploy pin not preserved:\n%s", rootOut)
	}

	candyOut, _ := os.ReadFile(candyPath)
	if !strings.Contains(string(candyOut), "9222") {
		t.Errorf("candy port wrongly stripped:\n%s", candyOut)
	}

	// Idempotent — a second pass changes nothing.
	again, err := MigrateDropBoxPort(dir, false)
	if err != nil {
		t.Fatalf("second pass error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("migration not idempotent — second pass rewrote %v", again)
	}
}

// TestMigrateDropBoxPortSkipsSubmodules proves the migrator does NOT recurse into
// git submodule directories — after the box inversion main/box/ is the submodule
// mount parent, and each charly-project repo migrates ITSELF.
func TestMigrateDropBoxPortSkipsSubmodules(t *testing.T) {
	dir := t.TempDir()
	// A submodule under box/ (gitlink `.git` file) holding a box that declares port.
	sub := filepath.Join(dir, "box", "cachyos")
	subBox := filepath.Join(sub, "box", "selkies")
	if err := os.MkdirAll(subBox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../.git/modules/box/cachyos\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	subYML := "box:\n    name: selkies\n    port:\n        - \"3000:3000\"\n"
	if err := os.WriteFile(filepath.Join(subBox, "charly.yml"), []byte(subYML), 0o644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := MigrateDropBoxPort(dir, false)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("migrator recursed into a submodule: rewrote %v", rewritten)
	}
	out, _ := os.ReadFile(filepath.Join(subBox, "charly.yml"))
	if !strings.Contains(string(out), "port:") {
		t.Errorf("submodule box port was stripped (it must be left for the submodule's own migrate):\n%s", out)
	}
}
