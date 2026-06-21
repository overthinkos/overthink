package main

import (
	"context"
	"sort"
	"strings"

	adb "github.com/zach-klippenstein/goadb"
)

// AndroidCollector is the kind:android SubstrateCollector. It enumerates every
// declared `target: android` deploy node (top-level AND nested under a pod) from
// the merged deploy set (charly.yml's folded kind:check beds + ~/.config/charly/
// charly.yml), resolves each to a live AndroidDevice via resolveAndroidDevice,
// and probes the backing adb server's host:devices for online / offline. A node
// whose device can't be resolved (emulator pod down, endpoint unreachable) is
// reported as absent rather than aborting the command (graceful degradation —
// the SubstrateCollector contract).
//
// Every row is stamped Kind=SubstrateAndroid, Source="adb". Container carries
// the device serial (or the in-pod container name when the serial is empty),
// and the Network cell notes the venue: "in-pod (<container>)" for a box
// device, "endpoint <host:port>" for a remote adb endpoint.
//
// Under opts.Nested the collector additionally polls sys.boot_completed on a
// reachable device so the table distinguishes "adb online but still booting"
// from "fully booted" — the same readiness condition the android Add gates on.
type AndroidCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &AndroidCollector{c: c} })
}

// Kind reports the android substrate.
func (a *AndroidCollector) Kind() SubstrateKind { return SubstrateAndroid }

// Available reports whether any `target: android` deploy is declared. With no
// android device declared there is nothing to probe and the collector is
// skipped silently.
func (a *AndroidCollector) Available(opts CollectOpts) bool {
	return len(collectAndroidDeployNodes(opts)) > 0
}

// Collect resolves and probes every declared android device. The work is
// sequential — there are at most a handful of android devices and each probe is
// a single cheap adb round-trip — so no worker pool is warranted.
func (a *AndroidCollector) Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error) {
	nodes := collectAndroidDeployNodes(opts)
	rows := make([]DeploymentStatus, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, a.collectOne(opts, n))
	}
	return rows, nil
}

// androidDeployNode is one declared `target: android` deploy node together with
// the dotted deploy path that addresses it (e.g.
// "check-android-emulator-pod.device") — the path resolveAndroidDevice needs to
// locate the in-pod parent container for a nested device.
type androidDeployNode struct {
	path string
	node BundleNode
}

// collectAndroidDeployNodes is the SINGLE enumeration of every `target: android`
// deploy node, shared by Available and Collect (no duplicated walk). It merges
// the charly.yml projection (incl. folded kind:check beds) with the local
// charly.yml — local wins per key, mirroring resolveTreeRoot's
// MergeDeployConfigs(projectDC, localDC) precedence — then pre-order walks every
// root so nested devices are discovered with their full dotted path.
func collectAndroidDeployNodes(opts CollectOpts) []androidDeployNode {
	merged := MergeDeployConfigs(unifiedDeployConfig(opts.Unified), opts.Deploy)
	if merged == nil || merged.Bundle == nil {
		return nil
	}
	var out []androidDeployNode
	for _, name := range sortedDeployKeys(merged.Bundle) {
		root := merged.Bundle[name]
		_ = bundleWalkPreOrder(&root, name, func(path string, node *BundleNode) error {
			if node != nil && node.Target == "android" {
				out = append(out, androidDeployNode{path: path, node: *node})
			}
			return nil
		})
	}
	return out
}

// unifiedDeployConfig projects a UnifiedFile to its BundleConfig (folded
// kind:check beds included) or nil when the file is absent.
func unifiedDeployConfig(uf *UnifiedFile) *BundleConfig {
	if uf == nil {
		return nil
	}
	return uf.ProjectBundleConfig()
}

// sortedDeployKeys returns the deploy map keys in name order so the android
// table is deterministic across runs.
func sortedDeployKeys(m map[string]BundleNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectOne builds the status row for one declared android device node. It
// resolves the kind:android spec, resolves the live device handle, and probes
// the adb server state. Resolution / probe failures degrade to an "absent" row
// — never an error that would drop the whole substrate.
func (a *AndroidCollector) collectOne(opts CollectOpts, dn androidDeployNode) DeploymentStatus {
	row := DeploymentStatus{
		Kind:    SubstrateAndroid,
		Source:  "adb",
		Image:   dn.path,
		Status:  "absent",
		RunMode: opts.RunMode,
	}

	spec := lookupAndroidSpec(opts.Unified, dn.node.From)
	if spec == nil {
		// Device reference not declared — surface the deploy path with an
		// absent status so the misconfiguration is visible, not swallowed.
		row.Container = dn.node.From
		return row
	}
	row.Container = spec.EffectiveSerial()
	if spec.IsEndpoint() {
		row.Network = "endpoint " + spec.Adb.Host
	} else if spec.Box != "" {
		row.Network = "in-pod " + spec.Box
	}

	dev, err := resolveAndroidDevice(spec, &dn.node, dn.path)
	if err != nil {
		// Emulator pod not running / endpoint unreachable — absent is the
		// correct, graceful answer.
		return row
	}
	if dev.Engine != "" && dev.Container != "" {
		row.Network = "in-pod (" + dev.Container + ")"
	}

	// Probe the adb server for this device's state (online / offline / …).
	d, err := adbDeviceForAddr(dev.AdbAddr, dev.serial())
	if err != nil {
		return row
	}
	state, err := d.State()
	if err != nil {
		// adb reachable but the device isn't enumerated by host:devices.
		return row
	}
	row.Status = adbStatusLabel(state)

	// --nested: poll the kernel boot flag so a freshly-started emulator that
	// has attached to adb but is still booting is distinguishable from one
	// that has reached steady state. Same readiness condition the android Add
	// gates on; a single non-blocking getprop, never a sleep loop.
	if opts.Nested && state == adb.StateOnline {
		if out, err := d.RunCommand("getprop", "sys.boot_completed"); err == nil && strings.TrimSpace(out) == "1" {
			row.Uptime = "boot_completed"
		} else {
			row.Uptime = "booting"
		}
	}
	return row
}

// lookupAndroidSpec resolves a kind:android device by name from the unified
// config. Returns nil when the file or the device is absent.
func lookupAndroidSpec(uf *UnifiedFile, name string) *AndroidSpec {
	if uf == nil || uf.Android == nil || name == "" {
		return nil
	}
	return uf.Android[name]
}

// adbStatusLabel renders a goadb DeviceState for the unified status table. It
// reuses adbStateString's vocabulary for every non-online state (offline /
// unauthorized / disconnected / …) and only overrides the online case: the
// `adb devices` CLI prints the literal "device" for an online device, but the
// status table's online/offline/absent vocabulary wants "online". Keeping the
// shared adbStateString switch and overriding the single divergent case avoids
// a parallel state map.
func adbStatusLabel(state adb.DeviceState) string {
	if state == adb.StateOnline {
		return "online"
	}
	return adbStateString(state)
}
