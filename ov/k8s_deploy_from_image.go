package main

import (
	"fmt"
	"path/filepath"
)

// -----------------------------------------------------------------------------
// Source-less K8s deploy — Part F.10.
//
// `ov deploy from-image <registry-ref> [name]` can deploy to K8s without any
// access to the repo's overthink.yml. Capabilities come from the pushed OCI
// image's org.overthinkos.* labels; runtime choices come from (per-machine
// ~/.config/ov/deploy.yml, cluster profile). This proves the self-contained
// image invariant from Part G.
// -----------------------------------------------------------------------------

// DeployFromImageOpts carries the source-less-deploy inputs.
type DeployFromImageOpts struct {
	Engine         string // "podman" | "docker" (auto-detected if empty)
	ImageRef       string // fully-qualified registry/name:tag
	DeploymentName string // optional override; defaults to the basename of ImageRef without tag
	Instance       string // optional "image/instance" suffix
	ClusterName    string // cluster profile name (ClusterProfile.Name)
	Namespace      string // optional override of cluster profile's default namespace
	DeployOverlay  *DeployImageConfig // optional: merged from ~/.config/ov/deploy.yml
	OutputDir      string // defaults to <cwd>/.overthink/k8s
	ProjectDir     string // for looking up clusters/<name>.yaml
}

// DeployFromImage performs the source-less deploy. Returns the absolute path
// to the Kustomize overlay directory produced (the argument to
// `kubectl apply -k`).
func DeployFromImage(opts DeployFromImageOpts) (string, error) {
	if opts.ImageRef == "" {
		return "", fmt.Errorf("image ref is required")
	}
	if opts.ClusterName == "" {
		return "", fmt.Errorf("--cluster is required")
	}

	// 1. Pull capabilities from OCI labels.
	engine := opts.Engine
	if engine == "" {
		engine = "podman"
	}
	caps, err := CapabilitiesFromLabels(engine, opts.ImageRef)
	if err != nil {
		return "", fmt.Errorf("reading capabilities from %q: %w", opts.ImageRef, err)
	}

	// 2. Load cluster profile.
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	cluster, err := LoadClusterProfile(projectDir, opts.ClusterName)
	if err != nil {
		return "", fmt.Errorf("loading cluster profile: %w", err)
	}

	// 3. Derive deployment name if not provided (use image basename without tag).
	deployName := opts.DeploymentName
	if deployName == "" {
		deployName = deriveDeploymentName(opts.ImageRef)
	}

	// 4. Build the deployment spec from the per-machine overlay if any.
	dc := DeployImageConfig{
		Target: "kubernetes",
	}
	if opts.DeployOverlay != nil {
		dc = *opts.DeployOverlay
		dc.Target = "kubernetes"
	}
	if dc.Kubernetes == nil {
		dc.Kubernetes = &K8sDeployConfig{}
	}
	dc.Kubernetes.Cluster = opts.ClusterName
	if opts.Namespace != "" {
		dc.Kubernetes.Namespace = opts.Namespace
	}

	// 5. Resolve output dir.
	outDir := opts.OutputDir
	if outDir == "" {
		outDir = filepath.Join(projectDir, ".overthink", "k8s")
	}

	// 6. Generate.
	return GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: deployName,
		Instance:       opts.Instance,
		ImageRef:       opts.ImageRef,
		Deployment:     dc,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
}

// deriveDeploymentName turns "quay.io/myorg/openclaw:v1" → "openclaw" and
// "registry.example.com/path/foo" → "foo".
func deriveDeploymentName(imageRef string) string {
	// Strip tag.
	ref := imageRef
	if idx := lastIndexByteInRef(ref, ':'); idx >= 0 {
		ref = ref[:idx]
	}
	// Return last path component.
	if idx := lastIndexByteInRef(ref, '/'); idx >= 0 {
		return ref[idx+1:]
	}
	return ref
}

// lastIndexByteInRef returns the last index of c in s, ignoring any '/' that
// appears after a port number in a registry host (e.g., "localhost:5000/foo:v1"
// should not treat the ":5000" colon as a tag boundary). Simple heuristic:
// return last ':' only if it appears after the last '/'.
func lastIndexByteInRef(s string, c byte) int {
	lastSlash := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			lastSlash = i
		}
	}
	last := -1
	start := 0
	if c == ':' {
		start = lastSlash + 1 // only look after final path segment for tag
	}
	for i := start; i < len(s); i++ {
		if s[i] == c {
			last = i
		}
	}
	return last
}
