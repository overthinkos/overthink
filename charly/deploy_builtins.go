package main

// The built-in deploy targets as DeployTargetProviders. Each constructs its
// UnifiedDeployTarget unchanged — the migration is behavior-preserving; the
// ResolveTarget dispatch switch + the legacy-spelling alias switch are replaced by
// providerRegistry.ResolveDeploy + RegisterBuiltinAlias.

type localTarget struct{ builtinDeployBase }

func (localTarget) Reserved() string { return "local" }
func (localTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &LocalUnifiedTarget{NodeName: name}, nil
}

type vmTarget struct{ builtinDeployBase }

func (vmTarget) Reserved() string { return "vm" }
func (vmTarget) ResolveTarget(_ *BundleNode, name string) (UnifiedDeployTarget, error) {
	return &VmUnifiedTarget{NodeName: name}, nil
}

type podTarget struct{ builtinDeployBase }

func (podTarget) Reserved() string { return "pod" }
func (podTarget) ResolveTarget(node *BundleNode, name string) (UnifiedDeployTarget, error) {
	// BaseImageRef is the image the rebuild's build/check steps target; node.Box is
	// the charly.yml `box:` field (Rebuild falls back to NodeName when empty).
	return &PodUnifiedTarget{NodeName: name, BaseImageRef: node.Box}, nil
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

func init() {
	for _, p := range []DeployTargetProvider{
		localTarget{}, vmTarget{}, podTarget{}, k8sTarget{}, androidTarget{},
	} {
		RegisterBuiltinProvider(p)
	}
	// Legacy spellings → canonical (the former alias-normalization switch).
	RegisterBuiltinAlias(ClassDeployTarget, "host", "local")
	RegisterBuiltinAlias(ClassDeployTarget, "container", "pod")
	RegisterBuiltinAlias(ClassDeployTarget, "kubernetes", "k8s")
	if err := checkDeployProviderBijection(); err != nil {
		panic(err)
	}
}
