package main

// Registers the (package-less, shared-scope) egress schemas for the Kubernetes
// manifests charly generates: the per-object envelope and the Kustomization file.
func init() {
	registerCueKind("k8s_object", "#K8sObject")
	registerCueKind("kustomization", "#Kustomization")
}
