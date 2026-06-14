package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DeployConfig represents per-machine deployment overrides (~/.config/charly/charly.yml).
// Only runtime/deployment fields are supported — build-time fields are structurally excluded.
//
// Schema v4: the top-level map key is `deployment:` (singular, flat). The
// legacy `images:` / `deployments.images.*` nesting is gone — all target
// kinds (host / vm / pod / k8s) live under the single `deployment:` map.
type DeployConfig struct {
	Provides *ProvidesConfig           `yaml:"provides,omitempty"`
	Deploy   map[string]DeploymentNode `yaml:"deploy"`
	// Sidecar carries the project's sidecar-template library (the embedded
	// default set merged with any project-declared root sidecar: entries).
	// Projected from UnifiedFile.Sidecar by ProjectDeployConfig(); deploy-time
	// resolution merges these UNDER each deploy node's own sidecar overrides.
	Sidecar map[string]SidecarDef `yaml:"sidecar,omitempty"`
}

// DeploymentNode is one node in the deployments tree declared in
// charly.yml. Every deployment is a node; each node may carry zero or
// more `children:` that run inside its environment. The node's Target
// discriminator picks the DeployTarget that owns execution:
//
//   - "host"        — local filesystem (LocalDeployTarget + ShellExecutor).
//   - "vm"          — a libvirt/QEMU VM referenced by VmSource (VmDeployTarget
//     over SSHDeployExecutor).
//   - "container"   — a podman/docker container (PodDeployTarget;
//     the default when Target is empty).
//   - "kubernetes"  — a Kustomize manifest tree (K8sDeployTarget; leaf-only,
//     not nestable).
//
// Nested topologies compose at the executor layer: a child's DeployExecutor
// wraps its parent's executor with a "shell jump" (podman exec, virsh
// console, or an additional ssh hop). That means "container-in-vm" doesn't
// need a new target implementation — it's PodDeployTarget whose
// executor happens to be NestedExecutor{Parent: SSHDeployExecutor{…}}.
//
// Addressing is dotted path: `charly deploy add stack.web.db` walks the tree
// stack → web → db and applies that leaf plus all of its descendants in
// pre-order. Map keys MUST NOT contain `.` — the load-time validator in
// unified.go rejects them with a remediation hint.
//
// Disposability is per-node and does NOT cascade. A parent with
// disposable: true does not authorize destroying its children unattended —
// each child's flag stands on its own (see /charly-internals:disposable).
type DeploymentNode struct {
	Version     string               `yaml:"version,omitempty"`
	Description string               `yaml:"description,omitempty"` // plain-string self-description; first line = summary
	Tunnel      *TunnelYAML          `yaml:"tunnel,omitempty"`
	DNS         string               `yaml:"dns,omitempty"`
	AcmeEmail   string               `yaml:"acme_email,omitempty"`
	Volume      []DeployVolumeConfig `yaml:"volume,omitempty"`
	Port        []string             `yaml:"port,omitempty"`
	// ResolvedPort is the concrete host:container expansion of an "auto"
	// sentinel in Port. Persisted by charly config / charly update — read by
	// MergeDeployOntoMetadata in preference to Port when present, so
	// charly start / charly logs / charly status see the same allocations between
	// rebuilds. Re-allocation happens on the next charly config / charly update
	// where Port still contains "auto" (operator-acknowledged churn).
	ResolvedPort    []string              `yaml:"resolved_port,omitempty"`
	Env             []string              `yaml:"env,omitempty"`
	EnvFile         string                `yaml:"env_file,omitempty"`
	Security        *SecurityConfig       `yaml:"security,omitempty"`
	Network         string                `yaml:"network,omitempty"`
	Engine          string                `yaml:"engine,omitempty"`
	Secret          []DeploySecretConfig  `yaml:"secret,omitempty"`
	ForwardGpgAgent *bool                 `yaml:"forward_gpg_agent,omitempty"`
	ForwardSshAgent *bool                 `yaml:"forward_ssh_agent,omitempty"`
	Sidecar         map[string]SidecarDef `yaml:"sidecar,omitempty"` // Sidecar container overrides

	// Plan carries local deploy-level acceptance + provisioning overlays
	// (Steps). They merge onto the image's label-baked plan at runtime by
	// step id (MergeDeployDescriptions); a step with skip:true disables the
	// baked step.
	Plan []Step `yaml:"plan,omitempty"`

	// Iterate carries the AI-loop orchestration (the former kind:score). When
	// set on a deploy / kind:check bed, `charly check run <name>` drives the
	// iterate loop scoring this entity's plan check:/agent-check: steps;
	// absent → the deterministic R10 bed sequence. Never baked into the image
	// (deploy/runtime state).
	Iterate *IterateConfig `yaml:"iterate,omitempty"`

	// CheckBed marks this entry as a `kind: check` disposable R10 bed,
	// folded into the Deploy map by foldCheckBeds() at load time so every
	// deploy verb resolves it by name with no per-verb change. Never
	// authored as a field — the `kind: check` discriminator is what sets
	// it. Read by `charly check run` to enumerate beds and by validate.go for
	// the bed-specific rules (disposable required, cross-ref resolvable).
	CheckBed bool `yaml:"-"`

	// Shell is the deploy-level overlay for the ai.opencharly.shell
	// label. Same id-based replace/skip/append semantics as Check —
	// applied via MergeDeployShell at deploy time. 2026-05 cutover.
	// Each entry carries optional `id:` (matches a baked candy/box
	// origin or "<origin>:<shell>") and either a generic body /
	// per-shell sub-blocks (replaces the baked entry) or `skip: true`
	// (drops the baked entry). Entries without a matching id append
	// to the deploy bucket with origin="deploy" if not set.
	Shell []DeployShellOverlay `yaml:"shell,omitempty"`

	// --- BuildTarget refactor fields (Task 13) ---
	//
	// Target selects the deploy destination. Empty or "container" →
	// the existing quadlet/podman pipeline. "host" → apply candies
	// directly to the invoking user's filesystem via LocalDeployTarget.
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

	// Replica — number of instances. Ignored for single-instance workloads
	// (daemon/batch/oneshot) or non-K8s targets that don't support scaling.
	Replica int `yaml:"replica,omitempty"`

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
	// the existing Volume list which covers container-target volume backing.
	Storage []DeployStorage `yaml:"storage,omitempty"`

	// Probes — target-agnostic liveness/readiness/startup specs.
	Probes *DeployProbes `yaml:"probes,omitempty"`

	// AddCandies are overlay candy refs applied on top of the image.
	// Each entry is a DeployRef (local name / local YAML path /
	// remote github ref). Same syntax as the command-line --add-candy
	// flag.
	AddCandy []string `yaml:"add_candy,omitempty"`

	// InstallOpts carries host-target-specific flags that would
	// otherwise have to be passed on every command invocation.
	InstallOpts *InstallOptsConfig `yaml:"install_opts,omitempty"`

	// --- Schema-v4 template references (exactly one matching Target) ---

	// Box names a kind:box directly. Used for target: pod when no
	// kind:pod template is needed (the common case), or as a legacy
	// fallback for other targets during migration.
	Box string `yaml:"box,omitempty"`

	// Pod names a kind:pod template (image ref + sidecars + optional
	// tests + shared env defaults). Only meaningful for target: pod.
	Pod string `yaml:"pod,omitempty"`

	// Vm names a kind:vm entity. Only meaningful for target: vm.
	// Schema v4 rename of the legacy `vm_source:` field.
	Vm string `yaml:"vm,omitempty"`

	// K8s names a kind:k8s template (workload defaults + cluster config;
	// absorbs former ClusterProfile fields). Only meaningful for
	// target: k8s. Replaces the legacy `cluster:` field.
	K8s string `yaml:"k8s,omitempty"`

	// Local names a kind:local template (candy stack + install_opts + env).
	// Optional — a target:local deployment MAY inline add_candy: directly
	// without a template.
	Local string `yaml:"local,omitempty"`

	// Android names a kind:android device (an in-pod emulator or a
	// remote/physical adb endpoint). Only meaningful for target: android —
	// the deploy installs its `add_candy:` candies' `apk:` packages onto the
	// device via AndroidDeployTarget. The apps ride in on add_candy: (the
	// same overlay mechanism every other target uses), so there is no
	// dedicated apk-list field here.
	Android string `yaml:"android,omitempty"`

	// Host is the destination machine for target:local deployments
	// (Ansible-style). The literal string "local" (or empty/absent) means
	// direct local execution via ShellExecutor. Anything else is treated
	// as an SSH target in the form "[user@]host[:port]" or an ssh-config
	// alias and runs through ssh(1), which reads ~/.ssh/config and
	// ssh-agent for keys, host-key checking, and any other connection
	// parameters. There is no `ssh:` block — your ssh-config IS the
	// configuration.
	Host string `yaml:"host,omitempty"`

	// User overrides the SSH user for this deployment (Ansible's
	// ansible_user). Only consulted when Host is non-"local" and Host
	// does NOT already contain "@". When Host carries an inline user
	// ("alice@server"), that wins and User is redundant — the validator
	// warns. When neither is set, ssh(1) reads the User directive from
	// ~/.ssh/config or falls back to $USER.
	User string `yaml:"user,omitempty"`

	// SSHArgs are extra arguments appended to every ssh/scp invocation
	// for this deployment (Ansible's ansible_ssh_extra_args). Pass-through:
	// we do NOT parse, validate, or interpret these args. Use sparingly —
	// ssh-config Host stanzas are the right home for persistent options.
	// Common cases: "-o ProxyJump=bastion", "-o ServerAliveInterval=30".
	SSHArgs []string `yaml:"ssh_arg,omitempty"`

	// --- Scalar overrides of target-template defaults ---

	// Cpus overrides kind:vm.cpus for this deployment instance.
	Cpus int `yaml:"cpus,omitempty"`

	// Ram overrides kind:vm.ram for this deployment instance
	// (format: "16G", "32Gi", etc.).
	Ram string `yaml:"ram,omitempty"`

	// DiskSize overrides kind:vm.disk_size for this deployment instance
	// (format: "40G", "80GiB", etc.).
	DiskSize string `yaml:"disk_size,omitempty"`

	// --- Derived nesting fields (authored via `nested:`, not these) ---

	// Inside is DERIVED from the parent's Nested tree at load time.
	// v4 REJECTS authored `inside:` entries with a migration-hint error;
	// author the parent's `nested:` map instead. Kept here because the
	// derived value is consulted by NestedExecutor routing.
	Inside string `yaml:"-"`

	// VmState is the runtime state written by VmDeployTarget on first
	// apply. Preserved across reboots so charly deploy del can reverse the
	// deploy, and so re-apply is idempotent (instance-id stays stable,
	// disk path points at the same qcow2, etc.).
	VmState *VmDeployState `yaml:"vm_state,omitempty"`

	// --- Disposable / lifecycle / ephemeral classification (see /charly-internals:disposable) ---

	// Disposable, when true, authorizes `charly update <name>` to
	// destroy + rebuild + restart this deploy unattended. Default
	// is false (conservative; explicit opt-in). There is NO
	// derivation from Lifecycle. See CLAUDE.md R10.
	//
	// Load-bearing implication: when Ephemeral is non-nil, Disposable
	// is auto-promoted to true at load time (see classification.go).
	// Authoring `disposable: false` together with `ephemeral: ...` is
	// rejected as a contradiction.
	//
	// Pointer-bool so `disposable: false` survives the YAML round-trip:
	// a plain `bool` with `omitempty` is indistinguishable from "absent"
	// at marshal time (Go YAML drops false), which silently erases an
	// operator's explicit lockdown intent on the next saveDeployState
	// call. nil = absent (default false behavior); &false = explicit
	// lockdown (preserved on write); &true = explicit authorization.
	Disposable *bool `yaml:"disposable,omitempty"`

	// Lifecycle is a free-form human-facing tier tag (scratch | dev |
	// test | qa | staging | prod | custom). Informational only — has
	// ZERO effect on disposability. Consumed by `charly status
	// --lifecycle <tier>` filters and display columns.
	Lifecycle string `yaml:"lifecycle,omitempty"`

	// Ephemeral is the operational-mandate counterpart to Disposable's
	// authorization: presence indicates the deploy MUST be destroyed as
	// soon as it isn't needed anymore (auto-cleanup at run end,
	// SIGTERM, or TTL expiry). The block-form unmarshal accepts both
	// `ephemeral: true` (boolean shorthand → defaults) and
	// `ephemeral: { ttl: ..., keep_on_failure: ..., naming_pattern:
	// ... }` (full block). nil means non-ephemeral.
	Ephemeral *EphemeralLifetime `yaml:"ephemeral,omitempty"`

	// Preemptible is the HOLDER side of the resource-arbitration axis: a
	// fourth, ORTHOGONAL classification alongside disposable / ephemeral /
	// lifecycle (see /charly-internals:disposable). Presence means "this deploy
	// occupies the named exclusive host-resource token(s) in `holds:`, and
	// MAY be gracefully stopped to free them for a claimant that needs them
	// — but MUST be restarted afterward (disk + definition preserved)". It
	// is the inverse of disposable: disposable says *you may wipe me*,
	// preemptible says *you may pause me, but bring me back*.
	//
	// NO derivation either way — preemptible neither implies nor is implied
	// by disposable/ephemeral/lifecycle. A deploy may legitimately be BOTH
	// preemptible (the arbiter stops it) and disposable (R10 may rebuild
	// it). nil means not preemptible. Authored as a token list shorthand
	// (`preemptible: [nvidia-gpu]`) or a block (`preemptible: {holds: [...],
	// stop: shutdown, restore: always}`).
	Preemptible *PreemptibleConfig `yaml:"preemptible,omitempty"`

	// RequiresExclusive is the CLAIMANT side: the named exclusive
	// host-resource token(s) this deploy needs sole use of while it runs.
	// Before bring-up, the resource arbiter (charly/preempt.go) finds every
	// RUNNING preemptible holder whose `holds:` intersects these tokens,
	// gracefully stops them, records a crash-safe restore obligation, and
	// restores them when this claim is released. A token with no declared
	// holder is valid (the resource is simply free). The token is an
	// operator-chosen NAME for a physical resource (e.g. "nvidia-gpu"),
	// deliberately decoupled from the access mechanism (PCI hostdev for a
	// VM, --device/CDI for a pod) so it unifies pod-vs-VM contention. nil/
	// empty means this deploy claims nothing exclusive.
	RequiresExclusive []string `yaml:"requires_exclusive,omitempty"`

	// FromSnapshot, on a target=vm deploy, names the snapshot on the
	// referenced kind:vm to use as the cloned overlay's backing disk.
	// Empty means "boot the template VM directly" (legacy behavior).
	// When set, the deploy's vm-target Add path uses qemu-img backing-
	// chain to materialize a fresh per-deploy disk from the snapshot.
	// Required for ephemeral deploys against a VM that has snapshots;
	// optional for persistent deploys (rare but supported).
	FromSnapshot string `yaml:"from_snapshot,omitempty"`

	// CloudInitClean, on a target=vm deploy, injects a `runcmd:
	// cloud-init clean --machine-id --logs` entry into the clone's
	// user-data so machine-id + ssh host keys regenerate inside the
	// guest on first boot. Default false. Mirrors the
	// VmSource.CloudInitClean field for clone-source templates;
	// applies only to deploy-level cloning via FromSnapshot.
	CloudInitClean bool `yaml:"cloud_init_clean,omitempty"`

	// --- Recursive tree: nested deployments (schema v4) ---
	//
	// Nested are DeploymentNodes whose execution venue is nested
	// inside this node's environment. A container node with a vm child
	// creates the container first, then boots the VM inside it; the
	// child's DeployExecutor composes this node's executor with a
	// shell jump (podman exec / ssh / virsh) so commands execute
	// inside the nested environment.
	//
	// Map keys MUST NOT contain `.` — dotted-path CLI addressing
	// treats `.` as a node separator (foo.bar.baz). LoadUnified
	// rejects offending keys at parse time with a remediation hint.
	//
	// Schema v4 rename: `children:` → `nested:`. The parent-reverse-
	// reference Inside is DERIVED from this tree at load time; authored
	// `inside:` entries are rejected.
	Nested map[string]*DeploymentNode `yaml:"nested,omitempty"`

	// --- Sibling peers: companion deployments brought up alongside ---
	//
	// Peer are full DeploymentNodes brought up ALONGSIDE this node on the
	// shared `charly` network — NOT nested inside it (contrast Nested, whose
	// children run INSIDE this node's venue and are addressed by a dotted
	// path). A peer is a companion INSTRUMENT — the canonical case is a
	// Chrome driver pod that CDP-probes this web-server subject via a check
	// with `on: <peer>`; peers are reachable by `${PEER_HOST:<name>}` over
	// the shared net and are NEVER check-live'd themselves (excluded from
	// bedCheckLiveRefs). foldPeers registers each peer as a top-level,
	// addressable Deploy entry at load time (inheriting this node's
	// disposability) so the SAME `charly config`/`charly start`/`charly stop` verbs the
	// deploy path already uses bring it up and tear it down — and a kind:check
	// bed inherits that lifecycle through its own `charly start <bed>` step.
	// Used identically by kind:check beds and kind:deploy deployments (one
	// codebase). Peer keys MUST be globally unique + carry no `.` (same rule
	// as Nested + bed names); the AUTHOR keeps peer host ports disjoint.
	Peer map[string]*DeploymentNode `yaml:"peer,omitempty"`

	// PeerOf, when set, names the deployment whose `peer:` block defined this
	// (folded) entry. DERIVED by foldPeers — never authored. Marks the entry
	// as a companion so it is brought up/torn down with its owner and is not
	// enumerated as an independent bed.
	PeerOf string `yaml:"-"`
}

// DeployShellOverlay is one entry in charly.yml `shell:`. ID matches
// a baked LabelShell entry's ID for replace/skip; absent ID makes the
// entry a fresh deploy-scope contribution. Skip:true drops the matched
// baked entry. The body fields (Init, PathAppend, Path, Priority,
// ByShell) mirror ShellSpec — when populated, they replace the baked
// entry's body wholesale.
type DeployShellOverlay struct {
	ID         string                `yaml:"id,omitempty"`
	Origin     string                `yaml:"origin,omitempty"`
	Skip       bool                  `yaml:"skip,omitempty"`
	Init       string                `yaml:"init,omitempty"`
	PathAppend []string              `yaml:"path_append,omitempty"`
	Path       string                `yaml:"path,omitempty"`
	Priority   int                   `yaml:"priority,omitempty"`
	ByShell    map[string]*ShellSpec `yaml:"-"` // populated by UnmarshalYAML for bash/zsh/fish/sh keys
}

// UnmarshalYAML two-pass parses a charly.yml shell-overlay entry,
// recognising both the intrinsic fields and the per-shell allowlist
// keys (bash/zsh/fish/sh) — same pattern as ShellConfig.UnmarshalYAML.
// Unknown non-allowlist keys raise a hard error so authors don't
// silently typo a shell name.
func (o *DeployShellOverlay) UnmarshalYAML(value *yaml.Node) error {
	type overlayAlias DeployShellOverlay
	var alias overlayAlias
	if err := value.Decode(&alias); err != nil {
		return err
	}
	*o = DeployShellOverlay(alias)
	if value.Kind != yaml.MappingNode {
		return nil
	}
	known := map[string]bool{
		"id": true, "origin": true, "skip": true,
		"init": true, "path_append": true, "path": true, "priority": true,
	}
	for i := 0; i < len(value.Content)-1; i += 2 {
		key := value.Content[i].Value
		if known[key] {
			continue
		}
		if !ShellAllowlist[key] {
			return fmt.Errorf("deploy.shell: unknown key %q (expected id/origin/skip/init/path_append/path/priority or bash/zsh/fish/sh)", key)
		}
		var spec ShellSpec
		if err := value.Content[i+1].Decode(&spec); err != nil {
			return fmt.Errorf("deploy.shell.%s: %w", key, err)
		}
		if o.ByShell == nil {
			o.ByShell = make(map[string]*ShellSpec)
		}
		o.ByShell[key] = &spec
	}
	return nil
}

// ToShellEntry converts a charly.yml overlay into the LabelShell
// ShellEntry shape consumed by MergeDeployShell.
func (o *DeployShellOverlay) ToShellEntry() ShellEntry {
	entry := ShellEntry{
		Origin:   o.Origin,
		ID:       o.ID,
		Priority: o.Priority,
	}
	if !o.Skip {
		hasGeneric := o.Init != "" || len(o.PathAppend) > 0 || o.Path != ""
		if hasGeneric {
			entry.Generic = &ShellSpec{
				Init:       o.Init,
				PathAppend: append([]string(nil), o.PathAppend...),
				Path:       o.Path,
			}
		}
		if len(o.ByShell) > 0 {
			entry.ByShell = make(map[string]*ShellSpec, len(o.ByShell))
			for k, v := range o.ByShell {
				if v == nil {
					continue
				}
				entry.ByShell[k] = &ShellSpec{
					Init:       v.Init,
					PathAppend: append([]string(nil), v.PathAppend...),
					Path:       v.Path,
				}
			}
		}
	}
	// Skip == true → leave Generic/ByShell nil; MergeDeployShell's
	// replaceShellEntryByID treats both-nil as the "drop matched entry"
	// signal.
	return entry
}

// IsDisposable returns true when the node is explicitly disposable OR
// is marked ephemeral (the load-bearing implication: ephemeral deploys
// MUST be auto-destroyed and therefore MAY be — see /charly-internals:disposable
// "the ephemeral exception"). Implements the Classified interface.
func (c DeploymentNode) IsDisposable() bool {
	return (c.Disposable != nil && *c.Disposable) || c.IsEphemeral()
}

// IsEphemeral reports whether this deploy is marked ephemeral
// (`ephemeral:` field present in charly.yml). Equivalent to
// `c.Ephemeral != nil`. The presence of the field is the marker; the
// block's contents (TTL, keep_on_failure, naming_pattern) parameterize
// the lifecycle.
func (c DeploymentNode) IsEphemeral() bool {
	return c.Ephemeral != nil
}

// IsPreemptible reports whether this deploy is a HOLDER of an exclusive
// resource that may be gracefully stopped to free it (the `preemptible:`
// field present). Orthogonal to IsDisposable/IsEphemeral — no derivation
// either way. Implements the Classified interface.
func (c DeploymentNode) IsPreemptible() bool {
	return c.Preemptible != nil && len(c.Preemptible.Holds) > 0
}

// PreemptionHolds returns the exclusive-resource token(s) this deploy
// occupies as a holder (nil-safe; empty when not preemptible).
func (c DeploymentNode) PreemptionHolds() []string {
	if c.Preemptible == nil {
		return nil
	}
	return c.Preemptible.Holds
}

// RequiredExclusive returns the exclusive-resource token(s) this deploy
// claims sole use of (the claimant side; nil-safe).
func (c DeploymentNode) RequiredExclusive() []string {
	return c.RequiresExclusive
}

// HasChildren reports whether this node has any nested deployments.
// Cheap predicate used by the tree walker to decide pre-order vs
// leaf-only dispatch.
func (n *DeploymentNode) HasChildren() bool {
	return n != nil && len(n.Nested) > 0
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
	for _, k := range sortedNestedKeys(n.Nested) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := n.Nested[k].WalkPreOrder(childPath, fn); err != nil {
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
	for _, k := range sortedNestedKeys(n.Nested) {
		childPath := k
		if path != "" {
			childPath = path + "." + k
		}
		if err := n.Nested[k].WalkPostOrder(childPath, fn); err != nil {
			return err
		}
	}
	return fn(path, n)
}

// ResolveNodePath walks roots[path0].Nested[path1]...[pathN] and
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
		next, ok := current.Nested[parts[i]]
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
	if slices.Contains(out, "") {
		return nil
	}
	return out
}

// sortedNestedKeys returns the keys of a children map in deterministic
// order so traversal produces stable output across runs.
func sortedNestedKeys(children map[string]*DeploymentNode) []string {
	out := make([]string, 0, len(children))
	for k := range children {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// bedCheckLiveRefs returns the ordered `charly check live` targets for a bed: the
// substrate itself first, then each nested child as a sorted dotted path. This
// is the pure list `charly check run` walks so a nested pod's BAKED candy/box check
// (e.g. the selkies candy's encoder + frame checks on a nested selkies-kde pod)
// is exercised against its real venue through the chain — not just the parent
// substrate. Without the nested entries, `charly check run` deploys nested children
// but never evaluates them. Pure + unit-tested.
func bedCheckLiveRefs(name string, children map[string]*DeploymentNode) []string {
	refs := []string{name}
	for _, k := range sortedNestedKeys(children) {
		// A nested child gets its own `charly check live <parent>.<child>` hop ONLY
		// if it is an independently-resolvable venue (a pod/vm/local child with
		// its own image/host that the chain can reach). A `target: android`
		// child shares the parent pod's venue and has NO own image — its
		// app-presence checks are baked into the parent's android-emulator-layer
		// and already run in the parent ref. `charly check live` has no android
		// dotted-path branch, so a hop for it would wrongly resolve to a
		// non-existent `charly-<parent>.device` container. Skip android children.
		if c := children[k]; c != nil && c.Target == "android" {
			continue
		}
		refs = append(refs, name+"."+k)
	}
	return refs
}

// EphemeralLifetime parameterizes the auto-destruction lifecycle for a
// deploy. The presence of this struct (Ephemeral != nil) is the marker;
// fields default to sane values when the block-form is absent.
//
// YAML accepts both:
//
//	ephemeral: true             # boolean shorthand → defaults
//	ephemeral:                  # block form
//	  ttl: 30m
//	  keep_on_failure: false
//	  naming_pattern: "{{.Source}}-eph-{{.UUID6}}"
type EphemeralLifetime struct {
	// TTL is the maximum wall-clock lifetime of an instantiated
	// ephemeral. Parsed via time.ParseDuration ("30m", "2h", "90s").
	// When empty (boolean-shorthand authoring), defaults to "1h".
	// The value is the safety floor: a systemd transient timer fires
	// `charly deploy del <name> --assume-yes` after this duration if all
	// higher-layer cleanup paths fail.
	TTL string `yaml:"ttl,omitempty"`

	// KeepOnFailure, when true, instructs the check-runner integration
	// to skip the post-run `charly deploy del` when assertions fail
	// — leaves the instance alive (still subject to TTL) for operator
	// inspection. Default false.
	KeepOnFailure bool `yaml:"keep_on_failure,omitempty"`

	// NamingPattern is the template for ephemeral instance names.
	// Available variables: {{.Source}} (the deploy name), {{.UUID6}}
	// (six-char random hex), {{.Iter}} (check-iter counter when called
	// from runner). Default: "{{.Source}}-eph-{{.UUID6}}".
	NamingPattern string `yaml:"naming_pattern,omitempty"`

	// boolForm captures whether YAML authored the field as a bare
	// boolean (`ephemeral: true`) vs a block. Used for round-trip
	// fidelity in `charly deploy show` and to distinguish "not set" from
	// "set to true with all defaults" in error messages.
	boolForm bool
}

// UnmarshalYAML accepts either a bare boolean (`ephemeral: true|false`)
// or a mapping (`ephemeral: { ttl: 30m, ... }`). Boolean-false is
// equivalent to omitting the field entirely (handled at the
// DeploymentNode-level by the loader; this method only fires when the
// node has the key set). Boolean-true populates an empty
// EphemeralLifetime — defaults apply at validation time.
func (e *EphemeralLifetime) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		// Boolean shorthand. Reject anything that isn't a recognizable
		// bool to surface authoring mistakes early.
		switch strings.ToLower(strings.TrimSpace(node.Value)) {
		case "true", "yes", "on":
			e.boolForm = true
			return nil
		case "false", "no", "off", "":
			// "false" is equivalent to absence. The charly.yml
			// loader's nil check still holds, but honor a literal
			// `ephemeral: false` by leaving fields zero. The caller
			// must check whether boolForm was set; in practice the
			// load path interprets nil-or-zero as non-ephemeral.
			return fmt.Errorf("ephemeral: false is not supported — omit the field instead (or set ephemeral: true / ephemeral: {block} to mark ephemeral)")
		default:
			return fmt.Errorf("ephemeral: scalar value %q is not a boolean", node.Value)
		}
	case yaml.MappingNode:
		// Block form. Decode the underlying type without recursing
		// through this UnmarshalYAML.
		type rawEphemeral struct {
			TTL           string `yaml:"ttl,omitempty"`
			KeepOnFailure bool   `yaml:"keep_on_failure,omitempty"`
			NamingPattern string `yaml:"naming_pattern,omitempty"`
		}
		var raw rawEphemeral
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("ephemeral block: %w", err)
		}
		e.TTL = raw.TTL
		e.KeepOnFailure = raw.KeepOnFailure
		e.NamingPattern = raw.NamingPattern
		e.boolForm = false
		return nil
	default:
		return fmt.Errorf("ephemeral: unsupported YAML node kind %d (expected boolean scalar or mapping)", node.Kind)
	}
}

// EffectiveTTL returns the parsed TTL with sane default. Empty TTL
// (boolean-shorthand authoring) → 1h.
func (e *EphemeralLifetime) EffectiveTTL() time.Duration {
	if e == nil {
		return 0
	}
	if e.TTL == "" {
		return time.Hour
	}
	d, err := time.ParseDuration(e.TTL)
	if err != nil || d <= 0 {
		return time.Hour
	}
	return d
}

// EffectiveNamingPattern returns the configured pattern with sane default.
func (e *EphemeralLifetime) EffectiveNamingPattern() string {
	if e == nil || e.NamingPattern == "" {
		return "{{.Source}}-eph-{{.UUID6}}"
	}
	return e.NamingPattern
}

// Canonical preemption policy values. Stop is the freeing mechanism;
// Restore is when the holder is brought back.
const (
	PreemptStopShutdown   = "shutdown"   // graceful ACPI shutdown / podman stop; disk preserved (only supported value)
	PreemptRestoreAlways  = "always"     // restart the holder regardless of the claim's outcome (default)
	PreemptRestoreSuccess = "on-success" // restart only if the claim released cleanly; leave stopped on failure
)

// PreemptibleConfig is the HOLDER-side block of the resource-arbitration
// axis (see DeploymentNode.Preemptible). Authoring forms:
//
//	preemptible: [nvidia-gpu]        # list shorthand → Holds, default stop/restore
//	preemptible:                     # block form
//	  holds: [nvidia-gpu]
//	  stop: shutdown
//	  restore: always
type PreemptibleConfig struct {
	// Holds is the set of named exclusive-resource tokens this deploy
	// occupies. REQUIRED, non-empty (validated): a preemptible holder that
	// holds nothing is meaningless. Tokens are operator-chosen names matched
	// by set-intersection against a claimant's requires_exclusive.
	Holds []string `yaml:"holds,omitempty"`

	// Stop is how the arbiter frees the resource. Only "shutdown" (graceful
	// ACPI shutdown for a VM / podman stop for a pod, disk + definition
	// preserved) is supported — the ONLY mechanism that releases a VFIO
	// passthrough device (suspend/pause keep the device assigned;
	// managedsave is unsupported with passthrough). Empty → "shutdown".
	Stop string `yaml:"stop,omitempty"`

	// Restore is when the arbiter restarts the holder after the claim is
	// released: "always" (default — the holder MUST survive, so it returns
	// regardless of the claim's outcome) or "on-success" (leave it stopped
	// on a failed claim, for operator inspection). Empty → "always".
	Restore string `yaml:"restore,omitempty"`
}

// UnmarshalYAML accepts either a token-list shorthand (`preemptible:
// [nvidia-gpu]` → Holds with default stop/restore) or a mapping block
// (`preemptible: {holds: [...], stop: ..., restore: ...}`). A bare scalar
// (e.g. `preemptible: true`) is rejected — a holder must name what it holds.
func (p *PreemptibleConfig) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.SequenceNode:
		var holds []string
		if err := node.Decode(&holds); err != nil {
			return fmt.Errorf("preemptible list: %w", err)
		}
		p.Holds = holds
		return nil
	case yaml.MappingNode:
		type rawPreemptible struct {
			Holds   []string `yaml:"holds,omitempty"`
			Stop    string   `yaml:"stop,omitempty"`
			Restore string   `yaml:"restore,omitempty"`
		}
		var raw rawPreemptible
		if err := node.Decode(&raw); err != nil {
			return fmt.Errorf("preemptible block: %w", err)
		}
		p.Holds = raw.Holds
		p.Stop = raw.Stop
		p.Restore = raw.Restore
		return nil
	default:
		return fmt.Errorf("preemptible: expected a token list (preemptible: [token, ...]) or a block (preemptible: {holds: [...]}), got YAML node kind %d", node.Kind)
	}
}

// EffectiveStop returns the configured stop mechanism with the default.
func (p *PreemptibleConfig) EffectiveStop() string {
	if p == nil || p.Stop == "" {
		return PreemptStopShutdown
	}
	return p.Stop
}

// EffectiveRestore returns the configured restore policy with the default.
func (p *PreemptibleConfig) EffectiveRestore() string {
	if p == nil || p.Restore == "" {
		return PreemptRestoreAlways
	}
	return p.Restore
}

// LifecycleTag returns the literal Lifecycle field. Implements the
// Classified interface.
func (c DeploymentNode) LifecycleTag() string {
	return c.Lifecycle
}

// VmDeployState is the auto-managed runtime state for a vm: deploy.
// Written by VmDeployTarget on first apply; preserved across charly deploy
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
	// (distinct from the host user running charly).
	SshUser string `yaml:"ssh_user,omitempty"`

	// Backend is the VM backend used to boot this VM: "qemu" or
	// "libvirt". Pinned at first apply so subsequent operations don't
	// thrash between backends if the user's vm.backend setting
	// changes underneath them.
	Backend string `yaml:"backend,omitempty"`

	// KeyInjectionResolved is the effective D13 state after auto
	// defaults + explicit overrides resolved. Two booleans (one per
	// channel). Informational; used by charly deploy show and for audit
	// purposes.
	KeyInjectionResolved *VmKeyInjectionResolved `yaml:"key_injection_resolved,omitempty"`

	// CharlyInstallStrategy is the VmCharlyInstall.Strategy chosen at first
	// apply: "auto", "scp", "url", or "skip". Informational.
	CharlyInstallStrategy string `yaml:"charly_install_strategy,omitempty"`

	// CloudInitRenderedDigest is the sha256 of the last rendered
	// user-data (structured intent + applied defaults). Lets VmDeployTarget
	// detect drift — if the current rendered user-data doesn't match
	// the recorded digest, the user changed the kind:vm entity and
	// the seed ISO needs to be regenerated before re-apply.
	CloudInitRenderedDigest string `yaml:"cloud_init_rendered_digest,omitempty"`

	// Snapshots is the set of snapshots known to charly for this VM (mode
	// + libvirt name + disk path + creation time + refcount). Acts as
	// a charly.yml-side mirror of the per-VM registry.json so other
	// commands can interrogate snapshot state without filesystem
	// access. Maintained by `charly vm snapshot create/delete/promote`.
	Snapshots []VmSnapshotState `yaml:"snapshot,omitempty"`

	// Ephemeral, when non-nil, captures the live runtime state of an
	// active ephemeral instantiation: the registered systemd transient
	// timer, the parent snapshot reference, the TTL deadline, and the
	// optional parent ephemeral ID for nested cases. Reset to nil on
	// `charly deploy del` (clean teardown) or `charly status --reap-orphans`
	// (orphan cleanup). Presence here means an instance is/was active;
	// the underlying-resource check (libvirt domain alive) determines
	// whether it's still healthy.
	Ephemeral *EphemeralRuntime `yaml:"ephemeral,omitempty"`
}

// VmSnapshotState records one snapshot in charly.yml's vm_state mirror.
// The authoritative store is the per-VM `registry.json` on disk; this
// mirror lets `charly status` / `charly deploy show` report state without
// filesystem reads.
type VmSnapshotState struct {
	// Name uniquely identifies the snapshot within this VM.
	Name string `yaml:"name"`

	// Mode is "external" or "internal".
	Mode string `yaml:"mode"`

	// LibvirtName is the snapshot's name as registered with libvirt
	// (typically same as Name; recorded for explicitness so the libvirt
	// snapshot APIs can be invoked without re-deriving).
	LibvirtName string `yaml:"libvirt_name,omitempty"`

	// DiskPath is the absolute path to the external snapshot file.
	// Empty for internal-mode snapshots.
	DiskPath string `yaml:"disk_path,omitempty"`

	// Description carries the operator-supplied note from --description
	// at create-time.
	Description string `yaml:"description,omitempty"`

	// Created is the ISO8601 creation timestamp.
	Created string `yaml:"created,omitempty"`

	// Parent is the prior snapshot in the chain at creation time
	// (informational; V1 builds chains implicitly).
	Parent string `yaml:"parent,omitempty"`

	// Refcount tracks active clones / ephemerals depending on this
	// snapshot. `charly vm snapshot delete` refuses while > 0.
	Refcount int `yaml:"refcount"`
}

// EphemeralRuntime captures the live runtime state of an active
// ephemeral instantiation. Persisted under VmDeployState.Ephemeral
// (also Pod/K8s analogues; same shape for symmetry).
type EphemeralRuntime struct {
	// ID is a six-char random hex string uniquely identifying this
	// ephemeral instantiation.
	ID string `yaml:"id"`

	// ParentVm names the kind:vm entity (or kind:box / kind:k8s for
	// pod / k8s targets) the ephemeral was instantiated from.
	ParentVm string `yaml:"parent_vm,omitempty"`

	// ParentSnapshot names the snapshot used as the cloned overlay's
	// backing disk, when applicable. Empty for pod/k8s ephemerals
	// (which don't have backing chains).
	ParentSnapshot string `yaml:"parent_snapshot,omitempty"`

	// ParentEphemeral, when non-empty, is the ID of the outer
	// ephemeral whose lifecycle wraps this one (nested case). Set
	// from CHARLY_EPHEMERAL_PARENT environment variable at Add time.
	ParentEphemeral string `yaml:"parent_ephemeral,omitempty"`

	// ChildRefcount tracks nested ephemerals that name this one as
	// their parent. Recursive teardown decrements before destroying.
	ChildRefcount int `yaml:"child_refcount,omitempty"`

	// TimerUnit is the systemd transient unit name the TTL safety
	// timer is registered as. Empty if registration failed or was
	// skipped (e.g., on systems without user systemd). On clean
	// teardown, `charly deploy del` cancels this unit.
	TimerUnit string `yaml:"timer_unit,omitempty"`

	// TtlDeadline is the absolute time (ISO8601) when the TTL timer
	// will fire. Computed at Add time as time.Now() + EffectiveTTL.
	// `charly status` displays the remaining time.
	TtlDeadline string `yaml:"ttl_deadline,omitempty"`

	// Status is one of "provisioning" | "active" | "tearing_down".
	// Reset to nil parent on clean teardown.
	Status string `yaml:"status,omitempty"`

	// InstanceName is the rendered NamingPattern result, e.g.
	// "arch-test-eph-a3f2c1" — the name as it appears to libvirt /
	// podman / kubectl.
	InstanceName string `yaml:"instance_name,omitempty"`
}

// VmKeyInjectionResolved is the effective per-channel toggle state
// after D13 auto-default resolution + explicit-wins merging.
type VmKeyInjectionResolved struct {
	SMBIOS    bool `yaml:"smbios"`
	CloudInit bool `yaml:"cloud_init"`
}

// InstallOptsConfig holds charly.yml install_opts settings for a host
// deploy. Mirrors the command-line flags on DeployAddCmd so a user can
// pin their choices in charly.yml instead of repeating them.
type InstallOptsConfig struct {
	WithServices     bool   `yaml:"with_service,omitempty"`
	AllowRepoChanges bool   `yaml:"allow_repo_changes,omitempty"`
	AllowRootTasks   bool   `yaml:"allow_root_tasks,omitempty"`
	SkipIncompatible bool   `yaml:"skip_incompatible,omitempty"`
	Verify           bool   `yaml:"verify,omitempty"`
	BuilderImage     string `yaml:"builder_image,omitempty"`
}

// ApplyTo merges install_opts settings into an EmitOpts. CLI flags
// still win — charly.yml provides defaults, not overrides. Nil
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

// DeployVolumeConfig overrides the backing for a candy-declared volume.
type DeployVolumeConfig struct {
	Name       string `yaml:"name"`                  // matches candy volume name
	Type       string `yaml:"type,omitempty"`        // "volume" (default), "bind", "encrypted"
	Host       string `yaml:"host,omitempty"`        // explicit host path (bind type only, optional)
	Path       string `yaml:"path,omitempty"`        // container path (only for deploy-only volumes not in any candy)
	DataSeeded bool   `yaml:"data_seeded,omitempty"` // tracks if data was provisioned from image
	DataSource string `yaml:"data_source,omitempty"` // image:tag that provided the data
}

// DeploySecretConfig overrides or provides a secret for deployment.
type DeploySecretConfig struct {
	Name   string `yaml:"name"`             // matches candy secret name
	Source string `yaml:"source,omitempty"` // "keyring" (default), "env:VAR", "file:/path"
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
	Path      string `yaml:"path,omitempty"`       // container mount path (optional — candy can declare)
}

// DeployProbes — target-agnostic probes. Each entry is a Check (same shape
// as the existing declarative test vocabulary: file, command, addr, http…).
type DeployProbes struct {
	Liveness  *Op `yaml:"liveness,omitempty"`
	Readiness *Op `yaml:"readiness,omitempty"`
	Startup   *Op `yaml:"startup,omitempty"`
}

// deployKey returns the charly.yml map key for an image, optionally qualified by instance.
// Base images use just the image name; instances use "image/instance".
func deployKey(boxName, instance string) string {
	if instance == "" {
		return boxName
	}
	return boxName + "/" + instance
}

// canonicalizeDeployArg splits Pattern A "<base>/<instance>" CLI positional
// args into their component (image, instance) pair. Idempotent: if the input
// is already split (instance != "") or contains no slash, returns as-is.
// Pattern B (FQ ref containing "/") is identified by presence of "@" or ":"
// suffix on the leftmost segment OR a registry-host pattern (contains "."
// before the first "/") and passed through untouched.
//
// MUST be called at the top of every CLI verb that takes a positional
// deploy-arg (`charly config`, `charly start`, `charly stop`, `charly shell`, `charly logs`,
// `charly update`, `charly status`, `charly remove`) — before any downstream code reads
// c.Image or c.Instance. Without this, Pattern A instance deploys leak
// past the canonicalization boundary and downstream code conflates the
// deploy key with the image short-name (see Bug 2/3 RCA notes —
// MergeDeployOntoMetadata composes wrong key, port/env overlays drop).
func canonicalizeDeployArg(arg, instance string) (box, inst string) {
	if instance != "" || arg == "" {
		return arg, instance
	}
	if !strings.Contains(arg, "/") {
		return arg, ""
	}
	// Registry-qualified ref (Pattern B): contains "." in the first segment
	// (registry host like ghcr.io) or "@" anywhere (digest pin) or the
	// trailing segment carries ":tag". Pass through.
	first := arg
	if before, _, ok := strings.Cut(arg, "/"); ok {
		first = before
	}
	if strings.Contains(first, ".") || strings.Contains(arg, "@") || strings.Contains(arg[strings.LastIndex(arg, "/"):], ":") {
		return arg, ""
	}
	return parseDeployKey(arg)
}

// parseDeployKey splits a charly.yml map key back into image name and instance.
// "selkies-desktop" → ("selkies-desktop", "")
// "selkies-desktop/foo" → ("selkies-desktop", "foo")
func parseDeployKey(key string) (boxName, instance string) {
	if before, after, ok := strings.Cut(key, "/"); ok {
		return before, after
	}
	return key, ""
}

// resolveDeployKeyToBox maps a deploy-key name to the `box:` field of
// its deploy entry. User (~/.config/charly/charly.yml) wins over project
// (charly.yml/check.yml) — the same precedence the check runner and
// `charly config` use. Returns "" when no entry declares a box for the key
// (caller decides the fallback). Implements the Pattern-B (arbitrary
// deploy-key + version-pin) and kind:check-bed (key != box) lookups.
// See /charly-core:deploy "Two supported deploy patterns".
func resolveDeployKeyToBox(key, instance string) string {
	if key == "" {
		return ""
	}
	// User-side first.
	if dc := loadDeployConfigForRead("resolveDeployKeyToBox"); dc != nil {
		if entry, ok := dc.Deploy[deployKey(key, instance)]; ok && entry.Box != "" {
			return entry.Box
		}
		if entry, ok := dc.Deploy[key]; ok && entry.Box != "" {
			return entry.Box
		}
	}
	// Project-level fallback.
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			if pc := uf.ProjectDeployConfig(); pc != nil {
				if entry, ok := pc.Deploy[key]; ok && entry.Box != "" {
					return entry.Box
				}
			}
		}
	}
	return ""
}

// findVmDeployNode finds the DeploymentNode for a vm-target deploy. It is
// THE shared "which deploy entry backs this VM" lookup used by both
// `charly deploy add` (artifact-env collection) and `charly check live` (tests
// overlay), so the two never diverge. Resolution order:
//  1. by deploy NAME (the entry key) — the precise match;
//  2. by the legacy "vm:<name>" key form;
//  3. by scanning for any target:vm entry whose `vm:` field == vmName (or
//     == name) — the fallback when the caller only knows the vm entity.
//
// Keying by the deploy NAME first is load-bearing: a bed whose key differs
// from its vm entity (e.g. check-k3s-vm -> vm: k3s-vm) is found by its key,
// not mis-resolved via the vm entity name.
func findVmDeployNode(deploys map[string]DeploymentNode, name, vmName string) (DeploymentNode, bool) {
	if deploys == nil {
		return DeploymentNode{}, false
	}
	if name != "" {
		if e, ok := deploys[name]; ok && (e.Target == "vm" || e.Vm != "") {
			return e, true
		}
		if e, ok := deploys["vm:"+name]; ok {
			return e, true
		}
	}
	for _, e := range deploys {
		if e.Target == "vm" && e.Vm != "" && (e.Vm == vmName || e.Vm == name) {
			return e, true
		}
	}
	return DeploymentNode{}, false
}

// vmEntityForDeploy resolves a vm-target deploy KEY to its `vm:` cross-ref
// (the kind:vm entity name) — operator overlay first, then project config,
// via the shared findVmDeployNode. Returns "" when no entry declares a vm
// entity. THE single deploy-key→vm-entity resolver so `charly update <bed>`
// (VmUnifiedTarget) and any other vm-deploy consumer agree: the deploy KEY
// (e.g. check-k3s-vm) is NOT the vm entity name (k3s-vm) when they differ —
// blindly using the key breaks `charly vm create`/`destroy` for such beds.
func vmEntityForDeploy(deployName string) string {
	if deployName == "" {
		return ""
	}
	if dc := loadDeployConfigForRead("vmEntityForDeploy"); dc != nil {
		if node, ok := findVmDeployNode(dc.Deploy, deployName, ""); ok && node.Vm != "" {
			return node.Vm
		}
	}
	if dir, err := os.Getwd(); err == nil {
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			if pc := uf.ProjectDeployConfig(); pc != nil {
				if node, ok := findVmDeployNode(pc.Deploy, deployName, ""); ok && node.Vm != "" {
					return node.Vm
				}
			}
		}
	}
	return ""
}

// resolveDeployBoxName is THE single deploy-key→image-name resolver used
// by every deploy-mode command that starts from a deploy key (charly config /
// start / shell / check live). It returns the deploy entry's declared
// `box:` (resolveDeployKeyToBox), falling back to the key itself when
// no entry declares one (the key==image convention). Before this was
// shared, `charly config` resolved key→image but `charly start`/`charly shell`/
// `charly check live` treated the key AS the image — so a kind:check bed
// (check-jupyter-pod → jupyter) or any Pattern-B deploy resolved a
// different (wrong/unresolvable) image per command. `charly update` reaches the
// same value via its already-resolved merged-tree node (node.Image), so it
// reads that directly rather than re-loading config here.
func resolveDeployBoxName(key, instance string) string {
	if img := resolveDeployKeyToBox(key, instance); img != "" {
		return img
	}
	return key
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
	for key := range dc.Deploy {
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

// isSameBaseBox returns true if source is the same base image (with or without instance).
func isSameBaseBox(source, boxName string) bool {
	return source == boxName || strings.HasPrefix(source, boxName+"/")
}

// DeployConfigPath returns the path to the deploy overlay file.
// Package-level var for testability (same pattern as RuntimeConfigPath).
var DeployConfigPath = defaultDeployConfigPath

func defaultDeployConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("determining config directory: %w", err)
	}
	return filepath.Join(configDir, "charly", "charly.yml"), nil
}

// LoadDeployConfig reads the per-host deploy overlay (~/.config/charly/charly.yml)
// through the unified loader — the SAME LoadUnified path as every project
// charly.yml. Returns nil, nil if the file doesn't exist.
//
// Every transform the old bespoke parser did — the `images:` legacy-key reject,
// the deployment-tree / required-box: / preemptible / ephemeral-naming
// validation, and the ephemeral→disposable auto-promotion — now runs INSIDE
// LoadUnified (its version gate + RejectLegacyPluralKeys + the deploy-validation
// block subsume the legacy check; the ephemeral/naming validators + promotion
// were consolidated there so a PROJECT charly.yml's inline deploy: entries get
// them too — R3, one path).
func LoadDeployConfig() (*DeployConfig, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, nil
	}
	configDir := filepath.Dir(path)

	// Host-file-existence guard: a host still on the legacy `deploy.yml`
	// filename would otherwise silently lose its overlay (LoadUnified reads
	// charly.yml only when the project is already at HEAD). Fail loud with the
	// migration hint — mirrors the old hasLegacyImagesKey safety.
	if legacy := filepath.Join(configDir, "deploy.yml"); fileExists(legacy) && !fileExists(path) {
		return nil, fmt.Errorf(
			"per-host deploy overlay at %s uses the legacy `deploy.yml` filename — run `charly migrate` to rename it to charly.yml (the unified per-host config)",
			legacy,
		)
	}

	uf, ok, err := LoadUnified(configDir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	// A present-but-empty config still returns a non-nil DeployConfig (matching
	// the old bespoke parser), so callers that range/index dc.Deploy without a
	// nil guard keep working after an overlay's last entry is removed.
	if dc := uf.ProjectDeployConfig(); dc != nil {
		return dc, nil
	}
	return &DeployConfig{}, nil
}

// hasLegacyImagesKey reports whether the raw YAML body has a top-level
// `images:` key — the legacy pre-2026-04 root shape — instead of the modern
// `deploy:` map. The detection is structural (yaml.v3 Node walk on root-
// level mapping nodes) rather than line-oriented to avoid false positives on
// nested `images:` fields inside test fixtures or comment text.
func hasLegacyImagesKey(data []byte) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return false
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return false
	}
	hasImages := false
	hasDeploy := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i]
		if key.Kind != yaml.ScalarNode {
			continue
		}
		switch key.Value {
		case "images":
			hasImages = true
		case "deploy":
			hasDeploy = true
		}
	}
	return hasImages && !hasDeploy
}

// OccupiedHostPorts returns the set of host ports already published by
// any deployment in dc except the named one (`excludeKey` is typically
// the deploy key for the entry currently being expanded — we want to
// allow it to keep its old allocations, not avoid them). Used by
// ResolveDeployPorts to keep auto-allocations from colliding across deploys.
func (dc *DeployConfig) OccupiedHostPorts(excludeKey string) map[int]bool {
	out := map[int]bool{}
	if dc == nil {
		return out
	}
	for key, entry := range dc.Deploy {
		if key == excludeKey {
			continue
		}
		// Prefer ResolvedPort over Port (Port may still contain "auto"
		// in another entry that hasn't been expanded yet).
		mappings := entry.ResolvedPort
		if mappings == nil {
			mappings = entry.Port
		}
		for _, m := range mappings {
			if IsAutoPort(m) {
				continue
			}
			if h, err := ParseHostPort(m); err == nil {
				out[h] = true
			}
		}
	}
	return out
}

// MergeDeployOntoMetadata applies a per-host charly.yml entry's overrides (ports,
// env, security, tunnel, secrets, …) onto label-derived image metadata.
// Field-level replace semantics.
//
// The overlay entry is keyed by deployName — the charly.yml key base the caller
// is operating on (the bed / instance / Pattern-B name), NOT meta.Image (the
// baked ai.opencharly.box short-name). For a plain deploy the two coincide,
// but a kind:check bed or a Pattern-B deploy carries a key distinct from its
// image, so the caller MUST pass its own deploy key (typically c.Image). Keying
// off meta.Image would read whichever sibling deploy merely shares the image and
// clobber this entry's explicit port:/env:/security: — e.g. a bed remapping
// 45434:11434 would lose its port to a running same-image deploy on 11434.
//
//nolint:gocyclo // field-by-field conditional overlay merge; every branch is a peer
func MergeDeployOntoMetadata(meta *BoxMetadata, dc *DeployConfig, deployName, instance string) {
	// Volume isolation runs UNCONDITIONALLY (independent of any charly.yml
	// overlay), so every distinctly-named deploy gets its own volume namespace
	// on the very first `charly config` and every run after — see
	// scopeVolumesToDeployKey.
	scopeVolumesToDeployKey(meta, deployName, instance)

	if dc == nil || dc.Deploy == nil || meta == nil {
		return
	}

	overlay, ok := dc.Deploy[deployKey(deployName, instance)]
	if !ok {
		return
	}

	if overlay.Description != "" {
		// A deploy overlay's description is purely informational — it carries no
		// status signal (the maturity rung lives on the candy `status:` field and
		// is baked into the image's ai.opencharly.status label). Keep the baked
		// meta.Status; only refresh the human-facing summary.
		meta.Info = descriptionInfo(overlay.Description)
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
	// Ports: prefer the persisted ResolvedPort (the auto-allocated /
	// pinned host:container mapping `charly config` computed via
	// ResolveDeployPorts). A deploy `port:` entry is only a PIN INPUT to that
	// resolution — never a wholesale replacement — so it is NOT applied here.
	// If ResolvedPort isn't set yet (deploy not configured), meta.Port keeps the
	// image-label's bare container ports (published 1:1 on 127.0.0.1 until the
	// next charly config resolves them).
	if overlay.ResolvedPort != nil {
		meta.Port = overlay.ResolvedPort
	}
	if overlay.Env != nil {
		meta.Env = overlay.Env
	}
	if overlay.Security != nil {
		// Field-level merge: overlay fields override, unset fields fall
		// through to the label-provided values. A full struct replace would
		// wipe candy defaults like shm_size when a user sets just --memory-max
		// via `charly config`.
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
		if overlay.Security.IpcMode != "" {
			meta.Security.IpcMode = overlay.Security.IpcMode
		}
		if overlay.Security.CgroupNS != "" {
			meta.Security.CgroupNS = overlay.Security.CgroupNS
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
	// Merge charly.yml secrets onto image label secrets
	if overlay.Secret != nil {
		deployByName := make(map[string]DeploySecretConfig, len(overlay.Secret))
		for _, ds := range overlay.Secret {
			deployByName[ds.Name] = ds
		}
		// Override matching secrets from image labels with charly.yml source config
		for i, ls := range meta.Secret {
			if _, ok := deployByName[ls.Name]; ok {
				// Deploy.yml provides this secret — keep the label entry
				// (the source override is used at provisioning time, not in the label)
				_ = i
			}
		}
		// Add deploy-only secrets that aren't in the image labels
		for _, ds := range overlay.Secret {
			found := false
			for _, ls := range meta.Secret {
				if ls.Name == ds.Name {
					found = true
					break
				}
			}
			if !found {
				meta.Secret = append(meta.Secret, LabelSecretEntry{
					Name:   ds.Name,
					Target: "/run/secrets/" + ds.Name,
				})
			}
		}
	}

}

// deployVolumePrefix is the named-volume prefix for a deploy: it equals the
// deploy's container name plus a dash, so EVERY distinctly-named deploy — a base
// deploy, a Pattern-B deploy, a `<base>/<instance>`, or a kind:check bed — gets
// its own volume namespace. Two deploys NEVER share a named volume unless they
// share a container name (which they can't — container names are unique). This
// is the single source of truth for volume naming; ResolveVolumeBacking,
// removeVolumes, and scopeVolumesToDeployKey all key off it.
func deployVolumePrefix(deployKey, instance string) string {
	return containerNameInstance(deployKey, instance) + "-"
}

// deployStorageDir is the per-deploy directory component for bind-auto paths and
// encrypted-volume directories. Like deployVolumePrefix it is unique per deploy
// (base vs instance vs Pattern-B vs bed), so auto-provisioned bind/encrypted
// storage is never shared across differently-named deploys. For a base deploy
// with no instance it is just the deploy key (unchanged from the historical
// layout); an instance appends "-<instance>".
func deployStorageDir(deployKey, instance string) string {
	if instance == "" {
		return deployKey
	}
	return deployKey + "-" + instance
}

// scopeVolumesToDeployKey renames meta's named-volume mounts from the
// image-derived prefix (charly-<image>-) to the deploy's own prefix
// (deployVolumePrefix), so every distinctly-named deploy ALWAYS gets volume
// mounts distinct from any other deploy of the same image — production pods,
// instances, and disposable kind:check beds alike. Before this, names were keyed
// by the baked ai.opencharly.box label, so two deploys of one image (e.g.
// the operator's immich plus a disposable immich bed, or two production pods)
// shared the SAME named volumes and could read or corrupt each other's data.
// No-op when the deploy's prefix already equals the image prefix (the common
// `charly config <image>` base deploy), so that deploy's volume names never change.
// Idempotent: re-running on already-scoped names is a no-op.
func scopeVolumesToDeployKey(meta *BoxMetadata, deployName, instance string) {
	if meta == nil || deployName == "" {
		return
	}
	newPrefix := deployVolumePrefix(deployName, instance)
	oldPrefix := "charly-" + meta.Box + "-"
	if newPrefix == oldPrefix {
		return
	}
	for i := range meta.Volume {
		if rest, ok := strings.CutPrefix(meta.Volume[i].VolumeName, oldPrefix); ok {
			meta.Volume[i].VolumeName = newPrefix + rest
		}
	}
}

// ResolveVolumeBacking splits image volumes into named volumes and bind mounts
// based on charly.yml volume configuration.
// Volumes without a deploy override remain as named volumes.
// Volumes with type=bind or type=encrypted become ResolvedBindMount.
// Deploy-only volumes (with Path set, not in labels) are also supported.
func ResolveVolumeBacking(boxName, instance string, labelVolumes []VolumeMount, deployVolumes []DeployVolumeConfig, home string, encStoragePath string, volumesPath string) ([]VolumeMount, []ResolvedBindMount) {
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
		// Extract short name from the deploy-scoped prefix (charly-<deploy>-<name>).
		shortName := strings.TrimPrefix(vol.VolumeName, deployVolumePrefix(boxName, instance))

		dv, hasOverride := deployByName[shortName]
		if hasOverride {
			matched[shortName] = true
		}

		if hasOverride && (dv.Type == "bind" || dv.Type == "encrypted") {
			var hostPath string
			switch {
			case dv.Type == "encrypted":
				if dv.Host != "" {
					// Explicit per-volume path: /path/{cipher,plain}
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					// Global default, per-deploy: <encStoragePath>/charly-<deploy>-<name>/{cipher,plain}
					hostPath = encryptedPlainDir(encStoragePath, deployStorageDir(boxName, instance), shortName)
				}
			case dv.Host != "":
				hostPath = expandHostHome(dv.Host)
			default:
				// Auto path, per-deploy: <volumesPath>/<deploy>/<name>
				hostPath = filepath.Join(volumesPath, deployStorageDir(boxName, instance), shortName)
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

	// Add deploy-only volumes (not in any candy, must have Path)
	for _, dv := range deployVolumes {
		if matched[dv.Name] || dv.Path == "" {
			continue
		}
		containerPath := ExpandPath(dv.Path, home)
		if dv.Type == "bind" || dv.Type == "encrypted" {
			var hostPath string
			switch {
			case dv.Type == "encrypted":
				if dv.Host != "" {
					hostPath = filepath.Join(expandHostHome(dv.Host), "plain")
				} else {
					hostPath = encryptedPlainDir(encStoragePath, deployStorageDir(boxName, instance), dv.Name)
				}
			case dv.Host != "":
				hostPath = expandHostHome(dv.Host)
			default:
				hostPath = filepath.Join(volumesPath, deployStorageDir(boxName, instance), dv.Name)
			}
			bindMounts = append(bindMounts, ResolvedBindMount{
				Name:      dv.Name,
				HostPath:  hostPath,
				ContPath:  containerPath,
				Encrypted: dv.Type == "encrypted",
			})
		} else {
			volumes = append(volumes, VolumeMount{
				VolumeName:    deployVolumePrefix(boxName, instance) + dv.Name,
				ContainerPath: containerPath,
			})
		}
	}

	return volumes, bindMounts
}

// LoadDeployFile reads a charly.yml from an arbitrary path.
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

// SaveDeployConfig writes a DeployConfig to the standard charly.yml
// path. Uses tempfile + os.Rename for atomic write — defense in depth
// against partial writes truncating the prior file (primary guard is
// loadDeployConfigForWrite's error propagation; this catches any
// remaining IO/marshal failure mid-write). The tempfile lives in the
// same directory as the target so rename stays on the same filesystem.
func SaveDeployConfig(dc *DeployConfig) error {
	path, err := DeployConfigPath()
	if err != nil {
		return fmt.Errorf("determining deploy config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if dc == nil {
		dc = &DeployConfig{}
	}
	// Write a unified-shaped per-host charly.yml: the HEAD `version:` stamp lets
	// a re-load through LoadUnified pass the schema gate, and `deploy:` /
	// `provides:` route by shape exactly like a project charly.yml.
	stamped := struct {
		Version  string                    `yaml:"version"`
		Provides *ProvidesConfig           `yaml:"provides,omitempty"`
		Deploy   map[string]DeploymentNode `yaml:"deploy"`
	}{
		Version:  LatestSchemaVersion().String(),
		Provides: dc.Provides,
		Deploy:   dc.Deploy,
	}
	data, err := yaml.Marshal(&stamped)
	if err != nil {
		return fmt.Errorf("marshaling deploy config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".charly.yml.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("renaming %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// Lookup returns the DeploymentNode for (deployName, instance), or
// (zero, false) when the entry is absent. Safe to call on a nil
// *DeployConfig — lets callers chain
// `loadDeployConfigForRead(...).Lookup(deployName, instance)` without a
// separate nil check. deployName is the charly.yml key base the caller is
// operating on (typically c.Image), NOT the baked image short-name — for a
// kind:check bed or Pattern-B deploy the two differ. Pass the deploy key, never
// a value derived from an image label (see MergeDeployOntoMetadata).
func (dc *DeployConfig) Lookup(deployName, instance string) (DeploymentNode, bool) {
	if dc == nil {
		return DeploymentNode{}, false
	}
	entry, ok := dc.Deploy[deployKey(deployName, instance)]
	return entry, ok
}

// LookupKey looks up a deploy entry by its full charly.yml key (e.g.
// "foo", "foo/instance", "vm:name"). Safe on nil receiver.
func (dc *DeployConfig) LookupKey(key string) (DeploymentNode, bool) {
	if dc == nil {
		return DeploymentNode{}, false
	}
	entry, ok := dc.Deploy[key]
	return entry, ok
}

// loadDeployConfigForRead loads charly.yml for read-only consumption.
// Unlike the historical `dc, _ := LoadDeployConfig()` pattern (silently
// discards validation errors → caller proceeds with nil → feature
// degrades invisibly), this helper SURFACES the load error as a stderr
// warning while still returning nil — preserving the existing caller
// nil-check contract but giving the operator visibility into why a
// command behaved as if charly.yml were absent.
//
// Sibling of loadDeployConfigForWrite — the write variant returns an
// error and callers MUST abort; the read variant returns nil and
// callers MAY continue with degraded behavior.
//
// context is a short human-readable label included in the warning
// message so the operator can trace which code path noticed the
// problem (e.g. "charly status", "config injectEnvProvides").
func loadDeployConfigForRead(context string) *DeployConfig {
	dc, err := LoadDeployConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s: charly.yml unavailable for read: %v\n", context, err)
		return nil
	}
	return dc
}

// loadDeployConfigForWrite loads charly.yml for mutation. Unlike the
// historical `dc, _ := LoadDeployConfig()` pattern (silently discards
// validation errors → writer constructs an empty config → SaveDeployConfig
// truncates the file), this helper PROPAGATES the load error so writers
// can ABORT instead of destroying data.
//
// Cautionary tale: pre-2026-05-16 the `charly deploy add --disposable` write
// path discarded the load error. The 2026-05-12 require-image schema
// cutover widened the set of conditions under which LoadDeployConfig
// returns an error; once any pre-existing charly.yml entry failed
// validation, the next `charly deploy add` constructed a fresh empty
// DeployConfig containing only the new entry and truncated the on-disk
// file. The user's `provides:` block and unrelated deploy entries
// vanished silently. New write sites MUST use this helper.
//
// context is a short human-readable label included in the error message
// (e.g. "saveDeployState"). Returns (nil, error) when the file exists
// but failed parse/validation; (fresh empty config, nil) when the file
// doesn't exist; (parsed config, nil) on clean load.
func loadDeployConfigForWrite(context string) (*DeployConfig, error) {
	dc, err := LoadDeployConfig()
	if err != nil {
		return nil, fmt.Errorf("%s: refusing to write — charly.yml load failed: %w", context, err)
	}
	if dc == nil {
		dc = &DeployConfig{Deploy: make(map[string]DeploymentNode)}
	}
	if dc.Deploy == nil {
		dc.Deploy = make(map[string]DeploymentNode)
	}
	return dc, nil
}

// MergeDeployConfigs merges multiple DeployConfigs left-to-right. Later
// configs take precedence (field-level replace per image). The merge walks
// every yaml-tagged field of DeploymentNode via reflect: a field copies
// from src → dst when src's value is non-zero (string != "", slice/map/ptr
// not nil, bool != false, numeric != 0). This makes adding a new field to
// DeploymentNode automatically merge-correct — the pre-2026-05 hand-rolled
// per-field merge silently dropped 19+ fields (ResolvedPort, Description,
// Secret, Sidecar, Shell, Kubernetes, ForwardGpgAgent, ForwardSshAgent,
// Kind, Replica, Restart, Schedule, Resources, Expose, Storage, Probes,
// Cpus, Ram, DiskSize) whenever any merge → save cycle ran.
//
// The yaml tag `-` (currently only DeploymentNode.Inside, a derived
// runtime field) skips the merge. Untagged fields are also skipped.
func MergeDeployConfigs(configs ...*DeployConfig) *DeployConfig {
	result := &DeployConfig{Deploy: make(map[string]DeploymentNode)}
	for _, dc := range configs {
		if dc == nil || dc.Deploy == nil {
			continue
		}
		for name, overlay := range dc.Deploy {
			existing := result.Deploy[name]
			result.Deploy[name] = MergeDeploymentNode(existing, overlay)
		}
	}
	return result
}

// MergeDeploymentNode applies non-zero fields from `src` onto `dst` and
// returns the merged copy. Walks every yaml-tagged field via reflect; the
// yaml `-` tag (derived/runtime-only fields) is skipped. Same precedence
// rule as the underlying merge: src non-zero wins, otherwise dst passes
// through. Per R3 the single helper replaces the hand-rolled per-field
// merges that previously lived in MergeDeployConfigs (drift-prone — every
// new struct field needed a remembered append, and 19+ were missed).
func MergeDeploymentNode(dst, src DeploymentNode) DeploymentNode {
	dstV := reflect.ValueOf(&dst).Elem()
	srcV := reflect.ValueOf(src)
	t := dstV.Type()
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		tag := ft.Tag.Get("yaml")
		// Skip derived fields (yaml:"-") and untagged fields (rare; not
		// part of the persisted schema, so not merge-relevant).
		if tag == "-" || tag == "" {
			continue
		}
		sv := srcV.Field(i)
		if sv.IsZero() {
			continue
		}
		dstV.Field(i).Set(sv)
	}
	return dst
}

// isAutoVmDeployEntry reports whether a VM deploy entry carries NOTHING beyond
// the fields saveVmDeployState auto-sets — target: vm, vm:, and vm_state. Such
// an entry is a pure runtime-state record (e.g. a disposable check-bed VM) that
// `charly vm destroy` should delete so it doesn't accumulate. Any OTHER non-zero
// field means operator-authored per-host config (preemptible, env, tunnel,
// port, security, …) that MUST survive a destroy→create cycle. Compares against
// the zero node after blanking the three auto-set fields, so a newly-added
// per-host field is covered automatically (no remembered append — same
// drift-proof discipline as MergeDeploymentNode).
func isAutoVmDeployEntry(entry DeploymentNode) bool {
	probe := entry
	probe.VmState = nil
	probe.Target = ""
	probe.Vm = ""
	return reflect.DeepEqual(probe, DeploymentNode{})
}

// RemoveBoxDeploy removes an image's entry from a deploy config.
func RemoveBoxDeploy(dc *DeployConfig, boxName string) {
	if dc != nil && dc.Deploy != nil {
		delete(dc.Deploy, boxName)
	}
}

// cleanDeployEntry removes an image's entry from charly.yml (best-effort).
// Also removes global service env vars that were injected by this image.
// If charly.yml becomes empty after removal, the file is deleted.
func cleanDeployEntry(boxName, instance string) {
	dc, err := LoadDeployConfig()
	if err != nil || dc == nil {
		return
	}

	key := deployKey(boxName, instance)
	hasImage := false
	if _, ok := dc.Deploy[key]; ok {
		hasImage = true
		RemoveBoxDeploy(dc, key)
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
			for k := range dc.Deploy {
				base, _ := parseDeployKey(k)
				if base == boxName {
					hasOtherEntries = true
					break
				}
			}
			if !hasOtherEntries {
				if len(dc.Provides.Env) > 0 {
					cleaned, removed := removeBySource(dc.Provides.Env, boxName)
					if removed {
						dc.Provides.Env = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed env provides from %s\n", boxName)
					}
				}
				if len(dc.Provides.MCP) > 0 {
					cleaned, removed := removeBySource(dc.Provides.MCP, boxName)
					if removed {
						dc.Provides.MCP = cleaned
						removedProvides = true
						fmt.Fprintf(os.Stderr, "Removed MCP provides from %s\n", boxName)
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

	if len(dc.Deploy) == 0 && dc.Provides == nil {
		if path, pathErr := DeployConfigPath(); pathErr == nil {
			_ = os.Remove(path)
		}
	} else if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not clean charly.yml: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "Cleaned charly.yml entry for %s\n", key)
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
	if before, _, ok := strings.Cut(entry, "="); ok {
		return before
	}
	return entry
}

// SaveDeployStateInput holds the deployment parameters to persist.
type SaveDeployStateInput struct {
	Ports []string
	// SetPorts gates whether Ports is written to charly.yml at all.
	// This ensures `charly config <name>`
	// (without --port flags) and `charly update <name>` no longer silently
	// overwrite operator port overrides with image-label defaults.
	// Writing Ports whenever input.Ports != nil would
	// turn every config-recompute into a port-state reset because the
	// caller always computes ports from `meta.Port` (image-label
	// defaults pre-merged with charly.yml). With SetPorts, the caller
	// explicitly opts in to writing only when the operator passed
	// `--port` flags. Same idiom as SetDisposable/SetLifecycle below.
	SetPorts bool
	Env      []string
	CleanEnv bool // true = replace env list; false = merge (upsert by key)
	EnvFile  string
	Network  string
	Security *SecurityConfig
	Volume   []DeployVolumeConfig
	Sidecar  map[string]SidecarDef
	Tunnel   *TunnelYAML

	// SecretNames lists env var names declared as secret_accepts /
	// secret_requires on the image. saveDeployState uses this list to
	// defensively strip any matching KEY=VAL entries from both the input
	// Env and the existing persisted entry.Env before writing. Defense in
	// depth for the §6 / Run() pipeline (MigratePlaintextEnvSecret and
	// scrubSecretCLIEnv are the primary gates). Populated by the Run()
	// call site from meta.SecretAccept/SecretRequires.
	SecretNames []string

	// Disposable + Lifecycle — the classification fields
	// (see /charly-internals:disposable). SetDisposable toggles whether the
	// Disposable field is written at all: when false, saveDeployState
	// leaves any pre-existing value untouched. Same idiom for lifecycle.
	SetDisposable bool
	Disposable    bool
	SetLifecycle  bool
	Lifecycle     string

	// Box + Target — the schema-required fields per the 2026-05-12
	// require-image cutover (validateDeployRequiresBox). Written
	// when non-empty AND when the existing entry doesn't already have
	// a value (don't clobber operator-authored refs on re-config).
	// Without these, `charly deploy add foo bar --disposable` would write
	// an entry that the validator then rejects on the next load —
	// hard-failing every subsequent `charly` invocation.
	Box    string
	Target string
}

// saveDeployState persists deployment parameters to charly.yml (best-effort).
// Merges onto any existing entry to preserve fields from charly deploy import.
//
// Defense-in-depth: any env entry whose key matches a name in input.SecretNames
// is stripped from both input.Env and the existing persisted entry.Env before
// writing. The primary gates against plaintext-credential leakage are
// MigratePlaintextEnvSecret and scrubSecretCLIEnv in config_image.go:Run();
// this scrub catches anything that slipped through (e.g., a future refactor
// that adds a new code path writing into dc.Env). Matches plan §6.7.
func saveDeployState(boxName, instance string, input SaveDeployStateInput) {
	dc, err := loadDeployConfigForWrite("saveDeployState")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to charly.yml: %v\n", err)
		return
	}
	key := deployKey(boxName, instance)
	entry := dc.Deploy[key] // preserve existing fields (tunnel, volumes, etc.)
	if input.Box != "" && entry.Box == "" {
		entry.Box = input.Box
	}
	if input.Target != "" && entry.Target == "" {
		entry.Target = input.Target
	}
	if input.Volume != nil {
		entry.Volume = input.Volume
	}
	// Ports gated on SetPorts: explicit opt-in required so a recompute
	// path that always-passes computed `meta.Port` doesn't silently
	// overwrite operator overrides. See SaveDeployStateInput.SetPorts
	// docstring.
	if input.SetPorts && input.Ports != nil {
		entry.Port = input.Ports
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
	if len(input.Sidecar) > 0 {
		entry.Sidecar = input.Sidecar
	}
	if input.Tunnel != nil {
		entry.Tunnel = input.Tunnel
	}
	// Classification fields: only written when the caller explicitly
	// opts in via SetDisposable / SetLifecycle. This lets repeated
	// saveDeployState calls from unrelated code paths (charly start, charly
	// config) leave a user-authored `disposable: true` in place.
	if input.SetDisposable {
		v := input.Disposable
		entry.Disposable = &v
	}
	if input.SetLifecycle {
		entry.Lifecycle = input.Lifecycle
	}
	// Defensive zero-write guard: refuse to persist a fully-zero
	// DeploymentNode (every field at its Go zero value). A future caller
	// that invokes saveDeployState with an empty SaveDeployStateInput on
	// a key that doesn't yet exist in the user overlay would otherwise
	// write `<key>: {}`, materializing an empty entry that masks any
	// matching entry from the project charly.yml deploy block (see
	// 2026-05 RCA: charly update did NOT directly do this, but the latent
	// shape was real and the user's charly.yml ended up empty by some
	// path we couldn't fully reconstruct — this guard makes the entire
	// regression class structurally impossible).
	if reflect.DeepEqual(entry, DeploymentNode{}) {
		return
	}
	dc.Deploy[key] = entry
	if err := SaveDeployConfig(dc); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save to charly.yml: %v\n", err)
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
		if before, _, ok := strings.Cut(kv, "="); ok {
			key = before
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
		key, _, _ := strings.Cut(kv, "=")
		newByKey[key] = kv
	}
	result := make([]string, 0, len(existing)+len(newVars))
	seen := make(map[string]bool)
	for _, kv := range existing {
		key, _, _ := strings.Cut(kv, "=")
		if newKV, ok := newByKey[key]; ok {
			result = append(result, newKV)
			seen[key] = true
		} else {
			result = append(result, kv)
		}
	}
	for _, kv := range newVars {
		key, _, _ := strings.Cut(kv, "=")
		if !seen[key] {
			result = append(result, kv)
		}
	}
	return result
}

// ExportAllBox exports all runtime-relevant fields for all enabled images in a Config.
func ExportAllBox(cfg *Config) *DeployConfig {
	dc := &DeployConfig{Deploy: make(map[string]DeploymentNode)}
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		// Schema v4: Tunnel / DNS / AcmeEmail / Engine no longer sourced
		// from BoxConfig (they're deploy-only).
		entry := DeploymentNode{
			Version:     img.Version,
			Description: img.Description,
			Env:         img.Env,
			EnvFile:     img.EnvFile,
			Security:    img.Security,
			Network:     img.Network,
		}
		// Only include if at least one field is set. Ports are no longer a box
		// field — they're inherited from candies and auto-allocated at deploy.
		if entry.Version != "" || entry.Description != "" ||
			entry.Env != nil ||
			entry.EnvFile != "" || entry.Security != nil || entry.Network != "" {
			dc.Deploy[name] = entry
		}
	}
	return dc
}
