package main

import (
	"fmt"
	"os"
	"os/exec"
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}

	enginePath, err := findExecutable(engine)
	if err != nil {
		return err
	}

	if tmuxHasSession(engine, name, c.Session) {
		// Attach to existing session
		args := []string{engine, "exec", "-it", name, "tmux", "attach-session", "-t", c.Session}
		return syscall.Exec(enginePath, args, os.Environ())
	}

	// Create new session and attach (new-session without -d attaches immediately)
	args := []string{engine, "exec", "-it", name, "tmux", "new-session", "-s", c.Session}
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}
	if !tmuxHasSession(engine, name, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, name)
	}

	if err := sendTmuxCommand(engine, name, c.Session, c.Command); err != nil {
		return err
	}

	if c.Notify {
		sendContainerNotification(engine, name,
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}
	if tmuxHasSession(engine, name, c.Session) {
		return fmt.Errorf("tmux session %q already exists in %s (use 'ov tmux attach' or 'ov tmux kill')", c.Session, name)
	}

	cmd := exec.Command(engine, "exec", name, "tmux", "new-session", "-d", "-s", c.Session, c.Command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting tmux session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Started tmux session %q in %s\n", c.Session, name)
	return nil
}

// TmuxAttachCmd attaches to an existing tmux session interactively.
type TmuxAttachCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxAttachCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}
	if !tmuxHasSession(engine, name, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, name)
	}

	enginePath, err := findExecutable(engine)
	if err != nil {
		return err
	}
	args := []string{engine, "exec", "-it", name, "tmux", "attach-session", "-t", c.Session}
	return syscall.Exec(enginePath, args, os.Environ())
}

// TmuxListCmd lists active tmux sessions in a container.
type TmuxListCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxListCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}

	err = execTmux(engine, name, "list-sessions")
	if err != nil {
		// tmux returns error when no server/sessions exist — not a real error
		fmt.Fprintf(os.Stderr, "No tmux sessions in %s\n", name)
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !tmuxHasSession(engine, name, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, name)
	}

	args := []string{"capture-pane", "-t", c.Session, "-p"}
	if c.Lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", c.Lines))
	}
	return execTmux(engine, name, args...)
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
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !tmuxHasSession(engine, name, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'ov tmux list' to see sessions)", c.Session, name)
	}

	args := []string{"send-keys", "-t", c.Session}
	if c.Literal {
		args = append(args, "-l")
	}
	args = append(args, c.Keys...)
	if c.Enter {
		args = append(args, "Enter")
	}
	return execTmux(engine, name, args...)
}

// TmuxKillCmd kills a tmux session.
type TmuxKillCmd struct {
	Image    string `arg:"" help:"Image name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxKillCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if err := execTmux(engine, name, "kill-session", "-t", c.Session); err != nil {
		return fmt.Errorf("killing tmux session %q: %w", c.Session, err)
	}
	fmt.Fprintf(os.Stderr, "Killed tmux session %q in %s\n", c.Session, name)
	return nil
}

// checkTmuxInstalled verifies tmux is available inside the container.
func checkTmuxInstalled(engine, containerName string) error {
	cmd := exec.Command(engine, "exec", containerName, "which", "tmux")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux is not installed in container %s (add the tmux layer to your image)", containerName)
	}
	return nil
}

// tmuxHasSession checks if a named tmux session exists in the container.
func tmuxHasSession(engine, containerName, session string) bool {
	cmd := exec.Command(engine, "exec", containerName, "tmux", "has-session", "-t", session)
	return cmd.Run() == nil
}

// execTmux runs a tmux command inside the container, connecting stdout/stderr.
func execTmux(engine, containerName string, args ...string) error {
	execArgs := append([]string{"exec", containerName, "tmux"}, args...)
	cmd := exec.Command(engine, execArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// captureTmux runs a tmux command inside the container and returns its stdout.
func captureTmux(engine, containerName string, args ...string) (string, error) {
	execArgs := append([]string{"exec", containerName, "tmux"}, args...)
	out, err := exec.Command(engine, execArgs...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sendTmuxCommand sends a command string followed by Enter to a tmux session.
func sendTmuxCommand(engine, containerName, session, command string) error {
	if err := execTmux(engine, containerName, "send-keys", "-t", session, "-l", command); err != nil {
		return fmt.Errorf("sending command to session %s: %w", session, err)
	}
	if err := execTmux(engine, containerName, "send-keys", "-t", session, "Enter"); err != nil {
		return fmt.Errorf("sending Enter to session %s: %w", session, err)
	}
	return nil
}
