package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// EvalCheckFailExitCode is the process exit code `ov eval` returns when an
// eval RAN to completion but one or more checks FAILED — deliberately
// distinct from 0 (all checks passed) and 1 (command / usage / infra error:
// couldn't build, deploy, or even run the eval). This lets automation (and
// `ov eval run <bed>`) tell "the thing under test is broken" apart from "the
// eval itself couldn't run". Mirrors the goss / pytest 0/1/2 convention;
// main() maps EvalFailedError to this code.
const EvalCheckFailExitCode = 2

// EvalFailedError marks an eval that ran but had failing checks. main()
// detects it via errors.As and exits EvalCheckFailExitCode. Wrap with %w to
// preserve the chain through callers.
type EvalFailedError struct {
	Failed int    // number of failed checks (0 = aggregate/unknown)
	Msg    string // optional message override (e.g. a bed-level aggregate)
}

func (e *EvalFailedError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("%d check(s) failed", e.Failed)
}

// EvalCmd is the unified `ov eval` command tree — declarative evaluation,
// AI-driven iteration, and live-container probe verbs all under one
// prefix. Three primary verbs (image / live / run) replace the old
// `ov eval image` / `ov eval live <image>` / `ov eval run <score>` split:
//
//   - `ov eval image <image>` — pure-image artifact eval (disposable
//     container, build-scope checks only, no host port mapping, no
//     volumes attached).
//   - `ov eval live <name>` — full-stack eval against a running
//     deployment (pod / vm / host / k8s); runtime variables resolved.
//   - `ov eval run <name>` — overloaded by the resolved kind: a
//     kind:eval bed runs the full R10 sequence (build → eval image →
//     deploy → eval live → fresh update → tear down); a kind:score
//     drives an AI through iteration cycles. `--all-beds` runs every
//     kind:eval bed. (Replaces the retired `ov eval kind <subkind>`,
//     whose hardcoded per-kind bed table moved into eval.yml as
//     kind:eval entities.)
//
// The mode is explicit; there is no autodetect or implicit fallback.
//
// Live-container probe verbs (cdp/wl/dbus/vnc/mcp/record/spice/libvirt/k8s)
// share the same "live" semantic: each requires a running target.
//
// Eval-run management subcommands (list-ai, list-recipe, list-score,
// list, sync-credential, report, scope, last-tag, note, run-local,
// self-evaluate) are the renamed `ov eval *` surface.
type EvalCmd struct {
	// Three primary modes
	Image  EvalImageCmd  `cmd:"" help:"Pure-image eval (disposable container, build-scope checks)"`
	Live   EvalLiveCmd   `cmd:"" help:"Full-stack eval against a running deployment"`
	Run    EvalRunCmd    `cmd:"" help:"Run a kind:eval R10 bed (full sequence) or drive an AI through a kind:score's iteration cycles"`
	Recipe EvalRecipeCmd `cmd:"" help:"Run a recipe's scenarios once (deterministic; no AI iteration)"`

	// Live-container probe verbs (each requires a running target)
	Cdp     CdpCmd     `cmd:"" help:"Chrome DevTools Protocol (open, list, click, eval)"`
	Dbus    DbusCmd    `cmd:"" help:"Interact with D-Bus services inside containers"`
	Libvirt LibvirtCmd `cmd:"" help:"VM management via libvirt API (info, screenshot, send-key, QMP, guest-agent, snapshots, events)"`
	Mcp     McpCmd     `cmd:"" help:"Probe MCP servers declared via mcp_provides"`
	Record  RecordCmd  `cmd:"" help:"Record terminal sessions or desktop video inside running containers"`
	Spice   SpiceCmd   `cmd:"" help:"VM SPICE display (handshake, inputs, native screenshot)"`
	Vnc     VncCmd     `cmd:"" help:"Control VNC desktop in running containers"`
	Wl      WlCmd      `cmd:"" help:"Desktop automation (input, windows, screenshots, sway IPC)"`
	K8s     K8sCmd     `cmd:"" name:"k8s" help:"Kubernetes cluster probes (nodes, wait-nodes, pods, ingress, storageclass, addons, apply, delete, raw)"`
	Adb     AdbCmd     `cmd:"" help:"Android Debug Bridge — devices, shell, install, uninstall, getprop, screencap, logcat-tail, wait-for-device"`
	Appium  AppiumCmd  `cmd:"" help:"Appium WebDriver — status, session-create/delete, install-app, find, click, send-keys, screenshot"`

	// Eval-run management (was `ov eval *`)
	ListAI     EvalListAICmd     `cmd:"" name:"list-ai" help:"List configured AIs from eval.yml"`
	ListRecipe EvalListRecipeCmd `cmd:"" name:"list-recipe" help:"List configured recipes (spec) from eval.yml"`
	ListScore  EvalListScoreCmd  `cmd:"" name:"list-score" help:"List configured scores (runner config) from eval.yml"`
	RunLocal   EvalRunLocalCmd   `cmd:"" name:"run-local" hidden:"" help:"Pod/VM-side iteration driver (not invoked directly)"`
	SyncCred   EvalSyncCredCmd   `cmd:"" name:"sync-credential" help:"Copy AI credentials into the score's target"`
	Scope      EvalScopeCmd      `cmd:"" name:"scope" help:"AI-facing: print current iteration scope"`
	LastTag    EvalLastTagCmd    `cmd:"" name:"last-tag" help:"AI-facing: print prior iteration's image tag"`
	SelfEval   EvalSelfEvalCmd   `cmd:"" name:"self-evaluate" help:"AI-facing: rebuild current clone + re-run live eval"`
	List       EvalListRunsCmd   `cmd:"" name:"list" help:"List past eval runs under .eval/<score>/"`
	Report     EvalReportCmd     `cmd:"" name:"report" help:"Render a past result-<calver>.yml"`
	Note       EvalNoteCmd       `cmd:"" name:"note" help:"Read/append the persistent NOTES.md memory for a score"`
}

// EvalLiveCmd runs tests against a running service — the deploy-time entry point.
//
//   - Extracts the image's three-section LabelEvalSet from OCI labels.
//   - Applies the local deploy.yml tests overlay (merge by id:).
//   - Resolves ${…} variables using meta + deploy + podman-inspect of the
//     running container.
//   - Executes the merged spec (container-internal verbs via exec; host-side
//     verbs directly).
//
// The command exits non-zero on any failed check. Skipped checks (missing
// runtime context, skip: true, id-override with skip) do not fail the run.
type EvalLiveCmd struct {
	Image    string   `arg:"" help:"Image name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Format   string   `long:"format" default:"text" help:"Output format: text, json, tap"`
	Filter   []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
	Section  string   `long:"section" help:"Only run this section: layer, image, or deploy"`
}

func (c *EvalLiveCmd) Run() error {
	// VM dispatch: if the name matches a vm.yml entity, route the test run
	// through SSH instead of podman exec. VM deploys don't have an OCI image
	// to pull labels from, so tests come exclusively from the deploy.yml
	// overlay. This keeps the same declarative `tests:` authoring surface
	// working for `ov deploy add vm:<name>` flows, and also works for bare VMs
	// created via `ov vm create` before `ov deploy add` has been run.
	if c.isVmTarget() {
		return c.runVm()
	}

	// Local dispatch: a `target: local` deploy is a host filesystem apply, not
	// a container, so its deploy-scope probes run on the host (or over SSH for
	// host: <remote>) via a ShellExecutor/SSHExecutor — the SAME target-dispatch
	// `ov deploy add` uses — instead of the podman-exec container path below.
	if c.isLocalTarget() {
		return c.runLocalEval()
	}

	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Load deploy overlay (local tests) AND project-level tests up front
	// so the deploy entry's `image:` field can drive metadata extraction.
	// Pre-2026-05-12 the code read the running container's image ref via
	// `containerImageRef`, which silently returned a stale ref on
	// volume-pinned deploys and dropped any probes added after the seed
	// image. The cutover deletes that fallback: the eval runner now
	// inspects what the operator declared, not what the container
	// happens to be running. The hard-required `image:` field
	// (validateDeployRequiresImage in unified.go / deploy.go) guarantees
	// this lookup always finds a non-empty value.
	dir, _ := os.Getwd()
	var localTests []Check
	var projectTests []Check
	var deployOverlay *DeploymentNode
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
		if pc := uf.ProjectDeployConfig(); pc != nil {
			if entry, ok := pc.Deploy[c.Image]; ok {
				projectTests = entry.Eval
			}
		}
	}
	dc := loadDeployConfigForRead("ov eval live")
	if dc != nil {
		if entry, ok := dc.Deploy[deployKey(c.Image, c.Instance)]; ok {
			localTests = entry.Eval
			deployOverlay = &entry
		} else if entry, ok := dc.Deploy[c.Image]; ok {
			localTests = entry.Eval
			deployOverlay = &entry
		}
	}

	// Resolve the deploy key → declared image short-name via THE shared
	// resolver (deploy.go resolveDeployImageName) — the same one ov config /
	// start / shell use. This used to be an inline operator-then-project
	// copy, which is exactly how `ov eval live` diverged from `ov config`
	// for kind:eval beds where key != image (eval-jupyter-pod → jupyter).
	// deployOverlay (loaded above) is still consulted for the tests overlay
	// + runtime var resolution. The hard-required `image:` field
	// (validateDeployRequiresImage) guarantees a real image for every pod
	// deploy, so the resolver returns the declared image, never the key.
	imageRef := resolveDeployImageName(c.Image, c.Instance)
	// Short names (e.g. `versa`) need to be resolved to a fully-
	// qualified registry ref before ExtractMetadata can read OCI
	// labels. Full refs and remote refs pass through unchanged. The
	// canonical helper (also used by deploy preflight) lives in
	// ensure_image.go.
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return fmt.Errorf("resolving deploy image %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Eval == nil {
		fmt.Fprintln(os.Stderr, "No tests defined for this image.")
		return nil
	}
	localTests = MergeDeployEval(projectTests, localTests)
	resolver, _ := ResolveEvalVarsRuntime(meta, deployOverlay, engine, c.Image, containerName, c.Instance)

	// Compose the final check list: layer + image + merged deploy.
	checks := collectChecksForRun(meta.Eval, localTests, c.Section, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(ContainerChain(engine, containerName), resolver, RunModeLive)
	runner.Image = c.Image
	runner.Instance = c.Instance
	runner.Distros = meta.Distro
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "Image: %s (container: %s)\n", meta.Image, containerName)
	fails := formatResults(results, c.Format)
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}

// isVmTarget returns true when c.Image names a `kind: vm` entity OR a
// kind:deployment with target:vm OR a dotted-path child deployment nested
// inside a target:vm parent. Cheap check — a missing/unreadable
// overthink.yml returns false and the caller falls through to the
// container dispatch path.
func (c *EvalLiveCmd) isVmTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok {
		return false
	}
	// Direct kind:vm entity name match.
	if uf.VM != nil {
		if _, present := uf.VM[c.Image]; present {
			return true
		}
	}
	// Schema v4: kind:deployment name with target:vm (possibly dotted).
	if uf.Deploy != nil {
		// Simple deployment-key lookup.
		if entry, present := uf.Deploy[c.Image]; present {
			if entry.Target == "vm" {
				return true
			}
		}
		// Dotted-path nested lookup: route via the VM parent when the
		// root segment points at a target:vm deployment (regardless of
		// the leaf's target — child commands execute through the
		// parent's SSH substrate).
		if idx := strings.Index(c.Image, "."); idx > 0 {
			root := c.Image[:idx]
			if entry, present := uf.Deploy[root]; present && entry.Target == "vm" {
				return true
			}
		}
	}
	return false
}

// resolveNestedNode walks a dotted path through the Nested tree rooted at
// the top-level deployment, returning the leaf DeploymentNode.
func resolveNestedNode(roots map[string]DeploymentNode, path string) *DeploymentNode {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}
	entry, ok := roots[parts[0]]
	if !ok {
		return nil
	}
	current := &entry
	for _, p := range parts[1:] {
		if current.Nested == nil {
			return nil
		}
		next, ok := current.Nested[p]
		if !ok || next == nil {
			return nil
		}
		current = next
	}
	return current
}

// runVm executes deploy-scope tests against a VM guest over SSH.
//
// Connection resolution order:
//  1. Start from VmSpec defaults (resolveVmSshUser / resolveVmSshPort / the
//     conventional key path under ~/.local/share/ov/vm/ov-<name>/).
//  2. Overlay any VmState-materialized fields from deploy.yml (user, port,
//     key path) so VMs whose layers have been applied via `ov deploy add vm:`
//     honor the exact state the deploy wrote.
//
// VMs have no OCI image labels, so no layer/image test section exists —
// only the local deploy overlay's `tests:` list applies.
func (c *EvalLiveCmd) runVm() error {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	// Schema v4: c.Image may be
	//   (a) a kind:vm entity name directly (e.g. "arch"),
	//   (b) a kind:deployment name with target:vm (e.g. "arch-vm") whose
	//       Vm field points at the actual kind:vm entity, OR
	//   (c) a dotted path "parent.child" where `parent` is a target:vm
	//       deployment and `child` is a nested node whose tests run in
	//       the parent's SSH substrate.
	vmName := c.Image
	var nestedLeaf *DeploymentNode
	if uf.Deploy != nil {
		if entry, ok := uf.Deploy[c.Image]; ok && entry.Target == "vm" && entry.Vm != "" {
			vmName = entry.Vm
		} else if idx := strings.Index(c.Image, "."); idx > 0 {
			root := c.Image[:idx]
			if parent, present := uf.Deploy[root]; present && parent.Target == "vm" {
				if parent.Vm != "" {
					vmName = parent.Vm
				}
				nestedLeaf = resolveNestedNode(uf.Deploy, c.Image)
			}
		}
	}
	var spec *VmSpec
	if uf.VM != nil {
		spec = uf.VM[vmName]
	}

	user := resolveVmSshUser(spec)
	port := resolveVmSshPort(spec)

	// Two deploy sources for VMs:
	//   - project-level: overthink.yml / deploy.yml `deployments.images["vm:<name>"]`
	//     → holds the authored `tests:` list (part of the repo).
	//   - per-machine:   ~/.config/ov/deploy.yml `images["vm:<name>"]`
	//     → holds VmState written by `ov deploy add vm:<name>` and any local
	//       overrides/additions.
	//
	// Schema v3: also accept plain-identifier deployment entries whose
	// `target: vm` + `vm: <c.Image>` resolves to the same VM.
	// This is what makes `ov eval live <deploy-name>` work for beds like
	// `arch-vm` that don't carry the legacy `vm:` prefix in the key.
	// Merge by id (local replaces project); same rules as MergeDeployEval.
	// Resolve the VM's deploy entry via THE shared findVmDeployNode (deploy.go)
	// — the same lookup `ov deploy add` uses — by deploy NAME (c.Image) first,
	// then the vm entity (vmName). Keying by name first means a bed whose key
	// differs from its vm entity (eval-k3s-vm -> vm: k3s-vm) resolves to its
	// own entry rather than being mis-matched via the vm entity name.
	var projectTests, localTests []Check
	// Nested dotted-path short-circuit: when the request is for a
	// child node, use its own Tests directly instead of the parent's.
	if nestedLeaf != nil {
		projectTests = nestedLeaf.Eval
	} else if pc := uf.ProjectDeployConfig(); pc != nil {
		if entry, ok := findVmDeployNode(pc.Deploy, c.Image, vmName); ok {
			projectTests = entry.Eval
		}
	}
	if dc := loadDeployConfigForRead("ov eval vm"); dc != nil {
		if entry, ok := findVmDeployNode(dc.Deploy, c.Image, vmName); ok {
			localTests = entry.Eval
			if entry.VmState != nil {
				if entry.VmState.SshUser != "" {
					user = entry.VmState.SshUser
				}
				if entry.VmState.SshPort > 0 {
					port = entry.VmState.SshPort
				}
			}
		}
	}
	tests := MergeDeployEval(projectTests, localTests)

	// SSH connection details (User/Port/IdentityFile) live in the
	// managed ssh-config Host stanza (ov-<vmName>) written at deploy
	// time. We point the executor at the alias and let ssh(1) resolve
	// the rest from ~/.ssh/config + agent.
	host := "127.0.0.1"
	var executor DeployExecutor = &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 10}

	// 2026-04 cutover: when c.Image is dotted ("vm.inner-pod"), walk
	// the deploy tree and construct the full chain via ResolveDeployChain
	// so leaf tests run inside the leaf's actual venue. Pre-cutover this
	// path was silently single-hop SSH — `command: id` for a pod-in-vm
	// leaf returned the VM's user, not the inner pod's.
	if strings.Contains(c.Image, ".") {
		if roots, _ := resolveTreeRoot(dir); roots != nil {
			if _, chain, chainErr := ResolveDeployChain(roots, c.Image, ShellExecutor{}); chainErr == nil && chain != nil {
				executor = chain
			}
		}
	}

	env := map[string]string{
		"IMAGE":          c.Image,
		"INSTANCE":       c.Instance,
		"HOST_PORT:22":   strconv.Itoa(port),
		"CONTAINER_IP":   host,
		"CONTAINER_NAME": "ov-" + c.Image,
		"USER":           user,
		"HOME":           "/home/" + user,
	}
	resolver := &EvalVarResolver{Env: env, HasRuntime: true}

	baked := &LabelEvalSet{}
	checks := collectChecksForRun(baked, tests, c.Section, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(executor, resolver, RunModeLive)
	runner.Image = c.Image
	runner.Instance = c.Instance
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "VM: ov-%s (ssh %s@%s:%d)\n", c.Image, user, host, port)
	fails := formatResults(results, c.Format)
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}

// isLocalTarget returns true when c.Image names a `target: local` deployment
// (a host filesystem apply) OR a dotted-path child whose root segment is a
// target:local deployment. Mirror of isVmTarget — a missing/unreadable
// overthink.yml returns false and the caller falls through to the container
// dispatch path.
func (c *EvalLiveCmd) isLocalTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf.Deploy == nil {
		return false
	}
	if entry, present := uf.Deploy[c.Image]; present && entry.Target == "local" {
		return true
	}
	if idx := strings.Index(c.Image, "."); idx > 0 {
		root := c.Image[:idx]
		if entry, present := uf.Deploy[root]; present && entry.Target == "local" {
			return true
		}
	}
	return false
}

// runLocalEval executes deploy-scope checks against a `target: local`
// deployment on its host venue. Mirror of runVm, but the venue is a
// ShellExecutor (host: local) or SSHExecutor (host: <remote>) selected by the
// shared rootExecutorForDeployNode, and dotted paths compose through
// ResolveDeployChain exactly like runVm.
//
// Local deploys carry no OCI image labels, so there is no layer/image test
// section — checks come from the resolved kind:local template's `eval:` (base)
// merged with the deploy entry's `eval:` and the per-host deploy.yml overlay
// (id-based replace/append, same as everywhere). Host-context vars only: no
// HOST_PORT:<N> / CONTAINER_IP (host services bind real ports; faking a port
// mapping would be wrong).
func (c *EvalLiveCmd) runLocalEval() error {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}

	// Resolve the target node (leaf for a dotted path; the entry otherwise)
	// and the root-segment node (whose host: selects the chain's root venue).
	dotted := strings.Contains(c.Image, ".")
	var node, rootNode *DeploymentNode
	if uf.Deploy != nil {
		if dotted {
			node = resolveNestedNode(uf.Deploy, c.Image)
			root := c.Image[:strings.Index(c.Image, ".")]
			if entry, ok := uf.Deploy[root]; ok {
				rn := entry
				rootNode = &rn
			}
		} else if entry, ok := uf.Deploy[c.Image]; ok {
			n := entry
			node = &n
			rootNode = &n
		}
	}
	if node == nil {
		return fmt.Errorf("eval live: local deployment %q not found", c.Image)
	}

	// Select the root venue from the root node's host:, then compose nested
	// hops for a dotted path through the shared ResolveDeployChain.
	executor, err := rootExecutorForDeployNode(rootNode)
	if err != nil {
		return fmt.Errorf("eval live %q: %w", c.Image, err)
	}
	if dotted {
		if roots, _ := resolveTreeRoot(dir); roots != nil {
			if _, chain, chainErr := ResolveDeployChain(roots, c.Image, executor); chainErr == nil && chain != nil {
				executor = chain
			}
		}
	}

	venue := "host (local)"
	if _, isShell := executor.(ShellExecutor); !isShell {
		venue = executor.Venue()
	}
	fmt.Fprintf(os.Stderr, "Local deploy: %s [%s]\n", c.Image, venue)

	fails, err := evalLocalDeployScope(dir, node, c.Image, c.Instance, c.Section, c.Filter, executor, c.Format)
	if err != nil {
		return err
	}
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}

// evalLocalDeployScope collects a local deployment's deploy-scope checks —
// kind:local template `eval:` (base) merged with the deploy entry `eval:`
// (extends/overrides) and the per-host deploy.yml overlay — and runs them on
// `exec`. Shared by `ov eval live <local>` (runLocalEval) and
// `ov deploy add <local> --verify` (LocalDeployTarget) so the two surfaces
// source + run probes identically (R3). Host-context vars only (no
// HOST_PORT:<N> / CONTAINER_IP). Returns the failure count.
func evalLocalDeployScope(dir string, node *DeploymentNode, image, instance, section string, filter []string, exec DeployExecutor, format string) (int, error) {
	var tests []Check
	if node != nil && strings.TrimSpace(node.Local) != "" {
		if spec := findLocalSpec(dir, strings.TrimSpace(node.Local)); spec != nil {
			tests = append(tests, spec.Eval...)
		}
	}
	if node != nil {
		tests = MergeDeployEval(tests, node.Eval)
	}
	if dc := loadDeployConfigForRead("ov eval live"); dc != nil {
		if entry, ok := dc.Deploy[deployKey(image, instance)]; ok {
			tests = MergeDeployEval(tests, entry.Eval)
		} else if entry, ok := dc.Deploy[image]; ok {
			tests = MergeDeployEval(tests, entry.Eval)
		}
	}

	user := os.Getenv("USER")
	home, herr := exec.ResolveHome(context.Background(), user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	resolver := &EvalVarResolver{Env: map[string]string{
		"IMAGE":    image,
		"INSTANCE": instance,
		"USER":     user,
		"HOME":     home,
	}, HasRuntime: true}

	checks := collectChecksForRun(&LabelEvalSet{}, tests, section, filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return 0, nil
	}
	runner := NewRunner(exec, resolver, RunModeLive)
	runner.Image = image
	runner.Instance = instance
	results := runner.Run(context.Background(), checks)
	return formatResults(results, format), nil
}

// EvalImageCmd runs PURE-IMAGE eval against a disposable container.
// Build-scope checks only (layer + image sections). Deploy-scope checks
// are skipped — they require a running deployment with port mappings,
// volumes, and resolved runtime variables. For full-stack eval against
// a running deployment, use `ov eval live <name>`.
//
// Image references resolve purely against local container storage via
// resolveLocalImageRef — never reads image.yml. Run `ov image pull <name>`
// or `ov image build <name>` first if the image isn't in local storage yet.
type EvalImageCmd struct {
	Image  string   `arg:"" help:"Image reference (full ref or short name resolved against local container storage; never reads image.yml)"`
	Format string   `long:"format" default:"text" help:"Output format: text, json, tap, yaml"`
	Filter []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
}

func (c *EvalImageCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	imageRef, err := resolveLocalImageRef(rt.RunEngine, c.Image)
	if err != nil {
		return err
	}

	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Eval == nil {
		fmt.Fprintln(os.Stderr, "No eval defined for this image.")
		return nil
	}

	// PURE-IMAGE: always disposable container, always layer + image
	// sections only. The mode is explicit; no autodetect, no fallback.
	executor := ImageChain(rt.RunEngine, imageRef)
	resolver := ResolveEvalVarsBuild(meta)
	mode := RunModeImage

	checks := gatherSections(meta.Eval, nil /* no local overlay at build time */, []string{"layer", "image"})
	checks = filterByVerb(checks, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(executor, resolver, mode)
	runner.Distros = meta.Distro
	results := runner.Run(context.Background(), checks)

	// Also run scenarios if a Description set is baked into the image —
	// the eval-run scorer reads this when --format yaml is requested.
	var scenarioResults []ScenarioResult
	if meta.Description != nil && !meta.Description.IsEmpty() {
		scenarioResults = RunScenarios(context.Background(), runner, meta.Description, nil, false)
	}

	liveContainer := "" // PURE-IMAGE never has a live container
	fmt.Fprintf(os.Stderr, "Image: %s\n", imageRef)

	// YAML format emits the shape ParseOvTestOutput expects —
	// this is the benchmark scorer's input format.
	if c.Format == "yaml" {
		return emitImageTestYAML(os.Stdout, imageRef, liveContainer, scenarioResults, results)
	}

	fails := formatResults(results, c.Format)
	if fails > 0 {
		return &EvalFailedError{Failed: fails}
	}
	return nil
}

// emitImageTestYAML writes the `ov image test --format yaml` payload
// that ParseOvTestOutput (benchmark_score.go) consumes. The shape is:
//
//	image: <ref>
//	mode: image | run
//	scenarios:
//	  - id, origin, name, tags, status, pending_steps, steps[]
//	summary: { total, pass, fail, skip }
func emitImageTestYAML(w io.Writer, imageRef, liveContainer string, scenarios []ScenarioResult, _ []EvalResult) error {
	mode := "image"
	if liveContainer != "" {
		mode = "run"
	}
	out := EvalRunResults{Image: imageRef, Mode: mode}
	for _, sr := range scenarios {
		tr := ScenarioEvalResult{
			ID:           sr.ScenarioID,
			Origin:       sr.Origin,
			Name:         sr.Name,
			Tag:          append([]string(nil), sr.Tag...),
			Status:       sr.Status.String(),
			PendingSteps: sr.Pending,
		}
		for _, sp := range sr.Step {
			stepRes := StepEvalResult{
				Keyword: sp.Keyword,
				Text:    sp.Text,
				StepID:  sp.StepID,
				Status:  sp.Result.Status.String(),
				Verb:    sp.Result.Verb,
			}
			if sp.Result.Verb == "" {
				stepRes.Pending = true
			}
			tr.Step = append(tr.Step, stepRes)
		}
		out.Scenario = append(out.Scenario, tr)
		out.Summary.Total++
		switch tr.Status {
		case "pass":
			out.Summary.Pass++
		case "fail":
			out.Summary.Fail++
		case "skip":
			out.Summary.Skip++
		}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// collectChecksForRun is the full ov-test assembly: all three label sections
// + the local deploy overlay, with optional section/verb filtering.
func collectChecksForRun(baked *LabelEvalSet, local []Check, section string, filter []string) []Check {
	sections := []string{"layer", "image", "deploy"}
	if section != "" {
		sections = []string{section}
	}
	checks := gatherSections(baked, local, sections)
	return filterByVerb(checks, filter)
}

// gatherSections concatenates the requested sections. For the deploy section,
// applies MergeDeployEval with any local overlay.
func gatherSections(baked *LabelEvalSet, local []Check, sections []string) []Check {
	var out []Check
	for _, s := range sections {
		switch s {
		case "layer":
			out = append(out, baked.Layer...)
		case "image":
			out = append(out, baked.Image...)
		case "deploy":
			out = append(out, MergeDeployEval(baked.Deploy, local)...)
		}
	}
	return out
}

// filterByVerb narrows the list to checks whose verb matches any of allowedVerbs.
// An empty filter returns the list unchanged.
func filterByVerb(checks []Check, allowedVerbs []string) []Check {
	if len(allowedVerbs) == 0 {
		return checks
	}
	want := map[string]bool{}
	for _, v := range allowedVerbs {
		want[v] = true
	}
	var out []Check
	for _, c := range checks {
		k, _ := c.Kind()
		if want[k] {
			out = append(out, c)
		}
	}
	return out
}

// formatResults writes results in the requested format and returns the fail count.
func formatResults(results []EvalResult, format string) int {
	switch strings.ToLower(format) {
	case "json":
		return FormatResultsJSON(os.Stdout, results)
	case "tap":
		return FormatResultsTAP(os.Stdout, results)
	default:
		return FormatResultsText(os.Stdout, results)
	}
}

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the eval runner.
