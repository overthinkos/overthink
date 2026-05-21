package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The HEAD step's Version must equal latestSchemaVersion (the gate's target).
func TestRegistryHeadMatchesLatest(t *testing.T) {
	steps := migrationSteps()
	head := steps[len(steps)-1]
	if head.Name != "calver-schema" {
		t.Errorf("last step = %q, want calver-schema (the stamp must stay last)", head.Name)
	}
	if head.Version != LatestSchemaVersion() {
		t.Errorf("HEAD step version %s != LatestSchemaVersion() %s", head.Version, LatestSchemaVersion())
	}
}

// The registry must be strictly ascending by CalVer — that ordering is the
// replay order, and a regression (equal or out-of-order entry) would migrate
// an old config incorrectly.
func TestMigrationStepsStrictlyOrdered(t *testing.T) {
	steps := migrationSteps()
	for i := 1; i < len(steps); i++ {
		prev, cur := steps[i-1].Version, steps[i].Version
		if !prev.Less(cur) {
			t.Errorf("step %d (%s @ %s) is not strictly after step %d (%s @ %s)",
				i, steps[i].Name, cur, i-1, steps[i-1].Name, prev)
		}
	}
}

// Step names must be unique (they appear in --dry-run / progress output).
func TestMigrationStepNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range migrationSteps() {
		if seen[s.Name] {
			t.Errorf("duplicate step name %q", s.Name)
		}
		seen[s.Name] = true
	}
}

// MigrateCalverSchema must stamp a legacy `version: 4` file to the HEAD CalVer,
// write exactly one backup, and be idempotent on a second run.
func TestMigrateCalverSchema_StampsAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	overthink := filepath.Join(dir, "overthink.yml")
	image := filepath.Join(dir, "image.yml")
	regWrite(t, overthink, "version: 4\nimage: {}\n")
	regWrite(t, image, "# comment kept\nversion: 4\nimage: {}\n")

	want := LatestSchemaVersion().String()
	changed, err := MigrateCalverSchema(dir, "", LatestSchemaVersion(), false)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(changed) != 2 {
		t.Fatalf("first run changed %d files, want 2: %v", len(changed), changed)
	}
	for _, p := range []string{overthink, image} {
		body, _ := os.ReadFile(p)
		if !strings.Contains(string(body), "version: "+want) {
			t.Errorf("%s not stamped to %s:\n%s", p, want, body)
		}
		if strings.Contains(string(body), "version: 4\n") {
			t.Errorf("%s still carries legacy version: 4:\n%s", p, body)
		}
	}
	// Comment preservation (line-oriented rewrite).
	body, _ := os.ReadFile(image)
	if !strings.Contains(string(body), "# comment kept") {
		t.Errorf("comment was lost in image.yml:\n%s", body)
	}
	// One backup per stamped file.
	baks, _ := filepath.Glob(overthink + ".bak.*")
	if len(baks) != 1 {
		t.Errorf("expected 1 backup for overthink.yml, got %d", len(baks))
	}

	// Idempotent: a second run changes nothing and writes no further backup.
	changed, err = MigrateCalverSchema(dir, "", LatestSchemaVersion(), false)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("second run changed %v, want none (idempotent)", changed)
	}
	baks, _ = filepath.Glob(overthink + ".bak.*")
	if len(baks) != 1 {
		t.Errorf("idempotent re-run wrote extra backups: %d", len(baks))
	}
}

// Files with no top-level version: key are left untouched (per-file-stamp
// layout adds no new version fields); a missing file is a silent no-op; a
// dry-run reports the change without writing.
func TestMigrateCalverSchema_EdgeCases(t *testing.T) {
	dir := t.TempDir()
	noVersion := filepath.Join(dir, "pod.yml")
	regWrite(t, noVersion, "pod: {}\n")
	// dry-run on a legacy file: reported, but not written.
	legacy := filepath.Join(dir, "overthink.yml")
	regWrite(t, legacy, "version: 4\n")

	changed, err := MigrateCalverSchema(dir, "", LatestSchemaVersion(), true)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if len(changed) != 1 || changed[0] != legacy {
		t.Errorf("dry-run changed = %v, want [%s]", changed, legacy)
	}
	body, _ := os.ReadFile(legacy)
	if string(body) != "version: 4\n" {
		t.Errorf("dry-run modified the file: %q", body)
	}
	body, _ = os.ReadFile(noVersion)
	if string(body) != "pod: {}\n" {
		t.Errorf("file without version: key was modified: %q", body)
	}
}

// The load-time gate must reject a legacy integer `version: 4` config with a
// hint pointing at the single `ov migrate` command.
func TestLoadUnified_CalVerGateRejectsLegacy(t *testing.T) {
	dir := t.TempDir()
	regWrite(t, filepath.Join(dir, "overthink.yml"), "version: 4\nimage: {}\n")
	_, _, err := LoadUnified(dir)
	if err == nil {
		t.Fatal("expected schema-gate error for version: 4, got nil")
	}
	if !strings.Contains(err.Error(), "ov migrate") {
		t.Errorf("gate error should point at `ov migrate`, got: %v", err)
	}
	// A file stamped to HEAD loads cleanly.
	regWrite(t, filepath.Join(dir, "overthink.yml"), "version: "+LatestSchemaVersion().String()+"\nimage: {}\n")
	if _, _, err := LoadUnified(dir); err != nil {
		t.Errorf("HEAD-stamped config should load, got: %v", err)
	}
}

func regWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
