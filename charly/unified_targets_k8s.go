package main

// unified_targets_k8s.go — Add / Del / Test / Update for the k8s target.
//
// K8s doesn't consume the InstallPlan IR — K8sDeployTarget.Emit is a
// no-op by design. The real work is GenerateK8sKustomize, which reads
// (Capabilities, BundleNode, cluster) and emits a Kustomize tree.
// Add wraps that with the ephemeral lifecycle hook; Del runs `kubectl
// delete -k` then removes the tree. Bodies lifted from the former
// per-kind k8s add/del paths.
//
// K8sUnifiedTarget is NOT a LifecycleTarget — cluster lifecycle is
// kubectl-managed outside charly.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Add emits a Kustomize tree for a target:k8s deployment and (with
// --apply) runs `kubectl apply -k`. plans is ignored (k8s doesn't
// consume the IR). node fields come from dctx.Node (the dispatch-merged
// node) — including the ephemeral check, never a charly.yml re-read.
func (t *K8sUnifiedTarget) Add(ctx context.Context, dctx *DeployContext, plans []*InstallPlan, opts EmitOpts) error {
	_ = plans
	node := dctx.Node
	if node == nil {
		return fmt.Errorf("deploy %q: no deployment entry in charly.yml", t.NodeName)
	}

	// Ephemeral lifecycle hook (FIRST action). Consumes the MERGED node.
	registerEphemeralIfMarked(node, t.NodeName)

	// Resolve the cluster profile from kind:k8s entries.
	uf, ufOk, err := LoadUnified(dctx.Dir)
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ufOk {
		return fmt.Errorf("deploy %q: no charly.yml in project (k8s deploys need cluster spec)", t.NodeName)
	}
	clusterName := node.From
	if clusterName == "" {
		return fmt.Errorf("deploy %q: target=k8s requires `k8s:` (cluster reference) on the deployment entry", t.NodeName)
	}
	cluster, ok := uf.K8s[clusterName]
	if !ok {
		return fmt.Errorf("deploy %q: cluster %q not declared in charly.yml's k8s: section", t.NodeName, clusterName)
	}

	// Resolve image + capabilities.
	imageRef := node.Image
	if imageRef == "" {
		imageRef = t.NodeName
	}
	rt, rerr := ResolveRuntime()
	if rerr != nil {
		return fmt.Errorf("resolving runtime: %w", rerr)
	}
	caps, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return fmt.Errorf("deploy %q: extracting capabilities from image %q: %w", t.NodeName, imageRef, err)
	}

	outDir, err := defaultK8sOutputDir()
	if err != nil {
		return err
	}

	overlayPath, err := GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: t.NodeName,
		ImageRef:       imageRef,
		Deploy:         *node,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
	if err != nil {
		return fmt.Errorf("deploy %q: generating kustomize: %w", t.NodeName, err)
	}
	fmt.Printf("emitted kustomize tree at %s\n", overlayPath)

	if opts.DryRun {
		fmt.Println("[dry-run] not running kubectl apply -k")
		return nil
	}
	if !opts.K8sApply {
		fmt.Printf("(skipping kubectl apply -k; pass --apply to deploy)\n")
		return nil
	}
	cmd := exec.Command("kubectl", "apply", "-k", overlayPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("deploy %q: kubectl apply -k %s: %w", t.NodeName, overlayPath, err)
	}
	return nil
}

// Del runs `kubectl delete -k <overlay>` then removes the kustomize tree
// from disk. The ephemeral teardown hook fires before disk cleanup so it
// can read the runtime metadata. Body lifted from the former per-kind k8s-del path.
func (t *K8sUnifiedTarget) Del(ctx context.Context, opts DelOpts) error {
	outDir, err := defaultK8sOutputDir()
	if err != nil {
		return err
	}
	overlayPath := filepath.Join(outDir, t.NodeName, "overlays", "default")

	if opts.DryRun {
		fmt.Printf("[dry-run] would run kubectl delete -k %s and remove the tree\n", overlayPath)
		return nil
	}

	if _, err := os.Stat(overlayPath); err == nil {
		cmd := exec.Command("kubectl", "delete", "-k", overlayPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: kubectl delete -k %s: %v (continuing with disk cleanup)\n", overlayPath, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "note: no kustomize tree at %s; skipping kubectl delete\n", overlayPath)
	}

	// Ephemeral lifecycle teardown. Read from charly.yml here (teardown
	// runs without a dispatch-merged node in hand — Del's contract is
	// node-free), matching every other kind's Del.
	if node, ok := loadDeployConfigForRead("charly bundle del k8s ephemeral-teardown").LookupKey(t.NodeName); ok && node.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&node, t.NodeName); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}

	root := filepath.Join(outDir, t.NodeName)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("removing kustomize tree %s: %w", root, err)
	}
	fmt.Printf("removed kustomize tree at %s\n", root)
	return nil
}

// Test / Update are not supported on the k8s target — the cluster is
// managed out-of-band via `kubectl apply -k` on the rendered tree.
func (t *K8sUnifiedTarget) Test(ctx context.Context, checks []Op, opts TestOpts) error {
	return fmt.Errorf("k8s %q: %w", t.NodeName, ErrNotSupportedOnK8s)
}
func (t *K8sUnifiedTarget) Update(ctx context.Context, plans []*InstallPlan, opts UpdateOpts) error {
	return fmt.Errorf("k8s %q: %w", t.NodeName, ErrNotSupportedOnK8s)
}
