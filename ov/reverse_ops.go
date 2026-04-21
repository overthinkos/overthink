package main

// reverse_ops.go — execute ReverseOp slices recorded by HostDeployTarget
// at install time, turning them into concrete teardown commands.
//
// Each InstallStep's Reverse() method records a list of ReverseOps
// when the step runs (see deploy_target_host.go). `ov deploy del`
// reads those ops from the layer ledger and hands them here for
// execution. The ops are opaque to the ledger — only the teardown
// logic in this file understands each Kind.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ReverseExecutor is the interface ReverseOp handlers expect. Allows
// us to pass either DeployDelCmd (for real teardown) or a test mock.
type ReverseExecutor interface {
	reverseDryRun() bool
	reverseKeepRepoChanges() bool
	reverseKeepServices() bool
}

// DeployDelCmd satisfies ReverseExecutor via thin wrappers — keeps
// the flag-accessor protocol decoupled from the concrete command type.
func (c *DeployDelCmd) reverseDryRun() bool          { return c.DryRun }
func (c *DeployDelCmd) reverseKeepRepoChanges() bool { return c.KeepRepoChanges }
func (c *DeployDelCmd) reverseKeepServices() bool    { return c.KeepServices }

// runReverseOps executes ops in REVERSE order (last-installed, first-
// removed). Idempotent where possible: a missing file is treated as
// "already removed" rather than an error.
func runReverseOps(ops []ReverseOp, exec ReverseExecutor) error {
	for i := len(ops) - 1; i >= 0; i-- {
		if err := runReverseOp(ops[i], exec); err != nil {
			// Keep going: a partial teardown is better than giving up
			// mid-way with half the layer removed. Log to stderr and
			// continue.
			fmt.Fprintf(os.Stderr, "reverse: %s failed: %v\n", ops[i].Kind, err)
		}
	}
	return nil
}

// runReverseOp dispatches on Kind and runs the appropriate command.
func runReverseOp(op ReverseOp, re ReverseExecutor) error {
	switch op.Kind {
	case ReverseOpPackageRemove:
		return reversePackageRemove(op, re)
	case ReverseOpPixiEnvRemove:
		return reversePixiEnvRemove(op, re)
	case ReverseOpCargoUninstall:
		return reverseCargoUninstall(op, re)
	case ReverseOpNpmUninstallG:
		return reverseNpmUninstallG(op, re)
	case ReverseOpRmFileSystem:
		return reverseRmFileSystem(op, re)
	case ReverseOpRmFileUser:
		return reverseRmFileUser(op, re)
	case ReverseOpRmDirRecursive:
		return reverseRmDir(op, re)
	case ReverseOpServiceDisable:
		return reverseServiceDisable(op, re)
	case ReverseOpServiceRemove:
		return reverseServiceRemove(op, re)
	case ReverseOpRemoveDropin:
		return reverseRemoveDropin(op, re)
	case ReverseOpRestoreEnabled:
		return reverseRestoreEnabled(op, re)
	case ReverseOpRemoveManaged:
		return reverseRemoveManaged(op, re)
	case ReverseOpRemoveEnvdFile:
		return reverseRemoveEnvdFile(op, re)
	case ReverseOpRemoveRepoFile:
		return reverseRemoveRepoFile(op, re)
	case ReverseOpCoprDisable:
		return reverseCoprDisable(op, re)
	}
	return fmt.Errorf("runReverseOp: unknown kind %q", op.Kind)
}

// ---------------------------------------------------------------------------
// Per-kind implementations.
// ---------------------------------------------------------------------------

func reversePackageRemove(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	var argv []string
	switch op.Format {
	case "rpm":
		argv = append([]string{"dnf", "remove", "-y"}, op.Targets...)
	case "deb":
		argv = append([]string{"DEBIAN_FRONTEND=noninteractive", "apt-get", "purge", "-y"}, op.Targets...)
	case "pac":
		argv = append([]string{"pacman", "-Rs", "--noconfirm"}, op.Targets...)
	default:
		return fmt.Errorf("reversePackageRemove: unknown format %q", op.Format)
	}
	return runSudoArgvReverse(argv, re)
}

func reversePixiEnvRemove(op ReverseOp, re ReverseExecutor) error {
	for _, envName := range op.Targets {
		path := filepath.Join(os.Getenv("HOME"), ".pixi", "envs", envName)
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -rf %s\n", path)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func reverseCargoUninstall(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	argv := append([]string{"cargo", "uninstall"}, op.Targets...)
	if re.reverseDryRun() {
		fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func reverseNpmUninstallG(op ReverseOp, re ReverseExecutor) error {
	if len(op.Targets) == 0 {
		return nil
	}
	argv := append([]string{"npm", "uninstall", "-g"}, op.Targets...)
	if re.reverseDryRun() {
		fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
		return nil
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func reverseRmFileSystem(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] sudo rm -f %s\n", path)
			continue
		}
		if err := runSudoArgvReverse([]string{"rm", "-f", path}, re); err != nil {
			return err
		}
	}
	return nil
}

func reverseRmFileUser(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func reverseRmDir(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -rf %s\n", path)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

func reverseServiceDisable(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, unit := range op.Targets {
		argv := []string{"systemctl", "disable", "--now", unit}
		if op.Scope == ScopeUser {
			argv = []string{"systemctl", "--user", "disable", "--now", unit}
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
				continue
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
			continue
		}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

func reverseServiceRemove(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, path := range op.Targets {
		if op.Scope == ScopeUser {
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
				continue
			}
			_ = os.Remove(path)
			continue
		}
		_ = runSudoArgvReverse([]string{"rm", "-f", path}, re)
	}
	return nil
}

func reverseRemoveDropin(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	for _, path := range op.Targets {
		if op.Scope == ScopeUser {
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
				continue
			}
			_ = os.Remove(path)
			// Also try to remove the now-empty .d parent directory.
			_ = os.Remove(filepath.Dir(path))
			continue
		}
		_ = runSudoArgvReverse([]string{"rm", "-f", path}, re)
		_ = runSudoArgvReverse([]string{"rmdir", "--ignore-fail-on-non-empty", filepath.Dir(path)}, re)
	}
	return nil
}

func reverseRestoreEnabled(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepServices() {
		return nil
	}
	// If the unit was enabled before ov touched it, re-enable after
	// disable. The disable in reverseServiceDisable will have stopped
	// it; we re-enable (and restart) here so the user's prior state is
	// preserved.
	for _, unit := range op.Targets {
		argv := []string{"systemctl", "enable", "--now", unit}
		if op.Scope == ScopeUser {
			argv = []string{"systemctl", "--user", "enable", "--now", unit}
			if re.reverseDryRun() {
				fmt.Fprintf(os.Stderr, "[dry-run] %s\n", strings.Join(argv, " "))
				continue
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			_ = cmd.Run()
			continue
		}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

func reverseRemoveManaged(op ReverseOp, re ReverseExecutor) error {
	// Managed-block removal happens at the session level, not per-op.
	// This kind is present for completeness but the runHostDel path
	// calls RemoveManagedBlock directly when the last deploy is torn
	// down.
	return nil
}

func reverseRemoveEnvdFile(op ReverseOp, re ReverseExecutor) error {
	for _, path := range op.Targets {
		if re.reverseDryRun() {
			fmt.Fprintf(os.Stderr, "[dry-run] rm -f %s\n", path)
			continue
		}
		_ = os.Remove(path)
	}
	return nil
}

func reverseRemoveRepoFile(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepRepoChanges() {
		return nil
	}
	for _, path := range op.Targets {
		if err := runSudoArgvReverse([]string{"rm", "-f", path}, re); err != nil {
			return err
		}
	}
	return nil
}

func reverseCoprDisable(op ReverseOp, re ReverseExecutor) error {
	if re.reverseKeepRepoChanges() {
		return nil
	}
	for _, copr := range op.Targets {
		argv := []string{"dnf", "-y", "copr", "disable", copr}
		_ = runSudoArgvReverse(argv, re)
	}
	return nil
}

// runSudoArgvReverse is the reverse-side analog of runSudoArgs. Accepts
// a possibly DEBIAN_FRONTEND-prefixed argv (we strip the prefix and
// set it as env instead).
func runSudoArgvReverse(argv []string, re ReverseExecutor) error {
	if re.reverseDryRun() {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo "+strings.Join(argv, " "))
		return nil
	}
	// Check for an env-var prefix like DEBIAN_FRONTEND=noninteractive.
	envPrefix := []string{}
	for len(argv) > 0 && strings.Contains(argv[0], "=") {
		envPrefix = append(envPrefix, argv[0])
		argv = argv[1:]
	}
	if len(argv) == 0 {
		return nil
	}
	fullArgv := append([]string{}, argv...)
	if len(envPrefix) > 0 {
		fullArgv = append([]string{"env"}, envPrefix...)
		fullArgv = append(fullArgv, argv...)
	}
	cmd := exec.Command("sudo", fullArgv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
