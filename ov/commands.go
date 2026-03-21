package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnableCmd generates a quadlet .container file and reloads systemd (quadlet only)
type EnableCmd struct {
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

func (c *EnableCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov enable requires run_mode=quadlet (current: %s)", rt.RunMode)
	}

	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemoteEnable(rt, ref)
	}

	return c.runEnable(rt)
}

func (c *EnableCmd) runEnable(rt *ResolvedRuntime) error {
	absWorkspace, err := filepath.Abs(c.Workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	// Check deploy.yml for previously saved workspace when using default
	if c.Workspace == "." {
		if dc, _ := LoadDeployConfig(); dc != nil {
			if entry, ok := dc.Images[c.Image]; ok && entry.Workspace != "" {
				if wInfo, wErr := os.Stat(entry.Workspace); wErr == nil && wInfo.IsDir() {
					absWorkspace = entry.Workspace
					fmt.Fprintf(os.Stderr, "Using workspace from deploy.yml: %s\n", absWorkspace)
				}
			}
		}
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

	// Always resolve from image labels (no images.yml dependency for deployment)
	imageRef := resolveShellImageRef("", c.Image, c.Tag)
	podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
	if err := EnsureImage(imageRef, podmanRT); err != nil {
		return err
	}
	meta, err := ExtractMetadata("podman", imageRef)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest ov", imageRef)
	}

	// Apply deploy.yml overrides onto label metadata
	dc, _ := LoadDeployConfig()
	MergeDeployOntoMetadata(meta, dc)

	uid, gid := meta.UID, meta.GID
	ports := meta.Ports
	volumes := meta.Volumes
	security := meta.Security
	network := meta.Network

	// Resolve bind mounts from labels + deploy.yml host paths
	var deployMounts []BindMountConfig
	if dc != nil {
		if overlay, ok := dc.Images[c.Image]; ok {
			deployMounts = overlay.BindMounts
		}
	}
	bindMounts := resolveBindMountsFromLabels(c.Image, meta.BindMounts, meta.Home, rt.EncryptedStoragePath, deployMounts)

	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
	}

	// Resolve tunnel config from labels
	var tunnelCfg *TunnelConfig
	if meta.Tunnel != nil {
		tunnelCfg = TunnelConfigFromMetadata(meta)
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)

	// Resolve env vars from labels + deploy.yml + CLI
	envVars, envErr := ResolveEnvVars(meta.Env, "", absWorkspace, c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}

	// For quadlet, resolve env file to absolute path for EnvironmentFile=
	var quadletEnvFile string
	if c.EnvFile != "" {
		quadletEnvFile, _ = filepath.Abs(c.EnvFile)
	}
	// Check deploy.yml env_file
	if quadletEnvFile == "" && dc != nil {
		if overlay, ok := dc.Images[c.Image]; ok && overlay.EnvFile != "" {
			quadletEnvFile = expandHostHome(overlay.EnvFile)
		}
	}
	// Also check workspace .env for quadlet EnvironmentFile
	if quadletEnvFile == "" {
		wsEnvPath := filepath.Join(absWorkspace, ".env")
		if _, statErr := os.Stat(wsEnvPath); statErr == nil {
			quadletEnvFile = wsEnvPath
		}
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
	}

	// Resolve network (default to shared "ov" network)
	resolvedNetwork, netErr := ResolveNetwork(network, rt.RunEngine)
	if netErr != nil {
		return netErr
	}

	// Apply port overrides from --port flags
	if len(c.PortMap) > 0 {
		ports, err = ApplyPortOverrides(ports, c.PortMap)
		if err != nil {
			return err
		}
	}

	// Pre-flight port conflict check (warning for enable, not hard error)
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, rt.RunEngine); len(conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: port conflicts detected:%s", FormatPortConflicts(conflicts, c.Image))
	}

	// For quadlet, we use EnvironmentFile= instead of inline Environment= for file-sourced vars.
	// Only pass CLI -e vars as inline Environment= entries.
	qcfg := QuadletConfig{
		ImageName:   c.Image,
		ImageRef:    imageRef,
		Workspace:   absWorkspace,
		Ports:       ports,
		Volumes:     volumes,
		BindMounts:  bindMounts,
		GPU:         detected.GPU,
		BindAddress: rt.BindAddress,
		Tunnel:      tunnelCfg,
		UID:         uid,
		GID:         gid,
		Env:         envVars,
		EnvFile:     quadletEnvFile,
		Instance:    c.Instance,
		Security:    security,
		Network:     resolvedNetwork,
	}

	// Suppress Env if we're using EnvFile (avoid duplication)
	// Only keep CLI -e flags as inline env vars
	if quadletEnvFile != "" {
		qcfg.Env = c.Env
	}

	// Persist deployment state to deploy.yml (source of truth)
	saveDeployState(c.Image, SaveDeployStateInput{
		Workspace: absWorkspace,
		Ports:     ports,
		Env:       c.Env,
		EnvFile:   quadletEnvFile,
		Network:   resolvedNetwork,
		Security:  &security,
	})

	content := generateQuadlet(qcfg)

	qdir, err := quadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}

	qpath := filepath.Join(qdir, quadletFilenameInstance(c.Image, c.Instance))
	if err := os.WriteFile(qpath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing quadlet file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Wrote %s\n", qpath)

	// Write companion tunnel service if cloudflare tunnel is configured
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		svcDir, err := systemdUserDir()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(svcDir, 0755); err != nil {
			return fmt.Errorf("creating systemd user directory: %w", err)
		}
		tunnelContent := generateTunnelUnit(qcfg)
		tunnelPath := filepath.Join(svcDir, tunnelServiceFilename(c.Image))
		if err := os.WriteFile(tunnelPath, []byte(tunnelContent), 0644); err != nil {
			return fmt.Errorf("writing tunnel service file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", tunnelPath)
	}

	// Write companion crypto service if encrypted bind mounts are configured
	if hasEncryptedBindMounts(bindMounts) {
		encContent := generateEncUnit(c.Image, bindMounts, rt.EncryptedStoragePath)
		if encContent != "" {
			svcDir, err := systemdUserDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(svcDir, 0755); err != nil {
				return fmt.Errorf("creating systemd user directory: %w", err)
			}
			encPath := filepath.Join(svcDir, encServiceFilename(c.Image))
			if err := os.WriteFile(encPath, []byte(encContent), 0644); err != nil {
				return fmt.Errorf("writing crypto service file: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", encPath)
		}
	}

	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")

	// Run post_enable hooks from image labels
	var hooks *HooksConfig
	if meta != nil {
		hooks = meta.Hooks
	}
	if hooks != nil && hooks.PostEnable != "" {
		containerName := containerNameInstance(c.Image, c.Instance)
		svc := serviceNameInstance(c.Image, c.Instance)

		start := exec.Command("systemctl", "--user", "start", svc)
		start.Stdout = os.Stderr
		start.Stderr = os.Stderr
		if err := start.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start %s for post_enable hook: %v\n", svc, err)
		} else {
			engine := EngineBinary(rt.RunEngine)
			if err := RunHook(engine, containerName, hooks.PostEnable, c.Env); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: post_enable hook failed: %v\n", err)
			}
		}
	}

	return nil
}

func (c *EnableCmd) runRemoteEnable(rt *ResolvedRuntime, ref string) error {
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

	ctx, err := ResolveRemoteImage(ref, c.Tag)
	if err != nil {
		return err
	}

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

	qcfg := QuadletConfig{
		ImageName:   ctx.ImageName,
		ImageRef:    ctx.ImageRef,
		Workspace:   absWorkspace,
		Ports:       ctx.Resolved.Ports,
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

	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
	return nil
}

// DisableCmd disables a quadlet service from auto-starting (quadlet only)
type DisableCmd struct {
	Image    string `arg:"" help:"Image name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *DisableCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov disable requires run_mode=quadlet (current: %s)", rt.RunMode)
	}

	imageName := resolveImageName(c.Image)
	svc := serviceNameInstance(imageName, c.Instance)
	cmd := exec.Command("systemctl", "--user", "disable", "--now", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	fmt.Fprintf(os.Stderr, "Disabled %s\n", svc)
	return nil
}

// StatusCmd shows the status of a service container
type StatusCmd struct {
	Image    string `arg:"" optional:"" help:"Image name or remote ref (omit to list all)"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	All      bool   `short:"a" long:"all" help:"Show all services including inactive"`
}

func (c *StatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if c.Image == "" {
		return c.statusAll(rt)
	}

	imageName := resolveImageName(c.Image)

	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(imageName, c.Instance)
		cmd := exec.Command("systemctl", "--user", "status", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
		return nil
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(imageName, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, c.Instance)
	cmd := exec.Command(engine, "inspect", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Container %s is not running\n", name)
	}
	return nil
}

// statusAll lists all running ov containers/services.
func (c *StatusCmd) statusAll(rt *ResolvedRuntime) error {
	if rt.RunMode == "quadlet" {
		args := []string{"--user", "list-units", "ov-*.service", "--no-pager"}
		if c.All {
			args = append(args, "--all")
		}
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	engine := EngineBinary(rt.RunEngine)
	args := []string{"ps", "--filter", "name=ov-",
		"--format", "table {{.Names}}\t{{.Status}}\t{{.Ports}}"}
	if c.All {
		args = append(args, "-a")
	}
	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// LogsCmd shows service container logs
type LogsCmd struct {
	Image    string `arg:"" help:"Image name or remote ref"`
	Follow   bool   `short:"f" long:"follow" help:"Follow log output"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *LogsCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	imageName := resolveImageName(c.Image)

	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(imageName, c.Instance)
		args := []string{"--user", "-u", svc}
		if c.Follow {
			args = append(args, "-f")
		}
		cmd := exec.Command("journalctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("journalctl failed: %w", err)
		}
		return nil
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(imageName, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, c.Instance)
	args := []string{"logs"}
	if c.Follow {
		args = append(args, "-f")
	}
	args = append(args, name)
	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s logs failed: %w", engine, err)
	}
	return nil
}

// UpdateCmd updates an image and restarts the service if active
type UpdateCmd struct {
	Image    string `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Tag      string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Build    bool   `long:"build" help:"Force local build instead of pulling from registry"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *UpdateCmd) Run() error {
	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemoteUpdate(ref)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(c.Image, rt.RunEngine)

	// Resolve image ref from labels (no images.yml dependency)
	imageRef := fmt.Sprintf("%s:%s", c.Image, c.Tag)
	meta, metaErr := ExtractMetadata(runEngine, imageRef)
	if metaErr == nil && meta != nil && meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
	}

	if rt.RunMode == "quadlet" {
		podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
		if err := EnsureImage(imageRef, podmanRT); err != nil {
			return err
		}

		svc := serviceNameInstance(c.Image, c.Instance)
		check := exec.Command("systemctl", "--user", "is-active", svc)
		if err := check.Run(); err == nil {
			fmt.Fprintf(os.Stderr, "Restarting %s\n", svc)
			restart := exec.Command("systemctl", "--user", "restart", svc)
			restart.Stdout = os.Stdout
			restart.Stderr = os.Stderr
			if err := restart.Run(); err != nil {
				return fmt.Errorf("restarting %s: %w", svc, err)
			}
			fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
		} else {
			fmt.Fprintf(os.Stderr, "Service %s is not active, skipping restart\n", svc)
		}
		return nil
	}

	// Direct mode
	imageRT := ImageRuntime(rt, runEngine)
	if err := EnsureImage(imageRef, imageRT); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Image updated. Restart with: ov stop %s && ov start %s\n", c.Image, c.Image)
	return nil
}

func (c *UpdateCmd) runRemoteUpdate(ref string) error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	ctx, err := ResolveRemoteImage(ref, c.Tag)
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
		if err := ctx.PullOrBuild(podmanRT, c.Tag, c.Build); err != nil {
			return err
		}

		svc := serviceNameInstance(ctx.ImageName, c.Instance)
		check := exec.Command("systemctl", "--user", "is-active", svc)
		if err := check.Run(); err == nil {
			fmt.Fprintf(os.Stderr, "Restarting %s\n", svc)
			restart := exec.Command("systemctl", "--user", "restart", svc)
			restart.Stdout = os.Stdout
			restart.Stderr = os.Stderr
			if err := restart.Run(); err != nil {
				return fmt.Errorf("restarting %s: %w", svc, err)
			}
			fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
		} else {
			fmt.Fprintf(os.Stderr, "Service %s is not active, skipping restart\n", svc)
		}
		return nil
	}

	// Direct mode
	if err := ctx.PullOrBuild(rt, c.Tag, c.Build); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Image updated. Restart with: ov stop %s && ov start %s\n", ctx.ImageName, ctx.ImageName)
	return nil
}

// RemoveCmd removes a service container
type RemoveCmd struct {
	Image       string   `arg:"" help:"Image name or remote ref"`
	Instance    string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	WithVolumes bool     `name:"volumes" help:"Also remove named volumes"`
	KeepDeploy  bool     `name:"keep-deploy" help:"Keep deploy.yml entry for this image"`
	Env         []string `short:"e" long:"env" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	imageName := resolveImageName(c.Image)

	// Stop tunnel before removing container (best-effort)
	stopTunnelForImage(imageName)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(imageName, rt.RunEngine)
	engine := EngineBinary(runEngine)
	containerName := containerNameInstance(imageName, c.Instance)

	// Run pre_remove hooks (best-effort, before stopping)
	c.runPreRemoveHook(engine, containerName, imageName)

	if rt.RunMode == "quadlet" {
		svc := serviceNameInstance(imageName, c.Instance)
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

		qdir, err := quadletDir()
		if err != nil {
			return err
		}

		qpath := filepath.Join(qdir, quadletFilenameInstance(imageName, c.Instance))
		if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing quadlet file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

		// Stop companion services before removing (best-effort)
		stopTunnel := exec.Command("systemctl", "--user", "stop", tunnelServiceFilename(imageName))
		_ = stopTunnel.Run()
		stopEnc := exec.Command("systemctl", "--user", "stop", encServiceFilename(imageName))
		_ = stopEnc.Run()

		svcDir, svcDirErr := systemdUserDir()
		if svcDirErr == nil {
			tunnelPath := filepath.Join(svcDir, tunnelServiceFilename(imageName))
			if err := os.Remove(tunnelPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", tunnelPath)
			}
			encPath := filepath.Join(svcDir, encServiceFilename(imageName))
			if err := os.Remove(encPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", encPath)
			}
		}

		cmd := exec.Command("systemctl", "--user", "daemon-reload")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}

		fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")

		// Clear any lingering failed state for main + companion services (best-effort)
		for _, unit := range []string{
			svc,
			tunnelServiceFilename(imageName),
			encServiceFilename(imageName),
		} {
			rf := exec.Command("systemctl", "--user", "reset-failed", unit)
			_ = rf.Run()
		}

		if c.WithVolumes {
			removeVolumes(engine, imageName, c.Instance)
		}
		if !c.KeepDeploy && c.Instance == "" {
			cleanDeployEntry(imageName)
		}
		return nil
	}

	// Direct mode: stop + rm
	name := containerNameInstance(imageName, c.Instance)

	stop := exec.Command(engine, "stop", name)
	_ = stop.Run()

	rm := exec.Command(engine, "rm", name)
	_ = rm.Run()

	fmt.Fprintf(os.Stderr, "Removed container %s\n", name)

	if c.WithVolumes {
		removeVolumes(engine, imageName, c.Instance)
	}
	if !c.KeepDeploy && c.Instance == "" {
		cleanDeployEntry(imageName)
	}
	return nil
}

// runPreRemoveHook runs pre_remove hooks (best-effort).
// Tries images.yml first, then falls back to image labels.
func (c *RemoveCmd) runPreRemoveHook(engine, containerName, imageName string) {
	var hooks *HooksConfig

	// Try images.yml
	dir, err := os.Getwd()
	if err == nil {
		cfg, cfgErr := LoadConfig(dir)
		if cfgErr == nil {
			layers, scanErr := ScanAllLayersWithConfig(dir, cfg)
			if scanErr == nil {
				hooks = CollectHooks(cfg, layers, imageName)
			}
		}
	}

	// Fall back to image labels
	if hooks == nil {
		// Inspect the running container's image for labels
		imageRef := containerImage(engine, containerName)
		if imageRef != "" {
			meta, metaErr := ExtractMetadata(engine, imageRef)
			if metaErr == nil && meta != nil {
				hooks = meta.Hooks
			}
		}
	}

	if hooks == nil || hooks.PreRemove == "" {
		return
	}
	if err := RunHook(engine, containerName, hooks.PreRemove, c.Env); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: pre_remove hook failed: %v\n", err)
	}
}

// containerImage returns the image name for a running container (best-effort).
func containerImage(engine, containerName string) string {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", "--format", "{{.Config.Image}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// resolveImageName extracts the short image name from a ref that may be
// a local image name or a remote ref (github.com/org/repo/image[@version]).
func resolveImageName(image string) string {
	ref := StripURLScheme(image)
	if IsRemoteImageRef(ref) {
		return ParseRemoteRef(ref).Name
	}
	return image
}
