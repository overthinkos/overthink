package main

// Deploy-name resolution + per-target dispatch for `charly update`.
//
// `charly update <name>` resolves a deploy name (VM/local/pod targets all
// dispatch from here) or a bare image name; this file consolidates the
// per-target dispatch into UpdateCmd so the user-facing surface is just
// one verb.
//
// Critical semantic: NONE of the dispatchers below regenerate the
// user-overlay deploy entry (no `charly deploy add` / `charly config` calls
// allowed in the pod path). The user's directive: "Any config changes
// should be done via charly config only." This verb updates ARTIFACTS
// (image bits, VM disk, local candies, quadlet/marker image refs);
// `charly config` updates CONFIG. The two responsibilities are strictly
// separated.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// dispatchByDeployTarget resolves c.Box as a charly.yml entry and
// invokes the target-specific update helper. Errors explicitly when:
//
//   - cwd has no charly.yml (use 'charly box pull' for image-only refresh)
//   - the name doesn't resolve to a deploy entry (same)
//   - the deploy entry's `box:` field is empty for pod targets
//     (config bug — deploy needs to know which image to refresh)
//   - target is unknown / unsupported (k8s)
//
// No silent fallbacks. The user gets a clear error pointing at the
// right alternative or the field they need to fix.
// resolveUpdateDeployNode looks up the deploy entry for an `charly update`
// invocation by the FULL deploy key. deployKey applies the -i instance,
// returning the bare (or dotted-nested) name unchanged when instance is
// empty — so `charly update <base> -i <inst>` finds the instance-keyed
// `<base>/<inst>` entry, plain names still resolve, and dotted nested
// paths (`a.b.c`) still walk. Mirrors the composition `charly start` uses via
// dc.Lookup(c.Box, c.Instance). On miss the error reports the full key.
func resolveUpdateDeployNode(tree map[string]DeploymentNode, image, instance string) (*DeploymentNode, error) {
	key := deployKey(image, instance)
	node, _, err := ResolveNodePath(tree, key)
	if err != nil || node == nil {
		return nil, fmt.Errorf("no deploy named %q in charly.yml. To refresh an image artifact only, use 'charly box pull %s'", key, image)
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
		return fmt.Errorf("no charly.yml found relative to %s; charly update requires a deploy name. To refresh an image artifact only, use 'charly box pull %s'", dir, c.Box)
	}
	node, err := resolveUpdateDeployNode(tree, c.Box, c.Instance)
	if err != nil {
		return err
	}

	// `charly update` obeys an EXPLICIT invocation on ANY target — the tool is
	// fully capable; the disposable-only constraint is a discipline on the AI's
	// AUTONOMOUS action (CLAUDE.md R10 + /charly-internals:disposable) and on the
	// check-runner's unattended fresh-rebuild (validateCheckBeds), NOT a capability
	// limit on this human-driven verb. For a non-disposable target we print a
	// one-line transparency note (the operator may have mistyped a name) and
	// proceed; we never refuse.
	noteUpdateDisposability(node, c.Box, c.Instance)

	// Normalize legacy target spellings before resolution. Empty / "container"
	// both mean "pod" (the schema invariant requires target:, so empty is only
	// pre-migration defensiveness).
	if node.Target == "" || node.Target == "container" {
		node.Target = "pod"
	}
	deployName := c.Box

	// UNIFIED dispatch — charly update for EVERY kind routes through the SAME
	// ResolveTarget → LifecycleTarget.Rebuild path; there is no per-kind update
	// code. Rebuild's contract is "redeploy the current artifact + restart"
	// (and, with --build, rebuild the artifact first); each kind's adapter
	// realizes it for its substrate (vm: destroy→create the domain, then
	// re-apply the deploy node's candies via deploy add; pod: deploy
	// add→config→start; local: re-apply candies). k8s has no live runtime
	// to rebuild (it is applied out-of-band via kubectl) so it is deliberately
	// NOT a LifecycleTarget and falls out here with a clear error.
	target, err := ResolveTarget(node, deployName)
	if err != nil {
		return err
	}
	lt, ok := target.(LifecycleTarget)
	if !ok {
		return fmt.Errorf("charly update %s: %q target has no live runtime to rebuild "+
			"(k8s is applied out-of-band via `kubectl apply -k` on the rendered Kustomize overlay)",
			deployName, node.Target)
	}
	return lt.Rebuild(context.Background(), RebuildOpts{RebuildImage: c.Build})
}

// bumpDeployAlias re-tags the freshly-pulled base image under the
// per-deploy alias name (`<registry>/<deployName>:<calver-from-baseRef>`)
// so subsequent ResolveNewestLocalCalVer(deployName) finds the new
// content. The alias mechanism (deploy_target_pod.go:tagDeployAlias)
// is what allows `charly config <deployName>` and the quadlet `Image=`
// line to resolve the right image when deploy-name differs from
// image-name (e.g. `versa` deploy → `versa` image; cross-kind name reuse).
//
// Returns the resolved alias ref (or baseRef itself when no aliasing
// is needed because deploy-name equals image-name). The CalVer tag is
// extracted from baseRef so the alias tracks the actual base content,
// not wall-clock time.
func bumpDeployAlias(runEngine, baseRef, deployName string, meta *BoxMetadata) (string, error) {
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
// note in that function for the cross-pollution case the bare
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

// noteUpdateDisposability prints a one-line transparency note when an EXPLICIT
// `charly update` targets a deploy that is NOT marked `disposable: true` (and not
// ephemeral — see IsDisposable() for the implication chain). It NEVER refuses:
// `charly update` is a human-driven verb that obeys any explicit invocation on any
// target. The `disposable:` flag remains load-bearing as the authorization for
// the AI's AUTONOMOUS destroy + rebuild (CLAUDE.md R10) and for the check-runner's
// unattended fresh-rebuild (validateCheckBeds) — it just no longer gates this
// command. The note lets an operator catch a mistyped name before the rebuild
// proceeds.
//
// Cross-kind name reuse is permitted, so the user-facing key includes the
// instance suffix when present (the deployKey form matches charly.yml + what the
// operator typed).
func noteUpdateDisposability(node *DeploymentNode, image, instance string) {
	if node == nil || node.IsDisposable() {
		return
	}
	key := deployKey(image, instance)
	lifecycle := node.Lifecycle
	if lifecycle == "" {
		lifecycle = "(unset)"
	}
	fmt.Fprintf(os.Stderr,
		"Note: %q is not marked `disposable: true` (lifecycle: %s); rebuilding it anyway per your explicit `charly update`.\n",
		key, lifecycle)
}
