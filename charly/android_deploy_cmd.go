package main

// android_deploy_cmd.go — shared kind:android helpers (device + apk
// resolution) used by AndroidUnifiedTarget.Add / .Del (unified_targets_apk.go).
//
// A `target: android` deploy installs its layers' `apk:` packages onto a
// kind:android DEVICE (an in-pod emulator or a remote adb endpoint). The
// apps ride in on the deploy's add_layer: set — the SAME overlay mechanism
// the local/vm targets use — so the android Add receives the already-compiled
// plans and hands their ApkInstallStep entries to AndroidDeployTarget.
//
// The device must already be reachable (the emulator pod started, or the
// endpoint's adb server up). The android Add gates on sys.boot_completed
// before installing — never a fixed sleep.

import (
	"fmt"
	"strings"
	"time"
)

// androidBootDeadline bounds the readiness gate for a freshly-started
// emulator (cold boot of an API-36 Play-Store image is the worst case).
const androidBootDeadline = 5 * time.Minute

// findAndroidSpec resolves a kind:android device by name from the unified
// config (sibling of findK8sSpec).
func findAndroidSpec(dir, name string) *AndroidSpec {
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf == nil || uf.Android == nil {
		return nil
	}
	return uf.Android[name]
}

// androidApkPackageIDs re-resolves the deploy's add_layer: layers and
// collects every apk: package id (committed-APK entries have no id and are
// skipped — they can't be uninstalled by id). Best-effort; returns nil on
// resolution failure.
func androidApkPackageIDs(node *DeploymentNode, dir string) []string {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil
	}
	layers, err := ScanAllLayerWithConfig(dir, cfg)
	if err != nil {
		return nil
	}
	var ids []string
	for _, ref := range node.AddLayer {
		lref, err := ResolveDeployRefAsLayer(ref, dir)
		if err != nil {
			continue
		}
		order, err := ResolveLayerOrder([]string{lref.Name}, layers, nil)
		if err != nil {
			continue
		}
		for _, name := range order {
			l := layers[name]
			if l == nil {
				continue
			}
			for _, a := range l.Apk() {
				if a.Package != "" {
					ids = append(ids, a.Package)
				}
			}
		}
	}
	return ids
}

// resolveAndroidDevice builds the AndroidDevice install handle from the spec
// and deploy context. Endpoint devices target a remote adb server (apkeep on
// the host); image devices target an in-pod emulator (apkeep in-pod). For a
// nested deploy (dotted path), the in-pod container is the PARENT pod
// (charly-<flat-parent-path>); for a top-level deploy it resolves by image name.
func resolveAndroidDevice(spec *AndroidSpec, node *DeploymentNode, path string) (AndroidDevice, error) {
	serial := spec.EffectiveSerial()

	// Remote/physical endpoint — host-side apkeep + goadb.
	if spec.IsEndpoint() {
		email, token := resolveAndroidGoogleCreds(spec.GoogleAccount)
		// A nested endpoint device may address its parent emulator pod's
		// published adb port dynamically via ${HOST_PORT:N} instead of a
		// hard-coded host port (decouples the device from the bed's fixed
		// publish). Resolve it against the parent pod's NetworkSettings.Ports.
		addr, err := resolveAndroidHostPortRef(spec.Adb.Host, path, node)
		if err != nil {
			return AndroidDevice{}, err
		}
		return AndroidDevice{
			AdbAddr:     addr,
			Serial:      serial,
			GoogleEmail: email,
			GoogleToken: token,
		}, nil
	}

	if spec.Box == "" {
		return AndroidDevice{}, fmt.Errorf("kind:android device has neither image: nor adb:")
	}

	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	var container string
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		// Nested under a pod — the emulator runs in the PARENT pod container.
		parent := path[:i]
		container = "charly-" + NestedContainerName(parent)
		engine = EngineBinary(engine)
		if !containerRunning(engine, container) {
			return AndroidDevice{}, fmt.Errorf("parent pod container %s is not running (start it before deploying the android device)", container)
		}
	} else {
		// Top-level — resolve the running container by the device's image.
		eng, name, err := resolveContainer(spec.Box, "")
		if err != nil {
			return AndroidDevice{}, err
		}
		engine, container = eng, name
	}

	addr, err := adbAddrForContainer(engine, container)
	if err != nil {
		return AndroidDevice{}, err
	}
	return AndroidDevice{Engine: engine, Container: container, AdbAddr: addr, Serial: serial}, nil
}

// resolveAndroidHostPortRef substitutes a single ${HOST_PORT:N} token in a
// nested endpoint device's adb host with the PARENT pod's host-mapped port for
// container port N — read from NetworkSettings.Ports via the same findHostPort
// the image-device branch uses (R3). The parent pod is derived from the deploy
// path (path[:lastDot]), exactly like the image branch. Returns addr unchanged
// when it carries no ${HOST_PORT:N} reference (a literal host:port endpoint).
func resolveAndroidHostPortRef(addr, path string, node *DeploymentNode) (string, error) {
	const marker = "${HOST_PORT:"
	idx := strings.Index(addr, marker)
	if idx < 0 {
		return addr, nil
	}
	rest := addr[idx+len(marker):]
	end := strings.IndexByte(rest, '}')
	if end < 0 {
		return "", fmt.Errorf("adb host %q: malformed ${HOST_PORT:N} (no closing brace)", addr)
	}
	var ctrPort int
	if _, err := fmt.Sscanf(rest[:end], "%d", &ctrPort); err != nil || ctrPort <= 0 {
		return "", fmt.Errorf("adb host %q: ${HOST_PORT:N} requires a positive container port", addr)
	}
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return "", fmt.Errorf("adb host %q uses ${HOST_PORT:%d} but the device is not nested under a pod (deploy path %q has no parent to read the published port from)", addr, ctrPort, path)
	}
	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	engine = EngineBinary(engine)
	container := "charly-" + NestedContainerName(path[:i])
	if !containerRunning(engine, container) {
		return "", fmt.Errorf("parent pod container %s is not running (start it before deploying the android endpoint device)", container)
	}
	insp, err := InspectContainer(engine, container)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", container, err)
	}
	hp, err := findHostPort(insp, ctrPort)
	if err != nil {
		return "", err
	}
	return addr[:idx] + fmt.Sprintf("%d", hp) + rest[end+1:], nil
}

// resolveAndroidGoogleCreds reads the apkeep google-play credentials from the
// credential store using the device's google_account secret-key refs (or the
// GOOGLE_ACCOUNT_EMAIL / GOOGLE_AAS_TOKEN defaults). Empty when unset — the
// google-play path errors clearly if it needs them.
func resolveAndroidGoogleCreds(ga *AndroidGoogleAccount) (email, token string) {
	emailKey, tokenKey := "GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN"
	if ga != nil {
		if ga.EmailSecret != "" {
			emailKey = ga.EmailSecret
		}
		if ga.TokenSecret != "" {
			tokenKey = ga.TokenSecret
		}
	}
	store := DefaultCredentialStore()
	email, _ = store.Get("charly/secret", emailKey)
	token, _ = store.Get("charly/secret", tokenKey)
	return email, token
}

// waitAndroidReady polls sys.boot_completed on the device until it returns
// "1" or the deadline elapses. The check IS the readiness condition — never a
// fixed sleep.
func waitAndroidReady(dev AndroidDevice, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if d, err := adbDeviceForAddr(dev.AdbAddr, dev.Serial); err == nil {
			if out, err := d.RunCommand("getprop", "sys.boot_completed"); err == nil && strings.TrimSpace(out) == "1" {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("android device (%s): sys.boot_completed != 1 after %s", dev.AdbAddr, timeout)
}
