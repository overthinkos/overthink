package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// WlCmd manages Wayland-native desktop interaction in running containers.
type WlCmd struct {
	Screenshot WlScreenshotCmd `cmd:"" help:"Capture desktop as PNG (via grim)"`
	Click      WlClickCmd      `cmd:"" help:"Click at x,y coordinates (via wlrctl)"`
	Type       WlTypeCmd       `cmd:"" help:"Type text as keyboard input (via wtype)"`
	Key        WlKeyCmd        `cmd:"" help:"Send a key press event (via wtype)"`
	Mouse      WlMouseCmd      `cmd:"" help:"Move mouse to x,y without clicking (via wlrctl)"`
	Status     WlStatusCmd     `cmd:"" help:"Check Wayland desktop and tool availability"`
	Windows    WlWindowsCmd    `cmd:"" help:"List X11 windows (via xdotool)"`
	Focus      WlFocusCmd      `cmd:"" help:"Focus an X11 window by name or class (via xdotool)"`
	Capture    WlCaptureCmd    `cmd:"" help:"Capture X11 screen or window as PNG (via import)"`
}

// WlScreenshotCmd captures the desktop as a PNG image via grim (or import --x11).
type WlScreenshotCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	File     string `arg:"" optional:"" default:"screenshot.png" help:"Output file path"`
	Output   string `long:"output" default:"HEADLESS-1" help:"Wayland output name"`
	Region   string `long:"region" help:"Capture region as 'X,Y WxH'"`
	X11      bool   `long:"x11" help:"Capture via X11 (import) instead of Wayland (grim)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlScreenshotCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	var captureCmd string
	if c.X11 {
		captureCmd = "DISPLAY=:0 import -window root png:-"
	} else if c.Region != "" {
		captureCmd = fmt.Sprintf("grim -g %s -", shellQuote(c.Region))
	} else {
		captureCmd = fmt.Sprintf("grim -o %s -", shellQuote(c.Output))
	}

	data, err := captureWlCmd(engine, name, captureCmd)
	if err != nil {
		return fmt.Errorf("capturing screenshot: %w", err)
	}

	if err := os.WriteFile(c.File, data, 0644); err != nil {
		return fmt.Errorf("writing screenshot: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%d bytes)\n", c.File, len(data))
	return nil
}

// WlClickCmd sends a pointer click at the given absolute coordinates via wlrctl.
type WlClickCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	X        int    `arg:"" help:"X coordinate"`
	Y        int    `arg:"" help:"Y coordinate"`
	Button   string `long:"button" default:"left" help:"Mouse button (left, right, middle)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	FromCDP  string `long:"from-cdp" help:"Translate from CDP viewport coords using this tab ID"`
	FromSway string `long:"from-sway" help:"Translate from window-relative coords using sway app_id"`
}

func (c *WlClickCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	clickX, clickY := c.X, c.Y

	// Translate from CDP viewport coordinates to desktop coordinates.
	if c.FromCDP != "" {
		client, err := connectTab(c.Image, c.FromCDP, c.Instance)
		if err != nil {
			return fmt.Errorf("connecting to CDP tab %s for coordinate translation: %w", c.FromCDP, err)
		}
		offset, err := cdpGetWindowOffset(client)
		client.Close()
		if err != nil {
			return fmt.Errorf("getting CDP window offset: %w", err)
		}
		clickX = int(float64(c.X) + offset.ScreenX)
		clickY = int(float64(c.Y) + offset.ScreenY + offset.ChromeHeight)
		fmt.Fprintf(os.Stderr, "Translated viewport (%d, %d) → desktop (%d, %d) via CDP tab %s\n",
			c.X, c.Y, clickX, clickY, c.FromCDP)
	}

	// Translate from window-relative coordinates to desktop coordinates via sway.
	if c.FromSway != "" {
		rect, err := FindWindowRect(engine, name, c.FromSway)
		if err != nil {
			return err
		}
		clickX = c.X + rect.X
		clickY = c.Y + rect.Y
		fmt.Fprintf(os.Stderr, "Translated window-relative (%d, %d) → desktop (%d, %d) via sway app_id=%s\n",
			c.X, c.Y, clickX, clickY, c.FromSway)
	}

	btn := wlButton(c.Button)
	if btn == "" {
		return fmt.Errorf("unknown button %q (valid: left, right, middle)", c.Button)
	}

	shellCmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d && sleep 0.05 && wlrctl pointer click %s",
		clickX, clickY, btn,
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("clicking at (%d, %d): %w", clickX, clickY, err)
	}

	fmt.Fprintf(os.Stderr, "Clicked %s at (%d, %d)\n", c.Button, clickX, clickY)
	return nil
}

// WlTypeCmd sends keyboard input via wtype.
type WlTypeCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Text     string `arg:"" help:"Text to type"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlTypeCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	shellCmd := fmt.Sprintf("wtype -- %s", shellQuote(c.Text))
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("typing text: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Typed %d characters\n", len(c.Text))
	return nil
}

// WlKeyCmd sends a key press event via wtype.
type WlKeyCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	KeyName  string `arg:"" help:"Key name (Return, Escape, Tab, etc.)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlKeyCmd) Run() error {
	if !wlValidKey(c.KeyName) {
		return fmt.Errorf("unknown key %q (valid: %s)", c.KeyName, wlKeyNames())
	}

	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	shellCmd := fmt.Sprintf("wtype -k %s", shellQuote(c.KeyName))
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("pressing key %s: %w", c.KeyName, err)
	}

	fmt.Fprintf(os.Stderr, "Pressed key %s\n", c.KeyName)
	return nil
}

// WlMouseCmd moves the mouse pointer to absolute coordinates via wlrctl.
type WlMouseCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	X        int    `arg:"" help:"X coordinate"`
	Y        int    `arg:"" help:"Y coordinate"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlMouseCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	shellCmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d",
		c.X, c.Y,
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("moving mouse to (%d, %d): %w", c.X, c.Y, err)
	}

	fmt.Fprintf(os.Stderr, "Moved mouse to (%d, %d)\n", c.X, c.Y)
	return nil
}

// WlStatusCmd checks Wayland desktop status and tool availability.
type WlStatusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlStatusCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Check Wayland tool availability.
	tools := []string{"grim", "wtype", "wlrctl"}
	for _, tool := range tools {
		shellCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", tool)
		if err := execWlCmdSilent(engine, name, shellCmd); err != nil {
			fmt.Printf("%-12s not found\n", tool+":")
		} else {
			fmt.Printf("%-12s available\n", tool+":")
		}
	}

	// Check X11 tool availability.
	x11tools := []string{"xdotool", "import", "xprop"}
	for _, tool := range x11tools {
		shellCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", tool)
		if err := execWlCmdSilent(engine, name, shellCmd); err != nil {
			fmt.Printf("%-12s not found\n", tool+":")
		} else {
			fmt.Printf("%-12s available\n", tool+":")
		}
	}

	// Get resolution from sway outputs.
	data, err := captureSwaymsg(engine, name, "-t", "get_outputs")
	if err != nil {
		fmt.Printf("%-12s unavailable (swaymsg failed)\n", "sway:")
		return nil
	}

	var outputs []struct {
		Name              string `json:"name"`
		CurrentMode       struct{ Width, Height int } `json:"current_mode"`
	}
	if err := json.Unmarshal(data, &outputs); err == nil && len(outputs) > 0 {
		o := outputs[0]
		fmt.Printf("%-12s %s %dx%d\n", "output:", o.Name, o.CurrentMode.Width, o.CurrentMode.Height)
	}

	// Check XWayland status.
	xwaylandCmd := "DISPLAY=:0 xprop -root _NET_CLIENT_LIST 2>/dev/null | grep -c window"
	xwOut, xwErr := captureWlCmd(engine, name, xwaylandCmd)
	if xwErr == nil {
		count := strings.TrimSpace(string(xwOut))
		fmt.Printf("%-12s enabled (DISPLAY=:0, %s windows)\n", "xwayland:", count)
	} else {
		fmt.Printf("%-12s disabled or not running\n", "xwayland:")
	}

	return nil
}

// WlWindowsCmd lists X11 windows via xdotool.
type WlWindowsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlWindowsCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	shellCmd := `DISPLAY=:0 xdotool search --name "" 2>/dev/null | while read wid; do
		name=$(xdotool getwindowname "$wid" 2>/dev/null)
		[ -n "$name" ] && printf "%s\t%s\n" "$wid" "$name"
	done`

	return execWlCmd(engine, name, shellCmd)
}

// WlFocusCmd focuses an X11 window by name or class via xdotool.
type WlFocusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Window title substring or class to focus"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlFocusCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	shellCmd := fmt.Sprintf(
		`DISPLAY=:0 xdotool search --name %s windowactivate 2>/dev/null || DISPLAY=:0 xdotool search --class %s windowactivate`,
		shellQuote(c.Target), shellQuote(c.Target),
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("focusing window %q: %w", c.Target, err)
	}

	fmt.Fprintf(os.Stderr, "Focused window matching %q\n", c.Target)
	return nil
}

// WlCaptureCmd captures an X11 screen or window via ImageMagick import.
type WlCaptureCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	File     string `arg:"" optional:"" default:"capture.png" help:"Output file path"`
	Window   string `long:"window" help:"Capture specific window by title (default: full screen)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlCaptureCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	var captureCmd string
	if c.Window != "" {
		// Capture a specific window by title
		captureCmd = fmt.Sprintf(
			`DISPLAY=:0 import -window "$(xdotool search --name %s 2>/dev/null | head -1)" png:-`,
			shellQuote(c.Window),
		)
	} else {
		// Capture the entire X11 root window
		captureCmd = "DISPLAY=:0 import -window root png:-"
	}

	data, err := captureWlCmd(engine, name, captureCmd)
	if err != nil {
		return fmt.Errorf("capturing X11 screen: %w", err)
	}

	if err := os.WriteFile(c.File, data, 0644); err != nil {
		return fmt.Errorf("writing capture: %w", err)
	}

	fmt.Fprintf(os.Stderr, "X11 capture saved to %s (%d bytes)\n", c.File, len(data))
	return nil
}

// --- Helper functions ---

// wlShellCmd wraps a command with Wayland environment variable exports.
func wlShellCmd(cmd string) string {
	return fmt.Sprintf(
		`export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp}" WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}" && %s`,
		cmd,
	)
}

// execWlCmd runs a shell command inside a container (or locally for ".").
func execWlCmd(engine, containerName, shellCmd string) error {
	wrapped := wlShellCmd(shellCmd)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", wrapped)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", wrapped)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// execWlCmdSilent runs a shell command silently (no stdout/stderr).
func execWlCmdSilent(engine, containerName, shellCmd string) error {
	wrapped := wlShellCmd(shellCmd)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", wrapped)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", wrapped)
	}
	return cmd.Run()
}

// captureWlCmd runs a shell command and captures stdout as bytes.
func captureWlCmd(engine, containerName, shellCmd string) ([]byte, error) {
	wrapped := wlShellCmd(shellCmd)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", wrapped)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", wrapped)
	}
	return cmd.Output()
}

// --- Key and button mappings ---

// wlKeySet contains valid XKB key names accepted by wtype -k.
var wlKeySet = map[string]bool{
	"Return": true, "Escape": true, "Tab": true, "BackSpace": true,
	"Delete": true, "Insert": true, "Home": true, "End": true,
	"Page_Up": true, "Page_Down": true, "space": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"F1": true, "F2": true, "F3": true, "F4": true,
	"F5": true, "F6": true, "F7": true, "F8": true,
	"F9": true, "F10": true, "F11": true, "F12": true,
	"Shift_L": true, "Shift_R": true,
	"Control_L": true, "Control_R": true,
	"Alt_L": true, "Alt_R": true,
	"Super_L": true, "Super_R": true,
	"Meta_L": true, "Meta_R": true,
	"Caps_Lock": true,
}

// wlValidKey returns true if the key name is in the known set.
func wlValidKey(name string) bool {
	return wlKeySet[name]
}

// wlKeyNames returns a sorted comma-separated list of valid key names.
func wlKeyNames() string {
	names := make([]string, 0, len(wlKeySet))
	for k := range wlKeySet {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// wlButton maps button names to wlrctl button arguments.
func wlButton(name string) string {
	switch name {
	case "left":
		return "left"
	case "right":
		return "right"
	case "middle":
		return "middle"
	default:
		return ""
	}
}
