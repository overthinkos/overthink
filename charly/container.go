package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// resolveContainer resolves engine + container name, verifying the container is running.
// Use "." as image name for local mode (returns empty engine and name).
func resolveContainer(image, instance string) (engine, name string, err error) {
	if image == "." {
		return "", "", nil
	}
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	imageName := resolveImageName(image)
	runEngine := ResolveImageEngineForDeploy(imageName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	name = containerNameInstance(imageName, instance)
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
}

// isHostNetworked checks if a running container uses --network host.
func isHostNetworked(engine, containerName string) bool {
	cmd := exec.Command(engine, "inspect", "--format",
		"{{.HostConfig.NetworkMode}}", containerName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "host"
}
