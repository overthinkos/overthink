package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployConfig represents per-machine deployment overrides (~/.config/ov/deploy.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
type DeployConfig struct {
	Provides *ProvidesConfig              `yaml:"provides,omitempty"`
	Images   map[string]DeploymentNode `yaml:"images"`
}

// DeploymentNode is one node in the deployments tree declared in
// deploy.yml. Every deployment is a node; each node may carry zero or
// more `children:` that run inside its environment. The node's Target
// discriminator picks the DeployTarget that owns execution:
//
//   - "host"        — local filesystem (HostDeployTarget + LocalDeployExecutor).
//   - "vm"          — a libvirt/QEMU VM referenced by VmSource (VmDeployTarget
//                     over SSHDeployExecutor).
//   - "container"   — a podman/docker container (PodDeployTarget;
//                     the default when Target is empty).
//   - "kubernetes"  — a Kustomize manifest tree (K8sDeployTarget; leaf-only,
//                     not nestable).
//
// Nested topologies compose at the executor layer: a child's DeployExecutor
// wraps its parent's executor with a "shell jump" (podman exec, virsh
// console, or an additional ssh hop). That means "container-in-vm" doesn't
// need a new target implementation — it's PodDeployTarget whose
// executor happens to be NestedExecutor{Parent: SSHDeployExecutor{…}}.
//
// Addressing is dotted path: `ov deploy add stack.web.db` walks the tree
// stack → web → db and applies that leaf plus all of its descendants in
// pre-order. Map keys MUST NOT contain `.` — the load-time validator in
// unified.go rejects them with a remediation hint.
//
// Disposability is per-node and does NOT cascade. A parent with
// disposable: true does not authorize destroying its children unattended —
// each child's flag stands on its own (see /ov-dev:disposable).
type DeploymentNode struct {
	Version    string               `yaml:"version,omitempty"`
	Status     string               `yaml:"status,omitempty"`
	Info       string               `yaml:"info,omitempty"`
	Tunnel     *TunnelYAML          `yaml:"tunnel,omitempty"`
	DNS        string               `yaml:"dns,omitempty"`
	AcmeEmail  string               `yaml:"acme_email,omitempty"`
	Volumes    []DeployVolumeConfig `yaml:"volumes,omitempty"`
	Ports      []string             `yaml:"ports,omitempty"`
	Env        []string             `yaml:"env,omitempty"`
	EnvFile    string               `yaml:"env_file,omitempty"`
	Security   *SecurityConfig      `yaml:"security,omitempty"`
	Network    string               `yaml:"network,omitempty"`
	Engine     string               `yaml:"engine,omitempty"`
	Secrets         []DeploySecretConfig `yaml:"secrets,omitempty"`
	ForwardGpgAgent *bool                `yaml:"forward_gpg_agent,omitempty"` // Override global forward_gpg_agent per image
	ForwardSshAgent *bool                `yaml:"forward_ssh_agent,omitempty"` // Override global forward_ssh_agent per image
	Sidecars        map[string]SidecarDef `yaml:"sidecars,omitempty"`          // Sidecar container overrides

	// Tests are local deploy-level overlays. They merge onto the image's
	// label-baked deploy section at runtime: entries with an id: that
	// matches a baked entry replace it; otherwise they append. An entry
	// with id:X and skip:true effectively disables the baked check.
	Tests []Check `yaml:"tests,omitempty"`

	// --- BuildTarget refactor fields (Task 13) ---
	//
	// Target selects the deploy destination. Empty or "container" →
	// the existing quadlet/podman pipeline. "host" → apply layers
	// directly to the invoking user's filesystem via HostDeployTarget.
	// "kubernetes" → emit a Kustomize tree via K8sDeployTarget (Part F).
	// Only honored when this entry's map key matches (host/kubernetes)
	// or when --target=... is passed on the CLI; a container-named
	// entry with target:host is a config error.
	Target string `yaml:"target,omitempty"`

	// Kubernetes carries the `kubernetes:` sub-block. Only consulted when
	// Target == "kubernetes". All cluster-wide K8s knobs (storage class,
	// ingress class, cert issuer) live in ClusterProfile, selected via
	// Kubernetes.Cluster — not here.
	Kubernetes *K8sDeployConfig `yaml:"kubernetes,omitempty"`

	// --- Generic target-agnostic intent fields (Part F.4).
	// Each is optional; empty defaults preserve today's behavior.

	// Kind describes the workload's intrinsic type. Drives the K8s
	// workload-kind heuristic (service + storage → StatefulSet, etc.) and
	// can inform systemd unit type or CronJob generation on other targets.
	// Empty → assumed "service" when target is kubernetes.
	Kind string `yaml:"kind,omitempty"` // service | daemon | batch | scheduled | oneshot

	// Replicas — number of instances. Ignored for single-instance workloads
	// (daemon/batch/oneshot) or non-K8s targets that don't support scaling.
	Replicas int `yaml:"replicas,omitempty"`

	// Restart policy — always | on-failure | never. K8s interprets this on
	// Pod/Job/CronJob; Deployment/StatefulSet/DaemonSet always use "Always"
	// per K8s semantics regardless of this value.
	Restart string `yaml:"restart,omitempty"`

	// Schedule — cron expression for kind: scheduled.
	Schedule string `yaml:"schedule,omitempty"`

	// Resources — CPU + memory requests. Limits come from the existing
	// Security.MemoryMax / .Cpus fields (preserves today's meaning).
	Resources *DeployResources `yaml:"resources,omitempty"`

	// Expose — external exposure intent. Target-agnostic: maps to Ingress
	// on K8s, Traefik router on container target, etc.
	Expose *DeployExpose `yaml:"expose,omitempty"`

	// Storage — declarative PVC/volume requests. Augments (does not replace)
	// the existing Volumes list which covers container-target volume backing.
	Storage []DeployStorage `yaml:"storage,omitempty"`

	// Probes — target-agnostic liveness/readiness/startup specs.
	Probes *DeployProbes `yaml:"probes,omitempty"`

	// AddLayers are overlay layer refs applied on top of the image.
	// Each entry is a DeployRef (local name / local YAML path /
	// remote github ref). Same syntax as the command-line --add-layer
	// flag.
	AddLayers []string `yaml:"add_layers,omitempty"`

	// InstallOpts carries host-target-specific flags that would
	// otherwise have to be passed on every command invocation.
	InstallOpts *InstallOptsConfig `yaml:"install_opts,omitempty"`

	// --- Schema-v3 cross-reference fields ---
	//
	// Image names the image this pod deployment is built from. When
	// non-empty, used in place of the deployment key during ref
	// resolution for target: pod (formerly container). Enables
	// deployment names like "sway-pod" that don't match an image
	// name in images.yml (e.g. openclaw-sway-browser).
	//
	// For target: vm, use VmSource instead.
	// For target: k8s, use Cluster.
	// For target: host with nested execution, use Inside.
	Image string `yaml:"image,omitempty"`

	// Cluster names a kind:cluster entity from the top-level cluster:
	// block. Only meaningful for target: k8s. The k8s: test verbs
	// can also name a cluster directly; this field is the deployment-
	// level default.
	Cluster string `yaml:"cluster,omitempty"`

	// Inside names another deployment in which this one's execution
	// venue is nested. Only meaningful for target: host — routes
	// layer application through a NestedExecutor that targets the
	// referenced deployment's executor (e.g. SSH into a vm). Enables
	// exercising the host-deploy code path against a VM guest's FS
	// with zero operator-side writes.
	Inside string `yaml:"inside,omitempty"`

	// --- VM-target fields (D9) ---

	// VmSource references a kind:vm entity by name. Only meaningful when
	// this entry represents a VM deploy (deploy name starts with "vm:"
	// or Target == "vm"). Resolves through the same unified-schema
	// loader that handles kind:image and kind:layer refs.
	VmSource string `yaml:"vm_source,omitempty"`

	// VmState is the runtime state written by VmDeployTarget on first
	// apply. Preserved across reboots so ov deploy del can reverse the
	// deploy, and so re-apply is idempotent (instance-id stays stable,
	// disk path points at the same qcow2, etc.).
	VmState *VmDeployState `yaml:"vm_state,omitempty"`

	// --- Disposable / lifecycle classification (see /ov-dev:disposable) ---

	// Disposable, when true, authorizes `ov rebuild <name>` to
	// destroy + rebuild + restart this deploy unattended. Default
	// is false (conservative; explicit opt-in). There is NO
	// derivation from Lifecycle. See CLAUDE.md R10.
	Disposable bool `yaml:"disposable,omitempty"`

	// Lifecycle is a free-form human-facing tier tag (scratch | dev |
	// test | qa | staging | prod | custom). Informational only — has
	// ZERO effect on disposability. Consumed by `ov status
	// --lifecycle <tier>` filters and display columns.
	Lifecycle string `yaml:"lifecycle,omitempty"`

	// --- Recursive tree: child deployments (schema v2) ---
	//
	// Children are DeploymentNodes whose execution venue is nested
	// inside this node's environment. A container node with a vm child
	// creates the container first, then boots the VM inside it; the
	// child's DeployExecutor composes this node's executor with a
	// shell jump (podman exec / ssh / virsh) so commands execute
	// inside the nested environment.
	//
	// Map keys MUST NOT contain `.` — dotted-path CLI addressing
	// treats `.` as a node separator (foo.bar.baz). LoadUnified
	// rejects offending keys at parse time with a remediation hint.
	Children map[string]*DeploymentNode `yaml:"children,omitempty"`
}

// IsDisposable returns the literal Disposable field. Implements the
// Classified interface.
func (c DeploymentNode) IsDisposable() bool {
	return c.Disposable
}

// HasChildren reports whether this node has any nested deployments.
// Cheap predicate used by the tree walker to decide pre-order vs
// leaf-only dispatch.
func (n *DeploymentNode) HasChildren() bool {
	return n != nil && len(n.Children) > 0
}

// WalkPreOrder invokes fn on this node, then recurses into every
// child in sorted key order. Pre-order is the add-order semantic: a
// parent's environment must exist before its children can run inside
// it, so the caller applies deploys root-first.
//
// fn receives the full dotted path to each node (e.g. "stack.web.db").
// The root path argument is prepended; callers pass the node's own
// key as `path`.
//
// When fn returns a non-nil error, traversal stops immediately and
// the error propagates.
func (n *DeploymentNode) WalkPreOrder(path string, fn func(path string, node *DeploymentNode) error) error {
	if n == nil {
		return nil
	}
	if err := fn(path, n); err != nil {
		return err
	}
	for _, k := range sortedChildKeys(n.Children) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := n.Children[k].WalkPreOrder(childPath, fn); err != nil {
			return err
		}
	}
	return nil
}

// WalkPostOrder invokes fn on every child (recursively, post-order)
// before invoking fn on this node. Post-order is the delete-order
// semantic: a child must be torn down while its parent environment
// is still alive, so the caller reverses leaves first.
func (n *DeploymentNode) WalkPostOrder(path string, fn func(path string, node *DeploymentNode) error) error {
	if n == nil {
		return nil
	}
	for _, k := range sortedChildKeys(n.Children) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := n.Children[k].WalkPostOrder(childPath, fn); err != nil {
			return err
		}
	}
	return fn(path, n)
}

// ResolveNodePath walks roots[path0].Children[path1]...[pathN] and
// returns the targeted node plus its parent chain (root-first,
// excluding the target itself). Returns a descriptive error when any
// path segment is missing so the user sees which segment doesn't
// exist.
//
// An empty path is invalid — callers dispatch to
// WalkPreOrder/WalkPostOrder against a named root instead of
// resolving "".
func ResolveNodePath(roots map[string]DeploymentNode, path string) (*DeploymentNode, []*DeploymentNode, error) {
	parts := splitDottedPath(path)
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("empty or malformed deployment path %q", path)
	}
	rootName := parts[0]
	rootEntry, ok := roots[rootName]
	if !ok {
		return nil, nil, fmt.Errorf("no deployment named %q", rootName)
	}
	current := &rootEntry
	var ancestors []*DeploymentNode
	for i := 1; i < len(parts); i++ {
		ancestors = append(ancestors, current)
		next, ok := current.Children[parts[i]]
		if !ok {
			prefix := strings.Join(parts[:i], ".")
			return nil, nil, fmt.Errorf("no child %q under %q", parts[i], prefix)
		}
		current = next
	}
	return current, ancestors, nil
}

// splitDottedPath splits a dotted deployment path into segments. An
// empty input or a path with any empty segment (leading/trailing/
// doubled dots) yields nil so callers can flag the error at their
// layer with the original offending path string.
func splitDottedPath(path string) []string {
	if path == "" {
		return nil
	}
	out := strings.Split(path, ".")
	for _, p := range out {
		if p == "" {
			return nil
		}
	}
	return out
}

// sortedChildKeys returns the keys of a children map in deterministic
// order so traversal produces stable output across runs.
func sortedChildKeys(children map[string]*DeploymentNode) []string {
	out := make([]string, 0, len(children))
	for k := range children {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LifecycleTag returns the literal Lifecycle field. Implements the
// Classified interface.
func (c DeploymentNode) LifecycleTag() string {
	return c.Lifecycle
}

// VmDeployState is the auto-managed runtime state for a vm: deploy.
// Written by VmDeployTarget on first apply; preserved across ov deploy
// add/del cycles so VM lifecycle is reversible and idempotent.
type VmDeployState struct {
	// InstanceID is the stable UUIDv4 cloud-init instance-id. Generated
	// once on first apply and persisted; re-renders produce the same
	// user-data (cloud-init treats an instance-id change as a new
	// instance, which breaks idempotency — so we pin it).
	InstanceID string `yaml:"instance_id,omitempty"`

	// DiskPath is the absolute path to the VM's qcow2 (may be a
	// copy-on-write overlay on top of a cached base image for
	// cloud_image sources).
	DiskPath string `yaml:"disk_path,omitempty"`

	// SeedIso is the absolute path to the NoCloud cidata ISO. Empty
	// when the source kind is bootc and cloud-init injection is
	// disabled (no seed ISO emitted).
	SeedIso string `yaml:"seed_iso,omitempty"`

	// SshPort is the host port forwarded to the guest's :22.
	SshPort int `yaml:"ssh_port,omitempty"`

	// SshUser is the guest account VmDeployTarget SSHes in as
	// (distinct from the host user running ov).
	SshUser string `yaml:"ssh_user,omitempty"`

	// SshKeyPath is the absolute path to the private key used for
	// VmDeployTarget's SSH sessions. May be auto-generated at first
	// apply (into ~/.local/share/ov/vm/<vm>/id_ed25519) when
	// VmSSH.KeySource == "generate", or a pre-existing user key when
	// KeySource == "auto".
	SshKeyPath string `yaml:"ssh_key_path,omitempty"`

	// Backend is the VM backend used to boot this VM: "qemu" or
	// "libvirt". Pinned at first apply so subsequent operations don't
	// thrash between backends if the user's vm.backend setting
	// changes underneath them.
	Backend string `yaml:"backend,omitempty"`

	// KeyInjectionResolved is the effective D13 state after auto
	// defaults + explicit overrides resolved. Two booleans (one per
	// channel). Informational; used by ov deploy show and for audit
	// purposes.
	KeyInjectionResolved *VmKeyInjectionResolved `yaml:"key_injection_resolved,omitempty"`

	// OvInstallStrategy is the VmOvInstall.Strategy chosen at first
	// apply: "auto", "scp", "url", or "skip". Informational.
	OvInstallStrategy string `yaml:"ov_install_strategy,omitempty"`

	// CloudInitRenderedDigest is the sha256 of the last rendered
	// user-data (structured intent + applied defaults). Lets VmDeployTarget
	// detect drift — if the current rendered user-data doesn't match
	// the recorded digest, the user changed the kind:vm entity and
	// the seed ISO needs to be regenerated before re-apply.
	CloudInitRenderedDigest string `yaml:"cloud_init_rendered_digest,omitempty"`
}

// VmKeyInjectionResolved is the effective per-channel toggle state
// after D13 auto-default resolution + explicit-wins merging.
type VmKeyInjectionResolved struct {
	SMBIOS    bool `yaml:"smbios"`
	CloudInit bool `yaml:"cloud_init"`
}

// InstallOptsConfig holds deploy.yml install_opts settings for a host
// deploy. Mirrors the command-line flags on DeployAddCmd so a user can
// pin their choices in deploy.yml instead of repeating them.
type InstallOptsConfig struct {
	WithServices     bool   `yaml:"with_services,omitempty"`
	AllowRepoChanges bool   `yaml:"allow_repo_changes,omitempty"`
	AllowRootTasks   bool   `yaml:"allow_root_tasks,omitempty"`
	SkipIncompatible bool   `yaml:"skip_incompatible,omitempty"`
	Verify           bool   `yaml:"verify,omitempty"`
	BuilderImage     string `yaml:"builder_image,omitempty"`
}

// ApplyTo merges install_opts settings into an EmitOpts. CLI flags
// still win — deploy.yml provides defaults, not overrides. Nil
// receiver is a no-op.
func (o *InstallOptsConfig) ApplyTo(opts EmitOpts) EmitOpts {
	if o == nil {
		return opts
	}
	if !opts.WithServices {
		opts.WithServices = o.WithServices
	}
	if !opts.AllowRepoChanges {
		opts.AllowRepoChanges = o.AllowRepoChanges
	}
	if !opts.AllowRootTasks {
		opts.AllowRootTasks = o.AllowRootTasks
	}
	if !opts.SkipIncompatible {
		opts.SkipIncompatible = o.SkipIncompatible
	}
	if !opts.Verify {
		opts.Verify = o.Verify
	}
	if opts.BuilderImageOverride == "" {
		opts.BuilderImageOverride = o.BuilderImage
	}
	return opts
}

// DeployVolumeConfig overrides the backing for a layer-declared volume.
type DeployVolumeConfig struct {
	Name       string `yaml:"name"`                    // matches layer volume name
	Type       string `yaml:"type,omitempty"`           // "volume" (default), "bind", "encrypted"
	Host       string `yaml:"host,omitempty"`           // explicit host path (bind type only, optional)
	Path       string `yaml:"path,omitempty"`           // container path (only for deploy-only volumes not in any layer)
	DataSeeded bool   `yaml:"data_seeded,omitempty"`    // tracks if data was provisioned from image
	DataSource string `yaml:"data_source,omitempty"`    // image:tag that provided the data
}

// DeploySecretConfig overrides or provides a secret for deployment.
type DeploySecretConfig struct {
	Name   string `yaml:"name"`              // matches layer secret name
	Source string `yaml:"source,omitempty"`   // "keyring" (default), "env:VAR", "file:/path"
}

// DeployResources — target-agnostic resource requests. Upper bounds (limits)
// come from the existing SecurityConfig (MemoryMax / Cpus). Values use K8s
// quantity strings ("500m" cpu, "512Mi" memory) which podman/systemd can
// interpret for container/host targets.
type DeployResources struct {
	CPURequest    string `yaml:"cpu_request,omitempty"`
	MemoryRequest string `yaml:"memory_request,omitempty"`
}

// DeployExpose — external exposure intent (URL host, path, TLS). Maps to
// K8s Ingress/HTTPRoute, Traefik router on container target, etc.
type DeployExpose struct {
	Host string `yaml:"host,omitempty"` // public DNS name
	Path string `yaml:"path,omitempty"` // URL path prefix, default "/"
	TLS  bool   `yaml:"tls,omitempty"`
	Port string `yaml:"port,omitempty"` // container port name or number
}

// DeployStorage — declarative storage request. class_hint is generic
// ("fast"/"cheap"/"encrypted"); the cluster profile maps it to a real
// K8s StorageClass name. access is generic
// (single-writer / many-readers / many-writers).
type DeployStorage struct {
	Name      string `yaml:"name"`
	Size      string `yaml:"size,omitempty"`       // e.g. "20Gi"
	ClassHint string `yaml:"class_hint,omitempty"` // fast | cheap | encrypted | default
	Access    string `yaml:"access,omitempty"`     // single-writer | many-readers | many-writers
	Path      string `yaml:"path,omitempty"`       // container mount path (optional — layer can declare)
}

// DeployProbes — target-agnostic probes. Each entry is a Check (same shape
// as the existing declarative test vocabulary: file, command, addr, http…).
type DeployProbes struct {
	Liveness  *Check `yaml:"liveness,omitempty"`
	Readiness *Check `yaml:"readiness,omitempty"`
	Startup   *Check `yaml:"startup,omitempty"`
}

// deployKey returns the deploy.yml map key for an image, optionally qualified by instance.
// Base images use just the image name; instances use "image/instance".
func deployKey(imageName, instance string) string {
	if instance == "" {
		return imageName
	}
	return imageName + "/" + instance
}

// parseDeployKey splits a deploy.yml map key back into image name and instance.
// "selkies-desktop" → ("selkies-desktop", "")
// "selkies-desktop/foo" → ("selkies-desktop", "foo")
func parseDeployKey(key string) (imageName, instance string) {
	if idx := strings.IndexByte(key, '/'); idx >= 0 {
		return key[:idx], key[idx+1:]
	}
	return key, ""
}

// DeployedContainerNames returns hostnames for all deployed images.
// Used to enrich NO_PROXY so Chrome (which doesn't support CIDR) can bypass
// the proxy for container-to-container traffic.
func (dc *DeployConfig) DeployedContainerNames() []string {
	if dc == nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for key := range dc.Images {
		img, inst := parseDeployKey(key)
		name := containerNameInstance(img, inst)
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	sort.Strings(names)
	return names
}

// isSameBaseImage returns true if source is the same base image (with or without instance).
func isSameBaseImage(source, imageName string) bool {
	return source == imageName || strings.HasPrefix(source, imageName+"/")
}

// DeployConfigPath returns the path to the deploy overlay file.
// Package-level var for testability (same pattern as RuntimeConfigPath).
var DeployConfigPath = defaultDeployConfigPath

func defaultDeployConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "ov", "deploy.yml"), nil
}

// LoadDeployConfig reads the deploy overlay file. Returns nil, nil if the file doesn't exist.
func LoadDeployConfig() (*DeployConfig, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var dc DeployConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &dc, nil
}

// MergeDeployOverlay patches cfg.Images in-place with deployment overrides from deploy.yml.
// Field-level replace: deploy.yml value fully replaces image.yml value.
// Unknown images in deploy.yml are silently ignored.
func MergeDeployOverlay(cfg *Config, dc *DeployConfig) {
	if dc == nil || dc.Images == nil {
		return
	}

	for name, overlay := range dc.Images {
		img, ok := cfg.Images[name]
		if !ok {
			continue // silently ignore unknown images
		}

		if overlay.Version != "" {
			img.Version = overlay.Version
		}
		if overlay.Status != "" {
			img.Status = overlay.Status
		}
		if overlay.Info != "" {
			img.Info = overlay.Info
		}
		if overlay.Tunnel != nil {
			img.Tunnel = overlay.Tunnel
		}
		if overlay.DNS != "" {
			img.DNS = overlay.DNS
		}
		if overlay.AcmeEmail != "" {
			img.AcmeEmail = overlay.AcmeEmail
		}
		if overlay.Ports != nil {
			img.Ports = overlay.Ports
		}
		if overlay.Env != nil {
			img.Env = overlay.Env
		}
		if overlay.EnvFile != "" {
			img.EnvFile = overlay.EnvFile
		}
		if overlay.Security != nil {
			img.Security = overlay.Security
		}
		if overlay.Network != "" {
			img.Network = overlay.Network
		}
		if overlay.Engine != "" {
			img.Engine = overlay.Engine
		}
		cfg.Images[name] = img
	}
}

// MergeDeployOntoMetadata applies deploy.yml overrides onto label-derived metadata.
// Same field-level replace semantics as MergeDeployOverlay.
func MergeDeployOntoMetadata(meta *ImageMetadata, dc *DeployConfig, instance string) {
	if dc == nil || dc.Images == nil || meta == nil {
		return
	}

	overlay, ok := dc.Images[deployKey(meta.Image, instance)]
	if !ok {
		return
	}

	if overlay.Status != "" {
		meta.Status = overlay.Status
	}
	if overlay.Info != "" {
		meta.Info = overlay.Info
	}
	if overlay.Tunnel != nil {
		meta.Tunnel = overlay.Tunnel
	}
	if overlay.DNS != "" {
		meta.DNS = overlay.DNS
	}
	if overlay.AcmeEmail != "" {
		meta.AcmeEmail = overlay.AcmeEmail
	}
	if overlay.Ports != nil {
		meta.Ports = overlay.Ports
	}
	if overlay.Env != nil {
		meta.Env = overlay.Env
	}
	if overlay.Security != nil {
		// Field-level merge: overlay fields override, unset fields fall
		// through to the label-provided values. A full struct replace would
		// wipe layer defaults like shm_size when a user sets just --memory-max
		// via `ov config`.
		if overlay.Security.Privileged {
			meta.Security.Privileged = true
		}
		if len(overlay.Security.CapAdd) > 0 {
			meta.Security.CapAdd = overlay.Security.CapAdd
		}
		if len(overlay.Security.Devices) > 0 {
			meta.Security.Devices = overlay.Security.Devices
		}
		if len(overlay.Security.SecurityOpt) > 0 {
			meta.Security.SecurityOpt = overlay.Security.SecurityOpt
		}
		if overlay.Security.ShmSize != "" {
			meta.Security.ShmSize = overlay.Security.ShmSize
		}
		if len(overlay.Security.GroupAdd) > 0 {
			meta.Security.GroupAdd = overlay.Security.GroupAdd
		}
		if len(overlay.Security.Mounts) > 0 {
			meta.Security.Mounts = overlay.Security.Mounts
		}
		if overlay.Security.MemoryMax != "" {
			meta.Security.MemoryMax = overlay.Security.MemoryMax
		}
		if overlay.Security.MemoryHigh != "" {
			meta.Security.MemoryHigh = overlay.Security.MemoryHigh
		}
		if overlay.Security.MemorySwapMax != "" {
			meta.Security.MemorySwapMax = overlay.Security.MemorySwapMax
		}
		if overlay.Security.Cpus != "" {
			meta.Security.Cpus = overlay.Security.Cpus
		}
	}
	if overlay.Network != "" {
		meta.Network = overlay.Network
	}
	if overlay.Engine != "" {
		meta.Engine = overlay.Engine
	}
	// Merge deploy.yml secrets onto image label secrets
	if overlay.Secrets != nil {
		deployByName := make(map[string]DeploySecretConfig, len(overlay.Secrets))
		for _, ds := range overlay.Secrets {
			deployByName[ds.Name] = ds
		}
		// Override matching secrets from image labels with deploy.yml source config
		for i, ls := range meta.Secrets {
			if _, ok := deployByName[ls.Name]; ok {
				// Deploy.yml provides this secret — keep the label entry
				// (the source override is used at provisioning time, not in the label)
				_ = i
			}
		}
		// Add deploy-only secrets that aren't in the image labels
		for _, ds := range overlay.Secrets {
			found := false
			for _, ls := range meta.Secrets {
				if ls.Name == ds.Name {
					found = true
					break
				}
			}
			if !found {
				meta.Secrets = append(meta.Secrets, LabelSecret{
					Name:   ds.Name,
					Target: "/run/secrets/" + ds.Name,
				})
			}
		}
	}
}

// ResolveVolumeBacking splits image volumes into named volumes and bind mounts
// based on deploy.yml volume configuration.
// Volumes without a deploy override remain as named volumes.
// Volumes with type=bind or type=encrypted become ResolvedBindMount.
// Deploy-only volumes (with Path set, not in labels) are also supported.
func ResolveVolumeBacking(imageName string, labelVolumes []VolumeMount, deployVolumes []DeployVolumeConfig, home string, encStoragePath string, volumesPath string) ([]VolumeMount, []ResolvedBindMount) {
	// Index deploy volume configs by name
	deployByName := make(map[string]DeployVolumeConfig, len(deployVolumes))
	for _, dv := range deployVolumes {
		deployByName[dv.Name] = dv
	}

	// Track which deploy entries matched a label volume
	matched := make(map[string]bool)

	var volumes []VolumeMount
	var bindMounts []ResolvedBindMount

	for _, vol := range labelVolumes {
		// Extract short name from "ov-<image>-<name>"
		shortName := strings.TrimPrefix(vol.VolumeName, "ov-"+imageName+"-")

		dv, hasOverride := deployByName[shortName]
		if hasOverride {
			matched[shortName] = true
		}

		if hasOverride && (dv.Type == "bind" || dv.Type == "encrypted") {
			var hostPath string
			if dv.Type == "encrypted" {
				if dv.Host != "" {
					// Explicit per-volume path: /path/{cipher,plain}
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					// Global default: <encStoragePath>/ov-<image>-<name>/{cipher,plain}
					hostPath = encryptedPlainDir(encStoragePath, imageName, shortName)
				}
			} else if dv.Host != "" {
				hostPath = expandHostHome(dv.Host)
			} else {
				// Auto path: <volumesPath>/<image>/<name>
				hostPath = filepath.Join(volumesPath, imageName, shortName)
			}
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      shortName,
				HostPath:  hostPath,
				ContPath:  vol.ContainerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			// Default: keep as named volume
			volumes = append(volumes, vol)
		}
	}

	// Add deploy-only volumes (not in any layer, must have Path)
	for _, dv := range deployVolumes {
		if matched[dv.Name] || dv.Path == "" {
			continue
		}
		containerPath := ExpandPath(dv.Path, home)
		if dv.Type == "bind" || dv.Type == "encrypted" {
			var hostPath string
			if dv.Type == "encrypted" {
				if dv.Host != "" {
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					hostPath = encryptedPlainDir(encStoragePath, imageName, dv.Name)
				}
			} else if dv.Host != "" {
				hostPath = expandHostHome(dv.Host)
			} else {
				hostPath = filepath.Join(volumesPath, imageName, dv.Name)
			}
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      dv.Name,
				HostPath:  hostPath,
				ContPath:  containerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			volumes = append(volumes, VolumeMount{
				VolumeName:    "ov-" + imageName + "-" + dv.Name,
				ContainerPath: containerPath,
			})
		}
	}

	return volumes, bindMounts
}

// LoadDeployFile reads a deploy.yml from an arbitrary path.
func LoadDeployFile(path string) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var dc DeployConfig
	if err := yaml.Unmarshal(data, &dc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &dc, nil
}

// SaveDeployConfig writes a DeployConfig to the standard deploy.yml path.
func SaveDeployConfig(dc *DeployConfig) error {
	path, err := DeployConfigPath()
	if err != nil {
		return fmt.Errorf("determining deploy config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := yaml.Marshal(dc)
	if err != nil {
		return fmt.Errorf("marshaling deploy config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// MergeDeployConfigs merges multiple DeployConfigs left-to-right.
// Later configs take precedence (field-level replace per image).
func MergeDeployConfigs(configs ...*DeployConfig) *DeployConfig {
	result := &DeployConfig{Images: make(map[string]DeploymentNode)}
	for _, dc := range configs {
		if dc == nil || dc.Images == nil {
			continue
		}
		for name, overlay := range dc.Images {
			existing := result.Images[name]
			if overlay.Tunnel != nil {
				existing.Tunnel = overlay.Tunnel
			}
			if overlay.DNS != "" {
				existing.DNS = overlay.DNS
			}
			if overlay.AcmeEmail != "" {
				existing.AcmeEmail = overlay.AcmeEmail
			}
			if overlay.Volumes != nil {
				existing.Volumes = overlay.Volumes
			}
			if overlay.Ports != nil {
				existing.Ports = overlay.Ports
			}
			if overlay.Env != nil {
				existing.Env = overlay.Env
			}
			if overlay.EnvFile != "" {
				existing.EnvFile = overlay.EnvFile
			}
			if overlay.Security != nil {
				existing.Security = overlay.Security
			}
			if overlay.Network != "" {
				existing.Network = overlay.Network
			}
			if overlay.Engine != "" {
				existing.Engine = overlay.Engine
			}
			if overlay.Version != "" {
				existing.Version = overlay.Version
			}
			// Declarative fields authored in the project deploy.yml:
			// target, vm_source, add_layers, tests, install_opts. The
			// local deploy.yml overlays via field-level replace — so a
			// per-machine add_layers list fully replaces the project list,
			// and per-machine tests replace project tests. For tests the
			// caller (ov test) can run MergeDeployTests to merge by id.
			if overlay.Target != "" {
				existing.Target = overlay.Target
			}
			if overlay.VmSource != "" {
				existing.VmSource = overlay.VmSource
			}
			// Schema-v3 cross-ref fields. Each is a string cross-reference
			// (to the top-level vm:/cluster: or another deployment) and
			// replaces the existing value when set.
			if overlay.Image != "" {
				existing.Image = overlay.Image
			}
			if overlay.Cluster != "" {
				existing.Cluster = overlay.Cluster
			}
			if overlay.Inside != "" {
				existing.Inside = overlay.Inside
			}
			// Disposable / Lifecycle (R10-load-bearing). The overlay is
			// authoritative when set; the baseline value from the project
			// config only applies when the overlay doesn't mention it.
			// Without this merge, per-machine overlays would silently
			// lose the disposable flag — a security-relevant bug.
			if overlay.Disposable {
				existing.Disposable = true
			}
			if overlay.Lifecycle != "" {
				existing.Lifecycle = overlay.Lifecycle
			}
			if overlay.AddLayers != nil {
				existing.AddLayers = overlay.AddLayers
			}
			if overlay.Tests != nil {
				existing.Tests = overlay.Tests
			}
			if overlay.InstallOpts != nil {
				existing.InstallOpts = overlay.InstallOpts
			}
			if overlay.Children != nil {
				existing.Children = overlay.Children
			}
			// VmState is per-machine state written by VmDeployTarget; it
			// only ever lives in the local deploy.yml, never in the
			// project file — so this simple "later wins" propagation is
			// the correct behavior.
			if overlay.VmState != nil {
				existing.VmState = overlay.VmState
			}
			result.Images[name] = existing
		}
	}
	return result
}

// RemoveImageDeploy removes an image's entry from a deploy config.
func RemoveImageDeploy(dc *DeployConfig, imageName string) {
	if dc != nil && dc.Images != nil {
		delete(dc.Images, imageName)
	}
}

// cleanDeployEntry removes an image's entry from deploy.yml (best-effort).
// Also removes global service env vars that were injected by this image.
// If deploy.yml becomes empty after removal, the file is deleted.
func cleanDeployEntry(imageName, instance string) {
	dc, err := LoadDeployConfig()
	if err != nil || dc == nil {
		return
	}

	key := deployKey(imageName, instance)
	hasImage := false
	if _, ok := dc.Images[key]; ok {
		hasImage = true
		RemoveImageDeploy(dc, key)
	}

	// Remove provides entries injected by this image/instance.
	// For instances: always clean entries sourced from the specific instance (exact match).
	// For base images: only clean ALL provides if no other instances remain deployed.
	removedProvides := false
	if dc.Provides != nil {
		if instance != "" {
			// Instance removal: remove only this instance's provides (exact source match)
			if len(dc.Provides.Env) > 0 {
				cleaned, removed := removeByExactSource(dc.Provides.Env, key)
				if removed {
					dc.Provides.Env = cleaned
					removedProvides = true
					fmt.Fprintf(os.Stderr, "Removed env provides from %s\n", key)
				}
			}
			if len(dc.Provides.MCP) > 0 {
				cleaned, removed := removeByExactSource(dc.Provides.MCP, key)
				if removed {
					dc.Provides.MCP = cleaned
					removedProvides = true
					fmt.Fprintf(os.Stderr, "Removed MCP provides from %s\n", key)
				}
			}
		} else {
			// Base image removal: only remove if no other entries for the same base image remain
			hasOtherEntries := false
			for k := range dc.Images {
				base, _ := parseDeployKey(k)
				if base == imageName {
					hasOtherEntries = true
					break
				}
			}
			if !hasOtherEntries {
				if len(dc.Provides.Env) > 0 {
					cleaned, removed := removeBySource(dc.Provides.Env, imageName)
					if removed {
						dc.Provides.Env = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed env provides from %s\n", imageName)
					}
				}
				if len(dc.Provides.MCP) > 0 {
					cleaned, removed := removeBySource(dc.Provides.MCP, imageName)
					if removed {
						dc.Provides.MCP = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed MCP provides from %s\n", imageName)
					}
				}
			}
		}
		if len(dc.Provides.MCP) == 0 && len(dc.Provides.Env) == 0 {
			dc.Provides = nil
		}
	}

	if !hasImage && !removedProvides {
		return
	}

	if len(dc.Images) == 0 && dc.Provides == nil {
		if path, pathErr := DeployConfigPath(); pathErr == nil {
			os.Remove(path)
		}
	} else if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not clean deploy.yml: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaned deploy.yml entry for %s\n", key)
}

// appendOrReplaceEnv adds or replaces an env var entry (KEY=VALUE) in a slice.
// If the key already exists, the value is replaced in-place.
func appendOrReplaceEnv(envs []string, entry string) []string {
	key := envKey(entry)
	for i, e := range envs {
		if envKey(e) == key {
			envs[i] = entry
			return envs
		}
	}
	return append(envs, entry)
}

// envKey extracts the KEY part from a KEY=VALUE string.
func envKey(entry string) string {
	if idx := strings.IndexByte(entry, '='); idx >= 0 {
		return entry[:idx]
	}
	return entry
}


// SaveDeployStateInput holds the deployment parameters to persist.
type SaveDeployStateInput struct {
	Ports    []string
	Env      []string
	CleanEnv bool // true = replace env list; false = merge (upsert by key)
	EnvFile  string
	Network  string
	Security *SecurityConfig
	Volumes  []DeployVolumeConfig
	Sidecars map[string]SidecarDef
	Tunnel   *TunnelYAML

	// SecretNames lists env var names declared as secret_accepts /
	// secret_requires on the image. saveDeployState uses this list to
	// defensively strip any matching KEY=VAL entries from both the input
	// Env and the existing persisted entry.Env before writing. Defense in
	// depth for the §6 / Run() pipeline (MigratePlaintextEnvSecrets and
	// scrubSecretCLIEnv are the primary gates). Populated by the Run()
	// call site from meta.SecretAccepts/SecretRequires.
	SecretNames []string

	// Disposable + Lifecycle — the classification fields
	// (see /ov-dev:disposable). SetDisposable toggles whether the
	// Disposable field is written at all: when false, saveDeployState
	// leaves any pre-existing value untouched. Same idiom for lifecycle.
	SetDisposable bool
	Disposable    bool
	SetLifecycle  bool
	Lifecycle     string
}

// saveDeployState persists deployment parameters to deploy.yml (best-effort).
// Merges onto any existing entry to preserve fields from ov deploy import.
//
// Defense-in-depth: any env entry whose key matches a name in input.SecretNames
// is stripped from both input.Env and the existing persisted entry.Env before
// writing. The primary gates against plaintext-credential leakage are
// MigratePlaintextEnvSecrets and scrubSecretCLIEnv in config_image.go:Run();
// this scrub catches anything that slipped through (e.g., a future refactor
// that adds a new code path writing into dc.Env). Matches plan §6.7.
func saveDeployState(imageName, instance string, input SaveDeployStateInput) {
	dc, _ := LoadDeployConfig()
	if dc == nil {
		dc = &DeployConfig{Images: make(map[string]DeploymentNode)}
	}
	key := deployKey(imageName, instance)
	entry := dc.Images[key] // preserve existing fields (tunnel, volumes, etc.)
	if input.Volumes != nil {
		entry.Volumes = input.Volumes
	}
	if input.Ports != nil {
		entry.Ports = input.Ports
	}
	// Defensive scrub: drop credential-backed env vars from both input and
	// existing entry before they land in the persisted file.
	if len(input.SecretNames) > 0 {
		input.Env = stripSecretEnvNames(input.Env, input.SecretNames)
		entry.Env = stripSecretEnvNames(entry.Env, input.SecretNames)
	}
	if len(input.Env) > 0 {
		if input.CleanEnv || len(entry.Env) == 0 {
			entry.Env = input.Env
		} else {
			entry.Env = mergeEnvVars(entry.Env, input.Env)
		}
	}
	if input.EnvFile != "" {
		entry.EnvFile = input.EnvFile
	}
	if input.Network != "" {
		entry.Network = input.Network
	}
	if input.Security != nil {
		entry.Security = input.Security
	}
	if len(input.Sidecars) > 0 {
		entry.Sidecars = input.Sidecars
	}
	if input.Tunnel != nil {
		entry.Tunnel = input.Tunnel
	}
	// Classification fields: only written when the caller explicitly
	// opts in via SetDisposable / SetLifecycle. This lets repeated
	// saveDeployState calls from unrelated code paths (ov start, ov
	// config) leave a user-authored `disposable: true` in place.
	if input.SetDisposable {
		entry.Disposable = input.Disposable
	}
	if input.SetLifecycle {
		entry.Lifecycle = input.Lifecycle
	}
	dc.Images[key] = entry
	if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to deploy.yml: %v\n", err)
	}
}

// stripSecretEnvNames removes any KEY=VAL entries from env whose KEY is in
// the blocked list. The blocked list is expected to be short (one entry per
// secret_* declaration on the image), so a linear contains check per entry
// is fine. Preserves the order of surviving entries.
func stripSecretEnvNames(env []string, blocked []string) []string {
	if len(env) == 0 || len(blocked) == 0 {
		return env
	}
	blockedSet := make(map[string]bool, len(blocked))
	for _, name := range blocked {
		blockedSet[name] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			key = kv[:idx]
		}
		if blockedSet[key] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// mergeEnvVars merges new env vars into existing ones (upsert by key).
// New vars override existing vars with the same key; existing vars with
// unmatched keys are preserved in their original order.
func mergeEnvVars(existing, newVars []string) []string {
	newByKey := make(map[string]string, len(newVars))
	for _, kv := range newVars {
		key := strings.SplitN(kv, "=", 2)[0]
		newByKey[key] = kv
	}
	result := make([]string, 0, len(existing)+len(newVars))
	seen := make(map[string]bool)
	for _, kv := range existing {
		key := strings.SplitN(kv, "=", 2)[0]
		if newKV, ok := newByKey[key]; ok {
			result = append(result, newKV)
			seen[key] = true
		} else {
			result = append(result, kv)
		}
	}
	for _, kv := range newVars {
		key := strings.SplitN(kv, "=", 2)[0]
		if !seen[key] {
			result = append(result, kv)
		}
	}
	return result
}

// ExportAllImages exports all runtime-relevant fields for all enabled images in a Config.
func ExportAllImages(cfg *Config) *DeployConfig {
	dc := &DeployConfig{Images: make(map[string]DeploymentNode)}
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		entry := DeploymentNode{
			Version:   img.Version,
			Status:    img.Status,
			Info:      img.Info,
			Ports:     img.Ports,
			Tunnel:    img.Tunnel,
			DNS:       img.DNS,
			AcmeEmail: img.AcmeEmail,
			Env:       img.Env,
			EnvFile:   img.EnvFile,
			Security:  img.Security,
			Network:   img.Network,
			Engine:    img.Engine,
		}
		// Only include if at least one field is set
		if entry.Version != "" || entry.Status != "" || entry.Info != "" ||
			entry.Ports != nil || entry.Tunnel != nil || entry.DNS != "" ||
			entry.AcmeEmail != "" || entry.Env != nil ||
			entry.EnvFile != "" || entry.Security != nil || entry.Network != "" ||
			entry.Engine != "" {
			dc.Images[name] = entry
		}
	}
	return dc
}

