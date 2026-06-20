package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// ServiceCmd manages services inside a running container
type ServiceCmd struct {
	Restart ServiceRestartCmd `cmd:"" help:"Restart an in-container service"`
	Start   ServiceStartCmd   `cmd:"" help:"Start an in-container service"`
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of in-container services"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop an in-container service"`
}

// ServiceStatusCmd shows status of all services
type ServiceStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "status")
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
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
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
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
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(engine, name, initDef, "restart", c.Service)
}

// resolveServiceInit resolves the container, engine, and init system for service management.
func resolveServiceInit(box, instance string) (engine, containerName string, initDef *InitDef, err error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", nil, err
	}
	boxName := resolveBoxName(box)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	containerName = containerNameInstance(boxName, instance)
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
		return "", "", nil, fmt.Errorf("no init system configured for container %s (rebuild image with the embedded init: vocabulary)", containerName)
	}

	// Load init config to get management commands
	initDef, err = resolveInitDefFromMeta(meta)
	if err != nil {
		return "", "", nil, err
	}

	return engine, containerName, initDef, nil
}

// wellKnownInitDefs is the runtime fallback registry for image-label-only
// deploys (where the build-time init: vocabulary is unavailable).
// Custom init systems declared via the embedded init: vocabulary (charly/charly.yml)
// are honored during build only; at runtime, only entries here are recognized.
//
// Adding a new init system at runtime is a one-table-edit: add entrypoint +
// management commands here and the rest of the codebase picks it up via
// resolveInitDefFromMeta and resolveEntrypointFromMeta.
var wellKnownInitDefs = map[string]*InitDef{
	"supervisord": {
		Entrypoint:     []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
		ManagementTool: "supervisorctl",
		ManagementCommands: map[string]string{
			"status":  "status",
			"start":   "start {{.Service}}",
			"stop":    "stop {{.Service}}",
			"restart": "restart {{.Service}}",
		},
	},
	"systemd": {
		// Systemd-on-bootc boots via VM init; container has no entrypoint.
		Entrypoint:     nil,
		ManagementTool: "systemctl",
		ManagementCommands: map[string]string{
			"status":  "--user status {{.Service}}",
			"start":   "--user start {{.Service}}",
			"stop":    "--user stop {{.Service}}",
			"restart": "--user restart {{.Service}}",
		},
	},
}

// resolveInitDefFromMeta returns the InitDef registered for meta.Init in
// wellKnownInitDefs. Errors when the init system is unrecognized — the
// hint asks the operator to declare the init system in the embedded init:
// vocabulary (which honors arbitrary names at build time).
func resolveInitDefFromMeta(meta *BoxMetadata) (*InitDef, error) {
	if def, ok := wellKnownInitDefs[meta.Init]; ok {
		return def, nil
	}
	return nil, fmt.Errorf("unknown init system %q; cannot determine management commands (no matching embedded init: vocabulary entry)", meta.Init)
}

// execInitCommand executes a service management command inside a container.
func execInitCommand(engine, containerName string, initDef *InitDef, operation string, args ...string) error {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rendered, err := initRenderManagementCommand(initDef, operation, serviceName)
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
		return fmt.Errorf("no opencharly metadata found for container %s", containerName)
	}
	if slices.Contains(meta.ServiceNames, serviceName) {
		return nil
	}
	return fmt.Errorf("service %q not found in image (available: %s)", serviceName, strings.Join(meta.ServiceNames, ", "))
}
