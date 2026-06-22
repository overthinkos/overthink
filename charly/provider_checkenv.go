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
	Box       string   `json:"box"`
	Instance  string   `json:"instance"`
	Mode      string   `json:"mode"` // "live" | "box"
	Distros   []string `json:"distros"`
	Venue     string   `json:"venue"`      // r.Exec.Venue()
	VenueKind string   `json:"venue_kind"` // r.Exec.Kind()
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
	ce := &CheckEnv{Box: r.Box, Instance: r.Instance, Distros: r.Distros, Mode: runModeName(r.Mode)}
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
	params, err := marshalJSON(c)
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: marshal op: %v", word, err)
		return res
	}
	env, err := marshalJSON(snapshotCheckEnv(r, c))
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("verb %q: marshal env: %v", word, err)
		return res
	}
	out, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpRun, Params: params, Env: env})
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
