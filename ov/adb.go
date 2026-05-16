package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	adb "github.com/zach-klippenstein/goadb"
)

// adb.go implements `ov eval adb …` — the host-side Android Debug Bridge
// client. The host `ov` binary connects to the running container's
// host-published ADB server port (container :5037 → host's HOST_PORT:5037,
// e.g. 35002 on android-emulator-pod) using github.com/zach-klippenstein/goadb,
// then issues ADB protocol operations against the emulator backing that
// server (typically `emulator-5554`).
//
// Same architecture pattern as `ov eval mcp …`: host-side protocol client,
// no container-side helper, works against any deploy that publishes the
// adb-server port — pod / vm / host / nested all transparently because the
// connection is plain TCP to `127.0.0.1:<host-port>` and the
// portforward/passt/etc layer handles the rest.
//
// Method allowlist + declarative dispatch live in evalrun_ov_verbs.go
// (adbMethods + runAdb).

// AdbCmd groups the 8 `ov eval adb …` leaves.
type AdbCmd struct {
	Devices       AdbDevicesCmd       `cmd:"" help:"List ADB devices/emulators with state"`
	Shell         AdbShellCmd         `cmd:"" help:"Run a shell command on the emulator and stream stdout"`
	Install       AdbInstallCmd       `cmd:"" help:"Install an APK from the host filesystem"`
	Uninstall     AdbUninstallCmd     `cmd:"" help:"Remove a package by id"`
	Getprop       AdbGetpropCmd       `cmd:"" help:"Read a system property (e.g. sys.boot_completed)"`
	Screencap     AdbScreencapCmd     `cmd:"" help:"Capture a screenshot to a host-side PNG file"`
	LogcatTail    AdbLogcatTailCmd    `cmd:"logcat-tail" help:"Dump recent logcat lines (logcat -d)"`
	WaitForDevice AdbWaitForDeviceCmd `cmd:"wait-for-device" help:"Block until the emulator is ready"`
}

// adbCommonFlags carries the deploy-addressing fields every leaf needs.
type adbCommonFlags struct {
	Instance string        `short:"i" long:"instance" help:"Instance name"`
	Serial   string        `long:"serial" default:"emulator-5554" help:"ADB serial (default is the first emulator)"`
	Timeout  time.Duration `long:"timeout" default:"30s" help:"Per-operation timeout (subset of host context.Context)"`
}

// adbDeviceFor resolves the host-mapped ADB port for the container and
// returns a goadb Device handle for the requested serial. Caller is
// responsible for not leaking the underlying ADB connection — goadb manages
// the socket per-call so there's nothing to Close().
//
// The host port is read from podman's NetworkSettings.Ports — same source
// of truth used by the eval test runner's HOST_PORT:N substitution.
func adbDeviceFor(image, instance, serial string) (*adb.Device, error) {
	engine, containerName, err := resolveContainer(image, instance)
	if err != nil {
		return nil, err
	}
	insp, err := InspectContainer(engine, containerName)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", containerName, err)
	}
	if insp == nil {
		return nil, fmt.Errorf("inspect %s: nil result", containerName)
	}
	port, err := findHostPort(insp, 5037)
	if err != nil {
		return nil, err
	}
	client, err := adb.NewWithConfig(adb.ServerConfig{
		// PathToAdb satisfies goadb's constructor PATH-check; it would
		// only be invoked if Dial() failed and goadb fell back to
		// spawning a local server. We never need that — the container's
		// adb-server is already running on Host:Port — so any existing
		// executable suffices. `/bin/true` is the smallest portable
		// stand-in (busybox / coreutils / FreeBSD all ship it).
		PathToAdb: "/bin/true",
		Host:      "127.0.0.1",
		Port:      port,
	})
	if err != nil {
		return nil, fmt.Errorf("adb client init (host port %d): %w", port, err)
	}
	if serial == "" {
		serial = "emulator-5554"
	}
	return client.Device(adb.DeviceWithSerial(serial)), nil
}

// findHostPort returns the first host-side port number bound to the given
// container port. Looks for both "5037" and "5037/tcp" keys because podman
// inspect emits the protocol-suffixed form.
func findHostPort(insp *ContainerInspection, containerPort int) (int, error) {
	// Host-networked containers expose the container port AS the host port.
	if insp.IsHostNetworked() {
		return containerPort, nil
	}
	keys := []string{
		fmt.Sprintf("%d/tcp", containerPort),
		fmt.Sprintf("%d", containerPort),
	}
	for _, k := range keys {
		binds, ok := insp.NetworkSettings.Ports[k]
		if !ok || len(binds) == 0 {
			continue
		}
		var port int
		if _, err := fmt.Sscanf(binds[0].HostPort, "%d", &port); err == nil && port > 0 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("container port %d not published on host (NetworkSettings.Ports has no binding); declare `ports: [%d]` on the image or publish via deploy.yml `port:`", containerPort, containerPort)
}

// ---------------------------------------------------------------------------
// adb devices
// ---------------------------------------------------------------------------

// AdbDevicesCmd: `ov eval adb devices <image>` — wraps `host:devices` and
// emits one line per device in `<serial>\t<state>` form (matches the
// `adb devices` CLI output without the header).
type AdbDevicesCmd struct {
	Image string `arg:"" help:"Image name (deploy address — instance via -i)"`
	adbCommonFlags
}

func (c *AdbDevicesCmd) Run() error {
	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	insp, err := InspectContainer(engine, containerName)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", containerName, err)
	}
	port, err := findHostPort(insp, 5037)
	if err != nil {
		return err
	}
	client, err := adb.NewWithConfig(adb.ServerConfig{
		// PathToAdb satisfies goadb's constructor PATH-check; it would
		// only be invoked if Dial() failed and goadb fell back to
		// spawning a local server. We never need that — the container's
		// adb-server is already running on Host:Port — so any existing
		// executable suffices. `/bin/true` is the smallest portable
		// stand-in (busybox / coreutils / FreeBSD all ship it).
		PathToAdb: "/bin/true",
		Host:      "127.0.0.1",
		Port:      port,
	})
	if err != nil {
		return fmt.Errorf("adb client init (host port %d): %w", port, err)
	}
	serials, err := client.ListDeviceSerials()
	if err != nil {
		return fmt.Errorf("adb host:devices: %w", err)
	}
	for _, s := range serials {
		dev := client.Device(adb.DeviceWithSerial(s))
		state, err := dev.State()
		if err != nil {
			fmt.Printf("%s\tunknown\n", s)
			continue
		}
		fmt.Printf("%s\t%s\n", s, adbStateString(state))
	}
	return nil
}

// adbStateString renders goadb's DeviceState enum into the canonical
// `adb devices` output strings (device / offline / unauthorized).
func adbStateString(s adb.DeviceState) string {
	switch s {
	case adb.StateOnline:
		return "device"
	case adb.StateOffline:
		return "offline"
	case adb.StateUnauthorized:
		return "unauthorized"
	case adb.StateDisconnected:
		return "disconnected"
	}
	return strings.ToLower(s.String())
}

// ---------------------------------------------------------------------------
// adb shell
// ---------------------------------------------------------------------------

// AdbShellCmd: `ov eval adb shell <image> -- <command…>` — runs a shell
// command on the device. The `--` delimiter is recommended in the runner's
// posShellArgs builder so flags like `-l` aren't claimed by Kong.
type AdbShellCmd struct {
	Image   string   `arg:"" help:"Image name"`
	Command []string `arg:"" passthrough:"" help:"Shell command + args"`
	adbCommonFlags
}

func (c *AdbShellCmd) Run() error {
	// Strip a leading `--` token if present — Kong's passthrough tag
	// captures the `--` separator as the first positional, but adb's
	// shell doesn't accept it as a flag terminator (the device-side
	// /system/bin/sh barfs with "--: unknown option"). The `--` is a
	// CLI hygiene marker (protect against Kong claiming `-l` / `-p`
	// etc. as flags) that has no semantic value adb-side.
	cmd := c.Command
	if len(cmd) > 0 && cmd[0] == "--" {
		cmd = cmd[1:]
	}
	if len(cmd) == 0 {
		return fmt.Errorf("adb shell: empty command (use `ov eval adb shell <image> -- <cmd> [args...]`)")
	}
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	out, err := dev.RunCommand(cmd[0], cmd[1:]...)
	if err != nil {
		return fmt.Errorf("adb shell %v: %w", cmd, err)
	}
	fmt.Print(out)
	return nil
}

// ---------------------------------------------------------------------------
// adb install
// ---------------------------------------------------------------------------

// AdbInstallCmd: `ov eval adb install <image> --apk <path>` — pushes the
// APK to the device and invokes `pm install`. Uses adb shell `pm install
// <remote>` after streaming the APK via the sync protocol.
type AdbInstallCmd struct {
	Image string `arg:"" help:"Image name"`
	Apk   string `long:"apk" required:"" help:"APK file path on host"`
	adbCommonFlags
}

func (c *AdbInstallCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	// Read APK from host filesystem.
	apkBytes, err := os.ReadFile(c.Apk)
	if err != nil {
		return fmt.Errorf("read APK %s: %w", c.Apk, err)
	}
	if len(apkBytes) == 0 {
		return fmt.Errorf("APK %s is empty", c.Apk)
	}
	// Push to /data/local/tmp/<basename> via the sync protocol.
	remote := fmt.Sprintf("/data/local/tmp/ov-install-%d.apk", time.Now().UnixNano())
	writer, err := dev.OpenWrite(remote, 0644, time.Now())
	if err != nil {
		return fmt.Errorf("adb push %s → %s: %w", c.Apk, remote, err)
	}
	if _, err := writer.Write(apkBytes); err != nil {
		writer.Close()
		return fmt.Errorf("write APK to device: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close push stream: %w", err)
	}
	// Install via pm.
	out, err := dev.RunCommand("pm", "install", "-r", remote)
	if err != nil {
		return fmt.Errorf("pm install: %w", err)
	}
	// pm install prints "Success" on success; anything else is failure.
	trimmed := strings.TrimSpace(out)
	fmt.Println(trimmed)
	// Best-effort cleanup of the staged APK.
	_, _ = dev.RunCommand("rm", "-f", remote)
	if !strings.Contains(trimmed, "Success") {
		return fmt.Errorf("pm install did not return Success: %s", trimmed)
	}
	return nil
}

// ---------------------------------------------------------------------------
// adb uninstall
// ---------------------------------------------------------------------------

// AdbUninstallCmd: `ov eval adb uninstall <image> <package>`.
type AdbUninstallCmd struct {
	Image   string `arg:"" help:"Image name"`
	Package string `arg:"" help:"Package id (e.g. com.example.android.apis)"`
	adbCommonFlags
}

func (c *AdbUninstallCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	out, err := dev.RunCommand("pm", "uninstall", c.Package)
	if err != nil {
		return fmt.Errorf("pm uninstall %s: %w", c.Package, err)
	}
	trimmed := strings.TrimSpace(out)
	fmt.Println(trimmed)
	if !strings.Contains(trimmed, "Success") {
		return fmt.Errorf("pm uninstall did not return Success: %s", trimmed)
	}
	return nil
}

// ---------------------------------------------------------------------------
// adb getprop
// ---------------------------------------------------------------------------

// AdbGetpropCmd: `ov eval adb getprop <image> <property>` — reads one
// system property and prints its value (trimmed). Use the bare
// `ov eval adb shell <image> -- getprop` for the full property dump.
type AdbGetpropCmd struct {
	Image    string `arg:"" help:"Image name"`
	Property string `arg:"" help:"Property key (e.g. sys.boot_completed, ro.build.version.release)"`
	adbCommonFlags
}

func (c *AdbGetpropCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	out, err := dev.RunCommand("getprop", c.Property)
	if err != nil {
		return fmt.Errorf("getprop %s: %w", c.Property, err)
	}
	fmt.Println(strings.TrimSpace(out))
	return nil
}

// ---------------------------------------------------------------------------
// adb screencap
// ---------------------------------------------------------------------------

// AdbScreencapCmd: `ov eval adb screencap <image> --artifact <png>` —
// captures a PNG via `screencap -p` and writes it to the host filesystem.
// The shell stream is base64-encoded round-trip safe through goadb's
// command interface (binary stdout would otherwise be mangled by line
// processing).
type AdbScreencapCmd struct {
	Image    string `arg:"" help:"Image name"`
	Artifact string `long:"artifact" required:"" help:"Output PNG path on host"`
	adbCommonFlags
}

func (c *AdbScreencapCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	// screencap -p writes PNG bytes to stdout. base64 to survive
	// goadb's shell stream which can mangle CR/LF in binary data on
	// some emulator builds; we decode host-side.
	out, err := dev.RunCommand("sh", "-c", "screencap -p | base64")
	if err != nil {
		return fmt.Errorf("screencap: %w", err)
	}
	// Strip whitespace (base64 with newlines is normal).
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, out)
	pngBytes, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return fmt.Errorf("decode base64 PNG: %w", err)
	}
	if err := os.WriteFile(c.Artifact, pngBytes, 0644); err != nil {
		return fmt.Errorf("write %s: %w", c.Artifact, err)
	}
	fmt.Printf("wrote %d bytes to %s\n", len(pngBytes), c.Artifact)
	return nil
}

// ---------------------------------------------------------------------------
// adb logcat-tail
// ---------------------------------------------------------------------------

// AdbLogcatTailCmd: `ov eval adb logcat-tail <image> [--lines N] [--filter TAG:LEVEL]`
// runs `logcat -d` (dump-and-exit) so the command always terminates.
// `--lines` limits to the last N lines; `--filter` is appended verbatim as
// the logcat filter spec (e.g. `MyApp:I *:S` to silence everything but
// MyApp). Empty filter = unfiltered.
type AdbLogcatTailCmd struct {
	Image  string `arg:"" help:"Image name"`
	Lines  int    `long:"lines" default:"50" help:"Last N lines (0 = all)"`
	Filter string `long:"filter" help:"logcat filter spec (e.g. \"MyApp:I *:S\")"`
	adbCommonFlags
}

func (c *AdbLogcatTailCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	args := []string{"-d"}
	if c.Filter != "" {
		// logcat filter spec uses positional words after flags.
		args = append(args, strings.Fields(c.Filter)...)
	}
	out, err := dev.RunCommand("logcat", args...)
	if err != nil {
		return fmt.Errorf("logcat: %w", err)
	}
	if c.Lines > 0 {
		lines := strings.Split(out, "\n")
		if len(lines) > c.Lines {
			lines = lines[len(lines)-c.Lines:]
		}
		out = strings.Join(lines, "\n")
	}
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
	return nil
}

// ---------------------------------------------------------------------------
// adb wait-for-device
// ---------------------------------------------------------------------------

// AdbWaitForDeviceCmd: `ov eval adb wait-for-device <image> [--timeout 60s]`
// — polls `getprop sys.boot_completed` until it returns "1" or the timeout
// expires. Exits 0 on ready, non-zero on timeout. Lighter than blocking on
// the wire-protocol `wait-for-device` because that command waits for the
// device to ATTACH (which an emulator does early in boot, well before
// sys.boot_completed is true).
type AdbWaitForDeviceCmd struct {
	Image string `arg:"" help:"Image name"`
	adbCommonFlags
}

func (c *AdbWaitForDeviceCmd) Run() error {
	dev, err := adbDeviceFor(c.Image, c.Instance, c.Serial)
	if err != nil {
		return err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := dev.RunCommand("getprop", "sys.boot_completed")
		if err == nil && strings.TrimSpace(out) == "1" {
			fmt.Println("ready")
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("wait-for-device: sys.boot_completed != 1 after %s", timeout)
}
