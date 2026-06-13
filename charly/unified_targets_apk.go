package main

// unified_targets_apk.go — Add / Del for the android target.
//
// NOTE: this file is deliberately NOT named *_android.go — that suffix is
// a GOOS build constraint (android) and would exclude it from a linux/amd64
// build. The "apk" name reflects the apk: package-install payload.
//
// A target:android deploy installs its add_candy: candies' apk: packages
// onto a kind:android DEVICE (an in-pod emulator or a remote adb endpoint)
// via AndroidDeployTarget. The apps ride in on the compiled plans'
// ApkInstallStep entries. Add resolves + readiness-gates the device then
// installs; Del uninstalls best-effort.
//
// AndroidUnifiedTarget is NOT a LifecycleTarget — the device's lifecycle
// belongs to its pod deploy / the remote host.

import (
	"context"
	"fmt"
	"os"
)

// Add installs the deploy's add_candy: apk packages onto the kind:android
// device. node fields come from dctx.Node (the dispatch-merged node).
func (t *AndroidUnifiedTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	node := dctx.Node
	if node == nil || node.Android == "" {
		return fmt.Errorf("deploy %q: target=android requires `android:` (kind:android device reference)", t.NodeName)
	}
	spec := findAndroidSpec(dctx.Dir, node.Android)
	if spec == nil {
		return fmt.Errorf("deploy %q: kind:android device %q not declared in the android: section", t.NodeName, node.Android)
	}

	// Dry-run prints the planned installs WITHOUT resolving (or requiring)
	// a live device — the emulator pod may not be running yet.
	if opts.DryRun {
		fmt.Printf("[dry-run] android device %q (apk packages from add_layer: %v)\n", node.Android, node.AddCandy)
		return (&AndroidDeployTarget{}).Emit(plans, opts)
	}

	dev, err := resolveAndroidDevice(spec, node, opts.Path)
	if err != nil {
		return fmt.Errorf("deploy %q: resolving android device %q: %w", t.NodeName, node.Android, err)
	}

	// Readiness gate — poll sys.boot_completed (a real synchronization
	// condition, never a fixed sleep).
	if err := waitAndroidReady(dev, androidBootDeadline); err != nil {
		return fmt.Errorf("deploy %q: %w", t.NodeName, err)
	}

	return (&AndroidDeployTarget{Device: dev}).Emit(plans, opts)
}

// Del uninstalls the deploy's apk packages best-effort. The device itself
// (the pod / remote endpoint) is left intact — its lifecycle belongs to
// the pod deploy / the remote host. Body lifted from the former per-kind
// android-del path. Del's contract is node-free, so it re-resolves the entry
// from the deploy tree (matching every other kind's Del).
func (t *AndroidUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	tree, _ := resolveTreeRoot(dir)
	node, ok := tree[t.NodeName]
	if !ok {
		fmt.Fprintf(os.Stderr, "charly deploy del %s: no android deploy entry; nothing to uninstall\n", t.NodeName)
		return nil
	}
	spec := findAndroidSpec(dir, node.Android)
	if spec == nil {
		return fmt.Errorf("deploy %q: kind:android device %q not declared", t.NodeName, node.Android)
	}
	dev, err := resolveAndroidDevice(spec, &node, t.NodeName)
	if err != nil {
		// Device gone (pod removed) — nothing to uninstall.
		fmt.Fprintf(os.Stderr, "charly deploy del %s: android device not reachable (%v); skipping uninstall\n", t.NodeName, err)
		return nil
	}
	pkgs := androidApkPackageIDs(&node, dir)
	for _, pkg := range pkgs {
		if opts.DryRun {
			fmt.Printf("[dry-run] android: would uninstall %s\n", pkg)
			continue
		}
		out, uerr := dev.Uninstall(pkg)
		if uerr != nil {
			fmt.Fprintf(os.Stderr, "charly deploy del %s: uninstall %s: %v\n", t.NodeName, pkg, uerr)
			continue
		}
		fmt.Printf("android: uninstalled %s: %s\n", pkg, out)
	}
	return nil
}

// Test / Update are not supported on the android target — the device is
// managed via its pod deploy / remote host; deploy-scope check and updates
// flow through that substrate, not this app-install target.
func (t *AndroidUnifiedTarget) Test(ctx context.Context, checks []Op, opts TestOpts) error {
	return fmt.Errorf("android %q: test not supported on android target (check the hosting pod/device)", t.NodeName)
}
func (t *AndroidUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("android %q: update not supported on android target (re-run charly deploy add to reinstall apks)", t.NodeName)
}
