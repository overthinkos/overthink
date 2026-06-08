package main

// eval_image_preflight.go — score-driven image preflight for
// `charly eval run` host-target dispatches.
//
// The deploy applies host packages + configs only (CLAUDE.md "Deploy
// fetches NOTHING speculative"); container images that scenarios
// spawn need to be present BEFORE the runner walks them. This file
// owns the score → image-set discovery only — the actual ensure
// logic lives in `ov/ensure_image.go::EnsureImagePresent`, the
// canonical helper used by every command (R3).

import (
	"context"
	"fmt"
	"os"
	"sort"
)

// ensureScoreImages collects every image identifier the score's
// in-scope scenarios may spawn (target_image + per-scenario pod
// values), deduplicates, and ensures each is present via the
// canonical EnsureImagePresent helper. Idempotent.
//
// Failures abort the eval BEFORE any scenario runs.
func ensureScoreImages(ctx context.Context, score *HarnessScore, uf *UnifiedFile, projectDir string) error {
	if score == nil || uf == nil {
		return nil
	}
	cfg := uf.ProjectConfig()

	want := map[string]struct{}{}
	if score.TargetImage != "" {
		want[score.TargetImage] = struct{}{}
	}
	if scenarios, _, err := ResolveScoreRecipe(score, uf.Recipe); err == nil {
		for _, sc := range scenarios {
			if sc.Pod != "" {
				want[sc.Pod] = struct{}{}
			}
		}
	}
	if len(want) == 0 {
		return nil
	}

	// Stable order so the preflight banner is deterministic.
	refs := make([]string, 0, len(want))
	for ref := range want {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	fmt.Fprintf(os.Stderr, "preflight: ensuring %d image(s) present in podman storage\n", len(refs))
	for _, ref := range refs {
		if err := EnsureImagePresent(ctx, ref, cfg, projectDir); err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}
	return nil
}
