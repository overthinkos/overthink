package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
)

// qemuGracefulShutdown sends a system_powerdown command via QMP for ACPI shutdown.
func qemuGracefulShutdown(stateDir string) error {
	qmpSocket := filepath.Join(stateDir, "qmp.sock")

	if _, err := os.Stat(qmpSocket); err != nil {
		return fmt.Errorf("QMP socket not found at %s", qmpSocket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disconnectedCh := make(chan struct{})
	qmp, _, err := govmmQemu.QMPStart(ctx, qmpSocket, govmmQemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		return fmt.Errorf("connecting to QMP: %w", err)
	}
	defer qmp.Shutdown()

	if err := qmp.ExecuteQMPCapabilities(ctx); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}

	if err := qmp.ExecuteSystemPowerdown(ctx); err != nil {
		return fmt.Errorf("QMP system_powerdown: %w", err)
	}

	return nil
}

// qemuForceShutdown sends a quit command via QMP to force QEMU to exit immediately.
func qemuForceShutdown(stateDir string) error {
	qmpSocket := filepath.Join(stateDir, "qmp.sock")

	if _, err := os.Stat(qmpSocket); err != nil {
		return fmt.Errorf("QMP socket not found at %s", qmpSocket)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	disconnectedCh := make(chan struct{})
	qmp, _, err := govmmQemu.QMPStart(ctx, qmpSocket, govmmQemu.QMPConfig{}, disconnectedCh)
	if err != nil {
		return fmt.Errorf("connecting to QMP: %w", err)
	}
	defer qmp.Shutdown()

	if err := qmp.ExecuteQMPCapabilities(ctx); err != nil {
		return fmt.Errorf("QMP capabilities: %w", err)
	}

	if err := qmp.ExecuteQuit(ctx); err != nil {
		return fmt.Errorf("QMP quit: %w", err)
	}

	return nil
}

// killQemuByPID reads the PID file and kills the QEMU process.
func killQemuByPID(stateDir string) {
	pidFile := filepath.Join(stateDir, "qemu.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	proc.Kill()
}
