package main

// Deploy-name resolution + per-target dispatch for `ov update`.
//
// Added 2026-05-09 in the rebuild→update cutover. Before this, `ov
// update <image>` only handled image-name input and only worked for
// pod targets. The deleted `ov rebuild` covered VM/local targets via
// the *UnifiedTarget.Rebuild methods. This file consolidates that
// dispatch into UpdateCmd so the user-facing surface is just one verb.
//
// Critical semantic: NONE of the dispatchers below regenerate the
// user-overlay deploy entry (no `ov deploy add` / `ov config` calls
// allowed in the pod path). The user's directive: "Any config changes
// should be done via ov config only." This verb updates ARTIFACTS
// (image bits, VM disk, local layers, quadlet/marker image refs);
// `ov config` updates CONFIG. The two responsibilities are strictly
// separated.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// dispatchByDeployTarget resolves c.Image as a deploy.yml entry and
// invokes the target-specific update helper. Errors explicitly when:
//
//   - cwd has no deploy.yml (use 'ov image pull' for image-only refresh)
//   - the name doesn't resolve to a deploy entry (same)
//   - the deploy entry's `image:` field is empty for pod targets
//     (config bug — deploy needs to know which image to refresh)
//   - target is unknown / unsupported (k8s)
//
// No silent fallbacks. The user gets a clear error pointing at the
// right alternative or the field they need to fix.
// resolveUpdateDeployNode looks up the deploy entry for an `ov update`
// invocation by the FULL deploy key. deployKey applies the -i instance,
// returning the bare (or dotted-nested) name unchanged when instance is
// empty — so `ov update <base> -i <inst>` finds the instance-keyed
// `<base>/<inst>` entry, plain names still resolve, and dotted nested
// paths (`a.b.c`) still walk. Mirrors the composition `ov start` uses via
// dc.Lookup(c.Image, c.Instance). On miss the error reports the full key.
func resolveUpdateDeployNode(tree map[string]DeploymentNode, image, instance string) (*DeploymentNode, error) {
	key := deployKey(image, instance)
	node, _, err := ResolveNodePath(tree, key)
	if err != nil || node == nil {
		return nil, fmt.Errorf("no deploy named %q in deploy.yml. To refresh an image artifact only, use 'ov image pull %s'", key, image)
	}
	return node, nil
}

func (c *UpdateCmd) dispatchByDeployTarget() error {
	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	tree, err := resolveTreeRoot(dir)
	if err != nil {
		return fmt.Errorf("loading deploy tree from %s: %w", dir, err)
	}
	if tree == nil {
		return fmt.Errorf("no deploy.yml found relative to %s; ov update requires a deploy name. To refresh an image artifact only, use 'ov image pull %s'", dir, c.Image)
	}
	node, err := resolveUpdateDeployNode(tree, c.Image, c.Instance)
	if err != nil {
		return err
	}

	// Enforce disposable-only autonomy: ov update destroys + recreates
	// the deploy unattended, so the only authorization for that is an
	// explicit `disposable: true` on the deploy entry (or `ephemeral:`,
	// which implies disposability — see IsDisposable() + /ov-internals:
	// disposable "the ephemeral exception"). Lifecycle tags alone do
	// NOT authorize — this is the anti-derivation invariant. Refusing
	// here protects shared-host production deploys from accidental
	// destroy-on-update.
	if err := checkUpdateDisposable(node, c.Image, c.Instance); err != nil {
		return err
	}

	// Normalize the target. Empty / "container" both mean "pod".
	target := node.Target
	if target == "" || target == "container" {
		target = "pod"
	}
	deployName := c.Image

	switch target {
	case "vm":
		return c.updateVmDeploy(deployName)
	case "local":
		return c.updateLocalDeploy(deployName)
	case "pod":
		if node.Image == "" {
			return fmt.Errorf("deploy %q has no 'image:' field in deploy.yml. ov update needs to know which image artifact to refresh; add 'image: <name>' to the deploy entry", deployName)
		}
		return c.updatePodDeploy(deployName, node.Image)
	case "k8s":
		return fmt.Errorf("ov update %s: k8s target updates are managed via kubectl apply on the rendered Kustomize overlay", deployName)
	default:
		return fmt.Errorf("ov update %s: unknown target %q", deployName, target)
	}
}

// updatePodDeploy refreshes a pod-target deploy's image without
// touching ANY user-overlay deploy.yml state. Steps (mode-agnostic
// prefix; mode-specific suffix):
//
//  1. Optionally rebuild the base image locally (--build).
//  2. Pull the latest base image (EnsureImage).
//  3. Sync data from the new image into bind-backed volumes (--seed).
//  4. Bump the per-deploy alias tag in podman storage so subsequent
//     short-name resolution (ResolveNewestLocalCalVer for the
//     deploy-name) finds the new content.
//  5. (quadlet) Rewrite ONLY the quadlet's `Image=` line to the new
//     alias ref. PublishPort, Volume, Env, security args — preserved
//     verbatim. systemctl daemon-reload + restart to pick up new image.
//  6. (direct) Update marker.ImageRef to the new alias ref. stop +
//     rm container. Re-invoke `ov start <deploy>` which reads the
//     marker and runs podman/docker with the new image.
//
// What this verb DOES NOT DO: call `ov config`, call `ov deploy add`,
// or write to deploy.yml. User-overlay state (operator port overrides,
// env additions, volume bindings, security overrides) is inviolable.
func (c *UpdateCmd) updatePodDeploy(deployName, imageName string) error {
	// imageName MUST be non-empty by contract — the dispatcher errors
	// out before reaching here when the deploy's `image:` field is
	// missing. No silent fallback to deployName (that masks the
	// underlying config bug and produces confusing image-resolution
	// behavior, as the 2026-05-09 stale-alias incident demonstrated).

	// Optionally rebuild the image locally before swapping it in.
	if c.Build {
		if err := updateCmdBuildFn(imageName, c.Tag); err != nil {
			return fmt.Errorf("building %s: %w", imageName, err)
		}
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	runEngine := ResolveImageEngineForDeploy(imageName, c.Instance, rt.RunEngine)

	// Resolve the base image ref. When c.Tag is empty (the common case),
	// `fmt.Sprintf("%s:%s", name, "")` produces `name:` which is INVALID
	// podman input — podman returns an error and ExtractMetadata fails.
	// resolveShellImageRef handles the empty-tag case by resolving to
	// the newest local CalVer via ResolveNewestLocalCalVer + the OCI
	// version label. Run it FIRST to get a real tag, then use that for
	// label extraction and the registry-qualified second resolution.
	imageRef := resolveShellImageRef("", imageName, c.Tag)
	meta, metaErr := ExtractMetadata(runEngine, imageRef)
	if metaErr == nil && meta != nil && meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, imageName, c.Tag)
	}

	// Pull/build the base image. Use the resolved engine for
	// consistency with deploy-time semantics (podman or docker).
	imageRT := &ResolvedRuntime{BuildEngine: rt.BuildEngine, RunEngine: runEngine}
	if err := EnsureImage(imageRef, imageRT); err != nil {
		return err
	}
	if c.Seed {
		c.syncData(runEngine, imageRef, meta, rt)
	}

	// Re-resolve the imageRef after EnsureImage in case it pulled (or
	// built — see updateCmdBuildFn fallback) a NEW CalVer-tagged image
	// that ResolveNewestLocalCalVer would now pick.
	if meta != nil && meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, imageName, c.Tag)
	} else {
		imageRef = resolveShellImageRef("", imageName, c.Tag)
	}

	// Bump the per-deploy alias to point at the freshly-pulled base.
	// The alias is what `ov config <deployName>` and the quadlet's
	// `Image=` line reference via ResolveNewestLocalCalVer. Without
	// this bump, the quadlet stays pinned at the OLD-CalVer alias from
	// the initial `ov deploy add` and a systemctl restart picks up no
	// new content.
	aliasRef, err := bumpDeployAlias(runEngine, imageRef, deployName, meta)
	if err != nil {
		return fmt.Errorf("bump deploy alias: %w", err)
	}
	if aliasRef != imageRef {
		fmt.Fprintf(os.Stderr, "Tagged %s → %s (deploy alias for %s)\n",
			imageRef, aliasRef, deployName)
	}

	// Mode dispatch: quadlet (systemd-user host) vs direct (no systemd).
	if rt.RunMode == "quadlet" {
		return c.updatePodDeployQuadlet(rt, deployName, aliasRef)
	}
	return c.updatePodDeployDirect(runEngine, deployName, aliasRef)
}

// updatePodDeployQuadlet handles the quadlet (systemd-user) restart
// path. Rewrites the quadlet's `Image=` line surgically, then refreshes
// every deployed quadlet's `Environment=` block so env_provides /
// env_accepts changes baked into the new image's OCI labels (e.g. a
// newly-declared producer URL or a newly-accepted consumer var) land in
// the runtime container env on restart.
//
// `Environment=` is a DERIVED value — it's the resolved cross-pod
// service-discovery view at deploy time, NOT operator-authored state.
// User-overlay state (operator port overrides, env additions, volume
// bindings, security overrides) lives in `~/.config/ov/deploy.yml` and
// is read by the quadlet regeneration step, so it is preserved by the
// refresh. The only thing the refresh loses are CLI `-e` flags passed
// once at the original `ov config` call — those were never persisted
// anywhere and would also be lost by a manual `ov config --update-all`.
func (c *UpdateCmd) updatePodDeployQuadlet(rt *ResolvedRuntime, deployName, newImageRef string) error {
	qpath, err := quadletPathForDeploy(deployName, c.Instance)
	if err != nil {
		return fmt.Errorf("locating quadlet for %s: %w", deployName, err)
	}
	if err := rewriteQuadletImageLine(qpath, newImageRef); err != nil {
		return fmt.Errorf("rewriting %s: %w", qpath, err)
	}
	fmt.Fprintf(os.Stderr, "Updated %s Image=%s\n", qpath, newImageRef)

	// Refresh every quadlet's Environment= block from current image
	// labels + deploy.yml provides registry. Picks up env_provides /
	// env_accepts changes baked into the just-built image. Also runs
	// systemctl --user daemon-reload internally, so the explicit reload
	// below is skipped on the success path.
	if err := updateAllDeployedQuadlets(rt, ""); err != nil {
		// Non-fatal — fall back to a plain daemon-reload + restart.
		// updateAllDeployedQuadlets logs the per-deploy failure itself.
		fmt.Fprintf(os.Stderr, "Warning: env refresh failed: %v\n", err)
		if reloadErr := exec.Command("systemctl", "--user", "daemon-reload").Run(); reloadErr != nil {
			return fmt.Errorf("systemctl --user daemon-reload: %w", reloadErr)
		}
	}

	svc := serviceNameInstance(deployName, c.Instance)
	check := exec.Command("systemctl", "--user", "is-active", svc)
	if err := check.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Service %s is not active, skipping restart\n", svc)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Restarting %s (deploy %s, image %s)\n",
		svc, deployName, newImageRef)
	restart := exec.Command("systemctl", "--user", "restart", svc)
	restart.Stdout = os.Stdout
	restart.Stderr = os.Stderr
	if err := restart.Run(); err != nil {
		return fmt.Errorf("restarting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
	return nil
}

// updatePodDeployDirect handles the direct-mode (no systemd) restart
// path for podman or docker engines. Updates the marker JSON's
// ImageRef field, stops + removes the existing container, then
// invokes `ov start <deploy>` which reads the marker (now pointing
// at the new image) and re-runs the engine with the same args.
//
// `ov start` is safe to invoke here because it does not mutate
// user-overlay deploy.yml state — it only reads deploy.yml + the
// marker to construct the run command.
func (c *UpdateCmd) updatePodDeployDirect(engine, deployName, newImageRef string) error {
	if err := updateDirectMarkerImageRef(deployName, c.Instance, newImageRef); err != nil {
		return fmt.Errorf("updating direct-mode marker for %s: %w", deployName, err)
	}
	fmt.Fprintf(os.Stderr, "Updated direct-mode marker for %s → image=%s\n",
		deployName, newImageRef)

	name := containerNameInstance(deployName, c.Instance)
	// Best-effort stop + rm of the existing container. Errors are
	// non-fatal — the container may already be stopped or absent.
	_ = exec.Command(engine, "stop", name).Run()
	_ = exec.Command(engine, "rm", "-f", name).Run()
	fmt.Fprintf(os.Stderr, "Stopped + removed %s\n", name)

	// Re-invoke `ov start <deploy>` which reads the (now-updated)
	// marker + deploy.yml and runs the engine with all preserved args.
	args := []string{"start", deployName}
	if c.Instance != "" {
		args = append(args, "-i", c.Instance)
	}
	if err := runOvSubcommand(args...); err != nil {
		return fmt.Errorf("ov start %s: %w", deployName, err)
	}
	return nil
}

// bumpDeployAlias re-tags the freshly-pulled base image under the
// per-deploy alias name (`<registry>/<deployName>:<calver-from-baseRef>`)
// so subsequent ResolveNewestLocalCalVer(deployName) finds the new
// content. The alias mechanism (deploy_target_pod.go:tagDeployAlias)
// is what allows `ov config <deployName>` and the quadlet `Image=`
// line to resolve the right image when deploy-name differs from
// image-name (e.g. `versa` deploy → `versa` image; cross-kind name reuse).
//
// Returns the resolved alias ref (or baseRef itself when no aliasing
// is needed because deploy-name equals image-name). The CalVer tag is
// extracted from baseRef so the alias tracks the actual base content,
// not wall-clock time.
func bumpDeployAlias(runEngine, baseRef, deployName string, meta *ImageMetadata) (string, error) {
	calver, err := tagPart(baseRef)
	if err != nil {
		return "", err
	}
	registry := ""
	if meta != nil && meta.Registry != "" {
		registry = meta.Registry
	} else if i := strings.LastIndex(baseRef, "/"); i > 0 {
		// Fallback: extract registry portion from baseRef itself.
		registry = baseRef[:i]
	}
	var aliasRef string
	if registry != "" {
		aliasRef = fmt.Sprintf("%s/%s:%s", registry, deployName, calver)
	} else {
		aliasRef = fmt.Sprintf("%s:%s", deployName, calver)
	}
	if aliasRef == baseRef {
		// deploy-name == image-name; no aliasing needed.
		return aliasRef, nil
	}
	cmd := exec.Command(runEngine, "tag", baseRef, aliasRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s tag %s %s: %w (%s)",
			runEngine, baseRef, aliasRef, err, strings.TrimSpace(string(out)))
	}
	return aliasRef, nil
}

// tagPart extracts the tag portion of an image ref. Handles both
// `<image>:<tag>` and `<registry>/<image>:<tag>` forms; refuses refs
// without an explicit tag (an empty `:<tag>` would be invalid podman
// input — caller should resolve to a CalVer first via
// resolveShellImageRef).
func tagPart(ref string) (string, error) {
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon == -1 || lastColon < lastSlash {
		// No tag at all, or the colon is in a host:port portion of
		// the registry (e.g. `localhost:5000/myimage` — no tag).
		return "", fmt.Errorf("image ref %q has no tag", ref)
	}
	tag := ref[lastColon+1:]
	if tag == "" {
		return "", fmt.Errorf("image ref %q has empty tag", ref)
	}
	return tag, nil
}

// quadletPathForDeploy returns the absolute path to the quadlet `.container`
// file for a deploy + optional instance.
func quadletPathForDeploy(deployName, instance string) (string, error) {
	qdir, err := quadletDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(qdir, quadletFilenameInstance(deployName, instance)), nil
}

// quadletImageLineRe matches the `Image=<value>` directive on its own
// line in a quadlet `.container` file. Multi-line mode (`(?m)`) anchors
// `^` / `$` at line boundaries.
var quadletImageLineRe = regexp.MustCompile(`(?m)^Image=.*$`)

// extractQuadletImageLine returns the value of the `Image=<value>`
// directive in the quadlet at `path`. Returns ("", error) when the file
// cannot be read; returns ("", nil) when the file is readable but
// contains no Image= directive (caller decides whether to fall back).
// Used by updateAllDeployedQuadlets to preserve the operator-chosen
// image ref across cross-deploy quadlet refreshes — see the bug-fix
// note in that function for the cross-pollution scenario the bare
// resolveShellImageRef lookup falls victim to when a sibling deploy's
// alias tag has been re-tagged onto the base image.
func extractQuadletImageLine(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m := quadletImageLineRe.FindString(string(content))
	if m == "" {
		return "", nil
	}
	return strings.TrimPrefix(m, "Image="), nil
}

// rewriteQuadletImageLine replaces the `Image=<old>` line in the
// quadlet at `path` with `Image=<newRef>`. All other lines are
// preserved verbatim. Atomic write: writes to `<path>.new`, then
// renames.
func rewriteQuadletImageLine(path, newRef string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading quadlet %s: %w", path, err)
	}
	if !quadletImageLineRe.Match(content) {
		return fmt.Errorf("no Image= line found in quadlet %s", path)
	}
	newContent := quadletImageLineRe.ReplaceAll(content, []byte("Image="+newRef))
	tmp := path + ".new"
	if err := os.WriteFile(tmp, newContent, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming %s → %s: %w", tmp, path, err)
	}
	return nil
}

// updateDirectMarkerImageRef rewrites the `image_ref` field in the
// direct-mode marker JSON for a deploy. Used by `ov update` in direct
// mode so a subsequent `ov start` reads the new ref.
func updateDirectMarkerImageRef(deployName, instance, newRef string) error {
	m, err := readDirectDeployMarker(deployName, instance)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("no direct-mode marker for deploy %q (instance %q); was the deploy created in direct mode?", deployName, instance)
	}
	m.ImageRef = newRef
	return writeDirectDeployMarker(*m)
}

// updateVmDeploy delegates to VmUnifiedTarget.Rebuild, which destroys +
// recreates the libvirt domain and restarts it. WITHOUT --build the existing
// qcow2 disk is re-attached (guest filesystem — installed layers, podman
// storage — persists across the recreate); WITH --build (c.Build) the disk is
// rebuilt from scratch (`ov vm build`) for a genuinely-clean guest.
func (c *UpdateCmd) updateVmDeploy(deployName string) error {
	target := &VmUnifiedTarget{NodeName: deployName}
	if err := target.Rebuild(context.Background(), RebuildOpts{
		DryRun:       false,
		RebuildImage: c.Build,
	}); err != nil {
		return fmt.Errorf("ov update %s (vm target): %w", deployName, err)
	}
	return nil
}

// updateLocalDeploy delegates to LocalUnifiedTarget which re-applies
// layers idempotently (the install_template steps are idempotent by
// design — re-running adds nothing if everything is already present).
// Local target updates are inherently in-place because there's nothing
// to destroy: the host filesystem is the venue.
func (c *UpdateCmd) updateLocalDeploy(deployName string) error {
	target := &LocalUnifiedTarget{NodeName: deployName}
	if err := target.Rebuild(context.Background(), RebuildOpts{
		DryRun:       false,
		RebuildImage: false,
	}); err != nil {
		return fmt.Errorf("ov update %s (local target): %w", deployName, err)
	}
	return nil
}

// checkUpdateDisposable enforces the disposable-only autonomy invariant
// at the `ov update` entry point. Refuses with a remediation message
// that mirrors `/ov-internals:disposable`'s sample refusal text when
// the deploy node is not explicitly disposable (and not ephemeral —
// see IsDisposable() for the implication chain).
//
// Cross-kind name reuse is permitted, so the user-facing key for the
// remediation hint must include the instance suffix when present (the
// deployKey form matches what's in deploy.yml and what the user typed).
func checkUpdateDisposable(node *DeploymentNode, image, instance string) error {
	if node == nil || node.IsDisposable() {
		return nil
	}
	key := deployKey(image, instance)
	lifecycle := node.Lifecycle
	if lifecycle == "" {
		lifecycle = "(unset)"
	}
	addArg := image
	if instance != "" {
		addArg = key
	}
	return fmt.Errorf("ov update: %q is not marked `disposable: true` in deploy.yml (current lifecycle: %s).\n"+
		"  `ov update` only acts on explicitly disposable deploys — lifecycle tags alone do NOT authorize autonomous destroy.\n"+
		"  To opt in: edit deploy.yml and set `disposable: true` on the entry, or run: ov deploy add %s <ref> --disposable",
		key, lifecycle, addArg)
}
