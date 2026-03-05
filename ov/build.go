package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// BuildCmd builds container images
type BuildCmd struct {
	Images   []string `arg:"" optional:"" help:"Images to build (default: all enabled). Supports remote refs (github.com/org/repo/image[@version])"`
	Push     bool     `long:"push" help:"Push to registry after building"`
	Tag      string   `long:"tag" help:"Override tag (default: CalVer)"`
	Platform string   `long:"platform" help:"Target platform (default: host platform)"`
	Cache    string   `long:"cache" help:"Build cache type: registry, image, gha, none (default: auto)" env:"OV_BUILD_CACHE"`
	NoCache  bool     `long:"no-cache" help:"Disable build cache entirely"`
	Jobs     int      `long:"jobs" help:"Max concurrent image builds per level (default: 4)" default:"4"`
}

func (c *BuildCmd) Run() error {
	// Check if any image arg is a remote ref
	for _, img := range c.Images {
		ref := StripURLScheme(img)
		if IsRemoteImageRef(ref) {
			return c.buildRemote(ref)
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Generate Containerfiles
	gen, err := NewGenerator(dir, c.Tag)
	if err != nil {
		return err
	}
	if err := gen.Generate(); err != nil {
		return fmt.Errorf("generating build files: %w", err)
	}

	// Resolve runtime config for build engine
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	engine := EngineBinary(rt.BuildEngine)

	// Determine platform
	platform := c.Platform
	if platform == "" && !c.Push {
		platform = hostPlatform()
	}

	if len(c.Images) > 0 {
		// Filtered build: use sequential order
		order, err := ResolveImageOrder(gen.Images, gen.Layers)
		if err != nil {
			return err
		}
		order, err = filterImages(order, c.Images, gen.Images)
		if err != nil {
			return err
		}
		for _, name := range order {
			img := gen.Images[name]
			content := gen.Containerfiles[name]
			if err := c.buildImage(engine, dir, name, img, gen.Config, platform, rt.BuildEngine, content); err != nil {
				return fmt.Errorf("building %s: %w", name, err)
			}
		}
	} else {
		// Full build: use level-based parallelism
		levels, err := ResolveImageLevels(gen.Images, gen.Layers)
		if err != nil {
			return err
		}

		jobs := c.Jobs
		if jobs < 1 {
			jobs = 1
		}

		for i, level := range levels {
			fmt.Fprintf(os.Stderr, "\n=== Build level %d/%d (%d images) ===\n", i+1, len(levels), len(level))

			if len(level) == 1 {
				// Single image, no need for goroutine overhead
				name := level[0]
				img := gen.Images[name]
				content := gen.Containerfiles[name]
				if err := c.buildImage(engine, dir, name, img, gen.Config, platform, rt.BuildEngine, content); err != nil {
					return fmt.Errorf("building %s: %w", name, err)
				}
				continue
			}

			g, _ := errgroup.WithContext(context.Background())
			g.SetLimit(jobs)

			for _, name := range level {
				name := name
				img := gen.Images[name]
				content := gen.Containerfiles[name]
				g.Go(func() error {
					if err := c.buildImage(engine, dir, name, img, gen.Config, platform, rt.BuildEngine, content); err != nil {
						return fmt.Errorf("building %s: %w", name, err)
					}
					return nil
				})
			}

			if err := g.Wait(); err != nil {
				return err
			}
		}
	}

	// Auto-merge if enabled
	if !c.Push {
		mergeCmd := &MergeCmd{All: true, Tag: "latest"}
		if err := mergeCmd.Run(); err != nil {
			// Non-fatal: log and continue
			fmt.Fprintf(os.Stderr, "Warning: merge --all: %v\n", err)
		}
	}

	return nil
}

// buildImage builds a single image with the configured engine.
// containerfileContent is piped via stdin (-f -) to avoid race conditions
// with concurrent ov generate overwrites on disk.
func (c *BuildCmd) buildImage(engine, dir, name string, img *ResolvedImage, cfg *Config, platform, engineName, containerfileContent string) error {
	// Compute tags
	tags := []string{img.FullTag}
	origCfg := cfg.Images[name]
	if origCfg.Tag == "" || origCfg.Tag == "auto" {
		if img.Registry != "" {
			tags = append(tags, fmt.Sprintf("%s/%s:latest", img.Registry, name))
		} else {
			tags = append(tags, fmt.Sprintf("%s:latest", name))
		}
	}

	var args []string

	if c.Push {
		args = c.buildPushArgs(engine, tags, img.Platforms, engineName, name, img.Registry)
	} else {
		args = c.buildLocalArgs(engine, tags, platform, name, img.Registry)
	}

	fmt.Fprintf(os.Stderr, "\n--- Building %s ---\n", name)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(containerfileContent)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build failed: %w", engine, err)
	}

	// Podman --manifest builds locally; push each tag separately with retry
	if c.Push && engineName == "podman" {
		for _, tag := range tags {
			fmt.Fprintf(os.Stderr, "Pushing %s\n", tag)
			pushTag := tag
			if err := retryCmd(3, 5*time.Second, func() error {
				pushCmd := exec.Command("podman", "manifest", "push", "--all", tags[0], "docker://"+pushTag)
				pushCmd.Dir = dir
				pushCmd.Stdout = os.Stderr
				pushCmd.Stderr = os.Stderr
				return pushCmd.Run()
			}); err != nil {
				return fmt.Errorf("podman manifest push %s failed: %w", tag, err)
			}
		}
	}

	return nil
}

// buildLocalArgs constructs args for a local (single-platform, load into store) build.
// Uses -f - to read the Containerfile from stdin.
func (c *BuildCmd) buildLocalArgs(engine string, tags []string, platform, name, registry string) []string {
	args := []string{engine, "build", "--layers=true", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	if engine == "podman" {
		args = append(args, "--jobs", strconv.Itoa(runtime.NumCPU()))
	}
	args = append(args, c.cacheArgs(name, registry, engine)...)
	args = append(args, ".")
	return args
}

// buildPushArgs constructs args for a multi-platform push build.
func (c *BuildCmd) buildPushArgs(engine string, tags []string, platforms []string, engineName, name, registry string) []string {
	if engineName == "podman" {
		return c.buildPodmanPushArgs(tags, platforms, name, registry)
	}
	return c.buildDockerPushArgs(tags, platforms, name, registry)
}

func (c *BuildCmd) buildDockerPushArgs(tags []string, platforms []string, name, registry string) []string {
	args := []string{"docker", "buildx", "build", "--push", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, c.cacheArgs(name, registry, "docker")...)
	args = append(args, ".")
	return args
}

// cacheArgs returns cache flags for the given image name based on the --cache setting.
// Default: "image" (read-only from registry) for local builds, "registry" (read+write) for push builds.
// Podman uses plain image refs; Docker buildx uses type=registry,ref=... syntax.
func (c *BuildCmd) cacheArgs(name, registry, engine string) []string {
	if c.NoCache || c.Cache == "none" {
		return nil
	}

	cacheType := c.Cache
	// Auto-detect: default to "image" for local, "registry" for push
	if cacheType == "" && registry != "" {
		if c.Push {
			if engine == "podman" {
				// Podman --cache-to doesn't support type=registry syntax.
				// Use image cache (read-only from published image) instead.
				cacheType = "image"
			} else {
				cacheType = "registry"
			}
		} else {
			cacheType = "image"
		}
	}

	switch cacheType {
	case "registry":
		if registry == "" {
			return nil
		}
		ref := fmt.Sprintf("%s/cache:%s", registry, name)
		return []string{
			"--cache-from", fmt.Sprintf("type=registry,ref=%s", ref),
			"--cache-to", fmt.Sprintf("type=registry,ref=%s,mode=max,compression=zstd", ref),
		}
	case "gha":
		return []string{
			"--cache-from", fmt.Sprintf("type=gha,scope=%s", name),
			"--cache-to", fmt.Sprintf("type=gha,mode=max,scope=%s", name),
		}
	case "image":
		if registry == "" {
			return nil
		}
		ref := fmt.Sprintf("%s/%s", registry, name)
		return []string{"--cache-from", ref}
	default:
		return nil
	}
}

func (c *BuildCmd) buildPodmanPushArgs(tags []string, platforms []string, name, registry string) []string {
	// Podman uses --manifest for multi-platform builds
	args := []string{"podman", "build", "--layers=true", "-f", "-"}
	if len(tags) > 0 {
		args = append(args, "--manifest", tags[0])
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, "--jobs", strconv.Itoa(runtime.NumCPU()))
	args = append(args, c.cacheArgs(name, registry, "podman")...)
	args = append(args, ".")
	return args
}

// retryCmd retries fn up to maxAttempts times with exponential backoff starting at baseDelay.
func retryCmd(maxAttempts int, baseDelay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		if i > 0 {
			delay := baseDelay * time.Duration(1<<(i-1))
			fmt.Fprintf(os.Stderr, "Retry %d/%d after %v...\n", i, maxAttempts-1, delay)
			time.Sleep(delay)
		}
		err = fn()
		if err == nil {
			return nil
		}
		fmt.Fprintf(os.Stderr, "Attempt %d/%d failed: %v\n", i+1, maxAttempts, err)
	}
	return err
}

// hostPlatform returns the host platform in OCI format.
func hostPlatform() string {
	arch := runtime.GOARCH
	return "linux/" + arch
}

// buildRemote builds a remote image ref locally from its cached source.
func (c *BuildCmd) buildRemote(ref string) error {
	tag := c.Tag
	if tag == "" {
		tag = "latest"
	}

	ctx, err := ResolveRemoteImage(ref, tag)
	if err != nil {
		return err
	}

	return ctx.BuildImage(nil, tag)
}

// filterImages filters the build order to only include the requested images
// and their dependencies.
func filterImages(order []string, requested []string, images map[string]*ResolvedImage) ([]string, error) {
	// Validate requested images exist
	for _, name := range requested {
		if _, ok := images[name]; !ok {
			return nil, fmt.Errorf("unknown image %q", name)
		}
	}

	// Collect requested images and their transitive base + builder dependencies
	needed := make(map[string]bool)
	var addDeps func(name string)
	addDeps = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		img := images[name]
		if !img.IsExternalBase {
			addDeps(img.Base)
		}
		if img.Builder != "" && img.Builder != name {
			if _, ok := images[img.Builder]; ok {
				addDeps(img.Builder)
			}
		}
	}
	for _, name := range requested {
		addDeps(name)
	}

	// Filter order preserving dependency order
	var filtered []string
	for _, name := range order {
		if needed[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered, nil
}
