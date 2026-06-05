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

// EnsureOvInVenue is the GENERIC "copy ov into a running system" mechanism: it
// guarantees an invokable `ov` on ANY deployment venue — container (podman cp),
// VM / SSH host (scp), or the local host (install) — and returns the command the
// caller should use to invoke it. It is venue-agnostic because it works only
// through the DeployExecutor abstraction (RunCapture to probe, PutFile to
// deliver — cp / scp / podman cp per executor), so one code path serves every
// substrate (R3). Used by every in-venue `ov` caller (dbus delegation, desktop
// notifications, the VM-deploy strategy wrapper, nested from-image delegation)
// so an image need NOT bake the `ov` layer for those transient needs.
//
// Resolution (quiet — the caller decides what, if anything, to print):
//
//	1. The venue's SYSTEM ov (PATH `ov`, normally a package-managed binary kept
//	   current by the package manager) is authoritative whenever it is at least
//	   as new as the host's (CalVer — the true build identity, never a content
//	   checksum). It is used as-is: NEVER shadowed, downgraded, or overwritten.
//	2. Otherwise — ONLY when the venue ov is ABSENT or strictly OLDER (the
//	   automatic CalVer check is never skipped) — the host's OWN binary
//	   (os.Executable(), guaranteed current and from-image-capable) is delivered
//	   to a /tmp/ov-<calver> path. That path is OUTSIDE $PATH and is invoked by
//	   EXPLICIT path, NEVER via a PATH lookup — so it can NOT shadow a
//	   package-manager ov (e.g. the overthink-git /usr/bin/ov), not even one a
//	   package manager installs LATER: nothing puts the copy into a higher-PATH-
//	   priority location than the package. The /tmp name embeds the host CalVer so
//	   repeated calls reuse the same copy (idempotent — a still-good prior copy is
//	   verified and reused, never re-transferred).
//
// Returns "ov" (use the venue's PATH ov) or "/tmp/ov-<calver>" (the delivered
// host copy). SweepStaleTemps reclaims leftover /tmp copies.
func EnsureOvInVenue(ctx context.Context, exec DeployExecutor, opts EmitOpts) (string, error) {
	hostVer := OvVersion()
	stdout, _, _, _ := exec.RunCapture(ctx,
		`command -v ov >/dev/null 2>&1 && ov version 2>/dev/null || true`)
	venueVer := strings.TrimSpace(stdout)

	// Venue ov present AND equal-or-newer → use the system ov as-is.
	if venueVer != "" && !hostOvIsNewer(hostVer, venueVer) {
		return "ov", nil
	}

	tmp := "/tmp/ov-" + hostVer

	// Idempotent: a prior copy at the stable /tmp path that still runs is reused
	// — never re-transferred (the host binary is unchanged within one CalVer).
	if _, _, exit, err := exec.RunCapture(ctx,
		deployShellQuote(tmp)+" version >/dev/null 2>&1"); err == nil && exit == 0 {
		return tmp, nil
	}

	// Venue ov absent or older → deliver the host binary at the /tmp path
	// (outside $PATH; the system ov, if any, is left untouched).
	if err := putHostOvInVenue(ctx, exec, tmp, false, opts); err != nil {
		return "", err
	}
	return tmp, nil
}

// EnsureOvInGuest is the VM-deploy strategy coordinator that VmDeployTarget calls
// after the guest is booted, sshd is up, and cloud-init has finished. It layers
// the cloud-init `ov_install.strategy` policy on top of the generic
// EnsureOvInVenue mechanism:
//
//	auto / scp — EnsureOvInVenue: use the guest's system ov when current
//	             (>= host by CalVer); ONLY when absent or older, deliver the host
//	             ov to a /tmp path (no shadow). Routine updates are the package
//	             manager's job.
//	url        — verify `ov version` works (cloud-init runcmd did the curl)
//	skip       — verify `command -v ov` exists; error if missing
//
// Returns an informational message on success suitable for printing at info
// level (the deploy context wants the detail; transient callers like dbus stay
// quiet by calling EnsureOvInVenue directly).
func EnsureOvInGuest(
	ctx context.Context,
	spec *VmSpec,
	exec DeployExecutor,
	opts EmitOpts,
) (string, error) {
	strategy := ResolveOvInstallStrategy(spec)

	switch strategy {
	case OvInstallAuto, OvInstallScp:
		cmd, err := EnsureOvInVenue(ctx, exec, opts)
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

// putHostOvInVenue delivers THIS process's OWN ov binary (os.Executable()) into
// the venue at remotePath via the DeployExecutor's PutFile (cp for the local
// host, scp for an SSH/VM venue, `podman cp` for a container) — the single
// host→venue ov-delivery primitive (R3), used by EnsureOvInVenue (the generic
// copy-in) AND by deployNestedPodsInGuest (the host→nested from-image
// delegation). Callers deliver to a /tmp path with ownerRoot=false — a non-$PATH
// copy that leaves a venue's own packaged ov untouched and un-shadowed; the
// caller invokes the delivered binary by explicit path.
//
// The host binary is guaranteed current and from-image-capable: it is the binary
// running this very deploy. That is the whole point — a venue's own PATH ov may
// be a stale layer install (a @github-fetched ov layer ships no bin/ov, so its
// cmd: curls a pre-from-image release that reports the wall clock as its version)
// and must never be trusted as the delegation binary.
func putHostOvInVenue(ctx context.Context, exec DeployExecutor, remotePath string, ownerRoot bool, opts EmitOpts) error {
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
		return fmt.Errorf("copying host ov into venue %s: %w", remotePath, err)
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
