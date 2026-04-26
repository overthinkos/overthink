package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
)

// DeployExecutor abstracts shell execution + file placement for deploy
// targets. HostDeployTarget uses LocalDeployExecutor (spawns bash directly);
// VmDeployTarget uses SSHExecutor (wraps scripts as `ssh vm sudo bash -s`,
// uses scp for file transfers). Nested topologies (container-in-vm,
// vm-in-container, host-in-vm-in-container, etc.) use NestedExecutor,
// which composes a parent DeployExecutor with a "shell jump" (podman
// exec / ssh / virsh) prepended to every primitive.
//
// The interface is narrow but carries one identity method — Venue() —
// that answers the question "where does bash actually run when I call
// RunSystem?". Ledger files live on that venue's filesystem, so the
// venue string is how install_ledger.go picks the right install
// database without a global constant.
type DeployExecutor interface {
	// Venue returns a stable identifier for where this executor's
	// commands physically run. Examples:
	//
	//   "local"                            — LocalDeployExecutor.
	//   "ssh://arch@127.0.0.1:2224"        — SSHExecutor.
	//   "nested:podman exec stack/local"   — NestedExecutor over local.
	//   "nested:ssh vm/local"              — NestedExecutor over SSH.
	//
	// The string is used as a map key for per-venue ledgers, so it
	// must be stable across invocations for the same logical target.
	// Not a URL — don't parse it; just compare.
	Venue() string

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

	// GetFile retrieves the contents of a file on the venue. asRoot==true
	// runs the read via sudo to handle paths the deploying user cannot
	// access (e.g. /etc/rancher/k3s/k3s.yaml on a k3s server). On the
	// host, this is os.ReadFile (or `sudo cat` when asRoot). On VM, this
	// is `ssh <host> sudo cat <path>` with stdout captured. On nested
	// executors, delegates through the jump via the parent's own RunSystem
	// semantics. Used by layer_artifacts.go to publish files back to the
	// operator after deploy completion.
	GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error)

	// RunCapture executes a single shell command (or short bash script) on
	// the venue and returns stdout/stderr/exit/err separately. Used by the
	// declarative test runner (testrun.go) to probe target state without
	// the streamed-output ergonomics of RunSystem/RunUser. No root
	// escalation — callers add `sudo` explicitly when needed; mirrors the
	// previous test-time Executor.Exec semantics. After the executor-
	// hierarchy cutover (2026-04), this is the single capture-output
	// method used by every probe across `ov test`, `ov image test`, and
	// `ov harness` scoring.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)

	// Kind returns a coarse classification of the venue used by the test
	// runner for reporting and skip decisions. Values:
	//   "host"      — LocalDeployExecutor (operator's machine)
	//   "container" — NestedExecutor with JumpPodmanExec / JumpDockerExec
	//   "image"     — NestedExecutor with JumpPodmanRun / JumpDockerRun
	//                 (disposable container per invocation)
	//   "vm"        — SSHExecutor or NestedExecutor with JumpSSH/JumpVirshConsole
	// Replaces the test-time Executor.Kind() method deleted in the
	// 2026-04 executor-hierarchy cutover.
	Kind() string
}

// LocalDeployExecutor implements DeployExecutor against the invoking user's shell
// + filesystem. Faithful behavior-preserving wrapper around the
// existing runSudoShell / runUserShell / BuilderRun helpers.
type LocalDeployExecutor struct{}

// VenueLocal is the stable Venue() identifier for the local host.
// Exported so install_ledger.go and tests can reference it without
// hard-coding the literal.
const VenueLocal = "local"

// Venue returns the fixed "local" identifier — commands always run on
// the invoking user's host.
func (LocalDeployExecutor) Venue() string { return VenueLocal }

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

// GetFile on the local executor is a direct filesystem read. When
// asRoot is set, the read is delegated to `sudo cat` so files with
// restricted permissions (e.g. /etc/shadow, rancher kubeconfig) can
// still be retrieved. Stdout is captured verbatim.
func (LocalDeployExecutor) GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error) {
	if opts.DryRun {
		return nil, nil
	}
	if !asRoot {
		return os.ReadFile(remotePath)
	}
	cmd := exec.CommandContext(ctx, "sudo", "cat", remotePath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapReadErr(err, remotePath, stderr.String())
	}
	return stdout.Bytes(), nil
}

// RunCapture executes a shell command on the local host and returns
// captured stdout/stderr/exit. Mirrors the deleted ContainerExecutor /
// ImageExecutor / VmTestExecutor behaviour from the pre-cutover test-
// time interface — callers (testrun.go verbs) get the same return
// shape via the unified DeployExecutor interface.
func (LocalDeployExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	return runCaptureCmd(cmd)
}

// Kind reports "host" — LocalDeployExecutor's commands run on the
// operator's machine.
func (LocalDeployExecutor) Kind() string { return "host" }

// runCaptureCmd is the shared output-capture helper. Identical behaviour
// to the pre-cutover testrun.go's runCapture (which lived on the now-
// deleted Executor interface): exit codes are NOT errors, only spawn
// failures are. Lives here so SSHExecutor / NestedExecutor implementations
// can share it without circular imports.
func runCaptureCmd(cmd *exec.Cmd) (string, string, int, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if asExitErrorDeploy(err, &ee) {
			return stdout.String(), stderr.String(), ee.ExitCode(), nil
		}
		return stdout.String(), stderr.String(), -1, err
	}
	return stdout.String(), stderr.String(), 0, nil
}

// asExitErrorDeploy unwraps to *exec.ExitError. Local copy of the helper
// in testrun.go to avoid an import cycle once the test-time Executor is
// removed.
func asExitErrorDeploy(err error, ee **exec.ExitError) bool {
	for err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			*ee = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// wrapReadErr is a small wrap helper so every executor's GetFile returns
// a consistent error shape.
func wrapReadErr(err error, path, stderr string) error {
	if stderr != "" {
		return &readFileError{path: path, stderr: stderr, cause: err}
	}
	return &readFileError{path: path, cause: err}
}

type readFileError struct {
	path   string
	stderr string
	cause  error
}

func (e *readFileError) Error() string {
	msg := "read " + e.path + ": " + e.cause.Error()
	if e.stderr != "" {
		msg += " (stderr: " + e.stderr + ")"
	}
	return msg
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
// 'foo'\”bar' trick.
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
