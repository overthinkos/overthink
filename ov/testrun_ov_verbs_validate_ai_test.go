package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test plan for the validate_ai_artifacts narrowed-allowlist + freshness-mtime gate
// behaviour added in 2026-04-27.
//
// The seven verb/method pairs in artifactValidatableMethods are the
// ONLY ones validate_ai_artifacts touches. ALL other probes always
// re-run via the harness's own subprocess — the harness remains
// authoritative for non-state-dependent probes.
//
// Tested invariants:
//   1. The allowlist EXACTLY matches the set of methods with
//      spec.artifact == true (no drift possible — load-bearing for
//      anti-deception).
//   2. Allowlisted method + flag set + file present + fresh mtime →
//      run validators against the file (no subprocess re-execution).
//   3. Allowlisted method + flag set + file MISSING → fail with
//      actionable error pointing at self-evaluate.
//   4. Allowlisted method + flag set + file present but STALE mtime
//      → fail with anti-deception error.
//   5. Allowlisted method + flag set + stdout matcher present →
//      fail (matchers require re-execution).

// TestArtifactValidatableMethods_MatchesArtifactProducingMethodSpecs
// is the load-bearing drift-prevention test. The allowlist (the seven
// state-dependent capture methods) and the set of methodSpecs marked
// artifact:true MUST be identical — adding a new artifact-producing
// method without updating the allowlist (or vice-versa) is a
// regression in either direction. This test catches that drift at
// `go test` time.
func TestArtifactValidatableMethods_MatchesArtifactProducingMethodSpecs(t *testing.T) {
	// Collect all methodSpec keys with artifact:true across every
	// verb's allowlist. The verb/method pair is "verb/method".
	specMethods := make(map[string]bool)
	for verb, table := range map[string]map[string]methodSpec{
		"cdp":     cdpMethods,
		"wl":      wlMethods,
		"vnc":     vncMethods,
		"libvirt": libvirtMethods,
		"spice":   spiceMethods,
		"record":  recordMethods,
		"k8s":     k8sMethods,
		"dbus":    dbusMethods,
		"mcp":     mcpMethods,
	} {
		for method, spec := range table {
			if spec.artifact {
				specMethods[verb+"/"+method] = true
			}
		}
	}

	// Bidirectional comparison:
	//   - Every allowlist entry must be in specMethods (no
	//     "validate this even though spec doesn't mark it artifact").
	//   - Every artifact:true spec must be in the allowlist (no
	//     "this method produces artifacts but harness will silently
	//     re-run it bypassing the AI's iteration capture").
	for key := range artifactValidatableMethods {
		if !specMethods[key] {
			t.Errorf("artifactValidatableMethods has %q but no methodSpec marks it artifact:true — drift", key)
		}
	}
	for key := range specMethods {
		if !artifactValidatableMethods[key] {
			t.Errorf("methodSpec %q has artifact:true but is NOT in artifactValidatableMethods — drift; either add it or document why it should always re-run", key)
		}
	}
}

// TestRunOvVerb_ValidateAi_AllowlistedMethod_FilePresent_FreshMtime_ValidatorsRun
// covers the happy path: flag set, allowlisted method, file present
// with fresh mtime, validators succeed.
//
// Strategy: build a minimal Runner with ValidateAiArtifacts=true and
// IterStartTime in the past, point a Check at a tempfile we just
// wrote, run the artifact-validators path. This exercises the
// "skip-subprocess + run-validators" branch directly via runOvVerb.
func TestRunOvVerb_ValidateAi_AllowlistedMethod_FilePresent_FreshMtime_ValidatorsRun(t *testing.T) {
	tmp := t.TempDir()
	cast := filepath.Join(tmp, "session.cast")
	// Minimal valid asciinema cast: header object + 5 event arrays =
	// 6 lines (5 events). artifact_min_cast_events: 5 should pass.
	body := `{"version":2,"width":80,"height":24}` + "\n" +
		`[0.1, "o", "line1\n"]` + "\n" +
		`[0.2, "o", "line2\n"]` + "\n" +
		`[0.3, "o", "line3\n"]` + "\n" +
		`[0.4, "o", "line4\n"]` + "\n" +
		`[0.5, "o", "line5\n"]` + "\n"
	if err := os.WriteFile(cast, []byte(body), 0o644); err != nil {
		t.Fatalf("write cast: %v", err)
	}

	r := &Runner{
		Mode:                RunModeTest,
		Image:               "fixture-desktop",
		ValidateAiArtifacts: true,
		IterStartTime:       time.Now().Add(-1 * time.Hour),
	}
	c := &Check{
		Record:                "stop",
		Artifact:              cast,
		ArtifactMinBytes:      50,
		ArtifactMinCastEvents: 5,
	}
	res := r.runOvVerb(context.Background(), c, "record", "stop", recordMethods)
	if res.Status != TestPass {
		t.Errorf("expected pass, got %s: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "validated AI-produced artifact") {
		t.Errorf("expected message to mention AI-artifact validation; got %q", res.Message)
	}
}

// TestRunOvVerb_ValidateAi_AllowlistedMethod_FileMissing_FailsActionable
// covers the "AI never ran the probe" failure mode.
func TestRunOvVerb_ValidateAi_AllowlistedMethod_FileMissing_FailsActionable(t *testing.T) {
	r := &Runner{
		Mode:                RunModeTest,
		Image:               "fixture-desktop",
		ValidateAiArtifacts: true,
		IterStartTime:       time.Now().Add(-1 * time.Hour),
	}
	c := &Check{
		Cdp:              "screenshot",
		Tab:              "1",
		Artifact:         "/nonexistent/cdp.png",
		ArtifactMinBytes: 100,
	}
	res := r.runOvVerb(context.Background(), c, "cdp", "screenshot", cdpMethods)
	if res.Status != TestFail {
		t.Fatalf("expected fail, got %s: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "validate_ai_artifacts requires") {
		t.Errorf("expected actionable error mentioning validate_ai_artifacts, got %q", res.Message)
	}
	if !strings.Contains(res.Message, "self-evaluate") {
		t.Errorf("expected error to point at `ov harness self-evaluate`, got %q", res.Message)
	}
}

// TestRunOvVerb_ValidateAi_AllowlistedMethod_StaleMtime_FailsAntiDeception
// covers the load-bearing freshness gate: a pre-staged file that
// existed BEFORE the iter started must be rejected.
func TestRunOvVerb_ValidateAi_AllowlistedMethod_StaleMtime_FailsAntiDeception(t *testing.T) {
	tmp := t.TempDir()
	stale := filepath.Join(tmp, "stale.png")
	if err := os.WriteFile(stale, make([]byte, 5000), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	// Force the file's mtime to be 2 hours in the past.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	r := &Runner{
		Mode:                RunModeTest,
		Image:               "fixture-desktop",
		ValidateAiArtifacts: true,
		IterStartTime:       time.Now().Add(-30 * time.Minute), // iter started 30m ago, file is 2h old
	}
	c := &Check{
		Cdp:              "screenshot",
		Tab:              "1",
		Artifact:         stale,
		ArtifactMinBytes: 1000,
	}
	res := r.runOvVerb(context.Background(), c, "cdp", "screenshot", cdpMethods)
	if res.Status != TestFail {
		t.Fatalf("expected fail (anti-deception), got %s: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "stale") {
		t.Errorf("expected error to mention stale, got %q", res.Message)
	}
	if !strings.Contains(res.Message, "pre-staged") {
		t.Errorf("expected error to mention pre-staged, got %q", res.Message)
	}
}

// TestRunOvVerb_ValidateAi_AllowlistedMethod_PhaseBoundary_AcceptsCrossPhaseArtifact
// is the regression test for the R cutover. The freshness floor is
// the BENCHMARK start, not the per-iter start: artifacts produced
// legitimately in earlier phases (e.g. record/stop's cast file in
// phase 6) MUST remain valid when scored in later phases (7, 8).
//
// The bug this test guards against: per-iter freshness floor caused
// fixture-desktop:12 (record-cast-has-events) to flip from solved in
// phase 6/7 to fail in phase 8 — even though the cast file was
// genuinely produced during the benchmark. Plus record/stop is not
// idempotent, so the AI's self-evaluate can't regenerate the cast in
// later phases.
//
// Test setup mirrors the real bug: artifact mtime is older than a
// putative per-iter start, but newer than the benchmark/run start.
// The MUST-PASS verdict here proves the run-start floor is in place.
func TestRunOvVerb_ValidateAi_AllowlistedMethod_PhaseBoundary_AcceptsCrossPhaseArtifact(t *testing.T) {
	tmp := t.TempDir()
	cast := filepath.Join(tmp, "session.cast")
	body := `{"version":2,"width":80,"height":24}` + "\n" +
		`[0.1, "o", "line1\n"]` + "\n" +
		`[0.2, "o", "line2\n"]` + "\n" +
		`[0.3, "o", "line3\n"]` + "\n" +
		`[0.4, "o", "line4\n"]` + "\n" +
		`[0.5, "o", "line5\n"]` + "\n"
	if err := os.WriteFile(cast, []byte(body), 0o644); err != nil {
		t.Fatalf("write cast: %v", err)
	}
	// Simulate an artifact produced in phase 6 of the benchmark:
	// 90 minutes ago (older than the current phase's iter start
	// would be, but younger than the benchmark start).
	earlierPhase := time.Now().Add(-90 * time.Minute)
	if err := os.Chtimes(cast, earlierPhase, earlierPhase); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// IterStartTime is the BENCHMARK start (per the R cutover
	// semantics): 2 hours ago — older than the artifact's mtime.
	// This MUST pass.
	r := &Runner{
		Mode:                RunModeTest,
		Image:               "fixture-desktop",
		ValidateAiArtifacts: true,
		IterStartTime:       time.Now().Add(-2 * time.Hour),
	}
	c := &Check{
		Record:                "stop",
		Artifact:              cast,
		ArtifactMinBytes:      50,
		ArtifactMinCastEvents: 5,
	}
	res := r.runOvVerb(context.Background(), c, "record", "stop", recordMethods)
	if res.Status != TestPass {
		t.Errorf("phase-boundary cross-phase artifact MUST pass with run-start freshness floor; got %s: %s",
			res.Status, res.Message)
	}
}

// TestRunOvVerb_ValidateAi_AllowlistedMethod_StdoutMatcher_FailsActionable
// covers the "stdout matchers require re-execution" combination.
// Without re-running the command there's no stdout to match against.
func TestRunOvVerb_ValidateAi_AllowlistedMethod_StdoutMatcher_FailsActionable(t *testing.T) {
	tmp := t.TempDir()
	png := filepath.Join(tmp, "cdp.png")
	if err := os.WriteFile(png, make([]byte, 5000), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}

	r := &Runner{
		Mode:                RunModeTest,
		Image:               "fixture-desktop",
		ValidateAiArtifacts: true,
		IterStartTime:       time.Now().Add(-1 * time.Hour),
	}
	c := &Check{
		Cdp:              "screenshot",
		Tab:              "1",
		Artifact:         png,
		ArtifactMinBytes: 1000,
		Stdout:           MatcherList{Matcher{Op: "contains", Value: "ok"}},
	}
	res := r.runOvVerb(context.Background(), c, "cdp", "screenshot", cdpMethods)
	if res.Status != TestFail {
		t.Fatalf("expected fail (stdout matcher incompatible), got %s: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "stdout/stderr/exit_status matchers cannot be evaluated") {
		t.Errorf("expected actionable error about matchers requiring re-execution, got %q", res.Message)
	}
}
