package main

// The built-in deploy targets as DeployTargetProviders. Each constructs its
// UnifiedDeployTarget unchanged — the migration is behavior-preserving; the
// ResolveTarget dispatch switch is replaced by providerRegistry.ResolveDeploy.

// localTarget (the `local` deploy target) lives in its own dedicated file
// (plugin_deploy_local.go) as the externalizable dedicated-provider pattern.

type vmTarget struct{ builtinDeployBase }

func (vmTarget) Reserved() string { return "vm" }
func (vmTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &VmUnifiedTarget{NodeName: name}, nil
}

type podTarget struct{ builtinDeployBase }

func (podTarget) Reserved() string { return "pod" }
func (podTarget) ResolveTarget(node *BundleNode, name string) (UnifiedDeployTarget, error) {
	// BaseImageRef is the image the rebuild's build/check steps target; node.Image is
	// the charly.yml `box:` field (Rebuild falls back to NodeName when empty).
	return &PodUnifiedTarget{NodeName: name, BaseImageRef: node.Image}, nil
}

type k8sTarget struct{ builtinDeployBase }

func (k8sTarget) Reserved() string { return "k8s" }
func (k8sTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &K8sUnifiedTarget{NodeName: name}, nil
}

type androidTarget struct{ builtinDeployBase }

func (androidTarget) Reserved() string { return "android" }
func (androidTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &AndroidUnifiedTarget{NodeName: name}, nil
}
