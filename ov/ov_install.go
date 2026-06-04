package main

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// OvInstallStrategy is the resolved strategy string after defaults.
// Values: "auto" | "scp" | "url" | "skip". Empty input resolves to
// "auto".
type OvInstallStrategy string

const (
	OvInstallAuto OvInstallStrategy = "auto"
	OvInstallScp  OvInstallStrategy = "scp"
	OvInstallURL  OvInstallStrategy = "url"
	OvInstallSkip OvInstallStrategy = "skip"
)

// ResolveOvInstallStrategy reads spec.CloudInit.OvInstall and applies
// the default. Used by VmDeployTarget to decide which post-boot action
// (if any) to perform for delivering the ov binary into the guest.
func ResolveOvInstallStrategy(spec *VmSpec) OvInstallStrategy {
	if spec == nil || spec.CloudInit == nil || spec.CloudInit.OvInstall == nil {
		return OvInstallAuto
	}
	s := spec.CloudInit.OvInstall.Strategy
	switch s {
	case "":
		return OvInstallAuto
	case "auto", "scp", "url", "skip":
		return OvInstallStrategy(s)
	default:
		// Validator should have caught this; defensive fallback.
		return OvInstallAuto
	}
}

// EnsureOvInGuest is the top-level coordinator that VmDeployTarget
// calls after the guest is booted, sshd is up, and cloud-init has
// finished. Behavior per strategy:
//
//	auto / scp — use the guest's system ov when it is current (>= host by
//	             CalVer); ONLY when it is absent or older, scp the host ov to a
//	             /tmp path (outside $PATH, no shadow) for explicit use
//	             (syncOvIntoGuest). Routine updates are the package manager's job.
//	url        — verify `ov --version` works (cloud-init runcmd did the curl)
//	skip       — verify `command -v ov` exists; error if missing
//
// The caller passes an DeployExecutor (SSHDeployExecutor wired to the guest) so
// the same function works for any future non-SSH transport (e.g. a
// hypothetical vsock channel). Returns an informational message on
// success suitable for printing at info level.
func EnsureOvInGuest(
	ctx context.Context,
	spec *VmSpec,
	exec DeployExecutor,
	opts EmitOpts,
) (string, error) {
	strategy := ResolveOvInstallStrategy(spec)

	switch strategy {
	case OvInstallAuto, OvInstallScp:
		cmd, err := syncOvIntoGuest(ctx, exec, opts)
		if err != nil {
			return "", err
		}
		if cmd == "ov" {
			return "guest ov is current (>= host); using the system ov (no scp)", nil
		}
		return fmt.Sprintf("guest ov absent/outdated; host ov provided at %s for deploy use", cmd), nil

	case OvInstallURL:
		return verifyOvPresent(ctx, exec, opts, "url")

	case OvInstallSkip:
		return verifyOvPresent(ctx, exec, opts, "skip")

	default:
		return "", fmt.Errorf("unknown ov_install.strategy: %q", strategy)
	}
}

// syncOvIntoGuest implements the auto/scp strategy of EnsureOvInGuest: it
// decides whether a guest's own PATH ov is fresh enough to keep, or whether the
// host should provide its own binary at a /tmp path. It returns the `ov` command
// EnsureOvInGuest reports in its info message.
//
// (The host→nested `ov deploy from-image` delegation no longer routes through
// here — deployNestedPodsInGuest delivers the host ov unconditionally via
// putHostOvInGuest, because the host binary is the from-image authority and the
// guest's freshly-pacman-installed /usr/bin/ov must NOT be the delegation
// binary nor shadowed by it.)
//
// The venue's SYSTEM ov (the PATH `ov`, normally a package-manager binary kept
// current by `pacman -Syu`) is authoritative whenever it is at least as new as
// the host's: it is used as-is — NEVER shadowed, NEVER downgraded, NEVER
// overwritten. ONLY when the venue's ov is ABSENT or OLDER (by CalVer — the true
// build identity, never a content checksum, which can say "different" but never
// "newer") does the host scp its OWN binary — to a /tmp path OUTSIDE $PATH,
// invoked by explicit path — so a host driving a deploy with newer code runs
// that code WITHOUT clobbering or shadowing the venue's package-managed ov. The
// scp is the dev crutch; routine updates are the package manager's job.
//
// Returns "ov" (use the venue's PATH ov) or "/tmp/ov-<calver>" (the scp'd host
// copy). The /tmp name embeds the host CalVer so repeated calls within one
// deploy reuse the same copy (idempotent); SweepStaleTemps reclaims leftovers.
func syncOvIntoGuest(ctx context.Context, exec DeployExecutor, opts EmitOpts) (string, error) {
	hostVer := OvVersion()
	stdout, _, _, _ := exec.RunCapture(ctx,
		`command -v ov >/dev/null 2>&1 && ov version 2>/dev/null || true`)
	venueVer := strings.TrimSpace(stdout)

	// Venue ov present AND equal-or-newer → use the system ov as-is.
	if venueVer != "" && !hostOvIsNewer(hostVer, venueVer) {
		return "ov", nil
	}

	// Venue ov absent or older → provide the host binary at a /tmp path (outside
	// $PATH; the system ov is left untouched), invoked explicitly by the caller.
	tmp := "/tmp/ov-" + hostVer
	shown := venueVer
	if shown == "" {
		shown = "absent"
	}
	fmt.Fprintf(os.Stderr,
		"guest ov (%s) is absent/older than host ov (%s) — using a host copy at %s for this deploy (system ov untouched)\n",
		shown, hostVer, tmp)
	if err := putHostOvInGuest(ctx, exec, tmp, false, opts); err != nil {
		return "ov", err
	}
	return tmp, nil
}

// putHostOvInGuest scp's THIS process's OWN ov binary (os.Executable()) into the
// guest at remotePath. It is the single host→guest ov-delivery primitive (R3),
// used by syncOvIntoGuest (the auto/scp strategy's absent/older branch) AND by
// deployNestedPodsInGuest (the host→nested from-image delegation). BOTH callers
// deliver to a /tmp path with ownerRoot=false — a non-$PATH copy that leaves the
// guest's own /usr/bin/ov (the overthink-git pacman package) untouched and
// un-shadowed; the caller invokes the delivered binary by explicit path.
//
// The host binary is guaranteed current and from-image-capable: it is the binary
// running this very deploy. That is the whole point — a guest's own PATH ov may
// be a stale layer install (a @github-fetched ov layer ships no bin/ov, so its
// cmd: curls a pre-from-image release that reports the wall clock as its version)
// and must never be trusted as the delegation binary.
func putHostOvInGuest(ctx context.Context, exec DeployExecutor, remotePath string, ownerRoot bool, opts EmitOpts) error {
	localPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating local ov via os.Executable(): %w", err)
	}
	// On Linux os.Executable returns the realpath; on macOS it can point at an
	// app bundle — stat it to ensure it's a regular file.
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", localPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local ov path %s is not a regular file", localPath)
	}
	if err := exec.PutFile(ctx, localPath, remotePath, 0o755, ownerRoot, opts); err != nil {
		return fmt.Errorf("copying host ov into guest %s: %w", remotePath, err)
	}
	return nil
}

// verifyOvPresent runs `command -v ov` in the guest and reports
// whether the binary is already in place. Tail of the strategy
// implementations for "url" (cloud-init did the download) and "skip"
// (user-managed).
func verifyOvPresent(ctx context.Context, exec DeployExecutor, opts EmitOpts, strategy string) (string, error) {
	// `ov version` (subcommand), not `ov --version`. Kong returns exit 80
	// for unknown flags, which this check otherwise surfaces as a false
	// "ov not present" error. The PKGBUILD installs to /usr/bin/ov, not
	// /usr/local/bin/ov — rely on PATH rather than hard-coding either.
	script := `
set -e
if ! command -v ov >/dev/null 2>&1; then
    echo "ov binary not present in guest (ov_install.strategy: ` + strategy + `)"
    exit 1
fi
ov version
`
	if err := exec.RunSystem(ctx, script, opts); err != nil {
		return "", fmt.Errorf("ov presence check (strategy=%s): %w", strategy, err)
	}
	return fmt.Sprintf("verified ov present in guest (strategy=%s)", strategy), nil
}
