package main

// -----------------------------------------------------------------------------
// K8sDeployConfig — the `kubernetes:` sub-block on BundleNode. Part F.
//
// Schema v4: deploy-side K8s knobs (namespace, workload kind override,
// patches, raw manifests) stay here. Cluster-wide policy (kubeconfig
// context, admission policy, storage, ingress defaults, etc.) MOVED to
// K8sSpec (kind:k8s template) in charly/k8s_spec.go. The Cluster string field
// below is deprecated — use BundleNode.K8s (template ref) instead.
// -----------------------------------------------------------------------------

// K8sPatchTarget identifies which generated resource a patch applies to.
type K8sPatchTarget struct {
	Kind      string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Name      string `yaml:"name,omitempty" json:"name,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// Schema v4: ClusterProfile / LoadClusterProfile / clusters/*.yaml loaders
// have been removed. Cluster config lives on K8sSpec (kind:k8s entities in
// charly.yml / k8s.yml). `charly migrate` synthesizes a kind:k8s
// entry from any pre-existing clusters/<name>.yaml.
