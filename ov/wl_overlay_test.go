package main

import (
	"strings"
	"testing"
)

func TestOverlayDaemonSession(t *testing.T) {
	if overlayDaemonSession != "ov-overlay-daemon" {
		t.Errorf("overlayDaemonSession = %q, want %q", overlayDaemonSession, "ov-overlay-daemon")
	}
}

func TestOverlayAutoName(t *testing.T) {
	name := overlayAutoName("text")
	if name == "" {
		t.Error("overlayAutoName returned empty string")
	}
	if !strings.HasPrefix(name, "text-") {
		t.Errorf("overlayAutoName(%q) = %q, want prefix %q", "text", name, "text-")
	}

	name2 := overlayAutoName("fade")
	if !strings.HasPrefix(name2, "fade-") {
		t.Errorf("overlayAutoName(%q) = %q, want prefix %q", "fade", name2, "fade-")
	}
}

func TestBuildOverlayShowArgs(t *testing.T) {
	cmd := &WlOverlayShowCmd{
		Type:     "text",
		Text:     "Hello World",
		Name:     "intro",
		Bg:       "rgba(0,0,0,0.7)",
		Color:    "white",
		FontSize: 48,
		Position: "center",
		Opacity:  1.0,
		Seconds:  3,
	}
	args := buildOverlayShowArgs(cmd)
	if !strings.HasPrefix(args, "ov-overlay show") {
		t.Errorf("buildOverlayShowArgs missing prefix: %s", args)
	}
	if !strings.Contains(args, "--type 'text'") {
		t.Errorf("buildOverlayShowArgs missing --type: %s", args)
	}
	if !strings.Contains(args, "--text 'Hello World'") {
		t.Errorf("buildOverlayShowArgs missing --text: %s", args)
	}
	if !strings.Contains(args, "--name 'intro'") {
		t.Errorf("buildOverlayShowArgs missing --name: %s", args)
	}
	if !strings.Contains(args, "--bg") {
		t.Errorf("buildOverlayShowArgs missing --bg: %s", args)
	}
	// Default position (center) should be omitted
	if strings.Contains(args, "--position") {
		t.Errorf("buildOverlayShowArgs should omit default position: %s", args)
	}
	// Default color (white) should be omitted
	if strings.Contains(args, "--color") {
		t.Errorf("buildOverlayShowArgs should omit default color: %s", args)
	}
	// Default font-size (48) should be omitted
	if strings.Contains(args, "--font-size") {
		t.Errorf("buildOverlayShowArgs should omit default font-size: %s", args)
	}
	// Default opacity (1.0) should be omitted
	if strings.Contains(args, "--opacity") {
		t.Errorf("buildOverlayShowArgs should omit default opacity: %s", args)
	}
}

func TestBuildOverlayShowArgsNonDefaults(t *testing.T) {
	cmd := &WlOverlayShowCmd{
		Type:     "lower-third",
		Text:     "Speaker",
		Subtitle: "Developer",
		Position: "bottom",
		Color:    "yellow",
		FontSize: 32,
		Opacity:  0.8,
		Duration: "5s",
		Seconds:  3,
	}
	args := buildOverlayShowArgs(cmd)
	if !strings.Contains(args, "--subtitle 'Developer'") {
		t.Errorf("buildOverlayShowArgs missing --subtitle: %s", args)
	}
	if !strings.Contains(args, "--position 'bottom'") {
		t.Errorf("buildOverlayShowArgs missing --position: %s", args)
	}
	if !strings.Contains(args, "--color 'yellow'") {
		t.Errorf("buildOverlayShowArgs missing --color: %s", args)
	}
	if !strings.Contains(args, "--font-size 32") {
		t.Errorf("buildOverlayShowArgs missing --font-size: %s", args)
	}
	if !strings.Contains(args, "--opacity 0.80") {
		t.Errorf("buildOverlayShowArgs missing --opacity: %s", args)
	}
	if !strings.Contains(args, "--duration '5s'") {
		t.Errorf("buildOverlayShowArgs missing --duration: %s", args)
	}
}

func TestBuildOverlayShowArgsCountdown(t *testing.T) {
	cmd := &WlOverlayShowCmd{
		Type:    "countdown",
		Seconds: 5,
		Color:   "white",
		Opacity: 1.0,
	}
	args := buildOverlayShowArgs(cmd)
	if !strings.Contains(args, "--seconds 5") {
		t.Errorf("buildOverlayShowArgs missing --seconds: %s", args)
	}
}

func TestBuildOverlayShowArgsHighlight(t *testing.T) {
	cmd := &WlOverlayShowCmd{
		Type:    "highlight",
		Region:  "100,200,400,300",
		Color:   "white",
		Opacity: 1.0,
		Seconds: 3,
	}
	args := buildOverlayShowArgs(cmd)
	if !strings.Contains(args, "--region '100,200,400,300'") {
		t.Errorf("buildOverlayShowArgs missing --region: %s", args)
	}
}

func TestWlOverlayCmdSubcommands(t *testing.T) {
	// Structural test: verify WlOverlayCmd has all expected subcommands.
	var cmd WlOverlayCmd
	_ = cmd.Show
	_ = cmd.Hide
	_ = cmd.List
	_ = cmd.Status
}

func TestWlCmdHasOverlay(t *testing.T) {
	// Structural test: verify Overlay field exists on WlCmd.
	var cmd WlCmd
	_ = cmd.Overlay
	_ = cmd.Overlay.Show
	_ = cmd.Overlay.Hide
	_ = cmd.Overlay.List
	_ = cmd.Overlay.Status
}
