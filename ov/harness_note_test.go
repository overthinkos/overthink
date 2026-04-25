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
	dir := t.TempDir()
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
	// Both notes should appear in order, each with a header.
	if !strings.Contains(body, "first note") || !strings.Contains(body, "second note") {
		t.Errorf("missing notes in output:\n%s", body)
	}
	idx1 := strings.Index(body, "first note")
	idx2 := strings.Index(body, "second note")
	if idx1 == -1 || idx2 == -1 || idx1 >= idx2 {
		t.Errorf("ordering wrong: idx1=%d idx2=%d", idx1, idx2)
	}
	// Headers should mention iter=1 and iter=2.
	if !strings.Contains(body, "iter=1") || !strings.Contains(body, "iter=2") {
		t.Errorf("headers should record iter=N; got:\n%s", body)
	}
	// File lives at .harness/rec/note/NOTES.md
	expected := filepath.Join(dir, ".harness", "rec", "note", "NOTES.md")
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("expected file at %s: %v", expected, err)
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

func TestNotePath(t *testing.T) {
	got := NotePath("/proj", "bench")
	want := "/proj/.harness/bench/note/NOTES.md"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
