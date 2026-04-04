package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// WlCmd manages Wayland-native desktop interaction in running containers.
type WlCmd struct {
	Screenshot  WlScreenshotCmd  `cmd:"" help:"Capture desktop as PNG (via grim)"`
	Click       WlClickCmd       `cmd:"" help:"Click at x,y coordinates (via wlrctl)"`
	Type        WlTypeCmd        `cmd:"" help:"Type text as keyboard input (via wtype)"`
	Key         WlKeyCmd         `cmd:"" help:"Send a key press event (via wtype)"`
	Mouse       WlMouseCmd       `cmd:"" help:"Move mouse to x,y without clicking (via wlrctl)"`
	Status      WlStatusCmd      `cmd:"" help:"Check Wayland desktop and tool availability"`
	Windows     WlWindowsCmd     `cmd:"" help:"List windows (wlrctl toplevel, xdotool fallback)"`
	Focus       WlFocusCmd       `cmd:"" help:"Focus a window by title (wlrctl toplevel, xdotool fallback)"`
	Toplevel    WlToplevelCmd    `cmd:"" help:"List Wayland toplevel windows (via wlrctl)"`
	Close       WlCloseCmd       `cmd:"" help:"Close a window by title (via wlrctl toplevel)"`
	Fullscreen  WlFullscreenCmd  `cmd:"" help:"Toggle fullscreen on a window (via wlrctl toplevel)"`
	Minimize    WlMinimizeCmd    `cmd:"" help:"Toggle minimize on a window (via wlrctl toplevel)"`
	Exec        WlExecCmd        `cmd:"" help:"Launch an application in the container"`
	Resolution  WlResolutionCmd  `cmd:"" help:"Set output resolution (via wlr-randr)"`
	KeyCombo    WlKeyComboCmd    `cmd:"key-combo" help:"Send a key combination (e.g. ctrl+c, alt+tab)"`
	DoubleClick WlDoubleClickCmd `cmd:"double-click" help:"Double-click at x,y coordinates"`
	Scroll      WlScrollCmd      `cmd:"" help:"Scroll at coordinates (via xdotool)"`
	Drag        WlDragCmd        `cmd:"" help:"Drag from (x1,y1) to (x2,y2) (experimental, XWayland)"`
	Clipboard   WlClipboardCmd   `cmd:"" help:"Read/write Wayland clipboard (via wl-clipboard)"`
	Xprop       WlXpropCmd       `cmd:"" help:"Query X11 window properties (via xprop)"`
	Geometry    WlGeometryCmd    `cmd:"" help:"Get window geometry (compositor-agnostic)"`
	Atspi       WlAtspiCmd       `cmd:"" help:"Query accessibility tree (via AT-SPI2)"`
	Sway        WlSwayCmd        `cmd:"" help:"Sway-specific compositor commands (requires sway)"`
}

// WlSwayCmd groups sway IPC commands. These require the sway compositor
// and use swaymsg. They will error on non-sway compositors (labwc, niri).
type WlSwayCmd struct {
	Msg        WlSwayMsgCmd        `cmd:"" help:"Run a swaymsg command"`
	Tree       WlSwayTreeCmd       `cmd:"" help:"Get window/container tree (JSON)"`
	Workspaces WlSwayWorkspacesCmd `cmd:"" help:"List workspaces (JSON)"`
	Outputs    WlSwayOutputsCmd    `cmd:"" help:"List outputs (JSON)"`
	Focus      WlSwayFocusCmd      `cmd:"" help:"Focus window by direction or criteria"`
	Move       WlSwayMoveCmd       `cmd:"" help:"Move focused window (direction, workspace, or scratchpad)"`
	Resize     WlSwayResizeCmd     `cmd:"" help:"Resize focused window"`
	Kill       WlSwayKillCmd       `cmd:"" help:"Close the focused window"`
	Floating   WlSwayFloatingCmd   `cmd:"" help:"Toggle floating on focused window"`
	Layout     WlSwayLayoutCmd     `cmd:"" help:"Set layout mode (tabbed, stacking, splitv, splith)"`
	Workspace  WlSwayWorkspaceCmd  `cmd:"" help:"Switch to a workspace"`
	Reload     WlSwayReloadCmd     `cmd:"" help:"Reload sway configuration"`
}

// WlScreenshotCmd captures the desktop as a PNG image.
// Auto-detects screenshot tool: pixelflux-screenshot (selkies) or grim (sway).
type WlScreenshotCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	File     string `arg:"" optional:"" default:"screenshot.png" help:"Output file path"`
	Output   string `long:"output" default:"HEADLESS-1" help:"Wayland output name"`
	Region   string `long:"region" help:"Capture region as 'X,Y WxH'"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlScreenshotCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Detect available screenshot tool.
	var captureCmd string
	if execWlCmdSilent(engine, name, "command -v pixelflux-screenshot >/dev/null 2>&1") == nil {
		// selkies-desktop: use pixelflux rendering pipeline capture.
		captureCmd = "pixelflux-screenshot"
	} else if execWlCmdSilent(engine, name, "command -v grim >/dev/null 2>&1") == nil {
		// sway-desktop: use grim (wlr-screencopy).
		if c.Region != "" {
			captureCmd = fmt.Sprintf("grim -g %s -", shellQuote(c.Region))
		} else {
			captureCmd = fmt.Sprintf("grim -o %s -", shellQuote(c.Output))
		}
	} else {
		return fmt.Errorf("no screenshot tool available (need pixelflux-screenshot or grim)")
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
	FromX11  string `name:"from-x11" help:"Translate from X11 window-internal coords (scales for XWayland fullscreen)"`
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

	// Translate from X11 window-internal coordinates to desktop coordinates.
	// XWayland windows may render at a different internal resolution than their
	// sway-managed desktop size (e.g. fullscreened 1280x600 app on 1920x1080).
	if c.FromX11 != "" {
		rect, err := FindWindowRect(engine, name, c.FromX11)
		if err != nil {
			return err
		}
		x11W, x11H, err := FindX11WindowGeometry(engine, name, c.FromX11)
		if err != nil {
			return err
		}
		clickX = rect.X + (c.X * rect.Width / x11W)
		clickY = rect.Y + (c.Y * rect.Height / x11H)
		fmt.Fprintf(os.Stderr, "Translated X11 (%d, %d) → desktop (%d, %d) (x11=%dx%d sway=%dx%d)\n",
			c.X, c.Y, clickX, clickY, x11W, x11H, rect.Width, rect.Height)
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

	// Quick check via shared probe function.
	ts := checkWlStatus(engine, name)
	fmt.Printf("WL:        %s\n", ts.Status)
	if ts.Detail != "" {
		fmt.Printf("Detail:    %s\n", ts.Detail)
	}

	// Verbose per-tool availability.
	tools := []string{"grim", "wtype", "wlrctl"}
	for _, tool := range tools {
		shellCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", tool)
		if err := execWlCmdSilent(engine, name, shellCmd); err != nil {
			fmt.Printf("%-12s not found\n", tool+":")
		} else {
			fmt.Printf("%-12s available\n", tool+":")
		}
	}

	// Check clipboard and randr tool availability.
	extraTools := []string{"wl-copy", "wl-paste", "wlr-randr"}
	for _, tool := range extraTools {
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

	// Check AT-SPI2 availability (use /usr/bin/python3 for system RPM packages).
	atspiCheck := `/usr/bin/python3 -c "import gi; gi.require_version('Atspi','2.0')" 2>/dev/null`
	if err := execWlCmdSilent(engine, name, atspiCheck); err != nil {
		fmt.Printf("%-12s not found\n", "atspi:")
	} else {
		fmt.Printf("%-12s available\n", "atspi:")
	}

	// Get resolution: try sway first, fall back to wlr-randr.
	gotResolution := false
	data, err := captureSwaymsg(engine, name, "-t", "get_outputs")
	if err == nil {
		var outputs []struct {
			Name        string `json:"name"`
			CurrentMode struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			} `json:"current_mode"`
		}
		if err := json.Unmarshal(data, &outputs); err == nil && len(outputs) > 0 {
			o := outputs[0]
			fmt.Printf("%-12s %s %dx%d (sway)\n", "output:", o.Name, o.CurrentMode.Width, o.CurrentMode.Height)
			gotResolution = true
		}
	}

	if !gotResolution {
		// Fall back to wlr-randr (works on labwc, niri, any wlroots compositor).
		randrOut, randrErr := captureWlCmd(engine, name, "wlr-randr 2>/dev/null | head -3")
		if randrErr == nil {
			lines := strings.TrimSpace(string(randrOut))
			if lines != "" {
				fmt.Printf("%-12s %s\n", "output:", strings.Split(lines, "\n")[0])
				gotResolution = true
			}
		}
	}

	if !gotResolution {
		fmt.Printf("%-12s unavailable (no sway or wlr-randr)\n", "output:")
	}

	// Check XWayland status via process detection (more reliable than xprop).
	xwCheck := `pgrep -f Xwayland >/dev/null 2>&1`
	if execWlCmdSilent(engine, name, xwCheck) == nil {
		// XWayland running — count X11 client windows.
		countCmd := `DISPLAY=:0 xdotool search --name "." 2>/dev/null | wc -l`
		countOut, _ := captureWlCmd(engine, name, countCmd)
		count := strings.TrimSpace(string(countOut))
		if count == "" || count == "0" {
			fmt.Printf("%-12s running (no X11 clients)\n", "xwayland:")
		} else {
			fmt.Printf("%-12s running (%s X11 windows)\n", "xwayland:", count)
		}
	} else {
		fmt.Printf("%-12s not running (starts on demand)\n", "xwayland:")
	}

	return nil
}

// checkWlStatus probes Wayland tool availability inside a container.
// Returns ToolStatus{Status: "-"} if the Wayland tools aren't present.
func checkWlStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "wl", Status: "-"}

	// Check for core tools: screenshot (grim or pixelflux-screenshot), wtype, wlrctl
	coreTools := []string{"wtype", "wlrctl"}
	var available []string
	for _, tool := range coreTools {
		shellCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", tool)
		if err := execWlCmdSilent(engine, containerName, shellCmd); err == nil {
			available = append(available, tool)
		}
	}

	// Also check for screenshot tool (grim or pixelflux-screenshot)
	for _, tool := range []string{"grim", "pixelflux-screenshot"} {
		shellCmd := fmt.Sprintf("command -v %s >/dev/null 2>&1", tool)
		if err := execWlCmdSilent(engine, containerName, shellCmd); err == nil {
			available = append(available, tool)
			break // only need one screenshot tool
		}
	}

	if len(available) == 0 {
		return ts
	}

	ts.Status = "ok"
	ts.Detail = strings.Join(available, ",")
	return ts
}

// WlWindowsCmd lists windows. Tries wlrctl toplevel (Wayland-native, works on
// both sway and labwc) then falls back to xdotool (X11/XWayland).
type WlWindowsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlWindowsCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Try wlrctl toplevel first (compositor-agnostic).
	if err := execWlCmdSilent(engine, name, "command -v wlrctl >/dev/null 2>&1"); err == nil {
		if err := execWlCmd(engine, name, "wlrctl toplevel list"); err == nil {
			return nil
		}
	}

	// Fall back to xdotool (XWayland).
	shellCmd := `export DISPLAY=:0 && xdotool search --name "." 2>/dev/null | while read wid; do
		name=$(xdotool getwindowname "$wid" 2>/dev/null)
		[ -n "$name" ] && printf "%s\t%s\n" "$wid" "$name"
	done`

	return execWlCmd(engine, name, shellCmd)
}

// WlFocusCmd focuses a window by title. Tries wlrctl toplevel (Wayland-native)
// then falls back to xdotool (X11/XWayland).
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

	// Try wlrctl toplevel focus (matches by app_id in wlrctl 0.2.2).
	if execWlCmdSilent(engine, name, "command -v wlrctl >/dev/null 2>&1") == nil {
		shellCmd := fmt.Sprintf("wlrctl toplevel focus %s", shellQuote(c.Target))
		if execWlCmdSilent(engine, name, shellCmd) == nil {
			fmt.Fprintf(os.Stderr, "Focused window matching %q via wlrctl\n", c.Target)
			return nil
		}
	}

	// Fall back to xdotool (XWayland).
	shellCmd := fmt.Sprintf(
		`export DISPLAY=:0 && xdotool search --name %s windowactivate 2>/dev/null || export DISPLAY=:0 && xdotool search --class %s windowactivate`,
		shellQuote(c.Target), shellQuote(c.Target),
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("focusing window %q: %w", c.Target, err)
	}

	fmt.Fprintf(os.Stderr, "Focused window matching %q via xdotool\n", c.Target)
	return nil
}


// FindX11WindowGeometry queries the X11 window geometry via xdotool for an XWayland window.
// Returns the window's internal (X11-reported) width and height.
func FindX11WindowGeometry(engine, containerName, target string) (int, int, error) {
	shellCmd := fmt.Sprintf(
		`export DISPLAY=:0 && xdotool search --class %s getwindowgeometry 2>/dev/null || export DISPLAY=:0 && xdotool search --name %s getwindowgeometry`,
		shellQuote(target), shellQuote(target),
	)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", shellCmd)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", shellCmd)
	}
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("querying X11 geometry for %q: %w", target, err)
	}

	// Parse "Geometry: WIDTHxHEIGHT" from xdotool output
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Geometry:") {
			geom := strings.TrimSpace(strings.TrimPrefix(line, "Geometry:"))
			parts := strings.SplitN(geom, "x", 2)
			if len(parts) == 2 {
				w, errW := strconv.Atoi(parts[0])
				h, errH := strconv.Atoi(parts[1])
				if errW == nil && errH == nil && w > 0 && h > 0 {
					return w, h, nil
				}
			}
		}
	}
	return 0, 0, fmt.Errorf("could not parse X11 geometry for %q from: %s", target, string(out))
}

// --- Phase 2: Window management commands (wlrctl toplevel) ---

// WlToplevelCmd lists Wayland toplevel windows via wlrctl.
// Works on all wlroots compositors (sway, labwc, niri).
type WlToplevelCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlToplevelCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execWlCmd(engine, name, "wlrctl toplevel list")
}

// WlCloseCmd closes a window by title via wlrctl toplevel.
type WlCloseCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Window title substring to close"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlCloseCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := wlrctlToplevel(engine, name, "close", c.Target); err != nil {
		return fmt.Errorf("closing window %q: %w", c.Target, err)
	}
	fmt.Fprintf(os.Stderr, "Closed window matching %q\n", c.Target)
	return nil
}

// WlFullscreenCmd toggles fullscreen on a window via wlrctl toplevel.
type WlFullscreenCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Window title substring"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlFullscreenCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := wlrctlToplevel(engine, name, "fullscreen", c.Target); err != nil {
		return fmt.Errorf("toggling fullscreen on %q: %w", c.Target, err)
	}
	fmt.Fprintf(os.Stderr, "Toggled fullscreen on window matching %q\n", c.Target)
	return nil
}

// WlMinimizeCmd toggles minimize on a window via wlrctl toplevel.
type WlMinimizeCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Window title substring"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlMinimizeCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := wlrctlToplevel(engine, name, "minimize", c.Target); err != nil {
		return fmt.Errorf("toggling minimize on %q: %w", c.Target, err)
	}
	fmt.Fprintf(os.Stderr, "Toggled minimize on window matching %q\n", c.Target)
	return nil
}

// WlExecCmd launches an application inside the container.
type WlExecCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlExecCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	// Background the process so it doesn't block.
	// Set DISPLAY=:0 for XWayland apps (like xterm) that need X11.
	// Don't shellQuote — the command may contain args (e.g. "xterm -hold").
	shellCmd := fmt.Sprintf("export DISPLAY=:0; %s &", c.Command)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("launching %q: %w", c.Command, err)
	}
	fmt.Fprintf(os.Stderr, "Launched %q\n", c.Command)
	return nil
}

// WlResolutionCmd sets the output resolution via wlr-randr.
// Works on all wlroots compositors (sway, labwc, niri).
type WlResolutionCmd struct {
	Image      string `arg:"" help:"Image name (use . for local)"`
	Resolution string `arg:"" help:"Resolution (e.g. 1920x1080)"`
	Output     string `short:"o" long:"output" default:"" help:"Output name (auto-detected if empty)"`
	Instance   string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlResolutionCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	parts := strings.SplitN(c.Resolution, "x", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid resolution %q (expected WxH, e.g. 1920x1080)", c.Resolution)
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return fmt.Errorf("invalid width in %q: %w", c.Resolution, err)
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return fmt.Errorf("invalid height in %q: %w", c.Resolution, err)
	}

	output := c.Output
	if output == "" {
		// Auto-detect first output via wlr-randr.
		data, err := captureWlCmd(engine, name, "wlr-randr 2>/dev/null | head -1")
		if err == nil {
			line := strings.TrimSpace(string(data))
			if fields := strings.Fields(line); len(fields) > 0 {
				output = fields[0]
			}
		}
		if output == "" {
			output = "HEADLESS-1"
		}
	}

	shellCmd := fmt.Sprintf("wlr-randr --output %s --custom-mode %s",
		shellQuote(output), shellQuote(c.Resolution))
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("setting resolution: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Set %s to %s\n", output, c.Resolution)
	return nil
}

// --- Phase 3: Input enhancements ---

// WlKeyComboCmd sends a key combination (e.g. ctrl+c, alt+tab, ctrl+shift+t).
type WlKeyComboCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Keys     string `arg:"" help:"Key combination (e.g. ctrl+c, alt+tab, super+l)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// wlModifierMap maps human-friendly modifier names to wtype -M arguments.
var wlModifierMap = map[string]string{
	"ctrl":    "ctrl",
	"control": "ctrl",
	"alt":     "alt",
	"shift":   "shift",
	"super":   "logo",
	"win":     "logo",
	"logo":    "logo",
	"meta":    "alt",
}

// parseKeyCombo splits a key combo string into wtype -M flags and the final key.
// Example: "ctrl+shift+t" → ([]string{"ctrl", "shift"}, "t")
func parseKeyCombo(combo string) (modifiers []string, key string, err error) {
	parts := strings.Split(strings.ToLower(combo), "+")
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("empty key combination")
	}

	key = parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		mod, ok := wlModifierMap[p]
		if !ok {
			return nil, "", fmt.Errorf("unknown modifier %q (valid: ctrl, alt, shift, super, win, logo, meta)", p)
		}
		modifiers = append(modifiers, mod)
	}
	return modifiers, key, nil
}

func (c *WlKeyComboCmd) Run() error {
	modifiers, key, err := parseKeyCombo(c.Keys)
	if err != nil {
		return err
	}

	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	var args []string
	for _, mod := range modifiers {
		args = append(args, "-M", mod)
	}
	// If the key is a single character, use it directly; otherwise use -k for named keys.
	if len(key) == 1 {
		args = append(args, key)
	} else {
		args = append(args, "-k", key)
	}
	shellCmd := "wtype " + strings.Join(args, " ")
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("sending key combo %s: %w", c.Keys, err)
	}
	fmt.Fprintf(os.Stderr, "Sent key combo %s\n", c.Keys)
	return nil
}

// WlDoubleClickCmd sends a double-click at absolute coordinates.
type WlDoubleClickCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	X        int    `arg:"" help:"X coordinate"`
	Y        int    `arg:"" help:"Y coordinate"`
	Button   string `long:"button" default:"left" help:"Mouse button (left, right, middle)"`
	Delay    int    `long:"delay" default:"50" help:"Delay between clicks in ms"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlDoubleClickCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	btn := wlButton(c.Button)
	if btn == "" {
		return fmt.Errorf("unknown button %q (valid: left, right, middle)", c.Button)
	}

	delayStr := fmt.Sprintf("%.3f", float64(c.Delay)/1000.0)
	shellCmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d && sleep 0.05 && wlrctl pointer click %s && sleep %s && wlrctl pointer click %s",
		c.X, c.Y, btn, delayStr, btn,
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("double-clicking at (%d, %d): %w", c.X, c.Y, err)
	}
	fmt.Fprintf(os.Stderr, "Double-clicked %s at (%d, %d)\n", c.Button, c.X, c.Y)
	return nil
}

// WlScrollCmd scrolls at the given coordinates via xdotool (XWayland).
// Button 4=up, 5=down, 6=left, 7=right in X11 convention.
type WlScrollCmd struct {
	Image     string `arg:"" help:"Image name (use . for local)"`
	X         int    `arg:"" help:"X coordinate"`
	Y         int    `arg:"" help:"Y coordinate"`
	Direction string `arg:"" help:"Scroll direction (up, down, left, right)"`
	Amount    int    `long:"amount" default:"3" help:"Number of scroll steps"`
	Instance  string `short:"i" long:"instance" help:"Instance name"`
}

// wlScrollButton maps scroll direction to X11 button number.
func wlScrollButton(dir string) (int, error) {
	switch strings.ToLower(dir) {
	case "up":
		return 4, nil
	case "down":
		return 5, nil
	case "left":
		return 6, nil
	case "right":
		return 7, nil
	default:
		return 0, fmt.Errorf("unknown scroll direction %q (valid: up, down, left, right)", dir)
	}
}

func (c *WlScrollCmd) Run() error {
	btn, err := wlScrollButton(c.Direction)
	if err != nil {
		return err
	}

	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Move pointer to target position first via wlrctl.
	moveCmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d",
		c.X, c.Y,
	)
	if err := execWlCmdSilent(engine, name, moveCmd); err != nil {
		return fmt.Errorf("moving pointer to (%d, %d): %w", c.X, c.Y, err)
	}

	// Scroll via xdotool (X11 button events work for XWayland windows like Chrome).
	var clickCmds []string
	for range c.Amount {
		clickCmds = append(clickCmds, fmt.Sprintf("DISPLAY=:0 xdotool click %d", btn))
	}
	scrollCmd := strings.Join(clickCmds, " && sleep 0.02 && ")

	if err := execWlCmd(engine, name, scrollCmd); err != nil {
		// Fall back to wtype Page_Up/Page_Down.
		var keyName string
		switch c.Direction {
		case "up":
			keyName = "Page_Up"
		case "down":
			keyName = "Page_Down"
		default:
			return fmt.Errorf("scrolling %s at (%d, %d): xdotool failed and no wtype fallback for %s: %w",
				c.Direction, c.X, c.Y, c.Direction, err)
		}
		for range c.Amount {
			keyCmd := fmt.Sprintf("wtype -k %s", keyName)
			if err := execWlCmd(engine, name, keyCmd); err != nil {
				return fmt.Errorf("scroll fallback via wtype: %w", err)
			}
		}
		fmt.Fprintf(os.Stderr, "Scrolled %s %d steps at (%d, %d) via wtype fallback\n",
			c.Direction, c.Amount, c.X, c.Y)
		return nil
	}

	fmt.Fprintf(os.Stderr, "Scrolled %s %d steps at (%d, %d)\n",
		c.Direction, c.Amount, c.X, c.Y)
	return nil
}

// WlDragCmd performs a mouse drag from (x1,y1) to (x2,y2).
// Experimental: requires XWayland (uses xdotool for press/release separation).
type WlDragCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	X1       int    `arg:"" help:"Start X coordinate"`
	Y1       int    `arg:"" help:"Start Y coordinate"`
	X2       int    `arg:"" help:"End X coordinate"`
	Y2       int    `arg:"" help:"End Y coordinate"`
	Button   string `long:"button" default:"left" help:"Mouse button"`
	Duration int    `long:"duration" default:"200" help:"Drag duration in ms"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlDragCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	btnNum := 1
	switch c.Button {
	case "left":
		btnNum = 1
	case "middle":
		btnNum = 2
	case "right":
		btnNum = 3
	default:
		return fmt.Errorf("unknown button %q (valid: left, right, middle)", c.Button)
	}

	delayStr := fmt.Sprintf("%.3f", float64(c.Duration)/1000.0)
	shellCmd := fmt.Sprintf(
		"export DISPLAY=:0 && xdotool mousemove %d %d mousedown %d && sleep %s && xdotool mousemove %d %d mouseup %d",
		c.X1, c.Y1, btnNum, delayStr, c.X2, c.Y2, btnNum,
	)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("dragging from (%d,%d) to (%d,%d): %w (requires XWayland)",
			c.X1, c.Y1, c.X2, c.Y2, err)
	}
	fmt.Fprintf(os.Stderr, "Dragged %s from (%d, %d) to (%d, %d)\n",
		c.Button, c.X1, c.Y1, c.X2, c.Y2)
	return nil
}

// --- Phase 4: Clipboard ---

// WlClipboardCmd reads or writes the Wayland clipboard via wl-clipboard.
type WlClipboardCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Action   string `arg:"" help:"Action: get, set, clear"`
	Text     string `arg:"" optional:"" help:"Text to set (for 'set' action)"`
	Primary  bool   `long:"primary" short:"p" help:"Use primary selection instead of clipboard"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlClipboardCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	primaryFlag := ""
	if c.Primary {
		primaryFlag = " -p"
	}

	switch c.Action {
	case "get":
		shellCmd := fmt.Sprintf("wl-paste%s 2>/dev/null", primaryFlag)
		return execWlCmd(engine, name, shellCmd)
	case "set":
		if c.Text == "" {
			return fmt.Errorf("text argument required for 'set' action")
		}
		shellCmd := fmt.Sprintf("printf '%%s' %s | wl-copy%s", shellQuote(c.Text), primaryFlag)
		if err := execWlCmd(engine, name, shellCmd); err != nil {
			return fmt.Errorf("setting clipboard: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Clipboard set (%d chars)\n", len(c.Text))
		return nil
	case "clear":
		shellCmd := fmt.Sprintf("wl-copy%s --clear", primaryFlag)
		if err := execWlCmd(engine, name, shellCmd); err != nil {
			return fmt.Errorf("clearing clipboard: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Clipboard cleared\n")
		return nil
	default:
		return fmt.Errorf("unknown action %q (valid: get, set, clear)", c.Action)
	}
}

// --- Phase 7: GUI introspection ---

// WlXpropCmd queries X11 window properties via xprop.
type WlXpropCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" optional:"" help:"Window title or ID (default: active window)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlXpropCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Check if XWayland is running.
	if execWlCmdSilent(engine, name, `pgrep -f Xwayland >/dev/null 2>&1`) != nil {
		fmt.Fprintf(os.Stderr, "XWayland is not running (no X11 clients have been launched)\n")
		fmt.Fprintf(os.Stderr, "Launch an X11 app first: ov wl exec <image> xterm\n")
		return nil
	}

	var shellCmd string
	if c.Target == "" {
		shellCmd = `export DISPLAY=:0 && WID=$(xdotool getactivewindow 2>/dev/null) && [ -n "$WID" ] && xprop -id "$WID" WM_CLASS _NET_WM_NAME _NET_WM_WINDOW_TYPE _NET_WM_PID 2>/dev/null && xdotool getwindowgeometry "$WID" 2>/dev/null || echo "No active X11 window"`
	} else {
		shellCmd = fmt.Sprintf(
			`export DISPLAY=:0 && WID=$(xdotool search --class %s 2>/dev/null | head -1 || xdotool search --name %s 2>/dev/null | head -1) && [ -n "$WID" ] && xprop -id "$WID" WM_CLASS _NET_WM_NAME _NET_WM_WINDOW_TYPE _NET_WM_PID 2>/dev/null && xdotool getwindowgeometry "$WID" 2>/dev/null || echo "No X11 window matching %s"`,
			shellQuote(c.Target), shellQuote(c.Target), c.Target,
		)
	}
	return execWlCmd(engine, name, shellCmd)
}

// WlGeometryCmd gets window geometry in a compositor-agnostic way.
// Tries sway tree first, then falls back to xdotool (XWayland).
type WlGeometryCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Window title or class"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *WlGeometryCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Try sway tree first (returns precise rect with x, y, width, height).
	rect, err := FindWindowRect(engine, name, c.Target)
	if err == nil {
		out, _ := json.Marshal(map[string]int{
			"x": rect.X, "y": rect.Y, "width": rect.Width, "height": rect.Height,
		})
		fmt.Println(string(out))
		return nil
	}

	// Fall back to xdotool (XWayland windows like xterm). Try class first, then name.
	shellCmd := fmt.Sprintf(
		`export DISPLAY=:0 && WID=$(xdotool search --class %s 2>/dev/null | head -1 || xdotool search --name %s 2>/dev/null | head -1) && [ -n "$WID" ] && xdotool getwindowgeometry "$WID" 2>/dev/null`,
		shellQuote(c.Target), shellQuote(c.Target),
	)
	data, err := captureWlCmd(engine, name, shellCmd)
	if err == nil {
		// Parse xdotool output: "Position: X,Y" and "Geometry: WxH"
		var x, y, w, h int
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Position:") {
				pos := strings.TrimSpace(strings.TrimPrefix(line, "Position:"))
				pos = strings.Split(pos, " ")[0] // strip "(screen: N)"
				if coords := strings.SplitN(pos, ",", 2); len(coords) == 2 {
					x, _ = strconv.Atoi(coords[0])
					y, _ = strconv.Atoi(coords[1])
				}
			}
			if strings.HasPrefix(line, "Geometry:") {
				geom := strings.TrimSpace(strings.TrimPrefix(line, "Geometry:"))
				if dims := strings.SplitN(geom, "x", 2); len(dims) == 2 {
					w, _ = strconv.Atoi(dims[0])
					h, _ = strconv.Atoi(dims[1])
				}
			}
		}
		out, _ := json.Marshal(map[string]int{
			"x": x, "y": y, "width": w, "height": h,
		})
		fmt.Println(string(out))
		return nil
	}

	// Last fallback: wlr-randr output geometry (for Wayland-native maximized windows).
	randrOut, randrErr := captureWlCmd(engine, name, "wlr-randr 2>/dev/null")
	if randrErr == nil {
		for _, line := range strings.Split(string(randrOut), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "current") && strings.Contains(line, "px") {
				res := strings.Fields(line)[0] // "1280x720"
				if dims := strings.SplitN(res, "x", 2); len(dims) == 2 {
					w, _ := strconv.Atoi(dims[0])
					h, _ := strconv.Atoi(dims[1])
					out, _ := json.Marshal(map[string]int{
						"x": 0, "y": 0, "width": w, "height": h,
					})
					fmt.Println(string(out))
					fmt.Fprintf(os.Stderr, "Using output resolution (Wayland-native window assumed maximized)\n")
					return nil
				}
			}
		}
	}

	return fmt.Errorf("querying geometry for %q: not found via sway, xdotool, or wlr-randr", c.Target)
}

// WlAtspiCmd queries the accessibility tree via AT-SPI2 (python3-pyatspi).
type WlAtspiCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Action   string `arg:"" help:"Action: tree, find, click"`
	Query    string `arg:"" optional:"" help:"Search query (element name, role, or name:role)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// atspiScript is the Python helper for AT-SPI2 introspection, embedded as a Go
// constant. Executed inside the container via python3 -c.
const atspiScript = `
import gi, json, sys
gi.require_version("Atspi", "2.0")
from gi.repository import Atspi

Atspi.init()
action = sys.argv[1] if len(sys.argv) > 1 else "tree"
query = sys.argv[2] if len(sys.argv) > 2 else ""

def node_info(node, depth=0):
    if not node:
        return None
    name = node.get_name() or ""
    role = node.get_role_name() or ""
    info = {"name": name, "role": role, "depth": depth}
    try:
        comp = node.get_component_iface()
        if comp:
            ext = comp.get_extents(Atspi.CoordType.SCREEN)
            info["x"] = ext.x
            info["y"] = ext.y
            info["width"] = ext.width
            info["height"] = ext.height
    except Exception:
        pass
    acts = node.get_action_iface()
    if acts:
        info["actions"] = [acts.get_action_name(i) for i in range(acts.get_n_actions())]
    return info

def walk(node, depth=0, results=None):
    if results is None:
        results = []
    if not node:
        return results
    info = node_info(node, depth)
    if info:
        results.append(info)
    for i in range(node.get_child_count()):
        walk(node.get_child_at_index(i), depth + 1, results)
    return results

def find_matches(node, query, depth=0, results=None):
    if results is None:
        results = []
    if not node:
        return results
    name = (node.get_name() or "").lower()
    role = (node.get_role_name() or "").lower()
    q = query.lower()
    if ":" in q:
        qn, qr = q.split(":", 1)
        match = (qn in name) and (qr in role)
    else:
        match = (q in name) or (q in role)
    if match:
        info = node_info(node, depth)
        if info:
            results.append(info)
    for i in range(node.get_child_count()):
        find_matches(node.get_child_at_index(i), query, depth + 1, results)
    return results

def click_match(node, query, depth=0):
    if not node:
        return False
    name = (node.get_name() or "").lower()
    role = (node.get_role_name() or "").lower()
    q = query.lower()
    if ":" in q:
        qn, qr = q.split(":", 1)
        match = (qn in name) and (qr in role)
    else:
        match = (q in name) or (q in role)
    if match:
        acts = node.get_action_iface()
        if acts:
            for i in range(acts.get_n_actions()):
                aname = acts.get_action_name(i)
                if aname in ("click", "press", "activate"):
                    acts.do_action(i)
                    print(json.dumps({"clicked": True, "name": node.get_name(), "role": node.get_role_name(), "action": aname}))
                    return True
    for i in range(node.get_child_count()):
        if click_match(node.get_child_at_index(i), query, depth + 1):
            return True
    return False

desktop = Atspi.get_desktop(0)
if action == "tree":
    results = []
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        results.extend(walk(app))
    print(json.dumps(results, indent=2))
elif action == "find":
    if not query:
        print("Error: query required for find", file=sys.stderr)
        sys.exit(1)
    results = []
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        results.extend(find_matches(app, query))
    print(json.dumps(results, indent=2))
elif action == "click":
    if not query:
        print("Error: query required for click", file=sys.stderr)
        sys.exit(1)
    found = False
    for i in range(desktop.get_child_count()):
        app = desktop.get_child_at_index(i)
        if click_match(app, query):
            found = True
            break
    if not found:
        print(json.dumps({"clicked": False, "error": f"no clickable element matching '{query}'"}))
        sys.exit(1)
else:
    print(f"Unknown action: {action}", file=sys.stderr)
    sys.exit(1)
`

func (c *WlAtspiCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	switch c.Action {
	case "tree", "find", "click":
		// Build python3 -c command with the embedded script.
		// Use /usr/bin/python3 explicitly because pixi's python3 may be first
		// in PATH and won't have system RPM packages (python3-pyatspi, python3-gobject).
		var shellCmd string
		if c.Query != "" {
			shellCmd = fmt.Sprintf("/usr/bin/python3 -c %s %s %s",
				shellQuote(atspiScript), shellQuote(c.Action), shellQuote(c.Query))
		} else {
			shellCmd = fmt.Sprintf("/usr/bin/python3 -c %s %s",
				shellQuote(atspiScript), shellQuote(c.Action))
		}
		// AT-SPI2 needs DBUS_SESSION_BUS_ADDRESS.
		wrappedCmd := fmt.Sprintf(
			`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && %s`,
			shellCmd,
		)
		return execWlCmd(engine, name, wrappedCmd)
	default:
		return fmt.Errorf("unknown atspi action %q (valid: tree, find, click)", c.Action)
	}
}

// --- Sway subcommand structs ---

type WlSwayMsgCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Command  string `arg:"" help:"Sway command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayTreeCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayWorkspacesCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayOutputsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayFocusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Direction (left/right/up/down) or [criteria]"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayMoveCmd struct {
	Image    string   `arg:"" help:"Image name (use . for local)"`
	Target   []string `arg:"" help:"Direction, 'scratchpad', or 'workspace N'"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayResizeCmd struct {
	Image     string `arg:"" help:"Image name (use . for local)"`
	Dimension string `arg:"" help:"Dimension: width or height"`
	Amount    string `arg:"" help:"Amount (e.g. 10px, -10px, 5ppt)"`
	Instance  string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayKillCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayFloatingCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayLayoutCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Mode     string `arg:"" help:"Layout mode: tabbed, stacking, splitv, splith, toggle"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayWorkspaceCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Number   int    `arg:"" help:"Workspace number"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type WlSwayReloadCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

// --- Sway subcommand Run methods ---

func (c *WlSwayMsgCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, c.Command)
}

func (c *WlSwayTreeCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_tree")
}

func (c *WlSwayWorkspacesCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_workspaces")
}

func (c *WlSwayOutputsCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_outputs")
}

func (c *WlSwayFocusCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	// If target looks like criteria (contains brackets or =), wrap as [criteria] focus.
	if strings.Contains(c.Target, "=") || strings.HasPrefix(c.Target, "[") {
		criteria := c.Target
		if !strings.HasPrefix(criteria, "[") {
			criteria = "[" + criteria + "]"
		}
		return execSwaymsg(engine, name, criteria+" focus")
	}
	return execSwaymsg(engine, name, "focus", c.Target)
}

func (c *WlSwayMoveCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	target := strings.Join(c.Target, " ")
	if strings.HasPrefix(target, "workspace") {
		ws := strings.TrimPrefix(target, "workspace ")
		return execSwaymsg(engine, name, "move", "container", "to", "workspace", "number", ws)
	}
	return execSwaymsg(engine, name, "move", target)
}

func (c *WlSwayResizeCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	amount := c.Amount
	direction := "grow"
	if strings.HasPrefix(amount, "-") {
		direction = "shrink"
		amount = strings.TrimPrefix(amount, "-")
	}
	return execSwaymsg(engine, name, "resize", direction, c.Dimension, amount)
}

func (c *WlSwayKillCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "kill")
}

func (c *WlSwayFloatingCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "floating", "toggle")
}

func (c *WlSwayLayoutCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "layout", c.Mode)
}

func (c *WlSwayWorkspaceCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "workspace", "number", fmt.Sprintf("%d", c.Number))
}

func (c *WlSwayReloadCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "reload")
}

// --- Sway IPC helpers ---

// swaymsgShellCmd builds a shell command that discovers SWAYSOCK and runs swaymsg.
func swaymsgShellCmd(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return fmt.Sprintf(
		`export SWAYSOCK=$(ls -t /tmp/sway-ipc.*.sock 2>/dev/null | head -1) && [ -n "$SWAYSOCK" ] && swaymsg %s`,
		strings.Join(quoted, " "),
	)
}

// execSwaymsg runs swaymsg inside a container (or locally when engine is empty).
func execSwaymsg(engine, containerName string, args ...string) error {
	shellCmd := swaymsgShellCmd(args...)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", shellCmd)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", shellCmd)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// captureSwaymsg runs swaymsg and captures stdout as bytes.
func captureSwaymsg(engine, containerName string, args ...string) ([]byte, error) {
	shellCmd := swaymsgShellCmd(args...)
	var cmd *exec.Cmd
	if engine == "" {
		cmd = exec.Command("sh", "-c", shellCmd)
	} else {
		cmd = exec.Command(engine, "exec", containerName, "sh", "-c", shellCmd)
	}
	return cmd.Output()
}

// checkSwayStatus probes the Sway compositor via IPC socket.
func checkSwayStatus(engine, containerName string) ToolStatus {
	ts := ToolStatus{Name: "sway", Status: "-"}
	data, err := captureSwaymsg(engine, containerName, "-t", "get_outputs")
	if err != nil {
		return ts
	}
	ts.Status = "ok"
	var outputs []struct {
		Name        string `json:"name"`
		CurrentMode struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"current_mode"`
	}
	if err := json.Unmarshal(data, &outputs); err == nil && len(outputs) > 0 {
		o := outputs[0]
		ts.Detail = fmt.Sprintf("%s %dx%d", o.Name, o.CurrentMode.Width, o.CurrentMode.Height)
	}
	return ts
}

// SwayRect represents a window's position and size on the desktop.
type SwayRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type swayWindowProperties struct {
	Class string `json:"class"`
}

type swayNode struct {
	Name             string                `json:"name"`
	AppID            string                `json:"app_id"`
	Rect             SwayRect              `json:"rect"`
	Focused          bool                  `json:"focused"`
	FullscreenMode   int                   `json:"fullscreen_mode"`
	WindowProperties *swayWindowProperties `json:"window_properties,omitempty"`
	Nodes            []swayNode            `json:"nodes"`
	FloatingNodes    []swayNode            `json:"floating_nodes"`
}

// FindWindowRect searches the sway tree for a window matching appID or X11 class.
func FindWindowRect(engine, containerName, appID string) (SwayRect, error) {
	data, err := captureSwaymsg(engine, containerName, "-t", "get_tree")
	if err != nil {
		return SwayRect{}, fmt.Errorf("querying sway tree: %w", err)
	}
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return SwayRect{}, fmt.Errorf("parsing sway tree: %w", err)
	}
	rect, found := searchSwayNode(&root, appID)
	if !found {
		return SwayRect{}, fmt.Errorf("window with app_id or class %q not found in sway tree", appID)
	}
	return rect, nil
}

func searchSwayNode(node *swayNode, appID string) (SwayRect, bool) {
	var matches []swayNode
	collectSwayMatches(node, appID, &matches)
	if len(matches) == 0 {
		return SwayRect{}, false
	}
	best := matches[0]
	for _, m := range matches[1:] {
		if m.Focused {
			best = m
			break
		}
		if m.FullscreenMode > best.FullscreenMode {
			best = m
		} else if m.FullscreenMode == best.FullscreenMode &&
			m.Rect.Width*m.Rect.Height > best.Rect.Width*best.Rect.Height {
			best = m
		}
	}
	return best.Rect, true
}

func collectSwayMatches(node *swayNode, appID string, matches *[]swayNode) {
	matched := (node.AppID == appID) ||
		(node.WindowProperties != nil && node.WindowProperties.Class == appID)
	if matched && node.Rect.Width > 0 {
		*matches = append(*matches, *node)
	}
	for i := range node.Nodes {
		collectSwayMatches(&node.Nodes[i], appID, matches)
	}
	for i := range node.FloatingNodes {
		collectSwayMatches(&node.FloatingNodes[i], appID, matches)
	}
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// --- Helper functions ---

// wlrctlToplevel runs a wlrctl toplevel action matching by app_id.
// Works on all wlroots compositors (sway, labwc, niri).
func wlrctlToplevel(engine, containerName, action, target string) error {
	shellCmd := fmt.Sprintf("wlrctl toplevel %s %s", action, shellQuote(target))
	return execWlCmdSilent(engine, containerName, shellCmd)
}

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
	out, err := cmd.Output()
	if err != nil {
		// Extract stderr from ExitError so the user sees the actual reason
		// (e.g., "capture failed (not connected)") instead of bare "exit status 1"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
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
