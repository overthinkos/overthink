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
	Image     string `arg:"" help:"Image name from images.yml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	GPUFlags  `embed:""`
}

func (c *StartCmd) Run() error {
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
	var ports []string
	var volumes []VolumeMount

	// Try images.yml first, fall back to image labels
	dir, _ := os.Getwd()
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr == nil {
		resolved, err := cfg.ResolveImage(c.Image, "unused")
		if err != nil {
			return err
		}
		layers, err := ScanLayers(dir)
		if err != nil {
			return err
		}
		volumes, err = CollectImageVolumes(cfg, layers, c.Image, resolved.Home)
		if err != nil {
			return err
		}
		imageRef = resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
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

	name := containerName(c.Image)
	args := buildStartArgs(engine, imageRef, absWorkspace, ports, name, volumes, gpu)

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
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *StopCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		svc := serviceName(c.Image)
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
	name := containerName(c.Image)

	cmd := exec.Command(engine, "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s stop failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// buildStartArgs constructs the container run argument list for detached supervisord.
func buildStartArgs(engine, imageRef, workspace string, ports []string, name string, volumes []VolumeMount, gpu bool) []string {
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
		args = append(args, "-p", localizePort(port))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	args = append(args, imageRef, "supervisord", "-n", "-c", "/etc/supervisord.conf")
	return args
}

// containerName returns the deterministic container name for an image.
func containerName(imageName string) string {
	return "ov-" + imageName
}
