package main

import (
	"fmt"
	"os"
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
