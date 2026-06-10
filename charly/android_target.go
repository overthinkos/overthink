package main

// android_target.go — AndroidDeployTarget, the sixth DeployTarget
// (alongside OCITarget, PodDeployTarget, LocalDeployTarget, VmDeployTarget,
// K8sDeployTarget).
//
// Unlike K8sDeployTarget (whose Emit is a no-op — manifests are generated
// out-of-band), AndroidDeployTarget CONSUMES the InstallPlan IR like
// LocalDeployTarget: it walks the plans, picks out ApkInstallStep entries
// (the `apk:` package format), and installs each app onto the device via the
// shared installer (android_install.go). Every NON-apk step is skipped — a
// device is not a host/pod, so a candy's tasks/services/packages don't apply
// to it; only its apk: list does.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Android's PackageManager keeps initializing for SECONDS after
// sys.boot_completed flips to 1 — a freshly-pushed APK installed in that
// window throws "Failed to parse APK file" even though the file is intact
// (root-caused live: the on-device APK is byte-identical and installs cleanly
// once PM settles). So the install is RETRIED until it succeeds or the
// deadline elapses: the install SUCCEEDING is the readiness condition (a real
// synchronization primitive, NOT a fixed sleep). This mirrors the
// `eventually:`/`retry_interval:` the `adb-install-apidemos` eval check
// already uses for the same op on the same device.
const (
	androidInstallDeadline = 180 * time.Second
	androidInstallInterval = 5 * time.Second
)

// installWithRetry runs an install op, retrying on failure until the deadline.
// Used for BOTH device kinds and BOTH install methods so neither the
// committed-APK push nor the apkeep download can race PackageManager init.
// deadline/interval are parameters (not the consts directly) so tests can
// drive the loop without real sleeps.
func installWithRetry(deadline, interval time.Duration, op func() (string, error)) (string, error) {
	end := time.Now().Add(deadline)
	for {
		out, err := op()
		if err == nil || time.Now().After(end) {
			return out, err
		}
		time.Sleep(interval)
	}
}

// AndroidDeployTarget installs a deploy's `apk:` packages onto a kind:android
// device. Device is the resolved install handle (in-pod emulator or remote
// adb endpoint); resolveAndroidDevice (deploy_add_cmd_android.go) builds it.
type AndroidDeployTarget struct {
	Device AndroidDevice
}

// Name satisfies the DeployTarget interface.
func (a *AndroidDeployTarget) Name() string { return "android" }

// Emit walks the plans and installs every ApkInstallStep's apps onto the
// device. Non-apk steps are skipped (logged) — they belong to host/pod
// targets, not an Android device.
func (a *AndroidDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, step := range p.Steps {
			apkStep, ok := step.(*ApkInstallStep)
			if !ok {
				// Tasks / services / OS packages don't apply to a device.
				continue
			}
			if err := a.installStep(apkStep, opts); err != nil {
				return err
			}
		}
	}
	return nil
}

// installStep installs every app in one ApkInstallStep, dispatching by entry
// kind: a committed local APK (Apk set) pushes via goadb; a package id
// downloads via apkeep then installs.
func (a *AndroidDeployTarget) installStep(s *ApkInstallStep, opts EmitOpts) error {
	for _, spec := range s.Packages {
		if spec.Apk != "" {
			path := resolveApkPath(spec.Apk, s.CandyDir)
			if opts.DryRun {
				fmt.Printf("[dry-run] android: would install committed APK %s\n", path)
				continue
			}
			fmt.Printf("android: installing %s (layer=%s)\n", path, s.CandyName)
			out, err := installWithRetry(androidInstallDeadline, androidInstallInterval, func() (string, error) { return a.Device.InstallFromHostApk(path) })
			if err != nil {
				return fmt.Errorf("apk install %s: %w", path, err)
			}
			if out != "" {
				fmt.Println(out)
			}
			continue
		}
		if spec.Package == "" {
			return fmt.Errorf("apk entry in layer %q has neither package: nor apk:", s.CandyName)
		}
		if opts.DryRun {
			fmt.Printf("[dry-run] android: would install %s (source %s) via apkeep\n", spec.Package, spec.EffectiveSource())
			continue
		}
		fmt.Printf("android: installing %s (source %s, layer=%s)\n", spec.Package, spec.EffectiveSource(), s.CandyName)
		specCopy := spec
		out, err := installWithRetry(androidInstallDeadline, androidInstallInterval, func() (string, error) { return a.Device.InstallByPackage(specCopy) })
		if err != nil {
			return fmt.Errorf("apk install %s: %w", spec.Package, err)
		}
		if out != "" {
			fmt.Println(out)
		}
	}
	return nil
}

// resolveApkPath resolves a committed-APK reference. Absolute paths are used
// verbatim; relative paths anchor to the candy's source dir, then fall back
// to the project cwd (so a candy can reference a shared project file like
// tests/data/ApiDemos-debug.apk).
func resolveApkPath(ref, candyDir string) string {
	if filepath.IsAbs(ref) {
		return ref
	}
	if candyDir != "" {
		cand := filepath.Join(candyDir, ref)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	// Fall back to project-root-relative (cwd).
	return ref
}
