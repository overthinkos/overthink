package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// BuildCmd builds container images
type BuildCmd struct {
	Images          []string `arg:"" optional:"" help:"Images to build (default: all enabled). Supports remote refs (github.com/org/repo/image[@version])"`
	Push            bool     `long:"push" help:"Push to registry after building"`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	Platform        string   `long:"platform" help:"Target platform (default: host platform)"`
	Cache           string   `long:"cache" help:"Build cache type: registry, image, gha, none (default: auto)" env:"OV_BUILD_CACHE"`
	NoCache         bool     `long:"no-cache" help:"Disable build cache entirely"`
	Jobs            int      `long:"jobs" help:"Max concurrent image builds per level (default: 4)" default:"4"`
	PodmanJobs      int      `long:"podman-jobs" help:"Override --jobs passed to podman build (0=auto, default min(NCPU,4)). Capped because podman-5.7.x races under high concurrency with --cache-from on multi-stage builds." env:"OV_PODMAN_JOBS"`
	IncludeDisabled bool     `long:"include-disabled" help:"Build images with enabled: false in image.yml (does not modify the file). Use for one-off operational rebuilds without flipping authored config."`
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

	// Generate Containerfiles. --include-disabled flows through Generator
	// so `ov image build <disabled-image> --include-disabled` reaches it
	// without the operator having to flip enabled: false in image.yml.
	// When the user named specific images on the command line, scope the
	// override to those names only — otherwise widening the working set
	// would surface unrelated disabled-image dep errors (e.g. images that
	// declare remote layers not yet fetched into the cache).
	resolveOpts := ResolveOpts{IncludeDisabled: c.IncludeDisabled}
	if c.IncludeDisabled && len(c.Images) > 0 {
		resolveOpts.IncludeDisabledNames = make(map[string]bool, len(c.Images))
		for _, name := range c.Images {
			resolveOpts.IncludeDisabledNames[name] = true
		}
	}
	gen, err := NewGenerator(dir, c.Tag, resolveOpts)
	if err != nil {
		return err
	}
	if err := gen.Generate(); err != nil {
		return fmt.Errorf("generating build files: %w", err)
	}

	if err := ensureOvBinaryFresh(dir, gen.Images, c.Images); err != nil {
		return fmt.Errorf("refreshing ov binary: %w", err)
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
		order, err = filterImage(order, c.Images, gen.Images)
		if err != nil {
			return err
		}
		for _, name := range order {
			img := gen.Images[name]
			content := gen.Containerfiles[name]
			if err := c.buildImage(engine, dir, name, img, gen.Config, platform, rt.BuildEngine, content); err != nil {
				return fmt.Errorf("building %s: %w", name, err)
			}
			mergeAfterBuild(name, img)
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
			} else {
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

			// Merge this level before building the next so children
			// start from a merged (fewer-layer) base image.
			for _, name := range level {
				mergeAfterBuild(name, gen.Images[name])
			}
		}
	}

	// Push after merge (Podman only; Docker buildx pushes during build)
	if c.Push && rt.BuildEngine == "podman" {
		order, err := ResolveImageOrder(gen.Images, gen.Layers)
		if err != nil {
			return err
		}
		if len(c.Images) > 0 {
			order, err = filterImage(order, c.Images, gen.Images)
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "\n=== Pushing images ===\n")
		for _, name := range order {
			img := gen.Images[name]
			tags := imageTags(name, img, gen.Config)
			if err := c.pushImage(dir, tags); err != nil {
				return err
			}
		}
	}

	return nil
}

// imageTags computes the tags for an image. ov is CalVer-only — it never
// emits `:latest`. Every built image carries exactly its CalVer tag
// (`img.FullTag`, e.g. `ghcr.io/overthinkos/fedora:2026.114.1042`), and
// short-name resolution goes through `ResolveNewestLocalCalVer` in
// local_image.go via the `org.overthinkos.version` OCI label.
func imageTags(name string, img *ResolvedImage, cfg *Config) []string {
	return []string{img.FullTag}
}

// mergeAfterBuild merges a single image if merge.auto is enabled.
// Called immediately after building so child images inherit a merged base.
func mergeAfterBuild(name string, img *ResolvedImage) {
	if img.Merge == nil || !img.Merge.Auto {
		return
	}
	mergeCmd := &MergeCmd{Image: name, Tag: ""}
	if err := mergeCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: merge %s: %v\n", name, err)
	}
}

// buildImage builds a single image with the configured engine.
// containerfileContent is piped via stdin (-f -) to avoid race conditions
// with concurrent ov generate overwrites on disk.
// For Podman --push, the image is built locally (--manifest) without pushing;
// push happens separately after merge in Run().
func (c *BuildCmd) buildImage(engine, dir, name string, img *ResolvedImage, cfg *Config, platform, engineName, containerfileContent string) error {
	tags := imageTags(name, img, cfg)

	// Pre-build phase for `from: builder:<name>` images: run the named
	// kind:bootstrap builder in a privileged container, capture its
	// rootfs.tar.gz into .build/<image>/<builder>.tar.gz so the
	// Containerfile's ADD <builder>.tar.gz / step finds it.
	if strings.HasPrefix(img.From, "builder:") {
		if err := c.runPrivilegedBootstrap(engineName, dir, name, img); err != nil {
			return err
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

	return nil
}

// runPrivilegedBootstrap executes the kind: bootstrap builder selected
// by img.From ("builder:<name>") via RunPrivileged. The output rootfs
// tarball lands at .build/<image>/<builder>.tar.gz, where the
// Containerfile's ADD step picks it up.
//
// Skipped (returns nil) when img.From is not a builder ref. Errors when
// the builder is missing, isn't kind: bootstrap, or doesn't have a
// resolved BuilderImage on the ResolvedImage.
func (c *BuildCmd) runPrivilegedBootstrap(engine, dir, imageName string, img *ResolvedImage) error {
	if !strings.HasPrefix(img.From, "builder:") {
		return nil
	}
	builderName := strings.TrimPrefix(img.From, "builder:")
	if img.BootstrapBuilderImage == "" {
		return fmt.Errorf("image %s: from: builder:%s requires bootstrap_builder_image: in image.yml", imageName, builderName)
	}
	if img.BuilderConfig == nil {
		return fmt.Errorf("image %s: build.yml builder: section is empty", imageName)
	}
	builder, ok := img.BuilderConfig.Builder[builderName]
	if !ok {
		return fmt.Errorf("image %s: builder %q is not declared in build.yml", imageName, builderName)
	}
	if !builder.IsBootstrap() {
		return fmt.Errorf("image %s: builder %q is not kind: bootstrap (got kind=%q)", imageName, builderName, builder.Kind)
	}
	if img.DistroDef == nil {
		return fmt.Errorf("image %s: distro %v has no resolved DistroDef", imageName, img.Distro)
	}

	output := builder.OutputArtifact
	if output == "" {
		output = "/out/rootfs.tar.gz"
	}
	outDest := filepath.Join(dir, ".build", imageName, fmt.Sprintf("%s.tar.gz", builderName))

	// Skip rebuild when the staged tarball is already present and the
	// builder image hash hasn't changed. Cheap stat is enough for now;
	// content-addressing is a future optimization.
	if _, err := os.Stat(outDest); err == nil {
		fmt.Fprintf(os.Stderr, "Bootstrap %s already staged at %s — skipping\n", builderName, outDest)
		return nil
	}

	// Resolve the builder image ref. Internal kind:image names get
	// resolved to the newest local CalVer tag via the same machinery
	// as `ov shell <name>` so build never tries to pull a `:latest`
	// that ov doesn't emit.
	builderRef := img.BootstrapBuilderImage
	if !strings.Contains(builderRef, "/") {
		resolved, err := resolveLocalImageRef(engine, builderRef)
		if err != nil {
			return fmt.Errorf("resolving builder image %q: %w (build the bootstrap_builder_image first)", builderRef, err)
		}
		builderRef = resolved
	}

	ctx := struct {
		Distro          *DistroDef
		Packages        []string
		ExtraPacmanConf string
		ExtraAptSources string
		Arch            string
		Variant         string
	}{
		Distro:   img.DistroDef,
		Packages: bootstrapPackagesForImage(img),
	}
	// CachyOS et al. need extra repo blocks injected into pacman.conf
	// before pacstrap so the new packages resolve from the right repos.
	if img.DistroDef.Pacstrap != nil && len(img.DistroDef.Pacstrap.ExtraRepos) > 0 {
		var b strings.Builder
		for _, r := range img.DistroDef.Pacstrap.ExtraRepos {
			fmt.Fprintf(&b, "[%s]\nServer = %s\n", r.Name, r.Server)
			if r.SigLevel != "" {
				fmt.Fprintf(&b, "SigLevel = %s\n", r.SigLevel)
			}
		}
		ctx.ExtraPacmanConf = b.String()
	}
	// Debian-family security/backports apt sources injected before stage-2.
	if img.DistroDef.Debootstrap != nil && len(img.DistroDef.Debootstrap.ExtraRepos) > 0 {
		var b strings.Builder
		for _, r := range img.DistroDef.Debootstrap.ExtraRepos {
			suite := r.Suite
			if suite == "" {
				suite = img.DistroDef.Debootstrap.Suite
			}
			components := r.Components
			if components == "" {
				components = img.DistroDef.Debootstrap.Components
				if components == "" {
					components = "main"
				}
			}
			fmt.Fprintf(&b, "echo 'deb %s %s %s' > /target/etc/apt/sources.list.d/%s.list\n", r.URL, suite, components, r.Name)
		}
		ctx.ExtraAptSources = b.String()
	}

	script, err := renderBootstrapScript(builder, ctx)
	if err != nil {
		return fmt.Errorf("rendering bootstrap script for %s: %w", imageName, err)
	}

	fmt.Fprintf(os.Stderr, "\n--- Bootstrap (%s) for %s ---\n", builderName, imageName)
	if err := RunPrivileged(PrivilegedRun{
		Image:      builderRef,
		Script:     script,
		OutputPath: output,
		OutputDest: outDest,
	}); err != nil {
		return fmt.Errorf("running %s for %s: %w", builderName, imageName, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", outDest)
	return nil
}

// bootstrapPackagesForImage returns base + per-image bootstrap packages.
// Per-image overrides aren't currently surfaced via image.yml; this
// returns just the distro defaults for now.
//
// Mirrors baseBootstrapPackages in vm_bootstrap.go but at the OCI-image
// build path (image.yml `from: builder:<name>` consumers). Same dispatch
// rules: Pacstrap.BasePackages for pacstrap-flavored, Debootstrap.BasePackages
// for debootstrap-flavored.
func bootstrapPackagesForImage(img *ResolvedImage) []string {
	if img.DistroDef == nil {
		return nil
	}
	if img.DistroDef.Pacstrap != nil {
		return img.DistroDef.Pacstrap.BasePackages
	}
	if img.DistroDef.Debootstrap != nil {
		return img.DistroDef.Debootstrap.BasePackages
	}
	return nil
}

// pushImage pushes a Podman image to the registry for each tag with retry.
// Detects whether the primary tag is a manifest list or a regular image
// and uses the appropriate push command.
func (c *BuildCmd) pushImage(dir string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}

	// Check if the primary tag is a manifest list
	isManifest := false
	checkCmd := exec.Command("podman", "manifest", "inspect", tags[0])
	checkCmd.Stdout = io.Discard
	checkCmd.Stderr = io.Discard
	if checkCmd.Run() == nil {
		isManifest = true
	}

	for _, tag := range tags {
		fmt.Fprintf(os.Stderr, "Pushing %s\n", tag)
		pushTag := tag
		if err := retryCmd(3, 5*time.Second, func() error {
			var pushCmd *exec.Cmd
			if isManifest {
				pushCmd = exec.Command("podman", "manifest", "push", "--all", tags[0], "docker://"+pushTag)
			} else {
				pushCmd = exec.Command("podman", "push", tags[0], "docker://"+pushTag)
			}
			pushCmd.Dir = dir
			pushCmd.Stdout = os.Stderr
			pushCmd.Stderr = os.Stderr
			return pushCmd.Run()
		}); err != nil {
			kind := "push"
			if isManifest {
				kind = "manifest push"
			}
			return fmt.Errorf("podman %s %s failed: %w", kind, tag, err)
		}
	}
	return nil
}

// podmanJobsDefault is the maximum number of concurrent stage builds ov
// asks podman to run inside a single `podman build` invocation. The cap
// exists because podman-5.7.x's storage backend races under high concurrency
// during multi-stage builds with --cache-from: when many goroutines call
// into storageImageDestination.TryReusingBlobWithOptions and queueOrCommit
// at the same time, the shared state gets corrupted and the process aborts
// with SIGABRT. Observed reproducibly on selkies-desktop (29-stage DAG)
// with --jobs runtime.NumCPU() (16 on a 16-core host) and --cache-from.
// Four is chosen as a balance: still meaningful parallelism for typical
// builds, narrow enough race window that the bug has not been observed
// to fire in practice. Override via --podman-jobs or OV_PODMAN_JOBS.
const podmanJobsDefault = 4

// numCPU is a package-level alias for runtime.NumCPU so tests can inject
// a fixed value via the init in build_jobs_test.go.
var numCPU = runtime.NumCPU

// resolvePodmanJobs returns the --jobs value to pass to `podman build`.
// If override > 0, it wins. Otherwise returns min(numCPU(), podmanJobsDefault).
func resolvePodmanJobs(override int) int {
	if override > 0 {
		return override
	}
	n := numCPU()
	if n < podmanJobsDefault {
		return n
	}
	return podmanJobsDefault
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
		args = append(args, "--jobs", strconv.Itoa(resolvePodmanJobs(c.PodmanJobs)))
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
// Podman uses plain image refs for --cache-from/--cache-to (no tags allowed for --cache-to).
// Docker buildx uses type=registry,ref=... syntax with a separate cache repo.
func (c *BuildCmd) cacheArgs(name, registry, engine string) []string {
	if c.NoCache || c.Cache == "none" {
		return nil
	}

	cacheType := c.Cache
	// Auto-detect: default to "image" for local, "registry" for push
	if cacheType == "" && registry != "" {
		if c.Push {
			cacheType = "registry"
		} else {
			cacheType = "image"
		}
	}

	switch cacheType {
	case "registry":
		if registry == "" {
			return nil
		}
		ref := fmt.Sprintf("%s/%s", registry, name)
		if engine == "podman" {
			// Podman --cache-to takes a plain repo ref (no tag, no type= syntax).
			// Intermediate build layers are pushed to the same repo as the image.
			return []string{
				"--cache-from", ref,
				"--cache-to", ref,
			}
		}
		// Docker buildx uses a separate cache repo with type=registry syntax
		cacheRef := fmt.Sprintf("%s/cache:%s", registry, name)
		return []string{
			"--cache-from", fmt.Sprintf("type=registry,ref=%s", cacheRef),
			"--cache-to", fmt.Sprintf("type=registry,ref=%s,mode=max,compression=zstd", cacheRef),
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
	args = append(args, "--jobs", strconv.Itoa(resolvePodmanJobs(c.PodmanJobs)))
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
		// ov is CalVer-only. A remote build with no explicit CalVer
		// gets a fresh one at build time — matching the local
		// `ov image build` behaviour (generate.go:ComputeCalVer).
		tag = ComputeCalVer()
	}

	ctx, err := ResolveRemoteImage(ref, tag)
	if err != nil {
		return err
	}

	return ctx.BuildImage(nil, tag)
}

// filterImage filters the build order to only include the requested images
// and their dependencies.
func filterImage(order []string, requested []string, images map[string]*ResolvedImage) ([]string, error) {
	// Validate requested images exist
	for _, name := range requested {
		if _, ok := images[name]; !ok {
			return nil, fmt.Errorf("unknown image %q", name)
		}
	}

	// Collect requested images and their transitive deps (Base + format builders +
	// BootstrapBuilderImage). Routed through imageDirectDeps in graph.go so this
	// walker stays in lockstep with ResolveImageOrder + ResolveImageLevels — see
	// the helper's docstring for the rationale (2026-05 cachyos-pacstrap-builder
	// regression). includeFormatBuilders=true here unconditionally because filtered
	// build sets must always include format-builder images that the requested
	// targets need at build time, regardless of ImageNeedsBuilder.
	needed := make(map[string]bool)
	var addDeps func(name string)
	addDeps = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		img := images[name]
		for _, dep := range imageDirectDeps(name, img, images, true) {
			addDeps(dep)
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

// ensureOvBinaryFresh rebuilds layers/ov/bin/ov when any image whose
// resolved layer chain includes the `ov` layer is in scope for the
// current build. Without this, podman build would COPY whatever stale
// binary happens to live at layers/ov/bin/ov — silently baking obsolete
// CLI behaviour into the image. Skipped (with a one-line warning) when
// `go` is not on PATH, so an end-user with a packaged ov install does
// not see a hard error.
func ensureOvBinaryFresh(dir string, images map[string]*ResolvedImage, requested []string) error {
	in := requested
	if len(in) == 0 {
		in = make([]string, 0, len(images))
		for name := range images {
			in = append(in, name)
		}
	}
	needs := false
	for _, name := range in {
		img, ok := images[name]
		if !ok {
			continue
		}
		for _, layer := range img.Layer {
			if layer == "ov" {
				needs = true
				break
			}
		}
		if needs {
			break
		}
	}
	if !needs {
		return nil
	}

	binPath := filepath.Join(dir, "layers", "ov", "bin", "ov")
	srcDir := filepath.Join(dir, "ov")
	upToDate, err := ovBinaryUpToDate(binPath, srcDir)
	if err == nil && upToDate {
		return nil
	}

	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "ov: warning: `go` not on PATH; skipping layers/ov/bin/ov rebuild (image will use existing binary)\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "ov: rebuilding layers/ov/bin/ov from ./ov before image build\n")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ovBinaryUpToDate returns true when binPath exists and is newer than
// every .go file under srcDir. Returns (false, nil) for any file system
// state that warrants a rebuild (missing binary, missing source dir).
func ovBinaryUpToDate(binPath, srcDir string) (bool, error) {
	binStat, err := os.Stat(binPath)
	if err != nil {
		return false, nil
	}
	binMtime := binStat.ModTime()
	upToDate := true
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(binMtime) {
			upToDate = false
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, walkErr
	}
	return upToDate, nil
}
