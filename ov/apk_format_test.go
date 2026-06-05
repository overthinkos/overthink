package main

import (
	"fmt"
	"testing"
	"time"
)

// TestInstallWithRetry covers the PackageManager-init-race remedy: the install
// retries until it succeeds, and returns the last error if it never does.
func TestInstallWithRetry(t *testing.T) {
	// Succeeds on the 3rd attempt (PM settles).
	n := 0
	out, err := installWithRetry(2*time.Second, time.Millisecond, func() (string, error) {
		n++
		if n < 3 {
			return "", fmt.Errorf("Failed to parse APK file (attempt %d)", n)
		}
		return "Success", nil
	})
	if err != nil || out != "Success" {
		t.Fatalf("installWithRetry should succeed once op does: out=%q err=%v (attempts=%d)", out, err, n)
	}
	if n != 3 {
		t.Errorf("expected 3 attempts, got %d", n)
	}

	// Never succeeds → returns the last error after the deadline (not a panic/hang).
	m := 0
	_, err = installWithRetry(20*time.Millisecond, time.Millisecond, func() (string, error) {
		m++
		return "", fmt.Errorf("permanently broken")
	})
	if err == nil {
		t.Error("installWithRetry should return the last error when the op never succeeds")
	}
	if m < 2 {
		t.Errorf("expected multiple attempts before the deadline, got %d", m)
	}
}

// TestCompileApkStep verifies the layer `apk:` package format compiles into a
// single ApkInstallStep carrying every entry, and that an empty apk: list
// compiles to nothing.
func TestCompileApkStep(t *testing.T) {
	none := &Layer{Name: "no-apk"}
	if step := compileApkStep(none); step != nil {
		t.Errorf("layer with no apk: should compile to nil step, got %T", step)
	}

	l := &Layer{Name: "test-apps", SourceDir: "/layers/test-apps"}
	l.apk = []ApkPackageSpec{
		{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		{Apk: "tests/data/x.apk"},
	}
	step := compileApkStep(l)
	if step == nil {
		t.Fatal("compileApkStep returned nil for a layer with apk: entries")
	}
	apk, ok := step.(*ApkInstallStep)
	if !ok {
		t.Fatalf("compileApkStep returned %T, want *ApkInstallStep", step)
	}
	if apk.Kind() != StepKindApkInstall {
		t.Errorf("Kind() = %q, want %q", apk.Kind(), StepKindApkInstall)
	}
	if len(apk.Packages) != 2 {
		t.Errorf("Packages len = %d, want 2", len(apk.Packages))
	}
	if apk.LayerName != "test-apps" || apk.LayerDir != "/layers/test-apps" {
		t.Errorf("LayerName/LayerDir = %q/%q", apk.LayerName, apk.LayerDir)
	}
	// PackageIDs excludes committed-APK entries (no id to uninstall by).
	ids := apk.PackageIDs()
	if len(ids) != 1 || ids[0] != "org.fdroid.fdroid" {
		t.Errorf("PackageIDs = %v, want [org.fdroid.fdroid]", ids)
	}
	if apk.Reverse() != nil {
		t.Errorf("ApkInstallStep.Reverse() should be nil (android teardown is not ledger-based)")
	}
}

// TestOCITargetSkipsApkInstall proves apk installs are SKIPPED at image-build
// (there is no device at build time) — emitStep returns nil and emits nothing.
func TestOCITargetSkipsApkInstall(t *testing.T) {
	tgt := &OCITarget{}
	step := &ApkInstallStep{
		Packages:  []ApkPackageSpec{{Package: "org.fdroid.fdroid"}},
		LayerName: "test-apps",
	}
	if err := tgt.emitStep(step, &InstallPlan{}); err != nil {
		t.Fatalf("OCITarget.emitStep(ApkInstallStep) = %v, want nil (skip)", err)
	}
	if tgt.buf.Len() != 0 {
		t.Errorf("OCITarget emitted %q for an apk step; should emit nothing", tgt.buf.String())
	}
}

// TestPopulateLayerApk verifies the candy manifest `apk:` field flows through the
// populator onto the resolved Layer.
func TestPopulateLayerApk(t *testing.T) {
	ly := &CandyYAML{
		Apk: []ApkPackageSpec{
			{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		},
	}
	l := &Layer{Name: "test-apps"}
	populateLayerFromYAML(l, ly)
	if !l.HasApk() {
		t.Fatal("HasApk() = false after populating apk: entries")
	}
	if len(l.Apk()) != 1 || l.Apk()[0].Package != "org.fdroid.fdroid" {
		t.Errorf("Apk() = %+v", l.Apk())
	}
}

// TestResolveApkPath checks committed-APK path resolution: absolute verbatim,
// layer-relative when present, else project-cwd-relative fallback.
func TestResolveApkPath(t *testing.T) {
	if got := resolveApkPath("/abs/x.apk", "/layers/foo"); got != "/abs/x.apk" {
		t.Errorf("absolute path = %q, want verbatim", got)
	}
	// Layer-relative miss → cwd-relative fallback (verbatim ref).
	if got := resolveApkPath("tests/data/x.apk", "/nonexistent-layer-dir"); got != "tests/data/x.apk" {
		t.Errorf("relative fallback = %q, want tests/data/x.apk", got)
	}
}
