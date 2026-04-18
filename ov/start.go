package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StartCmd launches a container with supervisord in the background
type StartCmd struct {
	Image      string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Tag        string   `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Build      bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env        []string `short:"e" long:"env" sep:"none" help:"Set container env var (direct mode only)"`
	EnvFile    string   `long:"env-file" help:"Load env vars from file (direct mode only)"`
	Instance   string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	Port       []string `short:"p" help:"Remap host port (direct mode only)"`
	VolumeFlag []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind       []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	AutoDetectFlags `embed:""`
}

func (c *StartCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `ov image pull`.
	if IsRemoteImageRef(StripURLScheme(c.Image)) {
		return fmt.Errorf("remote refs are not accepted here; run 'ov image pull %s' first, then 'ov start <image-name>'", c.Image)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		return c.runQuadlet(rt)
	}

	return c.runDirect(rt)
}

func (c *StartCmd) runDirect(rt *ResolvedRuntime) error {
	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	engine := rt.RunEngine

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	// Load deploy.yml for volume backing config
	dc, _ := LoadDeployConfig()
	var deployVolumes []DeployVolumeConfig
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployVolumes = overlay.Volumes
		}
	}

	// Resolve from image labels (+ deploy.yml overlay). No image.yml.
	imageRef := resolveShellImageRef("", c.Image, c.Tag)
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
	engine = ResolveImageEngineFromMeta(meta, rt.RunEngine)
	MergeDeployOntoMetadata(meta, dc, c.Instance)

	// Sidecars require quadlet mode (pod networking is only available via quadlet)
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok && len(overlay.Sidecars) > 0 {
			return fmt.Errorf("image %s has sidecars configured in deploy.yml; use 'ov config %s && ov start %s' (sidecars require quadlet mode)", c.Image, c.Image, c.Image)
		}
	}

	uid := meta.UID
	gid := meta.GID
	home := meta.Home
	ports := meta.Ports
	security := meta.Security
	network := meta.Network
	entrypoint := resolveEntrypointFromMeta(meta)

	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(c.Image, meta.Volumes, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	envAccepts := meta.EnvAccepts
	envRequires := meta.EnvRequires
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)

	// Auto-initialize and mount encrypted volumes if needed
	if err := ensureEncryptedMounts(c.Image, c.Instance, false); err != nil {
		return err
	}

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Image); err != nil {
		return err
	}

	// Resolve env vars from labels
	deployEnv := meta.Env
	var deployEnvFile string
	startCtrName := containerNameInstance(c.Image, c.Instance)
	startAccepted := AcceptedEnvSet(envAccepts, envRequires)
	startGlobalEnv := dc.GlobalEnvForImage(c.Image, startCtrName, startAccepted)
	envVars, err := ResolveEnvVars(startGlobalEnv, deployEnv, deployEnvFile, workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	// Resolve network (default to shared "ov" network)
	resolvedNetwork, netErr := ResolveNetwork(network, engine)
	if netErr != nil {
		return netErr
	}

	// Apply port overrides from --port flags and persist to deploy.yml
	if len(c.Port) > 0 {
		ports, err = ApplyPortOverrides(ports, c.Port)
		if err != nil {
			return err
		}
		saveDeployState(c.Image, c.Instance, SaveDeployStateInput{Ports: ports})
	}

	// Pre-flight port conflict check
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, c.Image))
	}

	// Inject agent forwarding mounts and env (direct mode only)
	var deployImage *DeployImageConfig
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployImage = &overlay
		}
	}
	agentFwd := ResolveAgentForwarding(rt, deployImage, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := containerNameInstance(c.Image, c.Instance)
	workDir := resolveWorkingDir(volumes, bindMounts, home)
	args := buildStartArgs(engine, imageRef, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, entrypoint, workDir, resolvedNetwork)

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s run failed: %w\n%s", EngineBinary(engine), err, strings.TrimSpace(string(output)))
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	fmt.Println(containerID)
	fmt.Fprintf(os.Stderr, "Started %s as %s\n", name, containerID)

	// Start tunnel if configured (deploy.yml-only; labels never carry tunnel).
	if meta.Tunnel != nil {
		tc := TunnelConfigFromMetadata(meta)
		if tc != nil {
			if err := TunnelStart(*tc); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
			}
		}
	}

	return nil
}


func (c *StartCmd) runQuadlet(rt *ResolvedRuntime) error {
	exists, err := quadletExistsInstance(c.Image, c.Instance)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("not configured; run 'ov config %s' first", c.Image)
	}

	// Mount encrypted volumes if needed (runtime concern, not config)
	if err := ensureEncryptedMounts(c.Image, c.Instance, false); err != nil {
		return err
	}

	svc := serviceNameInstance(c.Image, c.Instance)
	cmd := exec.Command("systemctl", "--user", "start", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", svc)
	return nil
}

// hasConfigOverrides returns true if the user passed any config flags that
// should trigger quadlet regeneration (port maps, env vars, env file).
// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Image    string `arg:"" help:"Image name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *StopCmd) Run() error {
	// Resolve the image name (handle remote refs)
	imageName := c.Image
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		imageName = ParseRemoteRef(ref).Name
	}

	// Stop tunnel before stopping container (best-effort)
	stopTunnelForImage(imageName, c.Instance)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Always use systemctl for quadlet-managed containers, regardless of run_mode.
	// Without this, podman stop + systemd Restart=always creates a restart loop.
	quadletActive, _ := quadletExistsInstance(imageName, c.Instance)
	if quadletActive {
		svc := serviceNameInstance(imageName, c.Instance)
		cmd := exec.Command("systemctl", "--user", "stop", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stopping %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Stopped %s\n", svc)
		return nil
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)

	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, c.Instance)

	cmd := exec.Command(engine, "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback: try the other engine if the container wasn't found
		otherEngine := "docker"
		if runEngine == "docker" {
			otherEngine = "podman"
		}
		otherBinary := EngineBinary(otherEngine)
		fallbackCmd := exec.Command(otherBinary, "stop", name)
		if _, fallbackErr := fallbackCmd.CombinedOutput(); fallbackErr == nil {
			fmt.Fprintf(os.Stderr, "Stopped %s (via %s)\n", name, otherEngine)
			return nil
		}
		return fmt.Errorf("%s stop failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// stopTunnelForImage attempts to stop any tunnel for the given image (best-effort).
func stopTunnelForImage(imageName, instance string) {
	var tc *TunnelConfig

	// Tunnel config comes from deploy.yml (overlaid onto ImageMetadata).
	ctrName := containerNameInstance(imageName, instance)
	imageRef := containerImage("podman", ctrName)
	if imageRef != "" {
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil {
			dc, _ := LoadDeployConfig()
			MergeDeployOntoMetadata(meta, dc, instance)
			if meta.Tunnel != nil {
				tc = TunnelConfigFromMetadata(meta)
			}
		}
	}

	if tc != nil {
		if err := TunnelStop(*tc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
}

// buildStartArgs constructs the container run argument list for a detached service.
// entrypoint is the init system command (e.g., ["supervisord", "-n", "-c", "/etc/supervisord.conf"])
// or the fallback (e.g., ["sleep", "infinity"]).
func buildStartArgs(engine, imageRef string, uid, gid int, ports []string, name string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, bindAddr string, envVars []string, security SecurityConfig, entrypoint []string, workingDir string, network ...string) []string {
	binary := EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-w", workingDir,
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
	args = append(args, imageRef)
	args = append(args, entrypoint...)
	return args
}

// resolveEntrypoint determines the init system entrypoint for an image.
// Uses init.yml config when available, falls back to well-known defaults.
func resolveEntrypoint(initConfig *InitConfig, layers map[string]*Layer, layerOrder []string, isBootc bool) []string {
	if initConfig != nil {
		initName, initDef := initConfig.ResolveInitSystem(layers, layerOrder, isBootc, "")
		if initName != "" && initDef != nil && len(initDef.Entrypoint) > 0 {
			return initDef.Entrypoint
		}
	}
	return []string{"sleep", "infinity"}
}

// resolveEntrypointFromMeta determines the entrypoint from image metadata (runtime mode).
// Uses well-known init system names; custom init systems declared via init.yml are
// only honored during build.
func resolveEntrypointFromMeta(meta *ImageMetadata) []string {
	if meta.Init == "" {
		return []string{"sleep", "infinity"}
	}
	switch meta.Init {
	case "supervisord":
		return []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}
	case "systemd":
		return nil // systemd images boot via VM, no container entrypoint
	default:
		return []string{"sleep", "infinity"}
	}
}

// containerName returns the deterministic container name for an image.
func containerName(imageName string) string {
	return "ov-" + imageName
}

// containerNameInstance returns the container name with optional instance suffix.
func containerNameInstance(imageName, instance string) string {
	if instance == "" {
		return containerName(imageName)
	}
	return "ov-" + imageName + "-" + instance
}
