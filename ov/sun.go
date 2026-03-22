package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// SunCmd manages Sunshine game streaming in running containers.
type SunCmd struct {
	Status  SunStatusCmd  `cmd:"" help:"Check Sunshine health and show config summary"`
	Passwd  SunPasswdCmd  `cmd:"" help:"Set Sunshine Web UI credentials"`
	Pair    SunPairCmd    `cmd:"" help:"Submit Moonlight pairing PIN"`
	Clients SunClientsCmd `cmd:"" help:"List paired Moonlight clients"`
	Unpair  SunUnpairCmd  `cmd:"" help:"Unpair Moonlight clients"`
	Apps    SunAppsCmd    `cmd:"" help:"List GameStream applications"`
	Config  SunConfigCmd  `cmd:"" help:"Show current Sunshine configuration"`
	Set     SunSetCmd     `cmd:"" help:"Modify a Sunshine config value"`
	Restart SunRestartCmd `cmd:"" help:"Restart Sunshine service"`
	Url     SunUrlCmd     `cmd:"" help:"Print Sunshine Web UI URL"`
	Diag    SunDiagCmd    `cmd:"" help:"Run comprehensive streaming diagnostics"`
}

// SunStatusCmd checks Sunshine health: supervisord service + API config.
type SunStatusCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunStatusCmd) Run() error {
	// Phase 1: Check supervisord service status via ov code path.
	engine, name, err := resolveSunContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if engine != "" {
		fmt.Fprintf(os.Stderr, "Container: %s\n", name)
		execSupervisorctl(engine, name, "status", "sunshine")
	}

	// Phase 2: Try API for version and config info.
	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "API:       not reachable (%v)\n", err)
		return nil
	}

	config, err := client.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "API:       not reachable (%v)\n", err)
		return nil
	}

	if v, ok := config["version"]; ok {
		fmt.Printf("Version:   %v\n", v)
	}
	if v, ok := config["encoder"]; ok {
		fmt.Printf("Encoder:   %v\n", v)
	}
	if v, ok := config["capture"]; ok {
		fmt.Printf("Capture:   %v\n", v)
	}
	if v, ok := config["platform"]; ok {
		fmt.Printf("Platform:  %v\n", v)
	}

	fmt.Fprintf(os.Stderr, "Sunshine API is reachable\n")
	return nil
}

// SunPasswdCmd sets Sunshine Web UI credentials via the REST API.
type SunPasswdCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Generate bool   `long:"generate" help:"Generate random password and print to stdout"`
	User     string `long:"user" default:"sunshine" help:"Web UI username"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunPasswdCmd) Run() error {
	var password string
	if c.Generate {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generating random password: %w", err)
		}
		password = hex.EncodeToString(b)
		fmt.Println(password)
	} else {
		fmt.Fprint(os.Stderr, "Sunshine password: ")
		var pw string
		if _, err := fmt.Scanln(&pw); err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		if pw == "" {
			return fmt.Errorf("password cannot be empty")
		}
		password = pw
	}

	// Resolve API URL (no auth needed for passwd).
	baseURL, err := connectSunshineNoAuth(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Try with stored credentials first (password change), fall back to empty (first-time setup).
	imageName := resolveImageName(c.Image)
	currentUser, currentPass := "", ""
	if u, p, err := resolveSunCredentials(imageName, c.Instance); err == nil {
		currentUser, currentPass = u, p
	}

	client := NewSunshineClient(baseURL, currentUser, currentPass)
	if err := client.SetPassword(currentUser, currentPass, c.User, password); err != nil {
		return fmt.Errorf("setting Sunshine credentials: %w", err)
	}

	// Store credentials in ov config.
	cfg, err := LoadRuntimeConfig()
	if err != nil {
		return err
	}
	if cfg.SunshineUsers == nil {
		cfg.SunshineUsers = make(map[string]string)
	}
	if cfg.SunshinePasswords == nil {
		cfg.SunshinePasswords = make(map[string]string)
	}
	configKey := imageName
	if c.Instance != "" {
		configKey = imageName + "-" + c.Instance
	}
	cfg.SunshineUsers[configKey] = c.User
	cfg.SunshinePasswords[configKey] = password
	if err := SaveRuntimeConfig(cfg); err != nil {
		return fmt.Errorf("saving Sunshine credentials to config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Sunshine credentials set for %s (user: %s)\n", configKey, c.User)
	return nil
}

var pinRegex = regexp.MustCompile(`^\d{4}$`)

// SunPairCmd submits a Moonlight pairing PIN.
type SunPairCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Pin      string `arg:"" help:"4-digit PIN from Moonlight client"`
	Name     string `long:"name" help:"Friendly name for the paired client"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunPairCmd) Run() error {
	if !pinRegex.MatchString(c.Pin) {
		return fmt.Errorf("PIN must be exactly 4 digits, got %q", c.Pin)
	}

	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if err := client.SubmitPIN(c.Pin, c.Name); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Pairing successful\n")
	return nil
}

// SunClientsCmd lists paired Moonlight clients.
type SunClientsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunClientsCmd) Run() error {
	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	clients, err := client.GetClients()
	if err != nil {
		return err
	}

	if len(clients) == 0 {
		fmt.Fprintln(os.Stderr, "No paired clients")
		return nil
	}

	for _, cl := range clients {
		name := cl.Name
		if name == "" {
			name = "(unnamed)"
		}
		if cl.UUID != "" {
			fmt.Printf("%s\t%s\n", cl.UUID, name)
		} else {
			fmt.Printf("%s\n", name)
		}
	}
	return nil
}

// SunUnpairCmd unpairs Moonlight clients.
type SunUnpairCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	UUID     string `arg:"" optional:"" help:"Client UUID to unpair (from 'ov sun clients')"`
	All      bool   `long:"all" help:"Unpair all clients"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunUnpairCmd) Run() error {
	if !c.All && c.UUID == "" {
		return fmt.Errorf("specify a client UUID or use --all to unpair all clients")
	}

	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if c.All {
		if err := client.UnpairAll(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "All clients unpaired")
	} else {
		if err := client.UnpairClient(c.UUID); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Client %s unpaired\n", c.UUID)
	}
	return nil
}

// SunAppsCmd lists GameStream applications.
type SunAppsCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunAppsCmd) Run() error {
	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	apps, err := client.GetApps()
	if err != nil {
		return err
	}

	if len(apps) == 0 {
		fmt.Fprintln(os.Stderr, "No applications configured")
		return nil
	}

	for i, app := range apps {
		cmd := app.Cmd
		if cmd == "" {
			cmd = "(desktop)"
		}
		fmt.Printf("%d\t%s\t%s\n", i, app.Name, cmd)
	}
	return nil
}

// SunConfigCmd shows current Sunshine configuration.
type SunConfigCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Json     bool   `long:"json" help:"Output raw JSON"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunConfigCmd) Run() error {
	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	config, err := client.GetConfig()
	if err != nil {
		return err
	}

	if c.Json {
		data, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	// Print sorted key=value pairs.
	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		fmt.Printf("%s = %v\n", k, config[k])
	}
	return nil
}

// SunSetCmd modifies a Sunshine config value.
type SunSetCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Key      string `arg:"" help:"Config key (e.g., encoder, capture, min_log_level)"`
	Value    string `arg:"" help:"Config value"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunSetCmd) Run() error {
	client, err := connectSunshine(c.Image, c.Instance)
	if err != nil {
		return err
	}

	settings := map[string]string{c.Key: c.Value}
	if err := client.PostConfig(settings); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Set %s = %s (some changes may require 'ov sun restart')\n", c.Key, c.Value)
	return nil
}

// SunRestartCmd restarts the Sunshine supervisord service.
type SunRestartCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunRestartCmd) Run() error {
	return (&ServiceRestartCmd{
		Image:    c.Image,
		Service:  "sunshine",
		Instance: c.Instance,
	}).Run()
}

// SunUrlCmd prints the Sunshine Web UI URL.
type SunUrlCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunUrlCmd) Run() error {
	baseURL, err := connectSunshineNoAuth(c.Image, c.Instance)
	if err != nil {
		return err
	}
	fmt.Println(baseURL)
	return nil
}

// SunDiagCmd runs comprehensive streaming diagnostics.
type SunDiagCmd struct {
	Image    string `arg:"" help:"Image name (use . for local)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *SunDiagCmd) Run() error {
	engine, name, err := resolveSunContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	fmt.Println("Sunshine Diagnostics:")

	// 1. Service status
	if engine != "" {
		fmt.Print("  Service:     ")
		out, _ := exec.Command(engine, "exec", name, "supervisorctl", "status", "sunshine").Output()
		fmt.Println(strings.TrimSpace(string(out)))
	}

	// 2. API config (encoder, capture, version)
	client, apiErr := connectSunshine(c.Image, c.Instance)
	if apiErr != nil {
		fmt.Printf("  API:         not reachable (%v)\n", apiErr)
	} else {
		config, err := client.GetConfig()
		if err != nil {
			fmt.Printf("  API:         error (%v)\n", err)
		} else {
			fmt.Println("  API:         reachable")
			for _, key := range []string{"version", "encoder", "capture", "platform"} {
				if v, ok := config[key]; ok {
					fmt.Printf("  %-12s %v\n", strings.Title(key)+":", v)
				}
			}
		}
	}

	// 3. GPU access inside container
	fmt.Println("\nGPU Access (inside container):")
	if engine != "" {
		// DRI devices
		driOut, _ := exec.Command(engine, "exec", name, "sh", "-c",
			`for d in /dev/dri/card* /dev/dri/renderD*; do [ -e "$d" ] || continue; driver=""; link=$(readlink /sys/class/drm/$(basename $d)/device/driver 2>/dev/null) && driver=$(basename "$link"); access="accessible"; dd if="$d" of=/dev/null bs=1 count=0 2>/dev/null || access="NO ACCESS"; printf "  %-22s %-10s %s\n" "$d" "$driver" "$access"; done`).Output()
		if len(driOut) > 0 {
			fmt.Print(string(driOut))
		}

		// NVIDIA devices
		nvidiaOut, _ := exec.Command(engine, "exec", name, "sh", "-c",
			`for d in /dev/nvidia0 /dev/nvidiactl /dev/nvidia-modeset; do [ -e "$d" ] || continue; access="accessible"; dd if="$d" of=/dev/null bs=1 count=0 2>/dev/null || access="NO ACCESS"; printf "  %-22s %s\n" "$d" "$access"; done`).Output()
		if len(nvidiaOut) > 0 {
			fmt.Print(string(nvidiaOut))
		}

		// nvidia-smi
		smiOut, _ := exec.Command(engine, "exec", name, "sh", "-c",
			`nvidia-smi --query-gpu=name,driver_version --format=csv,noheader 2>/dev/null || echo "not available"`).Output()
		fmt.Printf("  nvidia-smi:  %s", strings.TrimSpace(string(smiOut)))
		fmt.Println()
	}

	// 4. Encoder detection from container logs
	fmt.Println("\nEncoder Detection (from startup logs):")
	if engine != "" {
		logsOut, _ := exec.Command(engine, "logs", name).CombinedOutput()
		logStr := string(logsOut)
		encoders := []string{"h264_nvenc", "hevc_nvenc", "av1_nvenc", "h264_vaapi", "hevc_vaapi", "av1_vaapi"}
		for _, enc := range encoders {
			if strings.Contains(logStr, "Found "+enc[0:4]+" encoder: "+enc) ||
				strings.Contains(logStr, "Found H.264 encoder: "+enc) ||
				strings.Contains(logStr, "Found HEVC encoder: "+enc) ||
				strings.Contains(logStr, "Found AV1 encoder: "+enc) {
				fmt.Printf("  %-14s found\n", enc+":")
			}
		}
		// Check for permission errors
		if strings.Contains(logStr, "Permission denied") {
			fmt.Println("\n  WARNING: Permission denied errors detected in logs")
			for _, line := range strings.Split(logStr, "\n") {
				if strings.Contains(line, "Permission denied") {
					// Extract just the relevant part
					if idx := strings.Index(line, "Error:"); idx >= 0 {
						fmt.Printf("  > %s\n", strings.TrimSpace(line[idx:]))
					}
				}
			}
		}
	}

	// 5. Paired clients
	if client != nil {
		clients, err := client.GetClients()
		if err == nil {
			fmt.Printf("\nPaired Clients: %d\n", len(clients))
		}
	}

	// 6. Container network
	if engine != "" {
		fmt.Println("\nContainer Network:")
		ipOut, _ := exec.Command(engine, "inspect", name, "--format",
			`{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}`).Output()
		ip := strings.TrimSpace(string(ipOut))
		if ip != "" {
			fmt.Printf("  IP: %s\n", ip)
		}
		portsOut, _ := exec.Command(engine, "port", name).Output()
		if len(portsOut) > 0 {
			for _, line := range strings.Split(strings.TrimSpace(string(portsOut)), "\n") {
				if line != "" {
					fmt.Printf("  %s\n", line)
				}
			}
		}
	}

	return nil
}
