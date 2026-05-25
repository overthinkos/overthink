package main

import (
	"reflect"
	"testing"

	adb "github.com/zach-klippenstein/goadb"
)

func TestAdbStateString(t *testing.T) {
	cases := []struct {
		in   adb.DeviceState
		want string
	}{
		{adb.StateOnline, "device"},
		{adb.StateOffline, "offline"},
		{adb.StateUnauthorized, "unauthorized"},
		{adb.StateDisconnected, "disconnected"},
	}
	for _, c := range cases {
		got := adbStateString(c.in)
		if got != c.want {
			t.Errorf("adbStateString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPosShellArgs_PrefixesDoubleDash(t *testing.T) {
	// Shell args must be prefixed with `--` so flag-like tokens (-l, -p,
	// --color) don't get claimed by Kong as flags of `ov eval adb shell`.
	c := &Check{Args: []string{"getprop", "ro.build.version.release"}}
	got := posShellArgs(c)
	want := []string{"--", "getprop", "ro.build.version.release"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posShellArgs = %v, want %v", got, want)
	}
}

func TestPosApkFlag(t *testing.T) {
	c := &Check{Apk: "/workspace/app.apk"}
	got := posApkFlag(c)
	want := []string{"--apk", "/workspace/app.apk"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posApkFlag = %v, want %v", got, want)
	}
}

func TestPosPropertyArg(t *testing.T) {
	c := &Check{Property: "sys.boot_completed"}
	got := posPropertyArg(c)
	want := []string{"sys.boot_completed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posPropertyArg = %v, want %v", got, want)
	}
}

func TestPosPackageArg(t *testing.T) {
	c := &Check{Args: []string{"com.example.app"}}
	got := posPackageArg(c)
	want := []string{"com.example.app"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posPackageArg = %v, want %v", got, want)
	}
	// Empty Args returns nil — caller (validator) catches that earlier.
	if got := posPackageArg(&Check{}); got != nil {
		t.Errorf("posPackageArg(empty) = %v, want nil", got)
	}
}

func TestPosArtifactFlag(t *testing.T) {
	c := &Check{Artifact: "/tmp/x.png"}
	got := posArtifactFlag(c)
	want := []string{"--artifact", "/tmp/x.png"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posArtifactFlag = %v, want %v", got, want)
	}
}

func TestPosLogcatTail(t *testing.T) {
	// All optional — empty Check → no args.
	if got := posLogcatTail(&Check{}); got != nil {
		t.Errorf("posLogcatTail(empty) = %v, want nil", got)
	}
	// Lines only.
	got := posLogcatTail(&Check{Amount: 100})
	want := []string{"--lines", "100"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posLogcatTail lines = %v, want %v", got, want)
	}
	// Both lines + filter.
	got = posLogcatTail(&Check{Amount: 50, Query: "MyApp:I *:S"})
	want = []string{"--lines", "50", "--filter", "MyApp:I *:S"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posLogcatTail both = %v, want %v", got, want)
	}
}

func TestPosWaitForDevice(t *testing.T) {
	if got := posWaitForDevice(&Check{}); got != nil {
		t.Errorf("posWaitForDevice(empty) = %v, want nil (default applied subprocess-side)", got)
	}
	got := posWaitForDevice(&Check{Timeout: "120s"})
	want := []string{"--timeout", "120s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("posWaitForDevice = %v, want %v", got, want)
	}
}

func TestAdbMethods_AllowlistShape(t *testing.T) {
	// Every method in the allowlist must have a non-empty path[0] = "adb".
	for name, spec := range adbMethods {
		if len(spec.path) == 0 || spec.path[0] != "adb" {
			t.Errorf("adbMethods[%q]: path[0] = %v, want []string{\"adb\", ...}", name, spec.path)
		}
	}
	// The methods we promise must all be present.
	wantMethods := []string{
		"devices", "shell", "install", "install-app", "uninstall",
		"getprop", "screencap", "logcat-tail", "wait-for-device",
	}
	for _, name := range wantMethods {
		if _, ok := adbMethods[name]; !ok {
			t.Errorf("adbMethods missing method %q", name)
		}
	}
}

func TestPosInstallApp(t *testing.T) {
	cases := []struct {
		name string
		c    Check
		want []string
	}{
		{"package-only", Check{AppId: "org.fdroid.fdroid"}, []string{"--package", "org.fdroid.fdroid"}},
		{"with-source-arch", Check{AppId: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
			[]string{"--package", "org.fdroid.fdroid", "--source", "apk-pure", "--arch", "x86_64"}},
		{"google-play-version", Check{AppId: "com.example", Source: "google-play", AppVersion: "1.2.3"},
			[]string{"--package", "com.example", "--source", "google-play", "--app-version", "1.2.3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := posInstallApp(&tc.c)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("posInstallApp = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateOvVerb_InstallAppRequiresAppId(t *testing.T) {
	// Missing app_id ⇒ validation error.
	errs := &ValidationError{}
	validateOvVerb(&Check{Adb: "install-app"}, "adb", "loc", "deploy", errs)
	if !errs.HasErrors() {
		t.Error("install-app without app_id should fail validation")
	}
	// With app_id ⇒ no error.
	errs2 := &ValidationError{}
	validateOvVerb(&Check{Adb: "install-app", AppId: "org.fdroid.fdroid"}, "adb", "loc", "deploy", errs2)
	if errs2.HasErrors() {
		t.Errorf("install-app with app_id should pass, got: %v", errs2.Errors)
	}
}
