package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ServiceCmd manages services inside a running container
type ServiceCmd struct {
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of services"`
	Start   ServiceStartCmd   `cmd:"" help:"Start a service"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop a service"`
	Restart ServiceRestartCmd `cmd:"" help:"Restart a service"`
}

// ServiceStatusCmd shows status of all services
type ServiceStatusCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Image, c.Instance)
	if err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "status")
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "start", c.Service)
}

// ServiceStopCmd stops a service
type ServiceStopCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "stop", c.Service)
}

// ServiceRestartCmd restarts a service
type ServiceRestartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "restart", c.Service)
}

// resolveServiceInit resolves the container, engine, and init system for service management.
func resolveServiceInit(image, instance string) (engine, containerName string, initDef *InitDef, err error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", nil, err
	}
	imageName := resolveImageName(image)
	runEngine := ResolveImageEngineForDeploy(imageName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	containerName = containerNameInstance(imageName, instance)
	if !containerRunning(engine, containerName) {
		return "", "", nil, fmt.Errorf("container %s is not running", containerName)
	}

	// Determine init system from image labels
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return "", "", nil, fmt.Errorf("cannot determine image for container %s", containerName)
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot read image metadata: %w", err)
	}
	if meta == nil || meta.Init == "" {
		return "", "", nil, fmt.Errorf("no init system configured for container %s (rebuild image with init.yml support)", containerName)
	}

	// Load init config to get management commands
	initDef, err = resolveInitDefFromMeta(meta)
	if err != nil {
		return "", "", nil, err
	}

	return engine, containerName, initDef, nil
}

// resolveInitDefFromMeta creates a minimal InitDef from image metadata.
// In runtime mode we don't have init.yml, but we can try loading it from the project dir.
// If that fails, we construct a basic InitDef from the init system name.
func resolveInitDefFromMeta(meta *ImageMetadata) (*InitDef, error) {
	// Try loading init.yml from current directory
	dir, _ := os.Getwd()
	cfg, _ := LoadConfig(dir)
	if cfg != nil {
		initCfg, err := LoadInitConfigForImage(
			cfg.Defaults.FormatConfig, cfg.Defaults.FormatConfig, dir,
		)
		if err == nil && initCfg != nil {
			if def, ok := initCfg.Inits[meta.Init]; ok {
				return def, nil
			}
		}
	}

	// Fallback: construct minimal InitDef from well-known init system names
	switch meta.Init {
	case "supervisord":
		return &InitDef{
			ManagementTool: "supervisorctl",
			ManagementCommands: map[string]string{
				"status":  "status",
				"start":   "start {{.Service}}",
				"stop":    "stop {{.Service}}",
				"restart": "restart {{.Service}}",
			},
		}, nil
	case "systemd":
		return &InitDef{
			ManagementTool: "systemctl",
			ManagementCommands: map[string]string{
				"status":  "--user status {{.Service}}",
				"start":   "--user start {{.Service}}",
				"stop":    "--user stop {{.Service}}",
				"restart": "--user restart {{.Service}}",
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown init system %q; cannot determine management commands (no init.yml found)", meta.Init)
	}
}

// execInitCommand executes a service management command inside a container.
func execInitCommand(engine, containerName string, initDef *InitDef, operation string, args ...string) error {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rendered, err := initDef.RenderManagementCommand(operation, serviceName)
	if err != nil {
		return err
	}

	execArgs := append([]string{"exec", containerName, initDef.ManagementTool}, strings.Fields(rendered)...)
	cmd := exec.Command(engine, execArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// validateServiceName checks that a service name exists in the image's service list.
func validateServiceName(engine, containerName, serviceName string) error {
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return fmt.Errorf("cannot determine image for container %s", containerName)
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return fmt.Errorf("cannot read image metadata: %w", err)
	}
	if meta == nil {
		return fmt.Errorf("no overthinkos metadata found for container %s", containerName)
	}
	for _, s := range meta.Services {
		if s == serviceName {
			return nil
		}
	}
	return fmt.Errorf("service %q not found in image (available: %s)", serviceName, strings.Join(meta.Services, ", "))
}
