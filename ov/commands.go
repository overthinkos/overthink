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
	Image     string `arg:"" help:"Image name from images.yml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	GPUFlags  `embed:""`
}

func (c *EnableCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov enable requires run_mode=quadlet (current: %s)", rt.RunMode)
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

	gpu := ResolveGPU(c.GPUFlags.Mode())
	LogGPU(gpu)

	var imageRef string
	var ports []string
	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount
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
		ports = resolved.Ports
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
			return fmt.Errorf("image %s has no embedded metadata; run from project directory or rebuild with latest ov", imageRef)
		}
		ports = meta.Ports
		volumes = meta.Volumes
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

	// Resolve tunnel config if available
	var tunnelCfg *TunnelConfig
	if cfgErr == nil {
		resolved, resolveErr := cfg.ResolveImage(c.Image, "unused")
		if resolveErr == nil && resolved.Tunnel != nil {
			layers, scanErr := ScanAllLayers(dir)
			if scanErr == nil {
				tunnelYAML := cfg.Images[c.Image].Tunnel
				if tunnelYAML == nil {
					tunnelYAML = cfg.Defaults.Tunnel
				}
				tunnelCfg = ResolveTunnelConfig(tunnelYAML, c.Image, resolved.FQDN, layers, resolved.Layers)
			}
		}
	}

	qcfg := QuadletConfig{
		ImageName:   c.Image,
		ImageRef:    imageRef,
		Workspace:   absWorkspace,
		Ports:       ports,
		Volumes:     volumes,
		BindMounts:  bindMounts,
		GPU:         gpu,
		BindAddress: rt.BindAddress,
		Tunnel:      tunnelCfg,
		UID:         uid,
		GID:         gid,
	}

	content := generateQuadlet(qcfg)

	qdir, err := quadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}

	qpath := filepath.Join(qdir, quadletFilename(c.Image))
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
		cryptoContent := generateCryptoUnit(c.Image, bindMounts, rt.EncryptedStoragePath)
		if cryptoContent != "" {
			svcDir, err := systemdUserDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(svcDir, 0755); err != nil {
				return fmt.Errorf("creating systemd user directory: %w", err)
			}
			cryptoPath := filepath.Join(svcDir, cryptoServiceFilename(c.Image))
			if err := os.WriteFile(cryptoPath, []byte(cryptoContent), 0644); err != nil {
				return fmt.Errorf("writing crypto service file: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", cryptoPath)
		}
	}

	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
	return nil
}

// DisableCmd disables a quadlet service from auto-starting (quadlet only)
type DisableCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *DisableCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode != "quadlet" {
		return fmt.Errorf("ov disable requires run_mode=quadlet (current: %s)", rt.RunMode)
	}

	svc := serviceName(c.Image)
	cmd := exec.Command("systemctl", "--user", "disable", "--now", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Tolerate non-zero exit (service may not exist)
	_ = cmd.Run()

	fmt.Fprintf(os.Stderr, "Disabled %s\n", svc)
	return nil
}

// StatusCmd shows the status of a service container
type StatusCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *StatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		svc := serviceName(c.Image)
		cmd := exec.Command("systemctl", "--user", "status", svc)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// systemctl status exits non-zero for inactive services — not an error
		_ = cmd.Run()
		return nil
	}

	// Direct mode: engine inspect
	engine := EngineBinary(rt.RunEngine)
	name := containerName(c.Image)
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
	Image  string `arg:"" help:"Image name from images.yml"`
	Follow bool   `short:"f" long:"follow" help:"Follow log output"`
}

func (c *LogsCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		svc := serviceName(c.Image)
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

	// Direct mode: engine logs
	engine := EngineBinary(rt.RunEngine)
	name := containerName(c.Image)
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
	Image string `arg:"" help:"Image name from images.yml"`
	Tag   string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
}

func (c *UpdateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	resolved, err := cfg.ResolveImage(c.Image, "unused")
	if err != nil {
		return err
	}

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		podmanRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: "podman"}
		if err := EnsureImage(imageRef, podmanRT); err != nil {
			return err
		}

		svc := serviceName(c.Image)
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
	if err := EnsureImage(imageRef, rt); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Image updated. Restart with: ov stop %s && ov start %s\n", c.Image, c.Image)
	return nil
}

// RemoveCmd removes a service container
type RemoveCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *RemoveCmd) Run() error {
	// Stop tunnel before removing container (best-effort)
	stopTunnelForImage(c.Image)

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		svc := serviceName(c.Image)
		// Best-effort stop
		stop := exec.Command("systemctl", "--user", "stop", svc)
		_ = stop.Run()

		qdir, err := quadletDir()
		if err != nil {
			return err
		}

		qpath := filepath.Join(qdir, quadletFilename(c.Image))
		if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing quadlet file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

		// Remove companion tunnel service file if it exists
		svcDir, svcDirErr := systemdUserDir()
		if svcDirErr == nil {
			tunnelPath := filepath.Join(svcDir, tunnelServiceFilename(c.Image))
			if err := os.Remove(tunnelPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", tunnelPath)
			}
			// Remove companion crypto service file if it exists
			cryptoPath := filepath.Join(svcDir, cryptoServiceFilename(c.Image))
			if err := os.Remove(cryptoPath); err == nil {
				fmt.Fprintf(os.Stderr, "Removed %s\n", cryptoPath)
			}
		}

		cmd := exec.Command("systemctl", "--user", "daemon-reload")
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
		}

		fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
		return nil
	}

	// Direct mode: stop + rm
	engine := EngineBinary(rt.RunEngine)
	name := containerName(c.Image)

	// Best-effort stop
	stop := exec.Command(engine, "stop", name)
	_ = stop.Run()

	// Remove container (tolerate "no such container")
	rm := exec.Command(engine, "rm", name)
	_ = rm.Run()

	fmt.Fprintf(os.Stderr, "Removed container %s\n", name)
	return nil
}
