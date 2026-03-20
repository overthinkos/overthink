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

	layers, err := ScanAllLayersWithConfig(dir, cfg)
	if err != nil {
		return nil, err
	}

	if err := Validate(cfg, layers); err != nil {
		return nil, err
	}

	// Compute CalVer if tag not specified
	if tag == "" {
		tag = ComputeCalVer()
	}

	images, err := cfg.ResolveAllImages(tag)
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

	// Resolve builder ref for this image (builder itself doesn't use builder stages)
	builderRef := g.builderRefForImage(imageName)

	// Emit per-layer pixi build stages
	// Cache mounts for pixi/rattler caches prevent bloating build stage layers
	// (e.g. CUDA libraries cached by pixi can add 10GB+ to intermediate layers)
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		manifest := layer.PixiManifest()
		if manifest != "" {
			if builderRef == "" {
				return fmt.Errorf("image %q: layer %q has pixi manifest but no builder configured", imageName, layerName)
			}
			copySrc := g.layerCopySource(layerName)
			b.WriteString(fmt.Sprintf("FROM %s AS %s-pixi-build\n", builderRef, layer.Name))
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			b.WriteString(fmt.Sprintf("WORKDIR %s\n", img.Home))
			if layer.HasPixiLock {
				b.WriteString(fmt.Sprintf("COPY --chown=%d:%d %s/pixi.lock pixi.lock\n", img.UID, img.GID, copySrc))
			}
			b.WriteString(fmt.Sprintf("COPY --chown=%d:%d %s/%s %s\n", img.UID, img.GID, copySrc, manifest, manifest))
			b.WriteString("ENV PIXI_CACHE_DIR=/tmp/pixi-cache\n")
			b.WriteString("ENV RATTLER_CACHE_DIR=/tmp/rattler-cache\n")
			cacheMounts := fmt.Sprintf("--mount=type=cache,dst=/tmp/pixi-cache,uid=%d,gid=%d "+
				"--mount=type=cache,dst=/tmp/rattler-cache,uid=%d,gid=%d ",
				img.UID, img.GID, img.UID, img.GID)
			// Install and then remove manifests so they're not included when we COPY the home dir
			cleanup := fmt.Sprintf(" && rm -f %s pixi.lock", manifest)
			if manifest == "environment.yml" {
				b.WriteString(fmt.Sprintf("RUN %spixi project import %s && pixi install%s\n", cacheMounts, manifest, cleanup))
			} else if manifest == "pyproject.toml" {
				b.WriteString(fmt.Sprintf("RUN %spixi install --manifest-path pyproject.toml%s\n", cacheMounts, cleanup))
			} else if layer.HasPixiLock {
				b.WriteString(fmt.Sprintf("RUN %spixi install --frozen%s\n", cacheMounts, cleanup))
			} else {
				b.WriteString(fmt.Sprintf("RUN %spixi install%s\n", cacheMounts, cleanup))
			}
			b.WriteString("\n")
		}
	}

	// Emit per-layer npm build stages
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasPackageJson {
			if builderRef == "" {
				return fmt.Errorf("image %q: layer %q has package.json but no builder configured", imageName, layerName)
			}
			copySrc := g.layerCopySource(layerName)
			b.WriteString(fmt.Sprintf("FROM %s AS %s-npm-build\n", builderRef, layer.Name))
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			b.WriteString(fmt.Sprintf("WORKDIR %s\n", img.Home))
			b.WriteString(fmt.Sprintf("COPY --chown=%d:%d %s/package.json package.json\n", img.UID, img.GID, copySrc))
				b.WriteString(fmt.Sprintf("RUN --mount=type=cache,dst=/tmp/npm-cache,uid=%d,gid=%d \\\n", img.UID, img.GID))
			b.WriteString("    node -e 'var d=require(\"./package.json\").dependencies||{};for(var[n,v]of Object.entries(d))console.log(v===\"*\"?n:n+\"@\"+v)' | xargs npm install -g && rm -f package.json\n\n")
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

	// Check if this is a service image (has supervisord layers or port relays)
	hasServices := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasSupervisord || layer.HasPortRelay {
			hasServices = true
			break
		}
	}

	// Check if this image has systemd service files (for bootc images)
	hasSystemdServices := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasSystemdServices {
			hasSystemdServices = true
			break
		}
	}

	// For bootc images with systemd services, skip supervisord assembly
	useSystemd := img.Bootc && hasSystemdServices
	useSupervisord := hasServices && !useSystemd

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

	// Emit supervisord config stage if needed
	// When a child image adds services, include parent-provided supervisor configs
	// too so the assembled supervisord.conf contains all services from the full chain.
	if useSupervisord {
		supervisordLayerOrder := layerOrder
		if !img.IsExternalBase {
			// Collect ALL layers across entire base chain for complete supervisord assembly
			full := collectAllImageLayers(imageName, g.Images, g.Layers)
			if len(full) > 0 {
				supervisordLayerOrder = full
			}
		}
		if err := g.generateSupervisordFragments(imageName, supervisordLayerOrder); err != nil {
			return err
		}
		b.WriteString("FROM scratch AS supervisord-conf\n")
		b.WriteString("COPY templates/supervisord.header.conf /supervisor/00-header.conf\n")
		for i, layerName := range supervisordLayerOrder {
			layer := g.Layers[layerName]
			if layer.HasSupervisord {
				b.WriteString(fmt.Sprintf("COPY .build/%s/supervisor/%02d-%s.conf /supervisor/%02d-%s.conf\n", imageName, i+1, layerName, i+1, layerName))
			}
			if layer.HasPortRelay {
				for _, port := range layer.PortRelay() {
					confName := fmt.Sprintf("%02d-relay-%d.conf", i+1, port)
					b.WriteString(fmt.Sprintf("COPY .build/%s/supervisor/%s /supervisor/%s\n", imageName, confName, confName))
				}
			}
		}
		b.WriteString("\n")
	}

	// Emit systemd services stage for bootc images
	if useSystemd {
		systemdLayerOrder := layerOrder
		if !img.IsExternalBase {
			full := collectAllImageLayers(imageName, g.Images, g.Layers)
			if len(full) > 0 {
				systemdLayerOrder = full
			}
		}
		if err := g.generateSystemdFragments(imageName, systemdLayerOrder); err != nil {
			return err
		}
		b.WriteString("FROM scratch AS systemd-services\n")
		for _, layerName := range systemdLayerOrder {
			layer := g.Layers[layerName]
			if !layer.HasSystemdServices {
				continue
			}
			for _, svcPath := range layer.SystemdServices {
				svcName := filepath.Base(svcPath)
				b.WriteString(fmt.Sprintf("COPY .build/%s/systemd/%s /systemd/%s\n", imageName, svcName, svcName))
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

	// Copy pixi environments
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.PixiManifest() != "" {
			b.WriteString(fmt.Sprintf("# Copy pixi environment: %s\n", layerName))
			b.WriteString(fmt.Sprintf("COPY --from=%s-pixi-build --chown=%d:%d %s %s\n", layer.Name, img.UID, img.GID, img.Home, img.Home))
		}
	}

	// Ensure pixi binary is present if any pixi layer exists
	hasPixi := false
	for _, layerName := range layerOrder {
		if g.Layers[layerName].PixiManifest() != "" {
			hasPixi = true
			break
		}
	}
	if hasPixi {
		for _, layerName := range layerOrder {
			layer := g.Layers[layerName]
			if layer.PixiManifest() != "" {
				b.WriteString(fmt.Sprintf("COPY --from=%s-pixi-build /usr/local/bin/pixi /usr/local/bin/pixi\n\n", layer.Name))
				break
			}
		}
	}

	// Copy npm environments from build stages
	hasNpm := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasPackageJson {
			if !hasNpm {
				b.WriteString("# Copy npm packages\n")
				hasNpm = true
			}
			b.WriteString(fmt.Sprintf("COPY --from=%s-npm-build --chown=%d:%d %s %s\n", layer.Name, img.UID, img.GID, img.Home, img.Home))
		}
	}
	if hasNpm {
		b.WriteString("\n")
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
	// Post-layer steps (supervisord, traefik, bootc) run as root,
	// so the last layer must reset to root only if such steps exist.
	needsRootAfter := useSupervisord || useSystemd || (hasRoutes && hasTraefik) || img.Bootc
	inUserMode := false
	for i, layerName := range layerOrder {
		isLast := i == len(layerOrder)-1
		inUserMode = g.writeLayerSteps(&b, layerName, img, isLast && !needsRootAfter)
	}

	// Assemble supervisord config if needed
	if useSupervisord {
		b.WriteString("# Assemble supervisord.conf\n")
		b.WriteString("RUN --mount=type=bind,from=supervisord-conf,source=/supervisor,target=/supervisor \\\n")
		b.WriteString("    cat /supervisor/*.conf > /etc/supervisord.conf\n\n")
	}

	// Install systemd service files for bootc images
	if useSystemd {
		b.WriteString("# Install systemd user services\n")
		b.WriteString("RUN --mount=type=bind,from=systemd-services,source=/systemd,target=/systemd \\\n")
		b.WriteString("    cp /systemd/*.service /usr/lib/systemd/user/ && \\\n")
		b.WriteString("    for svc in /systemd/*.service; do systemctl --global enable \"$(basename \"$svc\")\"; done\n\n")
	}

	// Copy traefik dynamic routes if needed
	if hasRoutes && hasTraefik {
		b.WriteString("# Traefik dynamic routes\n")
		b.WriteString("COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml\n\n")
	}

	// Enable system-level services (sshd, qemu-guest-agent, cloud-init, etc.)
	if img.Bootc {
		var systemUnits []string
		for _, layerName := range layerOrder {
			layer := g.Layers[layerName]
			if layer.HasSystemServices {
				systemUnits = append(systemUnits, layer.SystemServiceUnits...)
			}
		}
		if len(systemUnits) > 0 {
			b.WriteString("# Enable system-level services\n")
			b.WriteString("RUN")
			for i, unit := range systemUnits {
				if i > 0 {
					b.WriteString(" && \\\n   ")
				} else {
					b.WriteString(" ")
				}
				b.WriteString(fmt.Sprintf("systemctl enable %s", unit))
			}
			b.WriteString("\n\n")
		}
	}

	// Bootc lint if applicable (must run as root)
	if img.Bootc {
		b.WriteString("RUN bootc container lint\n\n")
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

// builderRefForImage returns the full tag of the builder image for a given image,
// or "" if the image has no builder or is the builder itself.
func (g *Generator) builderRefForImage(imageName string) string {
	img := g.Images[imageName]
	if img.Builder == "" || img.Builder == imageName {
		return ""
	}
	if builderImg, ok := g.Images[img.Builder]; ok {
		return builderImg.FullTag
	}
	return ""
}

// writeBootstrap writes the bootstrap preamble for external base images
func (g *Generator) writeBootstrap(b *strings.Builder, img *ResolvedImage) {
	b.WriteString("# Bootstrap\n")

	// Install task
	b.WriteString("RUN ")
	if img.Pkg == "deb" {
		b.WriteString("--mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
		b.WriteString("    apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && \\\n    ")
	} else {
		b.WriteString("--mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n    ")
	}
	// Remove repos with corrupt zchunk metadata (dnf5 skip_if_unavailable doesn't handle this;
	// disabling via sed is insufficient because the shared libdnf5 cache mount retains stale metadata)
	if img.Pkg == "rpm" {
		b.WriteString("rm -f /etc/yum.repos.d/terra-mesa.repo 2>/dev/null || true && \\\n    ")
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
		// Use image FQDN if configured, otherwise layer's host
		host := r.cfg.Host
		if img.FQDN != "" {
			host = img.FQDN
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

// generateSupervisordFragments writes supervisor configs from layer.yml to .build/<image>/supervisor/
func (g *Generator) generateSupervisordFragments(imageName string, layerOrder []string) error {
	fragDir := filepath.Join(g.BuildDir, imageName, "supervisor")
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		return err
	}

	for i, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasSupervisord {
			content := layer.ServiceConf()
			if !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			fragFile := filepath.Join(fragDir, fmt.Sprintf("%02d-%s.conf", i+1, layerName))
			if err := os.WriteFile(fragFile, []byte(content), 0644); err != nil {
				return err
			}
		}
		if layer.HasPortRelay {
			for _, port := range layer.PortRelay() {
				content := generateRelayConf(port)
				confName := fmt.Sprintf("%02d-relay-%d.conf", i+1, port)
				fragFile := filepath.Join(fragDir, confName)
				if err := os.WriteFile(fragFile, []byte(content), 0644); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// generateRelayConf returns a supervisord config fragment for a port relay.
func generateRelayConf(port int) string {
	return fmt.Sprintf(`[program:relay-%d]
command=/usr/local/bin/relay-wrapper %d
autostart=true
autorestart=true
priority=1
startsecs=0
stdout_logfile=/dev/fd/1
stdout_logfile_maxbytes=0
redirect_stderr=true
`, port, port)
}

// generateSystemdFragments copies systemd .service files from layers to .build/<image>/systemd/
func (g *Generator) generateSystemdFragments(imageName string, layerOrder []string) error {
	fragDir := filepath.Join(g.BuildDir, imageName, "systemd")
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		return err
	}

	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasSystemdServices {
			continue
		}
		for _, svcPath := range layer.SystemdServices {
			content, err := os.ReadFile(svcPath)
			if err != nil {
				return fmt.Errorf("reading systemd service %s: %w", svcPath, err)
			}
			destFile := filepath.Join(fragDir, filepath.Base(svcPath))
			if err := os.WriteFile(destFile, content, 0644); err != nil {
				return err
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

	// 1. rpm or deb packages from layer.yml (root)
	rpm := layer.RpmConfig()
	deb := layer.DebConfig()
	if img.Pkg == "rpm" && rpm != nil && len(rpm.Packages) > 0 {
		g.writeDnfInstall(b, rpm)
	} else if img.Pkg == "deb" && deb != nil && len(deb.Packages) > 0 {
		g.writeAptInstall(b, deb)
	}

	// 2. root.yml (root)
	if layer.HasRootYml {
		g.writeRootYml(b, stageName, img.Pkg)
	}

	// 4. Cargo.toml (user)
	if layer.HasCargoToml {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writeCargoToml(b, stageName, img)
	}

	// 5. user.yml (user)
	if layer.HasUserYml {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writeUserYml(b, stageName, img)
	}

	// Reset to root for next layer (skip for last layer when no root steps follow)
	if asUser && !skipRootReset {
		b.WriteString("USER root\n")
	}

	b.WriteString("\n")
	return asUser
}

func (g *Generator) writeDnfInstall(b *strings.Builder, rpm *RpmConfig) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n")

	// Release RPMs: install first to add repo definitions
	for _, repo := range rpm.Repos {
		if repo.RPM != "" {
			b.WriteString(fmt.Sprintf("    dnf install -y %q && \\\n", repo.RPM))
		}
	}

	// Repo files: add disabled, import GPG keys
	for _, repo := range rpm.Repos {
		if repo.URL != "" {
			b.WriteString(fmt.Sprintf("    dnf5 config-manager addrepo --from-repofile=%q 2>/dev/null || true && \\\n", repo.URL))
			b.WriteString(fmt.Sprintf("    dnf5 config-manager setopt %q && \\\n", repo.Name+".enabled=0"))
			if repo.GPGKey != "" {
				b.WriteString(fmt.Sprintf("    rpm --import %s || true && \\\n", repo.GPGKey))
			}
		}
	}

	// COPR repos: enable first
	for _, repo := range rpm.Copr {
		b.WriteString(fmt.Sprintf("    dnf5 copr enable -y %s && \\\n", repo))
	}

	// Module streams: reset and enable
	for _, mod := range rpm.Modules {
		moduleName := strings.SplitN(mod, ":", 2)[0]
		b.WriteString(fmt.Sprintf("    dnf module reset -y %s && \\\n", moduleName))
		b.WriteString(fmt.Sprintf("    dnf module enable -y %s && \\\n", mod))
	}

	b.WriteString("    dnf install -y")

	// Extra options
	for _, opt := range rpm.Options {
		b.WriteString(fmt.Sprintf(" %s", opt))
	}

	// Enable repos for this install (url-type repos only; rpm-type repos enable themselves)
	for _, repo := range rpm.Repos {
		if repo.URL != "" {
			b.WriteString(fmt.Sprintf(" --enable-repo=%q", repo.Name))
		}
	}

	// Exclude patterns
	for _, excl := range rpm.Exclude {
		b.WriteString(fmt.Sprintf(" --exclude='%s'", excl))
	}

	// Packages
	for _, pkg := range rpm.Packages {
		b.WriteString(fmt.Sprintf(" \\\n      %s", pkg))
	}

	// Disable COPR repos after install
	for _, repo := range rpm.Copr {
		b.WriteString(fmt.Sprintf(" && \\\n    dnf5 config-manager setopt \"copr:copr.fedorainfracloud.org:%s.enabled=0\"", strings.ReplaceAll(repo, "/", ":")))
	}

	b.WriteString("\n")
}

func (g *Generator) writeAptInstall(b *strings.Builder, deb *DebConfig) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
	b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
	b.WriteString("    apt-get update && apt-get install -y --no-install-recommends")
	for _, pkg := range deb.Packages {
		b.WriteString(fmt.Sprintf(" \\\n      %s", pkg))
	}
	b.WriteString("\n")
}

func (g *Generator) writeRootYml(b *strings.Builder, layerName string, pkg string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	if pkg == "deb" {
		b.WriteString("    --mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
	} else {
		b.WriteString("    --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n")
	}
	b.WriteString("    cd /ctx && task -t root.yml install\n")
}

func (g *Generator) writeCargoToml(b *strings.Builder, layerName string, img *ResolvedImage) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=/tmp/cargo-cache,uid=%d,gid=%d \\\n", img.UID, img.GID))
	b.WriteString("    cargo install --path /ctx\n")
}

func (g *Generator) writeUserYml(b *strings.Builder, layerName string, img *ResolvedImage) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=/tmp/npm-cache,uid=%d,gid=%d \\\n", img.UID, img.GID))
	b.WriteString("    cd /ctx && task -t user.yml install\n")
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
	if img.FQDN != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelFQDN, img.FQDN))
	}
	if img.AcmeEmail != "" {
		b.WriteString(fmt.Sprintf("LABEL %s=%q\n", LabelAcmeEmail, img.AcmeEmail))
	}

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

	// Bind mounts: strip host paths (host-specific), keep name/path/encrypted
	imgCfg := g.Config.Images[imageName]
	if len(imgCfg.BindMounts) > 0 {
		var labelMounts []LabelBindMount
		for _, bm := range imgCfg.BindMounts {
			labelMounts = append(labelMounts, LabelBindMount{
				Name:      bm.Name,
				Path:      bm.Path,
				Encrypted: bm.Encrypted,
			})
		}
		writeJSONLabel(b, LabelBindMounts, labelMounts)
	}

	// Security: collected from layers + image config
	security := CollectSecurity(g.Config, g.Layers, imageName)
	if security.Privileged || len(security.CapAdd) > 0 || len(security.Devices) > 0 || len(security.SecurityOpt) > 0 {
		writeJSONLabel(b, LabelSecurity, security)
	}

	// Tunnel config
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

	// Bootc-only labels: VM config, libvirt snippets, system services
	if img.Bootc {
		if img.Vm != nil {
			writeJSONLabel(b, LabelVm, img.Vm)
		}

		libvirtSnippets := CollectLibvirtSnippets(g.Config, g.Layers, imageName)
		writeJSONLabel(b, LabelLibvirt, libvirtSnippets)

		var systemdUnits []string
		for _, layerName := range layerOrder {
			layer := g.Layers[layerName]
			if layer.HasSystemServices {
				systemdUnits = append(systemdUnits, layer.SystemServiceUnits...)
			}
		}
		writeJSONLabel(b, LabelSystemd, systemdUnits)
	}

	// Supervisord services: collected from layers (all images)
	var supervisordServices []string
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasSupervisord {
			supervisordServices = append(supervisordServices, layerName)
		}
	}
	writeJSONLabel(b, LabelSupervisord, supervisordServices)

	// Port relay: collected from layers
	var portRelay []int
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		portRelay = append(portRelay, layer.PortRelay()...)
	}
	writeJSONLabel(b, LabelPortRelay, portRelay)

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

