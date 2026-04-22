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
func (c *DeployAddCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	opts := c.emitOpts()

	// Resolve the primary ref. If none provided on the command line,
	// fall back to the matching entry in deploy.yml (keyed by deploy
	// name). This lets users pre-populate deploy.yml and then run
	// `ov deploy add host` (or `ov deploy add my-dev`) with no args.
	//
	// Two-tier deploy-config lookup: project deploy.yml (authored in the
	// repo, shipped via `overthink.yml deployments:`) provides the
	// canonical declaration — target, vm_source, add_layers, tests.
	// Per-machine ~/.config/ov/deploy.yml overlays machine-specific state
	// — ports, secrets, VmDeployState, host-local tests. MergeDeployConfigs
	// is left-to-right: local wins on field overlap.
	refStr := c.Ref
	var deployEntry *DeployImageConfig
	if refStr == "" {
		var projectDC *DeployConfig
		if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
			projectDC = uf.ProjectDeployConfig()
		}
		localDC, _ := LoadDeployConfig()
		merged := MergeDeployConfigs(projectDC, localDC)
		if merged != nil {
			if entry, ok := merged.Images[c.Name]; ok {
				deployEntry = &entry
				if entry.Version != "" {
					c.Tag = entry.Version
				}
				if entry.InstallOpts != nil {
					opts = entry.InstallOpts.ApplyTo(opts)
				}
				if len(c.AddLayer) == 0 && len(entry.AddLayers) > 0 {
					c.AddLayer = append([]string(nil), entry.AddLayers...)
				}
			}
		}
		if deployEntry == nil {
			return fmt.Errorf("ov deploy add: no <ref> and deploy.yml has no entry for %q", c.Name)
		}
		// The entry's image/name is what we actually deploy. deploy.yml
		// keys the entry by deploy name; the target image name lives
		// outside (use c.Name as a fallback when the entry lacks an
		// explicit image pointer — for now we use the deploy key).
		refStr = c.Name
	}

	// Load the project config so we can compile plans against resolved
	// distro/builder definitions.
	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(dir)
	if err != nil {
		return err
	}

	var plans []*InstallPlan
	var base string
	var layerSet []string

	// Compile per-layer plans. The strategy differs by ref kind:
	//   - image ref                → one plan per layer in the image
	//   - layer ref                → a single plan
	//   - `host` / `vm:<name>`     → no primary plan; all layers come from
	//                                 add_layers (compiled in the loop below)
	if c.Name == "host" || strings.HasPrefix(c.Name, "vm:") {
		// Target-only deploy: no primary image/layer ref. The entry's
		// add_layers (merged from project + local deploy.yml) carries the
		// full install payload.
		base = c.Name
	} else {
		ref, err := ResolveDeployRef(refStr, dir)
		if err != nil {
			return fmt.Errorf("resolving ref %q: %w", refStr, err)
		}
		plans, base, layerSet, err = c.compilePlans(ref, cfg, distroCfg, builderCfg, dir)
		if err != nil {
			return err
		}
	}

	// Merge add_layers: on top (if any). Each --add-layer is resolved
	// independently and contributes one per-layer plan appended to the
	// plan slice.
	for _, al := range c.AddLayer {
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

	// Stamp every plan with the deploy-id + add_layers provenance.
	deployID := computeDeployID(base, layerSet, c.AddLayer)
	for _, p := range plans {
		p.DeployID = deployID
		p.AddLayers = append([]string(nil), c.AddLayer...)
	}

	// Dry-run path: print the plans and exit.
	if c.DryRun {
		return c.printPlans(plans, opts)
	}

	// Dispatch to the chosen target.
	switch {
	case c.Name == "host":
		return c.runHost(plans, dir, distroCfg, opts)
	case strings.HasPrefix(c.Name, "vm:"):
		return c.runVM(plans, dir, opts)
	default:
		return c.runContainer(plans, base, distroCfg, builderCfg, opts)
	}
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
			if uf, ok, _ := LoadUnified(dir); ok && uf != nil && uf.VMs != nil {
				if spec, present := uf.VMs[vmName]; present {
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
	if err := tgt.Emit(plans, opts); err != nil {
		return err
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
		Distro:       []string{"archlinux"}, // cloud_image today is arch-cloud-base; extend when more VM distros land.
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
