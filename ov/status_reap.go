package main

import (
	"fmt"
	"os"
	"os/exec"
)

// ReapOrphansCmd finds and cleans up orphaned ephemeral deployments —
// entries whose deploy.yml ledger says "active" but whose underlying engine
// resource (libvirt domain, podman container, k8s namespace) is gone. Lifted
// out of the old `ov status --reap-orphans` flag so StatusCmd stays single-
// purpose.
//
// Pure orphan detection — no race resolution. If a teardown is concurrently
// in progress, the second `ov deploy del --force` no-ops on the already-
// removed pieces.
type ReapOrphansCmd struct{}

func (c *ReapOrphansCmd) Run() error {
	dc, err := LoadDeployConfig()
	if err != nil {
		return fmt.Errorf("loading deploy.yml: %w", err)
	}
	if dc == nil {
		fmt.Println("no deploy.yml; nothing to reap")
		return nil
	}
	var orphans []string
	for name, node := range dc.Deployment {
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if node.VmState.Ephemeral.Status != "active" {
			continue
		}
		if !ephemeralUnderlyingResourceAlive(name, node) {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) == 0 {
		fmt.Println("no orphaned ephemerals")
		return nil
	}
	for _, name := range orphans {
		fmt.Printf("reaping orphan %q ...\n", name)
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "deploy", "del", name, "--force")
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: ov deploy del %q: %v\n", name, err)
		}
	}
	return nil
}

// ephemeralUnderlyingResourceAlive returns true when the named ephemeral's
// underlying resource is still alive. Best-effort across targets — false
// negatives are OK (we just skip reaping that entry); false positives are
// bad (we'd nuke a still-running resource), so the checks lean conservative.
func ephemeralUnderlyingResourceAlive(name string, node DeploymentNode) bool {
	switch node.Target {
	case "vm":
		conn, err := connectLibvirt(libvirtSessionURI)
		if err != nil {
			return true // can't probe → conservative: assume alive
		}
		defer conn.Close()
		domName := "ov-" + node.Vm
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			domName = "ov-" + node.VmState.Ephemeral.InstanceName
		}
		if _, err := conn.lookupDomain(domName); err != nil {
			return false
		}
		return true
	case "pod", "container":
		check := exec.Command("podman", "container", "exists", "ov-"+name)
		return check.Run() == nil
	case "k8s", "kubernetes":
		ns := name
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			ns = node.VmState.Ephemeral.InstanceName
		}
		check := exec.Command("kubectl", "get", "namespace", ns)
		check.Stderr = nil
		check.Stdout = nil
		return check.Run() == nil
	}
	return true // unknown target — conservative
}
