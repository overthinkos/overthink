package main

import (
	"context"
)

// DeployExecutor abstracts shell execution + file placement for deploy
// targets. HostDeployTarget uses LocalDeployExecutor (spawns bash directly);
// VmDeployTarget uses SSHDeployExecutor (wraps scripts as `ssh vm sudo
// bash -s`, uses scp for file transfers).
//
// The interface is intentionally narrow: just the four operations
// HostDeployTarget currently performs on the host. Any future
// transport (e.g. nspawn container, remote Kubernetes pod) implements
// the same four methods.
type DeployExecutor interface {
	// RunSystem executes a bash script with root privileges. On the
	// host, this is `sudo bash -s <<<script`; on the VM target, it's
	// `ssh <user>@<host> sudo bash -s <<<script`. The script body runs
	// with set -e semantics at the caller's discretion.
	RunSystem(ctx context.Context, script string, opts EmitOpts) error

	// RunUser executes a bash script as the invoking user (no sudo).
	// On the host, it's `bash -s <<<script`; on VM, `ssh <user>@<host>
	// bash -s <<<script` where <user> is the unprivileged guest user.
	RunUser(ctx context.Context, script string, opts EmitOpts) error

	// RunBuilder invokes the multi-stage builder image (podman run
	// <builder>) to compile pixi/npm/cargo/aur artifacts. On the host
	// this calls the existing BuilderRun helper. On VM deploys, the
	// builder runs *on the host* and artifacts are scp'd into the
	// guest via PutFile — podman inside the guest is not required.
	RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error)

	// PutFile places a file at a remote path. ownerRoot == true means
	// the file is chown'd to root:root and chmod'd according to mode.
	// On the host, this is a plain os.WriteFile (plus sudo chown when
	// ownerRoot). On VM, this is scp into a tmp location followed by
	// `sudo install -m <mode> -o root -g root` on the guest.
	PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error
}

// LocalDeployExecutor implements DeployExecutor against the invoking user's shell
// + filesystem. Faithful behavior-preserving wrapper around the
// existing runSudoShell / runUserShell / BuilderRun helpers.
type LocalDeployExecutor struct{}

// RunSystem delegates to the package-level runSudoShell.
func (LocalDeployExecutor) RunSystem(_ context.Context, script string, opts EmitOpts) error {
	return runSudoShell(script, opts)
}

// RunUser delegates to the package-level runUserShell.
func (LocalDeployExecutor) RunUser(_ context.Context, script string, opts EmitOpts) error {
	return runUserShell(script, opts)
}

// RunBuilder delegates to the package-level BuilderRun.
func (LocalDeployExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	return BuilderRun(ctx, opts)
}

// PutFile on the local executor is a direct filesystem write. When
// ownerRoot is set, the installer uses `sudo install -m <mode> -o root
// -g root` so the target path can be /usr/local/bin or similar.
func (LocalDeployExecutor) PutFile(_ context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if ownerRoot {
		// Use sudo install for atomic, correct-permissions placement.
		// `install` creates target directory if missing (-D).
		script := "install -D -m " + permOctal(mode) + " -o root -g root " + deployShellQuote(localPath) + " " + deployShellQuote(remotePath)
		return runSudoShell(script, opts)
	}
	script := "install -D -m " + permOctal(mode) + " " + deployShellQuote(localPath) + " " + deployShellQuote(remotePath)
	return runUserShell(script, opts)
}

// permOctal renders a uint32 mode as a 4-digit octal string suitable
// for the `install -m` flag.
func permOctal(mode uint32) string {
	return fmtOctal(mode)
}

func fmtOctal(mode uint32) string {
	if mode == 0 {
		return "0644"
	}
	// Render as 0NNN.
	hi := (mode >> 9) & 0o7
	mi := (mode >> 6) & 0o7
	lo := (mode >> 3) & 0o7
	vlo := mode & 0o7
	return string([]byte{
		'0',
		byte('0' + hi),
		byte('0' + mi),
		byte('0' + lo),
		byte('0' + vlo),
	})
}

// deployShellQuote wraps a string in single-quotes for safe embedding in a
// bash script. Handles embedded single quotes via the standard
// 'foo'\''bar' trick.
func deployShellQuote(s string) string {
	// Empty string → ''
	if s == "" {
		return "''"
	}
	// Replace each ' with '\''
	var b []byte
	b = append(b, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b = append(b, '\'', '\\', '\'', '\'')
			continue
		}
		b = append(b, s[i])
	}
	b = append(b, '\'')
	return string(b)
}
