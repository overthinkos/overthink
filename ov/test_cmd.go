package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TestCmd groups the deploy-time test runner with the live-container
// interaction verbs (cdp, wl, dbus, vnc). The default subcommand is `run`
// (tagged default:"withargs"), so `ov test <image>` still dispatches to the
// test runner; explicit subcommand names (cdp/wl/dbus/vnc) take over when
// matched.
type TestCmd struct {
	Run  TestRunCmd `cmd:"" default:"withargs" help:"Run declarative tests against a running service"`
	Cdp  CdpCmd     `cmd:"" help:"Chrome DevTools Protocol (open, list, click, eval)"`
	Dbus DbusCmd    `cmd:"" help:"Interact with D-Bus services inside containers"`
	Vnc  VncCmd     `cmd:"" help:"Control VNC desktop in running containers"`
	Wl   WlCmd      `cmd:"" help:"Desktop automation (input, windows, screenshots, sway IPC)"`
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
	var deployOverlay *DeployImageConfig
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
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "Image: %s (container: %s)\n", meta.Image, containerName)
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

	resolver := ResolveTestVarsBuild(meta)

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

	runner := NewRunner(&ImageExecutor{Engine: rt.RunEngine, ImageRef: imageRef}, resolver, RunModeImageTest)
	results := runner.Run(context.Background(), checks)

	fmt.Fprintf(os.Stderr, "Image: %s\n", imageRef)
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
