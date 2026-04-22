package main

import (
	"context"
	"fmt"
	"os"
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
//	auto / scp — PutFile os.Executable() → /usr/local/bin/ov (root:root, 0755)
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
		return installOvViaSCP(ctx, exec, opts)

	case OvInstallURL:
		return verifyOvPresent(ctx, exec, opts, "url")

	case OvInstallSkip:
		return verifyOvPresent(ctx, exec, opts, "skip")

	default:
		return "", fmt.Errorf("unknown ov_install.strategy: %q", strategy)
	}
}

// installOvViaSCP copies the locally-running `ov` binary to
// /usr/local/bin/ov in the guest. Uses os.Executable() to find the
// binary backing the current process.
func installOvViaSCP(ctx context.Context, exec DeployExecutor, opts EmitOpts) (string, error) {
	localPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating local ov binary via os.Executable(): %w", err)
	}
	// On Linux os.Executable returns the realpath; on macOS it can
	// point at an app bundle — stat it to ensure it's a regular file.
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", localPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("local ov path %s is not a regular file", localPath)
	}

	if err := exec.PutFile(ctx, localPath, "/usr/local/bin/ov", 0o755, true, opts); err != nil {
		return "", fmt.Errorf("scp ov binary to guest: %w", err)
	}
	return fmt.Sprintf("copied %s → guest:/usr/local/bin/ov", localPath), nil
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
