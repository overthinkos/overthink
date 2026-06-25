package main

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/overthinkos/overthink/charly/plugin/sdk"
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

// runDNS uses the charly process's resolver (host-side) under RunModeLive, and
// `getent hosts` inside the container under RunModeBox. The hostname + modifiers
// (resolvable/addrs) arrive from the `dns` plugin's typed plugin_input (params.DnsInput,
// decoded by dnsVerb.RunVerb in plugin_dns.go) — the verb left the closed #Op, so they
// no longer ride the (removed) Op.DNS/Resolvable/Addrs fields. c is retained for result
// metadata + the shared r.Mode/r.Exec.
func (r *Runner) runDNS(ctx context.Context, c *Op, dns string, resolvable *bool, addrs []string) CheckResult {
	wantResolvable := true
	if resolvable != nil {
		wantResolvable = *resolvable
	}
	if r.Mode == RunModeBox {
		probe := fmt.Sprintf(`getent hosts %s >/dev/null 2>&1`, shellSingleQuote(dns))
		_, _, exit, err := r.Exec.RunCapture(ctx, probe)
		if err != nil {
			return failf(c, "probe: %v", err)
		}
		isResolvable := exit == 0
		if isResolvable != wantResolvable {
			return failf(c, "resolvable=%v, want %v", isResolvable, wantResolvable)
		}
		return passf(c, fmt.Sprintf("resolvable=%v", isResolvable))
	}
	// Host-side resolve
	ips, err := net.LookupIP(dns)
	isResolvable := err == nil && len(ips) > 0
	if isResolvable != wantResolvable {
		return failf(c, "resolvable=%v (err: %v), want %v", isResolvable, err, wantResolvable)
	}
	if len(addrs) > 0 && isResolvable {
		want := map[string]bool{}
		for _, a := range addrs {
			want[a] = true
		}
		for _, ip := range ips {
			if want[ip.String()] {
				return passf(c, fmt.Sprintf("resolved to %s (match)", ip))
			}
		}
		return failf(c, "no resolved address matched required list %v (got %v)", addrs, ips)
	}
	return passf(c, fmt.Sprintf("resolvable=%v", isResolvable))
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

// runKernelParam reads a sysctl value and matches it via the value matchers. `sysctl -n
// <key>` is exactly the contents of /proc/sys/<key-with-dots-as-slashes>, so the probe reads
// that file DIRECTLY (inside the target, via r.Exec — kernel params are netns-scoped, so the
// container's/VM's /proc/sys is the source of truth, NOT the host's). Reading the file needs
// no procps-ng (`sysctl`), which minimal images like fedora-minimal omit — only coreutils
// `cat`, which every base ships. The act half (RenderProvisionScript) keeps `sysctl -w`: it
// runs in a deploy/runtime provisioning context where procps-ng is present. The key + value
// matchers come from the `kernel-param` plugin's decoded plugin_input (the verb left #Op for
// its dedicated builtin plugin unit) — c is retained for failf/passf reporting context.
func (r *Runner) runKernelParam(ctx context.Context, c *Op, param string, want MatcherList) CheckResult {
	path := "/proc/sys/" + strings.ReplaceAll(param, ".", "/")
	probe := fmt.Sprintf(`cat %s 2>/dev/null`, shellSingleQuote(path))
	out, _, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 {
		return failf(c, "kernel param not readable (exit %d)", exit)
	}
	value := strings.TrimSpace(out)
	if len(want) == 0 {
		return passf(c, fmt.Sprintf("value=%s", value))
	}
	if err := sdk.MatchAll(value, want); err != nil {
		return failf(c, "value=%s: %v", value, err)
	}
	return passf(c, fmt.Sprintf("value=%s", value))
}

// runMount: `findmnt -J <path>` — present ⇒ mounted. Optional source, filesystem, and opts
// matching. The mountpoint + expected source/filesystem/opt matchers come from the `mount`
// plugin's decoded plugin_input (the verb left #Op for its dedicated builtin plugin unit) —
// c is retained for failf/passf reporting context.
func (r *Runner) runMount(ctx context.Context, c *Op, mountPath, wantSource, wantFstype string, wantOpts MatcherList) CheckResult {
	mp := shellSingleQuote(mountPath)
	probe := fmt.Sprintf(`findmnt -n -o SOURCE,FSTYPE,OPTIONS %s 2>/dev/null`, mp)
	out, _, exit, err := r.Exec.RunCapture(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 {
		return failf(c, "mount not found")
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 3 {
		return failf(c, "unexpected findmnt output: %q", out)
	}
	source, fstype, opts := fields[0], fields[1], fields[2]
	if wantSource != "" && source != wantSource {
		return failf(c, "source=%s, want %s", source, wantSource)
	}
	if wantFstype != "" && fstype != wantFstype {
		return failf(c, "filesystem=%s, want %s", fstype, wantFstype)
	}
	if len(wantOpts) > 0 {
		if err := sdk.MatchAll(opts, wantOpts); err != nil {
			return failf(c, "opts %q: %v", opts, err)
		}
	}
	return passf(c, fmt.Sprintf("source=%s fstype=%s", source, fstype))
}

// runAddr: host-side TCP dial (under RunModeLive) or `nc -z` (under RunModeBox). The
// host:port + reachable expectation arrive from the `addr` plugin's typed plugin_input
// (params.AddrInput, decoded by addrVerb.RunVerb in plugin_addr.go) — the verb left the
// closed #Op, so they no longer ride the (removed) Op.Addr/Reachable fields. c is
// retained for result metadata + the shared r.Mode/r.Exec/r.DialTimeout.
func (r *Runner) runAddr(ctx context.Context, c *Op, addrTarget string, reachableExpect *bool) CheckResult {
	wantReachable := true
	if reachableExpect != nil {
		wantReachable = *reachableExpect
	}
	if r.Mode == RunModeBox {
		host, port := splitHostPort(addrTarget)
		probe := fmt.Sprintf(`nc -z -w %d %s %s 2>/dev/null`, 3, shellSingleQuote(host), shellSingleQuote(port))
		_, _, exit, err := r.Exec.RunCapture(ctx, probe)
		if err != nil {
			return failf(c, "probe: %v", err)
		}
		reachable := exit == 0
		if reachable != wantReachable {
			return failf(c, "reachable=%v, want %v", reachable, wantReachable)
		}
		return passf(c, fmt.Sprintf("reachable=%v", reachable))
	}
	conn, err := net.DialTimeout("tcp", addrTarget, r.DialTimeout)
	reachable := err == nil
	if reachable {
		_ = conn.Close()
	}
	if reachable != wantReachable {
		return failf(c, "reachable=%v (err: %v), want %v", reachable, err, wantReachable)
	}
	return passf(c, fmt.Sprintf("reachable=%v", reachable))
}

// splitHostPort splits "host:port"; unlike net.SplitHostPort it doesn't
// error on missing port — returns ("", "") in that case so the probe fails
// with a clear message.
func splitHostPort(s string) (string, string) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return s, ""
}
