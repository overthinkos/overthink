package main

import (
	"strings"
	"testing"
	"time"
)

// shrinkEncMountTimings shrinks the poll deadline so tests complete quickly.
// Returns a restore function.
func shrinkEncMountTimings(t *testing.T) func() {
	origDeadline := encMountDeadline
	origPeriod := encMountPollPeriod
	encMountDeadline = 100 * time.Millisecond
	encMountPollPeriod = 10 * time.Millisecond
	return func() {
		encMountDeadline = origDeadline
		encMountPollPeriod = origPeriod
	}
}

// TestResolveEncPassphraseForMount_Default_FailsFast: src="default" under a
// keyring-capable backend fails immediately with no polling — "default" is
// terminal (credential not stored anywhere).
func TestResolveEncPassphraseForMount_Default_FailsFast(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "", "default"
	}
	start := time.Now()
	_, err := resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resolveCalls != 1 {
		t.Errorf("resolveCalls = %d, want 1 (no retry on 'default')", resolveCalls)
	}
	if elapsed > 20*time.Millisecond {
		t.Errorf("elapsed = %v, want near-zero (no sleep)", elapsed)
	}
	if !strings.Contains(err.Error(), "source=default") {
		t.Errorf("err = %v, want 'source=default'", err)
	}
	if !strings.Contains(err.Error(), "ov secrets set") {
		t.Errorf("err = %v, want remediation hint", err)
	}
}

// TestResolveEncPassphraseForMount_Locked_RetriesThenFails: src="locked"
// retries up to the deadline, then fails. Test uses a shrunken deadline.
func TestResolveEncPassphraseForMount_Locked_RetriesThenFails(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "", "locked"
	}
	start := time.Now()
	_, err := resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resolveCalls < 2 {
		t.Errorf("resolveCalls = %d, want ≥ 2 (should retry)", resolveCalls)
	}
	if elapsed < encMountDeadline {
		t.Errorf("elapsed = %v, want ≥ %v (should wait full deadline)", elapsed, encMountDeadline)
	}
	if !strings.Contains(err.Error(), "source=locked") {
		t.Errorf("err = %v, want 'source=locked'", err)
	}
}

// TestResolveEncPassphraseForMount_Unavailable_RetriesThenFails: src="unavailable"
// retries up to the deadline, then fails.
func TestResolveEncPassphraseForMount_Unavailable_RetriesThenFails(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "", "unavailable"
	}
	_, err := resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resolveCalls < 2 {
		t.Errorf("resolveCalls = %d, want ≥ 2", resolveCalls)
	}
	if !strings.Contains(err.Error(), "source=unavailable") {
		t.Errorf("err = %v, want 'source=unavailable'", err)
	}
}

// TestResolveEncPassphraseForMount_Locked_ThenSuccess: src="locked" on first
// attempt, then a successful resolve — should return the value.
func TestResolveEncPassphraseForMount_Locked_ThenSuccess(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		if resolveCalls == 1 {
			return "", "locked"
		}
		return "the-secret", "keyring"
	}
	val, err := resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "the-secret" {
		t.Errorf("val = %q, want %q", val, "the-secret")
	}
	if resolveCalls != 2 {
		t.Errorf("resolveCalls = %d, want 2", resolveCalls)
	}
}

// TestResolveEncPassphraseForMount_Success_ReturnsImmediately: src="keyring"
// with a value returns on first call.
func TestResolveEncPassphraseForMount_Success_ReturnsImmediately(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "immediate", "keyring"
	}
	val, err := resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "immediate" {
		t.Errorf("val = %q, want %q", val, "immediate")
	}
	if resolveCalls != 1 {
		t.Errorf("resolveCalls = %d, want 1", resolveCalls)
	}
}

// TestResolveEncPassphraseForMount_ExplicitConfigBackend_FailsFast: with
// backend="config", the function skips the poll loop entirely and fails on
// first miss.
func TestResolveEncPassphraseForMount_ExplicitConfigBackend_FailsFast(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "", "default"
	}
	start := time.Now()
	_, err := resolveEncPassphraseForMountWithResolver("testimg", "config", resolver, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if resolveCalls != 1 {
		t.Errorf("resolveCalls = %d, want 1", resolveCalls)
	}
	if elapsed > 20*time.Millisecond {
		t.Errorf("elapsed = %v, want near-zero", elapsed)
	}
	if !strings.Contains(err.Error(), "backend=config") {
		t.Errorf("err = %v, want 'backend=config'", err)
	}
}

// TestResolveEncPassphraseForMount_Reset_IsCalledBetweenRetries: the reset
// closure is invoked between retry attempts so the cached store gets
// re-probed on the next iteration.
func TestResolveEncPassphraseForMount_Reset_IsCalledBetweenRetries(t *testing.T) {
	defer shrinkEncMountTimings(t)()
	resolveCalls := 0
	resetCalls := 0
	resolver := func() (string, string) {
		resolveCalls++
		return "", "locked"
	}
	reset := func() { resetCalls++ }
	_, _ = resolveEncPassphraseForMountWithResolver("testimg", "auto", resolver, reset)
	if resetCalls == 0 {
		t.Errorf("resetCalls = 0, want > 0 (reset should be called between retries)")
	}
	// Reset should be called N-1 times for N resolve calls (reset after
	// each retry, not after the final failing attempt).
	if resetCalls >= resolveCalls {
		t.Errorf("resetCalls (%d) should be < resolveCalls (%d)", resetCalls, resolveCalls)
	}
}
