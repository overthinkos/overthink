package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	Test     ImageTestCmd `cmd:"" help:"Run declarative tests against a freshly-run container from a built image"`
	Validate ValidateCmd  `cmd:"" help:"Check image.yml + layers, exit 0 or 1"`
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
	Tag      string `long:"tag" default:"latest" help:"Image tag when resolving a short name"`
	Platform string `long:"platform" help:"Target platform (default: host)"`
}

func (c *ImagePullCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	// Remote ref: @github.com/org/repo/image[:version]
	ref := StripURLScheme(c.Image)
	if IsRemoteImageRef(ref) {
		ctx, err := ResolveRemoteImage(ref, c.Tag)
		if err != nil {
			return err
		}
		if err := ctx.PullImage(rt.RunEngine); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Pulled %s\n", ctx.ImageRef)
		return nil
	}

	// Fully-qualified ref: contains a "/" (registry segment) or explicit ":tag"
	// beyond bare "name:tag".
	if looksLikeFullRef(c.Image) {
		return c.pullRef(rt.RunEngine, c.Image)
	}

	// Short name: resolve registry via image.yml.
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, cfgErr := LoadConfig(dir)
	if cfgErr != nil {
		return fmt.Errorf("short name %q requires a project directory with image.yml; pass a fully-qualified ref (e.g. 'ghcr.io/org/%s:<tag>') to pull from anywhere", c.Image, c.Image)
	}
	resolved, err := cfg.ResolveImage(c.Image, c.Tag, dir)
	if err != nil {
		return err
	}
	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
	return c.pullRef(rt.RunEngine, imageRef)
}

// pullRef pulls a fully-qualified image reference via the configured engine.
func (c *ImagePullCmd) pullRef(engine, imageRef string) error {
	binary := EngineBinary(engine)
	args := []string{"pull"}
	if c.Platform != "" {
		args = append(args, "--platform", c.Platform)
	}
	args = append(args, imageRef)

	fmt.Fprintf(os.Stderr, "Pulling %s...\n", imageRef)
	cmd := exec.Command(binary, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pulling %s: %w", imageRef, err)
	}
	fmt.Fprintf(os.Stderr, "Pulled %s\n", imageRef)
	return nil
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
