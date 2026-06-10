package main

import (
	"strings"
	"testing"
)

func TestAndroidSpec_IsEndpoint(t *testing.T) {
	img := &AndroidSpec{Box: "android-emulator"}
	if img.IsEndpoint() {
		t.Error("image-source device should not be an endpoint")
	}
	ep := &AndroidSpec{Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}
	if !ep.IsEndpoint() {
		t.Error("adb-source device should be an endpoint")
	}
	// Adb present but empty host = not an endpoint.
	if (&AndroidSpec{Adb: &AndroidAdbEndpoint{}}).IsEndpoint() {
		t.Error("empty adb host should not count as an endpoint")
	}
}

func TestAndroidSpec_EffectiveSerial(t *testing.T) {
	if got := (&AndroidSpec{}).EffectiveSerial(); got != "emulator-5554" {
		t.Errorf("default serial = %q, want emulator-5554", got)
	}
	if got := (&AndroidSpec{Serial: "emulator-5556"}).EffectiveSerial(); got != "emulator-5556" {
		t.Errorf("serial override = %q, want emulator-5556", got)
	}
}

func TestApkPackageSpec_Defaults(t *testing.T) {
	s := ApkPackageSpec{Package: "org.fdroid.fdroid"}
	if s.EffectiveSource() != "apk-pure" {
		t.Errorf("default source = %q, want apk-pure", s.EffectiveSource())
	}
	if s.EffectiveArch() != "x86_64" {
		t.Errorf("default arch = %q, want x86_64", s.EffectiveArch())
	}
	s2 := ApkPackageSpec{Package: "x", Source: "f-droid", Arch: "arm64-v8a"}
	if s2.EffectiveSource() != "f-droid" || s2.EffectiveArch() != "arm64-v8a" {
		t.Errorf("overrides not honored: %+v", s2)
	}
}

func TestValidateLayerApk(t *testing.T) {
	cases := []struct {
		name    string
		apks    []ApkPackageSpec
		wantErr bool
	}{
		{"valid-package", []ApkPackageSpec{{Package: "org.fdroid.fdroid", Source: "apk-pure"}}, false},
		{"valid-committed", []ApkPackageSpec{{Apk: "tests/data/x.apk"}}, false},
		{"neither", []ApkPackageSpec{{Source: "apk-pure"}}, true},
		{"both", []ApkPackageSpec{{Package: "x", Apk: "y.apk"}}, true},
		{"bad-source", []ApkPackageSpec{{Package: "x", Source: "apk-mirror"}}, true},
		{"source-on-committed", []ApkPackageSpec{{Apk: "y.apk", Source: "apk-pure"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := &ValidationError{}
			validateLayerApk("test-layer", tc.apks, errs)
			if errs.HasErrors() != tc.wantErr {
				t.Errorf("validateLayerApk(%+v): HasErrors=%v want %v (%v)", tc.apks, errs.HasErrors(), tc.wantErr, errs.Errors)
			}
		})
	}
}

func TestSplitAdbAddr(t *testing.T) {
	host, port, err := splitAdbAddr("127.0.0.1:35002")
	if err != nil || host != "127.0.0.1" || port != 35002 {
		t.Errorf("splitAdbAddr = (%q,%d,%v), want (127.0.0.1,35002,nil)", host, port, err)
	}
	if _, _, err := splitAdbAddr(""); err == nil {
		t.Error("empty addr should error")
	}
	if _, _, err := splitAdbAddr("nohostport"); err == nil {
		t.Error("missing port should error")
	}
}

func TestAndroidDeviceVenue_AdbPrefix(t *testing.T) {
	// In-pod device uses the baked platform-tools adb against the local serial.
	inpod := AndroidDevice{Engine: "podman", Container: "charly-bed", Serial: "emulator-5554"}
	prefix, err := inpod.adbScriptPrefix()
	if err != nil {
		t.Fatalf("in-pod adbScriptPrefix err: %v", err)
	}
	if !strings.Contains(prefix, "/opt/android-sdk/platform-tools/adb") || !strings.Contains(prefix, "emulator-5554") {
		t.Errorf("in-pod adb prefix = %q", prefix)
	}
	// Endpoint device uses host adb pointed at the remote adb server.
	ep := AndroidDevice{AdbAddr: "127.0.0.1:35002", Serial: "emulator-5554"}
	prefix, err = ep.adbScriptPrefix()
	if err != nil {
		t.Fatalf("endpoint adbScriptPrefix err: %v", err)
	}
	if !strings.Contains(prefix, "adb -H 127.0.0.1 -P 35002") {
		t.Errorf("endpoint adb prefix = %q, want `adb -H 127.0.0.1 -P 35002 ...`", prefix)
	}
}
