package main

// legacy_local_images.go — shared YAML scanner for the 2026-05
// deploy-fetch-narrowing cutover. Detects (and, in the migration
// command, rewrites) any `images:` key nested under `local.<name>` in
// project YAML.
//
// The kind:local `images:` field was removed; see local_spec.go and
// the eval preflight in eval_image_preflight.go. Operators with
// legacy YAML run `ov migrate` to convert each block to
// a dated comment fence; the validator (validateLegacyLocalImagesField)
// hard-errors on any surviving key so legacy configs cannot silently
// load and behave differently than fresh ones.

import (
	"os"
	"path/filepath"
	"strings"
)

// LegacyImagesBlock describes one detected legacy block at file-level
// granularity. Used by both the validator (to emit one error per
// occurrence) and the migration command (to rewrite the lines in
// place).
type LegacyImagesBlock struct {
	Path         string   // file path
	TemplateName string   // local.<name> the block is nested under (best-effort; "" if unknown)
	StartLine    int      // 1-based line number of the `images:` line
	EndLine      int      // 1-based line number of the last list item
	OriginalRefs []string // raw values of the list entries (for migration commenting)
}

// scanLegacyLocalImagesBlocks walks every YAML file under dir and
// returns every detected legacy `images:` block. Skips vendor/build
// dirs to mirror the loader's discovery scope.
func scanLegacyLocalImagesBlocks(dir string) []LegacyImagesBlock {
	var out []LegacyImagesBlock
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "node_modules" || base == ".build" || base == ".cache" || base == ".eval" || base == "plugins" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yml") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		out = append(out, scanLegacyLocalImagesInFile(path, string(data))...)
		return nil
	})
	return out
}

// scanLegacyLocalImagesInFile inspects one YAML body for legacy
// `images:` blocks under `local.<name>`. The scan is line-oriented
// (indent-aware) so it preserves the file's existing comments and
// formatting — a structural YAML round-trip would erase those.
//
// The detection rule:
//   - Track the YAML key path implied by indentation.
//   - When the path's first segment is `local`, second segment is a
//     template name, and third segment is `images:` (a block-list
//     opening), record the line range until the indent unwinds.
//
// Already-migrated files carry the cutover marker comment immediately
// before the (now-list-shaped) block; we treat any `images:` line that
// is preceded within the previous five lines by the marker
// "deploy-fetch-narrowing cutover" as already-handled and skip it.
func scanLegacyLocalImagesInFile(path, body string) []LegacyImagesBlock {
	lines := strings.Split(body, "\n")
	var out []LegacyImagesBlock

	// Indent-tracking state.
	var (
		inLocalMap     bool   // we're inside the top-level `local:` map
		localIndent    int    // indent column of the `local:` key
		templateName   string // current `local.<templateName>` we're inside (or "")
		templateIndent int    // indent column of the template-name key
	)

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)

		// Top-level `local:` opener at column 0.
		if !inLocalMap && indent == 0 && (trimmed == "local:" || strings.HasPrefix(trimmed, "local: #")) {
			inLocalMap = true
			localIndent = 0
			templateName = ""
			continue
		}
		// We're inside `local:` and a sibling top-level key starts.
		if inLocalMap && indent == localIndent && trimmed != "local:" {
			inLocalMap = false
			templateName = ""
		}
		if !inLocalMap {
			continue
		}

		// Template name: indent == localIndent + N (typically 2),
		// trimmed line ends in ':'. Reset every time we see one at the
		// child-of-local indent level.
		if templateName == "" || indent == templateIndent {
			if indent > localIndent && strings.HasSuffix(strings.TrimSpace(trimmed), ":") {
				templateName = strings.TrimSuffix(strings.TrimSpace(trimmed), ":")
				templateIndent = indent
				continue
			}
		}
		if templateName == "" {
			continue
		}

		// We're inside `local.<templateName>`. Look for `images:` at
		// indent > templateIndent.
		if indent > templateIndent && trimmed == "images:" {
			// Skip already-migrated occurrences (they're commented out
			// in a marker fence).
			if previousLinesContainMarker(lines, i, 6, "deploy-fetch-narrowing cutover") {
				continue
			}
			// Collect the block. The list items follow at deeper indent
			// until the indent unwinds back to <= images-indent.
			imagesIndent := indent
			block := LegacyImagesBlock{
				Path:         path,
				TemplateName: templateName,
				StartLine:    i + 1,
				EndLine:      i + 1,
			}
			for j := i + 1; j < len(lines); j++ {
				peek := lines[j]
				peekTrim := strings.TrimLeft(peek, " \t")
				if peekTrim == "" || strings.HasPrefix(peekTrim, "#") {
					// Blank or comment lines are part of the block while
					// we're still indented inside it; advance EndLine
					// only if a real entry follows.
					continue
				}
				peekIndent := len(peek) - len(peekTrim)
				if peekIndent <= imagesIndent {
					break
				}
				// Block-list items only — `- foo` style.
				if strings.HasPrefix(peekTrim, "- ") || peekTrim == "-" {
					ref := strings.TrimSpace(strings.TrimPrefix(peekTrim, "-"))
					ref = strings.Trim(ref, `"'`)
					if ref != "" {
						block.OriginalRefs = append(block.OriginalRefs, ref)
					}
					block.EndLine = j + 1
				}
			}
			out = append(out, block)
		}
	}
	return out
}

// previousLinesContainMarker returns true when any of the n lines
// preceding the line at idx contains the marker substring. Used to
// skip already-migrated `images:` blocks (they're inside a marker
// comment fence).
func previousLinesContainMarker(lines []string, idx, n int, marker string) bool {
	start := idx - n
	if start < 0 {
		start = 0
	}
	for j := start; j < idx; j++ {
		if strings.Contains(lines[j], marker) {
			return true
		}
	}
	return false
}

// walkYAMLForLegacyLocalImages emits one validation error per detected
// legacy block. The error names the file, the template, and the
// migration command — exactly the breadcrumb pattern R5 requires when
// a stale schema field is loaded.
func walkYAMLForLegacyLocalImages(dir string, errs *ValidationError) {
	for _, block := range scanLegacyLocalImagesBlocks(dir) {
		errs.Add("kind:local %q in %s:%d: legacy `images:` field detected — run `ov migrate` to convert; the field was removed in the 2026-05 deploy-fetch-narrowing cutover (test-bed image preflight moved to `ov eval run`)",
			block.TemplateName, relPathForError(dir, block.Path), block.StartLine)
	}
}

// relPathForError returns a path relative to dir when possible, or the
// absolute path otherwise. Pure cosmetic helper for clearer errors.
func relPathForError(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// rewriteLegacyLocalImagesInFile produces the post-migration YAML body
// by replacing each detected legacy block with a dated comment fence.
// Returns the new body and the number of blocks rewritten. Idempotent
// — running twice on the same body produces identical output (the
// second pass finds no blocks because the marker fence flips the
// scanner predicate).
func rewriteLegacyLocalImagesInFile(path, body string) (string, int) {
	blocks := scanLegacyLocalImagesInFile(path, body)
	if len(blocks) == 0 {
		return body, 0
	}
	lines := strings.Split(body, "\n")

	// Collect the line ranges to delete (start..end inclusive, 1-based)
	// and the comment block to insert at the start position.
	// Process in reverse order so earlier indexes don't shift.
	type edit struct {
		start, end  int // 1-based inclusive
		replacement []string
	}
	edits := make([]edit, 0, len(blocks))
	for _, b := range blocks {
		// Indentation of the original `images:` line.
		origLine := lines[b.StartLine-1]
		indentStr := origLine[:len(origLine)-len(strings.TrimLeft(origLine, " \t"))]
		comment := []string{
			indentStr + "# 2026-05 deploy-fetch-narrowing cutover: 'images:' field removed.",
			indentStr + "# The deploy now fetches NOTHING speculative. Test-bed images are",
			indentStr + "# ensured by `ov eval run` preflight, sourced from each score's",
			indentStr + "# `target_image:` + scenario `pod:` declarations.",
		}
		if len(b.OriginalRefs) > 0 {
			comment = append(comment, indentStr+"# Original list:")
			for _, ref := range b.OriginalRefs {
				comment = append(comment, indentStr+"#   - "+ref)
			}
		}
		edits = append(edits, edit{start: b.StartLine, end: b.EndLine, replacement: comment})
	}
	// Apply in reverse so line numbers remain valid. Build the result
	// in a fresh slice — `append(lines[:s], lines[e:]...)` aliases the
	// backing array when capacity permits, which corrupts the tail
	// region with whatever the appended replacement just wrote there.
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		// 1-based to 0-based slice indexing.
		out := make([]string, 0, len(lines)-((e.end-e.start)+1)+len(e.replacement))
		out = append(out, lines[:e.start-1]...)
		out = append(out, e.replacement...)
		out = append(out, lines[e.end:]...)
		lines = out
	}
	return strings.Join(lines, "\n"), len(blocks)
}
