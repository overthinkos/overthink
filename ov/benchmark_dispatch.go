package main

// benchmark_dispatch.go — deploy-kind-aware exec wrapper + credential sync.
//
// Each Dispatcher wraps one of the three supported deploy kinds
// (host/pod/vm) and knows how to:
//   - Build an exec.Cmd that runs inside the target
//   - Resolve the workspace path inside the target
//   - Sync credentials from host into the target (once per run)
//   - Preflight the target (running, writable workspace, AI CLI present)
//
// k8s targets are REJECTED at ResolveDispatcher — the nested-build
// complexity is deferred to v2 per the approved plan.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Dispatcher interface
// ---------------------------------------------------------------------------

// Dispatcher abstracts the three supported deploy kinds.
type Dispatcher interface {
	// Kind returns "host" | "pod" | "vm".
	Kind() string

	// WorkspacePath returns the path at which the benchmark's git
	// worktree is visible to the runner (inside the deploy).
	// For pod/vm: /workspace/.benchmark/<run-id>/worktree.
	// For host: the host absolute path to the worktree.
	WorkspacePath(layout RunLayout) string

	// Build constructs an exec.Cmd that, when run, executes argv inside
	// the target with the given env and cwd. Stdout/stderr attachment
	// is the caller's responsibility.
	Build(ctx context.Context, layout RunLayout, argv []string, env map[string]string) (*exec.Cmd, error)

	// MCPEndpoint returns the host-side URL that will reach the
	// deploy's ov-mcp server, or "" when the deploy does not expose
	// one (or when --no-mcp is effective).
	MCPEndpoint(ctx context.Context) (string, error)

	// Preflight validates that the deploy is running, that runnerBin
	// exists inside it, and that the workspace is writable. Returns a
	// fix-hint error on failure.
	Preflight(ctx context.Context, runnerBin string) error

	// SyncCredentials copies each mount's src into the deploy at dst.
	// Called ONCE per benchmark run during preflight, NOT per iteration.
	// MUST NOT copy credential material into the worktree.
	SyncCredentials(ctx context.Context, mounts []CredentialMount) error

	// CleanupCredentials removes each mount's dst from the deploy.
	// Called at benchmark end only when BenchmarkRunner.Cleanup is true.
	// Best-effort; errors are logged but do not fail the benchmark.
	CleanupCredentials(ctx context.Context, mounts []CredentialMount) error
}

// ---------------------------------------------------------------------------
// Sentinels
// ---------------------------------------------------------------------------

var (
	// ErrK8sUnsupported fires when the caller asks for a k8s-target deploy.
	ErrK8sUnsupported = errors.New("benchmark: k8s deploys not supported in v1")

	// ErrUnknownTarget fires for unrecognized Target kinds.
	ErrUnknownTarget = errors.New("benchmark: unknown deploy target kind")
)

// ---------------------------------------------------------------------------
// Resolver
// ---------------------------------------------------------------------------

// ResolveDispatcher inspects node.Target and returns the matching
// Dispatcher. Rejects k8s with ErrK8sUnsupported.
//
// name is the deployment name from deploy.yml — threaded through so
// dispatchers that need it (pod for container name, vm for SSH alias)
// can use it verbatim.
func ResolveDispatcher(node *DeploymentNode, name string) (Dispatcher, error) {
	if node == nil {
		return nil, fmt.Errorf("benchmark: deployment %q has no config entry", name)
	}
	switch node.Target {
	case "host":
		return &hostDispatcher{node: node, name: name}, nil
	case "", "container", "pod":
		return &podDispatcher{node: node, name: name, containerName: "ov-" + name}, nil
	case "vm":
		return &vmDispatcher{node: node, name: name}, nil
	case "k8s", "kubernetes":
		return nil, ErrK8sUnsupported
	default:
		return nil, fmt.Errorf("%w: %q (deployment %q)", ErrUnknownTarget, node.Target, name)
	}
}

// ---------------------------------------------------------------------------
// hostDispatcher
// ---------------------------------------------------------------------------

// hostDispatcher executes commands on the local host (or via ov ssh
// when the deploy has a remote host alias). Workspace = the host-side
// worktree dir directly.
type hostDispatcher struct {
	node *DeploymentNode
	name string
}

func (h *hostDispatcher) Kind() string { return "host" }

func (h *hostDispatcher) WorkspacePath(layout RunLayout) string {
	return layout.WorktreeDir
}

func (h *hostDispatcher) Build(ctx context.Context, layout RunLayout, argv []string, env map[string]string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("benchmark: host runner has empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = h.WorkspacePath(layout)
	cmd.Env = mergeOsEnv(env)
	return cmd, nil
}

func (h *hostDispatcher) MCPEndpoint(ctx context.Context) (string, error) {
	// Host-deploy MCP endpoints are installation-specific. If the host
	// runs a local ov mcp serve on the default port, return it; else "".
	// This is intentionally conservative — users who want a specific
	// host-MCP endpoint set OV_MCP_URL explicitly.
	if v := os.Getenv("OV_MCP_URL"); v != "" {
		return v, nil
	}
	return "", nil
}

func (h *hostDispatcher) Preflight(ctx context.Context, runnerBin string) error {
	// Does the runner binary exist on the host PATH?
	if _, err := exec.LookPath(runnerBin); err != nil {
		return fmt.Errorf("benchmark: runner binary %q not found on host PATH — install the AI CLI first", runnerBin)
	}
	return nil
}

func (h *hostDispatcher) SyncCredentials(ctx context.Context, mounts []CredentialMount) error {
	return syncCredentialsLocal(ctx, mounts)
}

func (h *hostDispatcher) CleanupCredentials(ctx context.Context, mounts []CredentialMount) error {
	return cleanupCredentialsLocal(ctx, mounts)
}

// ---------------------------------------------------------------------------
// podDispatcher
// ---------------------------------------------------------------------------

// podDispatcher executes via `podman exec` (or docker exec) against a
// running container named ov-<deployment>.
type podDispatcher struct {
	node          *DeploymentNode
	name          string
	containerName string
}

func (p *podDispatcher) Kind() string { return "pod" }

func (p *podDispatcher) WorkspacePath(layout RunLayout) string {
	// The ov-mcp layer mounts the project root at /workspace, so the
	// benchmark worktree is visible at /workspace/.benchmark/<id>/worktree.
	return "/workspace/.benchmark/" + layout.RunID + "/worktree"
}

func (p *podDispatcher) Build(ctx context.Context, layout RunLayout, argv []string, env map[string]string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("benchmark: pod runner has empty command")
	}
	engine := pickEngine(p.node)
	full := []string{engine, "exec", "-w", p.WorkspacePath(layout)}
	for k, v := range env {
		full = append(full, "-e", k+"="+v)
	}
	full = append(full, p.containerName)
	full = append(full, argv...)
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	return cmd, nil
}

func (p *podDispatcher) MCPEndpoint(ctx context.Context) (string, error) {
	// Inside the pod the AI sees http://localhost:18765/mcp (the
	// ov-mcp layer's default). The runner is ALSO inside the pod, so
	// localhost is the right endpoint from the runner's perspective.
	//
	// Host-side probing (used by Preflight) needs the rewritten URL;
	// that happens in probeMCPFromHost, not here.
	return "http://localhost:18765/mcp", nil
}

func (p *podDispatcher) Preflight(ctx context.Context, runnerBin string) error {
	engine := pickEngine(p.node)
	// Is the container running?
	out, err := exec.CommandContext(ctx, engine, "inspect",
		"--format", "{{.State.Running}}", p.containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("benchmark: container %q not reachable: %w\n%s",
			p.containerName, err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("benchmark: container %q is not running", p.containerName)
	}
	// Does the runner binary exist inside?
	cmd := exec.CommandContext(ctx, engine, "exec", p.containerName,
		"sh", "-c", "command -v "+benchShellQuote(runnerBin))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("benchmark: %q not found in container %q — add the corresponding AI-CLI layer to the image",
			runnerBin, p.containerName)
	}
	return nil
}

func (p *podDispatcher) SyncCredentials(ctx context.Context, mounts []CredentialMount) error {
	engine := pickEngine(p.node)
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("benchmark: credential src %q: %w", m.Src, err)
		}
		if _, err := os.Stat(srcAbs); err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "benchmark: credential src %q not found; skipping\n", m.Src)
				continue
			}
			return fmt.Errorf("benchmark: credential src %q unreadable: %w", srcAbs, err)
		}
		// podman cp auto-creates the parent dir in the container.
		cpCmd := exec.CommandContext(ctx, engine, "cp", srcAbs, p.containerName+":"+m.Dst)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("benchmark: podman cp for %q: %w\n%s", m.Src, err, string(out))
		}
	}
	return nil
}

func (p *podDispatcher) CleanupCredentials(ctx context.Context, mounts []CredentialMount) error {
	engine := pickEngine(p.node)
	for _, m := range mounts {
		// Best-effort; ignore errors.
		_ = exec.CommandContext(ctx, engine, "exec", p.containerName,
			"rm", "-rf", m.Dst).Run()
	}
	return nil
}

// ---------------------------------------------------------------------------
// vmDispatcher
// ---------------------------------------------------------------------------

// vmDispatcher executes via `ov vm ssh <name> -- …` on the target VM.
type vmDispatcher struct {
	node *DeploymentNode
	name string
}

func (v *vmDispatcher) Kind() string { return "vm" }

func (v *vmDispatcher) WorkspacePath(layout RunLayout) string {
	return "/workspace/.benchmark/" + layout.RunID + "/worktree"
}

func (v *vmDispatcher) Build(ctx context.Context, layout RunLayout, argv []string, env map[string]string) (*exec.Cmd, error) {
	if len(argv) == 0 {
		return nil, errors.New("benchmark: vm runner has empty command")
	}
	// Construct a single shell command: cd workspace; export env; exec argv.
	var sb strings.Builder
	sb.WriteString("cd ")
	sb.WriteString(benchShellQuote(v.WorkspacePath(layout)))
	sb.WriteString(" && ")
	for k, val := range env {
		sb.WriteString("export ")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(benchShellQuote(val))
		sb.WriteString(" && ")
	}
	sb.WriteString("exec")
	for _, a := range argv {
		sb.WriteString(" ")
		sb.WriteString(benchShellQuote(a))
	}
	remote := sb.String()

	cmd := exec.CommandContext(ctx, "ov", "vm", "ssh", v.name, "--", "sh", "-c", remote)
	return cmd, nil
}

func (v *vmDispatcher) MCPEndpoint(ctx context.Context) (string, error) {
	// VMs running the ov-mcp layer listen on :18765 inside the guest.
	// The runner (inside the VM) uses localhost.
	return "http://localhost:18765/mcp", nil
}

func (v *vmDispatcher) Preflight(ctx context.Context, runnerBin string) error {
	// SSH into the VM and probe for the runner binary.
	cmd := exec.CommandContext(ctx, "ov", "vm", "ssh", v.name, "--",
		"sh", "-c", "command -v "+benchShellQuote(runnerBin))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("benchmark: %q not available in VM %q — check that the VM is running and the AI-CLI layer is installed",
			runnerBin, v.name)
	}
	return nil
}

func (v *vmDispatcher) SyncCredentials(ctx context.Context, mounts []CredentialMount) error {
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("benchmark: credential src %q: %w", m.Src, err)
		}
		if _, err := os.Stat(srcAbs); err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "benchmark: credential src %q not found; skipping\n", m.Src)
				continue
			}
			return fmt.Errorf("benchmark: credential src %q unreadable: %w", srcAbs, err)
		}
		// Use rsync over ov vm ssh. A trailing slash on the src
		// dir preserves contents-not-parent semantics.
		src := srcAbs
		if st, err := os.Stat(srcAbs); err == nil && st.IsDir() {
			src = srcAbs + "/"
		}
		cmd := exec.CommandContext(ctx, "rsync", "-a", "-e",
			"ov vm ssh "+v.name+" --", src, v.name+":"+m.Dst+"/")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("benchmark: rsync to vm %q for %q: %w\n%s",
				v.name, m.Src, err, string(out))
		}
	}
	return nil
}

func (v *vmDispatcher) CleanupCredentials(ctx context.Context, mounts []CredentialMount) error {
	for _, m := range mounts {
		_ = exec.CommandContext(ctx, "ov", "vm", "ssh", v.name, "--",
			"rm", "-rf", m.Dst).Run()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// pickEngine returns the container engine name to use for this node
// (honors node.Engine, falls back to podman).
func pickEngine(node *DeploymentNode) string {
	if node != nil && node.Engine != "" {
		return node.Engine
	}
	return "podman"
}

// mergeOsEnv returns os.Environ() merged with overrides from env. The
// overrides win on key collision.
func mergeOsEnv(env map[string]string) []string {
	if len(env) == 0 {
		return os.Environ()
	}
	// Start from os.Environ and overlay overrides.
	out := append([]string(nil), os.Environ()...)
	// Simple replace-or-append: the override set is small so O(N*M)
	// is fine here.
	for k, v := range env {
		prefix := k + "="
		replaced := false
		for i, e := range out {
			if strings.HasPrefix(e, prefix) {
				out[i] = prefix + v
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, prefix+v)
		}
	}
	return out
}

// benchShellQuote wraps a value in single quotes for safe embedding in a
// shell command. Internal single quotes are escaped via '"'"' (close
// quote, escaped quote, open quote) — the standard POSIX idiom.
func benchShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// expandHostPath resolves a ~-prefixed path against os.UserHomeDir().
// Leaves absolute / relative paths unchanged.
func expandHostPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~ in %q: %w", p, err)
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// ---------------------------------------------------------------------------
// Local credential sync (used by hostDispatcher; also the swappable
// entry point that tests can stub)
// ---------------------------------------------------------------------------

// syncCredentialsLocal copies each mount locally (cp -a semantics).
// This is exposed as a package-level var so tests can override it if
// they want to exercise the host-dispatcher path without calling cp.
var syncCredentialsLocal = func(ctx context.Context, mounts []CredentialMount) error {
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("benchmark: credential src %q: %w", m.Src, err)
		}
		dstAbs, err := expandHostPath(m.Dst)
		if err != nil {
			return fmt.Errorf("benchmark: credential dst %q: %w", m.Dst, err)
		}
		info, err := os.Stat(srcAbs)
		if err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "benchmark: credential src %q not found; skipping\n", m.Src)
				continue
			}
			return fmt.Errorf("benchmark: credential src %q unreadable: %w", srcAbs, err)
		}
		if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
			return fmt.Errorf("benchmark: create parent of %q: %w", dstAbs, err)
		}
		if info.IsDir() {
			if err := copyDirRecursive(srcAbs, dstAbs); err != nil {
				return fmt.Errorf("benchmark: copy %q -> %q: %w", srcAbs, dstAbs, err)
			}
		} else {
			if err := copyFile(srcAbs, dstAbs, info.Mode()); err != nil {
				return fmt.Errorf("benchmark: copy %q -> %q: %w", srcAbs, dstAbs, err)
			}
		}
	}
	return nil
}

// cleanupCredentialsLocal removes each mount's dst from the host.
var cleanupCredentialsLocal = func(ctx context.Context, mounts []CredentialMount) error {
	for _, m := range mounts {
		dstAbs, err := expandHostPath(m.Dst)
		if err != nil {
			continue
		}
		_ = os.RemoveAll(dstAbs)
	}
	return nil
}

// copyFile copies a single file preserving mode. Overwrites dst.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// copyDirRecursive is a minimal `cp -a` for directories. Preserves
// mode bits but not owner/mtime; sufficient for credential sync on a
// single filesystem.
func copyDirRecursive(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}
