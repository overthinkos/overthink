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
	"time"
)

// SSHExecutor implements DeployExecutor against an SSH-reachable guest.
// Used by VmDeployTarget to run the same InstallPlan IR that
// LocalDeployTarget runs — but wrapped as `ssh <user>@<host> sudo
// bash -s` instead of direct local bash, and scp for file transfers.
//
// The builder-container path (VenueContainerBuilder steps for
// pixi/npm/cargo/aur) runs on the **host** (where podman is available),
// and the resulting artifacts are scp'd into the guest via PutFile.
// This keeps podman out of the guest's dependency surface.
//
// Credential-free by design: SSHExecutor contains zero key paths, zero
// host-key overrides, zero ssh-agent socket detection. ssh(1) reads
// ~/.ssh/config and ssh-agent for everything. VMs publish a managed
// Host stanza into ~/.config/charly/ssh_config (Included from ~/.ssh/config)
// that names the IdentityFile + UserKnownHostsFile + StrictHostKeyChecking
// per VM; charly vm create writes the stanza, charly vm destroy removes it.
type SSHExecutor struct {
	// User is the SSH login user. Optional — when empty, ssh(1) reads
	// the User directive from ~/.ssh/config or falls back to $USER.
	User string

	// Host is the SSH target — a hostname, an "[user@]host[:port]"
	// destination, or an ssh-config alias (e.g., the "charly-<vmname>"
	// stanzas managed by charly vm create). Required.
	Host string

	// Port is the SSH port. Optional — when 0, ssh uses the Port
	// directive from ~/.ssh/config or default 22.
	Port int

	// Args are extra ssh-cli arguments appended verbatim before the
	// destination. Pass-through from the deployment's `ssh_args:` field
	// (Ansible's ansible_ssh_extra_args). We do NOT parse, validate,
	// or interpret. Use sparingly — ssh-config is the right home for
	// persistent options.
	Args []string

	// ConnectTimeout caps the `-o ConnectTimeout=<N>` used in every
	// ssh invocation. Defaults to 10 seconds when zero.
	ConnectTimeout int
}

// Venue returns a stable "ssh://<user>@<host>:<port>" identifier so
// install_ledger.go can scope per-target ledgers without colliding
// with the local-shell ledger or other SSH targets. Components that
// are empty stringify naturally ("ssh://server" when User+Port unset).
func (e *SSHExecutor) Venue() string {
	switch {
	case e.User != "" && e.Port > 0:
		return fmt.Sprintf("ssh://%s@%s:%d", e.User, e.Host, e.Port)
	case e.User != "":
		return fmt.Sprintf("ssh://%s@%s", e.User, e.Host)
	case e.Port > 0:
		return fmt.Sprintf("ssh://%s:%d", e.Host, e.Port)
	default:
		return fmt.Sprintf("ssh://%s", e.Host)
	}
}

// RunSystem executes a bash script as root on the guest.
// Wraps as `ssh vm 'sudo bash -s'` with the script fed on stdin.
func (e *SSHExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] ssh vm sudo bash -s <<CHARLY_ROOT")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "CHARLY_ROOT")
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
		fmt.Fprintln(os.Stderr, "[dry-run] ssh vm bash -s <<CHARLY_USER")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "CHARLY_USER")
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
	// has write access to its own /tmp/charly-staging/ directory.
	tmpName := "charly-staging-" + filepath.Base(remotePath) + "-" + strconv.FormatInt(randSeed(), 36)
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

// GetFile retrieves the contents of a file from the guest via
// `ssh <host> [sudo] cat <path>` with stdout captured. asRoot==true
// wraps the read in sudo so restricted files (e.g. kubeconfig under
// /etc/rancher) are accessible from the unprivileged SSH user.
func (e *SSHExecutor) GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error) {
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] ssh vm %scat %s\n",
			func() string {
				if asRoot {
					return "sudo "
				}
				return ""
			}(), remotePath)
		return nil, nil
	}
	args := e.sshBaseArgs()
	if asRoot {
		args = append(args, "sudo", "cat", remotePath)
	} else {
		args = append(args, "cat", remotePath)
	}
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh cat %s: %w (stderr: %s)", remotePath, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RunCapture executes a script on the guest and returns captured
// stdout/stderr/exit. Mirrors the deleted VmTestExecutor.Exec semantics:
// no automatic root escalation (callers that need root prefix sudo).
func (e *SSHExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	return runCaptureCmd(cmd)
}

// Kind reports "ssh" — SSHExecutor targets any host reachable over SSH
// (VMs via the managed charly-<name> aliases, remote machines via
// "[user@]host[:port]" or ssh-config aliases).
func (e *SSHExecutor) Kind() string { return "ssh" }

// ResolveHome returns $HOME for `user` on the SSH-reachable target.
// Empty user resolves to whoever ssh logged in as (via `echo $HOME`),
// non-empty user goes through `getent passwd <user>` so callers can
// resolve any user's home on the guest.
//
// This replaces the bug where `LocalDeployTarget.HostHome` was
// initialized from `os.Getenv("HOME")` — the operator's home, not the
// guest user's. Any subsequent shell-rc edit (env.d sourcing block,
// new shell:-schema managed-block, etc.) now lands in the right place.
func (e *SSHExecutor) ResolveHome(ctx context.Context, user string) (string, error) {
	var script string
	if user == "" {
		// $HOME on the SSH-login user's session.
		script = "printf %s \"$HOME\""
	} else {
		// getent passwd <user> | cut -d: -f6
		// Fallback to ~user expansion if getent isn't available.
		script = `entry=$(getent passwd ` + shellSingleQuoteSSH(user) + ` 2>/dev/null) && printf %s "$(printf %s "$entry" | cut -d: -f6)" || check "printf %s ~` + shellSingleQuoteSSH(user) + `"`
	}
	// Feed the script over stdin to `bash -s` (the same transport
	// RunCapture/RunUser use). Passing it as a `bash -c <script>` remote
	// argv is broken: ssh space-joins all remote-command args into one
	// string and the guest shell re-splits on whitespace, so
	// `bash -c printf %s "$HOME"` runs bare `printf` ($0=printf, no format)
	// and exits 2 — which hard-aborted every VM deploy's guest-home
	// preflight. stdin preserves the script verbatim.
	args := e.sshBaseArgs()
	args = append(args, "bash", "-s")
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdin = strings.NewReader(script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("SSHExecutor.ResolveHome(%q): %w", user, err)
	}
	home := strings.TrimSpace(stdout.String())
	if home == "" {
		return "", fmt.Errorf("SSHExecutor.ResolveHome(%q): empty result", user)
	}
	return home, nil
}

// shellSingleQuoteSSH quotes `s` for safe inclusion in a bash -c script
// passed via ssh. Mirrors the shellQuote helper in wl.go but kept local
// to deploy_executor_ssh.go so the SSH-quoting concern stays bundled
// with the SSH transport code.
func shellSingleQuoteSSH(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WaitForSSH polls the guest's sshd until it accepts connections
// (bounded by maxWaitSeconds). Returns nil on first successful
// connect, error on timeout. Used by VmDeployTarget right after
// `charly vm create`.
//
// The loop uses a wall-clock deadline (not an iteration count) and
// a 1-second sleep between attempts. The previous design (300
// iterations with no sleep) fast-failed in ~15 seconds when each
// SSH attempt errored quickly — connection-refused returns in
// ~50 ms, host-key-mismatch in ~20 ms — burning the entire
// "300-second budget" in a tiny window. With a 1 s sleep between
// attempts, slow-to-boot guests get the full polling window
// they were always supposed to receive. Fixed during the
// 2026-05-06 R10 follow-up.
func (e *SSHExecutor) WaitForSSH(ctx context.Context, maxWaitSeconds int) error {
	if maxWaitSeconds <= 0 {
		maxWaitSeconds = 120
	}
	deadline := time.Now().Add(time.Duration(maxWaitSeconds) * time.Second)
	for time.Now().Before(deadline) {
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
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("timed out waiting for sshd on %s:%d after %d seconds", e.Host, e.Port, maxWaitSeconds)
}

// WaitForCloudInit runs `cloud-init status --wait` on the guest,
// blocking until cloud-init finishes (or fails). Only meaningful for
// cloud-image VMs; callers should skip this for bootc sources with
// no cidata ISO attached.
func (e *SSHExecutor) WaitForCloudInit(ctx context.Context) error {
	// On first boot cloud-init regenerates the SSH host keys and restarts
	// sshd AFTER the initial sshd start (i.e. after WaitForSSH already
	// passed). That restart drops in-flight connections and resets the next
	// one mid-key-exchange ("kex_exchange_identification: Connection reset by
	// peer"), which otherwise fails the scp in EnsureCharlyInGuest. Retry until an
	// ssh connection SURVIVES `cloud-init status --wait` — that is the
	// deterministic signal that cloud-init has settled and sshd is stable
	// (not a sleep-and-pray). `|| true` tolerates a non-zero cloud-init result
	// (error/degraded): we wait for cloud-init to be DONE, not necessarily OK,
	// and a non-zero status delivered over a live connection still proves sshd
	// is stable.
	script := `if command -v cloud-init >/dev/null 2>&1; then cloud-init status --wait >/dev/null 2>&1 || true; fi`
	deadline := time.Now().Add(5 * time.Minute)
	for {
		var buf bytes.Buffer
		args := e.sshBaseArgs()
		args = append(args, "bash", "-s")
		cmd := exec.CommandContext(ctx, "ssh", args...)
		cmd.Stdin = strings.NewReader(script)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cloud-init wait: ssh did not stabilize within 5m")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// sshBaseArgs builds the common ssh invocation prefix. ssh(1) reads
// ~/.ssh/config + ssh-agent for keys, host-key checking, identity
// files, etc. We supply only the per-call ergonomics (LogLevel,
// ConnectTimeout) plus optional Port (when caller pre-parsed it from
// the destination string) plus the deployment's pass-through Args.
// The destination is "user@host" when User is set, otherwise just Host
// — letting ssh-config's User directive apply.
func (e *SSHExecutor) sshBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	args := []string{
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
	}
	if e.Port > 0 {
		args = append(args, "-p", strconv.Itoa(e.Port))
	}
	args = append(args, e.Args...)
	if e.User != "" {
		args = append(args, fmt.Sprintf("%s@%s", e.User, e.Host))
	} else {
		args = append(args, e.Host)
	}
	return args
}

// scpBaseArgs builds the scp-invocation prefix. scp reads ~/.ssh/config
// the same way ssh does. Note: scp's `-P` flag has uppercase semantics
// (vs ssh's lowercase `-p`); SSHArgs is pass-through, so callers
// targeting scp-specific options should put them in scp's expected form.
func (e *SSHExecutor) scpBaseArgs() []string {
	connectTimeout := e.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10
	}
	args := []string{
		"-o", "LogLevel=ERROR",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeout),
	}
	if e.Port > 0 {
		args = append(args, "-P", strconv.Itoa(e.Port))
	}
	args = append(args, e.Args...)
	return args
}

// randSeed returns a fast-enough unique number for staging filename
// suffixes. Not cryptographically strong — it only needs to avoid
// collisions between concurrent PutFile calls for the same remotePath.
func randSeed() int64 {
	return int64(os.Getpid())<<16 | int64(os.Getppid()&0xffff)
}
