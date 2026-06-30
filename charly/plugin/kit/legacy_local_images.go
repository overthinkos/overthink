package kit

// legacy_local_images.go — the shared YAML scanner/rewriter for the 2026-05
// deploy-fetch-narrowing cutover (legacy `images:` nested under local.<name>).
// Shared by charly core's validator (walkYAMLForLegacyLocalImages) AND the
// compiled-in candy/plugin-migrate's local-images migrator (R3 — ONE copy across
// the module boundary). Core aliases LegacyImagesBlock / ScanLegacyLocalImagesInFile
// / RewriteLegacyLocalImagesInFile; the candy aliases the rewriter.

import (
	"slices"
	"strings"
)

// LegacyImagesBlock describes one detected legacy block at file-level granularity.
// Used by both the validator (one error per occurrence) and the migrator (rewrite
// the lines in place).
type LegacyImagesBlock struct {
	Path         string   // file path
	TemplateName string   // local.<name> the block is nested under (best-effort; "" if unknown)
	StartLine    int      // 1-based line number of the `images:` line
	EndLine      int      // 1-based line number of the last list item
	OriginalRefs []string // raw values of the list entries (for migration commenting)
}

// ScanLegacyLocalImagesInFile detects every legacy `images:` block nested under a
// top-level local.<name> in body. Already-migrated occurrences (inside a marker
// comment fence) are skipped.
func ScanLegacyLocalImagesInFile(path, body string) []LegacyImagesBlock {
	lines := strings.Split(body, "\n")
	var out []LegacyImagesBlock

	var (
		inLocalMap     bool
		localIndent    int
		templateName   string
		templateIndent int
	)

	for i := range lines {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(trimmed)

		if !inLocalMap && indent == 0 && (trimmed == "local:" || strings.HasPrefix(trimmed, "local: #")) {
			inLocalMap = true
			localIndent = 0
			templateName = ""
			continue
		}
		if inLocalMap && indent == localIndent && trimmed != "local:" {
			inLocalMap = false
			templateName = ""
		}
		if !inLocalMap {
			continue
		}

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

		if indent > templateIndent && trimmed == "images:" {
			if previousLinesContainMarker(lines, i, 6, "deploy-fetch-narrowing cutover") {
				continue
			}
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
					continue
				}
				peekIndent := len(peek) - len(peekTrim)
				if peekIndent <= imagesIndent {
					break
				}
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

// previousLinesContainMarker returns true when any of the n lines preceding idx
// contains the marker substring (used to skip already-migrated blocks).
func previousLinesContainMarker(lines []string, idx, n int, marker string) bool {
	start := max(idx-n, 0)
	for j := start; j < idx; j++ {
		if strings.Contains(lines[j], marker) {
			return true
		}
	}
	return false
}

// RewriteLegacyLocalImagesInFile produces the post-migration YAML body by replacing
// each detected legacy block with a dated comment fence. Returns the new body and
// the number of blocks rewritten. Idempotent.
func RewriteLegacyLocalImagesInFile(path, body string) (string, int) {
	blocks := ScanLegacyLocalImagesInFile(path, body)
	if len(blocks) == 0 {
		return body, 0
	}
	lines := strings.Split(body, "\n")

	type edit struct {
		start, end  int
		replacement []string
	}
	edits := make([]edit, 0, len(blocks))
	for _, b := range blocks {
		origLine := lines[b.StartLine-1]
		indentStr := origLine[:len(origLine)-len(strings.TrimLeft(origLine, " \t"))]
		comment := []string{
			indentStr + "# 2026-05 deploy-fetch-narrowing cutover: 'images:' field removed.",
			indentStr + "# The deploy now fetches NOTHING speculative. Test-bed images are",
			indentStr + "# ensured by `charly check run` preflight, sourced from each score's",
			indentStr + "# `target_image:` + per-step `pod:` declarations.",
		}
		if len(b.OriginalRefs) > 0 {
			comment = append(comment, indentStr+"# Original list:")
			for _, ref := range b.OriginalRefs {
				comment = append(comment, indentStr+"#   - "+ref)
			}
		}
		edits = append(edits, edit{start: b.StartLine, end: b.EndLine, replacement: comment})
	}
	for _, e := range slices.Backward(edits) {
		out := make([]string, 0, len(lines)-((e.end-e.start)+1)+len(e.replacement))
		out = append(out, lines[:e.start-1]...)
		out = append(out, e.replacement...)
		out = append(out, lines[e.end:]...)
		lines = out
	}
	return strings.Join(lines, "\n"), len(blocks)
}
