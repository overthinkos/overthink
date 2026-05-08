package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ImageCmd groups build-mode commands that operate on image.yml (or, in the
// case of ImagePullCmd, resolve registry/tag via image.yml and then fetch the
// image into local storage so deploy-mode commands can read its OCI labels).
type ImageCmd struct {
	Build    BuildCmd     `cmd:"" help:"Build container images"`
	Generate GenerateCmd  `cmd:"" help:"Write .build/ (Containerfiles)"`
	Inspect  InspectCmd   `cmd:"" help:"Print resolved config for an image (JSON)"`
	List     ListCmd      `cmd:"" help:"List components from image.yml"`
	Merge    MergeCmd     `cmd:"" help:"Merge small layers in a built container image"`
	New      NewCmd       `cmd:"" help:"Scaffold new components"`
	Pull     ImagePullCmd `cmd:"" help:"Pull an image from its registry into local storage"`
	Validate ValidateCmd  `cmd:"" help:"Check image.yml + layers, exit 0 or 1"`

	// Authoring verbs — added so the MCP tool surface (auto-reflected from
	// Kong) can author a project from scratch over RPC.
	Set      ImageSetCmd      `cmd:"" help:"Set a value in image.yml by dot-path (e.g. images.foo.base fedora)"`
	AddLayer ImageAddLayerCmd `cmd:"add-layer" help:"Append a layer to an image's layers: list (idempotent)"`
	RmLayer  ImageRmLayerCmd  `cmd:"rm-layer" help:"Remove a layer from an image's layers: list"`
	Fetch    ImageFetchCmd    `cmd:"" help:"Pre-prime the remote-repo cache (default: overthinkos/overthink)"`
	Refresh  ImageRefreshCmd  `cmd:"" help:"Force re-clone of a remote project repo"`
	Write    ImageWriteCmd    `cmd:"" help:"Write file contents under the project root (escape hatch for free-form files)"`
	Cat      ImageCatCmd      `cmd:"" help:"Print file contents from under the project root"`
}

// ImagePullCmd fetches an image from its registry into the local container
// engine so deploy-mode commands can read its OCI labels. Accepts three
// input forms:
//
//   - short name (e.g. "jupyter")           — resolves registry + tag via
//     image.yml (requires a project directory)
//   - fully-qualified ref ("ghcr.io/...:v") — pulled as-is
//   - remote ref ("@github.com/org/repo/image[:version]") — downloads the
//     repo and pulls the registry ref from its image.yml
type ImagePullCmd struct {
	Image    string `arg:"" help:"Image name (short, resolved via image.yml), fully-qualified ref, or @github.com/org/repo/image[:version]"`
	Tag      string `long:"tag" help:"Image CalVer tag when resolving a short name (empty = resolve from image.yml metadata or error with explicit guidance)"`
	Platform string `long:"platform" help:"Target platform (default: host)"`
}

func (c *ImagePullCmd) Run() error {
	// `ov image pull` is the operator-facing alias for the canonical
	// EnsureImagePresent path: pull from registry, fall back to a
	// local build when the identifier maps to a project image.yml
	// entry. Same contract as BuilderRun, the eval preflight, and
	// EnsureImage in transfer.go (R3, no per-command divergence).
	dir, _ := os.Getwd()
	cfg, _ := LoadConfig(dir)
	if c.Tag != "" {
		// Tag override: only meaningful for short-name input. Resolve
		// the canonical short-name ref FIRST so the build-fallback
		// path picks up the requested tag.
		if !looksLikeFullRef(c.Image) && !IsRemoteImageRef(StripURLScheme(c.Image)) {
			if cfg == nil {
				return fmt.Errorf("short name %q with --tag requires a project directory with image.yml", c.Image)
			}
			resolved, err := cfg.ResolveImage(c.Image, c.Tag, dir, ResolveOpts{})
			if err != nil {
				return err
			}
			ref := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
			return EnsureImagePresent(context.Background(), ref, cfg, dir)
		}
	}
	return EnsureImagePresent(context.Background(), c.Image, cfg, dir)
}

// looksLikeFullRef returns true if the image ref contains a registry segment
// (a "/" before any ":") — e.g. "ghcr.io/org/name:tag" — so it can be pulled
// without image.yml resolution.
func looksLikeFullRef(ref string) bool {
	if strings.HasPrefix(ref, "@") {
		return false
	}
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return false
	}
	colon := strings.Index(ref, ":")
	return colon < 0 || slash < colon
}

// FormatCLIError wraps top-level Kong errors with a friendly recommendation
// when the underlying cause is a missing local image (ErrImageNotLocal).
// Called from main() just before FatalIfErrorf so the exit path still passes
// through Kong's standard error rendering.
func FormatCLIError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrImageNotLocal) {
		// ExtractMetadata wraps as "image not found in local storage: <ref>";
		// pull out the ref so we can render the recommendation.
		msg := err.Error()
		ref := strings.TrimPrefix(msg, ErrImageNotLocal.Error()+": ")
		return fmt.Errorf("image %q is not available locally.\nRun 'ov image pull %s' to fetch it first", ref, ref)
	}
	return err
}
