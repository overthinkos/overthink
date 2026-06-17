package main

import (
	"strings"
	"testing"
)

// TestWlShellCmdSourcesCompositorEnv proves wlShellCmd sources the LIVE
// compositor's session env (incl. DBUS_SESSION_BUS_ADDRESS for kdotool) from
// the running compositor process, not just the historical WAYLAND_DISPLAY
// fallback. Without the env-sourcing change this fails: the selkies-kde
// startplasma-wayland session runs under dbus-run-session on a random
// /tmp/dbus-XXXXXX bus, so a baked-ENV exec would send kdotool to the wrong bus.
func TestWlShellCmdSourcesCompositorEnv(t *testing.T) {
	got := wlShellCmd("kdotool search ''")
	for _, want := range []string{
		"kwin_wayland",             // probes for KWin among the compositors
		"DBUS_SESSION_BUS_ADDRESS", // sources the real session bus (kdotool needs it)
		"/proc/$__p/environ",       // from the live compositor process
		"WAYLAND_DISPLAY",          // fallback still present
	} {
		if !strings.Contains(got, want) {
			t.Errorf("wlShellCmd output missing %q:\n%s", want, got)
		}
	}
	// Regression guard: the /proc/environ exports MUST be applied with `eval`,
	// not `check` (a non-existent command). A prior `eval`→`check` corruption
	// silently dropped the sourced WAYLAND_DISPLAY=wayland-1 + the live
	// DBUS_SESSION_BUS_ADDRESS, so KWin wl probes (wtype hit wayland-0, kdotool
	// hit the baked /tmp/dbus-session) all failed on selkies-kde. Proven on a live
	// pod: with `eval` both wtype and kdotool reach the KWin session.
	if !strings.Contains(got, `eval "$(tr `) {
		t.Errorf("wlShellCmd must source /proc/environ via eval, got:\n%s", got)
	}
	if strings.Contains(got, `check "$(`) {
		t.Errorf("wlShellCmd uses `check` where `eval` is required (the env-sourcing typo):\n%s", got)
	}
	if !strings.HasSuffix(got, "&& kdotool search ''") {
		t.Errorf("wlShellCmd wrong suffix: %s", got)
	}
}

// TestDetectCompositor proves the KWin-vs-wlroots routing decision: KWin is
// detected when kwin_wayland is PID-present, else the venue is treated as a
// wlroots compositor (sway/labwc). This drives every per-method backend choice.
func TestDetectCompositor(t *testing.T) {
	kwin := &fakeExecutor{responses: []fakeResponse{
		{matchPrefix: "pgrep -x kwin_wayland", exit: 0},
	}}
	if got := detectCompositor(kwin); got != "kwin" {
		t.Errorf("detectCompositor(kwin running) = %q, want kwin", got)
	}

	wlroots := &fakeExecutor{responses: []fakeResponse{
		{matchPrefix: "pgrep -x kwin_wayland", exit: 1}, // KWin not running
	}}
	if got := detectCompositor(wlroots); got != "wlroots" {
		t.Errorf("detectCompositor(no KWin) = %q, want wlroots", got)
	}
}

// TestKdotoolSearchAction proves window-management on KWin routes through a
// `kdotool search --name <title> <verb> [args]` chain (KWin scripting), with
// the title shell-quoted and extra args appended.
func TestKdotoolSearchAction(t *testing.T) {
	ex := &fakeExecutor{responses: []fakeResponse{{matchPrefix: "kdotool", exit: 0}}}
	if err := kdotoolSearchAction(ex, "My Window", "windowstate", "--toggle", "FULLSCREEN"); err != nil {
		t.Fatalf("kdotoolSearchAction: %v", err)
	}
	if len(ex.calls) != 1 {
		t.Fatalf("expected 1 exec, got %d: %v", len(ex.calls), ex.calls)
	}
	want := "kdotool search --name 'My Window' windowstate --toggle FULLSCREEN"
	if !strings.Contains(ex.calls[0], want) {
		t.Errorf("kdotool command = %q, want substring %q", ex.calls[0], want)
	}
}

// TestErrKWinPointerUnsupported proves pointer methods fail on KWin with a
// clear, actionable error (not a hang or silent no-op) that names the method
// and points out which method groups ARE supported.
func TestErrKWinPointerUnsupported(t *testing.T) {
	err := errKWinPointerUnsupported("click")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"click", "KWin", "not supported", "Window management"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}
