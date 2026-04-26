package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubProber returns a fixed (score, total) on each call. Concurrent-safe.
type stubProber struct {
	mu    sync.Mutex
	score int
	total int
	err   error
}

func (s *stubProber) probe(_ context.Context) (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.score, s.total, s.err
}

func (s *stubProber) set(score, total int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.score = score
	s.total = total
	s.err = err
}

// TestProgressWatchdog_NoImprovementTimeoutFires — score stuck at 2,
// watchdog with no-improvement window of 50ms must fire OnTimeout.
func TestProgressWatchdog_NoImprovementTimeoutFires(t *testing.T) {
	stub := &stubProber{score: 2, total: 5}
	timeoutFired := make(chan string, 1)
	wd := &ProgressWatchdog{
		CheckInterval:        10 * time.Millisecond,
		NoImprovementTimeout: 50 * time.Millisecond,
		Probe:                stub.probe,
		OnTimeout: func(reason string) {
			select {
			case timeoutFired <- reason:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		wd.Run(ctx)
		close(done)
	}()

	select {
	case reason := <-timeoutFired:
		if !strings.Contains(reason, "no scoring progress") {
			t.Errorf("reason should mention 'no scoring progress', got %q", reason)
		}
		if !strings.Contains(reason, "2/5") {
			t.Errorf("reason should include current score 2/5, got %q", reason)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("OnTimeout did not fire within 500ms")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit after ctx cancel")
	}
}

// TestProgressWatchdog_ImprovementResetsTimer — score increases on every
// tick; OnTimeout must NOT fire even after 5x the no-improvement window.
func TestProgressWatchdog_ImprovementResetsTimer(t *testing.T) {
	var ticks int32
	stub := &stubProber{score: 0, total: 10}
	timeoutFired := make(chan struct{}, 1)
	wd := &ProgressWatchdog{
		CheckInterval:        10 * time.Millisecond,
		NoImprovementTimeout: 30 * time.Millisecond,
		Probe: func(ctx context.Context) (int, int, error) {
			n := atomic.AddInt32(&ticks, 1)
			stub.set(int(n), 10, nil) // score increases every tick
			return stub.probe(ctx)
		},
		OnTimeout: func(reason string) {
			select {
			case timeoutFired <- struct{}{}:
			default:
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	wd.Run(ctx)

	select {
	case <-timeoutFired:
		t.Errorf("OnTimeout fired despite continuous improvement")
	default:
	}
	if atomic.LoadInt32(&ticks) < 5 {
		t.Errorf("expected at least 5 probe ticks in 200ms, got %d", ticks)
	}
}

// TestProgressWatchdog_ContextCancelExitsCleanly — Run must return
// promptly when ctx is cancelled (no leaked goroutine).
func TestProgressWatchdog_ContextCancelExitsCleanly(t *testing.T) {
	wd := &ProgressWatchdog{
		CheckInterval:        100 * time.Millisecond,
		NoImprovementTimeout: 1 * time.Second,
		Probe: func(_ context.Context) (int, int, error) {
			return 0, 5, nil
		},
		OnTimeout: func(string) {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		wd.Run(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not exit within 200ms after cancel")
	}
}

// TestProgressWatchdog_OnTickFiresEveryCheckInterval — count tick
// callbacks over a window proportional to CheckInterval.
func TestProgressWatchdog_OnTickFiresEveryCheckInterval(t *testing.T) {
	var tickCount int32
	stub := &stubProber{score: 1, total: 5}
	wd := &ProgressWatchdog{
		CheckInterval:        20 * time.Millisecond,
		NoImprovementTimeout: 0, // disabled — testing tick rate only
		Probe:                stub.probe,
		OnTick: func(elapsed time.Duration, score, total int, lastImprovedAt time.Time) {
			atomic.AddInt32(&tickCount, 1)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Millisecond)
	defer cancel()
	wd.Run(ctx)

	got := atomic.LoadInt32(&tickCount)
	// Expect ~5 ticks (105ms / 20ms = 5.25). Allow 4-6 for scheduler jitter.
	if got < 4 || got > 6 {
		t.Errorf("expected 4-6 ticks in ~5x CheckInterval, got %d", got)
	}
}

// TestProgressWatchdog_DisabledByZeroCheckInterval — Run with
// CheckInterval=0 returns immediately without firing anything.
func TestProgressWatchdog_DisabledByZeroCheckInterval(t *testing.T) {
	var tickFired, timeoutFired bool
	wd := &ProgressWatchdog{
		CheckInterval:        0,
		NoImprovementTimeout: 30 * time.Millisecond,
		Probe: func(_ context.Context) (int, int, error) {
			return 0, 0, nil
		},
		OnTick:    func(time.Duration, int, int, time.Time) { tickFired = true },
		OnTimeout: func(string) { timeoutFired = true },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	wd.Run(ctx)

	if tickFired {
		t.Errorf("OnTick should not fire when CheckInterval=0")
	}
	if timeoutFired {
		t.Errorf("OnTimeout should not fire when CheckInterval=0")
	}
}

// TestProgressWatchdog_DisabledByZeroNoImprovementTimeout — score stuck
// but NoImprovementTimeout=0 means no termination ever (logging-only mode).
func TestProgressWatchdog_DisabledByZeroNoImprovementTimeout(t *testing.T) {
	stub := &stubProber{score: 2, total: 5}
	timeoutFired := make(chan struct{}, 1)
	wd := &ProgressWatchdog{
		CheckInterval:        10 * time.Millisecond,
		NoImprovementTimeout: 0, // disabled
		Probe:                stub.probe,
		OnTimeout: func(string) {
			select {
			case timeoutFired <- struct{}{}:
			default:
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	wd.Run(ctx)

	select {
	case <-timeoutFired:
		t.Errorf("OnTimeout fired with NoImprovementTimeout=0 — should be disabled")
	default:
	}
}

// TestProgressWatchdog_ProbeErrorDoesNotAdvanceTimer — errors from
// Probe must not count as "no progress"; the no-improvement window
// only advances on successful probes that report a stuck score.
func TestProgressWatchdog_ProbeErrorDoesNotAdvanceTimer(t *testing.T) {
	probeErr := errors.New("transient probe failure")
	wd := &ProgressWatchdog{
		CheckInterval:        10 * time.Millisecond,
		NoImprovementTimeout: 30 * time.Millisecond,
		Probe: func(_ context.Context) (int, int, error) {
			return 0, 0, probeErr
		},
		OnTimeout: func(reason string) {
			t.Errorf("OnTimeout fired despite all probes erroring: %s", reason)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	wd.Run(ctx)
}
