package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ImageConfigCmd groups image configuration subcommands.
// Default subcommand (no keyword): full setup (quadlet + secrets + enc).
type ImageConfigCmd struct {
	Setup   ImageConfigSetupCmd   `cmd:"" default:"withargs" help:"Setup quadlet, secrets, and encrypted volumes"`
	Status  ImageConfigStatusCmd  `cmd:"status" help:"Show encrypted volume status"`
	Mount   ImageConfigMountCmd   `cmd:"mount" help:"Mount encrypted volumes"`
	Unmount ImageConfigUnmountCmd `cmd:"unmount" help:"Unmount encrypted volumes"`
	Passwd  ImageConfigPasswdCmd  `cmd:"passwd" help:"Change gocryptfs password"`
	Remove  ImageConfigRemoveCmd  `cmd:"remove" help:"Remove quadlet and disable service"`
}

// ImageConfigSetupCmd configures an image: generates quadlet, provisions secrets,
// initializes and mounts encrypted volumes.
type ImageConfigSetupCmd struct {
	Image       string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Workspace   string   `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag         string   `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Build       bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env         []string `short:"e" long:"env" help:"Set container env var (KEY=VALUE)"`
	EnvFile     string   `long:"env-file" help:"Load env vars from file"`
	Instance    string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	Port        []string `short:"p" help:"Remap host port (newHost:containerPort, e.g., 5901:5900)"`
	KeepMounted bool     `long:"keep-mounted" help:"Keep encrypted volumes mounted after setup"`
	Password    string   `long:"password" default:"auto" enum:"auto,manual" help:"auto: generate secrets (default), manual: prompt for each"`
	VolumeFlag  []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path]). Type: volume|bind|encrypted"`
	Bind        []string `long:"bind" help:"Shorthand: configure volume as bind mount (name or name=path)"`
	Encrypt     []string `long:"encrypt" help:"Shorthand: configure volume as encrypted (gocryptfs)"`
	AutoDetectFlags `embed:""`
}

func (c *ImageConfigSetupCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov config requires run_mode=quadlet (current: %s)", rt.RunMode)
	}

	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemoteConfig(rt, ref)
	}

	return c.runConfig(rt)
}

func (c *ImageConfigSetupCmd) runConfig(rt *ResolvedRuntime) error {
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
	security := meta.Security
	network := meta.Network

	// Parse volume flags into deploy volume configs (CLI > env > deploy.yml)
	deployVolumes := c.parseVolumeFlags()
	if len(deployVolumes) == 0 {
		deployVolumes = parseVolumeEnv(c.Image)
	}
	if len(deployVolumes) == 0 && dc != nil {
		if overlay, ok := dc.Images[c.Image]; ok {
			deployVolumes = overlay.Volumes
		}
	}

	// Resolve volume backing from labels + deploy config
	volumes, bindMounts := ResolveVolumeBacking(c.Image, meta.Volumes, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

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
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	if detected.AMDGPU && detected.AMDGFXVersion != "" {
		envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
	}

	// Resolve network (default to shared "ov" network)
	resolvedNetwork, netErr := ResolveNetwork(network, rt.RunEngine)
	if netErr != nil {
		return netErr
	}

	// Apply port overrides from --port flags
	if len(c.Port) > 0 {
		ports, err = ApplyPortOverrides(ports, c.Port)
		if err != nil {
			return err
		}
	}

	// Pre-flight port conflict check (warning for config, not hard error)
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, rt.RunEngine); len(conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "Warning: port conflicts detected:%s", FormatPortConflicts(conflicts, c.Image))
	}

	// Collect and provision secrets from image labels
	collectedSecrets := CollectSecretsFromLabels(c.Image, meta.Secrets)
	autoGen := c.Password == "auto"
	provisioned, fallbackEnv, err := ProvisionPodmanSecrets(rt.RunEngine, c.Image, c.Instance, collectedSecrets, autoGen)
	if err != nil {
		return err
	}
	for _, kv := range fallbackEnv {
		envVars = appendEnvUnique(envVars, kv)
	}

	// For quadlet, we use EnvironmentFile= instead of inline Environment= for file-sourced vars.
	// Only pass CLI -e vars as inline Environment= entries.
	ovBin, _ := os.Executable()
	store := DefaultCredentialStore()
	_, isKeyring := store.(*KeyringStore)

	qcfg := QuadletConfig{
		ImageName:       c.Image,
		ImageRef:        imageRef,
		Workspace:       absWorkspace,
		Ports:           ports,
		Volumes:         volumes,
		BindMounts:      bindMounts,
		GPU:             detected.GPU,
		BindAddress:     rt.BindAddress,
		Tunnel:          tunnelCfg,
		UID:             uid,
		GID:             gid,
		Env:             envVars,
		EnvFile:         quadletEnvFile,
		Instance:        c.Instance,
		Security:        security,
		Network:         resolvedNetwork,
		Status:          meta.Status,
		Info:            meta.Info,
		Entrypoint:      resolveEntrypointFromMeta(meta),
		Secrets:         provisioned,
		OvBin:           ovBin,
		EncryptedMounts: hasEncryptedBindMounts(bindMounts),
		KeyringBackend:  isKeyring,
	}

	// Suppress file-sourced env vars if using EnvFile (avoid duplication).
	// Keep CLI -e flags + auto-detected env vars as inline env.
	if quadletEnvFile != "" {
		qcfg.Env = c.Env
		if detected.AMDGPU && detected.AMDGFXVersion != "" {
			qcfg.Env = appendEnvUnique(qcfg.Env, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
		}
	}

	// Persist deployment state to deploy.yml (source of truth)
	saveDeployState(c.Image, SaveDeployStateInput{
		Workspace: absWorkspace,
		Ports:     ports,
		Env:       c.Env,
		EnvFile:   quadletEnvFile,
		Network:   resolvedNetwork,
		Security:  &security,
		Volumes:   deployVolumes,
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
	if err := os.WriteFile(qpath, []byte(content), 0600); err != nil {
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

		// Setup: create tunnel, write cloudflared config, route DNS
		if _, _, setupErr := cloudflareTunnelSetup(*tunnelCfg); setupErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", setupErr)
		}
	}

	// Clean up stale enc service from previous ov versions
	if svcDir, svcErr := systemdUserDir(); svcErr == nil {
		encPath := filepath.Join(svcDir, encServiceFilename(c.Image))
		if _, statErr := os.Stat(encPath); statErr == nil {
			os.Remove(encPath)
			fmt.Fprintf(os.Stderr, "Removed stale %s\n", encPath)
		}
	}

	reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := reloadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")

	// Enable tunnel service so it auto-starts with the container
	if tunnelCfg != nil && tunnelCfg.Provider == "cloudflare" {
		enableCmd := exec.Command("systemctl", "--user", "enable", tunnelServiceFilename(c.Image))
		if output, err := enableCmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not enable tunnel service: %v\n%s", err, strings.TrimSpace(string(output)))
		}
	}

	// Initialize and mount encrypted volumes
	if hasEncryptedBindMounts(bindMounts) {
		if err := ensureEncryptedMounts(c.Image, autoGen); err != nil {
			return fmt.Errorf("setting up encrypted volumes: %w", err)
		}
		// Unmount after setup unless --keep-mounted
		if !c.KeepMounted {
			if err := encUnmount(c.Image, ""); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not unmount encrypted volumes: %v\n", err)
			}
		}
	}

	// Run post_enable hooks from image labels
	var hooks *HooksConfig
	if meta != nil {
		hooks = meta.Hooks
	}
	if hooks != nil && hooks.PostEnable != "" {
		ctrName := containerNameInstance(c.Image, c.Instance)
		svc := serviceNameInstance(c.Image, c.Instance)

		start := exec.Command("systemctl", "--user", "start", svc)
		start.Stdout = os.Stderr
		start.Stderr = os.Stderr
		if err := start.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to start %s for post_enable hook: %v\n", svc, err)
		} else {
			engine := EngineBinary(rt.RunEngine)
			if err := RunHook(engine, ctrName, hooks.PostEnable, c.Env); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: post_enable hook failed: %v\n", err)
			}
		}
	}

	return nil
}

func (c *ImageConfigSetupCmd) runRemoteConfig(rt *ResolvedRuntime, ref string) error {
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

	allVolumes, err := ctx.CollectVolumes()
	if err != nil {
		return err
	}

	// Parse volume flags for remote config
	deployVolumes := c.parseVolumeFlags()
	if len(deployVolumes) == 0 {
		deployVolumes = parseVolumeEnv(ctx.ImageName)
	}
	volumes, bindMounts := ResolveVolumeBacking(ctx.ImageName, allVolumes, deployVolumes, ctx.Resolved.Home, rt.EncryptedStoragePath, rt.VolumesPath)

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

	// Resolve entrypoint for quadlet
	remoteEntrypoint := []string{"sleep", "infinity"}
	if ctx.Layers != nil {
		resolvedLayers, _ := ResolveLayerOrder(ctx.Resolved.Layers, ctx.Layers, nil)
		remoteEntrypoint = resolveEntrypoint(ctx.Resolved.InitConfig, ctx.Layers, resolvedLayers, ctx.Resolved.Bootc)
	}

	remoteOvBin, _ := os.Executable()
	remoteStore := DefaultCredentialStore()
	_, remoteIsKeyring := remoteStore.(*KeyringStore)

	qcfg := QuadletConfig{
		ImageName:       ctx.ImageName,
		ImageRef:        ctx.ImageRef,
		Workspace:       absWorkspace,
		Ports:           ctx.Resolved.Ports,
		Volumes:         volumes,
		BindMounts:      bindMounts,
		GPU:             detected.GPU,
		BindAddress:     rt.BindAddress,
		UID:             ctx.Resolved.UID,
		GID:             ctx.Resolved.GID,
		Env:             envVars,
		Instance:        c.Instance,
		Security:        security,
		Network:         resolvedNetwork,
		Status:          ctx.Resolved.Status,
		Info:            ctx.Resolved.Info,
		Entrypoint:      remoteEntrypoint,
		OvBin:           remoteOvBin,
		EncryptedMounts: hasEncryptedBindMounts(bindMounts),
		KeyringBackend:  remoteIsKeyring,
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

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
	return nil
}

// ImageConfigStatusCmd shows encrypted volume status.
type ImageConfigStatusCmd struct {
	Image string `arg:"" help:"Image name"`
}

func (c *ImageConfigStatusCmd) Run() error {
	return encStatus(c.Image)
}

// ImageConfigMountCmd mounts encrypted volumes.
type ImageConfigMountCmd struct {
	Image  string `arg:"" help:"Image name"`
	Volume string `long:"volume" help:"Only mount this volume (by name)"`
}

func (c *ImageConfigMountCmd) Run() error {
	return encMount(c.Image, c.Volume)
}

// ImageConfigUnmountCmd unmounts encrypted volumes.
type ImageConfigUnmountCmd struct {
	Image  string `arg:"" help:"Image name"`
	Volume string `long:"volume" help:"Only unmount this volume (by name)"`
}

func (c *ImageConfigUnmountCmd) Run() error {
	return encUnmount(c.Image, c.Volume)
}

// ImageConfigPasswdCmd changes the gocryptfs password.
type ImageConfigPasswdCmd struct {
	Image string `arg:"" help:"Image name"`
}

func (c *ImageConfigPasswdCmd) Run() error {
	return encPasswd(c.Image)
}

// ImageConfigRemoveCmd removes a quadlet service (replaces ov disable).
type ImageConfigRemoveCmd struct {
	Image    string `arg:"" help:"Image name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ImageConfigRemoveCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov config remove requires run_mode=quadlet (current: %s)", rt.RunMode)
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

// parseVolumeFlags converts --volume, --bind, --encrypt flags into DeployVolumeConfig.
func (c *ImageConfigSetupCmd) parseVolumeFlags() []DeployVolumeConfig {
	var configs []DeployVolumeConfig
	seen := make(map[string]bool)

	// Parse --volume name:type[:path]
	for _, v := range c.VolumeFlag {
		parts := strings.SplitN(v, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		// Normalize: accept both "encrypt" and "encrypted"
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		if !seen[dv.Name] {
			configs = append(configs, dv)
			seen[dv.Name] = true
		}
	}

	// Parse --bind name or name=path
	for _, b := range c.Bind {
		if seen[b] || seen[strings.SplitN(b, "=", 2)[0]] {
			continue
		}
		if idx := strings.IndexByte(b, '='); idx >= 0 {
			name := b[:idx]
			host := b[idx+1:]
			configs = append(configs, DeployVolumeConfig{Name: name, Type: "bind", Host: host})
			seen[name] = true
		} else {
			configs = append(configs, DeployVolumeConfig{Name: b, Type: "bind"})
			seen[b] = true
		}
	}

	// Parse --encrypt name
	for _, e := range c.Encrypt {
		if !seen[e] {
			configs = append(configs, DeployVolumeConfig{Name: e, Type: "encrypted"})
			seen[e] = true
		}
	}

	return configs
}

// parseVolumeEnv parses OV_VOLUMES_<IMAGE> env var into DeployVolumeConfig.
func parseVolumeEnv(imageName string) []DeployVolumeConfig {
	envKey := "OV_VOLUMES_" + strings.ToUpper(strings.ReplaceAll(imageName, "-", "_"))
	envVal := os.Getenv(envKey)
	if envVal == "" {
		return nil
	}

	var configs []DeployVolumeConfig
	for _, entry := range strings.Split(envVal, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 3)
		dv := DeployVolumeConfig{Name: parts[0]}
		if len(parts) >= 2 {
			dv.Type = parts[1]
		}
		if len(parts) >= 3 {
			dv.Host = parts[2]
		}
		if dv.Type == "" {
			dv.Type = "volume"
		}
		if dv.Type == "encrypt" {
			dv.Type = "encrypted"
		}
		configs = append(configs, dv)
	}
	return configs
}
