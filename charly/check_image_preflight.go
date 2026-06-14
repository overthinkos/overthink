package main

// check_image_preflight.go — score-driven image preflight for
// `charly check run` host-target dispatches.
//
// The deploy applies host packages + configs only (CLAUDE.md "Deploy
// fetches NOTHING speculative"); container images that plan steps
// spawn need to be present BEFORE the runner walks them. This file
// owns the score → image-set discovery only — the actual ensure
// logic lives in `charly/ensure_image.go::EnsureImagePresent`, the
// canonical helper used by every command (R3).

import (
	"context"
	"fmt"
	"os"
	"sort"
)

// ensureScoreImages collects every image identifier the iterate plan's
// scored steps may spawn (per-step Op.Pod values), deduplicates, and ensures
// each is present via the canonical EnsureImagePresent helper. Idempotent.
//
// Failures abort the check BEFORE any step runs.
func ensureScoreImages(ctx context.Context, plan []Step, uf *UnifiedFile, projectDir string) error {
	if uf == nil {
		return nil
	}
	cfg := uf.ProjectConfig()

	want := map[string]struct{}{}
	for _, s := range plan {
		if s.Pod != "" {
			want[s.Pod] = struct{}{}
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
