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

// ReadinessConfig is the YAML/CUE shape. All fields are duration strings
// ("90s", "30m"); empty → the named fallback constant.
type ReadinessConfig struct {
	PollIntervalLocal  string `yaml:"poll_interval_local,omitempty" json:"poll_interval_local,omitempty"`
	PollIntervalRemote string `yaml:"poll_interval_remote,omitempty" json:"poll_interval_remote,omitempty"`
	PollIntervalHeavy  string `yaml:"poll_interval_heavy,omitempty" json:"poll_interval_heavy,omitempty"`
	PerAttempt         string `yaml:"per_attempt,omitempty" json:"per_attempt,omitempty"`
	NoProgress         string `yaml:"no_progress,omitempty" json:"no_progress,omitempty"`
	AbsoluteCap        string `yaml:"absolute_cap,omitempty" json:"absolute_cap,omitempty"`
	StopGrace          string `yaml:"stop_grace,omitempty" json:"stop_grace,omitempty"`
}

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
func (rc *ReadinessConfig) Resolve() (ResolvedReadiness, error) {
	if rc == nil {
		rc = &ReadinessConfig{}
	}
	var rr ResolvedReadiness
	specs := []struct {
		name, env, val string
		fb             time.Duration
		dst            *time.Duration
	}{
		{"poll_interval_local", "CHARLY_READINESS_POLL_LOCAL", rc.PollIntervalLocal, readinessIntervalLocalFallback, &rr.IntervalLocal},
		{"poll_interval_remote", "CHARLY_READINESS_POLL_REMOTE", rc.PollIntervalRemote, readinessIntervalRemoteFallback, &rr.IntervalRemote},
		{"poll_interval_heavy", "CHARLY_READINESS_POLL_HEAVY", rc.PollIntervalHeavy, readinessIntervalHeavyFallback, &rr.IntervalHeavy},
		{"per_attempt", "CHARLY_READINESS_PER_ATTEMPT", rc.PerAttempt, readinessPerAttemptFallback, &rr.PerAttempt},
		{"no_progress", "CHARLY_READINESS_NO_PROGRESS", rc.NoProgress, readinessNoProgressFallback, &rr.NoProgress},
		{"absolute_cap", "CHARLY_READINESS_ABSOLUTE_CAP", rc.AbsoluteCap, readinessAbsoluteCapFallback, &rr.AbsoluteCap},
		{"stop_grace", "CHARLY_READINESS_STOP_GRACE", rc.StopGrace, readinessStopGraceFallback, &rr.StopGrace},
	}
	for _, s := range specs {
		d, err := resolveReadinessDuration(s.name, s.env, s.val, s.fb)
		if err != nil {
			return ResolvedReadiness{}, err
		}
		*s.dst = d
	}
	if err := rr.validateOrdering(); err != nil {
		return ResolvedReadiness{}, err
	}
	return rr, nil
}

// validateOrdering enforces the bound ordering on the RESOLVED set (must run
// post-env, not on the raw YAML — an env override must not slip a bad bound
// through): each interval <= no_progress; no_progress <= absolute_cap;
// poll_interval_local <= stop_grace <= absolute_cap.
func (rr ResolvedReadiness) validateOrdering() error {
	for _, iv := range []struct {
		n string
		d time.Duration
	}{
		{"poll_interval_local", rr.IntervalLocal},
		{"poll_interval_remote", rr.IntervalRemote},
		{"poll_interval_heavy", rr.IntervalHeavy},
	} {
		if iv.d > rr.NoProgress {
			return fmt.Errorf("readiness: %s (%s) must be <= no_progress (%s)", iv.n, iv.d, rr.NoProgress)
		}
	}
	if rr.NoProgress > rr.AbsoluteCap {
		return fmt.Errorf("readiness: no_progress (%s) must be <= absolute_cap (%s)", rr.NoProgress, rr.AbsoluteCap)
	}
	if rr.StopGrace > rr.AbsoluteCap {
		return fmt.Errorf("readiness: stop_grace (%s) must be <= absolute_cap (%s)", rr.StopGrace, rr.AbsoluteCap)
	}
	if rr.StopGrace < rr.IntervalLocal {
		return fmt.Errorf("readiness: stop_grace (%s) must be >= poll_interval_local (%s)", rr.StopGrace, rr.IntervalLocal)
	}
	return nil
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
		rr, err := def.Resolve()
		if err != nil {
			fmt.Fprintf(os.Stderr, "readiness config invalid (%v) — using built-in defaults\n", err)
			rr, _ = (*ReadinessConfig)(nil).Resolve()
		}
		readinessCached = rr
	})
	return readinessCached
}
