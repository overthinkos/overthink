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
	Image     string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Workspace string   `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string   `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Build     bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env       []string `short:"e" long:"env" help:"Set container env var (KEY=VALUE)"`
	EnvFile   string   `long:"env-file" help:"Load env vars from file"`
	Instance  string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	PortMap   []string `short:"p" long:"port" help:"Remap host port (newHost:containerPort, e.g., 5901:5900)"`
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

	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	engine := rt.RunEngine

	var imageRef string
	var uid, gid int
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount
	var security SecurityConfig
	var network string

	// Try images.yml first, fall back to image labels
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
		// Resolve per-image engine
		engine = ResolveImageEngine(cfg, layers, c.Image, rt.RunEngine)
		volumes, err = CollectImageVolumes(cfg, layers, c.Image, resolved.Home, BindMountNames(cfg.Images[c.Image].BindMounts))
		if err != nil {
			return err
		}
		security = CollectSecurity(cfg, layers, c.Image)
		// Resolve bind mounts
		img := cfg.Images[c.Image]
		if len(img.BindMounts) > 0 {
			bindMounts = resolveBindMounts(c.Image, img.BindMounts, resolved.Home, rt.EncryptedStoragePath)
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
		uid = resolved.UID
		gid = resolved.GID
		ports = resolved.Ports
		network = resolved.Network
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
		dc, _ := LoadDeployConfig()
		MergeDeployOntoMetadata(meta, dc)

		uid = meta.UID
		gid = meta.GID
		ports = meta.Ports
		volumes = meta.Volumes
		security = meta.Security
		network = meta.Network

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

	if cfgErr == nil {
		imageRT := ImageRuntime(rt, engine)
		if err := EnsureImage(imageRef, imageRT); err != nil {
			return err
		}
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)

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
	envVars, err := ResolveEnvVars(deployEnv, deployEnvFile, absWorkspace, c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
	}

	// Resolve network (default to shared "ov" network)
	resolvedNetwork, netErr := ResolveNetwork(network, engine)
	if netErr != nil {
		return netErr
	}

	// Apply port overrides from --port flags and persist to deploy.yml
	if len(c.PortMap) > 0 {
		ports, err = ApplyPortOverrides(ports, c.PortMap)
		if err != nil {
			return err
		}
		saveDeployState(c.Image, SaveDeployStateInput{Ports: ports})
	}

	// Pre-flight port conflict check
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, c.Image))
	}

	name := containerNameInstance(c.Image, c.Instance)
	args := buildStartArgs(engine, imageRef, absWorkspace, uid, gid, ports, name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, resolvedNetwork)

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
		resolved, resolveErr := cfg.ResolveImage(c.Image, "unused")
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
			MergeDeployOntoMetadata(meta, dc)
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
		return c.runRemoteQuadlet(rt, ctx, absWorkspace, detected)
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

	volumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	bindMounts := ctx.CollectBindMounts(rt.EncryptedStoragePath)

	if err := verifyBindMounts(bindMounts, ctx.ImageName); err != nil {
		return err
	}

	// Resolve env vars
	envVars, err := ResolveEnvVars(nil, "", absWorkspace, c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Merge auto-detected devices
	security := SecurityConfig{}
	security.Devices = appendUnique(security.Devices, detected.Devices...)

	// Resolve network
	resolvedNetwork, netErr := ResolveNetwork("", engine)
	if netErr != nil {
		return netErr
	}

	name := containerNameInstance(ctx.ImageName, c.Instance)
	args := buildStartArgs(engine, ctx.ImageRef, absWorkspace,
		ctx.Resolved.UID, ctx.Resolved.GID, ctx.Resolved.Ports,
		name, volumes, bindMounts, detected.GPU, rt.BindAddress, envVars, security, resolvedNetwork)

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

func (c *StartCmd) runRemoteQuadlet(rt *ResolvedRuntime, ctx *RemoteImageContext, absWorkspace string, detected DetectedDevices) error {
	// For quadlet with remote refs: resolve and generate quadlet file
	volumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}
	bindMounts := ctx.CollectBindMounts(rt.EncryptedStoragePath)

	// Ensure image is in podman
	podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
	if err := ctx.PullOrBuild(podmanRT, c.Tag, c.Build); err != nil {
		return err
	}

	// Resolve env vars
	envVars, envErr := ResolveEnvVars(nil, "", absWorkspace, c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}

	// Merge auto-detected devices
	security := SecurityConfig{}
	security.Devices = appendUnique(security.Devices, detected.Devices...)

	// Resolve network
	resolvedNetwork, netErr := ResolveNetwork("", rt.RunEngine)
	if netErr != nil {
		return netErr
	}

	ports := ctx.Resolved.Ports
	if len(c.PortMap) > 0 {
		var portErr error
		ports, portErr = ApplyPortOverrides(ports, c.PortMap)
		if portErr != nil {
			return portErr
		}
	}

	qcfg := QuadletConfig{
		ImageName:   ctx.ImageName,
		ImageRef:    ctx.ImageRef,
		Workspace:   absWorkspace,
		Ports:       ports,
		Volumes:     volumes,
		BindMounts:  bindMounts,
		GPU:         detected.GPU,
		BindAddress: rt.BindAddress,
		UID:         ctx.Resolved.UID,
		GID:         ctx.Resolved.GID,
		Env:         envVars,
		Instance:    c.Instance,
		Security:    security,
		Network:     resolvedNetwork,
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
	if err := os.WriteFile(qpath, []byte(content), 0644); err != nil {
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
		if !rt.AutoEnable {
			return fmt.Errorf("not enabled; run 'ov enable %s' first, or set auto_enable=true", c.Image)
		}
		// Auto-enable: generate quadlet file
		enable := &EnableCmd{
			Image:           c.Image,
			Workspace:       c.Workspace,
			Tag:             c.Tag,
			Env:             c.Env,
			EnvFile:         c.EnvFile,
			Instance:        c.Instance,
			PortMap:         c.PortMap,
			AutoDetectFlags: c.AutoDetectFlags,
		}
		if err := enable.runEnable(rt); err != nil {
			return err
		}
	} else if c.hasConfigOverrides() {
		// Quadlet exists but config flags changed — regenerate
		enable := &EnableCmd{
			Image:           c.Image,
			Workspace:       c.Workspace,
			Tag:             c.Tag,
			Env:             c.Env,
			EnvFile:         c.EnvFile,
			Instance:        c.Instance,
			PortMap:         c.PortMap,
			AutoDetectFlags: c.AutoDetectFlags,
		}
		if err := enable.runEnable(rt); err != nil {
			return err
		}
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
func (c *StartCmd) hasConfigOverrides() bool {
	return len(c.PortMap) > 0 || len(c.Env) > 0 || c.EnvFile != ""
}

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
	stopTunnelForImage(imageName)

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
	runEngine := ResolveImageEngineForDeploy(imageName, rt.RunEngine)

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
func stopTunnelForImage(imageName string) {
	var tc *TunnelConfig

	// Try images.yml
	dir, err := os.Getwd()
	if err == nil {
		cfg, cfgErr := LoadConfig(dir)
		if cfgErr == nil {
			resolved, resolveErr := cfg.ResolveImage(imageName, "unused")
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
		containerName := containerNameInstance(imageName, "")
		imageRef := containerImage("podman", containerName)
		if imageRef != "" {
			meta, metaErr := ExtractMetadata("podman", imageRef)
			if metaErr == nil && meta != nil && meta.Tunnel != nil {
				dc, _ := LoadDeployConfig()
				MergeDeployOntoMetadata(meta, dc)
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

// buildStartArgs constructs the container run argument list for detached supervisord.
func buildStartArgs(engine, imageRef, workspace string, uid, gid int, ports []string, name string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, bindAddr string, envVars []string, security SecurityConfig, network ...string) []string {
	binary := EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
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
	if engine == "podman" && len(bindMounts) > 0 {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, imageRef, "supervisord", "-n", "-c", "/etc/supervisord.conf")
	return args
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
