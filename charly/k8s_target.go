package main

// -----------------------------------------------------------------------------
// K8sDeployTarget — the fourth DeployTarget implementation (Part F).
// Sibling of OCITarget (install_plan.go), ContainerDeployTarget, LocalDeployTarget.
//
// Unlike host target (which applies layers directly to the local filesystem)
// or container target (which emits podman quadlets), k8s target produces a
// Kustomize base + overlay tree under <dir>/.opencharly/k8s/<name>/ — which
// a subsequent `charly deploy sync` or `kubectl apply -k` applies to the cluster.
// -----------------------------------------------------------------------------

// K8sDeployTarget implements DeployTarget for kubernetes deploys. It doesn't
// consume install plans the same way LocalDeployTarget does (plans describe
// host/container mutations; K8s deploys describe desired cluster state
// instead). Instead, its Emit is a no-op wrapper — K8s manifest generation
// happens via GenerateK8sKustomize called separately by `charly deploy add
// --target kubernetes`, which has direct access to (deployment, capabilities,
// cluster profile) without going through the install-plan IR.
type K8sDeployTarget struct {
	// Capabilities read from the pushed OCI image (baked at build time).
	Capabilities *Capabilities

	// Deployment — the merged deployment spec (charly.yml:deployments.<name>
	// + ~/.config/charly/charly.yml overlay).
	Deploy DeploymentNode

	// Instance — blank for the bare image name; otherwise the instance name
	// after the "image/" prefix (e.g. "prod" for "openclaw/prod"). Used to
	// name the overlay dir under overlays/<instance>/.
	Instance string

	// OutputDir — where to emit the Kustomize tree. Defaults to
	// <ProjectDir>/.opencharly/k8s/<deployment-name>/.
	OutputDir string
}

// Name satisfies the DeployTarget interface.
func (k *K8sDeployTarget) Name() string { return "k8s" }

// Emit is a no-op for the K8s target — manifest generation is driven
// directly from (capabilities, deployment, cluster profile) via
// GenerateK8sKustomize, not from the install-plan IR.
func (k *K8sDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	return nil
}
