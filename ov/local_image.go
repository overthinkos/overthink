package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// LocalImageInfo describes an image present in the engine's local storage.
// Populated by ListLocalImages from `{podman,docker} images --format json`.
type LocalImageInfo struct {
	Names  []string          // Full refs: ["ghcr.io/overthinkos/jupyter:latest", ...]
	Labels map[string]string // OCI labels from the image config
}

// ListLocalImages returns all images in the engine's local storage.
// Package-level var for testability (same pattern as LocalImageExists, DetectGPU).
var ListLocalImages = defaultListLocalImages

func defaultListLocalImages(engine string) ([]LocalImageInfo, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "images", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing local images via %s: %w", binary, err)
	}

	var rawImages []map[string]any
	if err := json.Unmarshal(out, &rawImages); err != nil {
		return nil, fmt.Errorf("parsing %s images output: %w", binary, err)
	}

	result := make([]LocalImageInfo, 0, len(rawImages))
	for _, raw := range rawImages {
		info := LocalImageInfo{Labels: make(map[string]string)}
		if names, ok := raw["Names"].([]any); ok {
			for _, n := range names {
				if s, ok := n.(string); ok {
					info.Names = append(info.Names, s)
				}
			}
		}
		// Docker uses RepoTags; podman uses Names. Handle both.
		if len(info.Names) == 0 {
			if tags, ok := raw["RepoTags"].([]any); ok {
				for _, t := range tags {
					if s, ok := t.(string); ok {
						info.Names = append(info.Names, s)
					}
				}
			}
		}
		if labels, ok := raw["Labels"].(map[string]any); ok {
			for k, v := range labels {
				if s, ok := v.(string); ok {
					info.Labels[k] = s
				}
			}
		}
		result = append(result, info)
	}
	return result, nil
}

// resolveLocalImageRef resolves a user-supplied image reference against the
// engine's local storage — never reads image.yml. Used by test-mode commands
// (ov eval live, ov eval image) so they stay within the test-mode input set.
//
// For full refs (registry prefix present) it validates the image exists
// locally and passes through unchanged. For short names it resolves via
// CalVer: collect every local image matching the short name (either by
// `org.overthinkos.image=<short>` label or by the tag-suffix short-name
// match) and pick the one whose tag has the highest CalVer (or the
// highest `org.overthinkos.version` label). ov is CalVer-only — no
// `:latest` fallback. See `/ov-build:build` "CalVer-only" for the contract.
//
// Returns `ErrImageNotLocal` when nothing matches. An ambiguous result
// across multiple repos with the same highest CalVer tag surfaces as an
// explicit error asking for a full ref.
func resolveLocalImageRef(engine, input string) (string, error) {
	if looksLikeFullRef(input) {
		if !LocalImageExists(engine, input) {
			return "", fmt.Errorf("%w: %s", ErrImageNotLocal, input)
		}
		return input, nil
	}

	images, err := ListLocalImages(engine)
	if err != nil {
		return "", err
	}

	var labelCands, nameCands []resolverCandidate
	for _, img := range images {
		// Label-preferred: org.overthinkos.image equals the short name.
		if img.Labels[LabelImage] == input && input != "" {
			ver := img.Labels[LabelVersion]
			for _, n := range img.Names {
				// Prefer tag-CalVer over label-CalVer when both present;
				// the tag is what podman run / quadlet consumes.
				tagCalVer := extractCalVerTag(n)
				if tagCalVer == "" {
					tagCalVer = ver
				}
				labelCands = append(labelCands, resolverCandidate{ref: n, calver: tagCalVer})
			}
			continue
		}
		// Name-fallback: any of the image's tags has the short name as
		// its trailing repo component. This catches `<deploy-name>:<calver>`
		// aliases (tagDeployAlias) on overlay images that inherited
		// the base image's label.
		for _, name := range img.Names {
			if shortNameMatchesRef(name, input) {
				nameCands = append(nameCands, resolverCandidate{ref: name, calver: extractCalVerTag(name)})
			}
		}
	}

	cands := labelCands
	if len(cands) == 0 {
		cands = nameCands
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("%w: %s", ErrImageNotLocal, input)
	}

	// Sort by CalVer descending. YYYY.DDD.HHMM is NOT lexically
	// sortable — DDD is 1-366 (1 vs 99 vs 366) and HHMM is 0-2359
	// (9:49 → "949", 10:54 → "1054"), both variable-width. Parse each
	// component numerically. Entries with no CalVer sort to the
	// bottom so a tagged CalVer beats a label-only match of equal
	// rank.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].calver == cands[j].calver {
			return cands[i].ref < cands[j].ref
		}
		if cands[i].calver == "" {
			return false
		}
		if cands[j].calver == "" {
			return true
		}
		return compareCalVer(cands[i].calver, cands[j].calver) > 0
	})

	// If the top candidate has no CalVer AND there are multiple
	// distinct repositories among the candidates, that's a genuine
	// cross-repo ambiguity (e.g. two third-party `:latest` tags).
	// Surface the full list so the user can disambiguate with a full
	// ref. CalVer-tagged candidates never hit this branch.
	if cands[0].calver == "" && !sameRepoAcross(cands) {
		refs := make([]string, len(cands))
		for i, c := range cands {
			refs[i] = c.ref
		}
		return "", fmt.Errorf("ambiguous short name %q in local storage; candidates: %s. Re-run with a full ref.",
			input, strings.Join(refs, ", "))
	}

	return cands[0].ref, nil
}

// resolverCandidate pairs a full image ref with its parsed CalVer
// (from the `:<calver>` tag or the `org.overthinkos.version` label).
// Used internally by resolveLocalImageRef to sort candidates
// newest-first before picking one.
type resolverCandidate struct {
	ref    string
	calver string
}

// sameRepoAcross reports whether every candidate ref shares the same
// repository path (everything before the final `:<tag>`). Used to
// distinguish benign duplicate-tag cases (one image, multiple tags)
// from genuinely ambiguous matches (same short name across multiple
// unrelated repos).
func sameRepoAcross(cands []resolverCandidate) bool {
	if len(cands) <= 1 {
		return true
	}
	repoOf := func(ref string) string {
		if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
			if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
				return ref[:colon]
			}
		} else if colon := strings.LastIndex(ref, ":"); colon >= 0 {
			return ref[:colon]
		}
		return ref
	}
	first := repoOf(cands[0].ref)
	for _, c := range cands[1:] {
		if repoOf(c.ref) != first {
			return false
		}
	}
	return true
}

// compareCalVer compares two CalVer strings numerically component-by-component.
// Returns >0 if a > b, <0 if a < b, 0 if equal. Handles the variable-width
// HHMM and DDD components (e.g. "9" vs "10" — lexical compare gives the
// wrong answer because "10" < "9" as strings but 10 > 9 numerically).
// Non-numeric components fall through to lexical compare as a defensive
// fallback, but extractCalVerTag only returns valid numeric CalVers.
func compareCalVer(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		ai, aErr := strconv.Atoi(aParts[i])
		bi, bErr := strconv.Atoi(bParts[i])
		if aErr != nil || bErr != nil {
			// Fall back to lexical for this component.
			if aParts[i] < bParts[i] {
				return -1
			}
			if aParts[i] > bParts[i] {
				return 1
			}
			continue
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	if len(aParts) > len(bParts) {
		return 1
	}
	return 0
}

// extractCalVerTag returns the CalVer portion of a ref's tag, or ""
// if the tag is not a recognisable CalVer (`YYYY.DDD.HHMM`). Lets the
// resolver distinguish CalVer tags from legacy floats like `:latest`
// (which should never be chosen as the newest candidate).
func extractCalVerTag(ref string) string {
	// Find the tag portion: last ':' after the last '/'.
	tagStart := -1
	if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
		if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
			tagStart = colon + 1
		}
	} else if colon := strings.LastIndex(ref, ":"); colon >= 0 {
		tagStart = colon + 1
	}
	if tagStart < 0 || tagStart >= len(ref) {
		return ""
	}
	tag := ref[tagStart:]
	// CalVer shape: three dot-separated decimal parts. Legacy
	// `:latest` / `:stable` / `:dev` floats fall through.
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return ""
			}
		}
	}
	return tag
}

// ResolveNewestLocalCalVer is the canonical "find the newest local
// image for this short name" helper. Thin wrapper around
// resolveLocalImageRef — exposed so callers that start with an
// explicit short-name + empty-tag can resolve uniformly.
func ResolveNewestLocalCalVer(engine, short string) (string, error) {
	return resolveLocalImageRef(engine, short)
}

// shortNameMatchesRef reports whether a short name like "jupyter" matches a
// full ref like "ghcr.io/overthinkos/jupyter:latest" by comparing the trailing
// repo component (after the last "/", before the tag).
func shortNameMatchesRef(fullRef, short string) bool {
	// Strip tag: find the last ":" that comes after the last "/".
	repo := fullRef
	if lastSlash := strings.LastIndex(repo, "/"); lastSlash >= 0 {
		if colon := strings.LastIndex(repo, ":"); colon > lastSlash {
			repo = repo[:colon]
		}
		return repo[lastSlash+1:] == short
	}
	// No slash — compare the whole thing minus any tag.
	if colon := strings.LastIndex(repo, ":"); colon >= 0 {
		repo = repo[:colon]
	}
	return repo == short
}
