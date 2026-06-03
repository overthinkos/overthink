package main

import (
	"os"
	"path/filepath"
)

// deploy_add_cmd_k8s.go — shared k8s-target helper(s).
//
// The k8s deploy/teardown logic lives on K8sUnifiedTarget.Add /
// K8sUnifiedTarget.Del (unified_targets_k8s.go); both call the output-dir
// resolver below. K8s doesn't consume the InstallPlan IR — the real work
// is GenerateK8sKustomize, which reads (Capabilities, DeploymentNode,
// K8sSpec/cluster) and emits a Kustomize tree.

// defaultK8sOutputDir resolves the canonical output directory for
// emitted kustomize trees. Mirrors DeployFromImage's default.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".overthink", "k8s"), nil
}
