package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ---------------------------------------------------------------------------
// Phase 7 verbs: probes that complete the goss-parity feature set.
//
// Each probe follows the same pattern:
//   1. Build a shell snippet that emits a deterministic single-line result.
//   2. Run it via r.Exec (container or image executor).
//   3. Parse + compare against the Check's expected attributes.
//
// The probes are intentionally distribution-agnostic — each one tries the
// tools most commonly available (rpm / dpkg / pacman for packages,
// supervisorctl / systemctl for services) and falls back cleanly.
// ---------------------------------------------------------------------------

// resolvePackageName picks the correct package name for the running image's
// distro. If packageMap has a key matching any of the image's distro tags, that
// mapping wins; otherwise the pkg scalar is used as-is. The first matching distro
// tag wins — tags are authored in priority order ("fedora:43" before "fedora"), so
// a map keyed by either hits. The package name + per-distro map arrive from the
// `package` plugin's typed plugin_input (params.PackageInput) — the verb left the
// closed #Op, so they no longer ride the (removed) Op.Package/Op.PackageMap fields.
// Shared by the assert path (runPackage), the typed-step act (packageVerb.ConstructStep),
// and the runtime act (packageVerb.RenderProvisionScript).
func resolvePackageName(pkg string, packageMap map[string]string, distros []string) string {
	if len(packageMap) == 0 {
		return pkg
	}
	for _, tag := range distros {
		if name, ok := packageMap[tag]; ok && name != "" {
			return name
		}
	}
	return pkg
}

// runPackage: `rpm -q`, `dpkg -s`, or `pacman -Q` — exit 0 ⇒ installed. The package
// name, optional install expectation, version allow-list, and per-distro map arrive from
// the `package` plugin's typed plugin_input (params.PackageInput, decoded by
// packageVerb.RunVerb in plugin_verb_package.go) — the verb left the closed #Op, so they
// no longer ride the (removed) Op.Package/Op.Installed/Op.Versions/Op.PackageMap fields.
// c is retained only for result metadata (id/description via failf/passf) + r.Distros.
func (r *Runner) runPackage(ctx context.Context, c *Op, pkg string, packageMap map[string]string, installed *bool, versions []string) CheckResult {
	wantInstalled := true
	if installed != nil {
		wantInstalled = *installed
	}
	name := resolvePackageName(pkg, packageMap, r.Distros)
	pkgQ := shellSingleQuote(name)
	probe := fmt.Sprintf(
		`rpm -q %[1]s >/dev/null 2>&1 || (dpkg -s %[1]s 2>/dev/null | grep -q "^Status:.*install ok installed") || pacman -Q %[1]s >/dev/null 2>&1`,
		pkgQ)
	_, stderr, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (%s)", err, stderr)
	}
	isInstalled := exit == 0
	if isInstalled != wantInstalled {
		return failf(c, "installed=%v, want %v", isInstalled, wantInstalled)
	}
	if !isInstalled {
		return passf(c, "absent (as expected)")
	}
	if len(versions) > 0 {
		versionProbe := fmt.Sprintf(
			`rpm -q --qf '%%{VERSION}\n' %[1]s 2>/dev/null || dpkg -s %[1]s 2>/dev/null | awk '/^Version:/{print $2; exit}' || pacman -Q %[1]s 2>/dev/null | awk '{print $2}'`,
			pkgQ)
		ver, _, exit, err := r.Exec.RunCapture(ctx, versionProbe)
		if err != nil || exit != 0 {
			return failf(c, "version probe exit %d err %v", exit, err)
		}
		got := strings.TrimSpace(ver)
		matched := slices.Contains(versions, got)
		if !matched {
			return failf(c, "version %q not in %v", got, versions)
		}
	}
	return passf(c, "installed")
}

// runService: asks supervisorctl then systemctl. Matches `running` and `enabled`
// attributes when set. The service unit + the running/enabled expectations arrive from
// the `service` plugin's typed plugin_input (params.ServiceInput, decoded by
// serviceVerb.RunVerb in plugin_verb_service.go) — the verb left the closed #Op, so they
// no longer ride the (removed) Op.Service/Op.Running/Op.Enabled fields. c is retained
// only for result metadata (id/description via failf/passf).
func (r *Runner) runService(ctx context.Context, c *Op, service string, running, enabled *bool) CheckResult {
	svc := shellSingleQuote(service)
	// Running check
	if running != nil {
		probe := fmt.Sprintf(
			`supervisorctl status %[1]s 2>/dev/null | grep -q RUNNING || systemctl is-active --quiet %[1]s`,
			svc)
		_, _, exit, err := r.Exec.RunCapture(ctx, probe)
		if err != nil {
			return failf(c, "running probe: %v", err)
		}
		isRunning := exit == 0
		if isRunning != *running {
			return failf(c, "running=%v, want %v", isRunning, *running)
		}
	}
	// Enabled check (systemd concept; supervisord services are always enabled
	// while supervisord is up — treat supervisorctl presence as enabled).
	if enabled != nil {
		probe := fmt.Sprintf(
			`supervisorctl status %[1]s 2>/dev/null | grep -qE '(RUNNING|STARTING|STOPPED)' || systemctl is-enabled --quiet %[1]s`,
			svc)
		_, _, exit, _ := r.Exec.RunCapture(ctx, probe)
		isEnabled := exit == 0
		if isEnabled != *enabled {
			return failf(c, "enabled=%v, want %v", isEnabled, *enabled)
		}
	}
	return passf(c, "ok")
}

