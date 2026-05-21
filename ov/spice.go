package main

// `ov eval spice <vm-name> <verb>` — SPICE wire client.
//
// Connect to the VM's SPICE port (via libvirtxml-extracted address),
// handshake, and exercise the wire protocol: display channel
// screenshots, input channel clicks/keys, status reporting.

import (
	"context"
	"encoding/hex"
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	spice "github.com/Shells-com/spice"
)

// SpiceCmd groups all SPICE-wire test verbs.
type SpiceCmd struct {
	Status     SpiceStatusCmd     `cmd:"" help:"Handshake + report channel info"`
	Screenshot SpiceScreenshotCmd `cmd:"" help:"Capture display channel framebuffer (native SPICE decode)"`
	Click      SpiceClickCmd      `cmd:"" help:"Mouse button click at x,y"`
	Mouse      SpiceMouseCmd      `cmd:"" help:"Move pointer to x,y (no click)"`
	Type       SpiceTypeCmd       `cmd:"" help:"Type text as keyboard input"`
	Key        SpiceKeyCmd        `cmd:"" help:"Send a single key press/release"`
	Cursor     SpiceCursorCmd     `cmd:"" help:"Capture cursor bitmap + position"`
}

// ---------------- helper: open session from vm-name or --address ----------------

type spiceConnectFlags struct {
	Address  string `long:"address" help:"Bypass vm.yml lookup; host:port"`
	Socket   string `long:"socket" help:"Bypass vm.yml lookup; UNIX socket path (spice+unix://)"`
	Password string `long:"password" help:"SPICE password (for --address); empty = none"`
	Uri      string `name:"uri" env:"OV_LIBVIRT_URI" help:"Libvirt URI (default: qemu:///session). Use qemu+ssh://[user@]host/session for remote hypervisors (auto-tunnels the display socket)."`
}

// open resolves the SPICE endpoint for a VM and returns a connected
// session. Handles:
//   - --address host:port (direct TCP, no libvirt involvement)
//   - --socket /path     (direct UNIX socket, no libvirt involvement)
//   - --uri qemu+ssh://… (remote libvirt + auto SSH tunnel)
//   - default            (local qemu:///session)
//
// The returned session carries any tunnel-cleanup closure — callers
// must invoke Close() to tear it down.
func (f *spiceConnectFlags) open(vmName string) (*SpiceSession, error) {
	// Direct --address bypass (TCP).
	if f.Address != "" {
		parts := strings.SplitN(f.Address, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("--address must be host:port")
		}
		var port int
		if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil {
			return nil, fmt.Errorf("invalid port in --address: %v", err)
		}
		return DialSpiceTCP(parts[0], port, f.Password)
	}
	// Direct --socket bypass (UNIX).
	if f.Socket != "" {
		return DialSpiceUnix(f.Socket, f.Password)
	}
	// vm.yml path.
	t, err := ResolveVmTarget(vmName, f.Uri)
	if err != nil {
		return nil, err
	}
	ep, err := t.SpiceEndpoint()
	// Keep VmTarget's libvirt connection open only long enough to
	// read the endpoint; the SSH client inside VmTarget is closed
	// here too, so we must open a fresh one for tunneling when
	// remote. This separation makes the session independent.
	tunnelTarget := t.Uri
	t.Close()
	if err != nil {
		return nil, err
	}
	return dialSpiceEndpoint(ep, tunnelTarget)
}

// dialSpiceEndpoint opens a SpiceSession against a resolved
// DisplayEndpoint, setting up an SSH forward first when the endpoint
// is on a remote libvirt (uri is qemu+ssh://…).
func dialSpiceEndpoint(ep DisplayEndpoint, uri string) (*SpiceSession, error) {
	if !ep.TunnelNeeded {
		if ep.IsSocket {
			return DialSpiceUnix(ep.SocketPath, ep.Password)
		}
		return DialSpiceTCP(ep.Host, ep.Port, ep.Password)
	}
	// Remote — open an SSH tunnel forwarding the remote socket/TCP
	// endpoint to a local address, then dial that.
	parsed, err := ParseLibvirtURI(uri)
	if err != nil {
		return nil, err
	}
	tunnel, err := NewSSHTunnel(parsed.Remote)
	if err != nil {
		return nil, fmt.Errorf("ssh tunnel to %s: %w", parsed.Remote, err)
	}
	if ep.IsSocket {
		localSock, _, err := tunnel.ForwardUnix(context.Background(), ep.SocketPath)
		if err != nil {
			_ = tunnel.Close()
			return nil, fmt.Errorf("forwarding remote socket %s: %w", ep.SocketPath, err)
		}
		s, err := DialSpiceUnix(localSock, ep.Password)
		if err != nil {
			_ = tunnel.Close()
			return nil, err
		}
		s.tunnel = tunnel
		return s, nil
	}
	localAddr, _, err := tunnel.ForwardTCP(context.Background(), ep.Host, ep.Port)
	if err != nil {
		_ = tunnel.Close()
		return nil, fmt.Errorf("forwarding remote TCP %s:%d: %w", ep.Host, ep.Port, err)
	}
	parts := strings.SplitN(localAddr, ":", 2)
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)
	s, err := DialSpiceTCP(parts[0], port, ep.Password)
	if err != nil {
		_ = tunnel.Close()
		return nil, err
	}
	s.tunnel = tunnel
	return s, nil
}

// ---------------- status ----------------

type SpiceStatusCmd struct {
	Vm string `arg:"" help:"VM name"`
	spiceConnectFlags
}

func (c *SpiceStatusCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	// Wait briefly for channels to enumerate.
	_ = s.WaitForInputs(2 * time.Second)

	// Mirror cdp/wl/vnc status: emit "SPICE: ok" so probes can match
	// on `ok` for "handshake completed" without coupling to addr text.
	fmt.Println("SPICE:     ok")
	fmt.Printf("connected: %s\n", s.addr)
	if s.Display() != nil {
		b := s.Display().Bounds()
		fmt.Printf("display:   %dx%d\n", b.Dx(), b.Dy())
	} else {
		fmt.Println("display:   not yet received")
	}
	if s.Inputs() != nil {
		fmt.Println("inputs:    ready")
	} else {
		fmt.Println("inputs:    not yet ready")
	}
	if img, x, y := s.Cursor(); img != nil {
		fmt.Printf("cursor:    visible at (%d,%d)\n", x, y)
	} else {
		fmt.Println("cursor:    not yet received")
	}
	return nil
}

// ---------------- screenshot ----------------

type SpiceScreenshotCmd struct {
	Vm   string        `arg:"" help:"VM name"`
	File string        `arg:"" optional:"" default:"spice-screenshot.png" help:"Output file path (use '-' for stdout)"`
	Wait time.Duration `long:"wait" default:"5s" help:"Wait up to this for the first frame"`
	spiceConnectFlags
}

func (c *SpiceScreenshotCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WaitForDisplay(c.Wait); err != nil {
		return err
	}
	img := s.Display()
	if img == nil {
		return fmt.Errorf("no display frame available")
	}
	w, closeFn, err := openOutputPath(c.File)
	if err != nil {
		return fmt.Errorf("creating %s: %w", c.File, err)
	}
	if err := png.Encode(w, img); err != nil {
		_ = closeFn()
		return fmt.Errorf("encoding PNG: %w", err)
	}
	if err := closeFn(); err != nil {
		return err
	}
	b := img.Bounds()
	dest := c.File
	if dest == "-" {
		dest = "stdout"
	}
	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%dx%d, native SPICE display decode)\n",
		dest, b.Dx(), b.Dy())
	return nil
}

// ---------------- click / mouse ----------------

type SpiceClickCmd struct {
	Vm     string `arg:"" help:"VM name"`
	X      int    `arg:"" help:"X coordinate"`
	Y      int    `arg:"" help:"Y coordinate"`
	Button string `long:"button" default:"left" help:"left, right, middle"`
	spiceConnectFlags
}

func (c *SpiceClickCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return err
	}
	btn, err := spiceButtonCode(c.Button)
	if err != nil {
		return err
	}
	in := s.Inputs()
	in.MousePosition(uint32(c.X), uint32(c.Y))
	in.MouseDown(btn, uint32(c.X), uint32(c.Y))
	time.Sleep(50 * time.Millisecond)
	in.MouseUp(btn, uint32(c.X), uint32(c.Y))
	return nil
}

type SpiceMouseCmd struct {
	Vm string `arg:"" help:"VM name"`
	X  int    `arg:"" help:"X coordinate"`
	Y  int    `arg:"" help:"Y coordinate"`
	spiceConnectFlags
}

func (c *SpiceMouseCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return err
	}
	s.Inputs().MousePosition(uint32(c.X), uint32(c.Y))
	return nil
}

// ---------------- type / key ----------------

type SpiceTypeCmd struct {
	Vm   string `arg:"" help:"VM name"`
	Text string `arg:"" help:"Text to type"`
	spiceConnectFlags
}

func (c *SpiceTypeCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return err
	}
	in := s.Inputs()
	for _, r := range c.Text {
		scancode, shift := runeToScancode(r)
		if scancode == 0 {
			continue
		}
		if shift {
			in.OnKeyDown(encodeScancode(42)) // LeftShift
		}
		in.OnKeyDown(encodeScancode(scancode))
		time.Sleep(10 * time.Millisecond)
		in.OnKeyUp(encodeScancode(scancode))
		if shift {
			in.OnKeyUp(encodeScancode(42))
		}
	}
	return nil
}

type SpiceKeyCmd struct {
	Vm  string `arg:"" help:"VM name"`
	Key string `arg:"" help:"Key name (e.g. Return, Escape, F2)"`
	spiceConnectFlags
}

func (c *SpiceKeyCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.WaitForInputs(5 * time.Second); err != nil {
		return err
	}
	scancode, ok := spiceKeyNameToScancode[strings.ToLower(c.Key)]
	if !ok {
		return fmt.Errorf("unknown key: %s", c.Key)
	}
	in := s.Inputs()
	in.OnKeyDown(encodeScancode(scancode))
	time.Sleep(50 * time.Millisecond)
	in.OnKeyUp(encodeScancode(scancode))
	return nil
}

// ---------------- cursor ----------------

type SpiceCursorCmd struct {
	Vm   string        `arg:"" help:"VM name"`
	File string        `arg:"" optional:"" default:"spice-cursor.png" help:"Output file path (use '-' for stdout)"`
	Wait time.Duration `long:"wait" default:"5s" help:"Wait up to this for cursor data"`
	spiceConnectFlags
}

func (c *SpiceCursorCmd) Run() error {
	s, err := c.spiceConnectFlags.open(c.Vm)
	if err != nil {
		return err
	}
	defer s.Close()
	deadline := time.Now().Add(c.Wait)
	for time.Now().Before(deadline) {
		if img, x, y := s.Cursor(); img != nil {
			w, closeFn, err := openOutputPath(c.File)
			if err != nil {
				return err
			}
			if err := png.Encode(w, img); err != nil {
				_ = closeFn()
				return err
			}
			if err := closeFn(); err != nil {
				return err
			}
			b := img.Bounds()
			dest := c.File
			if dest == "-" {
				dest = "stdout"
			}
			fmt.Fprintf(os.Stderr, "Cursor saved to %s (%dx%d at position %d,%d)\n",
				dest, b.Dx(), b.Dy(), x, y)
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("no cursor data within %s", c.Wait)
}

// ---------------- helpers ----------------

func spiceButtonCode(name string) (uint8, error) {
	switch strings.ToLower(name) {
	case "left":
		return 1, nil
	case "right":
		return 2, nil
	case "middle":
		return 3, nil
	}
	return 0, fmt.Errorf("invalid button: %s (left/right/middle)", name)
}

// encodeScancode converts a PC AT scancode (1-byte for most keys) to
// the byte slice form Shells-com/spice's OnKeyDown/OnKeyUp expect.
// Two-byte codes (e.g. 0xE0 0x1C for numpad Enter) pack high byte first.
func encodeScancode(code uint8) []byte {
	return []byte{byte(code)}
}

func runeToScancode(r rune) (uint8, bool) {
	if r >= 'a' && r <= 'z' {
		return letterScancode[r-'a'], false
	}
	if r >= 'A' && r <= 'Z' {
		return letterScancode[r-'A'], true
	}
	if r >= '0' && r <= '9' {
		return digitScancode[r-'0'], false
	}
	switch r {
	case ' ':
		return 57, false
	case '.':
		return 52, false
	case ',':
		return 51, false
	case '/':
		return 53, false
	case '-':
		return 12, false
	case '=':
		return 13, false
	case ';':
		return 39, false
	case '\'':
		return 40, false
	case '\n':
		return 28, false
	}
	return 0, false
}

// PC AT scancodes (set 1). Indexed by 0-based letter/digit offset.
var letterScancode = [26]uint8{
	30, 48, 46, 32, 18, 33, 34, 35, 23, 36, 37, 38, 50,
	49, 24, 25, 16, 19, 31, 20, 22, 47, 17, 45, 21, 44,
}

var digitScancode = [10]uint8{
	11, 2, 3, 4, 5, 6, 7, 8, 9, 10,
}

// spiceKeyNameToScancode maps friendly names to scancodes for the
// SpiceKey command.
var spiceKeyNameToScancode = map[string]uint8{
	"return": 28, "enter": 28, "tab": 15,
	"escape": 1, "esc": 1,
	"backspace": 14, "space": 57, "capslock": 58,
	"f1": 59, "f2": 60, "f3": 61, "f4": 62, "f5": 63, "f6": 64,
	"f7": 65, "f8": 66, "f9": 67, "f10": 68, "f11": 87, "f12": 88,
	"up": 72, "down": 80, "left": 75, "right": 77,
	"home": 71, "end": 79, "pgup": 73, "pgdn": 81,
	"insert": 82, "delete": 83,
	"shift": 42, "ctrl": 29, "alt": 56, "meta": 125,
}

// unused import guard — if we later want raw-bytes debug output:
var _ = hex.EncodeToString
var _ = spice.Channel(0)
