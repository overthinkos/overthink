package main

// harness_watchdog.go — score-progress watchdog for the AI runner.
//
// Round 3 of the harness composition cutover replaced the hardcoded
// 30-minute per-iteration wall-clock cap (which contradicted the
// score's "Take all the time you need" prompt) with this watchdog,
// which bounds each iteration by SCORING PROGRESS rather than wall
// clock.
//
// Behavior per the spec:
//
//   1. Every CheckInterval (default 5 min), Run probes the live
//      deployments via the supplied Prober. The prober returns the
//      current (score, total) snapshot — typically by calling
//      RunRecipeScenariosLive against the iter's in-scope scenarios.
//   2. OnTick fires after every probe with the current observation
//      (host-side stderr logging, NOT into any AI-visible surface).
//   3. If the score has not increased in NoImprovementTimeout (default
//      30 min), Run invokes OnTimeout with a reason string. Callers
//      typically wire OnTimeout to cancel the runner's context, which
//      terminates the AI subprocess.
//
// The watchdog is HIDDEN from the AI by construction:
//   - Runs in the harness Go process, not in any tool the AI invokes.
//   - Adds no token to the prompt, no field to ${SCENARIOS}/${RECIPES},
//     no entry in `ov harness scope`, no log line in NOTES.md.
//   - The AI's view of the iteration is unchanged from before Round 3.
//
// `Run` exits cleanly when the runner's context is cancelled (the
// common case: the AI subprocess returned and the caller called
// cancelRunner()).

import (
	"context"
	"fmt"
	"time"
)

// Prober is the function signature the watchdog uses to sample the
// current score. Concrete implementations call RunRecipeScenariosLive
// against the iter's scenarios; tests pass a fake.
//
// Returns (score, total, err). On err, Run logs the error via
// OnTickError (or skips the tick) but does NOT count it as
// "no progress" — a transient probe failure (container missing during
// AI rebuild, podman lock contention, etc.) shouldn't trigger a false
// timeout.
type Prober func(ctx context.Context) (score, total int, err error)

// ProgressWatchdog ticks at CheckInterval, samples the current score
// via Prober, fires OnTick for observability, and fires OnTimeout
// when the score has not increased in NoImprovementTimeout.
type ProgressWatchdog struct {
	CheckInterval        time.Duration // default 5m (caller applies)
	NoImprovementTimeout time.Duration // default 30m (caller applies)

	// Probe samples the current iter score. Required.
	Probe Prober

	// OnTick fires after every probe with the current observation.
	// Optional. Receives wall-clock elapsed since Run was called,
	// the (score, total) snapshot, and the timestamp of the last
	// score increase (zero-valued if the score has never improved).
	OnTick func(elapsed time.Duration, score, total int, lastImprovedAt time.Time)

	// OnTickError fires when Probe returns an error. Optional. The
	// reason is the error string. Callers typically log to stderr.
	OnTickError func(err error)

	// OnTimeout fires when the score has not improved in
	// NoImprovementTimeout. Required for the watchdog to be useful;
	// callers typically wire it to cancel the runner's context.
	OnTimeout func(reason string)
}

// Run blocks until ctx is done OR OnTimeout has fired. After OnTimeout
// fires, Run continues to ctx.Done() so the goroutine exits cleanly
// alongside the cancelled runner.
//
// If NoImprovementTimeout <= 0, the watchdog never times out — it just
// emits OnTick for observability. If CheckInterval <= 0, Run returns
// immediately (watchdog disabled).
func (w *ProgressWatchdog) Run(ctx context.Context) {
	if w.CheckInterval <= 0 {
		return
	}
	ticker := time.NewTicker(w.CheckInterval)
	defer ticker.Stop()

	start := time.Now()
	bestScore := -1 // sentinel: no probe yet
	var lastImprovedAt time.Time
	timedOut := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(start)
			score, total, err := w.Probe(ctx)
			if err != nil {
				if w.OnTickError != nil {
					w.OnTickError(err)
				}
				// Probe failure does NOT advance the no-improvement
				// timer's "last improved" — it's neither progress nor
				// regress. We just skip this tick.
				continue
			}
			improved := score > bestScore
			if improved {
				bestScore = score
				lastImprovedAt = time.Now()
			}
			if w.OnTick != nil {
				w.OnTick(elapsed, score, total, lastImprovedAt)
			}
			// Timeout check — only after at least one improvement
			// recorded. If lastImprovedAt is zero (the AI has never
			// scored anything), wait until elapsed exceeds
			// NoImprovementTimeout to avoid trivial-zero-baseline races.
			if timedOut || w.NoImprovementTimeout <= 0 || w.OnTimeout == nil {
				continue
			}
			var idle time.Duration
			if lastImprovedAt.IsZero() {
				idle = elapsed
			} else {
				idle = time.Since(lastImprovedAt)
			}
			if idle >= w.NoImprovementTimeout {
				reason := fmt.Sprintf(
					"no scoring progress for %s (last improvement %s, current score %d/%d)",
					idle.Round(time.Second),
					lastImprovedSuffix(lastImprovedAt, start),
					score, total,
				)
				w.OnTimeout(reason)
				timedOut = true
				// Don't return — let ctx.Done() unwind us cleanly so
				// the caller's cancelRunner() takes effect first.
			}
		}
	}
}

// lastImprovedSuffix renders the lastImprovedAt timestamp as either
// "at HH:MM:SS" (if any improvement was observed) or "never observed
// (iteration started at HH:MM:SS)". Used by OnTimeout reason strings.
func lastImprovedSuffix(lastImprovedAt, start time.Time) string {
	if lastImprovedAt.IsZero() {
		return fmt.Sprintf("never observed (iteration started at %s)", start.Format("15:04:05"))
	}
	return "at " + lastImprovedAt.Format("15:04:05")
}
