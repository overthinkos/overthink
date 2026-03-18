package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ServiceCmd manages supervisord services inside a running container
type ServiceCmd struct {
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of supervisord services"`
	Start   ServiceStartCmd   `cmd:"" help:"Start a supervisord service"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop a supervisord service"`
	Restart ServiceRestartCmd `cmd:"" help:"Restart a supervisord service"`
}

// ServiceStatusCmd shows status of all supervisord services
type ServiceStatusCmd struct {
	Image    string `arg:"" help:"Image name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	// Resolve per-image engine
	dir, _ := os.Getwd()
	runEngine := ResolveImageEngineFromDir(dir, resolveImageName(c.Image), rt.RunEngine)
	engine := EngineBinary(runEngine)
	name := containerNameInstance(resolveImageName(c.Image), c.Instance)

	if !containerRunning(engine, name) {
		return fmt.Errorf("container %s is not running", name)
	}

	return execSupervisorctl(engine, name, "status")
}

// ServiceStartCmd starts a supervisord service
type ServiceStartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	engine, name, err := resolveServiceContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateSupervisordService(engine, name, c.Service); err != nil {
		return err
	}
	return execSupervisorctl(engine, name, "start", c.Service)
}

// ServiceStopCmd stops a supervisord service
type ServiceStopCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	engine, name, err := resolveServiceContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateSupervisordService(engine, name, c.Service); err != nil {
		return err
	}
	return execSupervisorctl(engine, name, "stop", c.Service)
}

// ServiceRestartCmd restarts a supervisord service
type ServiceRestartCmd struct {
	Image    string `arg:"" help:"Image name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	engine, name, err := resolveServiceContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}
	if err := validateSupervisordService(engine, name, c.Service); err != nil {
		return err
	}
	return execSupervisorctl(engine, name, "restart", c.Service)
}

func resolveServiceContainer(image, instance string) (engine, name string, err error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	// Resolve per-image engine
	dir, _ := os.Getwd()
	imageName := resolveImageName(image)
	runEngine := ResolveImageEngineFromDir(dir, imageName, rt.RunEngine)
	engine = EngineBinary(runEngine)
	name = containerNameInstance(imageName, instance)
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
}

func execSupervisorctl(engine, containerName string, args ...string) error {
	execArgs := append([]string{"exec", containerName, "supervisorctl"}, args...)
	cmd := exec.Command(engine, execArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func validateSupervisordService(engine, containerName, serviceName string) error {
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
	return checkSupervisordService(meta.Supervisord, serviceName)
}

func checkSupervisordService(available []string, serviceName string) error {
	for _, s := range available {
		if s == serviceName {
			return nil
		}
	}
	return fmt.Errorf("service %q not found in image (available: %s)", serviceName, strings.Join(available, ", "))
}
