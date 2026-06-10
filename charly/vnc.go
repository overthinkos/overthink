package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"net"
	"os"
	"strings"
	"time"
)

// VncCmd manages VNC desktop interaction in running containers and VMs.
// The top-level verbs (Click/Key/Mouse/etc.) target containers; the `Vm`
// subcommand group targets kind:vm entities declared in vm.yml.
type VncCmd struct {
	Click      VncClickCmd      `cmd:"" help:"Click at x,y coordinates"`
	Key        VncKeyCmd        `cmd:"" help:"Send a key press/release event"`
	Mouse      VncMouseCmd      `cmd:"" help:"Move mouse to x,y without clicking"`
	Passwd     VncPasswdCmd     `cmd:"" help:"Set VNC password for a deployment"`
	Rfb        VncRfbCmd        `cmd:"" help:"Send a raw RFB command"`
	Screenshot VncScreenshotCmd `cmd:"" help:"Capture VNC framebuffer as PNG"`
	Status     VncStatusCmd     `cmd:"" help:"Show VNC server status and display info"`
	Type       VncTypeCmd       `cmd:"" help:"Type text as keyboard input"`

	// VM-targeted verbs mirror the SPICE command shape. Resolves the
	// VM via vm.yml + libvirt, discovers the VNC listener, and
	// auto-tunnels it if the libvirt URI is remote.
	Vm VncVmCmd `cmd:"" help:"VM-targeted VNC verbs (status/screenshot/click/key/type/mouse)"`
}

// VncScreenshotCmd captures the VNC framebuffer as a PNG image.
type VncScreenshotCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	File     string `arg:"" optional:"" default:"screenshot.png" help:"Output file path"`
	Instance string `short:"i" long:"instance" help:"Instance name for multi-instance containers"`
}

func (c *VncScreenshotCmd) Run() error {
	img, w, h, err := connectVNCScreenshot(c.Box, c.Instance)
	if err != nil {
		return err
	}

	f, err := os.Create(c.File)
	if err != nil {
		return fmt.Errorf("creating file %s: %w", c.File, err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("encoding PNG: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Screenshot saved to %s (%dx%d)\n", c.File, w, h)
	return nil
}

// VncClickCmd sends a pointer click at the given coordinates.
type VncClickCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	X        uint16 `arg:"" help:"X coordinate"`
	Y        uint16 `arg:"" help:"Y coordinate"`
	Button   string `long:"button" default:"left" help:"Mouse button (left, right, middle)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	FromCDP  string `long:"from-cdp" help:"Translate from CDP viewport coords using this tab ID (queries window.screenX/screenY)"`
	FromSway string `long:"from-sway" help:"Translate from window-relative coords using sway window rect for this app_id"`
	FromX11  string `name:"from-x11" help:"Translate from X11 window-internal coords (scales for XWayland fullscreen)"`
}

func (c *VncClickCmd) Run() error {
	clickX, clickY := c.X, c.Y

	// Translate from CDP viewport coordinates to desktop coordinates.
	if c.FromCDP != "" {
		client, err := connectTab(c.Box, c.FromCDP, c.Instance)
		if err != nil {
			return fmt.Errorf("connecting to CDP tab %s for coordinate translation: %w", c.FromCDP, err)
		}
		offset, err := cdpGetWindowOffset(client)
		client.Close()
		if err != nil {
			return fmt.Errorf("getting CDP window offset: %w", err)
		}
		clickX = uint16(float64(c.X) + offset.ScreenX)
		clickY = uint16(float64(c.Y) + offset.ScreenY + offset.ChromeHeight)
		fmt.Fprintf(os.Stderr, "Translated viewport (%d, %d) → desktop (%d, %d) via CDP tab %s\n",
			c.X, c.Y, clickX, clickY, c.FromCDP)
	}

	// Translate from window-relative coordinates to desktop coordinates via sway.
	if c.FromSway != "" {
		venue, err := resolveEvalVenue(c.Box, c.Instance)
		if err != nil {
			return fmt.Errorf("resolving venue for sway: %w", err)
		}
		rect, err := FindWindowRect(venue.Exec, c.FromSway)
		if err != nil {
			return err
		}
		clickX = uint16(int(c.X) + rect.X)
		clickY = uint16(int(c.Y) + rect.Y)
		fmt.Fprintf(os.Stderr, "Translated window-relative (%d, %d) → desktop (%d, %d) via sway app_id=%s\n",
			c.X, c.Y, clickX, clickY, c.FromSway)
	}

	// Translate from X11 window-internal coordinates to desktop coordinates.
	if c.FromX11 != "" {
		venue, err := resolveEvalVenue(c.Box, c.Instance)
		if err != nil {
			return fmt.Errorf("resolving venue for X11: %w", err)
		}
		rect, err := FindWindowRect(venue.Exec, c.FromX11)
		if err != nil {
			return err
		}
		x11W, x11H, err := FindX11WindowGeometry(venue.Exec, c.FromX11)
		if err != nil {
			return err
		}
		clickX = uint16(rect.X + (int(c.X)*rect.Width)/x11W)
		clickY = uint16(rect.Y + (int(c.Y)*rect.Height)/x11H)
		fmt.Fprintf(os.Stderr, "Translated X11 (%d, %d) → desktop (%d, %d) (x11=%dx%d sway=%dx%d)\n",
			c.X, c.Y, clickX, clickY, x11W, x11H, rect.Width, rect.Height)
	}

	vncClient, err := connectVNC(c.Box, c.Instance)
	if err != nil {
		return err
	}
	defer vncClient.Close()

	if err := vncClient.PointerClick(clickX, clickY, vncButton(c.Button)); err != nil {
		return fmt.Errorf("clicking at (%d, %d): %w", clickX, clickY, err)
	}
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(os.Stderr, "Clicked %s at (%d, %d)\n", c.Button, clickX, clickY)
	return nil
}

// VncTypeCmd sends keyboard input as a sequence of key events.
type VncTypeCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	Text     string `arg:"" help:"Text to type"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncTypeCmd) Run() error {
	client, err := connectVNC(c.Box, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)
	if err := client.TypeText(c.Text); err != nil {
		return fmt.Errorf("typing text: %w", err)
	}
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(os.Stderr, "Typed %d characters\n", len(c.Text))
	return nil
}

// VncKeyCmd sends an individual key press/release event.
type VncKeyCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	KeyName  string `arg:"" help:"Key name (e.g., Return, Escape, Tab, F1-F12, Up, Down, Left, Right, Control_L, Shift_L, Alt_L, Super_L)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncKeyCmd) Run() error {
	keysym, ok := vncKeyMap[c.KeyName]
	if !ok {
		return fmt.Errorf("unknown key name %q (valid: %s)", c.KeyName, vncKeyNames())
	}

	client, err := connectVNC(c.Box, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	time.Sleep(100 * time.Millisecond)
	if err := client.KeyPress(keysym); err != nil {
		return fmt.Errorf("sending key %s: %w", c.KeyName, err)
	}
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(os.Stderr, "Pressed key %s\n", c.KeyName)
	return nil
}

// VncMouseCmd moves the mouse pointer without clicking.
type VncMouseCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	X        uint16 `arg:"" help:"X coordinate"`
	Y        uint16 `arg:"" help:"Y coordinate"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncMouseCmd) Run() error {
	client, err := connectVNC(c.Box, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.PointerMove(c.X, c.Y); err != nil {
		return fmt.Errorf("moving mouse to (%d, %d): %w", c.X, c.Y, err)
	}
	time.Sleep(50 * time.Millisecond)

	fmt.Fprintf(os.Stderr, "Moved mouse to (%d, %d)\n", c.X, c.Y)
	return nil
}

// VncStatusCmd checks VNC server reachability and reports display info.
type VncStatusCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncStatusCmd) Run() error {
	venue, err := resolveEvalVenue(c.Box, c.Instance)
	if err != nil {
		return err
	}
	// Resolve a host-reachable endpoint for the in-venue VNC port (an ssh -L
	// forward for VM/ssh venues) and probe it with a TCP dial — venue-agnostic.
	ep, err := resolveEvalEndpoint(venue, 5900)
	if err != nil {
		return fmt.Errorf("VNC server not reachable (port 5900): %w", err)
	}
	defer ep.Close()
	conn, derr := net.DialTimeout("tcp", ep.Addr, 3*time.Second)
	if derr != nil {
		return fmt.Errorf("VNC server not reachable at %s: %w", ep.Addr, derr)
	}
	_ = conn.Close()
	fmt.Printf("VNC:        ok (%s)\n", ep.Addr)
	fmt.Fprintf(os.Stderr, "VNC server is reachable\n")
	return nil
}

// VncPasswdCmd sets up VNC authentication for a deployment.
type VncPasswdCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	Generate bool   `long:"generate" help:"Generate random password and print to stdout"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncPasswdCmd) Run() error {
	venue, err := resolveEvalVenue(c.Box, c.Instance)
	if err != nil {
		return err
	}

	var password string
	if c.Generate {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generating random password: %w", err)
		}
		password = hex.EncodeToString(b)
		fmt.Println(password)
	} else {
		fmt.Fprint(os.Stderr, "VNC password: ")
		var pw string
		if _, err := fmt.Scanln(&pw); err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		if pw == "" {
			return fmt.Errorf("password cannot be empty")
		}
		password = pw
	}

	boxName := resolveBoxName(c.Box)
	configKey := boxName
	if c.Instance != "" {
		configKey = boxName + "-" + c.Instance
	}
	store := DefaultCredentialStore()
	if err := store.Set(CredServiceVNC, configKey, password); err != nil {
		return fmt.Errorf("saving VNC password: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Stored VNC password for '%s' in %s.\n", configKey, store.Name())
	fmt.Fprintf(os.Stderr, "To verify: charly config get vnc.password.%s\n", configKey)

	// Resolve $HOME on the venue to get absolute paths (wayvnc doesn't expand shell vars).
	homeOut, err := venueCapture(venue.Exec, "echo $HOME")
	if err != nil {
		return fmt.Errorf("resolving home directory on the target: %w", err)
	}
	configDir := strings.TrimSpace(string(homeOut)) + "/.config/wayvnc"

	if err := venueRunSilent(venue.Exec, "mkdir -p "+shellQuote(configDir)); err != nil {
		return fmt.Errorf("creating wayvnc config dir: %w", err)
	}

	if venueRunSilent(venue.Exec, fmt.Sprintf("test -f %s/tls.crt", configDir)) != nil {
		fmt.Fprintf(os.Stderr, "Generating TLS certificate...\n")
		if err := venueRun(venue.Exec, fmt.Sprintf("openssl req -x509 -newkey rsa:4096 -nodes -keyout %s/tls.key -out %s/tls.crt -days 3650 -subj '/CN=wayvnc' 2>/dev/null", configDir, configDir)); err != nil {
			return fmt.Errorf("generating TLS certificate: %w", err)
		}
	}

	if venueRunSilent(venue.Exec, fmt.Sprintf("test -f %s/rsa.key", configDir)) != nil {
		fmt.Fprintf(os.Stderr, "Generating RSA key...\n")
		if err := venueRun(venue.Exec, fmt.Sprintf("openssl genrsa -traditional -out %s/rsa.key 4096 2>/dev/null", configDir)); err != nil {
			return fmt.Errorf("generating RSA key: %w", err)
		}
	}

	configContent := fmt.Sprintf(`enable_auth=true
username=user
password=%s
private_key_file=%s/tls.key
certificate_file=%s/tls.crt
rsa_private_key_file=%s/rsa.key
`, password, configDir, configDir, configDir)

	if err := venueRunSilent(venue.Exec,
		"printf '%s' "+deployShellQuote(configContent)+" > "+shellQuote(configDir)+"/config"); err != nil {
		return fmt.Errorf("writing wayvnc config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Restarting wayvnc service...\n")
	restartCmd := &ServiceRestartCmd{
		Box:      c.Box,
		Service:  "wayvnc",
		Instance: c.Instance,
	}
	if err := restartCmd.Run(); err != nil {
		return fmt.Errorf("restarting wayvnc: %w", err)
	}

	fmt.Fprintf(os.Stderr, "VNC password set for %s\n", c.Box)
	return nil
}

// VncRfbCmd sends a raw RFB command.
type VncRfbCmd struct {
	Box      string `arg:"" help:"Box name (use . for local)"`
	Method   string `arg:"" help:"RFB method (key, pointer, cut-text, fbupdate-request)"`
	Params   string `arg:"" optional:"" help:"JSON params"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *VncRfbCmd) Run() error {
	client, err := connectVNC(c.Box, c.Instance)
	if err != nil {
		return err
	}
	defer client.Close()

	switch c.Method {
	case "key":
		var params struct {
			Key  uint32 `json:"key"`
			Down bool   `json:"down"`
		}
		if err := json.Unmarshal([]byte(c.Params), &params); err != nil {
			return fmt.Errorf("invalid JSON params: %w (expected: {\"key\": 65293, \"down\": true})", err)
		}
		return client.KeyEvent(params.Key, params.Down)

	case "pointer":
		var params struct {
			X      uint16 `json:"x"`
			Y      uint16 `json:"y"`
			Button uint8  `json:"button"`
		}
		if err := json.Unmarshal([]byte(c.Params), &params); err != nil {
			return fmt.Errorf("invalid JSON params: %w (expected: {\"x\": 100, \"y\": 200, \"button\": 1})", err)
		}
		return client.PointerEvent(params.Button, params.X, params.Y)

	case "cut-text":
		var params struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(c.Params), &params); err != nil {
			return fmt.Errorf("invalid JSON params: %w (expected: {\"text\": \"clipboard content\"})", err)
		}
		return client.ClientCutText(params.Text)

	case "fbupdate-request":
		fmt.Printf(`{"width":%d,"height":%d}`+"\n", client.Width(), client.Height())
		return nil

	default:
		return fmt.Errorf("unknown RFB method %q (valid: key, pointer, cut-text, fbupdate-request)", c.Method)
	}
}
