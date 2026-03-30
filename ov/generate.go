package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Generator holds state for generating build artifacts
type Generator struct {
	Dir            string
	Config         *Config
	Layers         map[string]*Layer
	Tag            string
	Images         map[string]*ResolvedImage
	BuildDir       string
	Containerfiles map[string]string // cached content per image (used by ov build to pipe via stdin)
	GlobalOrder    []string          // popularity-weighted global layer order for cache optimization
}

// globalOrderForImage returns the layer order for an image by filtering the
// global order to only include the image's needed layers. This ensures shared
// layers appear in the same order across all images, maximizing cache reuse.
func (g *Generator) globalOrderForImage(imageLayers []string, parentLayers map[string]bool) ([]string, error) {
	// Resolve needed layers (expand composition + transitive deps)
	needed, err := ResolveLayerOrder(imageLayers, g.Layers, parentLayers)
	if err != nil {
		return nil, err
	}

	neededSet := make(map[string]bool, len(needed))
	for _, l := range needed {
		neededSet[l] = true
	}

	// Filter global order to only include this image's needed layers
	var order []string
	for _, l := range g.GlobalOrder {
		if neededSet[l] {
			order = append(order, l)
		}
	}

	// Safety: if global order is missing some needed layers (shouldn't happen),
	// append them in their original order
	for _, l := range needed {
		found := false
		for _, o := range order {
			if o == l {
				found = true
				break
			}
		}
		if !found {
			order = append(order, l)
		}
	}

	return order, nil
}

// resolveUserContext detects existing user in base image or uses configured values
func (g *Generator) resolveUserContext(img *ResolvedImage) error {
	if !img.IsExternalBase {
		// Internal base - inherit from parent, but respect explicit overrides
		parentImg := g.Images[img.Base]
		origCfg := g.Config.Images[img.Name]

		if origCfg.User == "" {
			img.User = parentImg.User
		}
		if origCfg.UID == nil {
			img.UID = parentImg.UID
		}
		if origCfg.GID == nil {
			img.GID = parentImg.GID
		}

		// Resolve home directory
		if img.User == "root" {
			img.Home = "/root"
		} else if origCfg.User == "" && origCfg.UID == nil {
			img.Home = parentImg.Home
		} else {
			img.Home = fmt.Sprintf("/home/%s", img.User)
		}
		return nil
	}

	// External base - try to detect existing user at configured UID
	userInfo, err := InspectImageUser(img.Base, img.UID)
	if err != nil {
		// Can't inspect, use configured defaults
		return nil
	}

	if userInfo != nil {
		// Found existing user - use their info
		img.User = userInfo.Name
		img.Home = userInfo.Home
		img.GID = userInfo.GID
	}
	// else: no user found at UID, will create with configured values

	return nil
}

// NewGenerator creates a new generator
func NewGenerator(dir string, tag string) (*Generator, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}

	// Load default format configs early — needed for SetFormatNames before layer scanning
	if cfg.Defaults.FormatConfig == nil {
		return nil, fmt.Errorf("defaults.format_config is required in images.yml")
	}
	defaultDistroCfg, _, err := LoadDefaultFormatConfigs(cfg.Defaults.FormatConfig, dir)
	if err != nil {
		return nil, fmt.Errorf("loading default format configs: %w", err)
	}
	SetFormatNames(defaultDistroCfg)

	// Load default init config for layer init system detection
	defaultInitCfg, _ := LoadInitConfigForImage(nil, cfg.Defaults.FormatConfig, dir)

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, err
	}

	// Populate init systems on layers from init.yml config
	PopulateLayerInitSystems(layers, defaultInitCfg)

	if err := Validate(cfg, layers, dir); err != nil {
		return nil, err
	}

	// Compute CalVer if tag not specified
	if tag == "" {
		tag = ComputeCalVer()
	}

	images, err := cfg.ResolveAllImages(tag, dir)
	if err != nil {
		return nil, err
	}

	// Compute and inject auto-intermediate images
	updated, err := ComputeIntermediates(images, layers, cfg, tag)
	if err != nil {
		return nil, fmt.Errorf("computing intermediates: %w", err)
	}
	images = updated

	// Compute global layer order for consistent cross-image ordering
	globalOrder, err := GlobalLayerOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("computing global layer order: %w", err)
	}

	return &Generator{
		Dir:            dir,
		Config:         cfg,
		Layers:         layers,
		Tag:            tag,
		Images:         images,
		BuildDir:       filepath.Join(dir, ".build"),
		Containerfiles: make(map[string]string),
		GlobalOrder:    globalOrder,
	}, nil
}

// cleanStaleBuildDirs removes image directories in .build/ that don't correspond
// to any enabled image, and removes leftover files like docker-bake.hcl.
func (g *Generator) cleanStaleBuildDirs() error {
	entries, err := os.ReadDir(g.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			name := entry.Name()
			if _, exists := g.Images[name]; !exists {
				path := filepath.Join(g.BuildDir, name)
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("removing stale dir %s: %w", path, err)
				}
				fmt.Fprintf(os.Stderr, "Removed stale build dir: .build/%s\n", name)
			}
		} else if entry.Name() == "docker-bake.hcl" {
			// Remove leftover HCL file from pre-ov-build era
			path := filepath.Join(g.BuildDir, entry.Name())
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("removing stale file %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed stale file: .build/%s\n", entry.Name())
		}
	}
	return nil
}

// Generate generates all build artifacts
func (g *Generator) Generate() error {
	// Clean stale image directories from .build/ (leftovers from removed/renamed images)
	if err := g.cleanStaleBuildDirs(); err != nil {
		return fmt.Errorf("cleaning stale build dirs: %w", err)
	}

	// Create .build directory
	if err := os.MkdirAll(g.BuildDir, 0755); err != nil {
		return fmt.Errorf("creating .build directory: %w", err)
	}

	// Create symlinks for remote layers in .build/_layers/
	if err := g.createRemoteLayerCopies(); err != nil {
		return fmt.Errorf("creating remote layer symlinks: %w", err)
	}

	// Resolve image build order
	order, err := ResolveImageOrder(g.Images, g.Layers)
	if err != nil {
		return fmt.Errorf("resolving image order: %w", err)
	}

	// Resolve user context for each image (in order, so parents are resolved first)
	for _, name := range order {
		if err := g.resolveUserContext(g.Images[name]); err != nil {
			return fmt.Errorf("resolving user context for %s: %w", name, err)
		}
	}

	// Generate Containerfile for each image
	for _, name := range order {
		if err := g.generateContainerfile(name); err != nil {
			return fmt.Errorf("generating Containerfile for %s: %w", name, err)
		}
	}

	return nil
}

// generateContainerfile generates a Containerfile for a single image
func (g *Generator) generateContainerfile(imageName string) error {
	// Clean image build directory to remove stale files from previous generations
	imageDir := filepath.Join(g.BuildDir, imageName)
	if err := os.RemoveAll(imageDir); err != nil {
		return err
	}

	img := g.Images[imageName]
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("# .build/%s/Containerfile (generated -- do not edit)\n\n", imageName))

	// Resolve layer order for this image
	var parentLayers map[string]bool
	if !img.IsExternalBase {
		var err error
		parentLayers, err = LayersProvidedByImage(img.Base, g.Images, g.Layers)
		if err != nil {
			return err
		}
	}

	layerOrder, err := g.globalOrderForImage(img.Layers, parentLayers)
	if err != nil {
		return err
	}

	// ARG for base image must come first (before any FROM)
	resolvedBase := g.resolveBaseImage(img)
	b.WriteString(fmt.Sprintf("ARG BASE_IMAGE=%s\n\n", resolvedBase))

	// Emit scratch stages for each layer
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		stageName := layer.Name // use short name for stage alias
		b.WriteString(fmt.Sprintf("FROM scratch AS %s\n", stageName))
		b.WriteString(fmt.Sprintf("COPY %s/ /\n\n", g.layerCopySource(layerName)))
	}

	// Emit per-layer multi-stage build stages — fully config-driven from builder.yml.
	// Each builder in builder.yml declares detect_files and/or detect_config.
	// For each layer that matches, the builder's stage_template is rendered.
	if img.BuilderConfig != nil {
		// Process builders in deterministic order
		builderNames := img.BuilderConfig.BuilderNames()
		for _, builderName := range builderNames {
			builderDef := img.BuilderConfig.Builders[builderName]
			if builderDef.Inline {
				continue // inline builders handled in writeLayerSteps
			}
			if builderDef.StageTemplate == "" {
				continue
			}
			for _, layerName := range layerOrder {
				layer := g.Layers[layerName]
				if !g.layerNeedsBuilder(layer, builderDef) {
					continue
				}
				builderRef := g.builderRefForFormat(imageName, builderName)
				if builderRef == "" {
					return fmt.Errorf("image %q: layer %q needs builder %q but no builders.%s configured", imageName, layerName, builderName, builderName)
				}
				ctx := g.buildStageContext(layer, builderName, builderDef, img, builderRef)
				rendered, err := RenderTemplate(builderName+"-stage", builderDef.StageTemplate, ctx)
				if err != nil {
					return fmt.Errorf("image %q: rendering %s stage for layer %q: %w", imageName, builderName, layerName, err)
				}
				b.WriteString(rendered)
				b.WriteString("\n")
			}
		}
	}

	// Emit extraction stages for layers with extract field
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasExtract {
			continue
		}
		for i, ext := range layer.Extract() {
			stageName := fmt.Sprintf("%s-extract-%d", layerName, i)
			b.WriteString(fmt.Sprintf("FROM %s AS %s\n\n", ext.Source, stageName))
		}
	}

	// Detect active init systems from layers (driven by init.yml config)
	activeInits := make(map[string]*InitDef)
	if img.InitConfig != nil {
		activeInits = img.InitConfig.ActiveInits(g.Layers, layerOrder, img.Bootc)
	}
	// Store init system on ResolvedImage for downstream use (labels, etc.)
	if img.InitConfig != nil {
		img.InitSystem, img.InitDef = img.InitConfig.ResolveInitSystem(g.Layers, layerOrder, img.Bootc, "")
	}

	// Check if this image has route layers and traefik
	hasRoutes := false
	hasTraefik := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasRoute {
			hasRoutes = true
		}
		if layerName == "traefik" {
			hasTraefik = true
		}
	}

	// Generate traefik routes only when traefik is actually present
	if hasRoutes && hasTraefik {
		if err := g.generateTraefikRoutes(imageName, layerOrder, img); err != nil {
			return err
		}
		b.WriteString("FROM scratch AS traefik-routes\n")
		b.WriteString(fmt.Sprintf("COPY .build/%s/traefik-routes.yml /routes.yml\n\n", imageName))
	}

	// Emit init system stages (fragment assembly or file copy)
	// When a child image adds services, include parent-provided configs
	// so the assembled config contains all services from the full chain.
	for initName, def := range activeInits {
		initLayerOrder := layerOrder
		if !img.IsExternalBase {
			full := collectAllImageLayers(imageName, g.Images, g.Layers)
			if len(full) > 0 {
				initLayerOrder = full
			}
		}
		if err := g.generateInitFragments(imageName, initName, def, initLayerOrder); err != nil {
			return err
		}

		// Emit scratch stage with COPY lines for fragments
		b.WriteString(fmt.Sprintf("FROM scratch AS %s\n", def.StageName))
		if def.StageHeaderCopy != "" {
			b.WriteString(def.StageHeaderCopy + "\n")
		}
		for i, layerName := range initLayerOrder {
			layer := g.Layers[layerName]
			// Service content fragments (fragment_assembly model)
			if def.Model == "fragment_assembly" && layer.HasInit(initName) {
				fileName := fmt.Sprintf("%02d-%s.conf", i+1, layerName)
				copyLine, err := def.RenderStageFragmentCopy(imageName, fileName)
				if err != nil {
					return fmt.Errorf("rendering stage fragment copy for %s/%s: %w", initName, layerName, err)
				}
				b.WriteString(copyLine + "\n")
			}
			// Relay fragments
			if def.HasRelayTemplate() && len(layer.PortRelayPorts) > 0 {
				for _, port := range layer.PortRelayPorts {
					confName := fmt.Sprintf("%02d-relay-%d.conf", i+1, port)
					copyLine, err := def.RenderStageFragmentCopy(imageName, confName)
					if err != nil {
						return fmt.Errorf("rendering relay copy for %s/%s port %d: %w", initName, layerName, port, err)
					}
					b.WriteString(copyLine + "\n")
				}
			}
			// File copy model: copy detected service files
			if def.Model == "file_copy" && len(layer.ServiceFiles()) > 0 {
				for _, svcPath := range layer.ServiceFiles() {
					svcName := filepath.Base(svcPath)
					copyLine, err := def.RenderStageFragmentCopy(imageName, svcName)
					if err != nil {
						return fmt.Errorf("rendering service file copy for %s/%s: %w", initName, layerName, err)
					}
					b.WriteString(copyLine + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	// Main image
	b.WriteString("FROM ${BASE_IMAGE}\n\n")

	// Bootstrap preamble (only for external base images)
	if img.IsExternalBase {
		g.writeBootstrap(&b, img)
	} else {
		// Internal base - reset to root for layer processing
		b.WriteString("USER root\n\n")
	}

	// Collect and write environment variables from layers
	g.writeLayerEnv(&b, layerOrder, img)

	// Emit EXPOSE directives for layer ports
	g.writeExpose(&b, layerOrder)

	// Emit image metadata labels
	g.writeLabels(&b, imageName, layerOrder, img)

	// Copy builder artifacts — fully config-driven from builder.yml copy_artifacts/copy_binary
	if img.BuilderConfig != nil {
		builderNames := img.BuilderConfig.BuilderNames()
		for _, builderName := range builderNames {
			builderDef := img.BuilderConfig.Builders[builderName]
			if builderDef.Inline || builderDef.StageTemplate == "" {
				continue
			}

			// Find layers that triggered this builder
			hasArtifacts := false
			binaryCopied := false
			for _, layerName := range layerOrder {
				layer := g.Layers[layerName]
				if !g.layerNeedsBuilder(layer, builderDef) {
					continue
				}
				stageName := fmt.Sprintf("%s-%s-build", layer.Name, builderName)

				// Copy artifacts
				for _, art := range builderDef.CopyArtifacts {
					if !hasArtifacts {
						b.WriteString(fmt.Sprintf("# Copy %s artifacts\n", builderName))
						hasArtifacts = true
					}
					src := expandBuilderPath(art.Src, img)
					dst := expandBuilderPath(art.Dst, img)
					if art.Chown {
						b.WriteString(fmt.Sprintf("COPY --from=%s --chown=%d:%d %s %s\n", stageName, img.UID, img.GID, src, dst))
					} else {
						b.WriteString(fmt.Sprintf("COPY --from=%s %s %s\n", stageName, src, dst))
					}
				}

				// Copy binary (only once, from first matching layer)
				if builderDef.CopyBinary != nil && !binaryCopied {
					b.WriteString(fmt.Sprintf("COPY --from=%s %s %s\n", stageName, builderDef.CopyBinary.Src, builderDef.CopyBinary.Dst))
					binaryCopied = true
				}
			}
			if hasArtifacts || binaryCopied {
				b.WriteString("\n")
			}
		}
	}

	// Copy extracted files from multi-stage builds
	hasExtract := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasExtract {
			continue
		}
		if !hasExtract {
			b.WriteString("# Copy extracted files from Docker images\n")
			b.WriteString("USER root\n")
			hasExtract = true
		}
		for i, ext := range layer.Extract() {
			stageName := fmt.Sprintf("%s-extract-%d", layerName, i)
			b.WriteString(fmt.Sprintf("COPY --from=%s --chown=%d:%d %s %s\n",
				stageName, img.UID, img.GID, ext.Path, ext.Dest))
		}
	}
	if hasExtract {
		b.WriteString("\n")
	}

	// Process each layer
	// Post-layer steps (init assembly, traefik, bootc) run as root,
	// so the last layer must reset to root only if such steps exist.
	needsRootAfter := len(activeInits) > 0 || (hasRoutes && hasTraefik) || img.Bootc
	inUserMode := false
	for i, layerName := range layerOrder {
		isLast := i == len(layerOrder)-1
		inUserMode = g.writeLayerSteps(&b, layerName, img, isLast && !needsRootAfter)
	}

	// Assemble init system configs (driven by init.yml templates)
	for initName, def := range activeInits {
		assembly, err := def.RenderAssemblyTemplate()
		if err != nil {
			return fmt.Errorf("rendering assembly for %s: %w", initName, err)
		}
		if assembly != "" {
			b.WriteString(assembly)
			if !strings.HasSuffix(assembly, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		// System-level service enablement (e.g., systemctl enable sshd)
		var systemUnits []string
		for _, layerName := range layerOrder {
			layer := g.Layers[layerName]
			systemUnits = append(systemUnits, layer.SystemServiceUnits()...)
		}
		sysEnable, err := def.RenderSystemEnableTemplate(systemUnits)
		if err != nil {
			return fmt.Errorf("rendering system enable for %s: %w", initName, err)
		}
		if sysEnable != "" {
			b.WriteString(sysEnable)
			if !strings.HasSuffix(sysEnable, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}

		// Post-assembly step (e.g., bootc container lint)
		postAssembly, err := def.RenderPostAssemblyTemplate()
		if err != nil {
			return fmt.Errorf("rendering post-assembly for %s: %w", initName, err)
		}
		if postAssembly != "" {
			b.WriteString(postAssembly)
			if !strings.HasSuffix(postAssembly, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// Copy traefik dynamic routes if needed
	if hasRoutes && hasTraefik {
		b.WriteString("# Traefik dynamic routes\n")
		b.WriteString("COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml\n\n")
	}

	// Final USER directive (use UID for robustness)
	// Bootc images boot with systemd which manages users via login —
	// the container USER directive is irrelevant (bootc+systemd handles sessions).
	if img.Bootc {
		// leave as root — systemd handles user sessions
	} else if !inUserMode || needsRootAfter {
		b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
	}

	// imageDir was cleaned at the start of this function; ensure it exists
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	content := b.String()
	g.Containerfiles[imageName] = content

	containerfile := filepath.Join(imageDir, "Containerfile")
	return os.WriteFile(containerfile, []byte(content), 0644)
}

// resolveBaseImage returns the full base image reference.
// For internal bases, uses the exact CalVer tag so each image references
// the precise version of its parent. Both Docker and Podman resolve local
// images before pulling from registry.
func (g *Generator) resolveBaseImage(img *ResolvedImage) string {
	if img.IsExternalBase {
		return img.Base
	}
	parentImg := g.Images[img.Base]
	return parentImg.FullTag
}

// builderRefForFormat returns the full tag of the builder image for a given format,
// or "" if no builder is configured for that format.
func (g *Generator) builderRefForFormat(imageName, format string) string {
	img := g.Images[imageName]
	builder := img.Builders.BuilderFor(format)
	if builder == "" || builder == imageName {
		return ""
	}
	if builderImg, ok := g.Images[builder]; ok {
		return builderImg.FullTag
	}
	return ""
}

// writeBootstrap writes the bootstrap preamble for external base images.
// All distro-specific behavior is driven by distro.yml config.
func (g *Generator) writeBootstrap(b *strings.Builder, img *ResolvedImage) {
	b.WriteString("# Bootstrap\n")

	// Resolve distro config for bootstrap commands
	var distroDef *DistroDef
	if img.DistroConfig != nil {
		distroDef = img.DistroConfig.ResolveDistro(img.Distro)
	}

	b.WriteString("RUN ")
	// Cache mounts from distro config (or fall back to primary format's mounts)
	var cacheMounts []CacheMountDef
	if distroDef != nil {
		cacheMounts = distroDef.Bootstrap.CacheMounts
	} else if img.DistroDef != nil {
		if formatDef, ok := img.DistroDef.Formats[img.Pkg]; ok {
			cacheMounts = formatDef.CacheMounts
		}
	}
	for _, m := range cacheMounts {
		sharing := m.Sharing
		if sharing == "" {
			sharing = "locked"
		}
		b.WriteString(fmt.Sprintf("--mount=type=cache,dst=%s,sharing=%s \\\n    ", m.Dst, sharing))
	}

	// Install bootstrap packages using distro's install command
	if distroDef != nil && distroDef.Bootstrap.InstallCmd != "" && len(distroDef.Bootstrap.Packages) > 0 {
		b.WriteString(fmt.Sprintf("%s %s && \\\n    ", distroDef.Bootstrap.InstallCmd, strings.Join(distroDef.Bootstrap.Packages, " ")))
	}

	// Apply distro-specific workarounds
	if distroDef != nil {
		for _, w := range distroDef.Workarounds {
			b.WriteString(w + " && \\\n    ")
		}
	}

	b.WriteString("{ [ -L /usr/local ] && mkdir -p \"$(readlink /usr/local)\"; mkdir -p /usr/local/bin; } && \\\n")
	b.WriteString("    ARCH=$(uname -m) && \\\n")
	b.WriteString("    case \"$ARCH\" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac && \\\n")
	b.WriteString("    curl -fsSL \"https://github.com/go-task/task/releases/latest/download/task_linux_${ARCH}.tar.gz\" | tar -xzf - -C /usr/local/bin task\n\n")

	// Create user/group if they don't exist at configured UID/GID
	b.WriteString(fmt.Sprintf("RUN getent passwd %d >/dev/null 2>&1 || \\\n", img.UID))
	b.WriteString(fmt.Sprintf("    (getent group %d >/dev/null 2>&1 || groupadd -g %d %s && \\\n", img.GID, img.GID, img.User))
	b.WriteString(fmt.Sprintf("     useradd -m -u %d -g %d -s /bin/bash %s)\n\n", img.UID, img.GID, img.User))

	// WORKDIR only - ENV comes from layer env files
	b.WriteString(fmt.Sprintf("WORKDIR %s\n\n", img.Home))
}

// writeLayerEnv collects env configs from all layers and writes ENV directives
func (g *Generator) writeLayerEnv(b *strings.Builder, layerOrder []string, img *ResolvedImage) {
	var configs []*EnvConfig

	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasEnv {
			cfg, err := layer.EnvConfig()
			if err == nil && cfg != nil {
				configs = append(configs, cfg)
			}
		}
	}

	if len(configs) == 0 {
		return
	}

	// Merge all configs
	merged := MergeEnvConfigs(configs)

	// Expand paths with home directory
	expanded := ExpandEnvConfig(merged, img.Home)

	// Write ENV directives
	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("# Layer environment variables\n")
	}

	// Sort keys for deterministic output (prevents Docker cache invalidation)
	keys := make([]string, 0, len(expanded.Vars))
	for key := range expanded.Vars {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", key, expanded.Vars[key]))
	}

	// Append to PATH if there are path additions
	if len(expanded.PathAppend) > 0 {
		pathAdditions := strings.Join(expanded.PathAppend, ":")
		b.WriteString(fmt.Sprintf("ENV PATH=\"%s:${PATH}\"\n", pathAdditions))
	}

	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("\n")
	}
}

// writeExpose collects ports from all layers, deduplicates, sorts, and emits EXPOSE directives
func (g *Generator) writeExpose(b *strings.Builder, layerOrder []string) {
	seen := make(map[string]bool)
	var ports []string

	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasPorts {
			continue
		}
		layerPorts, err := layer.Ports()
		if err != nil {
			continue
		}
		for _, port := range layerPorts {
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}

	if len(ports) == 0 {
		return
	}

	sortStrings(ports)
	b.WriteString("# Exposed ports\n")
	for _, port := range ports {
		b.WriteString(fmt.Sprintf("EXPOSE %s\n", port))
	}
	b.WriteString("\n")
}

// generateTraefikRoutes generates a traefik dynamic config YAML for route layers
func (g *Generator) generateTraefikRoutes(imageName string, layerOrder []string, img *ResolvedImage) error {
	var b strings.Builder

	b.WriteString("# .build/" + imageName + "/traefik-routes.yml (generated -- do not edit)\n")
	b.WriteString("http:\n")
	b.WriteString("  routers:\n")

	// Collect routes in layer order (deterministic)
	type routeEntry struct {
		name string
		cfg  *RouteConfig
	}
	var routes []routeEntry
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasRoute {
			continue
		}
		route, err := layer.Route()
		if err != nil || route == nil {
			continue
		}
		routes = append(routes, routeEntry{name: layerName, cfg: route})
	}

	for _, r := range routes {
		// Use image DNS if configured, otherwise layer's host
		host := r.cfg.Host
		if img.DNS != "" {
			host = img.DNS
		}

		b.WriteString(fmt.Sprintf("    %s:\n", r.name))
		b.WriteString(fmt.Sprintf("      rule: \"Host(`%s`)\"\n", host))
		b.WriteString(fmt.Sprintf("      service: %s\n", r.name))
		b.WriteString("      entryPoints:\n")
		b.WriteString("        - websecure\n")
		b.WriteString("      tls:\n")
		b.WriteString("        certResolver: letsencrypt\n")
	}

	b.WriteString("  services:\n")
	for _, r := range routes {
		b.WriteString(fmt.Sprintf("    %s:\n", r.name))
		b.WriteString("      loadBalancer:\n")
		b.WriteString("        servers:\n")
		b.WriteString(fmt.Sprintf("          - url: \"http://127.0.0.1:%s\"\n", r.cfg.Port))
	}

	imageDir := filepath.Join(g.BuildDir, imageName)
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(imageDir, "traefik-routes.yml"), []byte(b.String()), 0644)
}

// generateInitFragments writes init system config fragments to .build/<image>/<fragmentDir>/
// Handles both fragment_assembly (supervisord) and file_copy (systemd) models.
func (g *Generator) generateInitFragments(imageName, initName string, def *InitDef, layerOrder []string) error {
	fragDir := filepath.Join(g.BuildDir, imageName, def.FragmentDir)
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		return err
	}

	for i, layerName := range layerOrder {
		layer := g.Layers[layerName]

		// Fragment assembly model: render service content via template
		if def.Model == "fragment_assembly" && layer.HasInit(initName) {
			content, err := def.RenderFragmentTemplate(layer.ServiceConf(), layerName, i+1)
			if err != nil {
				return fmt.Errorf("rendering fragment for %s/%s: %w", initName, layerName, err)
			}
			fragFile := filepath.Join(fragDir, fmt.Sprintf("%02d-%s.conf", i+1, layerName))
			if err := os.WriteFile(fragFile, []byte(content), 0644); err != nil {
				return err
			}
		}

		// Port relay fragments (via relay_template)
		if def.HasRelayTemplate() && len(layer.PortRelayPorts) > 0 {
			for _, port := range layer.PortRelayPorts {
				content, err := def.RenderRelayTemplate(port, layerName, i+1)
				if err != nil {
					return fmt.Errorf("rendering relay for %s/%s port %d: %w", initName, layerName, port, err)
				}
				confName := fmt.Sprintf("%02d-relay-%d.conf", i+1, port)
				fragFile := filepath.Join(fragDir, confName)
				if err := os.WriteFile(fragFile, []byte(content), 0644); err != nil {
					return err
				}
			}
		}

		// File copy model: copy detected service files
		if def.Model == "file_copy" {
			for _, svcPath := range layer.ServiceFiles() {
				content, err := os.ReadFile(svcPath)
				if err != nil {
					return fmt.Errorf("reading service file %s: %w", svcPath, err)
				}
				destFile := filepath.Join(fragDir, filepath.Base(svcPath))
				if err := os.WriteFile(destFile, content, 0644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// writeLayerSteps writes the RUN steps for a single layer.
// skipRootReset prevents emitting USER root after user-mode steps (used for the
// last layer when no post-layer root steps follow).
// Returns true if the layer ended in user mode.
func (g *Generator) writeLayerSteps(b *strings.Builder, layerName string, img *ResolvedImage, skipRootReset bool) bool {
	layer := g.Layers[layerName]
	stageName := layer.Name // short name used as scratch stage alias

	b.WriteString(fmt.Sprintf("# Layer: %s\n", layerName))

	// Track if we've switched to user mode
	asUser := false

	// 1. System packages from layer.yml — fully config-driven.
	// Phase 1: Walk distro: tags — first matching section wins (override).
	// Phase 2: If no distro match, walk build: formats — ALL matching sections installed in order.
	distroMatched := false
	for _, tag := range img.Distro {
		if tagCfg := layer.TagSection(tag); tagCfg != nil && len(tagCfg.Packages) > 0 {
			g.renderFormatInstallFromPackages(b, tagCfg.Packages, img.Pkg, img)
			distroMatched = true
			break
		}
	}

	if !distroMatched {
		for _, format := range img.BuildFormats {
			section := layer.FormatSection(format)
			if section == nil || len(section.Packages) == 0 {
				continue
			}
			if img.DistroDef == nil || img.DistroDef.Formats == nil {
				continue
			}
			formatDef := img.DistroDef.Formats[format]
			if formatDef == nil {
				continue
			}
			// Check if this format has a builder (multi-stage install step)
			if builderDef, ok := img.BuilderConfig.Builders[format]; ok && !builderDef.Inline {
				// Format with builder: use the format's install_template (e.g., aur COPY + pacman -U)
				ctx := &InstallContext{
					CacheMounts: formatDef.CacheMounts,
					Packages:    section.Packages,
					StageName:   fmt.Sprintf("%s-%s-build", layer.Name, format),
				}
				rendered, err := RenderTemplate(format+"-install", formatDef.InstallTemplate, ctx)
				if err == nil {
					b.WriteString(rendered)
				}
			} else {
				// Regular format: render install template
				ctx := NewInstallContext(section.Raw, formatDef.CacheMounts)
				rendered, err := RenderTemplate(format+"-install", formatDef.InstallTemplate, ctx)
				if err == nil {
					b.WriteString(rendered)
				}
			}
		}
	}

	// 2. root.yml (root)
	if layer.HasRootYml {
		g.writeRootYml(b, stageName, layer, img)
	}

	// 4. Inline builders (cargo, etc.) — config-driven from builder.yml
	if img.BuilderConfig != nil {
		for _, bName := range img.BuilderConfig.BuilderNames() {
			bDef := img.BuilderConfig.Builders[bName]
			if !bDef.Inline || bDef.InstallTemplate == "" {
				continue
			}
			if !g.layerNeedsBuilder(layer, bDef) {
				continue
			}
			if !asUser {
				b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
				asUser = true
			}
			ctx := &BuildStageContext{
				LayerStage:  stageName,
				UID:         img.UID,
				GID:         img.GID,
				CacheMounts: bDef.CacheMounts,
			}
			rendered, err := RenderTemplate(bName+"-inline", bDef.InstallTemplate, ctx)
			if err == nil {
				b.WriteString(rendered)
			}
		}
	}

	// 5. user.yml (user)
	if layer.HasUserYml {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writeUserYml(b, stageName, layer, img)
	}

	// Reset to root for next layer (skip for last layer when no root steps follow)
	if asUser && !skipRootReset {
		b.WriteString("USER root\n")
	}

	b.WriteString("\n")
	return asUser
}

// Old format-specific write functions removed — all generation is now
// config-driven via distro.yml format templates rendered by renderFormatInstall*
// and builder.yml templates rendered by buildStageContext + RenderTemplate.

func (g *Generator) writeRootYml(b *strings.Builder, layerName string, layer *Layer, img *ResolvedImage) {
	// Resolve which tasks to call: intersection of image tags and defined tasks
	tasks := img.MatchingTasks(layer.RootYmlTasks)
	if len(tasks) == 0 {
		return // no matching tasks for this image's tags
	}

	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	// Cache mounts from primary format's config
	var formatDef *FormatDef
	if img.DistroDef != nil {
		formatDef = img.DistroDef.Formats[img.Pkg]
	}
	if formatDef != nil {
		for _, m := range formatDef.CacheMounts {
			sharing := m.Sharing
			if sharing == "" {
				sharing = "locked"
			}
			b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=%s,sharing=%s \\\n", m.Dst, sharing))
		}
	}
	b.WriteString(fmt.Sprintf("    cd /ctx && task -t root.yml %s\n", strings.Join(tasks, " ")))
}

// expandBuilderPath replaces {{.Home}} placeholders in copy artifact paths.
func expandBuilderPath(path string, img *ResolvedImage) string {
	path = strings.ReplaceAll(path, "{{.Home}}", img.Home)
	return path
}

// layerNeedsBuilder checks if a layer triggers a builder's detection criteria.
func (g *Generator) layerNeedsBuilder(layer *Layer, builderDef *BuilderDef) bool {
	for _, f := range builderDef.DetectFiles {
		if layerHasFile(layer, f) {
			return true
		}
	}
	if builderDef.DetectConfig != "" {
		section := layer.FormatSection(builderDef.DetectConfig)
		if section != nil && len(section.Packages) > 0 {
			return true
		}
	}
	return false
}

// buildStageContext creates the template context for a builder's stage_template.
func (g *Generator) buildStageContext(layer *Layer, builderName string, builderDef *BuilderDef, img *ResolvedImage, builderRef string) *BuildStageContext {
	stageName := fmt.Sprintf("%s-%s-build", layer.Name, builderName)
	ctx := &BuildStageContext{
		BuilderRef:  builderRef,
		StageName:   stageName,
		LayerStage:  layer.Name,
		CopySrc:     g.layerCopySource(layer.Name),
		UID:         img.UID,
		GID:         img.GID,
		Home:        img.Home,
		User:        img.User,
		CacheMounts: builderDef.CacheMounts,
	}

	// Resolve manifest and install command for file-detected builders (pixi)
	if len(builderDef.InstallCommands) > 0 && len(builderDef.DetectFiles) > 0 {
		manifest := ""
		for _, f := range builderDef.DetectFiles {
			if layerHasFile(layer, f) {
				manifest = f
				break
			}
		}
		ctx.Manifest = manifest
		ctx.HasLockFile = fileExists(filepath.Join(layer.Path, manifest+".lock")) ||
			(manifest == "pixi.toml" && layer.HasPixiLock)

		// Select install command from builder config
		if ctx.HasLockFile {
			if cmd, ok := builderDef.InstallCommands[manifest+"+lock"]; ok {
				ctx.InstallCmd = cmd
			}
		}
		if ctx.InstallCmd == "" {
			if cmd, ok := builderDef.InstallCommands[manifest]; ok {
				ctx.InstallCmd = cmd
			}
		}
	}

	// Resolve manylinux fix template
	if builderDef.ManylinuxFix != "" && ctx.Manifest != "" {
		rendered, err := RenderTemplate(builderName+"-manylinux", builderDef.ManylinuxFix, ctx)
		if err == nil {
			ctx.ManylinuxFix = rendered
		}
	}

	// For config-detected builders (aur), extract packages/options from layer config
	if builderDef.DetectConfig != "" {
		section := layer.FormatSection(builderDef.DetectConfig)
		if section != nil {
			ctx.Packages = section.Packages
			ctx.Options = toStringSlice(section.Raw["options"])
		}
	}

	return ctx
}

// renderFormatInstallFromPackages renders install for a package list using the primary format's template.
// Used for distro-override sections that only have packages (no repos, options, etc.).
func (g *Generator) renderFormatInstallFromPackages(b *strings.Builder, packages []string, primaryFormat string, img *ResolvedImage) {
	if img.DistroDef == nil {
		return
	}
	formatDef := img.DistroDef.Formats[primaryFormat]
	if formatDef == nil {
		return
	}
	ctx := &InstallContext{
		CacheMounts: formatDef.CacheMounts,
		Packages:    packages,
	}
	rendered, err := RenderTemplate(primaryFormat+"-tag-install", formatDef.InstallTemplate, ctx)
	if err == nil {
		b.WriteString(rendered)
	}
}

func (g *Generator) writeUserYml(b *strings.Builder, layerName string, layer *Layer, img *ResolvedImage) {
	// Resolve which tasks to call: intersection of image tags and defined tasks
	tasks := img.MatchingTasks(layer.UserYmlTasks)
	if len(tasks) == 0 {
		return // no matching tasks for this image's tags
	}

	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=/tmp/npm-cache,uid=%d,gid=%d \\\n", img.UID, img.GID))
	b.WriteString(fmt.Sprintf("    cd /ctx && task -t user.yml %s\n", strings.Join(tasks, " ")))
}

// writeLabels emits OCI LABEL directives with all runtime-relevant metadata.
// Every runtime config option is embedded so images are fully self-contained.
func (g *Generator) writeLabels(b *strings.Builder, imageName string, layerOrder []string, img *ResolvedImage) {
	b.WriteString("# Image metadata\n")

	// Always-present labels
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelVersion, LabelSchemaVersion))
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelImage, imageName))
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelUID, strconv.Itoa(img.UID)))
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelGID, strconv.Itoa(img.GID)))
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelUser, img.User))
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelHome, img.Home))

	// Conditional string labels (omitted when empty)
	if img.Registry != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelRegistry, img.Registry))
	}
	if img.Bootc {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelBootc, "true"))
	}
	if img.Network != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelNetwork, img.Network))
	}
	// Emit resolved engine label (includes layer-level requirements)
	resolvedEngine := ResolveImageEngine(g.Config, g.Layers, imageName, "")
	if resolvedEngine != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelEngine, resolvedEngine))
	}
	if img.DNS != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelDNS, img.DNS))
	}
	if img.AcmeEmail != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelAcmeEmail, img.AcmeEmail))
	}

	// Distro, build, and builder labels
	writeJSONLabel(b, LabelTags, img.Tags)
	writeJSONLabel(b, LabelDistro, img.Distro)
	writeJSONLabel(b, LabelBuild, img.BuildFormats)
	if len(img.Builders) > 0 {
		writeJSONLabel(b, LabelBuilders, map[string]string(img.Builders))
	}
	writeJSONLabel(b, LabelBuilds, img.BuilderCapabilities)

	// JSON array labels (omitted when empty)
	writeJSONLabel(b, LabelPorts, img.Ports)

	// Port protocols: collect from layer PortSpec declarations
	portProtos := make(map[string]string)
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		for _, ps := range layer.PortSpecs() {
			if ps.Protocol != "" && ps.Protocol != "http" {
				portProtos[strconv.Itoa(ps.Port)] = ps.Protocol
			}
		}
	}
	writeJSONLabel(b, LabelPortProtos, portProtos)

	// Volumes: short form names (without ov-<image>- prefix)
	volumes, _ := CollectImageVolumes(g.Config, g.Layers, imageName, img.Home, nil)
	if len(volumes) > 0 {
		var labelVols []LabelVolume
		for _, v := range volumes {
			shortName := strings.TrimPrefix(v.VolumeName, "ov-"+imageName+"-")
			labelVols = append(labelVols, LabelVolume{Name: shortName, Path: v.ContainerPath})
		}
		writeJSONLabel(b, LabelVolumes, labelVols)
	}

	// Aliases: collected from layers + image-level config
	aliases, _ := CollectImageAliases(g.Config, g.Layers, imageName)
	writeJSONLabel(b, LabelAliases, aliases)

	// Security: collected from layers + image config
	security := CollectSecurity(g.Config, g.Layers, imageName)
	if security.Privileged || len(security.CapAdd) > 0 || len(security.Devices) > 0 || len(security.SecurityOpt) > 0 || len(security.GroupAdd) > 0 || security.ShmSize != "" || len(security.Mounts) > 0 {
		writeJSONLabel(b, LabelSecurity, security)
	}

	// Tunnel config
	imgCfg := g.Config.Images[imageName]
	if imgCfg.Tunnel != nil {
		writeJSONLabel(b, LabelTunnel, imgCfg.Tunnel)
	}

	// Image-level env vars
	writeJSONLabel(b, LabelEnv, imgCfg.Env)

	// Hooks: collected from layers
	hooks := CollectHooks(g.Config, g.Layers, imageName)
	if hooks != nil {
		writeJSONLabel(b, LabelHooks, hooks)
	}

	// Bootc-only labels: VM config, libvirt snippets
	if img.Bootc {
		if img.Vm != nil {
			writeJSONLabel(b, LabelVm, img.Vm)
		}

		libvirtSnippets := CollectLibvirtSnippets(g.Config, g.Layers, imageName)
		writeJSONLabel(b, LabelLibvirt, libvirtSnippets)
	}

	// Init system label: active init system name + per-init service list
	if img.InitConfig != nil {
		labelInitSystem, labelInitDef := img.InitConfig.ResolveInitSystem(g.Layers, layerOrder, img.Bootc, "")
		if labelInitSystem != "" && labelInitDef != nil {
			b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelInit, labelInitSystem))
			// Collect service names for this init system
			var serviceNames []string
			for _, layerName := range layerOrder {
				layer := g.Layers[layerName]
				if layer.HasInit(labelInitSystem) {
					serviceNames = append(serviceNames, layerName)
				}
			}
			if labelInitDef.LabelKey != "" {
				writeJSONLabel(b, labelInitDef.LabelKey, serviceNames)
			}
		}
	}

	// Port relay: collected from layers
	var portRelay []int
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		portRelay = append(portRelay, layer.PortRelayPorts...)
	}
	writeJSONLabel(b, LabelPortRelay, portRelay)

	// Secrets: collected from layers (metadata only, no values)
	// Deduplicate by name+env composite key: same podman secret can inject into multiple env vars.
	var labelSecrets []LabelSecret
	secretSeen := make(map[string]bool)
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		for _, s := range layer.Secrets() {
			key := s.Name + ":" + s.Env
			if secretSeen[key] {
				continue
			}
			secretSeen[key] = true
			target := s.Target
			if target == "" {
				target = "/run/secrets/" + s.Name
			}
			labelSecrets = append(labelSecrets, LabelSecret{
				Name:   s.Name,
				Target: target,
				Env:    s.Env,
			})
		}
	}
	if len(labelSecrets) > 0 {
		writeJSONLabel(b, LabelSecrets, labelSecrets)
	}

	// Routes: collected from layers
	var routes []LabelRoute
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasRoute {
			rc, err := layer.Route()
			if err == nil && rc != nil {
				port, _ := strconv.Atoi(rc.Port)
				routes = append(routes, LabelRoute{Host: rc.Host, Port: port})
			}
		}
	}
	writeJSONLabel(b, LabelRoutes, routes)

	// Layer env vars: merged from all layers
	var envConfigs []*EnvConfig
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.envConfig != nil {
			envConfigs = append(envConfigs, layer.envConfig)
		}
	}
	if len(envConfigs) > 0 {
		merged := MergeEnvConfigs(envConfigs)
		if len(merged.Vars) > 0 {
			writeJSONLabel(b, LabelEnvLayers, merged.Vars)
		}
		writeJSONLabel(b, LabelPathAppend, merged.PathAppend)
	}

	// Skills documentation URL
	skillPath := filepath.Join(g.Dir, "plugins", "ov-images", "skills", imageName, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		skillURL := fmt.Sprintf("https://github.com/overthinkos/overthink-plugins/blob/main/ov-images/skills/%s/SKILL.md", imageName)
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelSkills, skillURL))
	}

	// Status and info: aggregate worst status from image + layers
	effectiveStatus := img.Status
	var infoParts []string
	if img.Info != "" {
		infoParts = append(infoParts, img.Info)
	}
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		layerStatus := layer.Status
		effectiveStatus = worstStatus(effectiveStatus, layerStatus)
		if layer.Info != "" && resolveStatus(layerStatus) != "working" {
			infoParts = append(infoParts, layerName+": "+layer.Info)
		}
	}
	resolvedStatus := resolveStatus(effectiveStatus)
	b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelStatus, resolvedStatus))
	if len(infoParts) > 0 {
		combinedInfo := strings.Join(infoParts, "; ")
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelInfo, combinedInfo))
	}

	// Layer versions: map of layer name -> CalVer for layers with version set
	layerVersions := make(map[string]string)
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.Version != "" {
			layerVersions[layerName] = layer.Version
		}
	}
	writeJSONLabel(b, LabelLayerVersions, layerVersions)

	b.WriteString("\n")
}

// writeJSONLabel writes a JSON-encoded LABEL directive. Omits the label if the value is nil/empty.
func writeJSONLabel[T any](b *strings.Builder, key string, value T) {
	// Check for nil/empty slices and maps via JSON
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	s := string(data)
	if s == "null" || s == "[]" || s == "{}" {
		return
	}
	b.WriteString(fmt.Sprintf("LABEL %s='%s'\n", key, s))
}

// resolveStatus returns the effective status string. Empty defaults to "testing".
func resolveStatus(s string) string {
	if s == "" {
		return "testing"
	}
	return s
}

// statusSeverity returns a numeric severity for status comparison.
func statusSeverity(s string) int {
	switch resolveStatus(s) {
	case "working":
		return 0
	case "testing":
		return 1
	case "broken":
		return 2
	default:
		return 1 // unknown treated as testing
	}
}

// worstStatus returns the more severe of two status values.
func worstStatus(a, b string) string {
	if statusSeverity(b) > statusSeverity(a) {
		return resolveStatus(b)
	}
	return resolveStatus(a)
}

// createRemoteLayerCopies copies remote layer directories into .build/_layers/
// so that Docker/Podman can access them from the build context.
// Uses hard copies instead of symlinks because Podman doesn't follow symlinks
// that point outside the build context.
func (g *Generator) createRemoteLayerCopies() error {
	hasRemote := false
	for _, layer := range g.Layers {
		if layer.Remote {
			hasRemote = true
			break
		}
	}
	if !hasRemote {
		// Clean up _layers dir if it exists from a previous run
		os.RemoveAll(filepath.Join(g.BuildDir, "_layers"))
		return nil
	}

	layersDir := filepath.Join(g.BuildDir, "_layers")
	// Remove and recreate to ensure clean state
	os.RemoveAll(layersDir)
	if err := os.MkdirAll(layersDir, 0755); err != nil {
		return err
	}

	for ref, layer := range g.Layers {
		if !layer.Remote {
			continue
		}
		destPath := filepath.Join(layersDir, layer.Name)
		cmd := exec.Command("cp", "-a", layer.Path, destPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("copying remote layer %s: %s: %w", ref, string(out), err)
		}
	}

	return nil
}

// layerCopySource returns the COPY source path for a layer in the Containerfile.
// Local layers use "layers/<name>/", remote layers use ".build/_layers/<name>/".
func (g *Generator) layerCopySource(layerRef string) string {
	layer := g.Layers[layerRef]
	if layer.Remote {
		return ".build/_layers/" + layer.Name
	}
	return "layers/" + layerRef
}

