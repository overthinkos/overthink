package main

// k3s_post.go — post-provision finalization for deploys whose candies
// included k3s-server. Runs after RetrieveCandyArtifacts has pulled the
// kubeconfig to ~/.cache/charly/clusters/<deploy>/kubeconfig.yaml.
//
// Two things happen here that the generic artifact-retricheck pipeline
// cannot:
//   1. Merge the retrieved kubeconfig into ~/.kube/config under a context
//      named after the deploy, so `kubectl --context <deploy> …` and
//      `charly check k8s nodes --cluster <deploy>` both work immediately.
//   2. Write a ClusterProfile at ~/.config/charly/clusters/<deploy>.yaml with
//      ingress.class=traefik and storage.class_default=local-path so any
//      subsequent `charly bundle add <app> --target kubernetes` that selects
//      this cluster picks up the right defaults for the k3s addon stack.
//
// Called from deploy_add_cmd.go and deploy_add_cmd_vm.go after the artifact
// retricheck step when the deploy's candy list contains "k3s-server".

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// sanitizeDeployName turns a deploy name like "vm:arch" or "stack.web.db"
// into a shell-safe, path-safe, kubeconfig-context-safe identifier.
// Colons and dots are replaced with dashes; that keeps the semantics
// identifiable ("vm:arch" → "vm-arch") without breaking file paths.
func sanitizeDeployName(s string) string {
	r := strings.NewReplacer(":", "-", ".", "-", "/", "-")
	return r.Replace(s)
}

// K3sPostProvision runs the post-provision steps for a k3s-server deploy.
// No-op when the retrieved kubeconfig path does not exist (e.g. because
// the candy did not actually include k3s-server, or the artifact
// retricheck was skipped by --dry-run).
func K3sPostProvision(deployName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home: %w", err)
	}
	safe := sanitizeDeployName(deployName)
	retrieved := filepath.Join(home, ".cache", "charly", "clusters", safe, "kubeconfig.yaml")
	if _, err := os.Stat(retrieved); err != nil {
		// Not a k3s-server deploy, or retricheck was skipped. Nothing to do.
		return nil
	}

	contextName := safe
	if err := mergeKubeconfig(retrieved, contextName); err != nil {
		return fmt.Errorf("merging kubeconfig into ~/.kube/config: %w", err)
	}
	if err := WriteClusterProfile(safe, contextName); err != nil {
		return fmt.Errorf("writing ClusterProfile: %w", err)
	}
	fmt.Fprintf(os.Stderr, "k3s cluster %q registered — kubectl --context=%s get nodes\n", deployName, contextName)
	fmt.Fprintf(os.Stderr, "ClusterProfile written to ~/.config/charly/clusters/%s.yaml (ingress=traefik, storage=local-path)\n", safe)
	return nil
}

// mergeKubeconfig reads the retrieved kubeconfig and merges it into the
// operator's ~/.kube/config under the chosen context name. Existing
// entries with the same context/cluster/user name are OVERWRITTEN —
// deploy-add is the single source of truth for clusters it manages, so
// a rebuild cleanly picks up a fresh admin cert without stale entries.
func mergeKubeconfig(retrievedPath, contextName string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	kubeConfigPath := filepath.Join(home, ".kube", "config")

	// Load the retrieved kubeconfig.
	srcCfg, err := clientcmd.LoadFromFile(retrievedPath)
	if err != nil {
		return fmt.Errorf("loading retrieved kubeconfig %s: %w", retrievedPath, err)
	}
	if len(srcCfg.Contexts) == 0 {
		return fmt.Errorf("retrieved kubeconfig has no contexts")
	}

	// k3s emits kubeconfig with one context named "default" that points
	// at the "default" cluster + "default" user. Rename each of these to
	// the deploy-named context so multiple clusters can coexist in one
	// ~/.kube/config.
	// Find the single cluster/user/context in the source — k3s always
	// emits exactly one of each.
	var srcCtxName, srcClusterName, srcUserName string
	for n := range srcCfg.Contexts {
		srcCtxName = n
		break
	}
	srcCtx := srcCfg.Contexts[srcCtxName]
	srcClusterName = srcCtx.Cluster
	srcUserName = srcCtx.AuthInfo

	// Load (or initialize) the destination kubeconfig.
	var dstCfg *clientcmdapi.Config
	if _, err := os.Stat(kubeConfigPath); err == nil {
		dstCfg, err = clientcmd.LoadFromFile(kubeConfigPath)
		if err != nil {
			return fmt.Errorf("loading existing kubeconfig %s: %w", kubeConfigPath, err)
		}
	} else {
		dstCfg = clientcmdapi.NewConfig()
	}

	// Upsert with deploy-named keys.
	dstCfg.Clusters[contextName] = srcCfg.Clusters[srcClusterName]
	dstCfg.AuthInfos[contextName] = srcCfg.AuthInfos[srcUserName]
	newCtx := clientcmdapi.NewContext()
	newCtx.Cluster = contextName
	newCtx.AuthInfo = contextName
	if srcCtx.Namespace != "" {
		newCtx.Namespace = srcCtx.Namespace
	}
	dstCfg.Contexts[contextName] = newCtx
	// Leave CurrentContext alone — we don't want adding a cluster to
	// silently switch the operator's default context.

	if err := os.MkdirAll(filepath.Dir(kubeConfigPath), 0o755); err != nil {
		return err
	}
	if err := clientcmd.WriteToFile(*dstCfg, kubeConfigPath); err != nil {
		return fmt.Errorf("writing %s: %w", kubeConfigPath, err)
	}
	return nil
}

// deployHasCandy returns true when the deploy's candy list includes the
// given candy name. Used to gate whether K3sPostProvision runs — a no-op
// check against the ordered candy slice the deploy-add dispatcher already
// has in scope.
func deployHasCandy(layers []*Candy, name string) bool {
	for _, l := range layers {
		if l != nil && l.Name == name {
			return true
		}
	}
	return false
}
