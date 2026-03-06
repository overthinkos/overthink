package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OvNetworkName is the shared bridge network used by all ov containers.
const OvNetworkName = "ov"

// EnsureOvNetwork creates the "ov" network if it does not exist.
// It is a package-level var for testability.
var EnsureOvNetwork = defaultEnsureOvNetwork

func defaultEnsureOvNetwork(engine string) error {
	binary := EngineBinary(engine)
	// Check if network already exists
	check := exec.Command(binary, "network", "inspect", OvNetworkName)
	check.Stdout = nil
	check.Stderr = nil
	if check.Run() == nil {
		return nil
	}
	// Create it
	create := exec.Command(binary, "network", "create", OvNetworkName)
	output, err := create.CombinedOutput()
	if err != nil {
		// Handle race: network may have been created between inspect and create
		recheck := exec.Command(binary, "network", "inspect", OvNetworkName)
		recheck.Stdout = nil
		recheck.Stderr = nil
		if recheck.Run() == nil {
			return nil
		}
		return fmt.Errorf("creating %s network: %w\n%s", OvNetworkName, err, strings.TrimSpace(string(output)))
	}
	fmt.Fprintf(os.Stderr, "Created '%s' network\n", OvNetworkName)
	return nil
}

// ResolveNetwork returns the network to use for a container.
// If configured is non-empty (explicit override like "host"), it is returned as-is.
// Otherwise, the shared "ov" network is ensured and returned.
func ResolveNetwork(configured, engine string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if err := EnsureOvNetwork(engine); err != nil {
		return "", err
	}
	return OvNetworkName, nil
}
