package main

// K8sSpec is the target template for kind:k8s deployments. It absorbs BOTH
// workload-level defaults (image ref, replicas, resources, workload
// ingress hostnames) AND cluster-wide policy (kubeconfig context,
// admission policy, default namespace, storage class defaults, ingress
// defaults, gateway API, secrets backend, pod defaults, observability,
// network policy).
//
// In schema v4 the separate ClusterProfile concept is dropped — if
// multiple workloads share the same cluster policy, they share a
// kind:k8s template (via YAML anchors or by referencing the same
// template from multiple deployments).
type K8sSpec struct {
	// Box is the kind:box name this workload runs. Required.
	Box string `yaml:"box"`

	// --- Workload-level defaults ---

	// Replicas default for deployments using this template. Nil means
	// "use generator default" (typically 1 for Deployment, N for
	// StatefulSet). Deployment `replicas:` overrides.
	Replica *int `yaml:"replica,omitempty"`

	// Resources declares per-container CPU / memory requests and limits.
	// Deployment `resources:` deep-merges on top (see override semantics
	// in the plan).
	Resources *K8sResources `yaml:"resources,omitempty"`

	// Hostnames lists workload-specific ingress entries (hostname + tls
	// bool + optional path). Cluster-wide ingress class / cert-issuer
	// live in IngressDefaults (below). Deployment `hostnames:` replaces.
	Hostnames []K8sHostname `yaml:"hostnames,omitempty"`

	// --- Cluster-wide policy (absorbed from the former ClusterProfile) ---

	// KubeconfigContext names the kubectl context to target. Required if
	// the deployment is to reach a real cluster. Can be overridden
	// per-command via --context.
	KubeconfigContext string `yaml:"kubeconfig_context,omitempty"`

	// AdmissionPolicy is the Pod Security Admission level for emitted
	// manifests: restricted | baseline | privileged.
	AdmissionPolicy string `yaml:"admission_policy,omitempty"`

	// DefaultNamespace places the workload in a K8s namespace when the
	// deployment's namespace is unset. Defaults to "default".
	DefaultNamespace string `yaml:"default_namespace,omitempty"`

	// Storage carries cluster-wide storage class defaults.
	Storage K8sStorage `yaml:"storage,omitempty"`

	// Ingress carries cluster-wide ingress policy (class, cert-issuer).
	// Distinct from workload Hostnames (above). Renamed to keep
	// consumer sites stable.
	Ingress K8sIngressDefaults `yaml:"ingress,omitempty"`

	// GatewayAPI toggles HTTPRoute emission instead of Ingress when
	// enabled.
	GatewayAPI K8sGatewayAPI `yaml:"gateway_api,omitempty"`

	// Secret picks a secret backend: external-secrets | sealed-secrets
	// | raw.
	Secret K8sSecretsBackend `yaml:"secret,omitempty"`

	// ImageDefault carries cluster-wide image pull defaults. Renamed
	// from `images:` (plural) to `image_default:` (singular, semantic)
	// to avoid yaml-tag collision with the workload's `image:` field
	// above (field-singular cutover, 2026-05).
	ImageDefault K8sImagesDefaults `yaml:"image_default,omitempty"`

	// PodDefault are cluster-wide tolerations / nodeSelector / priority
	// class defaults applied to every generated pod spec.
	PodDefault K8sPodDefaults `yaml:"pod_default,omitempty"`

	// Observability toggles ServiceMonitor emission for prometheus-
	// tagged ports.
	Observability K8sObservability `yaml:"observability,omitempty"`

	// NetworkPolicy selects emission mode: auto | strict | none.
	NetworkPolicy string `yaml:"network_policy,omitempty"`

	// Defaults are cluster-wide labels + annotations applied to every
	// generated resource.
	Defaults K8sResourceDefaults `yaml:"defaults,omitempty"`

	// --- Target-specific tests (optional) ---

	Eval       []Check `yaml:"eval,omitempty"`
	DeployEval []Check `yaml:"deploy_eval,omitempty"`
}

// K8sResources is per-container CPU / memory requests + limits.
type K8sResources struct {
	Requests K8sResourceValues `yaml:"requests,omitempty"`
	Limits   K8sResourceValues `yaml:"limits,omitempty"`
}

// K8sResourceValues names the two resource axes K8s tracks natively.
type K8sResourceValues struct {
	CPU    string `yaml:"cpu,omitempty"`    // e.g. "500m", "2"
	Memory string `yaml:"memory,omitempty"` // e.g. "512Mi", "4Gi"
}

// K8sHostname is one workload ingress entry.
type K8sHostname struct {
	Host string `yaml:"host"`           // e.g. "app.example.com"
	TLS  bool   `yaml:"tls,omitempty"`  // emit TLS block
	Path string `yaml:"path,omitempty"` // default "/"
}

// K8sStorage carries cluster-wide storage class defaults (formerly
// ClusterStorage).
type K8sStorage struct {
	ClassDefault      string `yaml:"class_default,omitempty"`
	ClassCheap        string `yaml:"class_cheap,omitempty"`
	ClassEncrypted    string `yaml:"class_encrypted,omitempty"`
	ClassFast         string `yaml:"class_fast,omitempty"`
	AccessModeDefault string `yaml:"access_mode_default,omitempty"`
}

// K8sIngressDefaults carries cluster-wide ingress policy (formerly
// ClusterIngress).
type K8sIngressDefaults struct {
	Enabled         bool   `yaml:"enabled,omitempty"`
	Class           string `yaml:"class,omitempty"`
	CertIssuer      string `yaml:"cert_issuer,omitempty"`
	PathTypeDefault string `yaml:"path_type_default,omitempty"`
}

// K8sGatewayAPI toggles HTTPRoute emission (formerly ClusterGatewayAPI).
type K8sGatewayAPI struct {
	Enabled      bool   `yaml:"enabled,omitempty"`
	GatewayClass string `yaml:"gateway_class,omitempty"`
}

// K8sSecretsBackend picks a secret backend (formerly ClusterSecrets).
type K8sSecretsBackend struct {
	Backend string `yaml:"backend,omitempty"` // external-secrets | sealed-secrets | raw
	Store   string `yaml:"store,omitempty"`   // ExternalSecret SecretStore name
	Prefix  string `yaml:"prefix,omitempty"`  // prepended to secret keys
}

// K8sImagesDefaults carries cluster-wide image pull defaults (formerly
// ClusterImages).
type K8sImagesDefaults struct {
	PullPolicy  string   `yaml:"pull_policy,omitempty"`  // IfNotPresent | Always | Never
	PullSecrets []string `yaml:"pull_secrets,omitempty"` // imagePullSecrets names
}

// K8sPodDefaults carries cluster-wide pod defaults (formerly
// ClusterPodDefaults).
type K8sPodDefaults struct {
	PriorityClass string            `yaml:"priority_class,omitempty"`
	Tolerations   []map[string]any  `yaml:"tolerations,omitempty"`
	NodeSelector  map[string]string `yaml:"node_selector,omitempty"`
}

// K8sObservability toggles ServiceMonitor emission (formerly
// ClusterObservability).
type K8sObservability struct {
	ServiceMonitor         bool   `yaml:"service_monitor,omitempty"`
	ServiceMonitorInterval string `yaml:"service_monitor_interval,omitempty"`
}

// K8sResourceDefaults carries cluster-wide labels + annotations
// (formerly ClusterResourceDefaults).
type K8sResourceDefaults struct {
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}
