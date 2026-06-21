package main

import (
	"context"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// K8sCollector is the kubernetes SubstrateCollector. It surfaces every
// declared `target: k8s` deployment (from the folded charly.yml Deploy
// map) as one DeploymentStatus row, reporting whether its Kustomize tree has
// been generated under .opencharly/k8s/<name>/ and which cluster/context the
// referenced kind:k8s template points at.
//
// Provenance is Source="tree": k8s deploys do not run a container on this
// host — they emit a manifest tree that `charly bundle sync` / `kubectl apply -k`
// applies to a remote cluster. The flat (non-nested) view therefore reports
// generation state, never live pod health. Under opts.Nested it additionally
// queries the live cluster (via the same vendored client-go subset the
// `charly check kube` verbs use) for the workload's readiness and refines Status
// to running / not-ready / unreachable.
type K8sCollector struct {
	c *Collector
}

func init() {
	registerSubstrate(func(c *Collector) SubstrateCollector { return &K8sCollector{c: c} })
}

// Kind reports the k8s substrate.
func (k *K8sCollector) Kind() SubstrateKind { return SubstrateK8s }

// Available reports true when this host has any k8s footprint: either the
// .opencharly/k8s tree directory exists, or at least one target:k8s deploy is
// declared in the projected charly.yml. An absent unified projection plus
// an absent tree dir means there is nothing to report — skipped silently.
func (k *K8sCollector) Available(opts CollectOpts) bool {
	if dir, err := k8sTreeRoot(); err == nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return true
		}
	}
	return len(k8sDeployEntries(opts.Unified)) > 0
}

// Collect emits one row per declared target:k8s deploy. The flat view stats
// .opencharly/k8s/<name>/ (tree-present | not-generated) and reads
// cluster/context from the referenced kind:k8s template. Under opts.Nested it
// upgrades the status with a live cluster readiness probe.
func (k *K8sCollector) Collect(ctx context.Context, opts CollectOpts) ([]DeploymentStatus, error) {
	entries := k8sDeployEntries(opts.Unified)
	if len(entries) == 0 {
		return nil, nil
	}
	treeRoot, rootErr := k8sTreeRoot()

	rows := make([]DeploymentStatus, 0, len(entries))
	for _, name := range entries {
		node := opts.Unified.Bundle[name]

		row := DeploymentStatus{
			Kind:      SubstrateK8s,
			Source:    "tree",
			Image:     k8sImageRef(name, node),
			Container: name,
			RunMode:   opts.RunMode,
		}

		// Tree-present detection: <treeRoot>/<name>/ stats successfully iff
		// `charly bundle add` has generated the Kustomize tree.
		treePresent := false
		if rootErr == nil {
			if _, err := os.Stat(filepath.Join(treeRoot, name)); err == nil {
				treePresent = true
			}
		}
		if treePresent {
			row.Status = "tree-present"
		} else {
			row.Status = "not-generated"
		}

		// Cluster/context from the referenced kind:k8s template. Network is
		// the contextual cell used to show where the workload points.
		spec := k8sSpecFor(opts.Unified, node)
		if spec != nil && spec.KubeconfigContext != "" {
			row.Network = spec.KubeconfigContext
		} else if node.K8s != "" {
			row.Network = node.K8s
		}

		// Live readiness — only under --nested, only when a tree exists and a
		// cluster context is known. Failure degrades to the tree-state status
		// (never aborts the row).
		if opts.Nested && treePresent && spec != nil {
			if live, ok := k8sLiveStatus(ctx, name, node, spec); ok {
				row.Status = live
			}
		}

		rows = append(rows, row)
	}
	return rows, nil
}

// k8sTreeRoot returns <cwd>/.opencharly/k8s — the canonical root that
// defaultK8sOutputDir (deploy_add_cmd_k8s.go) emits Kustomize trees under.
func k8sTreeRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}

// k8sDeployEntries returns the names of every target:k8s deploy in the folded
// Bundle map, in deterministic (map-key) order via the shared classifier so
// legacy "kubernetes" spellings resolve identically to "k8s".
func k8sDeployEntries(uf *UnifiedFile) []string {
	if uf == nil || uf.Bundle == nil {
		return nil
	}
	var names []string
	for name, node := range uf.Bundle {
		n := node
		if classifyNodeTarget(&n, name) == "k8s" {
			names = append(names, name)
		}
	}
	return names
}

// k8sImageRef resolves the image a k8s deploy runs, mirroring
// K8sUnifiedTarget.Add: the node's explicit Box, falling back to the
// deploy name.
func k8sImageRef(name string, node BundleNode) string {
	if node.Box != "" {
		return node.Box
	}
	return name
}

// k8sSpecFor resolves the kind:k8s template referenced by node.K8s from the
// unified projection. Nil when unreferenced or absent.
func k8sSpecFor(uf *UnifiedFile, node BundleNode) *K8sSpec {
	if uf == nil || uf.K8s == nil || node.K8s == "" {
		return nil
	}
	return uf.K8s[node.K8s]
}

// k8sLiveStatus probes the live cluster for the deploy's workload readiness,
// reusing the same client-go subset and predicates the `charly check kube` verbs
// use. Returns ("running"|"not-ready", true) on a successful query, or
// ("unreachable", true) when the cluster can't be reached; ("", false) only
// when no workload GVR can be derived. Never panics, never blocks beyond the
// dynamic client's own dialing.
func k8sLiveStatus(ctx context.Context, name string, node BundleNode, spec *K8sSpec) (string, bool) {
	gvr, ok := kindToGVR(k8sWorkloadKind(node))
	if !ok {
		return "", false
	}
	flags := &k8sClusterFlags{Context: spec.KubeconfigContext}
	client, err := flags.dynamicClient()
	if err != nil {
		return "unreachable", true
	}
	ns := k8sWorkloadNamespace(node, spec)
	u, err := client.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "unreachable", true
	}
	if workloadReady(u) {
		return "running", true
	}
	return "not-ready", true
}

// k8sWorkloadKind derives the workload kind for a deploy node using the same
// heuristic GenerateK8sKustomize applies (service+storage → StatefulSet, etc.),
// so the live probe targets the SAME resource that was generated.
func k8sWorkloadKind(node BundleNode) string {
	return selectWorkloadKind(K8sGenerateOpts{Deploy: node})
}

// k8sWorkloadNamespace resolves the workload namespace exactly as
// deployNamespace does (deploy override → template default → "default").
func k8sWorkloadNamespace(node BundleNode, spec *K8sSpec) string {
	ns := deployNamespace(K8sGenerateOpts{Deploy: node, Cluster: spec})
	if ns == "" {
		ns = "default"
	}
	return ns
}
