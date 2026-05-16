package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// deploy_add_cmd_k8s.go — runK8s and runK8sDel: the K8s sibling of
// runHost/runContainer/runVM. Wires the tree-walker dispatch in
// deploy_add_cmd.go (lines ~329 and ~461 used to return error
// stubs — they now call into these functions).
//
// K8s doesn't consume the InstallPlan IR — K8sDeployTarget.Emit is a
// no-op stub by design. The real work is GenerateK8sKustomize, which
// reads (Capabilities, DeploymentNode, K8sSpec/cluster) and emits a
// Kustomize tree. runK8s wraps that with the same ephemeral lifecycle
// hooks runVM and runContainer use, so target=k8s ephemerals pick up
// TTL timer + recursive teardown automatically.
//
// runK8sDel runs `kubectl delete -k <overlay>` followed by removing
// the kustomize tree from disk. The ephemeral teardown hook fires
// before disk cleanup so the helper can read the runtime metadata.

// runK8s handles `ov deploy add <name>` for target: k8s entries.
// plans is unused (k8s doesn't consume install plans); kept in the
// signature for symmetry with runVM / runContainer.
func (c *DeployAddCmd) runK8s(plans []*InstallPlan, dir string, opts EmitOpts) error {
	_ = plans

	// Ephemeral lifecycle hook — registers timer + parent linkage
	// BEFORE any kustomize/kubectl invocation. Same one-line pattern
	// as runVM and runContainer.
	if node, ok := loadDeployConfigForRead("ov deploy add k8s ephemeral-register").LookupKey(c.Name); ok && node.IsEphemeral() {
		if _, regErr := RegisterEphemeralLifecycle(&node, c.Name); regErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
		}
	}

	// Resolve the deployment node + cluster reference.
	dc, err := LoadDeployConfig()
	if err != nil {
		return fmt.Errorf("loading deploy.yml: %w", err)
	}
	if dc == nil {
		return fmt.Errorf("deploy %q: no deploy.yml; nothing to deploy", c.Name)
	}
	node, ok := dc.Deploy[c.Name]
	if !ok {
		return fmt.Errorf("deploy %q: no deployment entry in deploy.yml", c.Name)
	}

	// Resolve the cluster profile from kind:k8s entries.
	uf, ufOk, err := LoadUnified(dir)
	if err != nil {
		return fmt.Errorf("loading overthink.yml: %w", err)
	}
	if !ufOk {
		return fmt.Errorf("deploy %q: no overthink.yml in project (k8s deploys need cluster spec)", c.Name)
	}
	clusterName := node.K8s
	if clusterName == "" {
		return fmt.Errorf("deploy %q: target=k8s requires `k8s:` (cluster reference) on the deployment entry", c.Name)
	}
	cluster, ok := uf.K8s[clusterName]
	if !ok {
		return fmt.Errorf("deploy %q: cluster %q not declared in overthink.yml's k8s: section", c.Name, clusterName)
	}

	// Resolve image + capabilities.
	imageRef := node.Image
	if imageRef == "" {
		imageRef = c.Name
	}
	rt, rerr := ResolveRuntime()
	if rerr != nil {
		return fmt.Errorf("resolving runtime: %w", rerr)
	}
	caps, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return fmt.Errorf("deploy %q: extracting capabilities from image %q: %w", c.Name, imageRef, err)
	}

	// Output dir mirrors the path DeployFromImage uses by default.
	outDir, err := defaultK8sOutputDir()
	if err != nil {
		return err
	}

	overlayPath, err := GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: c.Name,
		ImageRef:       imageRef,
		Deploy:         node,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
	if err != nil {
		return fmt.Errorf("deploy %q: generating kustomize: %w", c.Name, err)
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
		return fmt.Errorf("deploy %q: kubectl apply -k %s: %w", c.Name, overlayPath, err)
	}
	return nil
}

// runK8sDel handles `ov deploy del <name>` for target: k8s entries.
func (c *DeployDelCmd) runK8sDel(paths *LedgerPaths) error {
	_ = paths
	outDir, err := defaultK8sOutputDir()
	if err != nil {
		return err
	}
	overlayPath := filepath.Join(outDir, c.Name, "overlays", "default")

	if c.DryRun {
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

	// Ephemeral lifecycle teardown.
	if node, ok := loadDeployConfigForRead("ov deploy del k8s ephemeral-teardown").LookupKey(c.Name); ok && node.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&node, c.Name); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}

	// Remove the per-deployment tree.
	root := filepath.Join(outDir, c.Name)
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("removing kustomize tree %s: %w", root, err)
	}
	fmt.Printf("removed kustomize tree at %s\n", root)
	return nil
}

// defaultK8sOutputDir resolves the canonical output directory for
// emitted kustomize trees. Mirrors DeployFromImage's default.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".overthink", "k8s"), nil
}
