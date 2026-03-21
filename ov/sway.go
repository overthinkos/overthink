package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SwayCmd controls the Sway compositor in running containers.
type SwayCmd struct {
	Msg        SwayMsgCmd        `cmd:"" help:"Run a sway command"`
	Tree       SwayTreeCmd       `cmd:"" help:"Get window/container tree (JSON)"`
	Workspaces SwayWorkspacesCmd `cmd:"" help:"List workspaces (JSON)"`
	Outputs    SwayOutputsCmd    `cmd:"" help:"List outputs (JSON)"`
	Exec       SwayExecCmd       `cmd:"" help:"Launch an application"`
	Focus      SwayFocusCmd      `cmd:"" help:"Focus a window by direction or criteria"`
	Move       SwayMoveCmd       `cmd:"" help:"Move focused window (direction, workspace, or scratchpad)"`
	Resize     SwayResizeCmd     `cmd:"" help:"Resize focused window"`
	Kill       SwayKillCmd       `cmd:"" help:"Close the focused window"`
	Fullscreen SwayFullscreenCmd `cmd:"" help:"Toggle fullscreen on focused window"`
	Floating   SwayFloatingCmd   `cmd:"" help:"Toggle floating on focused window"`
	Layout     SwayLayoutCmd     `cmd:"" help:"Set layout mode"`
	Workspace  SwayWorkspaceCmd  `cmd:"" help:"Switch to a workspace"`
	Reload     SwayReloadCmd     `cmd:"" help:"Reload sway configuration"`
	Resolution SwayResolutionCmd `cmd:"" help:"Set output resolution"`
}

// --- Subcommand structs ---

type SwayMsgCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Command  string `arg:"" help:"Sway command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayTreeCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayWorkspacesCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayOutputsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayExecCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayFocusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Target   string `arg:"" help:"Direction (left/right/up/down) or [criteria]"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayMoveCmd struct {
	Image    string   `arg:"" help:"Image name (use . for local)"`
	Target   []string `arg:"" help:"Direction, 'scratchpad', or 'workspace N'"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
}

type SwayResizeCmd struct {
	Image     string `arg:"" help:"Image name (use . for local)"`
	Dimension string `arg:"" help:"Dimension: width or height"`
	Amount    string `arg:"" help:"Amount (e.g. 10px, -10px, 5ppt)"`
	Instance  string `short:"i" long:"instance" help:"Instance name"`
}

type SwayKillCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayFullscreenCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayFloatingCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayLayoutCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Mode     string `arg:"" help:"Layout mode: tabbed, stacking, splitv, splith, toggle"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayWorkspaceCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Number   int    `arg:"" help:"Workspace number"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayReloadCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

type SwayResolutionCmd struct {
	Image      string `arg:"" help:"Image name (use . for local)"`
	Resolution string `arg:"" help:"Resolution (e.g. 1920x1080, 2560x1440)"`
	Output     string `short:"o" long:"output" default:"HEADLESS-1" help:"Output name"`
	Instance   string `short:"i" long:"instance" help:"Instance name"`
}

// --- Run methods ---

func (c *SwayMsgCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, c.Command)
}

func (c *SwayTreeCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_tree")
}

func (c *SwayWorkspacesCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_workspaces")
}

func (c *SwayOutputsCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "-t", "get_outputs")
}

func (c *SwayExecCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "exec", c.Command)
}

func (c *SwayFocusCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	// If target looks like a criteria (contains brackets or =), wrap as [criteria] focus
	if strings.Contains(c.Target, "=") || strings.HasPrefix(c.Target, "[") {
		criteria := c.Target
		if !strings.HasPrefix(criteria, "[") {
			criteria = "[" + criteria + "]"
		}
		return execSwaymsg(engine, name, criteria+" focus")
	}
	return execSwaymsg(engine, name, "focus", c.Target)
}

func (c *SwayMoveCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	target := strings.Join(c.Target, " ")
	if strings.HasPrefix(target, "workspace") {
		// "workspace 2" -> "move container to workspace number 2"
		ws := strings.TrimPrefix(target, "workspace ")
		return execSwaymsg(engine, name, "move", "container", "to", "workspace", "number", ws)
	}
	return execSwaymsg(engine, name, "move", target)
}

func (c *SwayResizeCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
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

func (c *SwayKillCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "kill")
}

func (c *SwayFullscreenCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "fullscreen", "toggle")
}

func (c *SwayFloatingCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "floating", "toggle")
}

func (c *SwayLayoutCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "layout", c.Mode)
}

func (c *SwayWorkspaceCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "workspace", "number", fmt.Sprintf("%d", c.Number))
}

func (c *SwayReloadCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "reload")
}

func (c *SwayResolutionCmd) Run() error {
	engine, name, err := resolveSwayContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execSwaymsg(engine, name, "output", c.Output, "resolution", c.Resolution)
}

// --- Helpers ---

// resolveSwayContainer resolves the engine and container name.
// Use "." as image name for local mode (direct swaymsg execution).
func resolveSwayContainer(image, instance string) (engine, name string, err error) {
	return resolveContainer(image, instance)
}

// swaymsgShellCmd builds a shell command string that discovers SWAYSOCK and runs swaymsg.
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

// captureSwaymsg runs swaymsg and captures stdout as bytes (instead of piping to os.Stdout).
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

// SwayRect represents a window's position and size on the desktop.
type SwayRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type swayNode struct {
	Name          string     `json:"name"`
	AppID         string     `json:"app_id"`
	Rect          SwayRect   `json:"rect"`
	Nodes         []swayNode `json:"nodes"`
	FloatingNodes []swayNode `json:"floating_nodes"`
}

// FindWindowRect searches the sway tree for a window matching appID and returns its rect.
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
		return SwayRect{}, fmt.Errorf("window with app_id %q not found in sway tree", appID)
	}
	return rect, nil
}

func searchSwayNode(node *swayNode, appID string) (SwayRect, bool) {
	if node.AppID == appID && node.Rect.Width > 0 {
		return node.Rect, true
	}
	for i := range node.Nodes {
		if r, ok := searchSwayNode(&node.Nodes[i], appID); ok {
			return r, true
		}
	}
	for i := range node.FloatingNodes {
		if r, ok := searchSwayNode(&node.FloatingNodes[i], appID); ok {
			return r, true
		}
	}
	return SwayRect{}, false
}

// shellQuote wraps a string in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
