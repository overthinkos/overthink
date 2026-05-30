package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// RecordCmd manages recording sessions (terminal and desktop) on the venue
// (container / VM / host) via the shared DeployExecutor.
type RecordCmd struct {
	Cmd   RecordCmdCmd   `cmd:"" help:"Send a command to the recording's terminal"`
	List  RecordListCmd  `cmd:"" help:"List active recording sessions"`
	Start RecordStartCmd `cmd:"" help:"Start a recording session"`
	Stop  RecordStopCmd  `cmd:"" help:"Stop a recording session and save output"`
}

// RecordStartCmd starts a recording session on the venue.
type RecordStartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Name     string `short:"n" long:"name" default:"default" help:"Recording name"`
	Mode     string `short:"m" long:"mode" enum:"terminal,desktop,auto" default:"auto" help:"Recording mode (terminal=asciinema, desktop=video)"`
	Fps      int    `long:"fps" default:"30" help:"Frames per second (desktop mode)"`
	Audio    bool   `long:"audio" help:"Include audio capture (desktop mode)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordStartCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := checkTmuxInstalled(venue.Exec); err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if tmuxHasSession(venue.Exec, session) {
		return fmt.Errorf("recording %q already active (session %s). Stop it first with: ov eval record stop %s -n %s",
			c.Name, session, c.Image, c.Name)
	}

	// Determine recording mode and tool
	tool, mode, err := c.resolveMode(venue.Exec)
	if err != nil {
		return err
	}

	// Create output directory on the venue.
	if err := venueRunSilent(venue.Exec, "mkdir -p /tmp/ov-recordings"); err != nil {
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

	// Start tmux session with the recorder command on the venue.
	if err := execTmux(venue.Exec, "new-session", "-d", "-s", session, recorderCmd); err != nil {
		return fmt.Errorf("starting recording session: %w", err)
	}

	// Write mode metadata for stop command (best-effort).
	modeFile := "/tmp/ov-recordings/" + c.Name + ".mode"
	_ = venueRunSilent(venue.Exec, fmt.Sprintf("printf '%%s' %s > %s", shellQuote(mode), shellQuote(modeFile)))

	fmt.Fprintf(os.Stderr, "Recording started (mode: %s, tool: %s, session: %s)\n", mode, tool, session)
	fmt.Fprintf(os.Stderr, "  Output: %s (on the target)\n", outFile)
	fmt.Fprintf(os.Stderr, "  Stop with: ov eval record stop %s -n %s [-o local-file]\n", c.Image, c.Name)
	return nil
}

// resolveMode determines the recording tool and mode based on --mode flag and available tools.
func (c *RecordStartCmd) resolveMode(ex DeployExecutor) (tool, mode string, err error) {
	switch c.Mode {
	case "terminal":
		if !venueHasTool(ex, "asciinema") {
			return "", "", fmt.Errorf("terminal recording requires asciinema (add the asciinema layer)")
		}
		return "asciinema", "terminal", nil
	case "desktop":
		tool, err := detectDesktopRecorder(ex)
		if err != nil {
			return "", "", err
		}
		return tool, "desktop", nil
	default: // auto
		tool, mode, err := detectRecordTool(ex)
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
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if !tmuxHasSession(venue.Exec, session) {
		return fmt.Errorf("no active recording %q (session %s not found). Use 'ov eval record list %s' to see active recordings",
			c.Name, session, c.Image)
	}

	// Read recording mode from metadata file
	mode := readRecordingMode(venue.Exec, c.Name)

	// Stop the recorder gracefully
	if mode == "terminal" {
		// Terminal recording (asciinema): send "exit" to end the shell
		_ = execTmux(venue.Exec, "send-keys", "-t", session, "exit", "Enter")
	} else {
		// Desktop recording (pixelflux-record, wf-recorder): send SIGINT
		_ = execTmux(venue.Exec, "send-keys", "-t", session, "C-c")
	}

	// Wait for session to exit gracefully (bounded readiness probe).
	stopped := false
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if !tmuxHasSession(venue.Exec, session) {
			stopped = true
			break
		}
	}

	if !stopped {
		// Force kill if still running
		_ = execTmux(venue.Exec, "kill-session", "-t", session)
		fmt.Fprintf(os.Stderr, "Recording session force-killed after timeout\n")
	}

	// Clean up mode metadata file
	cleanupModeFile(venue.Exec, c.Name)

	outFile := recordingFilePath(c.Name, mode)

	if c.Output != "" {
		// Pull the recording off the venue (podman exec cat / ssh cat / local read).
		data, err := venue.Exec.GetFile(context.Background(), outFile, false, EmitOpts{})
		if err != nil {
			return fmt.Errorf("copying recording: %w (file: %s)", err, outFile)
		}
		if err := os.WriteFile(c.Output, data, 0o644); err != nil {
			return fmt.Errorf("writing recording to %s: %w", c.Output, err)
		}

		// Get file size for display
		if info, err := os.Stat(c.Output); err == nil {
			fmt.Fprintf(os.Stderr, "Recording saved to %s (%s)\n", c.Output, formatSize(info.Size()))
		} else {
			fmt.Fprintf(os.Stderr, "Recording saved to %s\n", c.Output)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Recording stopped. File on the target: %s\n", outFile)
		fmt.Fprintf(os.Stderr, "  Copy with: ov eval record stop %s -n %s -o <local-path>\n", c.Image, c.Name)
	}

	return nil
}

// RecordListCmd lists active recording sessions.
type RecordListCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *RecordListCmd) Run() error {
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// List tmux sessions with "record-" prefix
	out, err := captureTmux(venue.Exec, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		fmt.Fprintf(os.Stderr, "No active recordings in %s\n", c.Image)
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
		fmt.Fprintf(os.Stderr, "No active recordings in %s\n", c.Image)
		return nil
	}

	fmt.Printf("%-20s %-10s %s\n", "NAME", "MODE", "FILE")
	for _, session := range recordings {
		recName := strings.TrimPrefix(session, "record-")
		mode := readRecordingMode(venue.Exec, recName)
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
	venue, err := resolveEvalVenue(c.Image, c.Instance)
	if err != nil {
		return err
	}

	session := recordSessionName(c.Name)
	if !tmuxHasSession(venue.Exec, session) {
		return fmt.Errorf("no active recording %q (session %s not found)", c.Name, session)
	}

	if err := sendTmuxCommand(venue.Exec, session, c.Command); err != nil {
		return err
	}

	sendVenueNotification(venue.Exec,
		fmt.Sprintf("ov: sent to %s", session), c.Command)

	fmt.Fprintf(os.Stderr, "Sent to %s: %s\n", session, c.Command)
	return nil
}

// --- Helper functions ---

// recordSessionName returns the tmux session name for a recording.
func recordSessionName(name string) string {
	return "record-" + name
}

// recordingFilePath returns the on-target path for a recording.
func recordingFilePath(name, mode string) string {
	ext := ".mp4"
	if mode == "terminal" {
		ext = ".cast"
	}
	return "/tmp/ov-recordings/" + name + ext
}

// detectDesktopRecorder finds the best available desktop recording tool on the venue.
func detectDesktopRecorder(ex DeployExecutor) (string, error) {
	if venueHasTool(ex, "pixelflux-record") {
		return "pixelflux-record", nil
	}
	if venueHasTool(ex, "wf-recorder") {
		return "wf-recorder", nil
	}
	return "", fmt.Errorf("no desktop recorder available (need pixelflux-record or wf-recorder)")
}

// detectRecordTool probes the venue for available recording tools (auto mode).
func detectRecordTool(ex DeployExecutor) (tool, mode string, err error) {
	// Desktop tools first (more specific)
	if venueHasTool(ex, "pixelflux-record") {
		return "pixelflux-record", "desktop", nil
	}
	if venueHasTool(ex, "wf-recorder") {
		return "wf-recorder", "desktop", nil
	}
	// Terminal fallback
	if venueHasTool(ex, "asciinema") {
		return "asciinema", "terminal", nil
	}
	return "", "", fmt.Errorf("no recording tool available (need asciinema, pixelflux-record, or wf-recorder)")
}

// readRecordingMode reads the recording mode from the .mode metadata file on
// the venue. Returns "terminal" or "desktop". Falls back to "desktop" if missing.
func readRecordingMode(ex DeployExecutor, name string) string {
	modeFile := "/tmp/ov-recordings/" + name + ".mode"
	out, err := venueCapture(ex, "cat "+shellQuote(modeFile))
	if err == nil {
		mode := strings.TrimSpace(string(out))
		if mode == "terminal" || mode == "desktop" {
			return mode
		}
	}
	return "desktop" // default fallback
}

// cleanupModeFile removes the .mode metadata file after stopping a recording.
func cleanupModeFile(ex DeployExecutor, name string) {
	modeFile := "/tmp/ov-recordings/" + name + ".mode"
	_ = venueRunSilent(ex, "rm -f "+shellQuote(modeFile))
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
