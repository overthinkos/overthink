package main

import (
	"strings"
	"testing"
)

func TestApkeepArgString_Sources(t *testing.T) {
	cases := []struct {
		name     string
		spec     ApkPackageSpec
		contains []string
		absent   []string
	}{
		{
			"apk-pure-default",
			ApkPackageSpec{Package: "org.fdroid.fdroid"},
			[]string{"apkeep -a 'org.fdroid.fdroid'", "-d apk-pure", "-o arch='x86_64'", `"$TMP"`},
			[]string{"google-play", "GOOGLE_ACCOUNT_EMAIL"},
		},
		{
			"apk-pure-version-arch",
			ApkPackageSpec{Package: "com.example", Source: "apk-pure", Arch: "arm64-v8a", AppVersion: "1.2.3"},
			[]string{"apkeep -a 'com.example@1.2.3'", "-d apk-pure", "-o arch='arm64-v8a'"},
			nil,
		},
		{
			"google-play-creds",
			ApkPackageSpec{Package: "com.example", Source: "google-play"},
			[]string{"GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN", "-d google-play", "split_apk=1"},
			[]string{"-d apk-pure"},
		},
		{
			"f-droid",
			ApkPackageSpec{Package: "org.fdroid.fdroid", Source: "f-droid"},
			[]string{"-d 'f-droid'"},
			[]string{"-o arch="},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := apkeepArgString(tc.spec)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("apkeepArgString(%+v) missing %q in:\n%s", tc.spec, want, got)
				}
			}
			for _, no := range tc.absent {
				if strings.Contains(got, no) {
					t.Errorf("apkeepArgString(%+v) should NOT contain %q in:\n%s", tc.spec, no, got)
				}
			}
		})
	}
}

// TestInstallScript_SharedAcrossVenues proves the SINGLE install script (R3):
// the same installScript body drives both the in-pod (engine exec) and the
// host/endpoint (adb -H -P) venues — only the adb prefix differs.
func TestInstallScript_SharedAcrossVenues(t *testing.T) {
	spec := ApkPackageSpec{Package: "org.fdroid.fdroid"}

	inpod := installScript(spec, "/opt/android-sdk/platform-tools/adb -s emulator-5554")
	host := installScript(spec, "adb -H 127.0.0.1 -P 35002 -s emulator-5554")

	// Both carry the identical apkeep download + locate/install logic.
	for _, common := range []string{
		"apkeep -a 'org.fdroid.fdroid'",
		`xapks=("$TMP"/*.xapk)`,
		`$ADB install-multiple -r`,
		`$ADB install -r`,
		"mktemp -d",
	} {
		if !strings.Contains(inpod, common) {
			t.Errorf("in-pod script missing shared piece %q", common)
		}
		if !strings.Contains(host, common) {
			t.Errorf("host script missing shared piece %q", common)
		}
	}
	// Only the ADB= line differs.
	if !strings.Contains(inpod, `ADB="/opt/android-sdk/platform-tools/adb -s emulator-5554"`) {
		t.Errorf("in-pod ADB prefix wrong:\n%s", inpod)
	}
	if !strings.Contains(host, `ADB="adb -H 127.0.0.1 -P 35002 -s emulator-5554"`) {
		t.Errorf("host ADB prefix wrong:\n%s", host)
	}
}

func TestAndroidDevice_DefaultSerial(t *testing.T) {
	if got := (AndroidDevice{}).serial(); got != "emulator-5554" {
		t.Errorf("default serial = %q", got)
	}
}
