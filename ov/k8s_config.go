package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------------
// K8sDeployConfig — the `kubernetes:` sub-block on DeploymentNode. Part F.
//
// Deliberately thin: only fields that can't be expressed generically live
// here. Cluster-wide concerns (storage class names, ingress class, cert
// issuer, secret backend) live in ClusterProfile (see below), selected via
// `kubernetes.cluster:` in the deployment.
// -----------------------------------------------------------------------------

// K8sDeployConfig holds K8s-specific fields that genuinely can't be
// expressed by target-agnostic deployment intent. Most K8s-specific choices
// (storage class, ingress class, cert issuer, pod-security level) live in
// ClusterProfile, not here.
type K8sDeployConfig struct {
	// Cluster names the ~/.config/ov/clusters/<name>.yaml (or in-repo
	// clusters/<name>.yaml) profile to apply. Required when target: kubernetes.
	Cluster string `yaml:"cluster,omitempty"`

	// Namespace places the workload in a K8s namespace. Optional — when empty,
	// the cluster profile's default namespace (or "default") is used.
	Namespace string `yaml:"namespace,omitempty"`

	// Workload is an explicit override of the kind heuristic (service + storage
	// → StatefulSet, etc.). Use sparingly; prefer declaring generic
	// `deployment.kind:` instead. Accepts: Deployment, StatefulSet, DaemonSet,
	// Pod, Job, CronJob.
	Workload string `yaml:"workload,omitempty"`

	// Escape hatches — applied verbatim during Kustomize emission.
	Patches []K8sPatch `yaml:"patches,omitempty"` // strategic / JSON6902 patches
	Raw     []string   `yaml:"raw,omitempty"`     // paths to raw manifests copied into base/raw/
}

// K8sPatch is one Kustomize patch entry. Body is a raw YAML string applied
// as-is; Target names the target resource (kind, name optionally).
type K8sPatch struct {
	Target K8sPatchTarget `yaml:"target"`
	Patch  string         `yaml:"patch"`
}

// K8sPatchTarget identifies which generated resource a patch applies to.
type K8sPatchTarget struct {
	Kind      string `yaml:"kind,omitempty"`
	Name      string `yaml:"name,omitempty"`
	Namespace string `yaml:"namespace,omitempty"`
}

// -----------------------------------------------------------------------------
// ClusterProfile — the only place cluster-specific K8s knobs live. Part F.8.
// -----------------------------------------------------------------------------

// ClusterProfile holds cluster-wide K8s emission knobs: storage class names,
// ingress class, cert issuer, secret backend, observability mode, etc. Loaded
// from ~/.config/ov/clusters/<name>.yaml (per-user) or clusters/<name>.yaml
// (in-repo, discoverable via discover.clusters:).
type ClusterProfile struct {
	Version int    `yaml:"version"`
	Kind    string `yaml:"kind,omitempty"` // "cluster-profile"
	Name    string `yaml:"name"`

	// kubectl context name — used by `ov deploy sync` to target the right
	// cluster; can also be overridden per-command via --context.
	KubeconfigContext string `yaml:"kubeconfig_context,omitempty"`

	// Pod Security Admission level: restricted | baseline | privileged.
	// Drives the pod-level securityContext defaults the generator emits.
	AdmissionPolicy string `yaml:"admission_policy,omitempty"`

	// Default namespace when deployment.kubernetes.namespace is unset.
	DefaultNamespace string `yaml:"default_namespace,omitempty"`

	// Storage defaults
	Storage ClusterStorage `yaml:"storage,omitempty"`

	// Ingress defaults
	Ingress ClusterIngress `yaml:"ingress,omitempty"`

	// Gateway API preference (emitted as HTTPRoute instead of Ingress when
	// gateway_api.enabled: true).
	GatewayAPI ClusterGatewayAPI `yaml:"gateway_api,omitempty"`

	// Secret backend: external-secrets | sealed-secrets | raw.
	Secrets ClusterSecrets `yaml:"secrets,omitempty"`

	// Image pull defaults
	Images ClusterImages `yaml:"images,omitempty"`

	// Pod-wide defaults (tolerations, priority class, node selector baseline).
	PodDefaults ClusterPodDefaults `yaml:"pod_defaults,omitempty"`

	// Observability: ServiceMonitor emission for prometheus-tagged ports.
	Observability ClusterObservability `yaml:"observability,omitempty"`

	// Network-policy emission mode: auto (derive from env_requires graph) |
	// strict (deny-all default) | none.
	NetworkPolicy string `yaml:"network_policy,omitempty"`

	// Cluster-wide labels + annotations applied to every generated resource.
	Defaults ClusterResourceDefaults `yaml:"defaults,omitempty"`
}

type ClusterStorage struct {
	ClassDefault     string `yaml:"class_default,omitempty"`
	ClassCheap       string `yaml:"class_cheap,omitempty"`
	ClassEncrypted   string `yaml:"class_encrypted,omitempty"`
	ClassFast        string `yaml:"class_fast,omitempty"`
	AccessModeDefault string `yaml:"access_mode_default,omitempty"`
}

type ClusterIngress struct {
	Enabled         bool   `yaml:"enabled,omitempty"`
	Class           string `yaml:"class,omitempty"`
	CertIssuer      string `yaml:"cert_issuer,omitempty"`
	PathTypeDefault string `yaml:"path_type_default,omitempty"`
}

type ClusterGatewayAPI struct {
	Enabled      bool   `yaml:"enabled,omitempty"`
	GatewayClass string `yaml:"gateway_class,omitempty"`
}

type ClusterSecrets struct {
	Backend string `yaml:"backend,omitempty"` // external-secrets | sealed-secrets | raw
	Store   string `yaml:"store,omitempty"`   // ExternalSecret SecretStore name
	Prefix  string `yaml:"prefix,omitempty"`  // prepended to secret keys (e.g., "prod/")
}

type ClusterImages struct {
	PullPolicy  string   `yaml:"pull_policy,omitempty"`  // IfNotPresent | Always | Never
	PullSecrets []string `yaml:"pull_secrets,omitempty"` // imagePullSecrets names
}

type ClusterPodDefaults struct {
	PriorityClass string              `yaml:"priority_class,omitempty"`
	Tolerations   []map[string]any    `yaml:"tolerations,omitempty"`
	NodeSelector  map[string]string   `yaml:"node_selector,omitempty"`
}

type ClusterObservability struct {
	ServiceMonitor         bool   `yaml:"service_monitor,omitempty"`
	ServiceMonitorInterval string `yaml:"service_monitor_interval,omitempty"`
}

type ClusterResourceDefaults struct {
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// LoadClusterProfile loads a cluster profile by name. Resolution order:
//  1. In-repo: <dir>/clusters/<name>.yaml
//  2. Per-user: $XDG_CONFIG_HOME/ov/clusters/<name>.yaml (or ~/.config/ov/clusters/<name>.yaml)
func LoadClusterProfile(dir, name string) (*ClusterProfile, error) {
	inRepo := filepath.Join(dir, "clusters", name+".yaml")
	if fileExists(inRepo) {
		return readClusterProfile(inRepo)
	}
	configDir, err := os.UserConfigDir()
	if err == nil {
		userPath := filepath.Join(configDir, "ov", "clusters", name+".yaml")
		if fileExists(userPath) {
			return readClusterProfile(userPath)
		}
	}
	return nil, fmt.Errorf("cluster profile %q not found (looked in %s and ~/.config/ov/clusters)", name, inRepo)
}

func readClusterProfile(path string) (*ClusterProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var p ClusterProfile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if p.Name == "" {
		// Derive name from filename when unset.
		base := filepath.Base(path)
		p.Name = base[:len(base)-len(filepath.Ext(base))]
	}
	return &p, nil
}
