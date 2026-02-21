package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// isTerminal reports whether stdout is connected to a terminal.
// Package-level var for testability.
var isTerminal = defaultIsTerminal

func defaultIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// containerRunning checks if a container with the given name is currently running.
var containerRunning = defaultContainerRunning

func defaultContainerRunning(engine, name string) bool {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "container", "inspect",
		"--format", "{{.State.Running}}", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// ShellCmd starts a bash shell in a container image
type ShellCmd struct {
	Image     string `arg:"" help:"Image name from images.yml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Command   string `short:"c" help:"Command to execute instead of interactive shell"`
	GPUFlags  `embed:""`
}

func (c *ShellCmd) Run() error {
	// Resolve workspace to absolute path (needed regardless of config source)
	absWorkspace, err := filepath.Abs(c.Workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}
	info, err := os.Stat(absWorkspace)
	if err != nil {
		return fmt.Errorf("workspace path %q: %w", absWorkspace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q is not a directory", absWorkspace)
	}

	gpu := ResolveGPU(c.GPUFlags.Mode())
	LogGPU(gpu)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.RunEngine

	var imageRef string
	var uid, gid int
	var ports []string
	var volumes []VolumeMount

	// Try images.yml first (existing path)
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused")
		if err != nil {
			return err
		}
		layers, err := ScanLayers(dir)
		if err != nil {
			return err
		}
		volumes, err = CollectImageVolumes(cfg, layers, c.Image, resolved.Home)
		if err != nil {
			return err
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
		uid = resolved.UID
		gid = resolved.GID
		ports = resolved.Ports
	} else {
		// Label path: resolve from image labels
		imageRef = resolveShellImageRef("", c.Image, c.Tag)
		if err := EnsureImage(imageRef, rt); err != nil {
			return err
		}
		meta, err := ExtractMetadata(engine, imageRef)
		if err != nil {
			return err
		}
		if meta == nil {
			return fmt.Errorf("image %s has no embedded metadata; run from project directory or rebuild with latest ov", imageRef)
		}
		uid = meta.UID
		gid = meta.GID
		ports = meta.Ports
		volumes = meta.Volumes
		// Re-resolve imageRef with registry from labels if available
		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		}
	}

	// If the container is already running, exec into it instead of starting a new one
	name := containerName(c.Image)
	if containerRunning(engine, name) {
		args := buildExecArgs(engine, name, uid, gid, c.Command)
		enginePath, err := findExecutable(EngineBinary(engine))
		if err != nil {
			return err
		}
		return syscall.Exec(enginePath, args, os.Environ())
	}

	if cfgErr != nil {
		// Already ensured above in the label path
	} else {
		if err := EnsureImage(imageRef, rt); err != nil {
			return err
		}
	}

	args := buildShellArgs(engine, imageRef, absWorkspace, uid, gid, ports, volumes, gpu, c.Command, rt.BindAddress)

	// Find engine binary
	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}

	// Replace process with engine
	return syscall.Exec(enginePath, args, os.Environ())
}

// resolveShellImageRef builds the full image reference from registry, name, and tag.
func resolveShellImageRef(registry, name, tag string) string {
	if registry != "" {
		return fmt.Sprintf("%s/%s:%s", registry, name, tag)
	}
	return fmt.Sprintf("%s:%s", name, tag)
}

// buildShellArgs constructs the container run argument list.
func buildShellArgs(engine, imageRef, workspace string, uid, gid int, ports []string, volumes []VolumeMount, gpu bool, command string, bindAddr string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "run", "--rm", interactive,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	if gpu {
		args = append(args, GPURunArgs(engine)...)
	}
	for _, port := range ports {
		args = append(args, "-p", localizePort(port, bindAddr))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	args = append(args, "--entrypoint", "bash", imageRef)
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// buildExecArgs constructs the container exec argument list for attaching to a running container.
func buildExecArgs(engine, name string, uid, gid int, command string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "exec", interactive,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-w", "/workspace",
		name,
		"bash",
	}
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// localizePort prefixes a port mapping with the given bind address.
// "80:8000" -> "<bindAddr>:80:8000", "8080" -> "<bindAddr>:8080:8080"
func localizePort(mapping string, bindAddr string) string {
	if strings.Contains(mapping, ":") {
		return bindAddr + ":" + mapping
	}
	return fmt.Sprintf("%s:%s:%s", bindAddr, mapping, mapping)
}

// findExecutable locates an executable in PATH.
func findExecutable(name string) (string, error) {
	path, err := exec_LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", name)
	}
	return path, nil
}

// exec_LookPath wraps os/exec.LookPath to avoid importing os/exec in syscall code.
var exec_LookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("executable not found: %s", name)
}
