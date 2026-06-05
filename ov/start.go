package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// StartCmd launches a container with supervisord in the background
type StartCmd struct {
	Image           string   `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Tag             string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the org.overthinkos.version OCI label)"`
	Build           bool     `long:"build" help:"Force local build instead of pulling from registry"`
	Env             []string `short:"e" long:"env" sep:"none" help:"Set container env var (direct mode only)"`
	EnvFile         string   `long:"env-file" help:"Load env vars from file (direct mode only)"`
	Instance        string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
	Port            []string `short:"p" help:"Remap host port (direct mode only)"`
	VolumeFlag      []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind            []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	AutoDetectFlags `embed:""`
}

func (c *StartCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `ov image pull`.
	if IsRemoteImageRef(StripURLScheme(c.Image)) {
		return fmt.Errorf("remote refs are not accepted here; run 'ov image pull %s' first, then 'ov start <image-name>'", c.Image)
	}
	c.Image, c.Instance = canonicalizeDeployArg(c.Image, c.Instance)

	// Resource arbitration: starting a pod deploy that claims requires_exclusive
	// preempts the running holders of that resource (persistent lease —
	// released by `ov stop`/`ov remove`). No-op when gated by an outer
	// orchestrator or when this deploy claims nothing exclusive. See
	// ov/preempt.go.
	if dc := loadDeployConfigForRead("ov start"); dc != nil {
		key := deployKey(c.Image, c.Instance)
		if node, ok := dc.Deploy[key]; ok && len(node.RequiredExclusive()) > 0 {
			if _, perr := acquireExclusiveForClaimant(key, node, false); perr != nil {
				return perr
			}
		}
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

	// Load deploy.yml for volume backing config + later use (env merge,
	// sidecar check, agent forwarding, metadata overlay).
	dc := loadDeployConfigForRead("ov start")
	var deployVolumes []DeployVolumeConfig
	if overlay, ok := dc.Lookup(c.Image, c.Instance); ok {
		deployVolumes = overlay.Volume
	}

	// Resolve the deploy key to its declared image short-name via THE shared
	// resolver (deploy.go) — the same one ov config / shell / eval live use,
	// so no command diverges when key != image (kind:eval beds, Pattern B).
	// c.Image stays the deploy-KEY for container / quadlet / overlay lookups;
	// only the image ref uses the resolved name.
	deployImageName := resolveDeployImageName(c.Image, c.Instance)
	// Resolve from image labels (+ deploy.yml overlay). No image.yml.
	imageRef := resolveShellImageRef("", deployImageName, c.Tag)
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
	MergeDeployOntoMetadata(meta, dc, c.Image, c.Instance)

	// Sidecars require quadlet mode (pod networking is only available via quadlet)
	if overlay, ok := dc.Lookup(c.Image, c.Instance); ok && len(overlay.Sidecar) > 0 {
		return fmt.Errorf("image %s has sidecars configured in deploy.yml; use 'ov config %s && ov start %s' (sidecars require quadlet mode)", c.Image, c.Image, c.Image)
	}

	uid := meta.UID
	gid := meta.GID
	home := meta.Home
	ports := meta.Port
	security := meta.Security
	network := meta.Network
	entrypoint := resolveEntrypointFromMeta(meta)

	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(c.Image, c.Instance, meta.Volume, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	envAccepts := meta.EnvAccept
	envRequires := meta.EnvRequire
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, deployImageName, c.Tag)
	}

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
	startGlobalEnv := dc.GlobalEnvForImage(deployKey(c.Image, c.Instance), startCtrName, startAccepted)
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
		// SetPorts: true because this branch only runs when len(c.Port)>0
		// (operator explicitly passed --port flags via ov start). Per
		// the 2026-05-09 SaveDeployStateInput.SetPorts contract, ports
		// are written ONLY on explicit operator opt-in.
		saveDeployState(c.Image, c.Instance, SaveDeployStateInput{
			Ports:    ports,
			SetPorts: true,
		})
	}

	// Pre-flight port conflict check
	if conflicts := CheckPortAvailability(ports, rt.BindAddress, engine); len(conflicts) > 0 {
		return fmt.Errorf("port conflicts detected:%s", FormatPortConflicts(conflicts, c.Image))
	}

	// Inject agent forwarding mounts and env (direct mode only)
	var deployImage *DeploymentNode
	if overlay, ok := dc.Lookup(c.Image, c.Instance); ok {
		deployImage = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployImage, home)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	name := containerNameInstance(c.Image, c.Instance)
	workDir := resolveWorkingDir(volumes, bindMounts, home, c.Image, c.Instance)
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

	// Direct-mode deploy (no quadlet, but marker file recorded by
	// runConfigDirect). Fall through to `podman start ov-<name>` instead
	// of `systemctl --user start`. Encrypted-volume mounts are skipped
	// in direct mode (those require systemd-run --scope, see
	// runConfigDirect's warning path).
	if !exists && IsDirectDeploy(c.Image, c.Instance) {
		name := containerNameInstance(c.Image, c.Instance)
		cmd := exec.Command("podman", "start", name)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("starting %s (direct mode): %w", name, err)
		}
		fmt.Fprintf(os.Stderr, "Started %s (direct mode)\n", name)
		return nil
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
	Unmount  bool   `long:"unmount" help:"After stopping, also tear down encrypted FUSE mounts and gocryptfs scope units (ov-enc-<image>-<volume>.scope) for this image"`
}

func (c *StopCmd) Run() error {
	c.Image, c.Instance = canonicalizeDeployArg(c.Image, c.Instance)
	// Releasing a persistent exclusive claim restores any holder this deploy
	// preempted (no-op if no lease / gated by an outer orchestrator).
	defer releaseExclusiveForClaimant(deployKey(c.Image, c.Instance))
	// Resolve the image name (handle remote refs)
	imageName := c.Image
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		imageName = ParseRemoteRef(ref).Name
	}

	// Stop tunnel before stopping container (best-effort)
	stopTunnelForImage(imageName, c.Instance)

	if err := stopPodService(imageName, c.Instance); err != nil {
		return err
	}

	stopUnmountIfRequested(c.Unmount, imageName, c.Instance)
	return nil
}

// stopPodService stops a running pod deployment — the quadlet service when
// one exists (always via systemctl, so podman-stop + Restart=always can't
// create a restart loop), else the container directly via the resolved engine
// with a fallback to the other engine. It performs NO tunnel/unmount side
// effects — callers layer those on. Shared by StopCmd.Run and the resource
// arbiter (ov/preempt.go), whose preemption path wants a bare, reversible
// service stop that leaves the holder's disk/container intact for restart.
func stopPodService(imageName, instance string) error {
	quadletActive, _ := quadletExistsInstance(imageName, instance)
	if quadletActive {
		svc := serviceNameInstance(imageName, instance)
		cmd := exec.Command("systemctl", "--user", "stop", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stopping %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Stopped %s\n", svc)
		return nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	runEngine := ResolveImageEngineForDeploy(imageName, instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, instance)

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

// startPodService starts an already-configured pod deployment — the quadlet
// service when one exists, else the existing stopped container via the
// resolved engine. Used by the resource arbiter to restore a preempted holder:
// the deployment's quadlet/container already exists (the holder was running
// before preemption), so this is a plain service/container start, not a full
// `ov start` re-config.
func startPodService(imageName, instance string) error {
	quadletActive, _ := quadletExistsInstance(imageName, instance)
	if quadletActive {
		svc := serviceNameInstance(imageName, instance)
		cmd := exec.Command("systemctl", "--user", "start", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("starting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Started %s\n", svc)
		return nil
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	runEngine := ResolveImageEngineForDeploy(imageName, instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, instance)

	cmd := exec.Command(engine, "start", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s start failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", name)
	return nil
}

// stopUnmountIfRequested tears down encrypted-volume FUSE mounts after a
// successful stop, when the user passed --unmount. Best-effort by design
// (matches the stopTunnelForImage pattern); failures emit a warning but
// don't propagate, since the container has already stopped and the user
// can retry the unmount manually with `ov config unmount <image>`.
func stopUnmountIfRequested(want bool, imageName, instance string) {
	if !want {
		return
	}
	if err := encUnmount(imageName, instance, ""); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: encrypted-volume unmount failed: %v\n", err)
	}
}

// RestartCmd restarts a service container. In quadlet mode it issues a single
// `systemctl --user restart`, which is atomic from systemd's perspective —
// ExecStopPost (e.g. tailscale serve --off) runs before ExecStartPost
// (tailscale serve), and the unit ends in either active or failed, never the
// silent stopped state that a manual stop+start sequence can produce when
// start fails.
type RestartCmd struct {
	Image    string `arg:"" help:"Image name or remote ref"`
	Instance string `short:"i" long:"instance" help:"Instance name for running multiple containers of the same image"`
}

func (c *RestartCmd) Run() error {
	imageName := c.Image
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		imageName = ParseRemoteRef(ref).Name
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	quadletActive, _ := quadletExistsInstance(imageName, c.Instance)
	if quadletActive {
		svc := serviceNameInstance(imageName, c.Instance)
		cmd := exec.Command("systemctl", "--user", "restart", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("restarting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
		return nil
	}

	// Direct mode: delegate to engine restart.
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(imageName, c.Instance)

	cmd := exec.Command(engine, "restart", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s restart %s failed: %w\n%s", engine, name, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Restarted %s\n", name)
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
			dc := loadDeployConfigForRead("ov start tunnel merge")
			MergeDeployOntoMetadata(meta, dc, imageName, instance)
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
// Uses build.yml init: section config when available, falls back to well-known defaults.
func resolveEntrypoint(initConfig *InitConfig, layers map[string]*Layer, layerOrder []string) []string {
	if initConfig != nil {
		initName, initDef := initConfig.ResolveInitSystem(layers, layerOrder, "")
		if initName != "" && initDef != nil && len(initDef.Entrypoint) > 0 {
			return initDef.Entrypoint
		}
	}
	return []string{"sleep", "infinity"}
}

// resolveEntrypointFromMeta determines the entrypoint from image metadata (runtime mode).
// Uses well-known init system names; custom init systems declared via build.yml init: section are
// only honored during build.
func resolveEntrypointFromMeta(meta *BoxMetadata) []string {
	if meta.Init == "" {
		return []string{"sleep", "infinity"}
	}
	if def, ok := wellKnownInitDefs[meta.Init]; ok {
		return def.Entrypoint
	}
	return []string{"sleep", "infinity"}
}

// containerName returns the deterministic container name for an image
// (or for a `<base>/<instance>` deploy key — the `/` is canonicalized
// to `-` per the documented convention "container name is always
// `ov-<key-with-slash-replaced-by-dash>`"; see /ov-core:deploy "Two
// supported deploy patterns").
func containerName(imageName string) string {
	return "ov-" + strings.ReplaceAll(imageName, "/", "-")
}

// containerNameInstance returns the container name with optional instance suffix.
// Slashes in imageName are canonicalized to dashes — see containerName.
func containerNameInstance(imageName, instance string) string {
	if instance == "" {
		return containerName(imageName)
	}
	return "ov-" + strings.ReplaceAll(imageName, "/", "-") + "-" + instance
}
