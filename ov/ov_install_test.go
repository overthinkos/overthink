package main

import (
	"context"
	"strings"
	"testing"
)

// recordingExecutor is a DeployExecutor that returns a single canned RunCapture
// stdout (the guest's `ov version` probe output) and records every PutFile call,
// so syncOvIntoGuest's decision (use the system ov vs scp a /tmp copy) can be
// asserted without a real pod/VM. Distinct from evalrun_test.go's fakeExecutor.
type recordingExecutor struct {
	captureOut  string
	putFiles    []putFileCall
	runSystems  []string
	runCaptures []string
}

type putFileCall struct {
	local, remote string
	mode          uint32
	ownerRoot     bool
}

func (e *recordingExecutor) RunCapture(_ context.Context, script string) (string, string, int, error) {
	e.runCaptures = append(e.runCaptures, script)
	return e.captureOut, "", 0, nil
}
func (e *recordingExecutor) PutFile(_ context.Context, local, remote string, mode uint32, ownerRoot bool, _ EmitOpts) error {
	e.putFiles = append(e.putFiles, putFileCall{local, remote, mode, ownerRoot})
	return nil
}
func (e *recordingExecutor) RunSystem(_ context.Context, script string, _ EmitOpts) error {
	e.runSystems = append(e.runSystems, script)
	return nil
}
func (e *recordingExecutor) Venue() string                                         { return "rec" }
func (e *recordingExecutor) Kind() string                                          { return "vm" }
func (e *recordingExecutor) RunUser(_ context.Context, _ string, _ EmitOpts) error { return nil }
func (e *recordingExecutor) RunBuilder(_ context.Context, _ BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExecutor) GetFile(_ context.Context, _ string, _ bool, _ EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExecutor) ResolveHome(_ context.Context, _ string) (string, error) {
	return "/home/fake", nil
}

// TestSyncOvIntoGuest covers the unified host→guest ov resolver: use the venue's
// SYSTEM ov when it is current (>= host by CalVer; never shadow, never
// downgrade); scp the host binary to a /tmp path (outside $PATH) ONLY when the
// venue ov is absent or older.
func TestSyncOvIntoGuest(t *testing.T) {
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()
	BuildCalVer = "2026.154.1000" // host identity

	tests := []struct {
		name       string
		captureOut string // guest `ov version` probe output
		wantCmd    string // "ov" or the /tmp path
		wantPush   bool   // scp of a /tmp copy
	}{
		{"venue older → /tmp copy", "2026.154.900\n", "/tmp/ov-2026.154.1000", true},
		{"venue absent → /tmp copy", "", "/tmp/ov-2026.154.1000", true},
		{"venue equal → system ov (no scp)", "2026.154.1000\n", "ov", false},
		{"venue strictly newer → system ov (no downgrade)", "2026.155.10\n", "ov", false},
		{"venue 'unknown' (unstamped) → /tmp copy", "unknown\n", "/tmp/ov-2026.154.1000", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &recordingExecutor{captureOut: tt.captureOut}
			cmd, err := syncOvIntoGuest(context.Background(), ex, EmitOpts{})
			if err != nil {
				t.Fatalf("syncOvIntoGuest: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("ovCmd = %q, want %q", cmd, tt.wantCmd)
			}
			gotPush := false
			for _, p := range ex.putFiles {
				if strings.HasPrefix(p.remote, "/tmp/ov-") {
					gotPush = true
					if p.remote != tt.wantCmd {
						t.Errorf("pushed to %q, want %q", p.remote, tt.wantCmd)
					}
					if p.ownerRoot || p.mode != 0o755 {
						t.Errorf("temp push ownerRoot=%v mode=%o, want non-root + 0755", p.ownerRoot, p.mode)
					}
				}
				if p.remote == "/usr/local/bin/ov" {
					t.Errorf("must NEVER write /usr/local/bin/ov (shadows the system ov); got %+v", p)
				}
			}
			if gotPush != tt.wantPush {
				t.Errorf("temp push=%v, want %v (putFiles=%+v)", gotPush, tt.wantPush, ex.putFiles)
			}
		})
	}
}
