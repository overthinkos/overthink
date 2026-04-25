package main

// harness_note.go — persistent NOTES.md memory subsystem.
//
// One Markdown file per recipe at .harness/<recipe>/note/NOTES.md,
// shared across runs (not per-run). The harness exposes two affordances:
//
//   ov harness note read [<recipe>]   — print the file (or "(empty)")
//   ov harness note append [<recipe>] <text>  — atomic append with a header
//
// Append-only at the OS level (O_APPEND|O_CREATE) — no locking needed
// since the per-recipe flock at /workspace/.harness/.lock already
// serialises concurrent harness runs against the same recipe. The
// header is `## <calver> run=<run-id> iter=<k> ai=<name>` followed by
// a blank line, the body, and a trailing blank line; chronological by
// construction (file order = append order).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NotePath returns the absolute path to a recipe's NOTES.md file.
// projectDir is the cwd-resolved overthink project root; recipe is
// the recipe name as it appears in harness.yml's recipe: map.
func NotePath(projectDir, recipe string) string {
	return filepath.Join(projectDir, ".harness", recipe, "note", "NOTES.md")
}

// ReadNote returns the current contents of a recipe's NOTES.md, or
// "" if the file doesn't yet exist. The empty case is NOT an error —
// first-run reads succeed and the AI sees an empty memory snapshot.
//
// The function does NOT check recipe.notes — the caller's responsibility.
// (A disabled recipe shouldn't be calling ReadNote in the first place,
// but we read regardless if asked.)
func ReadNote(projectDir, recipe string) (string, error) {
	path := NotePath(projectDir, recipe)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// AppendNote writes one note to a recipe's NOTES.md, prefixed with a
// header line so the trail is parseable + chronologically ordered.
//
// Header format: `## <calver> run=<run-id> iter=<k> ai=<name>`. Empty
// fields render as `?` so partial context (e.g. CLI invocation outside
// an iteration) doesn't break the header layout.
//
// File is created if absent (with parent directory if needed). Writes
// are O_APPEND so concurrent invocations against different recipes are
// race-free at the OS level; same-recipe concurrency is gated by the
// per-target flock the loop holds.
func AppendNote(projectDir, recipe, runID, iter, ai, text string) error {
	if recipe == "" {
		return fmt.Errorf("note append: recipe name required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("note append: text required (got empty/whitespace)")
	}
	path := NotePath(projectDir, recipe)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("note append: mkdir %s: %w", filepath.Dir(path), err)
	}
	calver := ComputeCalVer()
	header := fmt.Sprintf("## %s run=%s iter=%s ai=%s\n\n",
		calver, orQuestion(runID), orQuestion(iter), orQuestion(ai))
	body := strings.TrimRight(text, "\n") + "\n\n"

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("note append: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(header + body); err != nil {
		return fmt.Errorf("note append: write %s: %w", path, err)
	}
	return nil
}

func orQuestion(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
