package main

import "testing"

// The apkeep-arg + install-script unit coverage (apkeepArgString / installScript) moved
// out of core with the Android app-install path in the adb → external-plugin dep-shed; it
// now lives in candy/plugin-adb. Core keeps only the resolved AndroidDevice handle, whose
// default-serial behaviour stays here.

func TestAndroidDevice_DefaultSerial(t *testing.T) {
	if got := (AndroidDevice{}).serial(); got != "emulator-5554" {
		t.Errorf("default serial = %q", got)
	}
}
