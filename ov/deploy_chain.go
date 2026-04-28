package main

import (
	"fmt"
	"sort"
	"strings"
)

// deploy_chain.go — single source of truth for "given a dotted deployment
// path, what DeployExecutor chain reaches the leaf?".
//
// Pre-cutover (2026-04), four call sites built executor chains (or partial
// chains) independently:
//   - ov deploy add  → deriveChildExecutorForPath in deploy_add_cmd.go
//   - ov test <name> → ad-hoc construction in test_cmd.go runHost/runContainer/runVm
//   - ov test parent.child → resolveNestedNode + a *flat* VmTestExecutor
//                            (silent single-hop bug — leaf tests ran on the
//                            parent VM via SSH instead of inside the leaf pod)
//   - ov harness     → hardcoded ContainerExecutor{ContainerName: "ov-"+pod}
//                      (single-hop only; could not reach pod-in-vm)
//
// Post-cutover, every call site routes through ResolveDeployChain. The
// function walks the deployment tree segment by segment and stacks one
// NestedExecutor hop per segment that needs a substrate change. Result:
// arbitrary-depth chains (host → ssh-vm → podman-exec-pod → podman-exec-
// nested-pod) work uniformly across deploy, test, and harness.

// ResolveDeployChain walks `dotted` through `roots` (typically the merged
// deployment tree from resolveTreeRoot) and returns the leaf
// DeploymentNode + a composed DeployExecutor chain that reaches it from
// `root`.
//
// `root` is typically &LocalDeployExecutor{} (the operator's host, or
// the inside-of-eval-pod context the harness loop runs in). Pass nil to
// substitute LocalDeployExecutor.
//
// For each path segment, a single hop is added based on the node's
// target classification:
//
//	target: pod / container → NestedExecutor with JumpPodmanExec /
//	                          JumpDockerExec into "ov-<flat-path>".
//	                          Container name flattens dot-separated
//	                          paths to underscore-separated to remain
//	                          a legal podman container name.
//	target: vm              → plain SSHExecutor when the parent chain
//	                          is local (no wrapper overhead), otherwise
//	                          NestedExecutor with JumpSSH on top.
//	target: host            → no hop (host nodes share the parent
//	                          venue).
//	target: k8s             → error (k8s manifests are leaves; not
//	                          traversable as exec targets).
//
// Returns clear errors with available-name hints when a segment fails
// to resolve.
func ResolveDeployChain(roots map[string]DeploymentNode, dotted string, root DeployExecutor) (*DeploymentNode, DeployExecutor, error) {
	if dotted == "" {
		return nil, nil, fmt.Errorf("ResolveDeployChain: empty path")
	}
	if root == nil {
		root = LocalDeployExecutor{}
	}
	parts := strings.Split(dotted, ".")

	// Resolve the root segment.
	rootEntry, ok := roots[parts[0]]
	if !ok {
		return nil, nil, fmt.Errorf("deployment %q not found%s", parts[0], didYouMeanDeploy(parts[0], roots))
	}

	chain := root
	current := &rootEntry

	// Hop into the root segment's substrate.
	next, err := appendHopForNode(chain, current, parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("entering %q: %w", parts[0], err)
	}
	chain = next

	// Walk remaining segments, stacking one hop per segment.
	for i, seg := range parts[1:] {
		traversed := strings.Join(parts[:i+1], ".")
		if current.Nested == nil || len(current.Nested) == 0 {
			return nil, nil, fmt.Errorf("path %q: %q has no nested children", dotted, traversed)
		}
		child, ok := current.Nested[seg]
		if !ok || child == nil {
			return nil, nil, fmt.Errorf("path %q: nested child %q not found under %q%s",
				dotted, seg, traversed, didYouMeanNestedChild(seg, current.Nested))
		}
		current = child
		// Container names flatten the FULL path so far (parts[:i+2]).
		flatPath := strings.Join(parts[:i+2], "_")
		next, err := appendHopForFlatPath(chain, current, flatPath)
		if err != nil {
			return nil, nil, fmt.Errorf("entering %q: %w", strings.Join(parts[:i+2], "."), err)
		}
		chain = next
	}

	return current, chain, nil
}

// appendHopForNode is the root-segment variant — uses `name` for the
// container target (no flattening needed at the root).
func appendHopForNode(chain DeployExecutor, node *DeploymentNode, name string) (DeployExecutor, error) {
	return appendHopForFlatPath(chain, node, name)
}

// appendHopForFlatPath stacks one executor hop so commands land inside
// `node`'s substrate. flatPath is the dotted path with dots replaced by
// underscores — used as the container name suffix.
func appendHopForFlatPath(chain DeployExecutor, node *DeploymentNode, flatPath string) (DeployExecutor, error) {
	switch classifyTarget(node) {
	case "host":
		// Host nodes share the parent venue. No new hop.
		return chain, nil

	case "pod":
		// Container name convention: "ov-<flat-path>". Matches the
		// harness's pre-cutover ContainerName construction
		// ("ov-"+pod) and the deploy convention used by quadlet
		// emission. Nested pods get "ov-<seg1>_<seg2>".
		name := "ov-" + flatPath
		engineJump := JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = JumpDockerExec
		}
		return &NestedExecutor{
			Parent: chain,
			Jump:   NestedJump{Kind: engineJump, Target: name},
		}, nil

	case "vm":
		ssh, err := sshParamsForVm(node)
		if err != nil {
			return nil, err
		}
		// If the parent chain is just LocalDeployExecutor, return a
		// plain SSHExecutor — no NestedExecutor wrapper needed.
		if _, isLocal := chain.(LocalDeployExecutor); isLocal {
			return ssh, nil
		}
		// Nested VM (inside a container or another VM): stack a JumpSSH.
		return &NestedExecutor{
			Parent: chain,
			Jump: NestedJump{
				Kind:       JumpSSH,
				Target:     fmt.Sprintf("%s@%s:%d", ssh.User, ssh.Host, ssh.Port),
				SSHKeyPath: ssh.KeyPath,
			},
		}, nil

	case "k8s":
		return nil, fmt.Errorf("k8s targets cannot be reached via the deploy chain (use kubectl)")
	}
	return nil, fmt.Errorf("unknown target %q on node", classifyTarget(node))
}

// ContainerChain returns a one-hop chain that exec's into a single named
// running container (`<engine> exec <name> bash`). Convenience for the
// simple `ov test <name>` path where there is no nested dotted path to
// walk — equivalent to ResolveDeployChain on a single-segment dotted
// path that resolves to a pod node, but skips the tree lookup.
func ContainerChain(engine, containerName string) DeployExecutor {
	jumpKind := JumpPodmanExec
	if engine == "docker" {
		jumpKind = JumpDockerExec
	}
	return &NestedExecutor{
		Parent: LocalDeployExecutor{},
		Jump:   NestedJump{Kind: jumpKind, Target: containerName},
	}
}

// ImageChain builds a one-hop chain that runs commands inside a freshly-
// spawned disposable container (`<engine> run --rm <imageRef> bash`).
// Used by `ov image test --section image` to replace the deleted
// ImageExecutor — same semantics, expressed via the unified chain
// primitive.
//
// engine defaults to "podman" when empty. imageRef is passed verbatim
// (full registry refs and short local names both work as long as
// `<engine> run --rm` accepts them).
func ImageChain(engine, imageRef string) DeployExecutor {
	jumpKind := JumpPodmanRun
	if engine == "docker" {
		jumpKind = JumpDockerRun
	}
	return &NestedExecutor{
		Parent: LocalDeployExecutor{},
		Jump:   NestedJump{Kind: jumpKind, Target: imageRef},
	}
}

// didYouMeanDeploy returns a "; available deployments: a, b, c" hint
// listing top-level deploy names sorted alphabetically. Empty when no
// candidates exist.
func didYouMeanDeploy(missed string, roots map[string]DeploymentNode) string {
	_ = missed // reserved for future fuzzy-matching
	if len(roots) == 0 {
		return ""
	}
	names := make([]string, 0, len(roots))
	for k := range roots {
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > 8 {
		names = append(names[:8], "…")
	}
	return "; available deployments: " + strings.Join(names, ", ")
}

// didYouMeanNestedChild renders a hint listing nested child keys under
// a given node. Empty when the parent has no nested children.
func didYouMeanNestedChild(missed string, nested map[string]*DeploymentNode) string {
	_ = missed
	if len(nested) == 0 {
		return ""
	}
	names := make([]string, 0, len(nested))
	for k := range nested {
		names = append(names, k)
	}
	sort.Strings(names)
	return "; available nested children: " + strings.Join(names, ", ")
}
