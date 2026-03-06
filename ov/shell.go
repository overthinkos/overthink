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
	Image     string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Workspace string   `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string   `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Command   string   `short:"c" help:"Command to execute instead of interactive shell"`
	Build     bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env       []string `short:"e" long:"env" help:"Set container env var (KEY=VALUE)"`
	EnvFile   string   `long:"env-file" help:"Load env vars from file"`
	Instance  string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	GPUFlags  `embed:""`
}

func (c *ShellCmd) Run() error {
	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemote(ref)
	}

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
	var bindMounts []ResolvedBindMount
	var security SecurityConfig
	var deployEnv []string
	var deployEnvFile string

	// Try images.yml first (existing path)
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused")
		if err != nil {
			return err
		}
		layers, err := ScanAllLayersWithConfig(dir, cfg)
		if err != nil {
			return err
		}
		volumes, err = CollectImageVolumes(cfg, layers, c.Image, resolved.Home, BindMountNames(cfg.Images[c.Image].BindMounts))
		if err != nil {
			return err
		}
		security = CollectSecurity(cfg, layers, c.Image)
		img := cfg.Images[c.Image]
		if len(img.BindMounts) > 0 {
			bindMounts = resolveBindMounts(c.Image, img.BindMounts, resolved.Home, rt.EncryptedStoragePath)
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
		uid = resolved.UID
		gid = resolved.GID
		ports = resolved.Ports
		deployEnv = img.Env
		deployEnvFile = img.EnvFile
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
			return fmt.Errorf("image %s has no embedded metadata; rebuild with latest ov", imageRef)
		}
		// Apply deploy.yml overrides
		dc, _ := LoadDeployConfig()
		MergeDeployOntoMetadata(meta, dc)

		uid = meta.UID
		gid = meta.GID
		ports = meta.Ports
		volumes = meta.Volumes
		security = meta.Security
		deployEnv = meta.Env

		// Resolve bind mounts from labels
		var deployMounts []BindMountConfig
		if dc != nil {
			if overlay, ok := dc.Images[c.Image]; ok {
				deployMounts = overlay.BindMounts
			}
		}
		bindMounts = resolveBindMountsFromLabels(c.Image, meta.BindMounts, meta.Home, rt.EncryptedStoragePath, deployMounts)

		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		}
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)
	envVars, err := ResolveEnvVars(deployEnv, deployEnvFile, absWorkspace, c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// If the container is already running, exec into it instead of starting a new one
	name := containerNameInstance(c.Image, c.Instance)
	if containerRunning(engine, name) {
		args := buildExecArgs(engine, name, uid, gid, c.Command, envVars)
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

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Image); err != nil {
		return err
	}

	args := buildShellArgs(engine, imageRef, absWorkspace, uid, gid, ports, volumes, bindMounts, gpu, c.Command, rt.BindAddress, envVars, security)

	// Find engine binary
	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}

	// Replace process with engine
	return syscall.Exec(enginePath, args, os.Environ())
}

func (c *ShellCmd) runRemote(ref string) error {
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

	ctx, err := ResolveRemoteImage(ref, c.Tag)
	if err != nil {
		return err
	}

	// Resolve env vars
	envVars, envErr := ResolveEnvVars(nil, "", absWorkspace, c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}

	// If the container is already running, exec into it
	name := containerNameInstance(ctx.ImageName, c.Instance)
	if containerRunning(engine, name) {
		args := buildExecArgs(engine, name, ctx.Resolved.UID, ctx.Resolved.GID, c.Command, envVars)
		enginePath, err := findExecutable(EngineBinary(engine))
		if err != nil {
			return err
		}
		return syscall.Exec(enginePath, args, os.Environ())
	}

	// Pull or build
	if err := ctx.PullOrBuild(rt, c.Tag, c.Build); err != nil {
		return err
	}

	volumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	bindMounts := ctx.CollectBindMounts(rt.EncryptedStoragePath)

	if err := verifyBindMounts(bindMounts, ctx.ImageName); err != nil {
		return err
	}

	args := buildShellArgs(engine, ctx.ImageRef, absWorkspace,
		ctx.Resolved.UID, ctx.Resolved.GID, ctx.Resolved.Ports,
		volumes, bindMounts, gpu, c.Command, rt.BindAddress, envVars, SecurityConfig{})

	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}
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
func buildShellArgs(engine, imageRef, workspace string, uid, gid int, ports []string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, command string, bindAddr string, envVars []string, security SecurityConfig) []string {
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
	args = append(args, SecurityArgs(security)...)
	for _, port := range ports {
		args = append(args, "-p", localizePort(port, bindAddr))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	for _, bm := range bindMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", bm.HostPath, bm.ContPath))
	}
	if engine == "podman" && len(bindMounts) > 0 {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, "--entrypoint", "bash", imageRef)
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// buildExecArgs constructs the container exec argument list for attaching to a running container.
func buildExecArgs(engine, name string, uid, gid int, command string, envVars []string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "exec", interactive,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-w", "/workspace",
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, name, "bash")
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
