package main

// -----------------------------------------------------------------------------
// K8sDeployConfig — the `kubernetes:` sub-block on DeploymentNode. Part F.
//
// Schema v4: deploy-side K8s knobs (namespace, workload kind override,
// patches, raw manifests) stay here. Cluster-wide policy (kubeconfig
// context, admission policy, storage, ingress defaults, etc.) MOVED to
// K8sSpec (kind:k8s template) in charly/k8s_spec.go. The Cluster string field
// below is deprecated — use DeploymentNode.K8s (template ref) instead.
// -----------------------------------------------------------------------------

// K8sDeployConfig holds K8s-specific fields that genuinely can't be
// expressed by target-agnostic deployment intent.
type K8sDeployConfig struct {
	// Namespace places the workload in a K8s namespace. Optional — when
	// empty, the kind:k8s template's DefaultNamespace (or "default") is
	// used.
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// Workload is an explicit override of the kind heuristic. Accepts:
	// Deployment, StatefulSet, DaemonSet, Pod, Job, CronJob.
	Workload string `yaml:"workload,omitempty" json:"workload,omitempty"`

	// Escape hatches — applied verbatim during Kustomize emission.
	Patches []K8sPatch `yaml:"patches,omitempty" json:"patches,omitempty"`
	Raw     []string   `yaml:"raw,omitempty" json:"raw,omitempty"`
}

// K8sPatch is one Kustomize patch entry.
type K8sPatch struct {
	Target K8sPatchTarget `yaml:"target" json:"target"`
	Patch  string         `yaml:"patch" json:"patch"`
}

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
