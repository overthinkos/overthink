package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RecordCmd manages recording sessions (terminal and desktop) inside containers.
type RecordCmd struct {
	Start RecordStartCmd `cmd:"" help:"Start a recording session"`
	Stop  RecordStopCmd  `cmd:"" help:"Stop a recording session and save output"`
	List  RecordListCmd  `cmd:"" help:"List active recording sessions"`
	Cmd   RecordCmdCmd   `cmd:"" help:"Send a command to the recording's terminal"`
	Term  RecordTermCmd  `cmd:"" help:"Run a command in a visible desktop terminal"`
}

// RecordStartCmd starts a recording session inside a container.
type RecordStartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Name     string `short:"n" long:"name" default:"default" help:"Recording name"`
	Mode     string `short:"m" long:"mode" enum:"terminal,desktop,auto" default:"auto" help:"Recording mode (terminal=asciinema, desktop=video)"`
	Fps      int    `long:"fps" default:"30" help:"Frames per second (desktop mode)"`
	Audio    bool   `long:"audio" help:"Include audio capture (desktop mode)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordStartCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(engine, name); err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if tmuxHasSession(engine, name, session) {
		return fmt.Errorf("recording %q already active (session %s). Stop it first with: ov record stop %s -n %s",
			c.Name, session, c.Image, c.Name)
	}

	// Determine recording mode and tool
	tool, mode, err := c.resolveMode(engine, name)
	if err != nil {
		return err
	}

	// Create output directory
	mkdirCmd := exec.Command(engine, "exec", name, "mkdir", "-p", "/tmp/ov-recordings")
	if err := mkdirCmd.Run(); err != nil {
		return fmt.Errorf("creating recording directory: %w", err)
	}

	// Build recorder command
	outFile := recordingFilePath(c.Name, mode)
	var recorderCmd string
	switch tool {
	case "asciinema":
		recorderCmd = fmt.Sprintf("asciinema rec %s", shellQuote(outFile))
	case "pixelflux-record":
		recorderCmd = fmt.Sprintf("pixelflux-record %s --fps %d", shellQuote(outFile), c.Fps)
		if c.Audio {
			recorderCmd += " --audio"
		}
	case "wf-recorder":
		recorderCmd = fmt.Sprintf(
			"env XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/tmp} WAYLAND_DISPLAY=${WAYLAND_DISPLAY:-wayland-0} wf-recorder -f %s",
			shellQuote(outFile))
		if c.Audio {
			recorderCmd += " --audio"
		}
		if c.Fps > 0 {
			recorderCmd += fmt.Sprintf(" -r %d", c.Fps)
		}
	}

	// Start tmux session with the recorder command
	cmd := exec.Command(engine, "exec", name, "tmux", "new-session", "-d", "-s", session, recorderCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting recording session: %w", err)
	}

	// Write mode metadata for stop command
	modeFile := "/tmp/ov-recordings/" + c.Name + ".mode"
	modeCmd := exec.Command(engine, "exec", name, "sh", "-c",
		fmt.Sprintf("echo %s > %s", shellQuote(mode), shellQuote(modeFile)))
	modeCmd.Run() // best-effort

	fmt.Fprintf(os.Stderr, "Recording started (mode: %s, tool: %s, session: %s)\n", mode, tool, session)
	fmt.Fprintf(os.Stderr, "  Output: %s (inside container)\n", outFile)
	fmt.Fprintf(os.Stderr, "  Stop with: ov record stop %s -n %s [-o local-file]\n", c.Image, c.Name)
	return nil
}

// resolveMode determines the recording tool and mode based on --mode flag and available tools.
func (c *RecordStartCmd) resolveMode(engine, containerName string) (tool, mode string, err error) {
	switch c.Mode {
	case "terminal":
		if err := checkToolAvailable(engine, containerName, "asciinema"); err != nil {
			return "", "", fmt.Errorf("terminal recording requires asciinema (add the asciinema layer)")
		}
		return "asciinema", "terminal", nil
	case "desktop":
		tool, err := detectDesktopRecorder(engine, containerName)
		if err != nil {
			return "", "", err
		}
		return tool, "desktop", nil
	default: // auto
		tool, mode, err := detectRecordTool(engine, containerName)
		if err != nil {
			return "", "", err
		}
		return tool, mode, nil
	}
}

// RecordStopCmd stops a recording session and optionally copies the output to the host.
type RecordStopCmd struct {
	Image    string `arg:"" help:"Image name"`
	Name     string `short:"n" long:"name" default:"default" help:"Recording name"`
	Output   string `short:"o" long:"output" help:"Copy recording to this local path"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordStopCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if !tmuxHasSession(engine, name, session) {
		return fmt.Errorf("no active recording %q (session %s not found). Use 'ov record list %s' to see active recordings",
			c.Name, session, c.Image)
	}

	// Read recording mode from metadata file
	mode := readRecordingMode(engine, name, c.Name)

	// Stop the recorder gracefully
	if mode == "terminal" {
		// Terminal recording (asciinema): send "exit" to end the shell
		execTmux(engine, name, "send-keys", "-t", session, "exit", "Enter")
	} else {
		// Desktop recording (pixelflux-record, wf-recorder): send SIGINT
		execTmux(engine, name, "send-keys", "-t", session, "C-c")
	}

	// Wait for session to exit gracefully
	stopped := false
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if !tmuxHasSession(engine, name, session) {
			stopped = true
			break
		}
	}

	if !stopped {
		// Force kill if still running
		execTmux(engine, name, "kill-session", "-t", session)
		fmt.Fprintf(os.Stderr, "Recording session force-killed after timeout\n")
	}

	// Clean up mode metadata file
	cleanupModeFile(engine, name, c.Name)

	outFile := recordingFilePath(c.Name, mode)

	if c.Output != "" {
		// Copy file from container to host
		cpCmd := exec.Command(engine, "cp", name+":"+outFile, c.Output)
		cpCmd.Stdout = os.Stdout
		cpCmd.Stderr = os.Stderr
		if err := cpCmd.Run(); err != nil {
			return fmt.Errorf("copying recording: %w (file: %s)", err, outFile)
		}

		// Get file size for display
		info, err := os.Stat(c.Output)
		if err == nil {
			fmt.Fprintf(os.Stderr, "Recording saved to %s (%s)\n", c.Output, formatSize(info.Size()))
		} else {
			fmt.Fprintf(os.Stderr, "Recording saved to %s\n", c.Output)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Recording stopped. File inside container: %s\n", outFile)
		fmt.Fprintf(os.Stderr, "  Copy with: ov record stop %s -n %s -o <local-path>\n", c.Image, c.Name)
	}

	return nil
}

// RecordListCmd lists active recording sessions.
type RecordListCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordListCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// List tmux sessions with "record-" prefix
	out, err := captureTmux(engine, name, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		fmt.Fprintf(os.Stderr, "No active recordings in %s\n", name)
		return nil
	}

	var recordings []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "record-") {
			recordings = append(recordings, line)
		}
	}

	if len(recordings) == 0 {
		fmt.Fprintf(os.Stderr, "No active recordings in %s\n", name)
		return nil
	}

	fmt.Printf("%-20s %-10s %s\n", "NAME", "MODE", "FILE")
	for _, session := range recordings {
		recName := strings.TrimPrefix(session, "record-")
		mode := readRecordingMode(engine, name, recName)
		file := recordingFilePath(recName, mode)
		fmt.Printf("%-20s %-10s %s\n", recName, mode, file)
	}

	return nil
}

// RecordCmdCmd sends a command to the recording's tmux session.
type RecordCmdCmd struct {
	Image    string `arg:"" help:"Image name"`
	Command  string `arg:"" help:"Command to send to the recording terminal"`
	Name     string `short:"n" long:"name" default:"default" help:"Recording name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordCmdCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if !tmuxHasSession(engine, name, session) {
		return fmt.Errorf("no active recording %q (session %s not found)", c.Name, session)
	}

	// Send command text followed by Enter
	if err := execTmux(engine, name, "send-keys", "-t", session, "-l", c.Command); err != nil {
		return fmt.Errorf("sending command: %w", err)
	}
	if err := execTmux(engine, name, "send-keys", "-t", session, "Enter"); err != nil {
		return fmt.Errorf("sending Enter: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Sent to %s: %s\n", session, c.Command)
	return nil
}

// RecordTermCmd runs a command in a visible desktop terminal (for video recording).
type RecordTermCmd struct {
	Image    string `arg:"" help:"Image name"`
	Command  string `arg:"" help:"Command to run in a visible terminal"`
	Name     string `short:"n" long:"name" default:"default" help:"Recording name"`
	Terminal string `long:"terminal" default:"" help:"Terminal emulator (auto-detected: xterm, xfce4-terminal)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordTermCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Warn if no active desktop recording (but don't error — the command is still useful)
	session := recordSessionName(c.Name)
	if !tmuxHasSession(engine, name, session) {
		fmt.Fprintf(os.Stderr, "Warning: no active recording %q — terminal will still launch\n", c.Name)
	}

	// Auto-detect terminal emulator
	term := c.Terminal
	if term == "" {
		var err error
		term, err = detectTerminal(engine, name)
		if err != nil {
			return err
		}
	}

	// Build terminal command with -hold to keep window open after command exits
	var termCmd string
	switch term {
	case "xterm":
		termCmd = fmt.Sprintf("xterm -hold -e %s", shellQuote(c.Command))
	case "xfce4-terminal":
		termCmd = fmt.Sprintf("xfce4-terminal --hold -e %s", shellQuote(c.Command))
	default:
		// Generic fallback: assume -e flag works
		termCmd = fmt.Sprintf("%s -e %s", term, shellQuote(c.Command))
	}

	// Launch via Wayland exec (same pattern as WlExecCmd)
	shellCmd := fmt.Sprintf("export DISPLAY=:0; %s &", termCmd)
	if err := execWlCmd(engine, name, shellCmd); err != nil {
		return fmt.Errorf("launching terminal: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Launched %q in %s (visible in desktop recording)\n", c.Command, term)
	return nil
}

// --- Helper functions ---

// recordSessionName returns the tmux session name for a recording.
func recordSessionName(name string) string {
	return "record-" + name
}

// recordingFilePath returns the in-container path for a recording.
func recordingFilePath(name, mode string) string {
	ext := ".mp4"
	if mode == "terminal" {
		ext = ".cast"
	}
	return "/tmp/ov-recordings/" + name + ext
}

// checkToolAvailable verifies a tool is installed inside the container.
func checkToolAvailable(engine, containerName, tool string) error {
	cmd := exec.Command(engine, "exec", containerName, "which", tool)
	return cmd.Run()
}

// detectDesktopRecorder finds the best available desktop recording tool.
func detectDesktopRecorder(engine, containerName string) (string, error) {
	if checkToolAvailable(engine, containerName, "pixelflux-record") == nil {
		return "pixelflux-record", nil
	}
	if checkToolAvailable(engine, containerName, "wf-recorder") == nil {
		return "wf-recorder", nil
	}
	return "", fmt.Errorf("no desktop recorder available (need pixelflux-record or wf-recorder)")
}

// detectRecordTool probes the container for available recording tools (auto mode).
func detectRecordTool(engine, containerName string) (tool, mode string, err error) {
	// Desktop tools first (more specific)
	if checkToolAvailable(engine, containerName, "pixelflux-record") == nil {
		return "pixelflux-record", "desktop", nil
	}
	if checkToolAvailable(engine, containerName, "wf-recorder") == nil {
		return "wf-recorder", "desktop", nil
	}
	// Terminal fallback
	if checkToolAvailable(engine, containerName, "asciinema") == nil {
		return "asciinema", "terminal", nil
	}
	return "", "", fmt.Errorf("no recording tool available (need asciinema, pixelflux-record, or wf-recorder)")
}

// readRecordingMode reads the recording mode from the .mode metadata file.
// Returns "terminal" or "desktop". Falls back to "desktop" if file is missing.
func readRecordingMode(engine, containerName, name string) string {
	modeFile := "/tmp/ov-recordings/" + name + ".mode"
	out, err := exec.Command(engine, "exec", containerName, "cat", modeFile).Output()
	if err == nil {
		mode := strings.TrimSpace(string(out))
		if mode == "terminal" || mode == "desktop" {
			return mode
		}
	}
	return "desktop" // default fallback
}

// cleanupModeFile removes the .mode metadata file after stopping a recording.
func cleanupModeFile(engine, containerName, name string) {
	modeFile := "/tmp/ov-recordings/" + name + ".mode"
	exec.Command(engine, "exec", containerName, "rm", "-f", modeFile).Run()
}

// detectTerminal finds the best available terminal emulator inside the container.
func detectTerminal(engine, containerName string) (string, error) {
	if checkToolAvailable(engine, containerName, "xterm") == nil {
		return "xterm", nil
	}
	if checkToolAvailable(engine, containerName, "xfce4-terminal") == nil {
		return "xfce4-terminal", nil
	}
	return "", fmt.Errorf("no terminal emulator available (need xterm or xfce4-terminal)")
}

// formatSize returns a human-readable file size string.
func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
