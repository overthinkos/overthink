// CUE schema for the `k8s` kind. #K8s validates ONE value of the `k8s:` map
// (K8sSpec; absorbed the former ClusterProfile). CLOSED — every K8sSpec field is
// modeled, so an unknown key is a typo. The documented enum domains + sub-object
// shapes are constrained. ONE exception: `pod_default.tolerations` stays OPEN —
// it is a genuine passthrough of raw Kubernetes Toleration objects
// ([]map[string]any). Plural field names that mirror Kubernetes output keys are
// preserved verbatim. Shared #Step from _common.cue.

#K8s: {
	// May be empty (a cluster-policy-only template runs no workload itself).
	box: string

	replica?:   int & >=0
	resources?: #K8sResources
	hostnames?: [...#K8sHostname]

	kubeconfig_context?: string
	admission_policy?:   "restricted" | "baseline" | "privileged"
	default_namespace?:  *"default" | (string & =~"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")

	storage?:        #K8sStorage
	ingress?:        #K8sIngressDefaults
	gateway_api?:    #K8sGatewayAPI
	secret?:         #K8sSecretsBackend
	image_default?:  #K8sImagesDefaults
	pod_default?:    #K8sPodDefaults
	observability?:  #K8sObservability
	network_policy?: "auto" | "strict" | "none"
	defaults?:       #K8sResourceDefaults

	plan?: [...#Step]
}

#K8sResources: {
	requests?: #K8sResourceValues
	limits?:   #K8sResourceValues
}
#K8sResourceValues: {
	cpu?:    string & =~"^[0-9]+(\\.[0-9]+)?m?$"
	memory?: string & =~"^[0-9]+(\\.[0-9]+)?(Ei|Pi|Ti|Gi|Mi|Ki|E|P|T|G|M|k|m)?$"
}
#K8sHostname: {
	host: string & =~"^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$"
	tls?:  bool
	path?: string & =~"^/"
}
#K8sStorage: {
	class_default?:       string
	class_cheap?:         string
	class_encrypted?:     string
	class_fast?:          string
	access_mode_default?: "ReadWriteOnce" | "ReadWriteMany" | "ReadOnlyMany" | "ReadWriteOncePod"
}
#K8sIngressDefaults: {
	enabled?:           bool
	class?:             string
	cert_issuer?:       string
	path_type_default?: "Prefix" | "Exact" | "ImplementationSpecific"
}
#K8sGatewayAPI: {
	enabled?:       bool
	gateway_class?: string
}
#K8sSecretsBackend: {
	backend?: "external-secrets" | "sealed-secrets" | "raw"
	store?:   string
	prefix?:  string
}
#K8sImagesDefaults: {
	pull_policy?: "IfNotPresent" | "Always" | "Never"
	pull_secrets?: [...string]
}
#K8sPodDefaults: {
	priority_class?: string
	// Raw Kubernetes Toleration objects (Go []map[string]any) — genuine
	// passthrough, so each element stays OPEN.
	tolerations?: [...{...}]
	node_selector?: [string]: string
}
#K8sObservability: {
	service_monitor?:          bool
	service_monitor_interval?: string
}
#K8sResourceDefaults: {
	labels?: [string]:      string
	annotations?: [string]: string
}
