package main

// deploy_target_container.go — PodDeployTarget deploys an
// InstallPlan as a running container.
//
// For container deploys, the same InstallPlan produced by
// BuildDeployPlan is consumed by two sub-systems:
//
//   1. Overlay Containerfile synthesis — when the deploy.yml has
//      `add_layers:` entries, we generate a new Containerfile that
//      inherits FROM the base image and applies the extra layers'
//      install steps on top. The overlay image is then passed to the
//      existing quadlet/podman machinery.
//
//   2. Container startup — after any overlay build, delegate to the
//      existing `ov start` path (start.go) which already handles
//      volume setup, tunnel config, traefik routes, env-provides wiring,
//      etc.
//
// For v1, PodDeployTarget.Emit acts as a thin bridge: it
// synthesizes the overlay image when needed, then hands off to the
// existing deploy pipeline. Later passes can migrate more of
// start.go's logic in here.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PodDeployTarget applies an InstallPlan as a container.
type PodDeployTarget struct {
	// DeployName is the name under which this container is known in
	// deploy.yml and in the systemd-quadlet layer.
	DeployName string

	// BaseImage is the image ref the overlay inherits from. May be the
	// project's own image (e.g. fedora-coder:2026.04.21) or a remote
	// ref already pulled into local storage.
	BaseImage string

	// Engine is "podman" or "docker". Defaults to "podman".
	Engine string

	// DistroDef + BuilderConfig feed the OCITarget used for overlay
	// synthesis. Supplied by the caller (deploy command wiring).
	DistroDef     *DistroDef
	BuilderConfig *BuilderConfig

	// OverlayBuildDir is where the synthesized Containerfile + build
	// context lives. Defaults to .build/overlay-<deploy-name>/.
	OverlayBuildDir string

	// Executor is the DeployExecutor used for the `podman build`
	// invocation. Defaults to LocalDeployExecutor when nil — matching
	// the pre-tree-schema behavior of building overlays on the
	// invoking host. When set to a NestedExecutor (the tree walker
	// does this for nested container nodes), the build runs in the
	// parent venue. Build context files are shipped via
	// Executor.PutFile before the build runs.
	Executor DeployExecutor

	// DryRunWriter receives dry-run text. Nil means os.Stderr.
	DryRunWriter *os.File

	// overlayImageRef is populated by Emit when an overlay was built;
	// read via OverlayImageRef() after Emit returns.
	overlayImageRef string
}

// exec returns the configured executor, defaulting to the local one.
func (t *PodDeployTarget) exec() DeployExecutor {
	if t.Executor == nil {
		return LocalDeployExecutor{}
	}
	return t.Executor
}

// Name identifies this target.
func (t *PodDeployTarget) Name() string { return "pod" }

// OverlayImageRef returns the overlay image reference that was built,
// or the base image when no overlay was needed. Caller passes this to
// the quadlet/start machinery.
func (t *PodDeployTarget) OverlayImageRef() string {
	if t.overlayImageRef != "" {
		return t.overlayImageRef
	}
	return t.BaseImage
}

// Emit is the DeployTarget entry point. Handles overlay synthesis when
// the plan set has any layers that aren't part of the base image.
// Does NOT perform the final container start — that stays in start.go
// via DeployUpCmd.
func (t *PodDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if len(plans) == 0 {
		return nil
	}
	if t.Engine == "" {
		t.Engine = "podman"
	}

	// Determine which plans represent overlay layers (add_layers:)
	// rather than layers already baked into the base image. v1 heuristic:
	// a plan's Layer is in any plan's AddLayers list → overlay.
	overlayLayers := collectOverlayLayers(plans)
	if len(overlayLayers) == 0 {
		// Nothing to overlay — the existing base image is deploy-ready.
		t.overlayImageRef = t.BaseImage
		// Schema v3: still tag the base as `<registry>/<deploy-name>:
		// latest` so `ov config/start <deploy-name>` can resolve it by
		// deployment name when deploy-name != image-name (e.g. a pod
		// deployment `sway-pod` targeting image `openclaw-sway-browser`).
		if opts.DryRun {
			return nil
		}
		if t.DeployName != "" && t.BaseImage != "" {
			if err := t.tagDeployAlias(opts); err != nil {
				return err
			}
		}
		return nil
	}

	// Synthesize overlay Containerfile.
	return t.buildOverlay(plans, overlayLayers, opts)
}

// tagDeployAlias tags t.overlayImageRef under
// `<registry>/<deploy-name>:latest` so deployment-name-keyed commands
// (`ov config setup`, `ov start`) resolve the image correctly when
// deploy-name differs from image-name (schema v3). Registry comes from
// the base image's `org.overthinkos.registry` OCI label.
func (t *PodDeployTarget) tagDeployAlias(opts EmitOpts) error {
	registry := readImageRegistry(t.Engine, t.overlayImageRef)
	aliasRef := t.DeployName + ":latest"
	if registry != "" {
		aliasRef = registry + "/" + t.DeployName + ":latest"
	}
	if aliasRef == t.overlayImageRef {
		return nil
	}
	tagScript := fmt.Sprintf("%s tag %s %s",
		t.Engine, deployShellQuote(t.overlayImageRef), deployShellQuote(aliasRef))
	if err := t.exec().RunUser(opts.ContextOrDefault(), tagScript, opts); err != nil {
		return fmt.Errorf("deploy-name alias tag: %w", err)
	}
	return nil
}

// collectOverlayLayers returns the set of layer names declared as
// add_layers in any plan's meta. v1 heuristic: union all plans'
// AddLayers slices.
func collectOverlayLayers(plans []*InstallPlan) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range plans {
		for _, n := range p.AddLayers {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// buildOverlay synthesizes an overlay Containerfile and builds the image.
func (t *PodDeployTarget) buildOverlay(plans []*InstallPlan, overlayLayers []string, opts EmitOpts) error {
	dir := t.OverlayBuildDir
	if dir == "" {
		dir = filepath.Join(".build", "overlay-"+t.DeployName)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("overlay build dir: %w", err)
	}
	// Render overlay Containerfile via OCITarget.
	oci := &OCITarget{
		DistroDef:     t.DistroDef,
		BuilderConfig: t.BuilderConfig,
	}
	// Only emit for the overlay layers.
	filtered := filterPlansByLayers(plans, overlayLayers)
	if err := oci.Emit(filtered, opts); err != nil {
		return err
	}

	var cf bytes.Buffer
	fmt.Fprintf(&cf, "# Overlay Containerfile for deploy %q\n", t.DeployName)
	fmt.Fprintf(&cf, "# Extra layers: %s\n\n", strings.Join(overlayLayers, ", "))
	fmt.Fprintf(&cf, "FROM %s\n\n", t.BaseImage)
	cf.WriteString(oci.String())

	cfPath := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(cfPath, cf.Bytes(), 0644); err != nil {
		return err
	}

	// Deterministic overlay tag: hash of base + sorted layer set.
	tag := overlayTagFor(t.BaseImage, overlayLayers)
	t.overlayImageRef = fmt.Sprintf("%s-overlay:%s", t.DeployName, tag)

	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] %s build -f %s -t %s %s\n",
			t.Engine, cfPath, t.overlayImageRef, dir)
		return nil
	}

	// Route the podman build via the configured executor. On the root
	// (LocalDeployExecutor) this is equivalent to the prior direct
	// exec.CommandContext call. On a NestedExecutor the command runs
	// in the parent venue — the caller is responsible for ensuring the
	// build context is reachable there (today this is only true when
	// the build dir is on a shared filesystem the parent can see; we
	// error loudly otherwise).
	if nested, ok := t.Executor.(*NestedExecutor); ok && nested != nil {
		return fmt.Errorf("PodDeployTarget: nested container overlay builds inside %s are not yet wired — build the base image locally, then `ov deploy add` with a pre-built ref", nested.Venue())
	}
	buildScript := fmt.Sprintf("%s build -f %s -t %s %s",
		t.Engine, deployShellQuote(cfPath), deployShellQuote(t.overlayImageRef), deployShellQuote(dir))
	if err := t.exec().RunUser(opts.ContextOrDefault(), buildScript, opts); err != nil {
		return fmt.Errorf("overlay build: %w", err)
	}

	// Schema v3: tag the overlay under `<registry>/<deploy-name>:
	// latest`. See tagDeployAlias.
	return t.tagDeployAlias(opts)
}

// readImageRegistry reads the org.overthinkos.registry OCI label from
// an image. Used by the schema-v3 alias tagging to preserve the
// registry prefix the quadlet generator expects.
func readImageRegistry(engine, imageRef string) string {
	out, err := exec.Command(engine, "inspect", "--format", "{{index .Config.Labels \"org.overthinkos.registry\"}}", imageRef).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// filterPlansByLayers returns only the plans whose Layer is in names.
func filterPlansByLayers(plans []*InstallPlan, names []string) []*InstallPlan {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []*InstallPlan
	for _, p := range plans {
		if want[p.Layer] {
			out = append(out, p)
		}
	}
	return out
}

// overlayTagFor computes a deterministic short tag from the base image
// ref + the (sorted) overlay layer set. Same inputs → same tag, so
// re-deploys of the same config don't churn overlay images.
func overlayTagFor(base string, layers []string) string {
	sorted := append([]string(nil), layers...)
	sortStrings(sorted)
	h := sha256.New()
	h.Write([]byte(base))
	h.Write([]byte{0})
	for _, l := range sorted {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (t *PodDeployTarget) stderr() *os.File {
	if t.DryRunWriter != nil {
		return t.DryRunWriter
	}
	return os.Stderr
}

// RemoveOverlayImage removes the overlay image produced by Emit. Used
// at `ov deploy del` time unless --keep-image is set.
func (t *PodDeployTarget) RemoveOverlayImage(opts EmitOpts) error {
	if t.overlayImageRef == "" || t.overlayImageRef == t.BaseImage {
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] %s rmi %s\n", t.Engine, t.overlayImageRef)
		return nil
	}
	cmd := exec.CommandContext(context.Background(), t.Engine, "rmi", t.overlayImageRef)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
