package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemote(ref)
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

	var imageRef string
	var uid, gid int
	var home string
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount
	var security SecurityConfig
	var network string
	var entrypoint []string

	// Load deploy.yml for volume backing config
	dc, _ := LoadDeployConfig()
	var deployVolumes []DeployVolumeConfig
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployVolumes = overlay.Volumes
		}
	}

	// Try images.yml first, fall back to image labels
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
		// Resolve entrypoint from init config
		img := cfg.Images[c.Image]
		resolvedLayers, _ := ResolveLayerOrder(img.Layers, layers, nil)
		entrypoint = resolveEntrypoint(resolved.InitConfig, layers, resolvedLayers, resolved.Bootc)
	} else {
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

		// Sidecars require quadlet mode (pod networking is only available via quadlet)
		if dc != nil {
			if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok && len(overlay.Sidecars) > 0 {
				return fmt.Errorf("image %s has sidecars configured in deploy.yml; use 'ov config %s && ov start %s' (sidecars require quadlet mode)", c.Image, c.Image, c.Image)
			}
		}

		uid = meta.UID
		gid = meta.GID
		home = meta.Home
		ports = meta.Ports
		security = meta.Security
		network = meta.Network
		entrypoint = resolveEntrypointFromMeta(meta)

		// Resolve volume backing from labels + deploy config
		cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
		volumes, bindMounts = ResolveVolumeBacking(c.Image, meta.Volumes, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		}
	}

	if cfgErr == nil {
		imageRT := ImageRuntime(rt, engine)
		if err := EnsureImage(imageRef, imageRT); err != nil {
			return err
		}
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

	// Resolve env vars
	var deployEnv []string
	var deployEnvFile string
	if cfgErr == nil {
		img := cfg.Images[c.Image]
		deployEnv = img.Env
		deployEnvFile = img.EnvFile
	}
	startCtrName := containerNameInstance(c.Image, c.Instance)
	startGlobalEnv := dc.GlobalEnvForImage(c.Image, startCtrName)
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
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}

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

	// Start tunnel if configured
	if cfgErr == nil {
		resolved, resolveErr := cfg.ResolveImage(c.Image, "unused", dir)
		if resolveErr == nil && resolved.Tunnel != nil {
			layers, scanErr := ScanAllLayersWithConfig(dir, cfg)
			if scanErr == nil {
				tc := ResolveTunnelConfig(
					c.findTunnelYAML(cfg),
					c.Image, resolved.DNS, layers, resolved.Layers,
					collectPortProtos(layers, resolved.Layers), resolved.Ports,
				)
				if tc != nil {
					if err := TunnelStart(*tc); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
					}
				}
			}
		}
	} else {
		// Label path: start tunnel from metadata
		meta, metaErr := ExtractMetadata(engine, imageRef)
		if metaErr == nil && meta != nil && meta.Tunnel != nil {
			dc, _ := LoadDeployConfig()
			MergeDeployOntoMetadata(meta, dc, c.Instance)
			tc := TunnelConfigFromMetadata(meta)
			if tc != nil {
				if err := TunnelStart(*tc); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
				}
			}
		}
	}

	return nil
}

func (c *StartCmd) runRemote(ref string) error {
	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	ctx, err := ResolveRemoteImage(ref, c.Tag)
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		return c.runRemoteQuadlet(rt, ctx, detected)
	}

	// Pull or build
	if err := ctx.PullOrBuild(rt, c.Tag, c.Build); err != nil {
		return err
	}

	// Resolve per-image engine from remote config
	engine := rt.RunEngine
	if ctx.Resolved != nil {
		engine = ResolveImageEngineFromMeta(&ImageMetadata{Engine: ctx.Resolved.Engine}, rt.RunEngine)
	}

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	allVolumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(ctx.ImageName, allVolumes, cliVolumes, ctx.Resolved.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	if err := verifyBindMounts(bindMounts, ctx.ImageName); err != nil {
		return err
	}

	// Resolve env vars with global env
	remoteDC, _ := LoadDeployConfig()
	remoteStartCtrName := containerNameInstance(ctx.ImageName, "")
	remoteStartGlobalEnv := remoteDC.GlobalEnvForImage(ctx.ImageName, remoteStartCtrName)
	envVars, err := ResolveEnvVars(remoteStartGlobalEnv, nil, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if err != nil {
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

	// Resolve network
	resolvedNetwork, netErr := ResolveNetwork("", engine)
	if netErr != nil {
		return netErr
	}

	// Resolve entrypoint from init config
	remoteEntrypoint := []string{"sleep", "infinity"}
	if ctx.Layers != nil {
		resolvedLayers, _ := ResolveLayerOrder(ctx.Resolved.Layers, ctx.Layers, nil)
		remoteEntrypoint = resolveEntrypoint(ctx.Resolved.InitConfig, ctx.Layers, resolvedLayers, ctx.Resolved.Bootc)
	}

	// Inject agent forwarding (remote direct mode, no deploy.yml overlay)
	remoteAgentFwd := ResolveAgentForwarding(rt, nil, ctx.Resolved.Home)
	for _, v := range remoteAgentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, remoteAgentFwd.Env...)

	name := containerNameInstance(ctx.ImageName, c.Instance)
	workDir := resolveWorkingDir(volumes, bindMounts, ctx.Resolved.Home)
	args := buildStartArgs(engine, ctx.ImageRef,
		ctx.Resolved.UID, ctx.Resolved.GID, ctx.Resolved.Ports,
		name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, remoteEntrypoint, workDir, resolvedNetwork)

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
	return nil
}

func (c *StartCmd) runRemoteQuadlet(rt *ResolvedRuntime, ctx *RemoteImageContext, detected DetectedDevices) error {
	// For quadlet with remote refs: resolve and generate quadlet file
	allVolumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(ctx.ImageName, allVolumes, cliVolumes, ctx.Resolved.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	// Ensure image is in podman
	podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
	if err := ctx.PullOrBuild(podmanRT, c.Tag, c.Build); err != nil {
		return err
	}

	// Resolve env vars with global env
	remoteQDC, _ := LoadDeployConfig()
	remoteQCtrName := containerNameInstance(ctx.ImageName, "")
	remoteQGlobalEnv := remoteQDC.GlobalEnvForImage(ctx.ImageName, remoteQCtrName)
	envVars, envErr := ResolveEnvVars(remoteQGlobalEnv, nil, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
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

	// Resolve network
	resolvedNetwork, netErr := ResolveNetwork("", rt.RunEngine)
	if netErr != nil {
		return netErr
	}

	ports := ctx.Resolved.Ports
	if len(c.Port) > 0 {
		var portErr error
		ports, portErr = ApplyPortOverrides(ports, c.Port)
		if portErr != nil {
			return portErr
		}
	}

	// Resolve entrypoint for quadlet
	remoteQEntrypoint := []string{"sleep", "infinity"}
	if ctx.Layers != nil {
		resolvedLayers, _ := ResolveLayerOrder(ctx.Resolved.Layers, ctx.Layers, nil)
		remoteQEntrypoint = resolveEntrypoint(ctx.Resolved.InitConfig, ctx.Layers, resolvedLayers, ctx.Resolved.Bootc)
	}

	qcfg := QuadletConfig{
		ImageName:      ctx.ImageName,
		ImageRef:       ctx.ImageRef,
		Home:           ctx.Resolved.Home,
		Ports:          ports,
		Volumes:        volumes,
		BindMounts:     bindMounts,
		GPU:            detected.GPU,
		BindAddress:    rt.BindAddress,
		UID:            ctx.Resolved.UID,
		GID:            ctx.Resolved.GID,
		Env:            envVars,
		Instance:       c.Instance,
		Security:       security,
		Network:        resolvedNetwork,
		Status:         ctx.Resolved.Status,
		Info:           ctx.Resolved.Info,
		Entrypoint:     remoteQEntrypoint,
	}

	content := generateQuadlet(qcfg)

	qdir, err := quadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}

	qpath := filepath.Join(qdir, quadletFilenameInstance(ctx.ImageName, c.Instance))
	if err := os.WriteFile(qpath, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing quadlet file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", qpath)

	reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := reloadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	svc := serviceNameInstance(ctx.ImageName, c.Instance)
	startCmd := exec.Command("systemctl", "--user", "start", svc)
	startCmd.Stdout = os.Stdout
	startCmd.Stderr = os.Stderr
	if err := startCmd.Run(); err != nil {
		return fmt.Errorf("starting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", svc)
	return nil
}

// findTunnelYAML returns the raw TunnelYAML from config for this image.
func (c *StartCmd) findTunnelYAML(cfg *Config) *TunnelYAML {
	img := cfg.Images[c.Image]
	if img.Tunnel != nil {
		return img.Tunnel
	}
	return cfg.Defaults.Tunnel
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

	// Try images.yml
	dir, err := os.Getwd()
	if err == nil {
		cfg, cfgErr := LoadConfig(dir)
		if cfgErr == nil {
			resolved, resolveErr := cfg.ResolveImage(imageName, "unused", dir)
			if resolveErr == nil && resolved.Tunnel != nil {
				layers, scanErr := ScanAllLayersWithConfig(dir, cfg)
				if scanErr == nil {
					tunnelYAML := cfg.Images[imageName].Tunnel
					if tunnelYAML == nil {
						tunnelYAML = cfg.Defaults.Tunnel
					}
					tc = ResolveTunnelConfig(tunnelYAML, imageName, resolved.DNS, layers, resolved.Layers, collectPortProtos(layers, resolved.Layers), resolved.Ports)
				}
			}
		}
	}

	// Fall back to image labels
	if tc == nil {
		ctrName := containerNameInstance(imageName, instance)
		imageRef := containerImage("podman", ctrName)
		if imageRef != "" {
			meta, metaErr := ExtractMetadata("podman", imageRef)
			if metaErr == nil && meta != nil && meta.Tunnel != nil {
				dc, _ := LoadDeployConfig()
				MergeDeployOntoMetadata(meta, dc, instance)
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
func resolveEntrypointFromMeta(meta *ImageMetadata) []string {
	if meta.Init == "" {
		return []string{"sleep", "infinity"}
	}
	// Try loading init.yml from project directory
	dir, _ := os.Getwd()
	cfg, _ := LoadConfig(dir)
	if cfg != nil {
		initCfg, err := LoadInitConfigForImage(
			cfg.Defaults.FormatConfig, cfg.Defaults.FormatConfig, dir,
		)
		if err == nil && initCfg != nil {
			if def, ok := initCfg.Inits[meta.Init]; ok {
				if len(def.Entrypoint) > 0 {
					return def.Entrypoint
				}
			}
		}
	}
	// Fallback for well-known init systems
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
