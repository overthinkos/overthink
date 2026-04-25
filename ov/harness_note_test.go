package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadNote_Empty(t *testing.T) {
	dir := t.TempDir()
	body, err := ReadNote(dir, "myrecipe")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestAppendNote_HeaderAndOrdering(t *testing.T) {
	dataRoot := t.TempDir()
	t.Setenv("OV_HARNESS_DATA_ROOT", dataRoot)
	dir := t.TempDir() // workspace (project) — not where notes live now
	if err := AppendNote(dir, "rec", "run-1", "1", "claude", "first note"); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote(dir, "rec", "run-1", "2", "claude", "second note"); err != nil {
		t.Fatal(err)
	}
	body, err := ReadNote(dir, "rec")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "first note") || !strings.Contains(body, "second note") {
		t.Errorf("missing notes in output:\n%s", body)
	}
	idx1 := strings.Index(body, "first note")
	idx2 := strings.Index(body, "second note")
	if idx1 == -1 || idx2 == -1 || idx1 >= idx2 {
		t.Errorf("ordering wrong: idx1=%d idx2=%d", idx1, idx2)
	}
	if !strings.Contains(body, "iter=1") || !strings.Contains(body, "iter=2") {
		t.Errorf("headers should record iter=N; got:\n%s", body)
	}
	// With OV_HARNESS_DATA_ROOT override the file lives at
	// $OV_HARNESS_DATA_ROOT/rec/note/NOTES.md (no project pollution).
	expected := filepath.Join(dataRoot, "rec", "note", "NOTES.md")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected file at %s: %v", expected, err)
	}
	// Project workspace MUST NOT have a .harness/ directory — the
	// whole point of the harness data root refactor.
	if _, err := os.Stat(filepath.Join(dir, ".harness")); err == nil {
		t.Errorf("harness must NOT pollute the project workspace; found %s/.harness", dir)
	}
}

func TestAppendNote_RequiresText(t *testing.T) {
	dir := t.TempDir()
	if err := AppendNote(dir, "r", "id", "1", "claude", ""); err == nil {
		t.Error("expected error for empty text")
	}
	if err := AppendNote(dir, "r", "id", "1", "claude", "   \n  "); err == nil {
		t.Error("expected error for whitespace-only text")
	}
}

func TestAppendNote_RequiresRecipe(t *testing.T) {
	dir := t.TempDir()
	if err := AppendNote(dir, "", "id", "1", "claude", "text"); err == nil {
		t.Error("expected error for empty recipe name")
	}
}

func TestNotePath_UnderHarnessDataRoot(t *testing.T) {
	// With override, the path is fully deterministic.
	t.Setenv("OV_HARNESS_DATA_ROOT", "/tmp/harness-data")
	got := NotePath("/proj", "bench")
	want := "/tmp/harness-data/bench/note/NOTES.md"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Confirm it's NOT under the project tree.
	if strings.Contains(got, "/proj/.harness") {
		t.Errorf("note path must not pollute the workspace: %q", got)
	}
}
