package main

// deploy_peers.go — sibling `peer:` deployments: the ONE shared lifecycle.
//
// A DeploymentNode's `peer:` map declares companion deployments brought up
// ALONGSIDE it on the shared `charly` network (NOT nested inside it). The canonical
// case is a Chrome driver pod that CDP-probes a web-server subject via a check
// with `on: <peer>` (see check_peer.go); peers are reachable by
// `${PEER_HOST:<name>}` and are never check-live'd themselves.
//
// foldPeers registers each peer as a top-level, addressable Deploy entry at
// load time (inheriting the owner's disposability), so a peer is brought
// up/torn down by the SAME `charly config`/`charly start`/`charly remove` verbs the deploy
// path already uses — no parallel bring-up logic (R3). bringUpPeers /
// tearDownPeers are the single shared helpers, invoked by BOTH the kind:check
// bed runner (check_bed_run.go) and the operator deploy path
// (deploy_add_cmd.go) — `peer:` works identically for check and deploy from one
// codebase.

import (
	"fmt"
	"os"
	"sort"
	"time"
)

// foldPeers copies every deploy node's `peer:` entries into the Deploy map as
// top-level addressable entries (PeerOf set, disposability inherited), so every
// deploy verb resolves a peer by name through the same path as any deploy.
// Runs AFTER foldCheckBeds (so a bed's peers fold too) and BEFORE
// validateDeploymentTree (so folded peers get the same deploy validation). A
// peer name colliding with any existing deploy/bed/peer entry is a hard error.
func foldPeers(uf *UnifiedFile) error {
	if uf == nil || len(uf.Deploy) == 0 {
		return nil
	}
	// Collect first (we mutate the map below). Iterate a sorted owner list so
	// a collision between two owners' peers is reported deterministically.
	type pendingPeer struct {
		key        string
		node       DeploymentNode
		owner      string
		disposable bool
	}
	var pending []pendingPeer
	for _, owner := range sortedDeployKeys(uf.Deploy) {
		ownerNode := uf.Deploy[owner]
		for _, peerKey := range sortedPeerKeys(ownerNode.Peer) {
			peerNode := ownerNode.Peer[peerKey]
			if peerNode == nil {
				return fmt.Errorf("deploy %q peer %q is empty", owner, peerKey)
			}
			pending = append(pending, pendingPeer{
				key:        peerKey,
				node:       *peerNode,
				owner:      owner,
				disposable: ownerNode.IsDisposable(),
			})
		}
	}
	for _, p := range pending {
		if _, clash := uf.Deploy[p.key]; clash {
			return fmt.Errorf(
				"peer name %q (declared under deploy %q) collides with an existing deploy/bed/peer entry — peer names must be globally unique; rename it",
				p.key, p.owner)
		}
		node := p.node
		node.PeerOf = p.owner
		// A companion inherits its owner's disposability so the owner's
		// teardown/rebuild (e.g. a kind:check bed's charly update) is authorized to
		// destroy + rebuild it too.
		if p.disposable {
			disposable := true
			node.Disposable = &disposable
		}
		uf.Deploy[p.key] = node
	}
	return nil
}

// validatePeers enforces the peer-specific invariants beyond the generic deploy
// validation (which already runs on the folded peers): peer keys carry no `.`
// (dots are reserved for nested dotted-path addressing) and reference a valid
// target kind. Pod-target peers get the required-image: check via the generic
// validateDeploymentTree on the folded entry.
func validatePeers(uf *UnifiedFile) error {
	if uf == nil {
		return nil
	}
	for _, owner := range sortedDeployKeys(uf.Deploy) {
		node := uf.Deploy[owner]
		for _, peerKey := range sortedPeerKeys(node.Peer) {
			if err := validateDeploymentName(peerKey, owner+" (peer)"); err != nil {
				return err
			}
			peerNode := node.Peer[peerKey]
			if peerNode == nil {
				continue
			}
			switch peerNode.Target {
			case "", "pod", "vm", "local", "k8s", "android":
				// "" defaults to pod; only these target kinds are valid.
			default:
				return fmt.Errorf("deploy %q peer %q has unsupported target %q (must be pod, vm, local, k8s, or android)", owner, peerKey, peerNode.Target)
			}
		}
	}
	return nil
}

// sortedPeerKeys returns the peer keys of a node in deterministic order.
func sortedPeerKeys(peers map[string]*DeploymentNode) []string {
	if len(peers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(peers))
	for k := range peers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bringUpPeers brings up every peer of `node` ALONGSIDE the (already-deployed)
// owner, in deterministic order, on the shared `charly` network. Each peer is a
// folded top-level deploy entry, so bring-up reuses the standard pod pipeline
// verbatim: persist the peer's declared deploy overrides (so its declared
// `port:` actually publishes — `charly config` otherwise sources ports from image
// labels behind an operator -p), then `charly config <peer>` + `charly start <peer>`,
// then wait for readiness. A non-pod peer (target: vm/local) is registered via
// `charly deploy add <peer>`. The SAME helper serves the kind:check bed runner and
// the operator deploy path (R3). Idempotent on an already-running peer.
func bringUpPeers(node *DeploymentNode) error {
	if node == nil || len(node.Peer) == 0 {
		return nil
	}
	for _, peerKey := range sortedPeerKeys(node.Peer) {
		peerNode := node.Peer[peerKey]
		// Seed the per-host charly.yml with the peer's deploy-shaped overrides
		// (port / volume / env / security / network) so its declared port:
		// publishes to the host — the cross-deployment cdp/vnc/mcp probe reaches
		// the driver via that host-published port.
		persistBedDeployOverrides(peerKey, *peerNode)
		if isPodPeer(peerNode) {
			for _, step := range [][]string{{"config", peerKey}, {"start", peerKey}} {
				if err := runCharlySubcommand(step...); err != nil {
					return fmt.Errorf("peer %q (%v): %w", peerKey, step, err)
				}
			}
			waitForContainerReady(peerKey, 30*time.Second)
		} else {
			if err := runCharlySubcommand("deploy", "add", peerKey); err != nil {
				return fmt.Errorf("peer %q (deploy add): %w", peerKey, err)
			}
		}
	}
	return nil
}

// tearDownPeers tears down every peer of `node` (best-effort, deterministic
// order) — the companion to bringUpPeers. Pod peers are removed + purged; non-
// pod peers are reversed via `charly deploy del`. Never fails the owner's teardown.
func tearDownPeers(node *DeploymentNode) {
	if node == nil || len(node.Peer) == 0 {
		return
	}
	for _, peerKey := range sortedPeerKeys(node.Peer) {
		peerNode := node.Peer[peerKey]
		var err error
		if isPodPeer(peerNode) {
			err = runCharlySubcommand("remove", peerKey, "--purge")
		} else {
			err = runCharlySubcommand(deployDelArgv(peerKey)...)
		}
		if err != nil {
			// Best-effort teardown never fails the owner's teardown — but a
			// silent discard once hid a flag-parse abort that leaked the peer
			// (see CHANGELOG), so surface it as a warning instead of swallowing.
			fmt.Fprintf(os.Stderr, "warning: peer %q teardown: %v\n", peerKey, err)
		}
	}
}

// isPodPeer reports whether a peer node is a container (pod) deployment — the
// default target. Pod peers go through config+start; other targets through
// deploy add.
func isPodPeer(node *DeploymentNode) bool {
	return node != nil && (node.Target == "" || node.Target == "pod")
}
