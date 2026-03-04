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
	Image     string `arg:"" help:"Image name or remote ref (github.com/org/repo/image[@version])"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Build     bool   `long:"build" help:"Force local build instead of pulling from registry"`
	GPUFlags  `embed:""`
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

	gpu := ResolveGPU(c.GPUFlags.Mode())
	LogGPU(gpu)

	engine := rt.RunEngine

	var imageRef string
	var uid, gid int
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount

	// Try images.yml first, fall back to image labels
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused")
		if err != nil {
			return err
		}
		layers, err := ScanAllLayers(dir)
		if err != nil {
			return err
		}
		volumes, err = CollectImageVolumes(cfg, layers, c.Image, resolved.Home, BindMountNames(cfg.Images[c.Image].BindMounts))
		if err != nil {
			return err
		}
		// Resolve bind mounts
		img := cfg.Images[c.Image]
		if len(img.BindMounts) > 0 {
			bindMounts = resolveBindMounts(c.Image, img.BindMounts, resolved.Home, rt.EncryptedStoragePath)
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
		uid = resolved.UID
		gid = resolved.GID
		ports = resolved.Ports
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
			return fmt.Errorf("image %s has no embedded metadata; run from project directory or rebuild with latest ov", imageRef)
		}
		uid = meta.UID
		gid = meta.GID
		ports = meta.Ports
		volumes = meta.Volumes
		if meta.Registry != "" {
			imageRef = resolveShellImageRef(meta.Registry, c.Image, c.Tag)
		}
	}

	if cfgErr == nil {
		if err := EnsureImage(imageRef, rt); err != nil {
			return err
		}
	}

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Image); err != nil {
		return err
	}

	name := containerName(c.Image)
	args := buildStartArgs(engine, imageRef, absWorkspace, uid, gid, ports, name, volumes, bindMounts, gpu, rt.BindAddress)

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
			// Re-resolve with layers for port defaulting
			layers, scanErr := ScanAllLayers(dir)
			if scanErr == nil {
				tc := ResolveTunnelConfig(
					c.findTunnelYAML(cfg),
					c.Image, resolved.FQDN, layers, resolved.Layers,
				)
				if tc != nil {
					if err := TunnelStart(*tc); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: tunnel setup failed: %v\n", err)
					}
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

	if rt.RunMode == "quadlet" {
		return c.runRemoteQuadlet(rt, ctx, absWorkspace, gpu)
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

	name := ctx.ContainerName()
	args := buildStartArgs(engine, ctx.ImageRef, absWorkspace,
		ctx.Resolved.UID, ctx.Resolved.GID, ctx.Resolved.Ports,
		name, volumes, bindMounts, gpu, rt.BindAddress)

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

func (c *StartCmd) runRemoteQuadlet(rt *ResolvedRuntime, ctx *RemoteImageContext, absWorkspace string, gpu bool) error {
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

	qcfg := QuadletConfig{
		ImageName:   ctx.ImageName,
		ImageRef:    ctx.ImageRef,
		Workspace:   absWorkspace,
		Ports:       ctx.Resolved.Ports,
		Volumes:     volumes,
		BindMounts:  bindMounts,
		GPU:         gpu,
		BindAddress: rt.BindAddress,
		UID:         ctx.Resolved.UID,
		GID:         ctx.Resolved.GID,
	}

	content := generateQuadlet(qcfg)

	qdir, err := quadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}

	qpath := filepath.Join(qdir, quadletFilename(ctx.ImageName))
	if err := os.WriteFile(qpath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing quadlet file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", qpath)

	reloadCmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := reloadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	svc := serviceName(ctx.ImageName)
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
	exists, err := quadletExists(c.Image)
	if err != nil {
		return err
	}

	if !exists {
		if !rt.AutoEnable {
			return fmt.Errorf("not enabled; run 'ov enable %s' first, or set auto_enable=true", c.Image)
		}
		// Auto-enable: generate quadlet file
		enable := &EnableCmd{
			Image:     c.Image,
			Workspace: c.Workspace,
			Tag:       c.Tag,
			GPUFlags:  c.GPUFlags,
		}
		if err := enable.runEnable(rt); err != nil {
			return err
		}
	}

	svc := serviceName(c.Image)
	cmd := exec.Command("systemctl", "--user", "start", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", svc)
	return nil
}

// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Image string `arg:"" help:"Image name or remote ref"`
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

	if rt.RunMode == "quadlet" {
		svc := serviceName(imageName)
		cmd := exec.Command("systemctl", "--user", "stop", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("stopping %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Stopped %s\n", svc)
		return nil
	}

	engine := EngineBinary(rt.RunEngine)
	name := containerName(imageName)

	cmd := exec.Command(engine, "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s stop failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// stopTunnelForImage attempts to stop any tunnel for the given image (best-effort).
func stopTunnelForImage(imageName string) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		return
	}
	resolved, err := cfg.ResolveImage(imageName, "unused")
	if err != nil || resolved.Tunnel == nil {
		return
	}
	layers, err := ScanAllLayers(dir)
	if err != nil {
		return
	}
	// Get the raw TunnelYAML
	img := cfg.Images[imageName]
	tunnelYAML := img.Tunnel
	if tunnelYAML == nil {
		tunnelYAML = cfg.Defaults.Tunnel
	}
	tc := ResolveTunnelConfig(tunnelYAML, imageName, resolved.FQDN, layers, resolved.Layers)
	if tc != nil {
		if err := TunnelStop(*tc); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: tunnel teardown failed: %v\n", err)
		}
	}
}

// buildStartArgs constructs the container run argument list for detached supervisord.
func buildStartArgs(engine, imageRef, workspace string, uid, gid int, ports []string, name string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, bindAddr string) []string {
	binary := EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
	}
	if gpu {
		args = append(args, GPURunArgs(engine)...)
	}
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
	args = append(args, imageRef, "supervisord", "-n", "-c", "/etc/supervisord.conf")
	return args
}

// containerName returns the deterministic container name for an image.
func containerName(imageName string) string {
	return "ov-" + imageName
}
