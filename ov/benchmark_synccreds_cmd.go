package main

// benchmark_synccreds_cmd.go — host-side `ov benchmark sync-credentials <pod>`.
//
// One-shot copy of AI-CLI auth material from the host's $HOME into
// the pod's $HOME. Split out from the old per-run sync path so:
//   - Credential rotation can happen without firing a benchmark run
//   - The persistent pod retains auth across iterations and across runs
//
// Reads CredentialMount entries from overthink.yml's benchmark.runners[*].credentials.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BenchmarkSyncCredentialsCmd copies one (or all) runner's credential
// mounts into the pod via `podman cp`.
type BenchmarkSyncCredentialsCmd struct {
	Pod    string `arg:"" help:"Pod deployment name (e.g., bench-pod)"`
	Runner string `help:"Sync only this runner's credentials (default: every configured runner)"`
}

// Run executes the credential sync.
func (c *BenchmarkSyncCredentialsCmd) Run() error {
	ctx := context.Background()

	projectDir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadBenchmarkConfig(projectDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("benchmark sync-credentials: overthink.yml has no benchmark: section")
	}

	containerName := "ov-" + c.Pod

	// Confirm the container is running before we start copying.
	if err := podRunning(ctx, containerName); err != nil {
		return err
	}

	var runners []*BenchmarkRunner
	if c.Runner != "" {
		r, err := ResolveRunner(cfg, c.Runner)
		if err != nil {
			return err
		}
		runners = append(runners, r)
	} else {
		for i := range cfg.Runners {
			runners = append(runners, &cfg.Runners[i])
		}
	}

	for _, runner := range runners {
		if len(runner.Credentials) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "benchmark sync-credentials: %s -> %s (%d mount(s))\n", runner.Name, c.Pod, len(runner.Credentials))
		if err := syncCredentialsToPod(ctx, containerName, runner.Credentials); err != nil {
			return fmt.Errorf("runner %s: %w", runner.Name, err)
		}
	}
	return nil
}

// podRunning errors if the container is not present + running.
func podRunning(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "podman", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("benchmark: container %q not reachable: %w\n%s", containerName, err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("benchmark: container %q is not running — `ov start %s` first",
			containerName, strings.TrimPrefix(containerName, "ov-"))
	}
	return nil
}

// syncCredentialsToPod copies each mount's src into the running pod
// at dst via `podman cp`. ~ in dst is expanded against the pod's $HOME.
func syncCredentialsToPod(ctx context.Context, containerName string, mounts []CredentialMount) error {
	var podHome string
	for _, m := range mounts {
		srcAbs, err := expandHostPath(m.Src)
		if err != nil {
			return fmt.Errorf("credential src %q: %w", m.Src, err)
		}
		if _, err := os.Stat(srcAbs); err != nil {
			if os.IsNotExist(err) && m.Optional {
				fmt.Fprintf(os.Stderr, "benchmark: credential src %q not found; skipping\n", m.Src)
				continue
			}
			return fmt.Errorf("credential src %q unreadable: %w", srcAbs, err)
		}
		dst := m.Dst
		if strings.HasPrefix(dst, "~") {
			if podHome == "" {
				h, err := resolveContainerHome(ctx, "podman", containerName)
				if err != nil {
					return fmt.Errorf("resolve pod $HOME for dst %q: %w", m.Dst, err)
				}
				podHome = h
			}
			dst = substTilde(dst, podHome)
		}
		parent := filepath.Dir(dst)
		if parent != "" && parent != "/" {
			mkCmd := exec.CommandContext(ctx, "podman", "exec", containerName, "mkdir", "-p", parent)
			if out, err := mkCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("mkdir -p %q in pod: %w\n%s", parent, err, string(out))
			}
		}
		cpCmd := exec.CommandContext(ctx, "podman", "cp", srcAbs, containerName+":"+dst)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("podman cp %q -> %q: %w\n%s", m.Src, dst, err, string(out))
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers retained from the old benchmark_dispatch.go (which is being deleted)
// ---------------------------------------------------------------------------

// expandHostPath resolves a ~-prefixed path against os.UserHomeDir().
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

// substTilde replaces a leading ~ with home (in-memory; no I/O).
func substTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// resolveContainerHome returns the in-pod $HOME for the running uid.
// Swappable as a package-level var for tests.
var resolveContainerHome = func(ctx context.Context, engine, container string) (string, error) {
	cmd := exec.CommandContext(ctx, engine, "exec", container,
		"sh", "-c", "getent passwd $(id -u) | cut -d: -f6")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("getent passwd in %s: %w", container, err)
	}
	home := strings.TrimSpace(string(out))
	if home == "" {
		return "", fmt.Errorf("empty $HOME for container %s", container)
	}
	return home, nil
}
