package main

// android_install.go — the resolved Android install TARGET (R3).
//
// Both `adb:` check steps (install / install-app / uninstall) AND
// `AndroidDeployTarget` (the `apk:` package format executor) drive Android app
// installs. The ONE implementation of "download by package id via apkeep +
// install" and "push a committed APK + install" lives OUT-OF-CORE in
// candy/plugin-adb (install.go) — together with the heavy goadb ADB-wire
// dependency. The core keeps only the resolved AndroidDevice handle and routes
// every device op through the adb plugin via invokeAdbPlugin (android_plugin.go).
//
// An AndroidDevice abstracts WHERE the work runs:
//   - in-pod (Container set): apkeep + adb run inside the emulator pod via
//     `engine exec` — apkeep's google-play creds come from the container env.
//   - host/endpoint (Container == ""): apkeep + adb run on the host; adb
//     targets the device's adb server over the network. google-play creds are
//     passed via the AdbDeviceEnv (resolved from the credential store).
//
// Committed-APK installs always push via the adb server at AdbAddr, so they're
// venue-agnostic (the in-pod device exposes AdbAddr via its published 5037; the
// endpoint device IS an adb server addr).

// AndroidDevice is a resolved install target — enough to drive apkeep + adb
// against a specific Android device, whether it's an in-pod emulator or a
// remote adb endpoint. It is the host-side handle; the goadb wire talk lives in
// candy/plugin-adb, reached via invokeAdbPlugin with this device's toEnv().
type AndroidDevice struct {
	Engine    string // container engine (podman|docker) — only for in-pod (Container != "")
	Container string // emulator pod container name; "" => host/endpoint venue
	AdbAddr   string // "host:port" of the device's adb server (resolved host-side via engine inspect)
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

// toEnv projects the resolved device into the AdbDeviceEnv the adb plugin decodes
// (android_plugin.go). It is the ONE mapping from the core's device handle to the
// plugin's deploy/status env (R3) — every device op (install / install-app /
// uninstall / wait-for-device) ships it.
func (d AndroidDevice) toEnv() AdbDeviceEnv {
	return AdbDeviceEnv{
		AdbAddr:     d.AdbAddr,
		Engine:      d.Engine,
		Container:   d.Container,
		Serial:      d.Serial,
		GoogleEmail: d.GoogleEmail,
		GoogleToken: d.GoogleToken,
	}
}

// InstallByPackage downloads an app by package id via apkeep and installs it onto
// the device, through the adb plugin (in-pod: apkeep + adb in the pod; host/endpoint:
// apkeep + `adb -H -P` on the host). Asserts "Success" plugin-side.
func (d AndroidDevice) InstallByPackage(spec ApkPackageSpec) (string, error) {
	return invokeAdbPlugin(&Op{
		Adb:        "install-app",
		AppId:      spec.Package,
		Source:     spec.Source,
		Arch:       spec.Arch,
		AppVersion: spec.AppVersion,
	}, d.toEnv())
}

// InstallFromHostApk pushes a committed local APK to the device (the goadb sync
// protocol + `pm install -r`, plugin-side) and asserts "Success". Venue-agnostic —
// the plugin talks to the adb server at AdbAddr.
func (d AndroidDevice) InstallFromHostApk(path string) (string, error) {
	return invokeAdbPlugin(&Op{Adb: "install", Apk: path}, d.toEnv())
}

// Uninstall removes a package by id (idempotent — "not installed" is not a hard
// error plugin-side). Used by AndroidUnifiedTarget.Del.
func (d AndroidDevice) Uninstall(pkg string) (string, error) {
	return invokeAdbPlugin(&Op{Adb: "uninstall", Args: []string{pkg}}, d.toEnv())
}
