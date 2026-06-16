package main

import (
	"strings"
	"testing"
	"time"
)

func TestReadinessConfig_ResolveDefaults(t *testing.T) {
	rr, err := (*ReadinessConfig)(nil).Resolve()
	if err != nil {
		t.Fatalf("nil/default resolve must succeed: %v", err)
	}
	if rr.NoProgress != readinessNoProgressFallback || rr.AbsoluteCap != readinessAbsoluteCapFallback || rr.StopGrace != readinessStopGraceFallback {
		t.Fatalf("defaults wrong: %+v", rr)
	}
	if err := rr.validateOrdering(); err != nil {
		t.Fatalf("default ordering must be valid: %v", err)
	}
}

func TestReadinessConfig_ParseError(t *testing.T) {
	_, err := (&ReadinessConfig{NoProgress: "ninety"}).Resolve()
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("bad duration must error, got %v", err)
	}
}

func TestReadinessConfig_OrderingRejected(t *testing.T) {
	// poll_interval_heavy (120s) > no_progress (90s default) — the env-bypass case.
	_, err := (&ReadinessConfig{PollIntervalHeavy: "120s"}).Resolve()
	if err == nil || !strings.Contains(err.Error(), "no_progress") {
		t.Fatalf("interval>no_progress must be rejected, got %v", err)
	}
	// stop_grace > absolute_cap.
	_, err = (&ReadinessConfig{StopGrace: "40m"}).Resolve()
	if err == nil || !strings.Contains(err.Error(), "absolute_cap") {
		t.Fatalf("stop_grace>absolute_cap must be rejected, got %v", err)
	}
}

func TestReadinessConfig_ValidOverride(t *testing.T) {
	rr, err := (&ReadinessConfig{NoProgress: "5m", AbsoluteCap: "1h"}).Resolve()
	if err != nil {
		t.Fatalf("valid override: %v", err)
	}
	if rr.NoProgress != 5*time.Minute || rr.AbsoluteCap != time.Hour {
		t.Fatalf("override not applied: %+v", rr)
	}
}
