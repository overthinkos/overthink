package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// TmuxCmd manages tmux sessions inside running containers.
type TmuxCmd struct {
	Attach  TmuxAttachCmd  `cmd:"" help:"Attach to an existing tmux session (interactive)"`
	Capture TmuxCaptureCmd `cmd:"" help:"Capture pane output from a session"`
	Cmd     TmuxCmdCmd     `cmd:"" help:"Send a command to a tmux session (with notification)"`
	Kill    TmuxKillCmd    `cmd:"" help:"Kill a tmux session"`
	List    TmuxListCmd    `cmd:"" help:"List active tmux sessions"`
	Run     TmuxRunCmd     `cmd:"" help:"Start a command in a new detached tmux session"`
	Send    TmuxSendCmd    `cmd:"" help:"Send keys to a running session"`
	Shell   TmuxShellCmd   `cmd:"" help:"Persistent shell — creates or reattaches to a tmux session"`
}

// TmuxShellCmd creates or reattaches to a persistent shell session.
// If the session exists, attaches. If not, creates it with bash and attaches.
type TmuxShellCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" default:"shell" help:"Session name (default: shell)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxShellCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}
	// Interactive attach replaces this process with `<engine> exec -it` for a
	// real TTY — inherently container-specific. For a VM the equivalent is an
	// ssh -t session.
	if !venue.IsContainer() {
		return fmt.Errorf("interactive tmux shell requires a container target; for a VM use: ov vm ssh %s -- tmux new-session -A -s %s", c.Image, c.Session)
	}

	enginePath, err := findExecutable(venue.Engine)
	if err != nil {
		return err
	}

	if tmuxHasSession(venue.Exec, c.Session) {
		// Attach to existing session
		args := []string{venue.Engine, "exec", "-it", venue.Name, "tmux", "attach-session", "-t", c.Session}
		return syscall.Exec(enginePath, args, os.Environ())
	}

	// Create new session and attach (new-session without -d attaches immediately)
	args := []string{venue.Engine, "exec", "-it", venue.Name, "tmux", "new-session", "-s", c.Session}
	return syscall.Exec(enginePath, args, os.Environ())
}

// TmuxCmdCmd sends a command to an existing tmux session with notification.
type TmuxCmdCmd struct {
	Image    string `arg:"" help:"Image name"`
	Command  string `arg:"" help:"Command to send"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Notify   bool   `long:"notify" negatable:"" default:"true" help:"Send desktop notification (--no-notify to disable)"`
}

func (c *TmuxCmdCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}
	if !tmuxHasSession(venue.Exec, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, c.Image)
	}

	if err := sendTmuxCommand(venue.Exec, c.Session, c.Command); err != nil {
		return err
	}

	if c.Notify {
		sendVenueNotification(venue.Exec,
			fmt.Sprintf("ov: sent to %s", c.Session),
			c.Command)
	}

	fmt.Fprintf(os.Stderr, "Sent to %s: %s\n", c.Session, c.Command)
	return nil
}

// TmuxRunCmd starts a command in a new detached tmux session.
type TmuxRunCmd struct {
	Image    string `arg:"" help:"Image name"`
	Command  string `arg:"" help:"Command to run in the session"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxRunCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}
	if tmuxHasSession(venue.Exec, c.Session) {
		return fmt.Errorf("tmux session %q already exists in %s (use 'ov tmux attach' or 'ov tmux kill')", c.Session, c.Image)
	}

	if err := execTmux(venue.Exec, "new-session", "-d", "-s", c.Session, c.Command); err != nil {
		return fmt.Errorf("starting tmux session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Started tmux session %q in %s\n", c.Session, c.Image)
	return nil
}

// TmuxAttachCmd attaches to an existing tmux session interactively.
type TmuxAttachCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxAttachCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}
	if !tmuxHasSession(venue.Exec, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, c.Image)
	}
	if !venue.IsContainer() {
		return fmt.Errorf("interactive tmux attach requires a container target; for a VM use: ov vm ssh %s -- tmux attach -t %s", c.Image, c.Session)
	}

	enginePath, err := findExecutable(venue.Engine)
	if err != nil {
		return err
	}
	args := []string{venue.Engine, "exec", "-it", venue.Name, "tmux", "attach-session", "-t", c.Session}
	return syscall.Exec(enginePath, args, os.Environ())
}

// TmuxListCmd lists active tmux sessions in a container.
type TmuxListCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxListCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}

	if err := execTmux(venue.Exec, "list-sessions"); err != nil {
		// tmux returns error when no server/sessions exist — not a real error
		fmt.Fprintf(os.Stderr, "No tmux sessions in %s\n", c.Image)
		return nil
	}
	return nil
}

// TmuxCaptureCmd captures pane output from a session.
type TmuxCaptureCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Lines    int    `short:"n" long:"lines" default:"0" help:"Number of history lines (0 = visible pane only)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxCaptureCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !tmuxHasSession(venue.Exec, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, c.Image)
	}

	args := []string{"capture-pane", "-t", c.Session, "-p"}
	if c.Lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", c.Lines))
	}
	return execTmux(venue.Exec, args...)
}

// TmuxSendCmd sends keys to a running tmux session.
type TmuxSendCmd struct {
	Image    string   `arg:"" help:"Image name"`
	Keys     []string `arg:"" help:"Keys to send (use tmux key names for special keys)"`
	Session  string   `short:"s" long:"session" required:"" help:"Session name"`
	Literal  bool     `short:"l" long:"literal" help:"Send keys literally (disable key name lookup)"`
	Enter    bool     `long:"enter" help:"Append Enter key after the keys"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxSendCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !tmuxHasSession(venue.Exec, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, c.Image)
	}

	args := []string{"send-keys", "-t", c.Session}
	if c.Literal {
		args = append(args, "-l")
	}
	args = append(args, c.Keys...)
	if c.Enter {
		args = append(args, "Enter")
	}
	return execTmux(venue.Exec, args...)
}

// TmuxKillCmd kills a tmux session.
type TmuxKillCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxKillCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if err := execTmux(venue.Exec, "kill-session", "-t", c.Session); err != nil {
		return fmt.Errorf("killing tmux session %q: %w", c.Session, err)
	}
	fmt.Fprintf(os.Stderr, "Killed tmux session %q in %s\n", c.Session, c.Image)
	return nil
}

// tmuxArgs shell-quotes each tmux argument and joins them, so a single-string
// RunCapture preserves arguments containing spaces / shell metacharacters
// (e.g. a `send-keys -l "<command>"` payload or a `-F "#{session_name}"` format).
func tmuxArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// checkTmuxInstalled verifies tmux is available on the venue (container/VM/host).
func checkTmuxInstalled(ex DeployExecutor) error {
	if !venueHasTool(ex, "tmux") {
		return fmt.Errorf("tmux is not installed on the target (add the tmux layer to your image, or install it on the host/VM)")
	}
	return nil
}

// tmuxHasSession checks if a named tmux session exists on the venue.
func tmuxHasSession(ex DeployExecutor, session string) bool {
	return venueRunSilent(ex, "tmux has-session -t "+shellQuote(session)) == nil
}

// execTmux runs a tmux command on the venue, streaming stdout/stderr.
func execTmux(ex DeployExecutor, args ...string) error {
	return venueRun(ex, "tmux "+tmuxArgs(args))
}

// captureTmux runs a tmux command on the venue and returns its trimmed stdout.
func captureTmux(ex DeployExecutor, args ...string) (string, error) {
	out, err := venueCapture(ex, "tmux "+tmuxArgs(args))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sendTmuxCommand sends a command string followed by Enter to a tmux session.
func sendTmuxCommand(ex DeployExecutor, session, command string) error {
	if err := execTmux(ex, "send-keys", "-t", session, "-l", command); err != nil {
		return fmt.Errorf("sending command to session %s: %w", session, err)
	}
	if err := execTmux(ex, "send-keys", "-t", session, "Enter"); err != nil {
		return fmt.Errorf("sending Enter to session %s: %w", session, err)
	}
	return nil
}
