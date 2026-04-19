package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
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
// (ov test, ov image test) so they stay within the test-mode input set.
//
// For full refs (registry prefix present) it validates the image exists locally
// and passes through unchanged. For short names it matches in two passes:
//  1. Label-preferred: images whose org.overthinkos.image label equals the short name.
//  2. Name-fallback: images whose full-ref trailing component (after the last "/")
//     matches the short name.
//
// Label matches take priority. Ambiguous matches (multiple candidates) error with
// the full candidate list so the user can disambiguate with a full ref. No match
// returns ErrImageNotLocal wrapped with the input — FormatCLIError renders the
// "ov image pull / ov image build" recommendation.
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

	var labelMatches, nameMatches []string
	for _, img := range images {
		if img.Labels[LabelImage] == input && input != "" {
			labelMatches = append(labelMatches, img.Names...)
			continue
		}
		for _, name := range img.Names {
			if shortNameMatchesRef(name, input) {
				nameMatches = append(nameMatches, name)
			}
		}
	}

	matches := labelMatches
	if len(matches) == 0 {
		matches = nameMatches
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %s", ErrImageNotLocal, input)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous short name %q in local storage; candidates: %s. Re-run with a full ref.",
			input, strings.Join(matches, ", "))
	}
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
