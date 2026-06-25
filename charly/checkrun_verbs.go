package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
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

// runUser: getent passwd. Parses uid/gid/home/shell for optional matching. The account
// name + expected uid/gid/home/shell come from the `user` plugin's decoded plugin_input
// (the verb left #Op for its dedicated builtin plugin unit) — c is retained for failf/passf
// reporting context.
func (r *Runner) runUser(ctx context.Context, c *Op, user string, wantUID, wantGID *int, wantHome, wantShell string) CheckResult {
	probe := fmt.Sprintf(`getent passwd %s`, shellSingleQuote(user))
	out, _, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 {
		return failf(c, "user not found")
	}
	// Fields: user:x:uid:gid:gecos:home:shell
	parts := strings.SplitN(strings.TrimSpace(out), ":", 7)
	if len(parts) < 7 {
		return failf(c, "unexpected passwd line: %q", out)
	}
	uid, _ := strconv.Atoi(parts[2])
	gid, _ := strconv.Atoi(parts[3])
	home, shell := parts[5], parts[6]
	if wantUID != nil && uid != *wantUID {
		return failf(c, "uid=%d, want %d", uid, *wantUID)
	}
	if wantGID != nil && gid != *wantGID {
		return failf(c, "gid=%d, want %d", gid, *wantGID)
	}
	if wantHome != "" && home != wantHome {
		return failf(c, "home=%s, want %s", home, wantHome)
	}
	if wantShell != "" && shell != wantShell {
		return failf(c, "shell=%s, want %s", shell, wantShell)
	}
	return passf(c, fmt.Sprintf("uid=%d gid=%d", uid, gid))
}

// runUnixGroup: getent group. Parses gid and members. The group name + expected gid come
// from the unix_group plugin's decoded plugin_input (the verb left #Op for its dedicated
// builtin plugin unit) — c is retained for failf/passf reporting context.
func (r *Runner) runUnixGroup(ctx context.Context, c *Op, group string, wantGID *int) CheckResult {
	probe := fmt.Sprintf(`getent group %s`, shellSingleQuote(group))
	out, _, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 {
		return failf(c, "group not found")
	}
	// Fields: group:x:gid:members
	parts := strings.SplitN(strings.TrimSpace(out), ":", 4)
	if len(parts) < 4 {
		return failf(c, "unexpected group line: %q", out)
	}
	gid, _ := strconv.Atoi(parts[2])
	if wantGID != nil && gid != *wantGID {
		return failf(c, "gid=%d, want %d", gid, *wantGID)
	}
	return passf(c, fmt.Sprintf("gid=%d", gid))
}

