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

	start := time.Now()
	cmd := exec.Command(engine, "exec", name, "sh", "-c", c.Command)
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
