package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"time"
)

// MoonCmd implements the GameStream client protocol for Moonlight/Sunshine.
type MoonCmd struct {
	Status MoonStatusCmd `cmd:"" help:"Show Sunshine server info (GPU, state, running app)"`
	Pair   MoonPairCmd   `cmd:"" help:"Pair with Sunshine as a Moonlight client"`
	Unpair MoonUnpairCmd `cmd:"" help:"Remove this client's pairing"`
	Apps   MoonAppsCmd   `cmd:"" help:"List available GameStream applications"`
	Launch MoonLaunchCmd `cmd:"" help:"Launch a GameStream application"`
	Quit   MoonQuitCmd   `cmd:"" help:"Quit the running application on server"`
}

// MoonStatusCmd queries /serverinfo (no pairing required).
type MoonStatusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonStatusCmd) Run() error {
	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}
	info, err := client.ServerInfo()
	if err != nil {
		return fmt.Errorf("server not reachable: %w", err)
	}

	fmt.Printf("Hostname:  %s\n", info.Hostname)
	if info.GPUType != "" {
		fmt.Printf("GPU:       %s\n", info.GPUType)
	}
	if info.AppVersion != "" {
		fmt.Printf("Version:   %s\n", info.AppVersion)
	}

	paired := "no"
	if info.PairStatus == "1" {
		paired = "yes"
	}
	fmt.Printf("Paired:    %s\n", paired)

	if info.CurrentGame != 0 {
		fmt.Printf("Running:   app %d\n", info.CurrentGame)
	} else {
		fmt.Printf("Running:   (none)\n")
	}

	state := "free"
	if strings.Contains(info.State, "_SERVER_BUSY") {
		state = "busy (streaming)"
	}
	fmt.Printf("State:     %s\n", state)

	return nil
}

// MoonPairCmd performs client-side pairing.
type MoonPairCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Pin      string `arg:"" optional:"" help:"4-digit PIN (auto-generated if omitted)"`
	Auto     bool   `long:"auto" help:"Automatically submit PIN to Sunshine (no manual step needed)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonPairCmd) Run() error {
	pin := c.Pin
	if pin == "" {
		// Generate random 4-digit PIN.
		b := make([]byte, 2)
		rand.Read(b)
		pinNum := (int(b[0])<<8 | int(b[1])) % 10000
		pin = fmt.Sprintf("%04d", pinNum)
	}
	if len(pin) != 4 {
		return fmt.Errorf("PIN must be exactly 4 digits, got %q", pin)
	}

	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if c.Auto {
		// Auto mode: submit PIN server-side in a goroutine.
		// Phase 1 (getservercert) blocks until the PIN is submitted, so we
		// start a background goroutine that submits the PIN after a brief delay
		// to let the getservercert request register with Sunshine first.
		fmt.Fprintf(os.Stderr, "Pairing with PIN %s (auto mode)...\n", pin)
		pinErr := make(chan error, 1)
		go func() {
			time.Sleep(2 * time.Second)
			sunClient, err := connectSunshine(c.Image, c.Instance)
			if err != nil {
				pinErr <- fmt.Errorf("connecting to Sunshine API for PIN submission: %w", err)
				return
			}
			pinErr <- sunClient.SubmitPIN(pin, "ov")
		}()

		if err := client.Pair(pin); err != nil {
			return err
		}

		// Check if PIN submission had an error (non-blocking — pair succeeded so this is informational).
		select {
		case err := <-pinErr:
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: PIN submission returned error: %v\n", err)
			}
		default:
		}
	} else {
		// Manual mode: print PIN and wait for user to submit it.
		fmt.Println(pin)
		fmt.Fprintf(os.Stderr, "Enter this PIN in Sunshine Web UI, or run:\n")
		fmt.Fprintf(os.Stderr, "  ov sun pair %s %s\n\n", c.Image, pin)
		fmt.Fprintf(os.Stderr, "Waiting for pairing handshake...\n")

		if err := client.Pair(pin); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Pairing successful\n")
	return nil
}

// MoonUnpairCmd removes this client's pairing.
type MoonUnpairCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonUnpairCmd) Run() error {
	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := client.Unpair(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Unpaired from %s\n", c.Image)
	return nil
}

// MoonAppsCmd lists available GameStream applications.
type MoonAppsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonAppsCmd) Run() error {
	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !client.IsPaired() {
		return fmt.Errorf("not paired with %s (run 'ov moon pair %s' first)", c.Image, c.Image)
	}
	apps, err := client.AppList()
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "No GameStream apps configured")
		return nil
	}
	for _, app := range apps {
		fmt.Printf("%d\t%s\n", app.ID, app.Name)
	}
	return nil
}

// MoonLaunchCmd launches a GameStream application.
type MoonLaunchCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	App      string `arg:"" help:"App name or ID"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonLaunchCmd) Run() error {
	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !client.IsPaired() {
		return fmt.Errorf("not paired with %s (run 'ov moon pair %s' first)", c.Image, c.Image)
	}

	apps, err := client.AppList()
	if err != nil {
		return err
	}

	// Find app by name (case-insensitive) or ID.
	appID := -1
	for _, app := range apps {
		if strings.EqualFold(app.Name, c.App) || fmt.Sprintf("%d", app.ID) == c.App {
			appID = app.ID
			break
		}
	}
	if appID < 0 {
		names := make([]string, len(apps))
		for i, app := range apps {
			names[i] = app.Name
		}
		return fmt.Errorf("app %q not found (available: %s)", c.App, strings.Join(names, ", "))
	}

	if err := client.Launch(appID); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Launched %q\n", c.App)
	return nil
}

// MoonQuitCmd quits the running application on the server.
type MoonQuitCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *MoonQuitCmd) Run() error {
	client, err := newGameStreamClient(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if !client.IsPaired() {
		return fmt.Errorf("not paired with %s (run 'ov moon pair %s' first)", c.Image, c.Image)
	}
	if err := client.Quit(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Quit running app\n")
	return nil
}
