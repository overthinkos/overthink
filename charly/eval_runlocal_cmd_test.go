package main

import (
	"context"
	"errors"
	"testing"
)

// TestDecideOverallExit_PlateauEndsRun is the regression test for the
// 2026-04-27 plateau-test-end-semantic fix. Before the fix, a phase
// that ended with ExitReason="plateau" silently advanced to the next
// phase via the comment-only line "Otherwise plateau or solved-all —
// both advance to next phase." That let the AI bypass any phase it
// stalled on and rack up easier wins later. The fix: plateau now ends
// the whole run, matching the user's "3 stalls × 30 min each, then
// the testing run should end" semantic and matching the cutover
// policy "stalled AI does not silently advance past failures."
//
// The decision predicate is now a pure function (decideOverallExit),
// so this test exercises every branch directly without setting up a
// full RunHarness fixture.
func TestDecideOverallExit_PlateauEndsRun(t *testing.T) {
	someCtxErr := errors.New("context cancelled")

	cases := []struct {
		name              string
		ctxErr            error
		phaseExitReason   string
		wantOverallReason string
		wantShouldBreak   bool
	}{
		{
			name:              "plateau-ends-run",
			ctxErr:            nil,
			phaseExitReason:   "plateau",
			wantOverallReason: "plateau",
			wantShouldBreak:   true,
		},
		{
			name:              "solved-all-continues-curriculum",
			ctxErr:            nil,
			phaseExitReason:   "solved-all",
			wantOverallReason: "",
			wantShouldBreak:   false,
		},
		{
			name:              "interrupted-via-ctx-ends-run",
			ctxErr:            someCtxErr,
			phaseExitReason:   "solved-all",
			wantOverallReason: "interrupted",
			wantShouldBreak:   true,
		},
		{
			name:              "ctx-cancel-overrides-plateau",
			ctxErr:            someCtxErr,
			phaseExitReason:   "plateau",
			wantOverallReason: "interrupted",
			wantShouldBreak:   true,
		},
		{
			name:              "ctx-cancel-overrides-empty",
			ctxErr:            context.Canceled,
			phaseExitReason:   "",
			wantOverallReason: "interrupted",
			wantShouldBreak:   true,
		},
		{
			name:              "unknown-reason-treated-as-continue",
			ctxErr:            nil,
			phaseExitReason:   "dry-run",
			wantOverallReason: "",
			wantShouldBreak:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotReason, gotBreak := decideOverallExit(tc.ctxErr, tc.phaseExitReason)
			if gotReason != tc.wantOverallReason {
				t.Errorf("overallExitReason: got %q, want %q",
					gotReason, tc.wantOverallReason)
			}
			if gotBreak != tc.wantShouldBreak {
				t.Errorf("shouldBreak: got %v, want %v",
					gotBreak, tc.wantShouldBreak)
			}
		})
	}
}
