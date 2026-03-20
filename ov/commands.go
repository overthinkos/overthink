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

	var imageRef string
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount
	var security SecurityConfig
	var network string
	uid, gid := 1000, 1000 // defaults

	// Try images.yml first, fall back to image labels
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused")
		if err != nil {
			return err
		}
		uid, gid = resolved.UID, resolved.GID
		layers, err := ScanAllLayersWithConfig(dir, cfg)
		if err != nil {
			return err
		}
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
		ports = resolved.Ports
		network = resolved.Network
	} else {
		imageRef = resolveShellImageRef("", c.Image, c.Tag)
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
		podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
		if err := EnsureImage(imageRef, podmanRT); err != nil {
			return err
		}
	}

	// Resolve tunnel config
	var tunnelCfg *TunnelConfig
	if cfgErr == nil {
		resolved, resolveErr := cfg.ResolveImage(c.Image, "unused")
		if resolveErr == nil && resolved.Tunnel != nil {
			layers, scanErr := ScanAllLayersWithConfig(dir, cfg)
			if scanErr == nil {
				tunnelYAML := cfg.Images[c.Image].Tunnel
				if tunnelYAML == nil {
					tunnelYAML = cfg.Defaults.Tunnel
				}
				tunnelCfg = ResolveTunnelConfig(tunnelYAML, c.Image, resolved.DNS, layers, resolved.Layers, collectPortProtos(layers, resolved.Layers), resolved.Ports)
			}
		}
	} else {
		// Label path: tunnel from metadata
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil && meta.Tunnel != nil {
			dc, _ := LoadDeployConfig()
			MergeDeployOntoMetadata(meta, dc)
			tunnelCfg = TunnelConfigFromMetadata(meta)
		}
	}

	// Apply instance-specific volume naming
	volumes = InstanceVolumes(volumes, c.Image, c.Instance)

	// Resolve env vars
	var deployEnv []string
	var deployEnvFile string
	if cfgErr == nil {
		img := cfg.Images[c.Image]
		deployEnv = img.Env
		deployEnvFile = img.EnvFile
	} else {
		// Use env from labels (deploy.yml already merged above)
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil {
			dc, _ := LoadDeployConfig()
			MergeDeployOntoMetadata(meta, dc)
			deployEnv = meta.Env
		}
	}
	envVars, envErr := ResolveEnvVars(deployEnv, deployEnvFile, absWorkspace, c.EnvFile, c.Env)
	if envErr != nil {
		return envErr
	}

	// For quadlet, resolve env file to absolute path for EnvironmentFile=
	var quadletEnvFile string
	if c.EnvFile != "" {
		quadletEnvFile, _ = filepath.Abs(c.EnvFile)
	} else if deployEnvFile != "" {
		quadletEnvFile = expandHostHome(deployEnvFile)
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
		// Persist overrides to deploy.yml for quadlet restarts
		if saveErr := SavePortOverride(c.Image, ports); saveErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not save port override to deploy.yml: %v\n", saveErr)
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

	// Run post_enable hooks
	var hooks *HooksConfig
	if cfgErr == nil {
		layers, scanErr := ScanAllLayersWithConfig(dir, cfg)
		if scanErr == nil {
			hooks = CollectHooks(cfg, layers, c.Image)
		}
	} else {
		// Label path: hooks from metadata
		meta, metaErr := ExtractMetadata("podman", imageRef)
		if metaErr == nil && meta != nil {
			hooks = meta.Hooks
		}
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
	Image    string `arg:"" help:"Image name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *StatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
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

	// Resolve per-image engine
	dir, _ := os.Getwd()
	runEngine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)
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

	// Resolve per-image engine
	dir, _ := os.Getwd()
	runEngine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)
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

	var imageRef string
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	// Resolve per-image engine
	runEngine := ResolveImageEngineFromDir(dir, c.Image, rt.RunEngine)
	if cfgErr == nil {
		resolved, resolveErr := cfg.ResolveImage(c.Image, "unused")
		if resolveErr != nil {
			return resolveErr
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
	} else {
		// Label path
		engine := runEngine
		ref := fmt.Sprintf("%s:%s", c.Image, c.Tag)
		meta, metaErr := ExtractMetadata(engine, ref)
		if metaErr == nil && meta != nil && meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		} else {
			imageRef = ref
		}
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

	// Resolve per-image engine
	dir, _ := os.Getwd()
	runEngine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)
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

		if c.WithVolumes {
			removeVolumes(engine, imageName, c.Instance)
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
