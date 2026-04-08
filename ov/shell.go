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

// isInsideContainer returns true if ov is running inside a container.
func isInsideContainer() bool {
	if _, err := os.Stat("/.containerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
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

// forceTTY overrides isTerminal() when set to true (e.g., by --tty flag).
// Allows automation tools like Claude Code to force TTY allocation.
var forceTTY bool

// ShellCmd starts a bash shell in a container image
type ShellCmd struct {
	Image      string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Tag        string   `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Command   string   `short:"c" help:"Command to execute instead of interactive shell"`
	Build      bool     `long:"build" help:"Force local build instead of pulling from registry"`
	TTY        bool     `long:"tty" help:"Force TTY allocation (for automation tools that lack a real terminal)"`
	Env        []string `short:"e" long:"env" help:"Set container env var (KEY=VALUE)"`
	EnvFile    string   `long:"env-file" help:"Load env vars from file"`
	Instance   string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	VolumeFlag []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind       []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	AutoDetectFlags `embed:""`
}

func (c *ShellCmd) Run() error {
	// Set global forceTTY so buildShellArgs/buildExecArgs pick it up
	forceTTY = c.TTY

	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemote(ref)
	}

	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.RunEngine

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	var imageRef string
	var uid, gid int
	var home string
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount
	var security SecurityConfig
	var network string
	var deployEnv []string
	var deployEnvFile string

	// Load deploy.yml for volume backing config
	dc, _ := LoadDeployConfig()
	var deployVolumes []DeployVolumeConfig
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployVolumes = overlay.Volumes
		}
	}

	// Try images.yml first (existing path)
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused", dir)
		if err != nil {
			return err
		}
		layers, err := ScanAllLayersWithConfig(dir, cfg)
		if err != nil {
			return err
		}
		// Resolve per-image engine
		engine = ResolveImageEngine(cfg, layers, c.Image, rt.RunEngine)
		allVolumes, err := CollectImageVolumes(cfg, layers, c.Image, resolved.Home, nil)
		if err != nil {
			return err
		}
		security = CollectSecurity(cfg, layers, c.Image)
		cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
		volumes, bindMounts = ResolveVolumeBacking(c.Image, allVolumes, mergeVolumeConfigs(deployVolumes, cliVolumes), resolved.Home, rt.EncryptedStoragePath, rt.VolumesPath)
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
		uid = resolved.UID
		gid = resolved.GID
		home = resolved.Home
		ports = resolved.Ports
		network = resolved.Network
		img := cfg.Images[c.Image]
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
		// Resolve per-image engine from labels
		engine = ResolveImageEngineFromMeta(meta, rt.RunEngine)
		// Apply deploy.yml overrides
		MergeDeployOntoMetadata(meta, dc, c.Instance)

		uid = meta.UID
		gid = meta.GID
		home = meta.Home
		ports = meta.Ports
		security = meta.Security
		network = meta.Network
		deployEnv = meta.Env

		// Resolve volume backing from labels + deploy config
		cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
		volumes, bindMounts = ResolveVolumeBacking(c.Image, meta.Volumes, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		}
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)
	shellCtrName := containerNameInstance(c.Image, c.Instance)
	shellGlobalEnv := dc.GlobalEnvForImage(c.Image, shellCtrName)
	envVars, err := ResolveEnvVars(shellGlobalEnv, deployEnv, deployEnvFile, workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Resolve agent forwarding (SSH/GPG socket mounts)
	var deployImage *DeployImageConfig
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployImage = &overlay
		}
	}
	agentFwd := ResolveAgentForwarding(rt, deployImage, home)

	// If the container is already running, exec into it instead of starting a new one
	name := containerNameInstance(c.Image, c.Instance)
	if containerRunning(engine, name) {
		// Exec path: inject env vars only (can't add volumes to running container)
		execEnv := append(envVars, agentFwd.Env...)
		workDir := resolveWorkingDir(volumes, bindMounts, home)
		args := buildExecArgs(engine, name, uid, gid, c.Command, execEnv, workDir)
		enginePath, err := findExecutable(EngineBinary(engine))
		if err != nil {
			return err
		}
		return execCommand(enginePath, args)
	}

	if cfgErr != nil {
		// Already ensured above in the label path
	} else {
		imageRT := ImageRuntime(rt, engine)
		if err := EnsureImage(imageRef, imageRT); err != nil {
			return err
		}
	}

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Image); err != nil {
		return err
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}

	// Inject agent forwarding mounts and env (new container path)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	// Resolve network (default to shared "ov" network)
	resolvedNetwork, err := ResolveNetwork(network, engine)
	if err != nil {
		return err
	}

	workDir := resolveWorkingDir(volumes, bindMounts, home)
	args := buildShellArgs(engine, imageRef, uid, gid, ports, volumes, bindMounts, detected.GPU, c.Command, rt.BindAddress, envVars, security, workDir, resolvedNetwork)

	// Find engine binary
	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}

	// Replace process with engine
	return execCommand(enginePath, args)
}

func (c *ShellCmd) runRemote(ref string) error {
	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.RunEngine

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	ctx, err := ResolveRemoteImage(ref, c.Tag)
	if err != nil {
		return err
	}

	allVolumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(ctx.ImageName, allVolumes, cliVolumes, ctx.Resolved.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	// Resolve env vars with global env
	shellRemoteDC, _ := LoadDeployConfig()
	shellRemoteCtrName := containerNameInstance(ctx.ImageName, "")
	shellRemoteGlobalEnv := shellRemoteDC.GlobalEnvForImage(ctx.ImageName, shellRemoteCtrName)
	envVars, envErr := ResolveEnvVars(shellRemoteGlobalEnv, nil, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}

	// Resolve agent forwarding for remote images (no deploy.yml overlay)
	remoteAgentFwd := ResolveAgentForwarding(rt, nil, ctx.Resolved.Home)

	// If the container is already running, exec into it
	name := containerNameInstance(ctx.ImageName, c.Instance)
	if containerRunning(engine, name) {
		execEnv := append(envVars, remoteAgentFwd.Env...)
		workDir := resolveWorkingDir(volumes, bindMounts, ctx.Resolved.Home)
		args := buildExecArgs(engine, name, ctx.Resolved.UID, ctx.Resolved.GID, c.Command, execEnv, workDir)
		enginePath, err := findExecutable(EngineBinary(engine))
		if err != nil {
			return err
		}
		return execCommand(enginePath, args)
	}

	// Pull or build
	if err := ctx.PullOrBuild(rt, c.Tag, c.Build); err != nil {
		return err
	}

	// Resolve per-image engine from remote config
	if ctx.Resolved != nil && ctx.Resolved.Engine != "" {
		engine = ctx.Resolved.Engine
	}

	if err := verifyBindMounts(bindMounts, ctx.ImageName); err != nil {
		return err
	}

	// Merge auto-detected devices
	security := SecurityConfig{}
	security.Devices = appendUnique(security.Devices, detected.Devices...)
	if detected.AMDGPU {
		security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
	}
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}

	// Inject agent forwarding mounts and env
	for _, v := range remoteAgentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, remoteAgentFwd.Env...)

	// Resolve network
	resolvedNetwork, netErr := ResolveNetwork("", engine)
	if netErr != nil {
		return netErr
	}

	workDir := resolveWorkingDir(volumes, bindMounts, ctx.Resolved.Home)
	args := buildShellArgs(engine, ctx.ImageRef,
		ctx.Resolved.UID, ctx.Resolved.GID, ctx.Resolved.Ports,
		volumes, bindMounts, detected.GPU, c.Command, rt.BindAddress, envVars, security, workDir, resolvedNetwork)

	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}
	return execCommand(enginePath, args)
}

// resolveShellImageRef builds the full image reference from registry, name, and tag.
func resolveShellImageRef(registry, name, tag string) string {
	if registry != "" {
		return fmt.Sprintf("%s/%s:%s", registry, name, tag)
	}
	return fmt.Sprintf("%s:%s", name, tag)
}

// buildShellArgs constructs the container run argument list.
func buildShellArgs(engine, imageRef string, uid, gid int, ports []string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, command string, bindAddr string, envVars []string, security SecurityConfig, workingDir string, network ...string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "run", "--rm", interactive,
		"-w", workingDir,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	if len(network) > 0 && network[0] != "" {
		args = append(args, "--network", network[0])
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
	for _, m := range security.Mounts {
		if strings.HasPrefix(m, "tmpfs:") {
			// tmpfs:/path:options → --tmpfs /path:options
			args = append(args, "--tmpfs", strings.TrimPrefix(m, "tmpfs:"))
		} else {
			args = append(args, "-v", m)
		}
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
func buildExecArgs(engine, name string, uid, gid int, command string, envVars []string, workingDir string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "exec", interactive,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-w", workingDir,
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

// execCommand runs the given args via syscall.Exec. When forceTTY is set
// and there is no real terminal, it wraps the command with `script` to
// provide a proper PTY so that programs requiring a TTY work correctly
// from automation tools.
func execCommand(path string, args []string) error {
	if forceTTY && !isTerminal() {
		// Wrap with script to provide a real PTY.
		// script -qefc "<cmd>" /dev/null
		//   -q: quiet (no "Script started" banner)
		//   -e: return child exit code
		//   -f: flush output after each write
		//   -c: command to run
		cmdStr := shellQuoteArgs(args)
		scriptPath, err := findExecutable("script")
		if err != nil {
			return fmt.Errorf("--tty requires 'script' (util-linux): %w", err)
		}
		scriptArgs := []string{"script", "-qefc", cmdStr, "/dev/null"}
		return syscall.Exec(scriptPath, scriptArgs, os.Environ())
	}
	return syscall.Exec(path, args, os.Environ())
}

// shellQuoteArgs joins args into a shell-safe command string.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'\\$`!#&|;(){}[]<>?*~") {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

// localizePort prefixes a port mapping with the given bind address.
// "80:8000" -> "<bindAddr>:80:8000", "8080" -> "<bindAddr>:8080:8080"
// Preserves protocol suffixes: "47998:47998/udp" -> "<bindAddr>:47998:47998/udp"
func localizePort(mapping string, bindAddr string) string {
	// Extract and preserve protocol suffix (/udp, /tcp)
	suffix := ""
	clean := mapping
	for _, proto := range []string{"/udp", "/tcp"} {
		if strings.HasSuffix(mapping, proto) {
			suffix = proto
			clean = strings.TrimSuffix(mapping, proto)
			break
		}
	}
	if strings.Contains(clean, ":") {
		return bindAddr + ":" + clean + suffix
	}
	return fmt.Sprintf("%s:%s:%s%s", bindAddr, clean, clean, suffix)
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
