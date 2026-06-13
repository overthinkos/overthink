package main

// deploy_add_shared.go — generic helpers shared across the per-kind
// UnifiedDeployTarget.Add methods (R3). Each one captures a step that
// was copy-pasted across the old per-kind deploy bodies; now there is
// ONE implementation, called from local/vm/pod Add.
//
// Ordering is load-bearing and preserved exactly:
//   - secrets are injected into the plans BEFORE any Emit (a candy's
//     OpStep body references the resolved token via env).
//   - artifactEnv is secretEnv first, then node.Env lines overlaid
//     (last-wins) — so a deploy entry's explicit env: overrides an
//     auto-generated secret of the same name.

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// prepareCandySecrets resolves the candies backing `plans`, computes their
// secret_requires / secret_accepts env (auto-generating + persisting any
// missing required token), and injects it into every plan's TaskSteps
// BEFORE emission. Returns the resolved candy list (the caller reuses it
// for artifact retrieval) and the secret env map.
//
// Shared by LocalUnifiedTarget.Add / VmUnifiedTarget.Add /
// PodUnifiedTarget.Add — the three paths that previously each ran
// CandyForPlan + ResolveSecretForCandy + InjectSecretsIntoPlans inline.
func prepareCandySecrets(plans []*InstallPlan, dir string) ([]*Candy, map[string]string, error) {
	candyList, err := CandyForPlan(plans, dir, nil)
	if err != nil {
		return nil, nil, err
	}
	secretEnv := ResolveSecretForCandy(candyList)
	InjectSecretsIntoPlans(plans, secretEnv)
	return candyList, secretEnv, nil
}

// buildArtifactEnv composes the env used for candy-artifact path
// substitution: the resolved secret env first, then the deploy node's
// own env: lines overlaid (last-wins). nil node contributes nothing.
//
// Shared by LocalUnifiedTarget.Add / VmUnifiedTarget.Add — both feed it
// to RetrieveCandyArtifacts so rewrite rules like ${K3S_KUBECONFIG_SERVER}
// resolve to the declared value rather than a literal placeholder. The
// node is the dispatch-merged DeploymentNode (never re-read from disk).
func buildArtifactEnv(secretEnv map[string]string, node *DeploymentNode) map[string]string {
	env := make(map[string]string, len(secretEnv))
	for k, v := range secretEnv {
		env[k] = v
	}
	if node != nil {
		for _, line := range node.Env {
			if idx := strings.Index(line, "="); idx > 0 {
				env[line[:idx]] = line[idx+1:]
			}
		}
	}
	return env
}

// retrieveArtifactsAndK3s pulls back the candies' published artifacts via
// the same executor the deploy used, then runs the k3s-server post-hook
// (merge kubeconfig + register ClusterProfile) when the candy set includes
// k3s-server. No-op under DryRun.
//
// Shared by LocalUnifiedTarget.Add / VmUnifiedTarget.Add.
func retrieveArtifactsAndK3s(ctx context.Context, exec DeployExecutor, candyList []*Candy, name string, artifactEnv map[string]string, opts EmitOpts) error {
	if opts.DryRun {
		return nil
	}
	if err := RetrieveCandyArtifacts(ctx, exec, candyList, sanitizeDeployName(name), artifactEnv, opts); err != nil {
		return err
	}
	if deployHasCandy(candyList, "k3s-server") {
		if err := K3sPostProvision(name); err != nil {
			return err
		}
	}
	return nil
}

// registerEphemeralIfMarked runs the ephemeral lifecycle registration
// (systemd transient timer + parent-detection) when the dispatch-merged
// node is ephemeral. FIRST action in vm/pod/k8s Add (panic-safe TTL
// ordering). Consumes the merged node — does NOT re-read charly.yml.
// Registration failure is logged (not fatal), matching the prior run*
// behavior; the returned error is always nil today but kept for symmetry.
func registerEphemeralIfMarked(node *DeploymentNode, name string) {
	if node == nil || !node.IsEphemeral() {
		return
	}
	if _, regErr := RegisterEphemeralLifecycle(node, name); regErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
	}
}
