package main

import (
	"strings"
	"testing"
)

// TestParseCurrentFocus covers the dumpsys-window focus extraction used by
// `adb current-focus` and `adb wait-ui-settled`.
func TestParseCurrentFocus(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "normal app focus",
			in:   "WINDOW MANAGER WINDOWS\n  mCurrentFocus=Window{abc u0 io.appium.android.apis/io.appium.android.apis.view.TextFields}\n  mFocusedApp=ActivityRecord{def}",
			want: "mCurrentFocus=Window{abc u0 io.appium.android.apis/io.appium.android.apis.view.TextFields}",
		},
		{
			name: "ANR dialog focus",
			in:   "  mCurrentFocus=Window{123 u0 Application Not Responding: com.google.android.apps.nexuslauncher}",
			want: "mCurrentFocus=Window{123 u0 Application Not Responding: com.google.android.apps.nexuslauncher}",
		},
		{
			name: "no focus line (transient boot state)",
			in:   "WINDOW MANAGER WINDOWS\n  some other state\n  mFocusedApp=null",
			want: "",
		},
		{
			name: "empty input",
			in:   "",
			want: "",
		},
		{
			name: "first display wins on multi-display",
			in:   "  mCurrentFocus=Window{d1 u0 first}\n  mCurrentFocus=Window{d2 u0 second}",
			want: "mCurrentFocus=Window{d1 u0 first}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseCurrentFocus(tc.in); got != tc.want {
				t.Errorf("parseCurrentFocus()\n got=%q\nwant=%q", got, tc.want)
			}
		})
	}

	// The ANR-detection used by wait-ui-settled keys on this exact substring.
	anr := parseCurrentFocus("  mCurrentFocus=Window{x u0 Application Not Responding: pkg}")
	if !strings.Contains(anr, "Application Not Responding") {
		t.Errorf("ANR focus line should contain the ANR marker: %q", anr)
	}
}

// TestWrapContainerCommand verifies the generic stdin-heredoc guard: an
// in-container command script must be wrapped in a brace group with stdin from
// /dev/null so a stdin-consuming subcommand (adb shell / ssh / read) cannot eat
// the rest of the heredoc-delivered script.
func TestWrapContainerCommand(t *testing.T) {
	script := "adb shell dumpsys window > /tmp/f\necho done"
	got := wrapContainerCommand(script)
	want := "{ adb shell dumpsys window > /tmp/f\necho done\n} </dev/null"
	if got != want {
		t.Errorf("wrapContainerCommand()\n got=%q\nwant=%q", got, want)
	}
	if !strings.HasPrefix(got, "{ ") {
		t.Errorf("must open a brace group: %q", got)
	}
	if !strings.HasSuffix(got, "} </dev/null") {
		t.Errorf("must close the group with stdin from /dev/null: %q", got)
	}
	// The original script body must survive verbatim inside the group.
	if !strings.Contains(got, script) {
		t.Errorf("wrapped form dropped the original script body: %q", got)
	}
}
