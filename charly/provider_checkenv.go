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
	params, err := marshalJSON(c)
	if err != nil {
		res.Status = TestFail
		res.Message = "plugin verb: marshal op: " + err.Error()
		return res
	}
	env, err := marshalJSON(snapshotCheckEnv(r, c))
	if err != nil {
		res.Status = TestFail
		res.Message = "plugin verb: marshal env: " + err.Error()
		return res
	}
	out, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpRun, Params: params, Env: env})
	if err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("plugin verb %q: %v", word, err)
		return res
	}
	var pr pluginCheckResult
	if err := json.Unmarshal(out.JSON, &pr); err != nil {
		res.Status = TestFail
		res.Message = fmt.Sprintf("plugin verb %q: decode result: %v", word, err)
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
