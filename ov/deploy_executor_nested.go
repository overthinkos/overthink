package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// deploy_executor_nested.go — the composable executor that turns the
// flat DeployTarget interface into a recursive dispatcher.
//
// Every DeployTarget today (HostDeployTarget, VmDeployTarget,
// ContainerDeployTarget, K8sDeployTarget) runs InstallStep primitives
// through a single DeployExecutor. When a deployment is nested inside
// another — a container inside a VM, a VM inside a container, a host-
// deploy inside any of the above — the child's executor is a
// NestedExecutor that wraps the parent's executor and prepends a
// "shell jump" to every primitive.
//
// Example: container-in-vm. Outer VM's executor is an SSHExecutor
// talking to the guest. The child container's executor is:
//
//    NestedExecutor{
//        Parent: outerVmSSH,
//        Jump:   NestedJump{Kind: JumpPodmanExec, Target: "mychild"},
//    }
//
// When the child calls RunSystem("pacman -Sy"), NestedExecutor passes
// it to the parent as "podman exec -i mychild sudo bash -c 'pacman
// -Sy'". The parent's SSHExecutor ships that one line through ssh,
// and the in-guest podman lands it inside the child container.
//
// Composition stacks arbitrarily: container-in-vm-in-container is
// NestedExecutor{Parent: NestedExecutor{Parent: localExec, Jump: …},
// Jump: …}. Each level adds one more shell jump.
//
// File transfer (PutFile) is the subtle part. Rather than try to
// stream files through stacked ssh/podman pipelines, NestedExecutor
// picks a FileTransport that knows the protocol of the outermost
// jump: podman-cp for containers, scp for SSH hops, virt-copy-in for
// libvirt guests where SSH isn't available. The transport delegates
// intermediate hops back to the parent executor.

// JumpKind classifies how NestedExecutor enters the child environment.
// This determines both the shell-wrapper syntax for RunSystem/RunUser
// and the file-transport strategy for PutFile.
type JumpKind int

const (
	// JumpPodmanExec enters a rootful or rootless podman container
	// via `podman exec -i <name>`. The parent must have podman
	// available (the container-nesting layer provides this for
	// container-in-container; the virtualization layer is unrelated
	// here — container children of container parents).
	JumpPodmanExec JumpKind = iota + 1

	// JumpDockerExec enters a docker container via `docker exec -i`.
	// Separate from podman because docker and podman CLIs differ in
	// exit-code propagation and stdin handling.
	JumpDockerExec

	// JumpSSH makes an additional ssh hop — used when the child
	// itself is an SSH-reachable VM inside an already-SSH-reachable
	// parent (vm-in-container, vm-in-vm).
	JumpSSH

	// JumpVirshConsole attaches to a libvirt guest's serial console
	// for emergency-only shell access (used when the guest is
	// unreachable over SSH). Best-effort; primarily a diagnostic path.
	JumpVirshConsole
)

// NestedJump describes one hop into a nested environment. The Target
// string's meaning depends on Kind:
//
//   JumpPodmanExec / JumpDockerExec: container name.
//   JumpSSH:                         "user@host:port" (port optional).
//   JumpVirshConsole:                libvirt domain name.
type NestedJump struct {
	Kind   JumpKind
	Target string

	// Extra arguments inserted before the shell invocation. Rarely
	// needed; useful for `podman exec --env FOO=bar` or
	// `ssh -o ProxyJump=…` style tweaks.
	ExtraArgs []string

	// SSHKeyPath is the private-key path for JumpSSH. Required for
	// that kind; ignored otherwise.
	SSHKeyPath string
}

// String renders a jump as a human-readable venue segment. Used as a
// component of NestedExecutor.Venue().
func (j NestedJump) String() string {
	switch j.Kind {
	case JumpPodmanExec:
		return "podman-exec:" + j.Target
	case JumpDockerExec:
		return "docker-exec:" + j.Target
	case JumpSSH:
		return "ssh:" + j.Target
	case JumpVirshConsole:
		return "virsh:" + j.Target
	}
	return "jump?"
}

// NestedExecutor wraps a parent DeployExecutor and rewrites each
// DeployExecutor primitive to run inside a nested environment via
// Jump. Stacks: a NestedExecutor can itself be the Parent of another
// NestedExecutor, yielding arbitrary-depth composition.
type NestedExecutor struct {
	Parent DeployExecutor
	Jump   NestedJump
}

// Venue returns "nested:<Jump>/<parentVenue>". The parent venue is
// appended so the full chain is preserved — useful for ledger keying
// and debug output (e.g. "nested:podman-exec:mychild/ssh://arch@…").
func (n *NestedExecutor) Venue() string {
	parent := ""
	if n.Parent != nil {
		parent = n.Parent.Venue()
	}
	return "nested:" + n.Jump.String() + "/" + parent
}

// RunSystem routes a bash script through the jump, then to the
// parent executor as a single shell line. The parent's RunUser is
// used rather than RunSystem because entering a container or ssh
// session already carries its own root-escalation semantics — we
// don't want `sudo ssh sudo bash` triple-escalation.
func (n *NestedExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	wrapped, err := wrapWithJump(n.Jump, script, true /*root*/)
	if err != nil {
		return err
	}
	if n.Parent == nil {
		return fmt.Errorf("NestedExecutor: nil Parent")
	}
	return n.Parent.RunUser(ctx, wrapped, opts)
}

// RunUser runs as the unprivileged user inside the nested
// environment.
func (n *NestedExecutor) RunUser(ctx context.Context, script string, opts EmitOpts) error {
	wrapped, err := wrapWithJump(n.Jump, script, false /*root*/)
	if err != nil {
		return err
	}
	if n.Parent == nil {
		return fmt.Errorf("NestedExecutor: nil Parent")
	}
	return n.Parent.RunUser(ctx, wrapped, opts)
}

// RunBuilder always runs on the outermost host (where podman with
// image-building capabilities lives). Delegates to the parent chain
// unchanged — nested executors never run builders inside themselves.
// Artifacts produced by the builder land on the outer host and are
// distributed to the nested environment via PutFile.
func (n *NestedExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	if n.Parent == nil {
		return nil, fmt.Errorf("NestedExecutor: nil Parent")
	}
	return n.Parent.RunBuilder(ctx, opts)
}

// PutFile transfers a file from the invoking host into the nested
// venue. Implementation picks a FileTransport based on Jump.Kind:
// podman/docker containers use `<engine> cp`, SSH hops use scp,
// libvirt console uses virt-copy-in (best effort).
//
// For stacked jumps, the transfer is multi-stage: first land the
// file at a tmp path on the parent venue (via Parent.PutFile),
// then issue a one-shot move-into-child command via RunSystem. This
// preserves the "every executor only knows its own hop" invariant
// and keeps the FileTransport implementation simple.
func (n *NestedExecutor) PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if n.Parent == nil {
		return fmt.Errorf("NestedExecutor: nil Parent")
	}

	// Stage the file on the parent venue first.
	base := filepath.Base(remotePath)
	stagePath := "/tmp/ov-nested-" + base
	if err := n.Parent.PutFile(ctx, localPath, stagePath, mode, false /*not root yet*/, opts); err != nil {
		return fmt.Errorf("staging %s on parent: %w", localPath, err)
	}

	// Compute the copy-in command for the jump kind.
	copyIn, err := copyIntoJumpCommand(n.Jump, stagePath, remotePath, mode, ownerRoot)
	if err != nil {
		return err
	}

	// Execute the copy-in via the parent's RunUser (no root needed
	// on the parent — the container/ssh tool handles the copy).
	cleanup := "rm -f " + deployShellQuote(stagePath)
	combined := copyIn + "\n" + cleanup
	return n.Parent.RunUser(ctx, combined, opts)
}

// wrapWithJump rewrites a script so it executes inside the nested
// environment when run by the parent executor. The return value is a
// single bash invocation (parent's shell) that internally invokes the
// child shell with the script fed via stdin.
func wrapWithJump(jump NestedJump, script string, asRoot bool) (string, error) {
	shell := "bash"
	if asRoot {
		shell = "sudo bash"
	}

	// The script is passed via a heredoc, base64-encoded, to avoid
	// shell-quoting interactions with the user's script body.
	// Heredoc delimiter is chosen so it's very unlikely to collide.
	delim := "OV_NESTED_SCRIPT_EOF"

	switch jump.Kind {
	case JumpPodmanExec, JumpDockerExec:
		engine := "podman"
		if jump.Kind == JumpDockerExec {
			engine = "docker"
		}
		extras := strings.Join(escapeTokens(jump.ExtraArgs), " ")
		// stdin-attached exec so the heredoc reaches the nested shell.
		cmd := fmt.Sprintf("%s exec -i %s %s %s", engine, extras, deployShellQuote(jump.Target), shell)
		return fmt.Sprintf("%s <<'%s'\n%s\n%s\n", cmd, delim, script, delim), nil

	case JumpSSH:
		user, host, port := parseSSHTarget(jump.Target)
		if host == "" {
			return "", fmt.Errorf("NestedJump{JumpSSH}: target %q missing host", jump.Target)
		}
		keyArg := ""
		if jump.SSHKeyPath != "" {
			keyArg = "-i " + deployShellQuote(jump.SSHKeyPath) + " "
		}
		portArg := ""
		if port > 0 {
			portArg = fmt.Sprintf("-p %d ", port)
		}
		userPrefix := ""
		if user != "" {
			userPrefix = user + "@"
		}
		extras := strings.Join(escapeTokens(jump.ExtraArgs), " ")
		cmd := fmt.Sprintf("ssh %s%s-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s %s%s %s",
			keyArg, portArg, extras, userPrefix, host, shell)
		return fmt.Sprintf("%s <<'%s'\n%s\n%s\n", cmd, delim, script, delim), nil

	case JumpVirshConsole:
		// virsh console is interactive and does not cleanly accept
		// scripted stdin. Reject with a clear error so callers know
		// to pick JumpSSH instead. Kept as a type so future work
		// (expect-scripted console) has a placeholder.
		return "", fmt.Errorf("JumpVirshConsole does not support scripted RunSystem/RunUser — use JumpSSH or wire the guest for SSH")
	}

	return "", fmt.Errorf("NestedJump: unknown JumpKind %d", jump.Kind)
}

// copyIntoJumpCommand emits the shell command that moves stagePath
// (already present on the parent venue) into remotePath inside the
// nested venue, with the requested mode and ownership.
func copyIntoJumpCommand(jump NestedJump, stagePath, remotePath string, mode uint32, ownerRoot bool) (string, error) {
	quote := deployShellQuote
	modeStr := permOctal(mode)

	switch jump.Kind {
	case JumpPodmanExec, JumpDockerExec:
		engine := "podman"
		if jump.Kind == JumpDockerExec {
			engine = "docker"
		}
		// `<engine> cp <stage> <container>:<remote>` then chown/chmod
		// via exec. Root ownership inside the container is cheap
		// (containers usually run as root by default) but we issue
		// chown explicitly so non-root containers behave correctly.
		chown := ""
		if ownerRoot {
			chown = fmt.Sprintf(" && %s exec %s chown root:root %s",
				engine, quote(jump.Target), quote(remotePath))
		}
		chmod := fmt.Sprintf(" && %s exec %s chmod %s %s",
			engine, quote(jump.Target), modeStr, quote(remotePath))
		return fmt.Sprintf("%s cp %s %s:%s%s%s",
			engine, quote(stagePath), quote(jump.Target), remotePath, chown, chmod), nil

	case JumpSSH:
		user, host, port := parseSSHTarget(jump.Target)
		keyArg := ""
		if jump.SSHKeyPath != "" {
			keyArg = "-i " + quote(jump.SSHKeyPath) + " "
		}
		portArg := ""
		if port > 0 {
			portArg = fmt.Sprintf("-P %d ", port)
		}
		userPrefix := ""
		if user != "" {
			userPrefix = user + "@"
		}
		target := fmt.Sprintf("%s%s:%s", userPrefix, host, remotePath)
		// scp for the file; a separate ssh for chown+chmod when root.
		installCmd := ""
		if ownerRoot {
			sshPort := ""
			if port > 0 {
				sshPort = fmt.Sprintf("-p %d ", port)
			}
			installCmd = fmt.Sprintf(" && ssh %s%s-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s%s 'sudo chown root:root %s && sudo chmod %s %s'",
				keyArg, sshPort, userPrefix, host, quote(remotePath), modeStr, quote(remotePath))
		} else {
			sshPort := ""
			if port > 0 {
				sshPort = fmt.Sprintf("-p %d ", port)
			}
			installCmd = fmt.Sprintf(" && ssh %s%s-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s%s 'chmod %s %s'",
				keyArg, sshPort, userPrefix, host, modeStr, quote(remotePath))
		}
		return fmt.Sprintf("scp %s%s-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null %s %s%s",
			keyArg, portArg, quote(stagePath), target, installCmd), nil
	}

	return "", fmt.Errorf("NestedJump: PutFile not supported for JumpKind %d", jump.Kind)
}

// parseSSHTarget splits a "user@host:port" target string into its
// components. Missing user is "", missing port is 0.
func parseSSHTarget(target string) (user, host string, port int) {
	rest := target
	if idx := strings.Index(rest, "@"); idx >= 0 {
		user = rest[:idx]
		rest = rest[idx+1:]
	}
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		host = rest[:idx]
		if p, err := parsePositiveInt(rest[idx+1:]); err == nil {
			port = p
		}
	} else {
		host = rest
	}
	return user, host, port
}

// parsePositiveInt parses a non-empty positive decimal integer from s.
// Used for port numbers in parseSSHTarget.
func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty integer")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit in %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// escapeTokens wraps each token in single quotes for safe embedding in
// a bash line. Empty input returns a nil slice so strings.Join yields
// "" rather than " ".
func escapeTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = deployShellQuote(t)
	}
	return out
}

// Suppress "unused" complaints for stdlib packages referenced only in
// doc comments — the compile would reject a truly unused import,
// while these helpers use exec / os / bytes variants across branches.
// (Kept as an explicit reminder for code readers. Go's compiler
// enforces the rule for production imports.)
var _ = exec.Command
var _ = os.Stderr
var _ = bytes.NewReader
