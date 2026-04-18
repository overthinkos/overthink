package main

import (
	"context"
	"fmt"
	"net"
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

// runPackage: `rpm -q`, `dpkg -s`, or `pacman -Q` — exit 0 ⇒ installed.
// When Versions is set, pulls the version string and compares exactly.
func (r *Runner) runPackage(ctx context.Context, c *Check) TestResult {
	wantInstalled := true
	if c.Installed != nil {
		wantInstalled = *c.Installed
	}
	pkg := shellSingleQuote(c.Package)
	probe := fmt.Sprintf(
		`rpm -q %[1]s >/dev/null 2>&1 || (dpkg -s %[1]s 2>/dev/null | grep -q "^Status:.*install ok installed") || pacman -Q %[1]s >/dev/null 2>&1`,
		pkg)
	_, stderr, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe failed: %v (%s)", err, stderr)
	}
	installed := exit == 0
	if installed != wantInstalled {
		return failf(c, "installed=%v, want %v", installed, wantInstalled)
	}
	if !installed {
		return passf(c, "absent (as expected)")
	}
	if len(c.Versions) > 0 {
		versionProbe := fmt.Sprintf(
			`rpm -q --qf '%%{VERSION}\n' %[1]s 2>/dev/null || dpkg -s %[1]s 2>/dev/null | awk '/^Version:/{print $2; exit}' || pacman -Q %[1]s 2>/dev/null | awk '{print $2}'`,
			pkg)
		ver, _, exit, err := r.Exec.Exec(ctx, versionProbe)
		if err != nil || exit != 0 {
			return failf(c, "version probe exit %d err %v", exit, err)
		}
		got := strings.TrimSpace(ver)
		matched := false
		for _, v := range c.Versions {
			if got == v {
				matched = true
				break
			}
		}
		if !matched {
			return failf(c, "version %q not in %v", got, c.Versions)
		}
	}
	return passf(c, "installed")
}

// runService: asks supervisorctl then systemctl. Matches `running` and
// `enabled` attributes when set.
func (r *Runner) runService(ctx context.Context, c *Check) TestResult {
	svc := shellSingleQuote(c.Service)
	// Running check
	if c.Running != nil {
		probe := fmt.Sprintf(
			`supervisorctl status %[1]s 2>/dev/null | grep -q RUNNING || systemctl is-active --quiet %[1]s`,
			svc)
		_, _, exit, err := r.Exec.Exec(ctx, probe)
		if err != nil {
			return failf(c, "running probe: %v", err)
		}
		running := exit == 0
		if running != *c.Running {
			return failf(c, "running=%v, want %v", running, *c.Running)
		}
	}
	// Enabled check (systemd concept; supervisord services are always enabled
	// while supervisord is up — treat supervisorctl presence as enabled).
	if c.Enabled != nil {
		probe := fmt.Sprintf(
			`supervisorctl status %[1]s 2>/dev/null | grep -qE '(RUNNING|STARTING|STOPPED)' || systemctl is-enabled --quiet %[1]s`,
			svc)
		_, _, exit, _ := r.Exec.Exec(ctx, probe)
		enabled := exit == 0
		if enabled != *c.Enabled {
			return failf(c, "enabled=%v, want %v", enabled, *c.Enabled)
		}
	}
	return passf(c, "ok")
}

// runProcess: pgrep -x by default (exact-name match).
func (r *Runner) runProcess(ctx context.Context, c *Check) TestResult {
	wantRunning := true
	if c.Running != nil {
		wantRunning = *c.Running
	}
	probe := fmt.Sprintf(`pgrep -x %s >/dev/null 2>&1`, shellSingleQuote(c.Process))
	_, _, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	running := exit == 0
	if running != wantRunning {
		return failf(c, "running=%v, want %v", running, wantRunning)
	}
	return passf(c, fmt.Sprintf("running=%v", running))
}

// runDNS uses the ov process's resolver (host-side) under RunModeTest, and
// `getent hosts` inside the container under RunModeImageTest.
func (r *Runner) runDNS(ctx context.Context, c *Check) TestResult {
	wantResolvable := true
	if c.Resolvable != nil {
		wantResolvable = *c.Resolvable
	}
	if r.Mode == RunModeImageTest {
		probe := fmt.Sprintf(`getent hosts %s >/dev/null 2>&1`, shellSingleQuote(c.DNS))
		_, _, exit, err := r.Exec.Exec(ctx, probe)
		if err != nil {
			return failf(c, "probe: %v", err)
		}
		resolvable := exit == 0
		if resolvable != wantResolvable {
			return failf(c, "resolvable=%v, want %v", resolvable, wantResolvable)
		}
		return passf(c, fmt.Sprintf("resolvable=%v", resolvable))
	}
	// Host-side resolve
	ips, err := net.LookupIP(c.DNS)
	resolvable := err == nil && len(ips) > 0
	if resolvable != wantResolvable {
		return failf(c, "resolvable=%v (err: %v), want %v", resolvable, err, wantResolvable)
	}
	if len(c.Addrs) > 0 && resolvable {
		want := map[string]bool{}
		for _, a := range c.Addrs {
			want[a] = true
		}
		for _, ip := range ips {
			if want[ip.String()] {
				return passf(c, fmt.Sprintf("resolved to %s (match)", ip))
			}
		}
		return failf(c, "no resolved address matched required list %v (got %v)", c.Addrs, ips)
	}
	return passf(c, fmt.Sprintf("resolvable=%v", resolvable))
}

// runUser: getent passwd. Parses uid/gid/home/shell for optional matching.
func (r *Runner) runUser(ctx context.Context, c *Check) TestResult {
	probe := fmt.Sprintf(`getent passwd %s`, shellSingleQuote(c.User))
	out, _, exit, err := r.Exec.Exec(ctx, probe)
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
	if c.UID != nil && uid != *c.UID {
		return failf(c, "uid=%d, want %d", uid, *c.UID)
	}
	if c.GID != nil && gid != *c.GID {
		return failf(c, "gid=%d, want %d", gid, *c.GID)
	}
	if c.Home != "" && home != c.Home {
		return failf(c, "home=%s, want %s", home, c.Home)
	}
	if c.Shell != "" && shell != c.Shell {
		return failf(c, "shell=%s, want %s", shell, c.Shell)
	}
	return passf(c, fmt.Sprintf("uid=%d gid=%d", uid, gid))
}

// runGroup: getent group. Parses gid and members.
func (r *Runner) runGroup(ctx context.Context, c *Check) TestResult {
	probe := fmt.Sprintf(`getent group %s`, shellSingleQuote(c.Group))
	out, _, exit, err := r.Exec.Exec(ctx, probe)
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
	if c.GID != nil && gid != *c.GID {
		return failf(c, "gid=%d, want %d", gid, *c.GID)
	}
	return passf(c, fmt.Sprintf("gid=%d", gid))
}

// runInterface: `ip -o addr show <name>` — verifies existence; Addrs + MTU
// checks parse the ip output.
func (r *Runner) runInterface(ctx context.Context, c *Check) TestResult {
	probe := fmt.Sprintf(`ip -o addr show %s 2>/dev/null`, shellSingleQuote(c.Interface))
	out, _, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 || strings.TrimSpace(out) == "" {
		return failf(c, "interface not found")
	}
	// MTU check via `ip link show`
	if c.MTU != nil {
		mtuOut, _, exit, err := r.Exec.Exec(ctx, fmt.Sprintf(`ip -o link show %s 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="mtu"){print $(i+1);exit}}'`, shellSingleQuote(c.Interface)))
		if err != nil || exit != 0 {
			return failf(c, "mtu probe exit %d err %v", exit, err)
		}
		got, _ := strconv.Atoi(strings.TrimSpace(mtuOut))
		if got != *c.MTU {
			return failf(c, "mtu=%d, want %d", got, *c.MTU)
		}
	}
	if len(c.Addrs) > 0 {
		for _, want := range c.Addrs {
			if !strings.Contains(out, want) {
				return failf(c, "missing address %s", want)
			}
		}
	}
	return passf(c, "ok")
}

// runKernelParam: `sysctl -n <key>`. Matches via the Matching attribute
// (treated as an expected exact value when scalar, or matcher when map).
func (r *Runner) runKernelParam(ctx context.Context, c *Check) TestResult {
	probe := fmt.Sprintf(`sysctl -n %s 2>/dev/null`, shellSingleQuote(c.KernelParam))
	out, _, exit, err := r.Exec.Exec(ctx, probe)
	if err != nil {
		return failf(c, "probe: %v", err)
	}
	if exit != 0 {
		return failf(c, "kernel param not readable (exit %d)", exit)
	}
	value := strings.TrimSpace(out)
	if len(c.Value) == 0 {
		return passf(c, fmt.Sprintf("value=%s", value))
	}
	if err := matchAll(value, c.Value); err != nil {
		return failf(c, "value=%s: %v", value, err)
	}
	return passf(c, fmt.Sprintf("value=%s", value))
}

// runMount: `findmnt -J <path>` — present ⇒ mounted. Optional source,
// filesystem, and opts matching.
func (r *Runner) runMount(ctx context.Context, c *Check) TestResult {
	mp := shellSingleQuote(c.Mount)
	probe := fmt.Sprintf(`findmnt -n -o SOURCE,FSTYPE,OPTIONS %s 2>/dev/null`, mp)
	out, _, exit, err := r.Exec.Exec(ctx, probe)
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
	if c.MountSource != "" && source != c.MountSource {
		return failf(c, "source=%s, want %s", source, c.MountSource)
	}
	if c.Filesystem != "" && fstype != c.Filesystem {
		return failf(c, "filesystem=%s, want %s", fstype, c.Filesystem)
	}
	if len(c.Opts) > 0 {
		if err := matchAll(opts, c.Opts); err != nil {
			return failf(c, "opts %q: %v", opts, err)
		}
	}
	return passf(c, fmt.Sprintf("source=%s fstype=%s", source, fstype))
}

// runAddr: host-side TCP dial (under RunModeTest) or `nc -z`
// (under RunModeImageTest).
func (r *Runner) runAddr(ctx context.Context, c *Check) TestResult {
	wantReachable := true
	if c.Reachable != nil {
		wantReachable = *c.Reachable
	}
	if r.Mode == RunModeImageTest {
		host, port := splitHostPort(c.Addr)
		probe := fmt.Sprintf(`nc -z -w %d %s %s 2>/dev/null`, 3, shellSingleQuote(host), shellSingleQuote(port))
		_, _, exit, err := r.Exec.Exec(ctx, probe)
		if err != nil {
			return failf(c, "probe: %v", err)
		}
		reachable := exit == 0
		if reachable != wantReachable {
			return failf(c, "reachable=%v, want %v", reachable, wantReachable)
		}
		return passf(c, fmt.Sprintf("reachable=%v", reachable))
	}
	conn, err := net.DialTimeout("tcp", c.Addr, r.DialTimeout)
	reachable := err == nil
	if reachable {
		_ = conn.Close()
	}
	if reachable != wantReachable {
		return failf(c, "reachable=%v (err: %v), want %v", reachable, err, wantReachable)
	}
	return passf(c, fmt.Sprintf("reachable=%v", reachable))
}

// runMatching is purely in-process: evaluates the Contains matchers against
// the Matching value. Useful as a building block for future derived checks
// that don't fit any other verb.
func (r *Runner) runMatching(ctx context.Context, c *Check) TestResult {
	value := matchValueString(c.Matching)
	if err := matchAll(value, c.Contains); err != nil {
		return failf(c, "%v", err)
	}
	return passf(c, fmt.Sprintf("value=%s", value))
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
