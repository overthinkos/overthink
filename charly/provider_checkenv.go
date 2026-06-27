package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// CheckEnv is the SERIALIZABLE subset of a *Runner that crosses the wire to an
// out-of-process verb provider. A *Runner cannot be marshalled (it holds a live
// DeployExecutor + closures), so a verb provider receives this snapshot as the
// Operation.Env. It carries only what a marshallable verb needs; verbs that reach
// host-side Runner internals (http/port-reachable/kill) stay in-process and are
// never routed to an out-of-proc provider.
type CheckEnv struct {
	Box      string `json:"box"`
	Instance string `json:"instance"`
	Mode     string `json:"mode"` // "live" | "box"
	// ContainerName is the HOST-AUTHORITATIVE container name for the deployment under
	// test (charly-<box>[_<instance>], with registry-ref stripping) — computed by the
	// host so an out-of-process verb (e.g. the external appium plugin) reaches the
	// running container's engine inspect / cp without re-deriving charly's naming
	// convention. Empty for an image-context (box-mode) run or a venue with no box.
	ContainerName string   `json:"container_name"`
	Distros       []string `json:"distros"`
	Venue         string   `json:"venue"`      // r.Exec.Venue()
	VenueKind     string   `json:"venue_kind"` // r.Exec.Kind()
	// Spice carries the host-resolved, dialable SPICE endpoint for a `spice:` verb
	// (the out-of-process candy/plugin-spice provider owns no go-libvirt). nil for
	// every non-spice verb and for a spice op with no resolved VM endpoint. Set by
	// invokeVerbProvider from preresolveSpiceEndpoint (spice_preresolve.go).
	Spice *SpiceEnv `json:"spice,omitempty"`
	// Mcp carries the host-resolved MCP context for a `mcp:` verb (the out-of-process
	// candy/plugin-mcp provider owns no podman / OCI-label machinery): the declared-server
	// list (for `servers`) plus the single picked, host-routable dial endpoint (for every
	// other method). nil for every non-mcp verb. Set by invokeVerbProvider from
	// preresolveMcpEndpoint (mcp_preresolve.go).
	Mcp *McpEnv `json:"mcp,omitempty"`
	// Cdp carries the host-resolved, dialable Chrome DevTools endpoint for a `cdp:` verb
	// (the out-of-process candy/plugin-cdp provider owns no podman / venue-resolution
	// machinery): the host-reachable DevTools base URL the plugin dials for the HTTP
	// (/json) + WebSocket (CDP) surface. nil for every non-cdp verb and for a cdp op with
	// no resolved endpoint. Set by invokeVerbProvider from preresolveCdpEndpoint
	// (cdp_preresolve.go).
	Cdp *CdpEnv `json:"cdp,omitempty"`
	// Vnc carries the host-resolved, dialable RFB endpoint for a `vnc:` verb (the
	// out-of-process candy/plugin-vnc provider owns no podman / venue / go-libvirt
	// machinery): the host-reachable "host:port" the plugin dials over TCP (a container's
	// published port 5900, or a VM's libvirt-discovered <graphics type='vnc'> listener
	// bridged/tunneled to a local address) plus the resolved VNC password. nil for every
	// non-vnc verb and for a vnc op with no resolved endpoint. Set by invokeVerbProvider
	// from preresolveVncEndpoint (vnc_preresolve.go).
	Vnc *VncEnv `json:"vnc,omitempty"`
}

func runModeName(m RunMode) string {
	switch m {
	case RunModeBox:
		return "box"
	default:
		return "live"
	}
}

// snapshotCheckEnv captures the serializable invocation context for a verb
// provider call.
func snapshotCheckEnv(r *Runner, _ *Op) *CheckEnv {
	// Box is the verb's TARGET name across the wire. For a VM deployment it must be the
	// resolved vm: ENTITY name (vmTargetName) — the out-of-process vm/spice/libvirt plugins
	// prefix charly- onto it to address the live domain and cannot LoadUnified to remap a
	// deploy/bed name themselves (the go-libvirt shed dropped that in-core remap). A
	// pod/k8s/android deployment leaves VmName empty, so vmTargetName() == Box (unchanged).
	ce := &CheckEnv{Box: r.vmTargetName(), Instance: r.Instance, Distros: r.Distros, Mode: runModeName(r.Mode)}
	// The container name is meaningful only for a live (non-box) run with a real box —
	// the same condition under which a live-container verb runs at all.
	if r.Mode != RunModeBox && r.Box != "" && r.Box != "." {
		ce.ContainerName = containerNameInstance(resolveBoxName(r.Box), r.Instance)
	}
	if r.Exec != nil {
		ce.Venue = r.Exec.Venue()
		ce.VenueKind = r.Exec.Kind()
	}
	return ce
}

// pluginCheckResult is the wire form a verb provider returns (Operation.Result
// JSON for the `verb` class). Kept minimal — status + message — so the result
// round-trips cleanly without serializing the host-only *Op/timing fields of a
// full CheckResult.
type pluginCheckResult struct {
	Status  string `json:"status"` // "pass" | "fail" | "skip"
	Message string `json:"message"`
}

// decodePluginInput decodes a builtin CheckVerbProvider step's op.PluginInput map
// into the plugin's CUE-generated params struct (dst, a *params.<Verb>Input). The
// host has already validated the input against the plugin's served schema before
// RunVerb runs (runPluginVerb → validateAuthoredPluginInput), so a decode failure
// here is a programming/version defect, not an authored-input error — best-effort
// (dst is left zero on failure). The ONE decode path every in-proc plugin verb
// shares (R3): process/port/dns and the examplerunverb reference all route through it.
func decodePluginInput(input map[string]any, dst any) {
	if len(input) == 0 {
		return
	}
	if b, err := json.Marshal(input); err == nil {
		_ = json.Unmarshal(b, dst)
	}
}

// pluginInputStr returns the string value of the named key in op.PluginInput when op is
// the matching plugin verb (op.Plugin == verb), else "". Used by the NON-runner consumers
// (k8s probe generation in k8s_generate.go, the report subject label in checkrun.go) that
// read an extracted verb's discriminator (http / addr) from plugin_input after the verb
// left the closed #Op (so op.HTTP / op.Addr no longer exist). The runner itself decodes
// the full typed params via decodePluginInput in each verb's RunVerb.
func pluginInputStr(op *Op, verb string) string {
	if op == nil || op.Plugin != verb || op.PluginInput == nil {
		return ""
	}
	if v, ok := op.PluginInput[verb].(string); ok {
		return v
	}
	return ""
}

// runPluginVerb dispatches the generic `plugin:` verb to its registered Provider
// (built-in OR out-of-tree, transport-invisible). This is the permanent plugin
// fall-through the foundation cutover (C0) adds; the built-in verb switch above is
// migrated into the registry in C1.
func (r *Runner) runPluginVerb(ctx context.Context, c *Op) CheckResult {
	word := c.Plugin
	res := CheckResult{Verb: "plugin"}
	prov, ok := providerRegistry.ResolveVerb(word)
	if !ok {
		// An unresolved plugin verb is a FAILURE, not a skip — a bed asserting a
		// plugin verb that never registered must go red, not fake-green (mirrors
		// the unresolvable-${HOST:...} rule).
		res.Status = TestFail
		res.Message = fmt.Sprintf("no provider registered for plugin verb %q", word)
		return res
	}
	// Validate the authored plugin_input against the plugin's SERVED CUE schema
	// (base ++ plugin) BEFORE dispatch — a typo / missing / empty marker is a hard
	// failure here, never a silent runtime surprise. Transport-invisible: the def
	// comes from the process-wide schema set the load gate filled, identically for a
	// builtin and an external plugin.
	inputJSON := []byte("{}")
	if c.PluginInput != nil {
		j, err := marshalJSON(c.PluginInput)
		if err != nil {
			res.Status = TestFail
			res.Message = "plugin verb: marshal plugin_input: " + err.Error()
			return res
		}
		inputJSON = j
	}
	if err := validateAuthoredPluginInput(ClassVerb, word, inputJSON); err != nil {
		res.Status = TestFail
		res.Message = err.Error()
		return res
	}
	// A CheckVerbProvider plugin unit is IN-PROCESS and keeps the live executor: an
	// EXECUTION-NEEDING verb (one that reaches r.Exec / the *Runner) dispatches via
	// RunVerb, which carries the *Runner that cannot cross the wire. Only an
	// OUT-OF-PROCESS provider falls through to invokeVerbProvider, which marshals the
	// Op into the Invoke envelope (necessarily dropping the *Runner).
	if cv, ok := prov.(CheckVerbProvider); ok {
		res = cv.RunVerb(ctx, r, c)
		res.Verb = "plugin"
		return res
	}
	res = r.invokeVerbProvider(ctx, prov, word, c)
	res.Verb = "plugin"
	return res
}

// invokeVerbProvider marshals the Op + the check env, Invokes the provider's OpRun, and
// decodes the pluginCheckResult into a CheckResult. It is the transport-invisible verb
// dispatch shared by the `plugin:` verb (runPluginVerb, after plugin_input validation)
// AND the external-charly-verb path (a live verb word — cdp/kube/… — whose provider is
// OUT-OF-PROCESS, not a CheckVerbProvider): an external verb reads the FULL Op it is
// handed here (params_json), so a verb's params stay authored in #Op with NO migration
// when its implementation moves out-of-tree. The caller sets res.Verb.
func (r *Runner) invokeVerbProvider(ctx context.Context, prov Provider, word string, c *Op) CheckResult {
	res := CheckResult{}
	// Resolve a relative committed-APK path (appium: install-app, `apk: ./tests/data/…`)
	// against the ORIGINATING candy's source tree HOST-side, BEFORE marshaling — an
	// out-of-process verb has no Runner.CandyDirs, so it cannot anchor the fixture itself.
	// Same walk-up the in-proc adb verb uses in runCharlyVerb (R3); the plugin then sees
	// an absolute, candy-anchored path.
	if c.Apk != "" {
		resolved, err := r.resolveCheckApk(c.Apk, c.Origin)
		if err != nil {
			res.Status = TestFail
			res.Message = fmt.Sprintf("verb %q: %v", word, err)
			return res
		}
		if resolved != c.Apk {
			cc := *c
			cc.Apk = resolved
			c = &cc
		}
	}
	// Pre-resolve a `kube:` op's `cluster: <profile>` to a concrete kubeconfig context
	// host-side — an out-of-process kube verb cannot reach core's project loader
	// (findK8sSpec) to map a ClusterProfile name to a context. Copy-on-write, like the
	// apk path above; a no-op for every non-kube verb.
	c = preresolveKubeCluster(c)
	// Pre-resolve a `spice:` op's VM (r.Box) to a dialable SPICE endpoint host-side — an
	// out-of-process spice verb owns no go-libvirt. A no-op for every non-spice verb;
	// for a spice op it may short-circuit with a SKIP (no SPICE device) / FAIL
	// (resolution error). The cleanup tears down any opened SSH tunnel AFTER Invoke.
	spiceEnv, spiceCleanup, spiceEarly := r.preresolveSpiceEndpoint(c)
	defer spiceCleanup()
	if spiceEarly != nil {
		return *spiceEarly
	}
	// Pre-resolve a `mcp:` op's deployment (r.Box) to the declared-server list + the
	// picked, host-routable dial endpoint — an out-of-process mcp verb owns no podman /
	// OCI-label machinery. A no-op for every non-mcp verb; for a mcp op it may
	// short-circuit with a FAIL (no mcp_provides / resolution error).
	mcpEnv, mcpEarly := r.preresolveMcpEndpoint(c)
	if mcpEarly != nil {
		return *mcpEarly
	}
	// Pre-resolve a `cdp:` op's deployment (r.Box) to the host-reachable Chrome DevTools
	// base URL — an out-of-process cdp verb owns no venue/port-mapping machinery. A no-op
	// for every non-cdp verb; for a cdp op it may short-circuit with a FAIL (endpoint
	// resolution error). The cleanup tears down any opened SSH forward AFTER Invoke (the
	// forward carries the live CDP HTTP/WebSocket connection), so defer it across Invoke.
	cdpEnv, cdpCleanup, cdpEarly := r.preresolveCdpEndpoint(c)
	defer cdpCleanup()
	if cdpEarly != nil {
		return *cdpEarly
	}
	// Pre-resolve a `vnc:` op's deployment (r.Box) to the host-reachable RFB address — an
	// out-of-process vnc verb owns no venue/port-mapping/libvirt machinery. A no-op for
	// every non-vnc verb; for a vnc op it may short-circuit with a SKIP (VM declares no
	// VNC display device) / FAIL (endpoint resolution error). The cleanup tears down any
	// opened ssh forward / bridge listener / SSH tunnel AFTER Invoke (it carries the live
	// RFB connection), so defer it across Invoke.
	vncEnv, vncCleanup, vncEarly := r.preresolveVncEndpoint(c)
	defer vncCleanup()
	if vncEarly != nil {
		return *vncEarly
	}
	params, err := marshalJSON(c)
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: marshal op: %v", word, err)
		return res
	}
	ce := snapshotCheckEnv(r, c)
	ce.Spice = spiceEnv
	ce.Mcp = mcpEnv
	ce.Cdp = cdpEnv
	ce.Vnc = vncEnv
	env, err := marshalJSON(ce)
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: marshal env: %v", word, err)
		return res
	}
	// Attach the host's live executor over the E3b reverse channel when the provider is
	// out-of-process (executorInvoker — the grpcProvider) and a live venue executor exists,
	// so an EXEC-based external check verb (record — and dbus/wl when they externalize) can
	// call BACK RunCapture/GetFile
	// against the running container. A port-based external verb (cdp/vnc/mcp/spice/kube)
	// never dials the broker; a builtin verb never reaches here (a CheckVerbProvider
	// dispatches in-proc via RunVerb in runPluginVerb).
	op := &Operation{Reserved: word, Op: OpRun, Params: params, Env: env}
	var out *Result
	if ei, ok := prov.(executorInvoker); ok && r.Exec != nil {
		out, err = ei.InvokeWithExecutor(ctx, op, r.Exec)
	} else {
		out, err = prov.Invoke(ctx, op)
	}
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: %v", word, err)
		return res
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: decode result: %v", word, err)
		return res
	}
	switch pr.Status {
	case "pass":
		res.Status = TestPass
	case "skip":
		res.Status = TestSkip
	default:
		res.Status = TestFail
	}
	res.Message = pr.Message
	return res
}
