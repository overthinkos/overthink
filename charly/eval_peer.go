package main

// eval_peer.go — cross-deployment probing for `charly eval`.
//
// Two seams let ONE deployment act as a test DRIVER probing a SEPARATE
// deployment as the SUBJECT (e.g. a Chrome pod CDP-probing a web-server pod):
//
//  1. The `on:` step modifier (Check.On) dispatches a probe against a named
//     DRIVER deployment instead of the subject under test. Its wiring into
//     `charly eval live` lives here (liveTargetResolver); the per-step swap is in
//     evalrun.go runOne; the harness path wires its own resolveScoringChain.
//
//  2. The ${PEER_*} address variables let the driven probe TARGET the subject
//     over the shared `charly` network or the host:
//       ${PEER_HOST:name}          -> the subject deployment's container DNS
//                                     name on the shared `charly` net (charly-<name>),
//                                     the pod->pod address. Inspect-free + it
//                                     verifies the subject is running.
//       ${PEER_ENDPOINT:name:port} -> a host-reachable 127.0.0.1:NNNN for that
//                                     deployment's <port>, via the shared
//                                     resolveEvalEndpoint (container published
//                                     port, or ssh -L forward for a VM/host
//                                     subject). The host-vantage address a
//                                     local/host driver uses to reach a pod/VM.
//
// Peer vars are pre-resolved per run and overlaid by Runner.effectiveEnv onto
// WHATEVER resolver is active (primary, on:-swapped, or harness bucket), so a
// `cdp:` check with `on: chrome` and `url: http://${PEER_HOST:web}:8080` works
// the same in `charly eval live`, a kind:eval bed, and an AI-iteration recipe (R3).

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Cross-deployment variable names. Registered runtime-only (IsRuntimeOnlyVar)
// so a build-scope check can't reference them.
const (
	peerHostVar     = "PEER_HOST"
	peerEndpointVar = "PEER_ENDPOINT"
)

// applyPeerVars scans the given checks for ${PEER_*} references, resolves each,
// and stores the result on the runner (PeerVars + peerCleanups). Idempotent and
// a no-op when no peer refs are present. The caller MUST `defer r.ClosePeers()`
// so any ssh -L forwards opened for ${PEER_ENDPOINT} against a VM/host subject
// are torn down at run end.
func applyPeerVars(r *Runner, checks []Op, instance string) {
	refs := collectPeerRefs(checks)
	if len(refs) == 0 {
		return
	}
	vars, cleanups := resolvePeerVars(refs, instance)
	if r.PeerVars == nil {
		r.PeerVars = map[string]string{}
	}
	for k, v := range vars {
		r.PeerVars[k] = v
	}
	r.peerCleanups = append(r.peerCleanups, cleanups...)
}

// applyPeerVarsScenarios is the scenario-list counterpart (harness / recipe /
// feature-run paths), flattening every step's embedded Check.
func applyPeerVarsScenarios(r *Runner, scenarios []Scenario, instance string) {
	var checks []Op
	for _, sc := range scenarios {
		for _, st := range sc.Step {
			checks = append(checks, st.Op)
		}
		for _, st := range sc.Setup {
			checks = append(checks, st.Op)
		}
		for _, st := range sc.Teardown {
			checks = append(checks, st.Op)
		}
	}
	applyPeerVars(r, checks, instance)
}

// collectPeerRefs returns the distinct ${PEER_*} variable keys referenced
// across every string field of every check (keys in the "NAME:arg" form used
// by ExpandTestVars).
func collectPeerRefs(checks []Op) []string {
	seen := map[string]bool{}
	var out []string
	for i := range checks {
		for _, p := range checks[i].StringFields() {
			if *p == "" {
				continue
			}
			for _, key := range TestVarRefs(*p) {
				name := key
				if idx := strings.IndexByte(key, ':'); idx >= 0 {
					name = key[:idx]
				}
				if name != peerHostVar && name != peerEndpointVar {
					continue
				}
				if !seen[key] {
					seen[key] = true
					out = append(out, key)
				}
			}
		}
	}
	return out
}

// resolvePeerVars resolves each ${PEER_*} key to its address. A key that can't
// be resolved (subject not running, bad port) is left OUT of the map; the
// referencing check then FAILS via runOne's unresolved-peer-var path
// (filterPeerVars) — an unreachable peer is a real failure, NEVER a SKIP (a
// skip on an unreachable dependency would be a fake pass). Returns cleanups for
// any ssh -L forwards opened.
func resolvePeerVars(refs []string, instance string) (map[string]string, []func()) {
	vars := map[string]string{}
	var cleanups []func()
	for _, key := range refs {
		name, arg, ok := splitPeerKey(key)
		if !ok {
			continue
		}
		switch name {
		case peerHostVar:
			// arg is the deployment name. Resolve to the running container's
			// DNS name on the shared `charly` net (charly-<name>); also verifies it
			// is actually running.
			if _, ctr, err := resolveContainer(arg, instance); err == nil {
				vars[key] = ctr
			} else {
				fmt.Fprintf(os.Stderr, "eval: ${%s} — %v\n", key, err)
			}
		case peerEndpointVar:
			// arg is "<name>:<port>".
			dep, portStr, hasPort := strings.Cut(arg, ":")
			if !hasPort {
				fmt.Fprintf(os.Stderr, "eval: ${%s} — expected PEER_ENDPOINT:<deployment>:<port>\n", key)
				continue
			}
			port, perr := strconv.Atoi(strings.TrimSpace(portStr))
			if perr != nil || port < 1 || port > 65535 {
				fmt.Fprintf(os.Stderr, "eval: ${%s} — invalid port %q\n", key, portStr)
				continue
			}
			venue, verr := resolveEvalVenue(dep, instance)
			if verr != nil {
				fmt.Fprintf(os.Stderr, "eval: ${%s} — %v\n", key, verr)
				continue
			}
			ep, eerr := resolveEvalEndpoint(venue, port)
			if eerr != nil {
				fmt.Fprintf(os.Stderr, "eval: ${%s} — %v\n", key, eerr)
				continue
			}
			vars[key] = ep.Addr
			cleanups = append(cleanups, ep.Close)
		}
	}
	return vars, cleanups
}

// splitPeerKey splits a "PEER_HOST:web" / "PEER_ENDPOINT:web:8080" key into the
// variable name and the remaining argument(s) (everything after the FIRST colon).
func splitPeerKey(key string) (name, arg string, ok bool) {
	idx := strings.IndexByte(key, ':')
	if idx < 0 {
		return key, "", false
	}
	return key[:idx], key[idx+1:], true
}

// filterPeerVars returns the subset of unresolved variable keys that are
// cross-deployment PEER vars (${PEER_HOST}/${PEER_ENDPOINT}). runOne FAILS a
// check that references any of these unresolved — an unresolved PEER var means
// the peer/subject is unreachable, which is a real failure, never a SKIP (a skip
// on an unreachable dependency is a fake pass). Other unresolved vars (a
// deploy-only var under build scope, an unmounted volume) stay a legitimate skip.
func filterPeerVars(missing []string) []string {
	var out []string
	for _, key := range missing {
		name := key
		if idx := strings.IndexByte(key, ':'); idx >= 0 {
			name = key[:idx]
		}
		if name == peerHostVar || name == peerEndpointVar {
			out = append(out, key)
		}
	}
	return out
}

// liveTargetResolver builds the `on:` TargetResolver used by `charly eval live`
// (and kind:eval beds, which drive `charly eval live`). For a named DRIVER
// deployment it resolves the execution venue (resolveEvalVenue — container / VM
// / local, the same classifier the interactive verbs use) plus a best-effort
// runtime var resolver (the driver's own ${HOST_PORT}/${CONTAINER_IP}). The
// per-step swap in evalrun.go also sets r.Image = <driver> so the host-side
// cdp/wl/vnc/mcp dispatch (`charly eval cdp <method> <driver>`) connects to the
// driver's endpoint. Peer/${PEER_*} addressing of the SUBJECT rides in via
// Runner.PeerVars (effectiveEnv overlay), independent of which resolver is active.
func liveTargetResolver(instance string) func(string) (*EvalVarResolver, DeployExecutor, error) {
	return func(target string) (*EvalVarResolver, DeployExecutor, error) {
		venue, err := resolveEvalVenue(target, instance)
		if err != nil {
			return nil, nil, err
		}
		res := liveDeployVarResolver(target, instance, venue)
		return res, venue.Exec, nil
	}
}

// liveDeployVarResolver builds a runtime var resolver for a named pod
// deployment (container venue). Best-effort: a non-container venue or an
// unreadable image label yields an empty resolver (the driven probe then relies
// on ${PEER_*} + literals, which is the common cross-deployment case). Shares
// the ResolveEvalVarsRuntime primitive with the primary target (R3).
func liveDeployVarResolver(name, instance string, venue *EvalVenue) *EvalVarResolver {
	if venue == nil || !venue.IsContainer() {
		return &EvalVarResolver{}
	}
	dir, _ := os.Getwd()
	var projectCfg *Config
	var deployOverlay *DeploymentNode
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
	}
	if dc := loadDeployConfigForRead("charly eval live on:"); dc != nil {
		if entry, ok := dc.Deploy[deployKey(name, instance)]; ok {
			deployOverlay = &entry
		} else if entry, ok := dc.Deploy[name]; ok {
			deployOverlay = &entry
		}
	}
	imageRef := resolveDeployBoxName(name, instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return &EvalVarResolver{}
	}
	meta, err := ExtractMetadata(venue.Engine, resolvedRef)
	if err != nil || meta == nil {
		return &EvalVarResolver{}
	}
	res, _ := ResolveEvalVarsRuntime(meta, deployOverlay, venue.Engine, name, venue.Name, instance)
	return res
}
