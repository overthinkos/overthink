package main

import (
	"fmt"
	"os"
	"path/filepath"
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

// TestCompileApkStep verifies the candy `apk:` package format compiles into a
// single ApkInstallStep carrying every entry, and that an empty apk: list
// compiles to nothing.
func TestCompileApkStep(t *testing.T) {
	none := &Candy{Name: "no-apk"}
	if step := compileApkStep(none); step != nil {
		t.Errorf("candy with no apk: should compile to nil step, got %T", step)
	}

	l := &Candy{Name: "test-apps", SourceDir: "/layers/test-apps"}
	l.apk = []ApkPackageSpec{
		{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		{Apk: "tests/data/x.apk"},
	}
	step := compileApkStep(l)
	if step == nil {
		t.Fatal("compileApkStep returned nil for a candy with apk: entries")
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
	if apk.CandyName != "test-apps" || apk.CandyDir != "/layers/test-apps" {
		t.Errorf("CandyName/CandyDir = %q/%q", apk.CandyName, apk.CandyDir)
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
		CandyName: "test-apps",
	}
	if err := tgt.emitStep(step, &InstallPlan{}); err != nil {
		t.Fatalf("OCITarget.emitStep(ApkInstallStep) = %v, want nil (skip)", err)
	}
	if tgt.buf.Len() != 0 {
		t.Errorf("OCITarget emitted %q for an apk step; should emit nothing", tgt.buf.String())
	}
}

// TestPopulateCandyApk verifies the candy manifest `apk:` field flows through the
// populator onto the resolved Candy.
func TestPopulateCandyApk(t *testing.T) {
	ly := &CandyYAML{
		Apk: []ApkPackageSpec{
			{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
		},
	}
	l := &Candy{Name: "test-apps"}
	populateCandyFromYAML(l, ly)
	if !l.HasApk() {
		t.Fatal("HasApk() = false after populating apk: entries")
	}
	if len(l.Apk()) != 1 || l.Apk()[0].Package != "org.fdroid.fdroid" {
		t.Errorf("Apk() = %+v", l.Apk())
	}
}

// TestResolveApkPath checks committed-APK path resolution: absolute verbatim,
// candy-relative when present, project-root-relative via walk-up (the @github
// fetched-candy case), else verbatim ref when nothing resolves.
func TestResolveApkPath(t *testing.T) {
	if got := resolveApkPath("/abs/x.apk", "/layers/foo"); got != "/abs/x.apk" {
		t.Errorf("absolute path = %q, want verbatim", got)
	}
	// No anchor resolves (candyDir + ancestors lack the file) → verbatim ref.
	if got := resolveApkPath("tests/data/x.apk", "/nonexistent-layer-dir"); got != "tests/data/x.apk" {
		t.Errorf("relative fallback = %q, want tests/data/x.apk", got)
	}

	// Fetched-candy layout: <repo>/tests/data/x.apk exists, candyDir is
	// <repo>/candy/android-apidemos (the file is NOT under candyDir). The walk-up
	// must resolve the project-root-relative ref to <repo>/tests/data/x.apk.
	// Without the walk-up this returns the bare ref (cwd-relative) → install
	// fails with "open tests/data/x.apk: no such file or directory".
	repo := t.TempDir()
	candyDir := filepath.Join(repo, "candy", "android-apidemos")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	apk := filepath.Join(repo, "tests", "data", "x.apk")
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveApkPath("tests/data/x.apk", candyDir); got != apk {
		t.Errorf("project-root walk-up = %q, want %q", got, apk)
	}

	// Candy-relative takes priority (closest anchor wins) when the file sits
	// directly under candyDir.
	localApk := filepath.Join(candyDir, "local.apk")
	if err := os.WriteFile(localApk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveApkPath("local.apk", candyDir); got != localApk {
		t.Errorf("candy-relative = %q, want %q", got, localApk)
	}
}

// TestResolveCheckApk covers the eval-verb path resolution (adb: install /
// appium: install-app), which anchors a relative committed-APK ref against a
// resolved candy's source tree — including the fallback used when the AUTHORING
// candy wasn't collected into the eval-live candy map (the reachability gap).
func TestResolveCheckApk(t *testing.T) {
	repo := t.TempDir()
	apk := filepath.Join(repo, "tests", "data", "x.apk") // project-root fixture
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	authorDir := filepath.Join(repo, "candy", "android-emulator-layer")
	siblingDir := filepath.Join(repo, "candy", "sshd")
	for _, d := range []string{authorDir, siblingDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// LOCAL candy: map key == bare name. Origin "candy:<name>" → resolves.
	r := &Runner{CandyDirs: map[string]string{"android-emulator-layer": authorDir, "sshd": siblingDir}}
	if got := r.resolveCheckApk("./tests/data/x.apk", "candy:android-emulator-layer"); got != apk {
		t.Errorf("local-candy resolve = %q, want %q", got, apk)
	}
	// FETCHED candy: map key == bare @github ref, and CollectDescriptions stamps Origin
	// with that same ref. CandyDirs[origin-key] must match (the real bug — keying
	// by bare NAME instead of the ref form left the lookup empty → install failed).
	const ref = "github.com/owner/repo/candy/android-emulator-layer"
	rRemote := &Runner{CandyDirs: map[string]string{ref: authorDir}}
	if got := rRemote.resolveCheckApk("./tests/data/x.apk", "candy:"+ref); got != apk {
		t.Errorf("fetched-candy (ref-keyed) resolve = %q, want %q", got, apk)
	}
	// Authoring candy NOT in CandyDirs → ref left verbatim (no black-magic
	// fallback to a different candy).
	r2 := &Runner{CandyDirs: map[string]string{"sshd": siblingDir}}
	if got := r2.resolveCheckApk("./tests/data/x.apk", "candy:android-emulator-layer"); got != "./tests/data/x.apk" {
		t.Errorf("unknown-candy = %q, want verbatim", got)
	}
	// Absolute passes through; no-candies leaves the ref verbatim (no regression).
	if got := r2.resolveCheckApk("/abs/y.apk", "candy:foo"); got != "/abs/y.apk" {
		t.Errorf("absolute = %q, want verbatim", got)
	}
	r3 := &Runner{}
	if got := r3.resolveCheckApk("./tests/data/x.apk", "candy:foo"); got != "./tests/data/x.apk" {
		t.Errorf("no-candies fallthrough = %q, want verbatim", got)
	}
}
