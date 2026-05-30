package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// overlayDaemonSession is the tmux session name for the overlay daemon.
const overlayDaemonSession = "ov-overlay-daemon"

// WlOverlayCmd groups overlay management subcommands.
type WlOverlayCmd struct {
	Hide   WlOverlayHideCmd   `cmd:"" help:"Hide an overlay by name (or all)"`
	List   WlOverlayListCmd   `cmd:"" help:"List active overlays"`
	Show   WlOverlayShowCmd   `cmd:"" help:"Show an overlay on the desktop"`
	Status WlOverlayStatusCmd `cmd:"" help:"Show overlay daemon status"`
}

// WlOverlayShowCmd creates and displays an overlay.
type WlOverlayShowCmd struct {
	Image    string  `arg:"" help:"Image name (use . for local)"`
	Type     string  `long:"type" required:"" enum:"text,lower-third,watermark,countdown,highlight,fade" help:"Overlay type"`
	Text     string  `long:"text" help:"Text content"`
	Subtitle string  `long:"subtitle" help:"Subtitle (lower-third)"`
	Name     string  `long:"name" default:"" help:"Overlay name (auto-generated if empty)"`
	Position string  `long:"position" default:"center" help:"center, top, bottom, top-left, top-right, bottom-left, bottom-right"`
	Bg       string  `long:"bg" default:"" help:"Background color (CSS rgba)"`
	Color    string  `long:"color" default:"white" help:"Text color"`
	FontSize int     `long:"font-size" default:"48" help:"Font size in pixels"`
	Opacity  float64 `long:"opacity" default:"1.0" help:"Overall opacity (0.0-1.0)"`
	Duration string  `long:"duration" default:"" help:"Auto-hide after duration (e.g. 5s, 1m)"`
	Seconds  int     `long:"seconds" default:"3" help:"Countdown seconds"`
	Region   string  `long:"region" default:"" help:"Highlight region 'X,Y,W,H'"`
	Instance string  `short:"i" long:"instance" help:"Instance name"`
}

// WlOverlayHideCmd hides one or all overlays.
type WlOverlayHideCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Name     string `long:"name" default:"" help:"Overlay name to hide"`
	All      bool   `long:"all" help:"Hide all overlays"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// WlOverlayListCmd lists active overlays.
type WlOverlayListCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// WlOverlayStatusCmd checks if the overlay daemon is running.
type WlOverlayStatusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlOverlayShowCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkOverlayAvailable(venue.Exec); err != nil {
		return err
	}
	if err := ensureOverlayDaemon(venue.Exec); err != nil {
		return err
	}
	return execWlCmd(venue.Exec, buildOverlayShowArgs(c))
}

func (c *WlOverlayHideCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !c.All && c.Name == "" {
		return fmt.Errorf("specify --name or --all")
	}
	args := "ov-overlay hide"
	if c.All {
		args += " --all"
	} else {
		args += " --name " + shellQuote(c.Name)
	}
	return execWlCmd(venue.Exec, args)
}

func (c *WlOverlayListCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execWlCmd(venue.Exec, "ov-overlay list")
}

func (c *WlOverlayStatusCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execWlCmd(venue.Exec, "ov-overlay status")
}

// buildOverlayShowArgs constructs the ov-overlay show command string from flags.
func buildOverlayShowArgs(c *WlOverlayShowCmd) string {
	var parts []string
	parts = append(parts, "ov-overlay", "show")
	parts = append(parts, "--type", shellQuote(c.Type))
	if c.Text != "" {
		parts = append(parts, "--text", shellQuote(c.Text))
	}
	if c.Subtitle != "" {
		parts = append(parts, "--subtitle", shellQuote(c.Subtitle))
	}
	if c.Name != "" {
		parts = append(parts, "--name", shellQuote(c.Name))
	}
	if c.Position != "" && c.Position != "center" {
		parts = append(parts, "--position", shellQuote(c.Position))
	}
	if c.Bg != "" {
		parts = append(parts, "--bg", shellQuote(c.Bg))
	}
	if c.Color != "" && c.Color != "white" {
		parts = append(parts, "--color", shellQuote(c.Color))
	}
	if c.FontSize != 0 && c.FontSize != 48 {
		parts = append(parts, "--font-size", fmt.Sprintf("%d", c.FontSize))
	}
	if c.Opacity != 1.0 {
		parts = append(parts, "--opacity", fmt.Sprintf("%.2f", c.Opacity))
	}
	if c.Duration != "" {
		parts = append(parts, "--duration", shellQuote(c.Duration))
	}
	if c.Seconds != 3 {
		parts = append(parts, "--seconds", fmt.Sprintf("%d", c.Seconds))
	}
	if c.Region != "" {
		parts = append(parts, "--region", shellQuote(c.Region))
	}
	return strings.Join(parts, " ")
}

// checkOverlayAvailable verifies ov-overlay is installed on the venue
// (container / VM / host).
func checkOverlayAvailable(ex DeployExecutor) error {
	if err := execWlCmdSilent(ex, "command -v ov-overlay >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("ov-overlay not available on the target (add the wl-overlay layer to your image, or install it on the host/VM)")
	}
	return nil
}

// ensureOverlayDaemon starts the overlay daemon in a tmux session on the venue
// if not already running. The daemon hosts the overlay socket; the tmux session
// keeps it alive across the short-lived `ov eval wl overlay` invocations.
func ensureOverlayDaemon(ex DeployExecutor) error {
	// Check if daemon socket already exists (daemon running)
	if execWlCmdSilent(ex, "test -S /tmp/ov-overlay.sock") == nil {
		return nil
	}

	// Need tmux to host the daemon (on every venue).
	if err := execWlCmdSilent(ex, "command -v tmux >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("tmux not available on the target (needed to host the overlay daemon)")
	}

	// Clean up any stale socket
	_ = execWlCmdSilent(ex, "rm -f /tmp/ov-overlay.sock")

	// Start the daemon in a tmux session. The daemon needs the same Wayland
	// environment exports as every other wl command (wlShellCmd).
	daemonCmd := wlShellCmd("ov-overlay daemon")
	startScript := fmt.Sprintf("tmux new-session -d -s %s sh -c %s",
		overlayDaemonSession, shellQuote(daemonCmd))
	if _, stderr, exit, err := ex.RunCapture(context.Background(), startScript); err != nil {
		return fmt.Errorf("starting overlay daemon: %w", err)
	} else if exit != 0 {
		return fmt.Errorf("starting overlay daemon: %s", strings.TrimSpace(stderr))
	}

	// Wait for socket to appear (bounded readiness probe, not a blind sleep).
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		if execWlCmdSilent(ex, "test -S /tmp/ov-overlay.sock") == nil {
			return nil
		}
	}
	return fmt.Errorf("overlay daemon started but socket not ready after 5s")
}

// overlayAutoName generates a unique overlay name from the type.
func overlayAutoName(overlayType string) string {
	return fmt.Sprintf("%s-%d", overlayType, time.Now().UnixMilli()%100000)
}
