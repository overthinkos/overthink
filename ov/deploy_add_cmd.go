package main

// deploy_add_cmd.go — `ov deploy add <name> [<ref>]` and
// `ov deploy del <name>`. Thin wiring on top of the pieces already
// built: BuildDeployPlan → {OCITarget, HostDeployTarget,
// ContainerDeployTarget}.
//
// Name semantics:
//   - literal "host" → deploy to the local machine via HostDeployTarget
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
	Tag     string `long:"tag" default:"latest" help:"Image tag"`
	DryRun  bool   `long:"dry-run" help:"Print the plan without executing"`
	Format  string `long:"format" default:"table" enum:"table,json" help:"Output format for --dry-run"`
	Pull    bool   `long:"pull" help:"Force re-fetch of remote refs / image pull"`
	Verify  bool   `long:"verify" help:"Re-run layer tests: on the host after install"`

	// Host-only gates.
	WithServices       bool   `long:"with-services" help:"Install systemd services (host target only)"`
	AllowRepoChanges   bool   `long:"allow-repo-changes" help:"Allow repo config mutations (host target only)"`
	AllowRootTasks     bool   `long:"allow-root-tasks" help:"Allow arbitrary root cmd: tasks (host target only)"`
	SkipIncompatible   bool   `long:"skip-incompatible" help:"Skip layers without host-matching format (host target only)"`
	BuilderImage       string `long:"builder-image" help:"Override the compile builder image"`
	AssumeYes          bool   `long:"yes" short:"y" help:"Assume yes; implies all allow-* gates plus skip sudo preflight"`

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

	AssumeYes        bool `long:"yes" short:"y" help:"Skip confirmation prompts"`
	KeepRepoChanges  bool `long:"keep-repo-changes" help:"Don't revert repo config even at zero refcount"`
	KeepServices     bool `long:"keep-services" help:"Don't disable systemd units (just stop tracking)"`
	KeepImage        bool `long:"keep-image" help:"Don't remove the synthesized overlay image (container target only)"`
	DryRun           bool `long:"dry-run" help:"Print the teardown plan without executing"`

	// Runner is populated by runVmDel / runHostDel etc. to route reverse
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
	if tree != nil {
		if n, _, nodeErr := ResolveNodePath(tree, targetPath); nodeErr == nil {
			rootNode = n
			resolvedPath = targetPath
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

	return WalkDeploymentTree(resolvedPath, rootNode, nil, func(path string, node *DeploymentNode, parentExec DeployExecutor) (DeployExecutor, error) {
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
		// Entry-driven: the deploy key IS the ref (by convention).
		// Per-node ref could be introduced later via `image:` field.
		refStr = pathLeaf(path)
	}

	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(dir)
	if err != nil {
		return err
	}

	var plans []*InstallPlan
	var base string
	var layerSet []string

	target := classifyNodeTarget(node, path)

	// Target-only deploys (host, vm) don't compile primary plans —
	// everything comes from add_layers.
	if target == "host" || target == "vm" {
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

	for _, al := range addLayers {
		alRef, err := ResolveDeployRef(al, dir)
		if err != nil {
			return fmt.Errorf("resolving --add-layer %q: %w", al, err)
		}
		alPlans, _, _, err := c.compilePlans(alRef, cfg, distroCfg, builderCfg, dir)
		if err != nil {
			return fmt.Errorf("compiling --add-layer %q: %w", al, err)
		}
		plans = append(plans, alPlans...)
	}

	deployID := computeDeployID(base, layerSet, addLayers)
	for _, p := range plans {
		p.DeployID = deployID
		p.AddLayers = append([]string(nil), addLayers...)
	}

	if c.DryRun {
		return c.printPlans(plans, opts)
	}

	// Target dispatch. Use node.Target as the canonical source
	// when present; fall back to the legacy name-prefix heuristic
	// for refs without deploy.yml entries.
	switch target {
	case "host":
		return c.runHost(plans, dir, distroCfg, opts)
	case "vm":
		// runVM keys off c.Name to resolve the VM entity (the prefix
		// "vm:" is a target-type tag, not part of the vm name).
		//
		//   Root vm deploy   — c.Name is already "vm:<name>" as typed
		//                      by the user; leave untouched.
		//   Nested vm child  — path looks like "stack.myvm"; c.Name
		//                      must be rewritten to "vm:<leaf>" so
		//                      parseVmDeployName finds the entity.
		saved := c.Name
		if strings.Contains(path, ".") {
			c.Name = "vm:" + pathLeaf(path)
		}
		err := c.runVM(plans, dir, opts)
		c.Name = saved
		return err
	case "kubernetes":
		return fmt.Errorf("target=kubernetes dispatch via tree walker not yet wired — use `ov deploy add --target kubernetes` directly")
	default:
		// container target. runContainer uses c.Name as the
		// container name; for nested containers we flatten the
		// dotted path into a podman-legal name.
		saved := c.Name
		if path != "" {
			c.Name = NestedContainerName(path)
		}
		err := c.runContainer(plans, base, distroCfg, builderCfg, opts)
		c.Name = saved
		return err
	}
}

// classifyNodeTarget picks the target discriminator for a node. Uses
// node.Target when non-empty, else falls back to the legacy path-
// prefix conventions (`host` literal root, `vm:` prefix) to preserve
// ref-based deploys that have no deploy.yml entry.
func classifyNodeTarget(node *DeploymentNode, path string) string {
	if node != nil && node.Target != "" {
		return node.Target
	}
	// Legacy name-prefix inference.
	leaf := pathLeaf(path)
	if leaf == "host" {
		return "host"
	}
	if strings.HasPrefix(leaf, "vm:") {
		return "vm"
	}
	return "container"
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
	case "host":
		if parentExec != nil {
			return parentExec, nil
		}
		return LocalDeployExecutor{}, nil
	case "container":
		name := NestedContainerName(path)
		engineJump := JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = JumpDockerExec
		}
		if parentExec == nil {
			parentExec = LocalDeployExecutor{}
		}
		return &NestedExecutor{
			Parent: parentExec,
			Jump:   NestedJump{Kind: engineJump, Target: name},
		}, nil
	case "vm":
		return vmChildExecutor(node, parentExec)
	case "kubernetes":
		return nil, fmt.Errorf("kubernetes targets cannot have children")
	}
	return parentExec, nil
}

// Run executes `ov deploy del`.
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

	switch {
	case c.Name == "host":
		return c.runHostDel(paths)
	case strings.HasPrefix(c.Name, "vm:"):
		return c.runVmDel(paths)
	default:
		return c.runContainerDel(paths)
	}
}

// runHostDel tears down host deploys: runs each ReverseOp, removes
// ledger entries and (for layers whose refcount drops to zero) cleans
// up env.d files and shell-profile managed blocks.
func (c *DeployDelCmd) runHostDel(paths *LedgerPaths) error {
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No deployments recorded.")
			return nil
		}
		return err
	}

	hostHome := os.Getenv("HOME")
	anyRemoved := false

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
		if rec.Target != "host" {
			continue
		}
		if c.DryRun {
			fmt.Printf("[dry-run] would tear down host deploy %s (image=%s, %d layers)\n",
				rec.DeployID, rec.Image, len(rec.Layers))
			continue
		}
		if err := c.tearDownDeploy(paths, &rec, hostHome); err != nil {
			return err
		}
		anyRemoved = true
		fmt.Printf("Removed host deploy %s (%s)\n", rec.DeployID, rec.Image)
	}

	// If nothing is deployed anymore, strip the shell managed block.
	if anyRemoved && !c.DryRun {
		if remainingLayers, _ := os.ReadDir(paths.Layers); len(remainingLayers) == 0 {
			shell := DetectLoginShell()
			_ = RemoveManagedBlock(shell, hostHome)
		}
	}
	return nil
}

// tearDownDeploy reverses a single host deploy record: for each layer
// in the deploy, decrement its refcount; if the layer's refcount drops
// to zero, run its ReverseOps, delete its env.d file, delete its
// ledger entry.
func (c *DeployDelCmd) tearDownDeploy(paths *LedgerPaths, rec *DeployRecord, hostHome string) error {
	for _, layer := range rec.Layers {
		layerRec, shouldRemove, err := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return err
		}
		if !shouldRemove {
			continue
		}
		// Execute the ReverseOps for this layer.
		if err := runReverseOps(layerRec.ReverseOps, c); err != nil {
			return fmt.Errorf("reversing layer %s: %w", layer, err)
		}
		// Remove the env.d file (always, regardless of ReverseOps).
		_ = RemoveEnvdFile(hostHome, layer)
		// Delete the layer ledger.
		if err := DeleteLayerRecord(paths, layer); err != nil {
			return err
		}
	}
	return DeleteDeployRecord(paths, rec.DeployID)
}

// runContainerDel stops + removes the container deploy, removes the
// overlay image (unless --keep-image), and cleans up the ledger entry.
func (c *DeployDelCmd) runContainerDel(paths *LedgerPaths) error {
	rec, err := findContainerDeploy(paths, c.Name)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("no container deploy named %q in ledger", c.Name)
	}
	if c.DryRun {
		fmt.Printf("[dry-run] would stop container %s, remove image %s (keep=%v)\n",
			c.Name, rec.Image, c.KeepImage)
		return nil
	}
	// Stop + remove the container via podman.
	engine := "podman"
	_ = runPodmanCommand(engine, "stop", c.Name)
	_ = runPodmanCommand(engine, "rm", "-f", c.Name)

	// Remove the overlay image if any was recorded and --keep-image not set.
	overlayRef := rec.Image
	if !c.KeepImage && strings.HasSuffix(overlayRef, "-overlay") {
		_ = runPodmanCommand(engine, "rmi", overlayRef)
	}

	// Decrement refcounts on each included layer (same as host del).
	for _, layer := range rec.Layers {
		_, shouldRemove, err := RemoveLayerDeployment(paths, layer, rec.DeployID)
		if err != nil {
			return err
		}
		if shouldRemove {
			_ = DeleteLayerRecord(paths, layer)
		}
	}

	// Remove deploy record.
	if err := DeleteDeployRecord(paths, rec.DeployID); err != nil {
		return err
	}
	fmt.Printf("Removed container deploy %s\n", c.Name)
	return nil
}

// findContainerDeploy locates the deploy record with matching Target
// (Target is "container:<name>").
func findContainerDeploy(paths *LedgerPaths, name string) (*DeployRecord, error) {
	entries, err := os.ReadDir(paths.Deploys)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	want := "container:" + name
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
		if rec.Target == want {
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
		if vmName, _, perr := parseVmDeployName(c.Name); perr == nil {
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

func (c *DeployAddCmd) runHost(plans []*InstallPlan, dir string, distroCfg *DistroConfig, opts EmitOpts) error {
	_ = distroCfg
	_ = dir
	hostDistro, _ := DetectHostDistro()
	tgt := &HostDeployTarget{
		HostHome: os.Getenv("HOME"),
		Distro:   hostDistro,
	}
	// When this host deploy is nested inside a container or VM, the
	// tree walker sets opts.ParentExec to the parent's executor. The
	// HostDeployTarget runs all its bash primitives through that
	// executor instead of the local shell — so "apply these layers
	// inside the parent container/VM" works without a different target
	// type.
	if opts.ParentExec != nil {
		tgt.Executor = opts.ParentExec
	}
	return tgt.Emit(plans, opts)
}

func (c *DeployAddCmd) runContainer(plans []*InstallPlan, base string, distroCfg *DistroConfig, builderCfg *BuilderConfig, opts EmitOpts) error {
	// Only the overlay build piece is implemented v1; the final
	// container start (volumes, quadlet, traefik) is still routed
	// through the existing `ov start` command.
	tgt := &ContainerDeployTarget{
		DeployName:    c.Name,
		BaseImage:     base + ":" + c.Tag,
		DistroDef:     resolveDistroDef(distroCfg, detectHostContext().Distro),
		BuilderConfig: builderCfg,
	}
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
	_ = cfg // FormatConfig deprecated — unified loader reads overthink.yml directly.
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	SetFormatNames(distroCfg)
	return cfg, distroCfg, builderCfg, nil
}

var _ = context.Background // silence "imported and not used" if future work removes the Background ref
