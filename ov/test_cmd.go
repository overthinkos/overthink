package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// TestCmd groups the deploy-time test runner with the live-container
// interaction verbs (cdp, wl, dbus, vnc, mcp, spice, libvirt, record). The
// default subcommand is `run` (tagged default:"withargs"), so
// `ov test <image>` still dispatches to the test runner; explicit subcommand
// names take over when matched.
type TestCmd struct {
	Run     TestRunCmd `cmd:"" default:"withargs" help:"Run declarative tests against a running service"`
	Cdp     CdpCmd     `cmd:"" help:"Chrome DevTools Protocol (open, list, click, eval)"`
	Dbus    DbusCmd    `cmd:"" help:"Interact with D-Bus services inside containers"`
	Libvirt LibvirtCmd `cmd:"" help:"VM management via libvirt API (info, screenshot, send-key, QMP, guest-agent, snapshots, events)"`
	Mcp     McpCmd     `cmd:"" help:"Probe MCP servers declared via mcp_provides"`
	Record  RecordCmd  `cmd:"" help:"Record terminal sessions or desktop video inside running containers"`
	Spice   SpiceCmd   `cmd:"" help:"VM SPICE display (handshake, inputs, native screenshot)"`
	Vnc     VncCmd     `cmd:"" help:"Control VNC desktop in running containers"`
	Wl      WlCmd      `cmd:"" help:"Desktop automation (input, windows, screenshots, sway IPC)"`
	K8s     K8sCmd     `cmd:"" name:"k8s" help:"Kubernetes cluster probes (nodes, wait-nodes, pods, ingress, storageclass, addons, apply, delete, raw)"`
}

// TestRunCmd runs tests against a running service — the deploy-time entry point.
//
//   - Extracts the image's three-section LabelTestSet from OCI labels.
//   - Applies the local deploy.yml tests overlay (merge by id:).
//   - Resolves ${…} variables using meta + deploy + podman-inspect of the
//     running container.
//   - Executes the merged spec (container-internal verbs via exec; host-side
//     verbs directly).
//
// The command exits non-zero on any failed check. Skipped checks (missing
// runtime context, skip: true, id-override with skip) do not fail the run.
type TestRunCmd struct {
	Image    string   `arg:"" help:"Image name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Format   string   `long:"format" default:"text" help:"Output format: text, json, tap"`
	Filter   []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
	Section  string   `long:"section" help:"Only run this section: layer, image, or deploy"`
}

func (c *TestRunCmd) Run() error {
	// VM dispatch: if the name matches a vms.yml entity, route the test run
	// through SSH instead of podman exec. VM deploys don't have an OCI image
	// to pull labels from, so tests come exclusively from the deploy.yml
	// overlay. This keeps the same declarative `tests:` authoring surface
	// working for `ov deploy add vm:<name>` flows, and also works for bare VMs
	// created via `ov vm create` before `ov deploy add` has been run.
	if c.isVmTarget() {
		return c.runVm()
	}

	engine, containerName, err := resolveContainer(c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Image ref is needed to pull labels (they live on the image, not the container).
	imageRef, err := containerImageRef(engine, containerName)
	if err != nil {
		return err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Tests == nil {
		fmt.Fprintln(os.Stderr, "No tests defined for this image.")
		return nil
	}

	// Load deploy overlay (local tests, if any).
	dc, _ := LoadDeployConfig()
	var localTests []Check
	if dc != nil {
		if entry, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			localTests = entry.Tests
		}
	}

	// Build runtime variable resolver.
	var deployOverlay *DeploymentNode
	if dc != nil {
		if entry, ok := dc.Images[deployKey(c.Image, c.Instance)]; ok {
			deployOverlay = &entry
		}
	}
	resolver, _ := ResolveTestVarsRuntime(meta, deployOverlay, engine, containerName)
	if c.Instance != "" {
		resolver.Env["INSTANCE"] = c.Instance
	}

	// Compose the final check list: layer + image + merged deploy.
	checks := collectChecksForRun(meta.Tests, localTests, c.Section, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(&ContainerExecutor{Engine: engine, ContainerName: containerName}, resolver, RunModeTest)
	runner.Image = c.Image
	runner.Instance = c.Instance
	runner.Distros = meta.Distro
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "Image: %s (container: %s)\n", meta.Image, containerName)
	fails := formatResults(results, c.Format)
	if fails > 0 {
		return fmt.Errorf("%d check(s) failed", fails)
	}
	return nil
}

// isVmTarget returns true when c.Image names a `kind: vm` entity in vms.yml.
// Cheap check — a missing/unreadable overthink.yml returns false and the
// caller falls through to the container dispatch path.
func (c *TestRunCmd) isVmTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf.VM == nil {
		return false
	}
	_, present := uf.VM[c.Image]
	return present
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
func (c *TestRunCmd) runVm() error {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	spec := uf.VM[c.Image]

	user := resolveVmSshUser(spec)
	port := resolveVmSshPort(spec)
	home, _ := os.UserHomeDir()
	keyPath := home + "/.local/share/ov/vm/ov-" + c.Image + "/id_ed25519"

	// Two deploy sources for VMs:
	//   - project-level: overthink.yml / deploy.yml `deployments.images["vm:<name>"]`
	//     → holds the authored `tests:` list (part of the repo).
	//   - per-machine:   ~/.config/ov/deploy.yml `images["vm:<name>"]`
	//     → holds VmState written by `ov deploy add vm:<name>` and any local
	//       overrides/additions.
	// Merge by id (local replaces project), same rules as MergeDeployTests.
	var projectTests, localTests []Check
	if pc := uf.ProjectDeployConfig(); pc != nil {
		if entry, ok := pc.Images["vm:"+c.Image]; ok {
			projectTests = entry.Tests
		}
	}
	if dc, _ := LoadDeployConfig(); dc != nil {
		if entry, ok := dc.Images["vm:"+c.Image]; ok {
			localTests = entry.Tests
			if entry.VmState != nil {
				if entry.VmState.SshUser != "" {
					user = entry.VmState.SshUser
				}
				if entry.VmState.SshPort > 0 {
					port = entry.VmState.SshPort
				}
				if entry.VmState.SshKeyPath != "" {
					keyPath = entry.VmState.SshKeyPath
				}
			}
		}
	}
	tests := MergeDeployTests(projectTests, localTests)

	if user == "" || port == 0 || keyPath == "" {
		return fmt.Errorf("vm:%s has incomplete SSH config (user=%q port=%d key=%q); run `ov vm create %s` first",
			c.Image, user, port, keyPath, c.Image)
	}

	host := "127.0.0.1"
	executor := &VmTestExecutor{User: user, Host: host, Port: port, KeyPath: keyPath}

	env := map[string]string{
		"IMAGE":          c.Image,
		"INSTANCE":       c.Instance,
		"HOST_PORT:22":   strconv.Itoa(port),
		"CONTAINER_IP":   host,
		"CONTAINER_NAME": "ov-" + c.Image,
		"USER":           user,
		"HOME":           "/home/" + user,
	}
	resolver := &TestVarResolver{Env: env, HasRuntime: true}

	baked := &LabelTestSet{}
	checks := collectChecksForRun(baked, tests, c.Section, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(executor, resolver, RunModeTest)
	runner.Image = c.Image
	runner.Instance = c.Instance
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "VM: ov-%s (ssh %s@%s:%d)\n", c.Image, user, host, port)
	fails := formatResults(results, c.Format)
	if fails > 0 {
		return fmt.Errorf("%d check(s) failed", fails)
	}
	return nil
}

// ImageTestCmd runs tests against a disposable container started from the
// built image — a test-mode entry point that executes build-scope and
// image-scope checks without requiring a long-running deployment.
//
//   - Executes layer + image sections by default.
//   - --include-deploy also executes the deploy section (useful for
//     exercising deploy-default checks that don't need runtime vars).
//
// Image references resolve purely against local container storage via
// resolveLocalImageRef — never reads image.yml. Run `ov image pull <name>` or
// `ov image build <name>` first if the image isn't in local storage yet.
type ImageTestCmd struct {
	Image         string   `arg:"" help:"Image reference (full ref or short name resolved against local container storage; never reads image.yml)"`
	Format        string   `long:"format" default:"text" help:"Output format: text, json, tap"`
	Filter        []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
	IncludeDeploy bool     `long:"include-deploy" help:"Also run the deploy section (normally skipped)"`
}

func (c *ImageTestCmd) Run() error {
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
	if meta == nil || meta.Tests == nil {
		fmt.Fprintln(os.Stderr, "No tests defined for this image.")
		return nil
	}

	// Executor selection:
	//   - --include-deploy: prefer a running container (built from this
	//     image) so deploy tests see real service state, real port
	//     mappings, real runtime env. Only fall back to the disposable
	//     container if nothing is running — and in that case deploy tests
	//     will largely skip or fail, which is the honest outcome.
	//   - Otherwise (build-scope only): always use the disposable container.
	//     Layer/image tests are build-time invariants; a running container
	//     is unnecessary and could mask differences between the built image
	//     and its deployed state.
	var executor Executor
	var resolver *TestVarResolver
	var liveContainer string
	mode := RunModeImageTest

	if c.IncludeDeploy {
		// Use the image's own short name from the OCI label, not c.Image —
		// c.Image may be a full ref (`ghcr.io/overthinkos/fedora-coder:latest`)
		// which wouldn't map to the `ov-<image>` container name scheme.
		imageName := meta.Image
		engineBin := EngineBinary(rt.RunEngine)
		candidate := containerNameInstance(imageName, "")
		if containerRunning(engineBin, candidate) {
			liveContainer = candidate
			executor = &ContainerExecutor{Engine: rt.RunEngine, ContainerName: candidate}
			// Runtime var resolver populates HOST_PORT, INSTANCE, etc. from
			// the live container's inspect output. Load deploy.yml overlay
			// so deploy-overridden settings (e.g. remapped ports) win.
			dc, _ := LoadDeployConfig()
			var deployOverlay *DeploymentNode
			if dc != nil {
				if entry, ok := dc.Images[deployKey(imageName, "")]; ok {
					deployOverlay = &entry
				}
			}
			resolver, _ = ResolveTestVarsRuntime(meta, deployOverlay, rt.RunEngine, candidate)
			mode = RunModeTest
		}
	}

	if executor == nil {
		executor = &ImageExecutor{Engine: rt.RunEngine, ImageRef: imageRef}
		resolver = ResolveTestVarsBuild(meta)
	}

	// Build the check list. Under ov image test the default sections are
	// layer + image; --include-deploy opts in the deploy section too.
	sections := []string{"layer", "image"}
	if c.IncludeDeploy {
		sections = append(sections, "deploy")
	}
	checks := gatherSections(meta.Tests, nil /* no local overlay at build time */, sections)
	checks = filterByVerb(checks, c.Filter)
	if len(checks) == 0 {
		fmt.Fprintln(os.Stderr, "No checks to run after filtering.")
		return nil
	}

	runner := NewRunner(executor, resolver, mode)
	if liveContainer != "" {
		// Use short image name (from OCI label) so sub-command invocations
		// like `ov test mcp ping <image>` and HOST_PORT resolution work —
		// c.Image may be a full ref which doesn't map to container naming.
		runner.Image = meta.Image
	}
	runner.Distros = meta.Distro
	results := runner.Run(context.Background(), checks)

	if liveContainer != "" {
		fmt.Fprintf(os.Stderr, "Image: %s (live container: %s)\n", imageRef, liveContainer)
	} else {
		fmt.Fprintf(os.Stderr, "Image: %s\n", imageRef)
	}
	fails := formatResults(results, c.Format)
	if fails > 0 {
		return fmt.Errorf("%d check(s) failed", fails)
	}
	return nil
}

// collectChecksForRun is the full ov-test assembly: all three label sections
// + the local deploy overlay, with optional section/verb filtering.
func collectChecksForRun(baked *LabelTestSet, local []Check, section string, filter []string) []Check {
	sections := []string{"layer", "image", "deploy"}
	if section != "" {
		sections = []string{section}
	}
	checks := gatherSections(baked, local, sections)
	return filterByVerb(checks, filter)
}

// gatherSections concatenates the requested sections. For the deploy section,
// applies MergeDeployTests with any local overlay.
func gatherSections(baked *LabelTestSet, local []Check, sections []string) []Check {
	var out []Check
	for _, s := range sections {
		switch s {
		case "layer":
			out = append(out, baked.Layer...)
		case "image":
			out = append(out, baked.Image...)
		case "deploy":
			out = append(out, MergeDeployTests(baked.Deploy, local)...)
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
func formatResults(results []TestResult, format string) int {
	switch strings.ToLower(format) {
	case "json":
		return FormatResultsJSON(os.Stdout, results)
	case "tap":
		return FormatResultsTAP(os.Stdout, results)
	default:
		return FormatResultsText(os.Stdout, results)
	}
}

// containerImageRef looks up the image ref backing a running container so
// we can pull labels from the image (not the container, which podman inspect
// does not propagate labels from by default).
func containerImageRef(engine, containerName string) (string, error) {
	out, _, exit, err := runCapture(exec.Command(EngineBinary(engine), "inspect", "--format", "{{.Config.Image}}", containerName))
	if err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", containerName, err)
	}
	if exit != 0 {
		return "", fmt.Errorf("inspect %s: exit %d", containerName, exit)
	}
	return strings.TrimSpace(out), nil
}
