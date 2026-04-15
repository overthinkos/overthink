package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StatusCmd is defined in status.go

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
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)
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
	Seed      bool   `long:"seed" default:"true" negatable:"" help:"Sync data from new image into bind-backed volumes (default: true)"`
	ForceSeed bool   `long:"force-seed" help:"Overwrite existing data in volumes (default: only add new files)"`
	DataFrom  string `long:"data-from" help:"Sync data from this data image instead"`
}

// updateCmdBuildFn is the hook invoked when UpdateCmd.Run sees --build for a
// local image. Default implementation runs BuildCmd; tests override this to
// spy on invocations without actually building.
var updateCmdBuildFn = func(image, tag string) error {
	fmt.Fprintf(os.Stderr, "Building %s locally (--build requested)\n", image)
	bc := &BuildCmd{
		Images: []string{image},
		Tag:    tag,
	}
	return bc.Run()
}

func (c *UpdateCmd) Run() error {
	// Handle remote image refs
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		return c.runRemoteUpdate(ref)
	}

	// Defect E fix: honor --build for LOCAL images too. Previously this flag
	// was only threaded through the runRemoteUpdate path; local images
	// silently no-op'd because EnsureImage returned early when the image
	// existed locally, regardless of staleness.
	if c.Build {
		if err := updateCmdBuildFn(c.Image, c.Tag); err != nil {
			return fmt.Errorf("building %s: %w", c.Image, err)
		}
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(c.Image, c.Instance, rt.RunEngine)

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

		// Sync data from new image into bind-backed volumes (merge mode)
		if c.Seed {
			c.syncData(runEngine, imageRef, meta, rt)
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

	// Sync data in direct mode too
	if c.Seed {
		c.syncData(runEngine, imageRef, meta, rt)
	}

	fmt.Fprintf(os.Stderr, "Image updated. Restart with: ov stop %s && ov start %s\n", c.Image, c.Image)
	return nil
}

// syncData merges data from the (new) image into bind-backed volumes.
// Uses merge mode (cp -an) to add new files without overwriting existing user data.
func (c *UpdateCmd) syncData(engine string, imageRef string, meta *ImageMetadata, rt *ResolvedRuntime) {
	// Re-extract metadata from the new image
	newMeta, err := ExtractMetadata(engine, imageRef)
	if err != nil || newMeta == nil {
		return
	}

	dataMeta := newMeta
	dataRef := imageRef

	// Use external data image if --data-from specified
	if c.DataFrom != "" {
		dataRef = c.DataFrom
		if !strings.Contains(dataRef, ":") {
			dataRef += ":latest"
		}
		dm, err := ExtractMetadata(engine, dataRef)
		if err != nil || dm == nil {
			fmt.Fprintf(os.Stderr, "Warning: could not load data image %s: %v\n", dataRef, err)
			return
		}
		dataMeta = dm
	}

	if len(dataMeta.DataEntries) == 0 {
		return
	}

	// Load deploy config to find bind-backed volumes
	dc, _ := LoadDeployConfig()
	if dc == nil {
		return
	}
	imgDeploy, ok := dc.Images[deployKey(c.Image, c.Instance)]
	if !ok {
		return
	}

	volumes, bindMounts := ResolveVolumeBacking(c.Image, newMeta.Volumes, imgDeploy.Volumes,
		newMeta.Home, rt.EncryptedStoragePath, rt.VolumesPath)
	if len(bindMounts) == 0 && len(volumes) == 0 {
		return
	}

	mode := DataProvisionMerge
	if c.ForceSeed {
		mode = DataProvisionForce
	}

	fmt.Fprintln(os.Stderr, "Syncing data from new image...")
	seeded, err := provisionData(engine, dataRef, dataMeta, bindMounts, volumes, mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: data sync failed: %v\n", err)
		return
	}

	// Update deploy.yml with new data source
	if seeded > 0 {
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
			fmt.Fprintf(os.Stderr, "Warning: could not save data source to deploy.yml: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "Synced data for %d volume(s)\n", seeded)
	}
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
	Purge       bool     `long:"purge" help:"Also remove named volumes"`
	KeepDeploy  bool     `name:"keep-deploy" help:"Keep deploy.yml entry for this image"`
	Env         []string `short:"e" long:"env" sep:"none" help:"Set env var for hooks (KEY=VALUE)"`
}

func (c *RemoveCmd) Run() error {
	imageName := resolveImageName(c.Image)

	// Stop tunnel before removing container (best-effort)
	stopTunnelForImage(imageName, c.Instance)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Resolve per-image engine from deploy.yml
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)
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

		// Remove pod file if it exists (sidecar mode)
		podPath := filepath.Join(qdir, podQuadletFilenameInstance(imageName, c.Instance))
		if err := os.Remove(podPath); err == nil {
			fmt.Fprintf(os.Stderr, "Removed %s\n", podPath)
		}

		// Remove sidecar .container files (glob for ov-<image>-*.container in quadlet dir)
		podPrefix := PodNameInstance(imageName, c.Instance) + "-"
		if entries, err := os.ReadDir(qdir); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if strings.HasPrefix(name, podPrefix) && strings.HasSuffix(name, ".container") {
					scPath := filepath.Join(qdir, name)
					if err := os.Remove(scPath); err == nil {
						fmt.Fprintf(os.Stderr, "Removed %s\n", scPath)
					}
				}
			}
		}

		// Remove sidecar config files (e.g., tailscale serve config)
		if scDir, scErr := sidecarConfigDir(); scErr == nil {
			scPrefix := PodNameInstance(imageName, c.Instance) + "-"
			if entries, err := os.ReadDir(scDir); err == nil {
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), scPrefix) {
						scfPath := filepath.Join(scDir, entry.Name())
						if err := os.Remove(scfPath); err == nil {
							fmt.Fprintf(os.Stderr, "Removed %s\n", scfPath)
						}
					}
				}
			}
		}

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

		if c.Purge {
			removeVolumes(engine, imageName, c.Instance)
		}
		if !c.KeepDeploy {
			cleanDeployEntry(imageName, c.Instance)
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

	if c.Purge {
		removeVolumes(engine, imageName, c.Instance)
	}
	if !c.KeepDeploy {
		cleanDeployEntry(imageName, c.Instance)
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
