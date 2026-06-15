package main

// android_spec.go — the `kind: android` schema (AndroidSpec) and the
// `apk:` candy-package schema (ApkPackageSpec).
//
// kind:android is a first-class deploy SUBSTRATE, modeled on kind:k8s: it
// describes an Android DEVICE (an in-pod emulator OR a remote/physical adb
// endpoint) onto which `apk` packages are installed by a `target: android`
// deploy. The apps themselves are NOT a kind — `apk` is a package format
// declared in candies like pac/aur/rpm/deb (see ApkPackageSpec + the
// candy manifest's `apk:` field), and an android deploy applies candies (their
// `apk:` packages) onto the device via AndroidDeployTarget.
//
// Build-vs-runtime split: the Android system image + API level are baked
// into the referenced kind:box at BUILD time (sdkmanager in the
// android-sdk candy). kind:android REFERENCES that box — it never drives
// a build. The api_level/device fields are informational documentation of
// the baked profile, not assertions or build drivers.

// AndroidSpec is the kind:android target template — an Android device.
// Exactly one device source must be set: Box (an in-pod emulator hosted
// by a kind:box) XOR Adb (a remote/physical adb endpoint).
type AndroidSpec struct {
	// Box names the kind:box that bakes the emulator (Android SDK +
	// system image + adb + apkeep). In-pod source: apkeep runs inside the
	// running pod and adb reaches the emulator via the pod. XOR Adb.
	Box string `yaml:"box,omitempty" json:"box,omitempty"`

	// Adb names a remote/physical adb endpoint (network host:port served by
	// an adb server). Endpoint source: apkeep runs on the host and the APK
	// is pushed via goadb to the endpoint. XOR Box.
	Adb *AndroidAdbEndpoint `yaml:"adb,omitempty" json:"adb,omitempty"`

	// Serial selects the device the adb server manages. Default
	// "emulator-5554" (the first emulator).
	Serial string `yaml:"serial,omitempty" json:"serial,omitempty"`

	// GoogleAccount names the credential-store secret keys feeding apkeep's
	// google-play source (an AAS token, NOT a password). Optional — only the
	// `source: google-play` apk path consults it.
	GoogleAccount *AndroidGoogleAccount `yaml:"google_account,omitempty" json:"google_account,omitempty"`

	// --- Informational (documents the referenced box's baked profile) ---
	// Neither asserted nor used to reconfigure a running emulator (the live
	// device profile lives in the box/candy env; overrides apply at the
	// next pod `charly update`). Present so a kind:android entity is
	// self-describing.
	Device   string `yaml:"device,omitempty" json:"device,omitempty"`       // e.g. "pixel_9a"
	ApiLevel int    `yaml:"api_level,omitempty" json:"api_level,omitempty"` // e.g. 36

	// --- Target-specific plan steps (parity with K8sSpec) ---
	Plan []Step `yaml:"plan,omitempty" json:"plan,omitempty"`
}

// AndroidAdbEndpoint addresses a remote/physical device's adb server.
type AndroidAdbEndpoint struct {
	// Host is the "host:port" of an adb server (the published 5037 of an
	// emulator pod, or a host running `adb connect`-ed to a network device).
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
}

// AndroidGoogleAccount selects the credential-store keys the apkeep
// google-play source reads (resolved from the secret store, never inlined).
type AndroidGoogleAccount struct {
	EmailSecret string `yaml:"email_secret,omitempty" json:"email_secret,omitempty"` // default GOOGLE_ACCOUNT_EMAIL
	TokenSecret string `yaml:"token_secret,omitempty" json:"token_secret,omitempty"` // default GOOGLE_AAS_TOKEN
}

// IsEndpoint reports whether the device is a remote/physical adb endpoint
// (apkeep runs on the host, APK pushed via goadb) rather than an in-pod
// emulator.
func (a *AndroidSpec) IsEndpoint() bool {
	return a != nil && a.Adb != nil && a.Adb.Host != ""
}

// EffectiveSerial returns the configured serial or the emulator default.
func (a *AndroidSpec) EffectiveSerial() string {
	if a != nil && a.Serial != "" {
		return a.Serial
	}
	return "emulator-5554"
}

// apkSourceAllowlist is the set of valid apkeep download sources. Mirrors
// the AdbInstallAppCmd --source enum; validated for candy `apk:` entries.
var apkSourceAllowlist = map[string]bool{
	"apk-pure":           true,
	"google-play":        true,
	"f-droid":            true,
	"huawei-app-gallery": true,
}

// ApkPackageSpec is one Android app install entry — the unit of the `apk:`
// package format declared in a candy. Exactly one of Package (download by id
// via apkeep) XOR Apk (a committed local APK path pushed via the adb sync
// protocol) must be set.
type ApkPackageSpec struct {
	// Package is the app package id apkeep downloads (e.g. org.fdroid.fdroid).
	// XOR Apk.
	Package string `yaml:"package,omitempty" json:"package,omitempty"`

	// Apk is a committed local APK path (relative to the candy dir or the
	// project root), pushed via the adb sync protocol. XOR Package.
	Apk string `yaml:"apk,omitempty" json:"apk,omitempty"`

	// Source is the apkeep download source for Package installs. Default
	// "apk-pure" (no credentials). Ignored for committed Apk installs.
	Source string `yaml:"source,omitempty" json:"source,omitempty"`

	// Arch is the native ABI apkeep requests (apk-pure only) — must match the
	// emulator's ABI. Default "x86_64".
	Arch string `yaml:"arch,omitempty" json:"arch,omitempty"`

	// AppVersion pins a specific app version (apkeep -a pkg@version). Empty =
	// latest.
	AppVersion string `yaml:"app_version,omitempty" json:"app_version,omitempty"`
}

// EffectiveSource returns the configured apkeep source or the default.
func (s ApkPackageSpec) EffectiveSource() string {
	if s.Source != "" {
		return s.Source
	}
	return "apk-pure"
}

// EffectiveArch returns the configured ABI or the x86_64 default.
func (s ApkPackageSpec) EffectiveArch() string {
	if s.Arch != "" {
		return s.Arch
	}
	return "x86_64"
}
