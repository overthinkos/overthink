package main

// builder_run.go — `podman run <builder>` wrapper for HostDeployTarget.
//
// The host target delegates compile-needing steps (pixi/npm/cargo/aur)
// to the existing multi-stage builder images. BuilderRun spawns the
// builder container with the correct flags:
//
//   --user $(id -u):$(id -g)   match host uid/gid so artifacts are owned
//                               correctly at bind-mount time
//   -v $HOME/.pixi:$HOME/.pixi:rw
//   -v $HOME/.cargo:$HOME/.cargo:rw
//   -v $HOME/.npm-global:$HOME/.npm-global:rw
//   -v $HOME/.cache/ov:$HOME/.cache/ov:rw
//   -v <layer-dir>:/work:ro
//   -e HOME=$HOME
//   -e PIXI_CACHE_DIR=$HOME/.cache/ov/pixi
//   -e NPM_CONFIG_PREFIX=$HOME/.npm-global
//   -e CARGO_HOME=$HOME/.cargo
//   -w /work
//
// The invoked shell command comes from the builder's stage_template
// (with {{.Home}} resolved to the host user's home).
//
// For aur specifically, the bind-mount set is different: the output
// /tmp/aur-pkgs/ is mounted to a tmpdir so the caller can run
// `sudo pacman -U <tmpdir>/*.pkg.tar.zst` on the host afterwards.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuilderRunOpts describes one invocation. ScriptBody is the rendered
// stage template content (already {{.Home}}-substituted).
type BuilderRunOpts struct {
	Engine       string // "podman" or "docker"; default "podman"
	BuilderImage string // full image ref, e.g. "ghcr.io/overthinkos/fedora-builder:latest"
	LayerDir     string // absolute path to layer source (bind-mounted as /work)
	ScriptBody   string // shell script contents to pass to bash -c

	// Bind-mounts. Keys are container paths; values are host paths.
	// Set by the caller based on the builder kind — pixi/npm/cargo use
	// the same HOME-subdir layout, aur uses a tmpdir for /tmp/aur-pkgs.
	BindMounts map[string]string

	// Env vars to set inside the container (in addition to HOME).
	Env map[string]string

	// HostHome is the invoking user's absolute home dir. Set via HOME=
	// inside the container so path-baking (pixi shebangs, etc.) resolves
	// to a path that's valid both inside (via bind-mount) and outside.
	HostHome string

	// DryRun returns the command line that would run without executing.
	// Used for --dry-run deploy.
	DryRun bool
}

// BuilderRun runs the configured builder container. Returns the
// command's combined stdout+stderr on success; on failure, returns the
// output plus the exec error.
func BuilderRun(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	engine := opts.Engine
	if engine == "" {
		engine = "podman"
	}
	args := buildBuilderRunArgs(opts)

	if opts.DryRun {
		// Build a human-readable command line. Log-only; never touch the
		// host filesystem.
		shellified := engine + " " + shellJoin(args)
		fmt.Fprintln(os.Stderr, "[dry-run] "+shellified)
		fmt.Fprintln(os.Stderr, "[dry-run] script body:")
		for _, line := range strings.Split(opts.ScriptBody, "\n") {
			fmt.Fprintln(os.Stderr, "[dry-run]   "+line)
		}
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, engine, args...)
	cmd.Stdin = strings.NewReader(opts.ScriptBody)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("BuilderRun: %w\nOutput:\n%s", err, out)
	}
	return out, nil
}

// buildBuilderRunArgs assembles the podman/docker run argv. Extracted
// for unit-testability — we assert the produced argv rather than
// actually spawning a container in tests.
func buildBuilderRunArgs(opts BuilderRunOpts) []string {
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{"run", "--rm",
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-i", // bash -c reads the script from stdin
	}

	// Deterministic bind-mount ordering so dry-run output is
	// reproducible. Sorted by container path.
	containerPaths := make([]string, 0, len(opts.BindMounts))
	for cpath := range opts.BindMounts {
		containerPaths = append(containerPaths, cpath)
	}
	sortStrings(containerPaths)
	for _, cpath := range containerPaths {
		hpath := opts.BindMounts[cpath]
		args = append(args, "-v", fmt.Sprintf("%s:%s:rw", hpath, cpath))
	}

	// Layer source always mounted read-only at /work.
	if opts.LayerDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/work:ro", opts.LayerDir))
	}

	// HOME + extra env vars.
	if opts.HostHome != "" {
		args = append(args, "-e", "HOME="+opts.HostHome)
	}
	envKeys := make([]string, 0, len(opts.Env))
	for k := range opts.Env {
		envKeys = append(envKeys, k)
	}
	sortStrings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+opts.Env[k])
	}

	args = append(args, "-w", "/work")
	args = append(args, opts.BuilderImage, "bash", "-s")
	return args
}

// ---------------------------------------------------------------------------
// Helpers for building the standard bind-mount sets.
// ---------------------------------------------------------------------------

// UserScopeBindMounts returns the standard subdirs each user-scope
// builder needs bind-mounted: $HOME/.pixi, $HOME/.cargo,
// $HOME/.npm-global, $HOME/.cache/ov. Ensures the source dirs exist on
// the host (empty dirs — so podman's --mount doesn't reject them).
func UserScopeBindMounts(hostHome string) (map[string]string, error) {
	if hostHome == "" {
		return nil, fmt.Errorf("UserScopeBindMounts: empty HOME")
	}
	subdirs := []string{".pixi", ".cargo", ".npm-global", ".cache/ov"}
	out := make(map[string]string, len(subdirs))
	for _, sub := range subdirs {
		p := filepath.Join(hostHome, sub)
		if err := os.MkdirAll(p, 0755); err != nil {
			return nil, fmt.Errorf("UserScopeBindMounts mkdir %s: %w", p, err)
		}
		out[p] = p // same path inside and outside the container
	}
	return out, nil
}

// UserScopeEnv returns the env-var set that pixi/npm/cargo need to
// write into the bind-mounted directories rather than the builder's
// own home paths.
func UserScopeEnv(hostHome string) map[string]string {
	return map[string]string{
		"PIXI_CACHE_DIR":    filepath.Join(hostHome, ".cache", "ov", "pixi"),
		"RATTLER_CACHE_DIR": filepath.Join(hostHome, ".cache", "ov", "rattler"),
		"NPM_CONFIG_PREFIX": filepath.Join(hostHome, ".npm-global"),
		"CARGO_HOME":        filepath.Join(hostHome, ".cargo"),
	}
}

// shellJoin renders argv as a shell-safe string for log/dry-run output.
// Not security-sensitive (we aren't actually executing this via shell;
// exec.CommandContext is used).
func shellJoin(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(a, " \t\n\"'$") {
			b.WriteByte('\'')
			b.WriteString(strings.ReplaceAll(a, "'", `'\''`))
			b.WriteByte('\'')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}
