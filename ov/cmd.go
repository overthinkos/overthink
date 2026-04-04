package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// CmdCmd runs a single command in a running container with optional notification.
type CmdCmd struct {
	Image    string `arg:"" help:"Image name"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Notify   bool   `long:"notify" negatable:"" default:"true" help:"Send desktop notification on completion (--no-notify to disable)"`
}

func (c *CmdCmd) Run() error {
	engine, name, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Resolve agent forwarding env vars for exec
	rt, rtErr := ResolveRuntime()
	var agentEnv []string
	if rtErr == nil {
		dc, _ := LoadDeployConfig()
		var deployImage *DeployImageConfig
		if dc != nil {
			if overlay, ok := dc.Images[c.Image]; ok {
				deployImage = &overlay
			}
		}
		// Use host user's home as a reasonable default for GPG socket path.
		// For exec, sockets are already mounted — this only affects env vars.
		hostHome, _ := os.UserHomeDir()
		agentFwd := ResolveAgentForwarding(rt, deployImage, hostHome)
		agentEnv = agentFwd.Env
	}

	start := time.Now()
	args := []string{engine, "exec"}
	for _, e := range agentEnv {
		args = append(args, "-e", e)
	}
	args = append(args, name, "sh", "-c", c.Command)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	runErr := cmd.Run()
	elapsed := time.Since(start).Truncate(time.Millisecond)

	if c.Notify {
		status := "completed"
		if runErr != nil {
			status = "failed"
		}
		sendContainerNotification(engine, name,
			fmt.Sprintf("ov: command %s", status),
			fmt.Sprintf("%s (%s)", c.Command, elapsed))
	}

	return runErr
}
