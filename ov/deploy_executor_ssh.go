package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHExecutor implements DeployExecutor against an SSH-reachable guest.
// Used by VmDeployTarget to run the same InstallPlan IR that
// HostDeployTarget runs — but wrapped as `ssh <user>@<host> sudo
// bash -s` instead of direct local bash, and scp for file transfers.
//
// The builder-container path (VenueContainerBuilder steps for
// pixi/npm/cargo/aur) runs on the **host** (where podman is available),
// and the resulting artifacts are scp'd into the guest via PutFile.
// This keeps podman out of the guest's dependency surface.
type SSHExecutor struct {
	// User is the guest account to SSH as (typically "ov" for
	// cloud-image VMs; "root" for bootc VMs).
	User string

	// Host is the SSH target address. For user-mode-networking VMs
	// this is "127.0.0.1" and the guest's :22 is forwarded to Port.
	Host string

	// Port is the host-side port forwarded to the guest's :22.
	Port int

	// KeyPath is the absolute path to the SSH private key.
	KeyPath string

	// ConnectTimeout caps the `-o ConnectTimeout=<N>` used in every
	// ssh invocation. Defaults to 10 seconds when zero.
	ConnectTimeout int

	// KnownHostsFile is the path to the known_hosts file. When empty,
	// "/dev/null" is used — appropriate for ephemeral VMs where host
	// keys rotate on every VM recreation. Set for long-lived VMs where
	// you want man-in-the-middle protection.
	KnownHostsFile string
}

// Venue returns a stable "ssh://<user>@<host>:<port>" identifier so
// install_ledger.go can scope per-VM ledgers without colliding with
// the local-host ledger or other VMs.
func (e *SSHExecutor) Venue() string {
	return fmt.Sprintf("ssh://%s@%s:%d", e.User, e.Host, e.Port)
}

// RunSystem executes a bash script as root on the guest.
// Wraps as `ssh vm 'sudo bash -s'` with the script fed on stdin.
func (e *SSHExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] ssh vm sudo bash -s <<OV_ROOT")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_ROOT")
		return nil
	}
	args := e.sshBaseArgs()
	args = append(args, "sudo", "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunUser executes a bash script as the guest's unprivileged user
// (i.e. spec.SSH.User, the account SSHExecutor connects as).
func (e *SSHExecutor) RunUser(ctx context.Context, script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] ssh vm bash -s <<OV_USER")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_USER")
		return nil
	}
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunBuilder delegates to BuilderRun which runs the podman container
// *on the host*. Caller (VmDeployTarget) is responsible for scp-ing
// the resulting artifacts into the guest via PutFile afterwards —
// this executor doesn't shuttle artifact trees itself.
func (e *SSHExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	return BuilderRun(ctx, opts)
}

// PutFile copies a local file into the guest via scp, then uses sudo
// install on the guest to place it with the correct mode/owner.
// This two-step dance is needed because scp runs as the guest's
// unprivileged user (you can't scp directly to /usr/local/bin).
func (e *SSHExecutor) PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] scp %s vm:%s  (mode=%o, ownerRoot=%v)\n",
			localPath, remotePath, mode, ownerRoot)
		return nil
	}

	// Stage to a tmp path in the guest's home. The guest user always
	// has write access to its own /tmp/ov-staging/ directory.
	tmpName := "ov-staging-" + filepath.Base(remotePath) + "-" + strconv.FormatInt(randSeed(), 36)
	tmpRemote := "/tmp/" + tmpName

	// scp <local> <user>@<host>:<tmpRemote>
	scpArgs := e.scpBaseArgs()
	scpArgs = append(scpArgs, localPath, fmt.Sprintf("%s@%s:%s", e.User, e.Host, tmpRemote))
	scpCmd := exec.CommandContext(ctx, "scp", scpArgs...)
	scpCmd.Stderr = os.Stderr
	if err := scpCmd.Run(); err != nil {
		return fmt.Errorf("scp %s -> %s: %w", localPath, tmpRemote, err)
	}

	// ssh guest: move staged file into place with correct mode+owner.
	var installScript string
	modeOctal := fmtOctal(mode)
	if ownerRoot {
		installScript = fmt.Sprintf(
			"set -e\nsudo install -D -m %s -o root -g root %s %s\nrm -f %s\n",
			modeOctal,
			deployShellQuote(tmpRemote),
			deployShellQuote(remotePath),
			deployShellQuote(tmpRemote),
		)
	} else {
		installScript = fmt.Sprintf(
			"set -e\ninstall -D -m %s %s %s\nrm -f %s\n",
			modeOctal,
			deployShellQuote(tmpRemote),
			deployShellQuote(remotePath),
			deployShellQuote(tmpRemote),
		)
	}
	return e.RunSystem(ctx, installScript, opts)
}

// WaitForSSH polls the guest's sshd until it accepts connections
// (bounded by maxWaitSeconds). Returns nil on first successful
// connect, error on timeout. Used by VmDeployTarget right after
// `ov vm create`.
func (e *SSHExecutor) WaitForSSH(ctx context.Context, maxWaitSeconds int) error {
	if maxWaitSeconds <= 0 {
		maxWaitSeconds = 120
	}
	deadline := maxWaitSeconds / 3 // try 3× per second on average
	for i := 0; i < deadline*3; i++ {
		args := e.sshBaseArgs()
		args = append(args, "-o", "BatchMode=yes", "-o", "ConnectTimeout=2", "true")
		cmd := exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := cmd.Run(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return fmt.Errorf("timed out waiting for sshd on %s:%d after %d seconds", e.Host, e.Port, maxWaitSeconds)
}

// WaitForCloudInit runs `cloud-init status --wait` on the guest,
// blocking until cloud-init finishes (or fails). Only meaningful for
// cloud-image VMs; callers should skip this for bootc sources with
// no cidata ISO attached.
func (e *SSHExecutor) WaitForCloudInit(ctx context.Context) error {
	script := `
if command -v cloud-init >/dev/null 2>&1; then
    cloud-init status --wait
else
    echo "cloud-init not installed; skipping wait" >&2
fi
`
	var buf bytes.Buffer
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	return cmd.Run()
}

// sshBaseArgs builds the common ssh invocation prefix (options +
// destination). Used by RunSystem / RunUser / WaitForSSH /
// WaitForCloudInit.
func (e *SSHExecutor) sshBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	knownHosts := e.KnownHostsFile
	if knownHosts == "" {
		knownHosts = "/dev/null"
	}
	args := []string{
		"-i", e.KeyPath,
		"-p", strconv.Itoa(e.Port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
		fmt.Sprintf("%s@%s", e.User, e.Host),
	}
	return args
}

// scpBaseArgs builds the scp-invocation prefix. scp uses the same
// SSH options but scp's `-P` flag has uppercase semantics (vs ssh's
// lowercase `-p`).
func (e *SSHExecutor) scpBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	knownHosts := e.KnownHostsFile
	if knownHosts == "" {
		knownHosts = "/dev/null"
	}
	return []string{
		"-i", e.KeyPath,
		"-P", strconv.Itoa(e.Port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
	}
}

// randSeed returns a fast-enough unique number for staging filename
// suffixes. Not cryptographically strong — it only needs to avoid
// collisions between concurrent PutFile calls for the same remotePath.
func randSeed() int64 {
	return int64(os.Getpid())<<16 | int64(os.Getppid()&0xffff)
}
