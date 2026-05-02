package main

// deploy_add_cmd.go — `ov deploy add <name> [<ref>]` and
// `ov deploy del <name>`. Thin wiring on top of the pieces already
// built: BuildDeployPlan → {OCITarget, LocalDeployTarget,
// PodDeployTarget}.
//
// Name semantics:
//   - literal "host" → deploy to the local machine via LocalDeployTarget
//   - any other name → a named container deployment (ContainerDeploy
//     + existing quadlet/podman machinery)
//
// Both commands defer the heavy lifting to the targets. This file is
// just glue: ref resolution, plan compilation, target selection, and
// flag passing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DeployAddCmd implements `ov deploy add <name> [<ref>]`.
type DeployAddCmd struct {
	Name string `arg:"" help:"Deploy name ('host' for local system; any other string is a container deploy name)"`
	Ref  string `arg:"" optional:"" help:"Image or layer reference (local name, ./path.yml, or github.com/org/repo[/images/<n>|/layers/<n>][@ref])"`

	// Layer overlays (repeatable).
	AddLayer []string `long:"add-layer" help:"Extra layer to apply on top of the base image (repeatable)"`

	// Plan-level flags.
	Tag    string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the org.overthinkos.version OCI label)"`
	DryRun bool   `long:"dry-run" help:"Print the plan without executing"`
	Format string `long:"format" default:"table" enum:"table,json" help:"Output format for --dry-run"`
	Pull   bool   `long:"pull" help:"Force re-fetch of remote refs / image pull"`
	Verify bool   `long:"verify" help:"Re-run layer tests: on the host after install"`

	// Host-only gates.
	WithServices     bool   `long:"with-services" help:"Install systemd services (host target only)"`
	AllowRepoChanges bool   `long:"allow-repo-changes" help:"Allow repo config mutations (host target only)"`
	AllowRootTasks   bool   `long:"allow-root-tasks" help:"Allow arbitrary root cmd: tasks (host target only)"`
	SkipIncompatible bool   `long:"skip-incompatible" help:"Skip layers without host-matching format (host target only)"`
	BuilderImage     string `long:"builder-image" help:"Override the compile builder image"`
	AssumeYes        bool   `long:"yes" short:"y" help:"Assume yes; implies all allow-* gates plus skip sudo preflight"`

	// Disposable + lifecycle classification (see /ov-dev:disposable).
	// --disposable writes `disposable: true` into the deploy.yml
	// entry and authorizes autonomous `ov rebuild`. --lifecycle writes
	// the informational tier tag; it has NO effect on disposability
	// (no derivation).
	Disposable bool   `long:"disposable" help:"Mark this deploy disposable (authorizes autonomous ov rebuild; writes disposable: true into deploy.yml)"`
	Lifecycle  string `long:"lifecycle" help:"Informational tier tag (scratch|dev|test|qa|staging|prod|custom). NO effect on disposability — use --disposable for that."`
}

// DeployDelCmd implements `ov deploy del <name>`.
type DeployDelCmd struct {
	Name string `arg:"" help:"Deploy name (literal 'host' or a container deploy name)"`

	AssumeYes       bool `long:"yes" short:"y" help:"Skip confirmation prompts"`
	KeepRepoChanges bool `long:"keep-repo-changes" help:"Don't revert repo config even at zero refcount"`
	KeepServices    bool `long:"keep-services" help:"Don't disable systemd units (just stop tracking)"`
	KeepImage       bool `long:"keep-image" help:"Don't remove the synthesized overlay image (container target only)"`
	DryRun          bool `long:"dry-run" help:"Print the teardown plan without executing"`

	// Runner is populated by runVmDel / runLocalDel etc. to route reverse
	// ops to the right privilege context. Nil falls back to the local-exec
	// path in reverse_ops.go. Not exposed as a Kong flag.
	Runner ReverseRunner `kong:"-"`
}

// Run executes `ov deploy add`.
//
// For a schema-v2 config, c.Name may be a dotted path (foo.bar.baz)
// pointing into the deployments tree. The root segment (foo) is
// dispatched first; each descendant is dispatched afterwards with
// ParentExec threaded through via EmitOpts so nested targets execute
// inside their parent's venue.
//
// For a flat name (no children, no dots) the behavior is unchanged —
// exactly one target's Emit() call.
func (c *DeployAddCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	// Resolve the named root + any dotted-path subtree the user
	// targeted. Supports three call shapes:
	//
	//   ov deploy add host                   — legacy; root = "host"
	//   ov deploy add openclaw-stack         — v2 root with children
	//   ov deploy add openclaw-stack.web.db  — v2 subtree
	targetPath := c.Name
	tree, _ := resolveTreeRoot(dir)
	var rootNode *DeploymentNode
	var resolvedPath string
	// parentExec is the executor chain derived from any ANCESTORS the
	// dotted path walks through. Without this, `ov deploy add a.b.c`
	// would run c's dispatch locally — ignoring a/b's substrate.
	var parentExec DeployExecutor
	if tree != nil {
		if n, ancestors, nodeErr := ResolveNodePath(tree, targetPath); nodeErr == nil {
			rootNode = n
			resolvedPath = targetPath
			// Derive the parent's executor chain from ancestors so the
			// target node runs in the right substrate (SSH into parent
			// VM, podman exec into parent pod, etc.).
			segments := splitDottedPath(targetPath)
			for i, anc := range ancestors {
				ancPath := strings.Join(segments[:i+1], ".")
				next, derr := deriveChildExecutorForPath(ancPath, anc, parentExec)
				if derr != nil {
					return fmt.Errorf("deriving executor for ancestor %q: %w", ancPath, derr)
				}
				parentExec = next
			}
		}
	}

	// Walk pre-order. At each node, we dispatch using the existing
	// target-specific runHost/runVM/runContainer helpers, with
	// opts.ParentExec set to the executor derived from the parent
	// chain.
	//
	// When rootNode is nil (ref-based deploy with no deploy.yml entry
	// e.g. `ov deploy add foo ./path/to/image.yml`) we fall through
	// to the single-dispatch path.
	if rootNode == nil {
		return c.dispatchNode(resolvedPath, nil, nil, dir)
	}

	return WalkDeploymentTree(resolvedPath, rootNode, parentExec, func(path string, node *DeploymentNode, parentExec DeployExecutor) (DeployExecutor, error) {
		if err := c.dispatchNode(path, node, parentExec, dir); err != nil {
			return nil, err
		}
		return deriveChildExecutorForPath(path, node, parentExec)
	})
}

// dispatchNode compiles plans for a single node and runs the
// appropriate target. Factored out of Run so the tree walker can call
// it once per node.
//
// path is the dotted identifier ("", "openclaw-stack", or
// "openclaw-stack.web.db"). It's propagated via opts.Path so the
// target's logging can identify which node is executing.
//
// node is the resolved DeploymentNode; nil when the caller provided
// an explicit ref (Ref != "") with no matching deploy.yml entry.
//
// parentExec is the DeployExecutor of the enclosing environment; nil
// at the root. Non-nil means "this node is a child of something" —
// its target composes a NestedExecutor over parentExec.
func (c *DeployAddCmd) dispatchNode(path string, node *DeploymentNode, parentExec DeployExecutor, dir string) error {
	opts := c.emitOpts()
	opts.ParentExec = parentExec
	opts.Path = path
	// Note: opts.ParentNode is populated by the walker when available.

	// Per-node field overlays from the deploy.yml entry. On the root
	// this matches the pre-v2 behavior; on children we must reload
	// fields from the child node (not c.Name's top-level entry).
	refStr := c.Ref
	addLayers := append([]string(nil), c.AddLayer...)
	tag := c.Tag
	if node != nil {
		if node.Version != "" {
			tag = node.Version
		}
		if node.InstallOpts != nil {
			opts = node.InstallOpts.ApplyTo(opts)
		}
		if len(addLayers) == 0 && len(node.AddLayers) > 0 {
			addLayers = append([]string(nil), node.AddLayers...)
		}
	}
	if refStr == "" {
		if node == nil {
			return fmt.Errorf("ov deploy add: no <ref> and deploy.yml has no entry for %q", path)
		}
		// Schema v3: prefer the explicit `image:` cross-ref when set,
		// so deployment names like "sway-pod" don't need to match an
		// image name. Falls back to the deploy key for legacy entries.
		switch {
		case node.Image != "":
			refStr = node.Image
		default:
			refStr = pathLeaf(path)
		}
	}

	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(dir)
	if err != nil {
		return err
	}

	var plans []*InstallPlan
	var base string
	var layerSet []string

	target := classifyNodeTarget(node, path)

	// Resolve a kind:local template, when referenced. Template fields
	// (layers + install_opts + env) merge BENEATH deployment-level
	// overrides — so the precedence is CLI > deployment > template.
	// `InstallOptsConfig.ApplyTo` is fill-empty, so calling it with the
	// template's opts after the deployment's leaves the deployment's
	// values intact and only fills the gaps.
	if target == "local" && node != nil && node.Local != "" {
		tmpl := findLocalSpec(dir, node.Local)
		if tmpl == nil {
			return fmt.Errorf("deployment %q: unknown kind:local template %q", path, node.Local)
		}
		// Prepend template layers; deployment add_layers are appended.
		merged := append([]string(nil), tmpl.Layers...)
		merged = append(merged, addLayers...)
		addLayers = merged
		// Fill install_opts gaps from the template.
		opts = tmpl.InstallOpts.ApplyTo(opts)
	}

	// Target-only deploys (local, vm) don't compile primary plans —
	// everything comes from add_layers.
	if target == "local" || target == "vm" {
		base = path
	} else {
		ref, err := ResolveDeployRef(refStr, dir)
		if err != nil {
			return fmt.Errorf("resolving ref %q: %w", refStr, err)
		}
		// Save c.Tag for compilePlans; restore after.
		savedTag := c.Tag
		c.Tag = tag
		plans, base, layerSet, err = c.compilePlans(ref, cfg, distroCfg, builderCfg, dir)
		c.Tag = savedTag
		if err != nil {
			return err
		}
	}

	// For pod/k8s targets the add_layers must compile against the BASE
	// IMAGE's context (distro=fedora, pkg=rpm, etc.) rather than the
	// operator host's context — otherwise the layer's install tasks pick
	// the wrong distro section and the overlay build fails. Only host/vm
	// targets use syntheticHostImage / syntheticVmImage (handled inside
	// compileLayerPlans).
	var baseImg *ResolvedImage
	if (target == "pod" || target == "k8s") && refStr != "" {
		if baseResolved, rerr := cfg.ResolveImage(refStr, tag, dir); rerr == nil {
			baseImg = baseResolved
			if distroCfg != nil {
				baseImg.DistroDef = distroCfg.ResolveDistro(baseImg.Distro)
			}
			if builderCfg != nil {
				baseImg.BuilderConfig = builderCfg
			}
		}
	}
	for _, al := range addLayers {
		alRef, err := ResolveDeployRef(al, dir)
		if err != nil {
			return fmt.Errorf("resolving --add-layer %q: %w", al, err)
		}
		var alPlans []*InstallPlan
		if baseImg != nil {
			alPlans, _, _, err = c.compileLayerPlansWithContext(alRef, cfg, distroCfg, builderCfg, dir, baseImg)
		} else {
			alPlans, _, _, err = c.compilePlans(alRef, cfg, distroCfg, builderCfg, dir)
		}
		if err != nil {
			return fmt.Errorf("compiling --add-layer %q: %w", al, err)
		}
		// Mark each plan's own layer (plus transitive deps) as overlay
		// layers so the Pod target picks them ALL up — not just the
		// user-facing ref name (k3s-server without its k3s base dep).
		overlayNames := make([]string, 0, len(alPlans))
		for _, p := range alPlans {
			if p.Layer != "" {
				overlayNames = append(overlayNames, p.Layer)
			}
		}
		for _, p := range alPlans {
			p.AddLayers = append(p.AddLayers, overlayNames...)
		}
		plans = append(plans, alPlans...)
	}

	deployID := computeDeployID(base, layerSet, addLayers)
	for _, p := range plans {
		p.DeployID = deployID
		// Union — don't clobber. The per-alPlan propagation loop above
		// already populated p.AddLayers with the overlay-layer names
		// (explicit add_layers + their transitive deps). Plain overwrite
		// with the user-facing addLayers list drops the transitive
		// entries, so (e.g.) an overlay declaring add_layers:[k3s-server]
		// would ship k3s-server but not its k3s base layer — runtime
		// failure.
		seen := make(map[string]bool, len(p.AddLayers))
		for _, al := range p.AddLayers {
			seen[al] = true
		}
		for _, al := range addLayers {
			if !seen[al] {
				p.AddLayers = append(p.AddLayers, al)
				seen[al] = true
			}
		}
	}

	if c.DryRun {
		return c.printPlans(plans, opts)
	}

	// Target dispatch. Canonical schema-v3 values: host|vm|pod|k8s.
	// Legacy "container"/"kubernetes" normalized once here so all
	// downstream code uses the new vocabulary.
	switch target {
	case "container":
		target = "pod"
	case "kubernetes":
		target = "k8s"
	}
	switch target {
	case "local":
		return c.runLocal(node, plans, dir, opts)
	case "vm":
		// runVM keys off c.Name to resolve the VM entity.
		//   Schema v3 plain name — node.Vm names the vm entity;
		//                          rewrite c.Name to "vm:<vm_source>"
		//                          for the legacy VM constructor path.
		//   Nested vm child      — path has dots; use leaf.
		//   Legacy user input    — "vm:<name>" already; leave.
		saved := c.Name
		switch {
		case node != nil && node.Vm != "" && !strings.HasPrefix(c.Name, "vm:"):
			c.Name = "vm:" + node.Vm
		case strings.Contains(path, "."):
			c.Name = "vm:" + pathLeaf(path)
		}
		err := c.runVM(plans, dir, opts)
		c.Name = saved
		return err
	case "k8s":
		return c.runK8s(plans, dir, opts)
	case "pod":
		// Pod target (formerly container). runContainer uses c.Name
		// as the pod name; nested pods flatten the dotted path.
		saved := c.Name
		if path != "" {
			c.Name = NestedContainerName(path)
		}
		err := c.runContainer(plans, base, distroCfg, builderCfg, opts)
		c.Name = saved
		return err
	default:
		return fmt.Errorf("unknown target %q; want host|vm|pod|k8s", target)
	}
}

// classifyNodeTarget picks the target discriminator for a node. Uses
// node.Target when non-empty. Returns canonical schema-v3 values
// (host|vm|pod|k8s); legacy "container"/"kubernetes" spellings are
// normalized to pod/k8s for transition compatibility — the `ov
// migrate deploy-v3` command converts them to the canonical values
// on-disk.
//
// For ref-based deploys with no deploy.yml entry (e.g. `ov deploy add
// foo ./image.yml` where foo isn't declared), the deploy name itself
// is the hint: literal `host` → host target; anything else → pod.
// The legacy `vm:<name>` name-prefix heuristic was removed — VM
// deploys are now always tree-backed with explicit target:vm.
func classifyNodeTarget(node *DeploymentNode, path string) string {
	if node != nil && node.Target != "" {
		switch node.Target {
		case "container":
			return "pod"
		case "kubernetes":
			return "k8s"
		case "host":
			// Legacy spelling — schema v4 uses "local". Routed for
			// graceful in-progress migration; the loader rejects
			// authored target:host entries with a hard error pointing
			// at `ov migrate target-local`.
			return "local"
		}
		return node.Target
	}
	if pathLeaf(path) == "host" || pathLeaf(path) == "local" {
		return "local"
	}
	return "pod"
}

// pathLeaf returns the last segment of a dotted path. "foo.bar.baz"
// → "baz"; "foo" → "foo"; "" → "".
func pathLeaf(path string) string {
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// deriveChildExecutorForPath is a small shim over deriveChildExecutor
// that supplies the current node's flattened container name (derived
// from the dotted path) when the node's target is container. Keeps
// the pure-function deriveChildExecutor free of path-awareness.
func deriveChildExecutorForPath(path string, node *DeploymentNode, parentExec DeployExecutor) (DeployExecutor, error) {
	if node == nil {
		return parentExec, nil
	}
	if !node.HasChildren() {
		return parentExec, nil
	}
	switch classifyNodeTarget(node, path) {
	case "local":
		if parentExec != nil {
			return parentExec, nil
		}
		return ShellExecutor{}, nil
	case "pod":
		name := NestedContainerName(path)
		engineJump := JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = JumpDockerExec
		}
		if parentExec == nil {
			parentExec = ShellExecutor{}
		}
		return &NestedExecutor{
			Parent: parentExec,
			Jump:   NestedJump{Kind: engineJump, Target: name},
		}, nil
	case "vm":
		return vmChildExecutor(node, parentExec, path)
	case "k8s":
		return nil, fmt.Errorf("k8s targets cannot have children")
	}
	return parentExec, nil
}

// Run executes `ov deploy del`. Dispatch resolves the deployment node
// (when present in deploy.yml) and routes via the target's Kind().
// Legacy name-prefix routing (`host` literal, `vm:<name>`) still works
// for ref-based deploys without a deploy.yml entry.
func (c *DeployDelCmd) Run() error {
	paths, err := DefaultLedgerPaths()
	if err != nil {
		return err
	}
	lock, err := AcquireLedgerLock(paths)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Resolve target-kind: first check the deploy.yml tree for an
	// explicit target:, else fall back to the name-prefix heuristic.
	kind := c.resolveDelTargetKind()
	switch kind {
	case "local", "host":
		return c.runLocalDel(paths)
	case "vm":
		if !strings.HasPrefix(c.Name, "vm:") {
			// Schema v3: plain identifier — find the matching node's
			// VmSource so runVmDel's legacy parseVmDeployName path works.
			if cwd, _ := os.Getwd(); cwd != "" {
				if tree, _ := resolveTreeRoot(cwd); tree != nil {
					if node, ok := tree[c.Name]; ok && node.Vm != "" {
						saved := c.Name
						c.Name = "vm:" + node.Vm
						err := c.runVmDel(paths)
						c.Name = saved
						return err
					}
				}
			}
		}
		return c.runVmDel(paths)
	case "pod":
		return c.runContainerDel(paths)
	case "k8s":
		return c.runK8sDel(paths)
	default:
		return c.runContainerDel(paths)
	}
}

// resolveDelTargetKind returns the canonical schema-v3 target kind
// (host|vm|pod|k8s) for c.Name, using the deploy.yml tree when
// available. Fallback: legacy name-prefix heuristic.
func (c *DeployDelCmd) resolveDelTargetKind() string {
	if c.Name == "host" {
		return "host"
	}
	if strings.HasPrefix(c.Name, "vm:") {
		return "vm"
	}
	cwd, _ := os.Getwd()
	if cwd != "" {
		if tree, _ := resolveTreeRoot(cwd); tree != nil {
			if node, ok := tree[c.Name]; ok && node.Target != "" {
				switch node.Target {
				case "container":
					return "pod"
				case "kubernetes":
					return "k8s"
				}
				return node.Target
			}
		}
	}
	return "pod"
}

// runLocalDel is a thin wrapper that constructs a LocalUnifiedTarget
// with this cmd's gate flags and delegates teardown to the unified
// target's Del method (see unified_targets_host.go). The body lives on
// LocalUnifiedTarget.Del so future schema-v3 dispatchers can call into
// the same logic without going through DeployDelCmd.
func (c *DeployDelCmd) runLocalDel(paths *LedgerPaths) error {
	target := &LocalUnifiedTarget{
		NodeName:        c.Name,
		KeepRepoChanges: c.KeepRepoChanges,
		KeepServices:    c.KeepServices,
		RevRunner:       c.Runner,
	}
	return target.Del(context.Background(), DelOpts{
		DryRun:    c.DryRun,
		AssumeYes: c.AssumeYes,
	})
}

// runContainerDel is a thin wrapper that constructs a PodUnifiedTarget
// with this cmd's gate flags and delegates teardown to the unified
// target's Del method (see unified_targets_pod.go).
func (c *DeployDelCmd) runContainerDel(paths *LedgerPaths) error {
	target := &PodUnifiedTarget{
		NodeName:  c.Name,
		KeepImage: c.KeepImage,
	}
	return target.Del(context.Background(), DelOpts{
		DryRun:    c.DryRun,
		AssumeYes: c.AssumeYes,
	})
}

// findContainerDeploy locates the deploy record with matching Target.
// Accepts both schema-v3 ("pod:<name>") and legacy ("container:<name>")
// keying; Phase 6 migration rewrites existing records to the new form.
func findContainerDeploy(paths *LedgerPaths, name string) (*DeployRecord, error) {
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	wantPod := "pod:" + name
	wantContainer := "container:" + name
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(paths.Deploys, e.Name()))
		if err != nil {
			continue
		}
		var rec DeployRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		if rec.Target == wantPod || rec.Target == wantContainer {
			return &rec, nil
		}
	}
	return nil, nil
}

// runPodmanCommand invokes the given podman subcommand, capturing
// errors via the command's exit status but returning nil for
// idempotent commands (e.g. rmi of a non-existent image shouldn't
// fail the teardown).
func runPodmanCommand(engine string, args ...string) error {
	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (c *DeployAddCmd) emitOpts() EmitOpts {
	return EmitOpts{
		DryRun:               c.DryRun,
		FormatJSON:           c.Format == "json",
		AllowRepoChanges:     c.AllowRepoChanges,
		AllowRootTasks:       c.AllowRootTasks,
		WithServices:         c.WithServices,
		SkipIncompatible:     c.SkipIncompatible,
		AssumeYes:            c.AssumeYes,
		Verify:               c.Verify,
		Pull:                 c.Pull,
		BuilderImageOverride: c.BuilderImage,
	}
}

// compilePlans resolves the ref to a layer set and builds plans for
// each. For image refs: walk the image's Layers in topological order.
// For layer refs: compile a single plan. For remote refs: fetch and
// proceed (remote fetch is handled by existing EnsureRepoDownloaded).
func (c *DeployAddCmd) compilePlans(ref *DeployRef, cfg *Config, distroCfg *DistroConfig, builderCfg *BuilderConfig, dir string) ([]*InstallPlan, string, []string, error) {
	if ref.Source == RefSourceRemote {
		// Remote fetch: deferred. Returning a typed error keeps the
		// surface clean for tests + the command help output.
		return nil, "", nil, fmt.Errorf("remote refs not yet fetched by DeployAddCmd (layer=%s)", ref.Name)
	}
	if ref.Kind == RefKindImage {
		return c.compileImagePlans(ref, cfg, distroCfg, builderCfg, dir)
	}
	return c.compileLayerPlans(ref, cfg, distroCfg, builderCfg, dir)
}

func (c *DeployAddCmd) compileImagePlans(ref *DeployRef, cfg *Config, distroCfg *DistroConfig, builderCfg *BuilderConfig, dir string) ([]*InstallPlan, string, []string, error) {
	_ = distroCfg
	_ = builderCfg
	img, err := cfg.ResolveImage(ref.Name, c.Tag, dir)
	if err != nil {
		return nil, "", nil, err
	}
	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, "", nil, err
	}
	var parent map[string]bool
	order, err := ResolveLayerOrder(img.Layers, layers, parent)
	if err != nil {
		return nil, "", nil, err
	}
	var plans []*InstallPlan
	hostCtx := detectHostContext()
	for _, layerName := range order {
		layer := layers[layerName]
		if layer == nil {
			continue
		}
		p, err := BuildDeployPlan(layer, img, hostCtx)
		if err != nil {
			return nil, "", nil, fmt.Errorf("compiling %s: %w", layerName, err)
		}
		plans = append(plans, p)
	}
	return plans, img.Name, order, nil
}

// compileLayerPlansWithContext is the same as compileLayerPlans but uses
// the provided *ResolvedImage as the compile context (so add_layers for
// a pod/k8s deployment compile against the base image's distro/user
// context, not the operator host's).
func (c *DeployAddCmd) compileLayerPlansWithContext(ref *DeployRef, cfg *Config, distroCfg *DistroConfig, builderCfg *BuilderConfig, dir string, ctx *ResolvedImage) ([]*InstallPlan, string, []string, error) {
	_ = builderCfg
	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, "", nil, err
	}
	if _, ok := layers[ref.Name]; !ok {
		return nil, "", nil, fmt.Errorf("layer %q not found", ref.Name)
	}
	order, err := ResolveLayerOrder([]string{ref.Name}, layers, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolving deps for %s: %w", ref.Name, err)
	}
	if distroCfg != nil && ctx.DistroDef == nil {
		ctx.DistroDef = distroCfg.ResolveDistro(ctx.Distro)
	}
	if builderCfg != nil && ctx.BuilderConfig == nil {
		ctx.BuilderConfig = builderCfg
	}
	hostCtx := detectHostContext()
	var plans []*InstallPlan
	for _, name := range order {
		p, err := BuildDeployPlan(layers[name], ctx, hostCtx)
		if err != nil {
			return nil, "", nil, fmt.Errorf("compiling %s: %w", name, err)
		}
		plans = append(plans, p)
	}
	return plans, ref.Name, order, nil
}

func (c *DeployAddCmd) compileLayerPlans(ref *DeployRef, cfg *Config, distroCfg *DistroConfig, builderCfg *BuilderConfig, dir string) ([]*InstallPlan, string, []string, error) {
	_ = builderCfg
	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, "", nil, err
	}
	if _, ok := layers[ref.Name]; !ok {
		return nil, "", nil, fmt.Errorf("layer %q not found", ref.Name)
	}
	// Expand transitive deps — a layer deploy (either a bare
	// `ov deploy add <layer>` or a `--add-layer <name>`) MUST pull in
	// the layer's `depends:` graph in topological order. Without this,
	// layers whose build-time tasks rely on upstream binaries (e.g.
	// `pre-commit`'s `cargo install` requiring `rust`) fail at first
	// execution with "command not found" errors. Feeding the requested
	// layer through ResolveLayerOrder matches what compileImagePlans
	// does for image-level deploys.
	order, err := ResolveLayerOrder([]string{ref.Name}, layers, nil)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolving deps for %s: %w", ref.Name, err)
	}
	// Pick the synthetic image template that matches the deploy target
	// so `${USER}` in layer tasks resolves correctly: guest user for
	// vm:<name>, host user for host/other targets.
	var img *ResolvedImage
	if strings.HasPrefix(c.Name, "vm:") {
		if vmName, perr := vmNameFromDeployName(c.Name); perr == nil {
			if uf, ok, _ := LoadUnified(dir); ok && uf != nil && uf.VM != nil {
				if spec, present := uf.VM[vmName]; present {
					img = syntheticVmImage(spec)
				}
			}
		}
	}
	if img == nil {
		img = syntheticHostImage()
	}
	hostCtx := detectHostContext()
	if distroCfg != nil {
		img.DistroDef = distroCfg.ResolveDistro(img.Distro)
	}
	if builderCfg != nil {
		img.BuilderConfig = builderCfg
	}
	var plans []*InstallPlan
	for _, name := range order {
		p, err := BuildDeployPlan(layers[name], img, hostCtx)
		if err != nil {
			return nil, "", nil, fmt.Errorf("compiling %s: %w", name, err)
		}
		plans = append(plans, p)
	}
	return plans, ref.Name, order, nil
}

func (c *DeployAddCmd) printPlans(plans []*InstallPlan, opts EmitOpts) error {
	if opts.FormatJSON {
		return json.NewEncoder(os.Stdout).Encode(plans)
	}
	for _, p := range plans {
		fmt.Println(DescribePlan(p))
	}
	return nil
}

// runLocal executes a target:local deployment. The destination is
// selected by node.Host (Ansible-style):
//   - "" or "local"   → ShellExecutor (direct local shell)
//   - anything else   → SSHExecutor (ssh(1) reads ~/.ssh/config + agent)
//
// The optional `local: <name>` field references a kind:local template
// whose layers + install_opts + env merge with this deployment's
// overrides via the standard 3-tier precedence (CLI > deployment >
// template). Nested local nodes inherit opts.ParentExec.
func (c *DeployAddCmd) runLocal(node *DeploymentNode, plans []*InstallPlan, dir string, opts EmitOpts) error {
	hostDistro, _ := DetectHostDistro()
	tgt := &LocalDeployTarget{
		HostHome: os.Getenv("HOME"),
		Distro:   hostDistro,
	}
	// Pick the executor.
	var exec DeployExecutor = ShellExecutor{}
	switch {
	case opts.ParentExec != nil:
		// Nested local-target inside a container/VM — run through the
		// parent's venue.
		tgt.Executor = opts.ParentExec
		exec = opts.ParentExec
	case node != nil:
		hostField := strings.TrimSpace(node.Host)
		if hostField != "" && hostField != "local" {
			sshTarget, perr := ParseSSHTarget(hostField)
			if perr != nil {
				return fmt.Errorf("deployment %q: invalid host %q: %w", c.Name, hostField, perr)
			}
			user := ""
			if strings.Contains(hostField, "@") {
				user = sshTarget.User
			} else if node.User != "" {
				user = node.User
			}
			sshExec := &SSHExecutor{
				User:           user,
				Host:           sshTarget.Host,
				Port:           sshTarget.Port,
				Args:           append([]string(nil), node.SSHArgs...),
				ConnectTimeout: 10,
			}
			tgt.Executor = sshExec
			exec = sshExec
		}
	}

	// Resolve layer secret_requires / secret_accepts and inject them
	// into each TaskStep's env BEFORE emission. Missing required
	// secrets fail the deploy immediately (R1).
	layerList, err := LayersForPlans(plans, dir, nil)
	if err != nil {
		return fmt.Errorf("loading layers for secret resolution: %w", err)
	}
	secretEnv, missing := ResolveSecretsForLayers(layerList)
	if err := FormatMissingSecretsError(missing); err != nil {
		return err
	}
	InjectSecretsIntoPlans(plans, secretEnv)

	// Collect env for artifact substitution — merges resolved secrets +
	// any deploy.yml env: entries on this node.
	artifactEnv := map[string]string{}
	for k, v := range secretEnv {
		artifactEnv[k] = v
	}
	if dc, _ := LoadDeployConfig(); dc != nil {
		if entry, exists := dc.Deployment[c.Name]; exists {
			for _, line := range entry.Env {
				if idx := strings.Index(line, "="); idx > 0 {
					artifactEnv[line[:idx]] = line[idx+1:]
				}
			}
		}
	}

	if err := tgt.Emit(plans, opts); err != nil {
		return err
	}

	// Retrieve layer artifacts (files the layer publishes back — e.g.
	// kubeconfig from a k3s-server layer). Ignored when opts.DryRun.
	if !opts.DryRun {
		if err := RetrieveLayerArtifacts(context.Background(), exec, layerList, sanitizeDeployName(c.Name), artifactEnv, opts); err != nil {
			return fmt.Errorf("retrieving layer artifacts: %w", err)
		}
		// k3s-server post-hook: merge retrieved kubeconfig into
		// ~/.kube/config and write a ClusterProfile so the new cluster
		// is immediately usable as an `ov deploy add --target kubernetes`
		// destination. No-op when k3s-server isn't in the layer list.
		if deployHasLayer(layerList, "k3s-server") {
			if err := K3sPostProvision(c.Name); err != nil {
				return fmt.Errorf("k3s post-provision: %w", err)
			}
		}
	}
	return nil
}

func (c *DeployAddCmd) runContainer(plans []*InstallPlan, base string, distroCfg *DistroConfig, builderCfg *BuilderConfig, opts EmitOpts) error {
	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering).
	// When the deploy is marked ephemeral, register the systemd
	// transient timer + parent-detection metadata BEFORE any container
	// build / quadlet emission. Pod-target ephemerals don't have a
	// snapshot refcount (containers don't have backing chains), so
	// the helper handles only timer + parent linkage in this path.
	if dc, _ := LoadDeployConfig(); dc != nil {
		if node, ok := dc.Deployment[c.Name]; ok && node.IsEphemeral() {
			if _, regErr := RegisterEphemeralLifecycle(&node, c.Name); regErr != nil {
				fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
			}
		}
	}

	// Only the overlay build piece is implemented v1; the final
	// container start (volumes, quadlet, traefik) is still routed
	// through the existing `ov start` command.
	// Build a Generator + ResolvedImage so the overlay's OCITarget can
	// render tasks as RUN directives (not comments). Without these the
	// overlay image would be byte-identical to the base image.
	dir, _ := os.Getwd()
	gen, _ := NewGenerator(dir, c.Tag)
	var resolvedImg *ResolvedImage
	if gen != nil && gen.Images != nil {
		resolvedImg = gen.Images[base]
	}
	// Resolve DistroDef from the BASE IMAGE's distro, not the operator
	// host's. The overlay's SystemPackagesSteps render using the base
	// image's package format (e.g. fedora → rpm for a fedora-ov-based
	// overlay). Using the host distro produces "no distro definition
	// for format rpm" when the operator runs on arch but the base
	// image is fedora.
	var podDistroDef *DistroDef
	if resolvedImg != nil && len(resolvedImg.Distro) > 0 {
		podDistroDef = resolveDistroDef(distroCfg, resolvedImg.Distro[0])
	} else {
		// Fallback to host context only when the base image's distro
		// is unknown (shouldn't happen for any well-formed image).
		podDistroDef = resolveDistroDef(distroCfg, detectHostContext().Distro)
	}
	// Build the BaseImage ref. With CalVer-only resolution, c.Tag
	// defaults to empty — in that case resolve the newest local
	// CalVer for this short name so the overlay Containerfile's
	// `FROM <ref>` line gets a real tag (never a trailing colon).
	var baseRef string
	if c.Tag != "" {
		baseRef = base + ":" + c.Tag
	} else {
		// ResolveNewestLocalCalVer returns the full "<registry>/<name>:<calver>"
		// ref; fall back to bare short name on miss (EnsureImage will
		// surface the error with a CalVer-specification hint).
		engineForLookup := "podman"
		if resolvedImg != nil && resolvedImg.Registry != "" {
			engineForLookup = "podman"
		}
		if resolved, err := ResolveNewestLocalCalVer(engineForLookup, base); err == nil && resolved != "" {
			baseRef = resolved
		} else {
			baseRef = base
		}
	}
	tgt := &PodDeployTarget{
		DeployName:    c.Name,
		BaseImage:     baseRef,
		DistroDef:     podDistroDef,
		BuilderConfig: builderCfg,
		Generator:     gen,
		Image:         resolvedImg,
	}
	// Resolve + inject layer secrets (secret_requires) so the overlay
	// Containerfile emits `export VAR=VALUE` before each task's bash
	// body. Without this, layers like k3s-server fail at build with
	// "K3S_CLUSTER_TOKEN: unbound variable". Mirrors the runHost /
	// runVM injection paths.
	layerList, err := LayersForPlans(plans, dir, nil)
	if err != nil {
		return fmt.Errorf("loading layers for secret resolution: %w", err)
	}
	secretEnv, missing := ResolveSecretsForLayers(layerList)
	if err := FormatMissingSecretsError(missing); err != nil {
		return err
	}
	InjectSecretsIntoPlans(plans, secretEnv)
	// Thread ParentExec: when this container is a child of another
	// deployment, the overlay build (if any) must run in the parent's
	// venue. The target's own check rejects the combo with a clear
	// error for cases we haven't wired yet (build-context transfer).
	if opts.ParentExec != nil {
		tgt.Executor = opts.ParentExec
	}
	if err := tgt.Emit(plans, opts); err != nil {
		return err
	}
	// Persist classification flags into deploy.yml when the user
	// passed --disposable / --lifecycle. saveDeployState merges onto
	// any existing entry; SetDisposable / SetLifecycle gate whether
	// we actually write each field (so an unrelated code path can't
	// silently clear a prior explicit opt-in).
	if c.Disposable || c.Lifecycle != "" {
		saveDeployState(c.Name, "", SaveDeployStateInput{
			SetDisposable: c.Disposable,
			Disposable:    c.Disposable,
			SetLifecycle:  c.Lifecycle != "",
			Lifecycle:     c.Lifecycle,
		})
		if c.Disposable {
			fmt.Fprintln(os.Stderr, "Marked deploy disposable — `ov rebuild` will act unattended on this deploy.")
		}
	}
	fmt.Printf("Overlay image ready: %s\n", tgt.OverlayImageRef())
	fmt.Println("To start the container, run: ov start " + c.Name)
	return nil
}

// ---------------------------------------------------------------------------
// Small glue helpers.
// ---------------------------------------------------------------------------

// detectHostContext builds the HostContext struct used by the compiler
// for host-target deploys. Returns a zero-value struct for container
// deploys (the compiler ignores host-only fields there).
func detectHostContext() HostContext {
	hd, _ := DetectHostDistro()
	glibc, _ := DetectHostGlibc()
	if hd == nil {
		return HostContext{}
	}
	return HostContext{
		Target:       "host",
		Distro:       hd.PrimaryTag(),
		GlibcVersion: glibc,
	}
}

// syntheticHostImage returns a minimal ResolvedImage suitable for
// compiling a single-layer plan against the host. Used when the user
// invokes `ov deploy add host <layer-ref>` without a containing image.
func syntheticHostImage() *ResolvedImage {
	hd, _ := DetectHostDistro()
	img := &ResolvedImage{
		Name:         "host-adhoc",
		Home:         os.Getenv("HOME"),
		User:         os.Getenv("USER"),
		BuildFormats: []string{},
	}
	if hd != nil {
		img.Distro = append(img.Distro, hd.Tags...)
		if hint := hd.FormatHint(); hint != "" {
			img.Pkg = hint
			img.BuildFormats = []string{hint}
		}
	}
	return img
}

// syntheticVmImage returns a ResolvedImage tuned for `ov deploy add
// vm:<name>` — the User/UID/GID/Home fields come from the VM spec's SSH
// config (not the host's env), so `${USER}` in a layer's `user:` field
// resolves to the GUEST user (e.g. `arch`) and task scope classification
// dispatches user-scoped tasks to RunUser (bare ssh bash -s) instead of
// RunSystem (ssh sudo bash -s). Without this, `cargo install taplo-cli`
// under the pre-commit layer ends up in /root/.cargo/bin/ instead of
// /home/<user>/.cargo/bin/, and $HOME-anchored layer tests fail.
//
// Cloud-image VMs conventionally use uid/gid 1000 for the first non-root
// user (cloud-init's adopt path respects that). bootc VMs default to
// root, in which case we fall back to the same syntheticHostImage()
// semantics (System scope, no per-user path).
func syntheticVmImage(spec *VmSpec) *ResolvedImage {
	user := resolveVmSshUser(spec)
	if user == "" || user == "root" {
		img := syntheticHostImage()
		img.Name = "vm-adhoc"
		img.User = "root"
		img.Home = "/root"
		return img
	}
	img := &ResolvedImage{
		Name:         "vm-adhoc",
		User:         user,
		UID:          1000,
		GID:          1000,
		Home:         "/home/" + user,
		Distro:       []string{"archlinux"}, // cloud_image today is arch; extend when more VM distros land.
		Pkg:          "pac",
		BuildFormats: []string{"pac"},
	}
	return img
}

// resolveDistroDef returns the DistroDef for a given distro tag.
func resolveDistroDef(cfg *DistroConfig, distroTag string) *DistroDef {
	if cfg == nil || distroTag == "" {
		return nil
	}
	return cfg.ResolveDistro([]string{distroTag})
}

// loadConfigForDeploy loads image.yml + build.yml for the current
// project directory. Runs SetFormatNames as a side effect since the
// layer scanner needs it.
func loadConfigForDeploy(dir string) (*Config, *DistroConfig, *BuilderConfig, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	SetFormatNames(distroCfg)
	return cfg, distroCfg, builderCfg, nil
}

var _ = context.Background // silence "imported and not used" if future work removes the Background ref
