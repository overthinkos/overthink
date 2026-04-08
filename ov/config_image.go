package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	Image       string   `arg:"" optional:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
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
	Seed        bool     `long:"seed" default:"true" negatable:"" help:"Seed bind-backed volumes with data from image (default: true)"`
	ForceSeed   bool     `long:"force-seed" help:"Re-seed even if target directory is not empty"`
	DataFrom    string   `long:"data-from" help:"Seed data from this data image instead of the target image"`
	UpdateAll    bool     `long:"update-all" help:"Regenerate quadlets for all other deployed images to pick up env_provides changes"`
	Sidecar      []string `long:"sidecar" help:"Attach sidecar (from built-in templates, e.g. 'tailscale')"`
	ListSidecars bool     `long:"list-sidecars" help:"List available sidecar templates and exit"`
	AutoDetectFlags `embed:""`
}

func (c *ImageConfigSetupCmd) Run() error {
	if c.ListSidecars {
		cfg, err := LoadEmbeddedSidecarConfig()
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Sidecars))
		for name := range cfg.Sidecars {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sc := cfg.Sidecars[name]
			fmt.Printf("%-20s %s\n", name, sc.Description)
		}
		return nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov config requires run_mode=quadlet (current: %s)", rt.RunMode)
	}

	if c.Image == "" {
		return fmt.Errorf("image name is required")
	}

	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemoteConfig(rt, ref)
	}

	return c.runConfig(rt)
}

func (c *ImageConfigSetupCmd) runConfig(rt *ResolvedRuntime) error {
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
	MergeDeployOntoMetadata(meta, dc, c.Instance)

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
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
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

	// Inject provides BEFORE env resolution so this image's own provides
	// (pod case) and other images' provides are available in the quadlet.
	if meta != nil && len(meta.EnvProvides) > 0 {
		if _, injErr := injectEnvProvides(c.Image, c.Instance, meta.EnvProvides); injErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not inject env_provides: %v\n", injErr)
		}
	}
	if meta != nil && len(meta.MCPProvides) > 0 {
		if _, injErr := injectMCPProvides(c.Image, c.Instance, meta.MCPProvides); injErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not inject mcp_provides: %v\n", injErr)
		}
	}
	// Reload deploy config after injection to pick up newly injected provides
	dc, _ = LoadDeployConfig()

	// Resolve env vars from global provides + labels + deploy.yml + CLI
	ctrName := containerNameInstance(c.Image, c.Instance)
	globalEnv := dc.GlobalEnvForImage(c.Image, ctrName)
	envVars, envErr := ResolveEnvVars(globalEnv, meta.Env, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
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
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok && overlay.EnvFile != "" {
			quadletEnvFile = expandHostHome(overlay.EnvFile)
		}
	}
	// Also check workspace .env for quadlet EnvironmentFile
	if quadletEnvFile == "" {
		if wsHost := workspaceBindHost(bindMounts); wsHost != "" {
			wsEnvPath := filepath.Join(wsHost, ".env")
			if _, statErr := os.Stat(wsEnvPath); statErr == nil {
				quadletEnvFile = wsEnvPath
			}
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
	// Determine keyring backend from configured secret_backend setting, not runtime
	// probe. At boot or when keyring is locked, DefaultCredentialStore() may return
	// ConfigFileStore even though the user configured "keyring". The quadlet must
	// reflect the intended backend so TimeoutStartSec=0 and WantedBy are correct.
	backend := resolveSecretBackend()
	isKeyring := backend == "keyring" || backend == "auto" || backend == ""

	// Resolve sidecars: embedded templates + deploy.yml + --sidecar flags
	var deploySidecars map[string]SidecarDef
	if dc != nil {
		if overlay, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deploySidecars = overlay.Sidecars
		}
	}
	// Merge --sidecar flags into deploy sidecars
	for _, scName := range c.Sidecar {
		if deploySidecars == nil {
			deploySidecars = make(map[string]SidecarDef)
		}
		if _, ok := deploySidecars[scName]; !ok {
			deploySidecars[scName] = SidecarDef{} // empty override, inherits from template
		}
	}

	var resolvedSidecars []ResolvedSidecar
	var mergedSidecarDefs map[string]SidecarDef
	if len(deploySidecars) > 0 {
		// Route CLI -e flags: sidecar-related env vars go to the sidecar, not the app
		sidecarEnvKeys := SidecarEnvKeys(deploySidecars)
		var appEnv, sidecarEnvOverrides []string
		for _, e := range c.Env {
			key := e
			if idx := strings.IndexByte(e, '='); idx >= 0 {
				key = e[:idx]
			}
			if scName, ok := sidecarEnvKeys[key]; ok {
				// Route to sidecar
				if deploySidecars[scName].Env == nil {
					def := deploySidecars[scName]
					def.Env = make(map[string]string)
					deploySidecars[scName] = def
				}
				def := deploySidecars[scName]
				if idx := strings.IndexByte(e, '='); idx >= 0 {
					def.Env[key] = e[idx+1:]
				}
				deploySidecars[scName] = def
				sidecarEnvOverrides = append(sidecarEnvOverrides, e)
			} else {
				appEnv = append(appEnv, e)
			}
		}
		// Replace c.Env with app-only env vars (sidecar vars saved to deploy.yml)
		c.Env = appEnv

		// Resolve: embedded templates + deploy.yml overrides
		var resolveErr error
		mergedSidecarDefs, resolveErr = ResolveSidecarsForConfig(deploySidecars)
		if resolveErr != nil {
			return fmt.Errorf("resolving sidecars: %w", resolveErr)
		}
		if len(mergedSidecarDefs) > 0 {
			resolvedSidecars = ResolveSidecars(mergedSidecarDefs, c.Image, c.Instance)
		}

		// Log routed env vars
		if len(sidecarEnvOverrides) > 0 {
			for _, e := range sidecarEnvOverrides {
				key := e
				if idx := strings.IndexByte(e, '='); idx >= 0 {
					key = e[:idx]
				}
				scName := sidecarEnvKeys[key]
				fmt.Fprintf(os.Stderr, "Routed %s to sidecar %s\n", key, scName)
			}
		}
	}

	// Provision sidecar secrets as podman secrets
	for i, sc := range resolvedSidecars {
		if len(sc.Secrets) > 0 {
			scProvisioned, scFallback, scErr := ProvisionPodmanSecrets(rt.RunEngine, c.Image, c.Instance, sc.Secrets, autoGen)
			if scErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not provision sidecar %s secrets: %v\n", sc.Name, scErr)
			}
			resolvedSidecars[i].Secrets = scProvisioned
			for _, kv := range scFallback {
				envVars = appendEnvUnique(envVars, kv)
			}
		}
	}

	// When sidecars are present, set PodName to enable pod mode
	podName := ""
	if len(resolvedSidecars) > 0 {
		podName = PodNameInstance(c.Image, c.Instance)
	}

	qcfg := QuadletConfig{
		ImageName:       c.Image,
		ImageRef:        imageRef,
		Home:            meta.Home,
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
		PodName:         podName,
		Sidecars:        resolvedSidecars,
	}

	// Suppress file-sourced env vars if using EnvFile (avoid duplication).
	// Keep CLI -e flags + provides env vars + auto-detected env vars as inline env.
	// Provides vars (from env_provides) are NOT in the env file — they're resolved
	// at ov config time from deploy.yml and must remain as inline Environment= entries.
	if quadletEnvFile != "" {
		qcfg.Env = append([]string{}, globalEnv...)
		qcfg.Env = append(qcfg.Env, c.Env...)
		if detected.AMDGPU && detected.AMDGFXVersion != "" {
			qcfg.Env = appendEnvUnique(qcfg.Env, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
		}
	}

	// Persist deployment state to deploy.yml (source of truth)
	saveDeployState(c.Image, c.Instance, SaveDeployStateInput{
		Ports:     ports,
		Env:       c.Env,
		EnvFile:   quadletEnvFile,
		Network:   resolvedNetwork,
		Security:  &security,
		Volumes:   deployVolumes,
		Sidecars:  deploySidecars,
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

	// Write pod and sidecar files when sidecars are configured
	if len(resolvedSidecars) > 0 {
		// Generate and write .pod file
		podContent := generatePodQuadlet(qcfg)
		podPath := filepath.Join(qdir, podQuadletFilenameInstance(c.Image, c.Instance))
		if err := os.WriteFile(podPath, []byte(podContent), 0600); err != nil {
			return fmt.Errorf("writing pod file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", podPath)

		// Generate and write sidecar .container files
		for _, sc := range resolvedSidecars {
			scContent := generateSidecarQuadlet(sc, podName)
			scPath := filepath.Join(qdir, sidecarQuadletFilenameInstance(c.Image, c.Instance, sc.Name))
			if err := os.WriteFile(scPath, []byte(scContent), 0600); err != nil {
				return fmt.Errorf("writing sidecar file for %s: %w", sc.Name, err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", scPath)
		}
	}

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
		if err := ensureEncryptedMounts(c.Image, c.Instance, autoGen); err != nil {
			return fmt.Errorf("setting up encrypted volumes: %w", err)
		}
		// Unmount after setup unless --keep-mounted
		if !c.KeepMounted {
			if err := encUnmount(c.Image, c.Instance, ""); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not unmount encrypted volumes: %v\n", err)
			}
		}
	}

	// Reload deploy config after saveDeployState wrote the volumes
	dc, _ = LoadDeployConfig()

	// Provision data from image staging (/data/) into bind-backed volumes
	if c.Seed && len(bindMounts) > 0 {
		dataMeta := meta
		dataRef := imageRef
		dataEngine := ResolveImageEngineForDeploy(c.Image, c.Instance, rt.RunEngine)

		// Use external data image if --data-from specified
		if c.DataFrom != "" {
			dataRef = c.DataFrom
			if !strings.Contains(dataRef, ":") {
				dataRef += ":latest"
			}
			dm, err := ExtractMetadata(dataEngine, dataRef)
			if err != nil {
				return fmt.Errorf("extracting metadata from data image %s: %w", dataRef, err)
			}
			if dm == nil {
				return fmt.Errorf("data image %s has no embedded metadata", dataRef)
			}
			dataMeta = dm
		}

		if len(dataMeta.DataEntries) > 0 {
			// Determine provisioning mode
			mode := DataProvisionInitial
			if c.ForceSeed {
				mode = DataProvisionForce
			} else {
				// Check if already seeded (idempotent re-run)
				allSeeded := true
				for _, dvc := range deployVolumes {
					if dvc.DataSeeded {
						continue
					}
					allSeeded = false
					break
				}
				if allSeeded && len(deployVolumes) > 0 && !c.ForceSeed {
					// Skip if all volumes already seeded and no force
					fmt.Fprintln(os.Stderr, "Data already provisioned (use --force-seed to re-provision)")
					goto skipDataProvision
				}
			}

			fmt.Fprintln(os.Stderr, "Provisioning data into bind-backed volumes...")
			seeded, err := provisionData(dataEngine, dataRef, dataMeta, bindMounts, mode)
			if err != nil {
				return fmt.Errorf("data provisioning: %w", err)
			}

			// Update deploy.yml with seeded state
			if seeded > 0 {
				if dc == nil {
					dc = &DeployConfig{Images: make(map[string]DeployImageConfig)}
				}
				imgDeploy := dc.Images[deployKey(c.Image, c.Instance)]
				for i := range imgDeploy.Volumes {
					for _, entry := range dataMeta.DataEntries {
						if imgDeploy.Volumes[i].Name == entry.Volume {
							imgDeploy.Volumes[i].DataSeeded = true
							imgDeploy.Volumes[i].DataSource = dataRef
						}
					}
				}
				dc.Images[deployKey(c.Image, c.Instance)] = imgDeploy
				if err := SaveDeployConfig(dc); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not save data seeded state to deploy.yml: %v\n", err)
				}
				fmt.Fprintf(os.Stderr, "Provisioned data for %d volume(s)\n", seeded)
			}
		}
	}
skipDataProvision:

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

	// Regenerate quadlets for all other deployed images if --update-all
	if c.UpdateAll {
		if err := updateAllDeployedQuadlets(rt, deployKey(c.Image, c.Instance)); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update all quadlets: %v\n", err)
		}
	}

	// Warn about missing env_requires vars
	if meta != nil && len(meta.EnvRequires) > 0 {
		warnMissingEnvRequires(c.Image, meta.EnvRequires, envVars)
	}

	// Warn about missing mcp_requires servers
	if meta != nil && len(meta.MCPRequires) > 0 {
		dc, _ := LoadDeployConfig()
		var mcpServers []MCPProvidesEntry
		if dc != nil && dc.Provides != nil {
			mcpServers = podAwareMCPProvides(dc.Provides.MCP, c.Image, containerNameInstance(c.Image, c.Instance))
		}
		warnMissingMCPRequires(c.Image, meta.MCPRequires, mcpServers)
	}

	return nil
}

func (c *ImageConfigSetupCmd) runRemoteConfig(rt *ResolvedRuntime, ref string) error {
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

	// Resolve env vars with global env
	dc, _ := LoadDeployConfig()
	remoteCtrName := containerNameInstance(ctx.ImageName, "")
	remoteGlobalEnv := dc.GlobalEnvForImage(ctx.ImageName, remoteCtrName)
	envVars, envErr := ResolveEnvVars(remoteGlobalEnv, nil, "", workspaceBindHost(bindMounts), c.EnvFile, c.Env)
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
	remoteBackend := resolveSecretBackend()
	remoteIsKeyring := remoteBackend == "keyring" || remoteBackend == "auto" || remoteBackend == ""

	qcfg := QuadletConfig{
		ImageName:       ctx.ImageName,
		ImageRef:        ctx.ImageRef,
		Home:            ctx.Resolved.Home,
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
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ImageConfigStatusCmd) Run() error {
	return encStatus(c.Image, c.Instance)
}

// ImageConfigMountCmd mounts encrypted volumes.
type ImageConfigMountCmd struct {
	Image    string `arg:"" help:"Image name"`
	Volume   string `long:"volume" help:"Only mount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ImageConfigMountCmd) Run() error {
	return encMount(c.Image, c.Instance, c.Volume)
}

// ImageConfigUnmountCmd unmounts encrypted volumes.
type ImageConfigUnmountCmd struct {
	Image    string `arg:"" help:"Image name"`
	Volume   string `long:"volume" help:"Only unmount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ImageConfigUnmountCmd) Run() error {
	return encUnmount(c.Image, c.Instance, c.Volume)
}

// ImageConfigPasswdCmd changes the gocryptfs password.
type ImageConfigPasswdCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ImageConfigPasswdCmd) Run() error {
	return encPasswd(c.Image, c.Instance)
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

	// Also disable pod and sidecar services (best-effort)
	podSvc := PodNameInstance(imageName, c.Instance) + "-pod.service"
	disablePod := exec.Command("systemctl", "--user", "disable", "--now", podSvc)
	_ = disablePod.Run()

	// Disable sidecar services by scanning quadlet directory
	if qdir, qErr := quadletDir(); qErr == nil {
		podPrefix := PodNameInstance(imageName, c.Instance) + "-"
		if entries, dErr := os.ReadDir(qdir); dErr == nil {
			for _, entry := range entries {
				name := entry.Name()
				if strings.HasPrefix(name, podPrefix) && strings.HasSuffix(name, ".container") {
					scSvc := strings.TrimSuffix(name, ".container") + ".service"
					disableSc := exec.Command("systemctl", "--user", "disable", "--now", scSvc)
					_ = disableSc.Run()
				}
			}
		}
	}

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

// injectEnvProvides resolves env_provides templates and stores them in deploy.yml provides.env.
// Returns true if any env vars were added or changed.
func injectEnvProvides(imageName, instance string, envProvides map[string]string) (bool, error) {
	if len(envProvides) == 0 {
		return false, nil
	}

	dc, _ := LoadDeployConfig()
	if dc == nil {
		dc = &DeployConfig{Images: make(map[string]DeployImageConfig)}
	}
	if dc.Provides == nil {
		dc.Provides = &ProvidesConfig{}
	}

	ctrName := containerNameInstance(imageName, instance)
	changed := false

	// Sort keys for deterministic output
	keys := sortedStringMapKeys(envProvides)
	for _, key := range keys {
		tmpl := envProvides[key]
		value := resolveTemplate(tmpl, ctrName)
		source := deployKey(imageName, instance)
		resolved := EnvProvidesEntry{
			Name:   key,
			Value:  value,
			Source: source,
		}

		// Check if already set to same value (dedup by name+source)
		found := false
		for i, existing := range dc.Provides.Env {
			if existing.Name == key && existing.Source == source {
				if existing.Value == value {
					found = true
					break
				}
				dc.Provides.Env[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.Env = append(dc.Provides.Env, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "Env provides injected: %s=%s\n", key, value)
		}
	}

	if changed {
		if err := SaveDeployConfig(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// injectMCPProvides resolves mcp_provides templates and adds them to deploy.yml.
// Returns true if any servers were added or changed.
func injectMCPProvides(imageName, instance string, mcpProvides []MCPServerYAML) (bool, error) {
	if len(mcpProvides) == 0 {
		return false, nil
	}

	dc, _ := LoadDeployConfig()
	if dc == nil {
		dc = &DeployConfig{Images: make(map[string]DeployImageConfig)}
	}
	if dc.Provides == nil {
		dc.Provides = &ProvidesConfig{}
	}

	ctrName := containerNameInstance(imageName, instance)
	source := deployKey(imageName, instance)
	changed := false

	// Remove stale entries from this source (handles name changes on re-config)
	var cleaned []MCPProvidesEntry
	for _, e := range dc.Provides.MCP {
		if e.Source != source {
			cleaned = append(cleaned, e)
		}
	}
	if len(cleaned) != len(dc.Provides.MCP) {
		dc.Provides.MCP = cleaned
	}

	for _, mcp := range mcpProvides {
		url := resolveTemplate(mcp.URL, ctrName)
		transport := mcp.Transport
		if transport == "" {
			transport = "http"
		}
		// Disambiguate MCP name for instances so consumers see unique servers
		mcpName := mcp.Name
		if instance != "" {
			mcpName = mcp.Name + "-" + instance
		}
		resolved := MCPProvidesEntry{
			Name:      mcpName,
			URL:       url,
			Transport: transport,
			Source:    source,
		}

		// Check if already set to same value
		found := false
		for i, existing := range dc.Provides.MCP {
			if existing.Name == mcpName && existing.Source == source {
				if existing.URL == resolved.URL && existing.Transport == resolved.Transport {
					found = true
					break
				}
				dc.Provides.MCP[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.MCP = append(dc.Provides.MCP, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "MCP provides injected: %s → %s\n", mcpName, url)
		}
	}

	if changed {
		if err := SaveDeployConfig(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// warnMissingMCPRequires checks resolved MCP servers against required MCP dependencies
// and prints warnings for any that are missing.
func warnMissingMCPRequires(imageName string, requires []EnvDependency, mcpServers []MCPProvidesEntry) {
	resolved := make(map[string]bool, len(mcpServers))
	for _, s := range mcpServers {
		resolved[s.Name] = true
	}
	for _, dep := range requires {
		if !resolved[dep.Name] {
			desc := dep.Description
			if desc != "" {
				desc = " (" + desc + ")"
			}
			fmt.Fprintf(os.Stderr, "Warning: %s requires MCP server %s%s — not available\n", imageName, dep.Name, desc)
		}
	}
}

// warnMissingEnvRequires checks resolved env vars against required env dependencies
// and prints warnings for any that are missing.
func warnMissingEnvRequires(imageName string, requires []EnvDependency, resolvedEnv []string) {
	// Build set of resolved env var names
	resolved := make(map[string]bool, len(resolvedEnv))
	for _, e := range resolvedEnv {
		if k := envKey(e); k != "" {
			resolved[k] = true
		}
	}

	for _, dep := range requires {
		if !resolved[dep.Name] {
			desc := dep.Description
			if desc != "" {
				desc = " (" + desc + ")"
			}
			fmt.Fprintf(os.Stderr, "Warning: %s requires %s%s — not set\n", imageName, dep.Name, desc)
		}
	}
}

// updateAllDeployedQuadlets regenerates quadlets for all other deployed images
// to pick up global env changes. Lightweight: only regenerates the quadlet file,
// does NOT re-provision secrets, encrypted volumes, or data.
func updateAllDeployedQuadlets(rt *ResolvedRuntime, skipImage string) error {
	dc, err := LoadDeployConfig()
	if err != nil || dc == nil {
		return nil
	}

	var updated []string
	for key := range dc.Images {
		if key == skipImage {
			continue
		}
		imageName, instance := parseDeployKey(key)

		// Check if quadlet file exists (only update deployed images)
		qdir, err := quadletDir()
		if err != nil {
			continue
		}
		qpath := filepath.Join(qdir, quadletFilenameInstance(imageName, instance))
		if _, err := os.Stat(qpath); os.IsNotExist(err) {
			continue
		}

		// Extract metadata from base image (not the deploy key)
		imageRef := resolveShellImageRef("", imageName, "latest")
		meta, err := ExtractMetadata("podman", imageRef)
		if err != nil || meta == nil {
			fmt.Fprintf(os.Stderr, "Warning: could not read metadata for %s, skipping quadlet update\n", key)
			continue
		}

		// Apply deploy.yml overrides (instance-aware)
		MergeDeployOntoMetadata(meta, dc, instance)

		// Resolve env vars with updated global env
		updateCtrName := containerNameInstance(imageName, instance)
		globalEnv := dc.GlobalEnvForImage(imageName, updateCtrName)
		envVars, err := ResolveEnvVars(globalEnv, meta.Env, "", "", "", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not resolve env for %s: %v\n", key, err)
			continue
		}

		// Resolve network
		resolvedNetwork, _ := ResolveNetwork(meta.Network, rt.RunEngine)

		// Detect devices for GPU config
		detected := DetectHostDevices()

		// Build volumes from metadata
		var deployVolumes []DeployVolumeConfig
		if overlay, ok := dc.Images[key]; ok {
			deployVolumes = overlay.Volumes
		}
		volumes, bindMounts := ResolveVolumeBacking(imageName, meta.Volumes, deployVolumes, meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

		// Resolve env file
		var quadletEnvFile string
		if overlay, ok := dc.Images[key]; ok && overlay.EnvFile != "" {
			quadletEnvFile = expandHostHome(overlay.EnvFile)
		}
		if quadletEnvFile == "" {
			if wsHost := workspaceBindHost(bindMounts); wsHost != "" {
				wsEnvPath := filepath.Join(wsHost, ".env")
				if _, statErr := os.Stat(wsEnvPath); statErr == nil {
					quadletEnvFile = wsEnvPath
				}
			}
		}

		// Merge security
		security := meta.Security
		if !security.Privileged {
			security.Devices = appendUnique(security.Devices, detected.Devices...)
			if detected.AMDGPU {
				security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
			}
		}
		if detected.AMDGPU && detected.AMDGFXVersion != "" {
			envVars = appendEnvUnique(envVars, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
		}

		// Collect secrets from labels (for quadlet Secret= directives)
		provisioned := CollectSecretsFromLabels(imageName, meta.Secrets)

		ovBin, _ := os.Executable()
		backend := resolveSecretBackend()
		isKeyring := backend == "keyring" || backend == "auto" || backend == ""

		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, imageName, "latest")
		}

		qcfg := QuadletConfig{
			ImageName:       imageName,
			ImageRef:        imageRef,
			Home:            meta.Home,
			Ports:           meta.Ports,
			Volumes:         volumes,
			BindMounts:      bindMounts,
			GPU:             detected.GPU,
			BindAddress:     rt.BindAddress,
			UID:             meta.UID,
			GID:             meta.GID,
			Env:             envVars,
			EnvFile:         quadletEnvFile,
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

		// Suppress file-sourced env vars if using EnvFile.
		// Keep provides env vars — they're not in the env file.
		if quadletEnvFile != "" {
			qcfg.Env = append([]string{}, globalEnv...)
			if detected.AMDGPU && detected.AMDGFXVersion != "" {
				qcfg.Env = appendEnvUnique(qcfg.Env, "HSA_OVERRIDE_GFX_VERSION="+detected.AMDGFXVersion)
			}
		}

		content := generateQuadlet(qcfg)
		if err := os.WriteFile(qpath, []byte(content), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not update quadlet for %s: %v\n", key, err)
			continue
		}

		updated = append(updated, key)
		fmt.Fprintf(os.Stderr, "Updated quadlet for %s\n", key)
	}

	if len(updated) > 0 {
		reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
		if output, err := reloadCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
		fmt.Fprintf(os.Stderr, "Restart affected services to pick up changes\n")
	}

	return nil
}

// sortedStringMapKeys returns the keys of a string map in sorted order.
func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
