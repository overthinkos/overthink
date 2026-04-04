package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// overlayDaemonSession is the tmux session name for the overlay daemon.
const overlayDaemonSession = "ov-overlay-daemon"

// WlOverlayCmd groups overlay management subcommands.
type WlOverlayCmd struct {
	Show   WlOverlayShowCmd   `cmd:"" help:"Show an overlay on the desktop"`
	Hide   WlOverlayHideCmd   `cmd:"" help:"Hide an overlay by name (or all)"`
	List   WlOverlayListCmd   `cmd:"" help:"List active overlays"`
	Status WlOverlayStatusCmd `cmd:"" help:"Check overlay daemon status"`
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkOverlayAvailable(engine, name); err != nil {
		return err
	}
	if err := ensureOverlayDaemon(engine, name); err != nil {
		return err
	}
	return execWlCmd(engine, name, buildOverlayShowArgs(c))
}

func (c *WlOverlayHideCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
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
	return execWlCmd(engine, name, args)
}

func (c *WlOverlayListCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execWlCmd(engine, name, "ov-overlay list")
}

func (c *WlOverlayStatusCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execWlCmd(engine, name, "ov-overlay status")
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

// checkOverlayAvailable verifies ov-overlay is installed in the container.
func checkOverlayAvailable(engine, containerName string) error {
	if engine == "" {
		// Local mode
		if _, err := exec.LookPath("ov-overlay"); err != nil {
			return fmt.Errorf("ov-overlay not found in PATH (install the wl-overlay layer)")
		}
		return nil
	}
	cmd := exec.Command(engine, "exec", containerName, "which", "ov-overlay")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ov-overlay not available in container %s (add the wl-overlay layer to your image)", containerName)
	}
	return nil
}

// ensureOverlayDaemon starts the overlay daemon in a tmux session if not already running.
func ensureOverlayDaemon(engine, containerName string) error {
	// Check if daemon socket already exists (daemon running)
	if execWlCmdSilent(engine, containerName, "test -S /tmp/ov-overlay.sock") == nil {
		return nil
	}

	// Need tmux to host the daemon
	if engine != "" {
		if err := checkTmuxInstalled(engine, containerName); err != nil {
			return err
		}
	}

	// Clean up any stale socket
	_ = execWlCmdSilent(engine, containerName, "rm -f /tmp/ov-overlay.sock")

	// Start the daemon in a tmux session.
	// The daemon needs Wayland environment variables, same as other wl commands.
	daemonCmd := wlShellCmd("ov-overlay daemon")

	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("tmux", "new-session", "-d", "-s", overlayDaemonSession, "sh", "-c", daemonCmd)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "tmux", "new-session", "-d", "-s", overlayDaemonSession, "sh", "-c", daemonCmd)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting overlay daemon: %w", err)
	}

	// Wait for socket to appear
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		if execWlCmdSilent(engine, containerName, "test -S /tmp/ov-overlay.sock") == nil {
			return nil
		}
	}
	return fmt.Errorf("overlay daemon started but socket not ready after 5s")
}

// overlayAutoName generates a unique overlay name from the type.
func overlayAutoName(overlayType string) string {
	return fmt.Sprintf("%s-%d", overlayType, time.Now().UnixMilli()%100000)
}
