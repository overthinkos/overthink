package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// readiness_config.go — the `defaults.readiness:` block: the config-sourced,
// env-overridable, load-validated bounds for the unified pollUntil readiness
// primitive (poll.go). This is the R4 half of the unified-readiness cutover:
// every poll bound is NAMED (the readiness*Fallback consts in poll.go),
// CONFIG-SOURCED (this block), and VALIDATED on load (Resolve → validate, on the
// RESOLVED post-env values — so an env override cannot smuggle in a nonsensical
// bound). No magic literal survives at any call site.

// resolveReadinessDuration: env (CHARLY_READINESS_*) wins over the config
// string, which wins over the named fallback. Parses + rejects non-positive.
func resolveReadinessDuration(field, envKey, cfgVal string, fallback time.Duration) (time.Duration, error) {
	raw := cfgVal
	if e := os.Getenv(envKey); e != "" {
		raw = e
	}
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("readiness.%s: invalid duration %q: %w", field, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("readiness.%s: must be > 0, got %s", field, d)
	}
	return d, nil
}

// Resolve materializes the validated ResolvedReadiness from this block plus env
// overrides plus the named fallbacks. Nil-safe (nil → all fallbacks).
// readinessSpec is one readiness field: its config-key name, its CHARLY_READINESS_*
// env override, its built-in fallback, and accessors for the config value (in) and
// the resolved field pointer (out). readinessSpecs is the SINGLE source for the field
// set (R3): readinessResolve READS it (config/env/fallback → resolved); readinessPluginEnv
// WRITES it (resolved → the CHARLY_READINESS_* env an out-of-process plugin re-reads).
type readinessSpec struct {
	name, env string
	fb        time.Duration
	cfg       func(*ReadinessConfig) string
	dst       func(*ResolvedReadiness) *time.Duration
}

var readinessSpecs = []readinessSpec{
	{"poll_interval_local", "CHARLY_READINESS_POLL_LOCAL", readinessIntervalLocalFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalLocal }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalLocal }},
	{"poll_interval_remote", "CHARLY_READINESS_POLL_REMOTE", readinessIntervalRemoteFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalRemote }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalRemote }},
	{"poll_interval_heavy", "CHARLY_READINESS_POLL_HEAVY", readinessIntervalHeavyFallback, func(rc *ReadinessConfig) string { return rc.PollIntervalHeavy }, func(rr *ResolvedReadiness) *time.Duration { return &rr.IntervalHeavy }},
	{"per_attempt", "CHARLY_READINESS_PER_ATTEMPT", readinessPerAttemptFallback, func(rc *ReadinessConfig) string { return rc.PerAttempt }, func(rr *ResolvedReadiness) *time.Duration { return &rr.PerAttempt }},
	{"per_attempt_heavy", "CHARLY_READINESS_PER_ATTEMPT_HEAVY", readinessPerAttemptHeavyFallback, func(rc *ReadinessConfig) string { return rc.PerAttemptHeavy }, func(rr *ResolvedReadiness) *time.Duration { return &rr.PerAttemptHeavy }},
	{"no_progress", "CHARLY_READINESS_NO_PROGRESS", readinessNoProgressFallback, func(rc *ReadinessConfig) string { return rc.NoProgress }, func(rr *ResolvedReadiness) *time.Duration { return &rr.NoProgress }},
	{"absolute_cap", "CHARLY_READINESS_ABSOLUTE_CAP", readinessAbsoluteCapFallback, func(rc *ReadinessConfig) string { return rc.AbsoluteCap }, func(rr *ResolvedReadiness) *time.Duration { return &rr.AbsoluteCap }},
	{"stop_grace", "CHARLY_READINESS_STOP_GRACE", readinessStopGraceFallback, func(rc *ReadinessConfig) string { return rc.StopGrace }, func(rr *ResolvedReadiness) *time.Duration { return &rr.StopGrace }},
}

func readinessResolve(rc *ReadinessConfig) (ResolvedReadiness, error) {
	if rc == nil {
		rc = &ReadinessConfig{}
	}
	var rr ResolvedReadiness
	for _, s := range readinessSpecs {
		d, err := resolveReadinessDuration(s.name, s.env, s.cfg(rc), s.fb)
		if err != nil {
			return ResolvedReadiness{}, err
		}
		*s.dst(&rr) = d
	}
	if err := rr.ValidateOrdering(); err != nil {
		return ResolvedReadiness{}, err
	}
	return rr, nil
}

// readinessPluginEnv emits the CHARLY_READINESS_* entries (KEY=VALUE) for the host's
// RESOLVED readiness, so an out-of-process plugin — which cannot LoadUnified to read
// the project's defaults.readiness — re-reads the host-resolved bounds via its own
// readinessResolve. Appended to a plugin's spawn env; durations round-trip through
// String() ⟷ resolveReadinessDuration. Resolved values that already came from a host
// CHARLY_READINESS_* env re-emit identically (no ambiguity on the duplicate key).
func readinessPluginEnv() []string {
	rr := loadedReadiness()
	out := make([]string, 0, len(readinessSpecs))
	for _, s := range readinessSpecs {
		out = append(out, s.env+"="+(*s.dst(&rr)).String())
	}
	return out
}

// loadedReadiness resolves the project's readiness bounds ONCE (config + env,
// validated). A site deep in the executors that has no threaded ResolvedReadiness
// calls this. On absence/error it falls back to the named constants (always
// safe + never-hang) with a logged warning — a bad config block degrades to the
// built-in defaults rather than breaking every deploy.
var (
	readinessOnce   sync.Once
	readinessCached ResolvedReadiness
)

func loadedReadiness() ResolvedReadiness {
	readinessOnce.Do(func() {
		var def *ReadinessConfig
		if uf, ok, err := LoadUnified("."); err == nil && ok && uf != nil {
			def = uf.Defaults.Readiness
		}
		rr, err := readinessResolve(def)
		if err != nil {
			fmt.Fprintf(os.Stderr, "readiness config invalid (%v) — using built-in defaults\n", err)
			rr, _ = readinessResolve(nil)
		}
		readinessCached = rr
	})
	return readinessCached
}
