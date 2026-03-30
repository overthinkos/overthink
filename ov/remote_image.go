package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// RemoteImageContext holds the resolved state of a remote image reference.
// It contains everything needed to pull/build and run the image.
type RemoteImageContext struct {
	Ref        ParsedRef
	CacheDir   string
	Config     *Config
	Resolved   *ResolvedImage
	Layers     map[string]*Layer
	ImageRef   string // registry/name:tag for pull
	ImageName  string // short name (e.g. "openclaw-browser")
}

// ResolveRemoteImage resolves a remote image reference to a full context.
// Format: @github.com/org/repo/image:version
func ResolveRemoteImage(ref string, tag string) (*RemoteImageContext, error) {
	parsed := ParseRemoteRef(ref)
	if parsed.RepoPath == "" || parsed.Name == "" {
		return nil, fmt.Errorf("invalid remote image ref %q: expected @github.com/org/repo/image:version", ref)
	}

	version := parsed.Version
	if version == "" {
		repoURL := RepoGitURL(parsed.RepoPath)
		tag, err := GitLatestTag(repoURL)
		if err != nil {
			return nil, fmt.Errorf("resolving latest version for %s: %w", parsed.RepoPath, err)
		}
		version = tag
		fmt.Fprintf(os.Stderr, "Resolved @%s -> %s\n", parsed.RepoPath, version)
	}

	// Download/cache the repo
	cachePath, err := EnsureRepoDownloaded(parsed.RepoPath, version)
	if err != nil {
		return nil, fmt.Errorf("downloading %s:%s: %w", parsed.RepoPath, version, err)
	}

	// Load the remote images.yml
	cfg, err := LoadConfig(cachePath)
	if err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", parsed.RepoPath, err)
	}

	// Resolve the image
	calverTag := ComputeCalVer()
	resolved, err := cfg.ResolveImage(parsed.Name, calverTag, cachePath)
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}

	// Scan layers from the cached repo
	layers, err := ScanAllLayersWithConfig(cachePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("scanning layers in %s: %w", parsed.RepoPath, err)
	}

	// Build the registry image ref for pulling
	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, tag)

	return &RemoteImageContext{
		Ref:       *parsed,
		CacheDir:  cachePath,
		Config:    cfg,
		Resolved:  resolved,
		Layers:    layers,
		ImageRef:  imageRef,
		ImageName: parsed.Name,
	}, nil
}

// PullImage attempts to pull the image from the registry.
func (ctx *RemoteImageContext) PullImage(engine string) error {
	binary := EngineBinary(engine)
	fmt.Fprintf(os.Stderr, "Pulling %s...\n", ctx.ImageRef)
	cmd := exec.Command(binary, "pull", ctx.ImageRef)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling %s: %w", ctx.ImageRef, err)
	}
	return nil
}

// BuildImage builds the image locally from the cached source.
func (ctx *RemoteImageContext) BuildImage(rt *ResolvedRuntime, tag string) error {
	gen, err := NewGenerator(ctx.CacheDir, "")
	if err != nil {
		return fmt.Errorf("creating generator for %s: %w", ctx.Ref.RepoPath, err)
	}
	if err := gen.Generate(); err != nil {
		return fmt.Errorf("generating build files for %s: %w", ctx.Ref.RepoPath, err)
	}

	buildCmd := &BuildCmd{
		Images: []string{ctx.ImageName},
		Tag:    tag,
	}
	origDir, _ := os.Getwd()
	if err := os.Chdir(ctx.CacheDir); err != nil {
		return fmt.Errorf("changing to cache dir: %w", err)
	}
	defer os.Chdir(origDir)

	return buildCmd.Run()
}

// PullOrBuild tries to pull the image from the registry first.
// If pull fails or forceBuild is true, builds locally from cached source.
func (ctx *RemoteImageContext) PullOrBuild(rt *ResolvedRuntime, tag string, forceBuild bool) error {
	if !forceBuild {
		if ctx.Resolved.Registry != "" {
			if err := ctx.PullImage(rt.RunEngine); err == nil {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Pull failed, building locally...\n")
		}
	}

	return ctx.BuildImage(rt, tag)
}

// ContainerName returns the container name for a remote image.
func (ctx *RemoteImageContext) ContainerName() string {
	return containerName(ctx.ImageName)
}

// CollectVolumes collects volumes for the remote image.
func (ctx *RemoteImageContext) CollectVolumes() ([]VolumeMount, error) {
	return CollectImageVolumes(
		ctx.Config, ctx.Layers, ctx.ImageName,
		ctx.Resolved.Home,
		nil,
	)
}

// RemoteContainerName returns the container name for a remote ref.
func RemoteContainerName(ref string) string {
	parsed := ParseRemoteRef(ref)
	return containerName(parsed.Name)
}

// StripURLScheme removes http:// or https:// from a remote ref if present.
func StripURLScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	return ref
}
