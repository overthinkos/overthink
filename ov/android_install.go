package main

// android_install.go — the SINGLE Android app-install path (R3).
//
// Both `ov eval adb install-app` / `ov eval adb install` (the eval probe
// verbs) AND `AndroidDeployTarget` (the `apk:` package format executor) call
// the helpers here. There is exactly ONE implementation of "download by
// package id via apkeep + install" and ONE of "push a committed APK +
// install" — no per-call-site re-implementation of the single/split/.xapk
// handling.
//
// An AndroidDevice abstracts WHERE the work runs:
//   - in-pod (Container set): apkeep + adb run inside the emulator pod via
//     `engine exec` — the apkeep download lands in the pod and the pod's own
//     adb installs onto its emulator. apkeep's google-play creds come from
//     the container env (secret_accepts).
//   - host/endpoint (Container == ""): apkeep + adb run on the host; adb
//     targets the device's adb server over the network (`adb -H host -P
//     port`). Requires android-tools (adb) + apkeep on the host. google-play
//     creds are passed via the exec env (resolved from the credential store).
//
// Committed-APK installs (InstallFromHostApk) always push via goadb against
// AdbAddr, so they're venue-agnostic (the in-pod device exposes AdbAddr via
// its published 5037; the endpoint device IS an adb server addr).

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	adb "github.com/zach-klippenstein/goadb"
)

// AndroidDevice is a resolved install target — enough to run apkeep + adb
// against a specific Android device, whether it's an in-pod emulator or a
// remote adb endpoint.
type AndroidDevice struct {
	Engine    string // container engine (podman|docker) — only for in-pod (Container != "")
	Container string // emulator pod container name; "" => host/endpoint venue
	AdbAddr   string // "host:port" of the device's adb server (for goadb + host `adb -H -P`)
	Serial    string // adb serial (default emulator-5554)

	// GoogleEmail / GoogleToken are the apkeep google-play credentials,
	// resolved from the credential store. Used ONLY on the host venue (the
	// in-pod venue reads them from the container env). Empty when unset; the
	// google-play path errors clearly if it needs them and they're absent.
	GoogleEmail string
	GoogleToken string
}

func (d AndroidDevice) serial() string {
	if d.Serial != "" {
		return d.Serial
	}
	return "emulator-5554"
}

// adbScriptPrefix returns the adb invocation the install script uses. In-pod
// it's the baked platform-tools adb against the local emulator; on the host
// it's the system adb pointed at the device's adb server over the network.
func (d AndroidDevice) adbScriptPrefix() (string, error) {
	if d.Container != "" {
		return "/opt/android-sdk/platform-tools/adb -s " + d.serial(), nil
	}
	host, port, err := splitAdbAddr(d.AdbAddr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("adb -H %s -P %d -s %s", host, port, d.serial()), nil
}

// apkeepArgString renders the apkeep command (without the trailing output
// dir) for one package spec. Shared by the in-pod and host scripts so the
// per-source flag logic lives in ONE place. The caller appends the output
// dir (`"$TMP"`). google-play reads $GOOGLE_ACCOUNT_EMAIL/$GOOGLE_AAS_TOKEN
// from the env (container env in-pod; exec env on the host).
func apkeepArgString(spec ApkPackageSpec) string {
	appArg := spec.Package
	if spec.AppVersion != "" {
		appArg = spec.Package + "@" + spec.AppVersion
	}
	switch spec.EffectiveSource() {
	case "google-play":
		return `if [ -z "${GOOGLE_ACCOUNT_EMAIL:-}" ] || [ -z "${GOOGLE_AAS_TOKEN:-}" ]; then ` +
			`echo "apk source google-play requires GOOGLE_ACCOUNT_EMAIL + GOOGLE_AAS_TOKEN (set via ov secrets / kind:android google_account)" >&2; exit 1; fi; ` +
			`apkeep -a ` + shellSingleQuote(appArg) + ` -d google-play -e "$GOOGLE_ACCOUNT_EMAIL" -t "$GOOGLE_AAS_TOKEN" -o split_apk=1 "$TMP"`
	case "f-droid", "huawei-app-gallery":
		return `apkeep -a ` + shellSingleQuote(appArg) + ` -d ` + shellSingleQuote(spec.EffectiveSource()) + ` "$TMP"`
	default: // apk-pure
		s := `apkeep -a ` + shellSingleQuote(appArg) + ` -d apk-pure`
		if spec.EffectiveArch() != "" {
			s += ` -o arch=` + shellSingleQuote(spec.EffectiveArch())
		}
		return s + ` "$TMP"`
	}
}

// installScript builds the ONE bash script that downloads via apkeep and
// installs the result (single .apk / split set / .xapk) onto the device.
// adbPrefix selects the venue's adb (in-pod baked adb or host `adb -H -P`).
func installScript(spec ApkPackageSpec, adbPrefix string) string {
	return `set -euo pipefail
export PATH="/opt/android-sdk/platform-tools:$PATH"
ADB="` + adbPrefix + `"
TMP="$(mktemp -d /tmp/ov-apk-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
` + apkeepArgString(spec) + `
shopt -s nullglob
xapks=("$TMP"/*.xapk)
apks=("$TMP"/*.apk)
if [ ${#xapks[@]} -gt 0 ]; then
  mkdir -p "$TMP/x"; unzip -o -q "${xapks[0]}" -d "$TMP/x"
  $ADB install-multiple -r "$TMP"/x/*.apk
elif [ ${#apks[@]} -gt 1 ]; then
  $ADB install-multiple -r "${apks[@]}"
elif [ ${#apks[@]} -eq 1 ]; then
  $ADB install -r "${apks[0]}"
else
  echo "apk install: apkeep produced no .apk/.xapk in $TMP" >&2; ls -la "$TMP" >&2 || true; exit 1
fi`
}

// InstallByPackage downloads an app by package id via apkeep and installs it
// onto the device. In-pod (Container set) runs the script via `engine exec`;
// host/endpoint runs it via local bash with host apkeep + `adb -H -P`.
// Asserts "Success" in the output (apkeep + adb install both succeeded).
func (d AndroidDevice) InstallByPackage(spec ApkPackageSpec) (string, error) {
	adbPrefix, err := d.adbScriptPrefix()
	if err != nil {
		return "", err
	}
	script := installScript(spec, adbPrefix)

	var cmd *exec.Cmd
	if d.Container != "" {
		cmd = exec.Command(d.Engine, "exec", d.Container, "bash", "-c", script)
	} else {
		cmd = exec.Command("bash", "-c", script)
		cmd.Env = os.Environ()
		if d.GoogleEmail != "" {
			cmd.Env = append(cmd.Env, "GOOGLE_ACCOUNT_EMAIL="+d.GoogleEmail)
		}
		if d.GoogleToken != "" {
			cmd.Env = append(cmd.Env, "GOOGLE_AAS_TOKEN="+d.GoogleToken)
		}
	}
	out, runErr := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if runErr != nil {
		return trimmed, fmt.Errorf("install %s (source %s): %v: %s", spec.Package, spec.EffectiveSource(), runErr, trimmed)
	}
	if !strings.Contains(trimmed, "Success") {
		return trimmed, fmt.Errorf("install %s did not return Success: %s", spec.Package, trimmed)
	}
	return trimmed, nil
}

// InstallFromHostApk pushes a committed local APK to the device via the goadb
// sync protocol and runs `pm install -r`. Venue-agnostic — goadb talks to the
// adb server at AdbAddr (an in-pod device's published 5037 or a remote
// endpoint). Asserts "Success".
func (d AndroidDevice) InstallFromHostApk(path string) (string, error) {
	dev, err := adbDeviceForAddr(d.AdbAddr, d.serial())
	if err != nil {
		return "", err
	}
	apkBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read APK %s: %w", path, err)
	}
	if len(apkBytes) == 0 {
		return "", fmt.Errorf("APK %s is empty", path)
	}
	remote := fmt.Sprintf("/data/local/tmp/ov-apk-%d.apk", time.Now().UnixNano())
	writer, err := dev.OpenWrite(remote, 0644, time.Now())
	if err != nil {
		return "", fmt.Errorf("adb push %s → %s: %w", path, remote, err)
	}
	if _, err := writer.Write(apkBytes); err != nil {
		writer.Close()
		return "", fmt.Errorf("write APK to device: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close push stream: %w", err)
	}
	out, err := dev.RunCommand("pm", "install", "-r", remote)
	if err != nil {
		return "", fmt.Errorf("pm install: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	_, _ = dev.RunCommand("rm", "-f", remote)
	if !strings.Contains(trimmed, "Success") {
		return trimmed, fmt.Errorf("pm install did not return Success: %s", trimmed)
	}
	return trimmed, nil
}

// Uninstall removes a package by id (idempotent — "not installed" is not an
// error). Used by `ov eval adb uninstall` and runAndroidDel.
func (d AndroidDevice) Uninstall(pkg string) (string, error) {
	dev, err := adbDeviceForAddr(d.AdbAddr, d.serial())
	if err != nil {
		return "", err
	}
	out, err := dev.RunCommand("pm", "uninstall", pkg)
	if err != nil {
		return "", fmt.Errorf("pm uninstall %s: %w", pkg, err)
	}
	return strings.TrimSpace(out), nil
}

// splitAdbAddr parses a "host:port" adb-server address into its parts.
func splitAdbAddr(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, fmt.Errorf("empty adb address")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid adb address %q (want host:port): %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return "", 0, fmt.Errorf("invalid adb port in %q", addr)
	}
	return host, port, nil
}

// adbDeviceForAddr returns a goadb Device handle for an adb server reachable
// at "host:port" (the in-pod published 5037, or a remote adb endpoint).
func adbDeviceForAddr(addr, serial string) (*adb.Device, error) {
	host, port, err := splitAdbAddr(addr)
	if err != nil {
		return nil, err
	}
	client, err := adb.NewWithConfig(adb.ServerConfig{
		// PathToAdb only matters if goadb has to spawn a local server; the
		// server is already running at host:port, so any existing binary
		// suffices. /bin/true is the smallest portable stand-in.
		PathToAdb: "/bin/true",
		Host:      host,
		Port:      port,
	})
	if err != nil {
		return nil, fmt.Errorf("adb client init (%s): %w", addr, err)
	}
	if serial == "" {
		serial = "emulator-5554"
	}
	return client.Device(adb.DeviceWithSerial(serial)), nil
}
