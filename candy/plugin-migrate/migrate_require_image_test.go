package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateRequireImage_PatternA_InfersBase verifies the
// `<base>/<instance>` deploy-key form (Pattern A) gets `image: <base>`
// injected automatically.
func TestMigrateRequireImage_PatternA_InfersBase(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    versa:
        image: versa
        target: pod
    versa/ecovoyage:
        target: pod
        port:
            - "32718:2718"
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 || results[0].Path != path {
		t.Fatalf("expected 1 modified file (%s), got %+v", path, results)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warnings)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "versa/ecovoyage:") {
		t.Fatalf("missing entry: %s", got)
	}
	// The injected image: line must appear inside the ecovoyage entry.
	gotStr := string(got)
	idxEntry := strings.Index(gotStr, "versa/ecovoyage:")
	idxImage := strings.Index(gotStr[idxEntry:], "image: versa")
	if idxImage < 0 {
		t.Fatalf("image: versa not injected on ecovoyage:\n%s", gotStr)
	}
}

// TestMigrateRequireImage_Idempotent verifies re-running the migration
// on an already-migrated tree is a no-op.
func TestMigrateRequireImage_Idempotent(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    versa:
        image: versa
        target: pod
    versa/ecovoyage:
        image: versa
        target: pod
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 0 || len(warnings) != 0 {
		t.Fatalf("expected idempotent no-op, got results=%v warnings=%v", results, warnings)
	}
}

// TestMigrateRequireImage_PodSuffix_InfersBase verifies the
// `<base>-pod` deploy-key suffix gets `image: <base>` injected (the
// established multi-pod convention).
func TestMigrateRequireImage_PodSuffix_InfersBase(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    jupyter-pod:
        target: pod
    jupyter-ml-pod:
        target: pod
    sway-browser-vnc-pod:
        target: pod
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 modified file, got %+v", results)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warnings)
	}
	got, _ := os.ReadFile(path)
	for _, want := range []string{
		"image: jupyter",
		"image: jupyter-ml",
		"image: sway-browser-vnc",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing injection %q:\n%s", want, got)
		}
	}
}

// TestMigrateRequireImage_PatternB_KeyMatchesImage verifies the
// "deploy key matches a kind:image entry" inference (Pattern B subset).
func TestMigrateRequireImage_PatternB_KeyMatchesImage(t *testing.T) {
	dir := t.TempDir()
	src := `
image:
    versa:
        layer: [marimo]
deploy:
    versa:
        target: pod
`
	path := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 modified file, got %+v", results)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %v", warnings)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "image: versa") {
		t.Fatalf("image: versa not injected:\n%s", got)
	}
}

// TestMigrateRequireImage_BoxKeyRecognized verifies a box-format pod deploy (the
// current candy/box-rebrand schema) is recognized as ALREADY carrying its image
// reference — no warning, no injection. Before the box-awareness fix the step
// checked only the legacy `image:` key, so it warned spuriously on every `box:`
// entry (e.g. the per-host deploy.yml's sway-browser-vnc).
func TestMigrateRequireImage_BoxKeyRecognized(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    sway-browser-vnc:
        target: pod
        box: sway-browser-vnc
    selkies-desktop:
        target: pod
        box: selkies-desktop
`
	path := filepath.Join(dir, "overthink.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("box-format pod deploys must not warn (box: already supplies the image ref), got: %v", warnings)
	}
	if len(results) != 0 {
		t.Errorf("box-format pod deploys must not be mutated (no image: injection), got: %+v", results)
	}
}

// TestMigrateRequireImage_NonInferable_RaisesWarning verifies that a
// deploy entry that doesn't match any inference rule produces a
// warning rather than a silent skip or wrong injection.
func TestMigrateRequireImage_NonInferable_RaisesWarning(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    some-arbitrary-name:
        target: pod
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 modifications when nothing inferable, got %+v", results)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "some-arbitrary-name") {
		t.Fatalf("expected warning naming the entry, got %v", warnings)
	}
}

// TestMigrateRequireImage_VmAndLocalSkipped verifies entries with
// target other than pod (vm, local, k8s) are NOT touched — image: is
// not required for those targets.
func TestMigrateRequireImage_VmAndLocalSkipped(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    arch-vm:
        target: vm
        vm: arch
    ov-cachyos:
        target: local
        local: ov-cachyos
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, warnings, err := MigrateRequireImage(dir, false, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 modifications for non-pod targets, got %+v", results)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for non-pod targets, got %v", warnings)
	}
}

// TestMigrateRequireImage_DryRun verifies dry-run reports changes
// without touching files.
func TestMigrateRequireImage_DryRun(t *testing.T) {
	dir := t.TempDir()
	src := `
deploy:
    versa:
        image: versa
        target: pod
    versa/ecovoyage:
        target: pod
`
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	results, _, err := MigrateRequireImage(dir, true, false)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 file in result, got %+v", results)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "versa/ecovoyage:\n        image:") {
		t.Fatalf("dry-run should NOT modify file:\n%s", got)
	}
}
