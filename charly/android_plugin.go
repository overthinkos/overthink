package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// android_plugin.go — the host-side invoker that routes the `target: android` deploy +
// the `charly status` android-device ops through the SAME out-of-process adb provider
// the `adb:` check verb uses (candy/plugin-adb). It exists so the heavy goadb ADB-wire
// dependency lives ENTIRELY in the plugin, out of charly's core go.mod: the core resolves
// the device's adb-server address host-side
// (engine inspect, no goadb — adbAddrForContainer in android_deploy_cmd.go), builds a
// synthetic #Op + this AdbDeviceEnv, and hands them to the plugin's Invoke, which speaks
// the ADB wire protocol and returns a {status,message} verdict.

// AdbDeviceEnv is the deploy/status half of the env the adb plugin decodes (the json
// tags MUST match candy/plugin-adb/device.go's adbEnv deploy fields exactly). Unlike the
// check verb's CheckEnv (box/instance/mode/container_name, from which the plugin resolves
// the device's adb-server port itself), the deploy/status path resolves the AdbAddr
// host-side and ships it pre-resolved here, plus the in-pod Engine/Container venue and the
// google-play creds the by-package installer needs.
type AdbDeviceEnv struct {
	AdbAddr     string `json:"adb_addr"`
	Engine      string `json:"engine"`
	Container   string `json:"container"`
	Serial      string `json:"serial"`
	GoogleEmail string `json:"google_email"`
	GoogleToken string `json:"google_token"`
}

// invokeAdbPlugin dispatches one synthetic adb #Op (install / install-app / uninstall /
// wait-for-device / …) to the registered out-of-process adb provider with the AdbDeviceEnv,
// and returns the plugin's message (or an error when the plugin reports a failure). It is a
// swappable package-level var (like InspectContainer) so the deploy/status callers stay
// unit-testable without a live plugin. Mirrors invokeVerbProvider's Operation envelope +
// pluginCheckResult decode (R3) — the difference is only the env shape (a deploy-resolved
// AdbDeviceEnv vs the runner's CheckEnv snapshot).
var invokeAdbPlugin = func(op *Op, env AdbDeviceEnv) (string, error) {
	prov, ok := providerRegistry.ResolveVerb("adb")
	if !ok {
		return "", fmt.Errorf("adb plugin not loaded — the android deploy must compose candy/plugin-adb (its provider serves the goadb-backed device ops)")
	}
	params, err := marshalJSON(op)
	if err != nil {
		return "", fmt.Errorf("adb plugin: marshal op: %w", err)
	}
	envJSON, err := marshalJSON(env)
	if err != nil {
		return "", fmt.Errorf("adb plugin: marshal env: %w", err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: "adb", Op: OpRun, Params: params, Env: envJSON})
	if err != nil {
		return "", fmt.Errorf("adb plugin: %w", err)
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		return "", fmt.Errorf("adb plugin: decode result: %w", err)
	}
	if pr.Status == "fail" {
		return pr.Message, fmt.Errorf("adb plugin: %s", pr.Message)
	}
	return pr.Message, nil
}
