package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ValidationError collects multiple validation errors
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return fmt.Sprintf("validation error: %s", e.Errors[0])
	}
	return fmt.Sprintf("%d validation errors:\n\n  %s", len(e.Errors), strings.Join(e.Errors, "\n  "))
}

// Add adds an error to the collection
func (e *ValidationError) Add(format string, args ...interface{}) {
	e.Errors = append(e.Errors, fmt.Sprintf(format, args...))
}

// HasErrors returns true if there are any errors
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// Validate validates the configuration and layers
func Validate(cfg *Config, layers map[string]*Layer, dir string) error {
	errs := &ValidationError{}

	// Load default format configs for global validation
	var defaultDistroCfg *DistroConfig
	var defaultBuilderCfg *BuilderConfig
	if cfg.Defaults.FormatConfig != nil {
		dc, blc, err := LoadDefaultFormatConfigs(cfg.Defaults.FormatConfig, dir)
		if err != nil {
			errs.Add("loading default format configs: %v", err)
		} else {
			defaultDistroCfg = dc
			defaultBuilderCfg = blc
		}
	}

	// Validate build and distro values
	if defaultDistroCfg != nil {
		validateBuildAndDistro(cfg, defaultDistroCfg, errs)
	}

	// Validate layers referenced in images
	validateLayerReferences(cfg, layers, errs)

	// Validate layer contents
	validateLayerContents(layers, errs)

	// Validate env files
	validateEnvFiles(layers, errs)

	// Validate package config (rpm/deb/pac/aur sections in layer.yml)
	validatePkgConfig(layers, errs)

	// Validate image base references
	validateBaseReferences(cfg, errs)

	// Validate no circular dependencies in images
	validateImageDAG(cfg, layers, dir, errs)

	// Validate ports
	validatePorts(cfg, layers, errs)

	// Validate routes
	validateRoutes(cfg, layers, errs)

	// Validate volumes
	validateVolumes(layers, errs)

	// Validate merge config
	validateMergeConfig(cfg, errs)

	// Validate aliases
	validateAliases(cfg, layers, errs)

	// Validate builders and builds
	if defaultBuilderCfg != nil {
		validateBuilders(cfg, layers, defaultBuilderCfg, errs)
	}

	// Validate DNS and ACME email
	validateDNS(cfg, errs)

	// Validate tunnel configuration
	validateTunnel(cfg, layers, errs)

	// Validate layer composition (layers: field)
	validateLayerIncludes(layers, errs)

	// Validate no circular dependencies in layers
	validateLayerDAG(cfg, layers, errs)

	// Validate remote layer consistency
	validateRemoteLayers(cfg, layers, errs)

	// Validate systemd service files
	validateSystemdServices(cfg, layers, errs)

	// Validate system_services entries
	validateSystemServices(cfg, layers, errs)

	// Validate libvirt snippets
	validateLibvirt(cfg, layers, errs)

	// Validate engine declarations
	validateEngineConfig(cfg, layers, errs)

	// Validate port_relay declarations
	validatePortRelay(cfg, layers, errs)

	// Warn about cross-image port overlaps
	validatePortOverlap(cfg, errs)

	// Validate status fields
	validateStatus(cfg, layers, errs)

	// Validate version fields
	validateVersionFields(cfg, layers, errs)

	// Validate service_env declarations
	validateServiceEnv(layers, errs)

	// Validate data layers and data images
	validateDataLayers(cfg, layers, errs)

	// Validate init system dependencies (driven by init.yml)
	var defaultInitCfg *InitConfig
	if cfg.Defaults.FormatConfig != nil {
		ic, icErr := LoadInitConfigForImage(nil, cfg.Defaults.FormatConfig, dir)
		if icErr != nil {
			errs.Add("loading default init config: %v", icErr)
		} else {
			defaultInitCfg = ic
		}
	}
	if defaultInitCfg != nil {
		validateInitDependencies(cfg, defaultInitCfg, layers, errs)
	}

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// validateInitDependencies checks that images using an init system have the
// required dependency layer in their resolved dependency chain.
// For example, images with supervisord services must include the "supervisord" layer.
func validateInitDependencies(cfg *Config, initCfg *InitConfig, layers map[string]*Layer, errs *ValidationError) {
	if initCfg == nil {
		return
	}

	for imgName, img := range cfg.Images {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}

		// Resolve all layers for this image (own + transitive deps)
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue // other validators handle resolution errors
		}

		// For each init system with a depends_layer, check if it's needed and present
		for initName, def := range initCfg.Inits {
			if def.DependsLayer == "" {
				continue // no dependency requirement (e.g., systemd is provided by base OS)
			}

			// Check if any layer requires this init system
			var needsInit []string
			for _, layerName := range resolved {
				layer, ok := layers[layerName]
				if !ok {
					continue
				}
				if layer.HasInit(initName) {
					needsInit = append(needsInit, layerName+" ("+initName+")")
				}
				// port_relay triggers init systems with relay_template
				if def.HasRelayTemplate() && len(layer.PortRelayPorts) > 0 {
					needsInit = append(needsInit, layerName+" (port_relay)")
				}
			}

			if len(needsInit) == 0 {
				continue
			}

			// Check if the depends_layer is in the resolved layers
			hasDepLayer := false
			for _, layerName := range resolved {
				if layerName == def.DependsLayer {
					hasDepLayer = true
					break
				}
			}

			// Also check base chain — dependency may be provided by a parent image
			if !hasDepLayer {
				images, resolveErr := cfg.ResolveAllImages("unused", ".")
				if resolveErr == nil {
					allLayers := collectAllImageLayers(imgName, images, layers)
					for _, l := range allLayers {
						if l == def.DependsLayer {
							hasDepLayer = true
							break
						}
					}
				}
			}

			if !hasDepLayer {
				errs.Add("image %q has layers requiring %s (%s) but missing the %q layer in its dependency chain; add %q to the image's layers or a base image",
					imgName, initName, strings.Join(needsInit, ", "), def.DependsLayer, def.DependsLayer)
			}
		}
	}
}

// validateBuildAndDistro validates build: and distro: entries.
// build: entries are checked against distro.yml format definitions.
// distro: is free-form (any string, including distro:version).
func validateBuildAndDistro(cfg *Config, distroCfg *DistroConfig, errs *ValidationError) {
	validateBuild := func(context string, build BuildFormats) {
		for _, b := range build {
			if !distroCfg.ValidFormat(b) {
				errs.Add("%s: build entry %q is not valid (known formats: %s)", context, b, strings.Join(distroCfg.AllFormatNames(), ", "))
			}
		}
		// Check for duplicates
		seen := make(map[string]bool)
		for _, b := range build {
			if seen[b] {
				errs.Add("%s: duplicate build entry %q", context, b)
			}
			seen[b] = true
		}
	}

	// Validate defaults
	validateBuild("defaults", cfg.Defaults.Build)

	// Validate per-image
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		validateBuild(fmt.Sprintf("image %q", name), img.Build)
	}
}


// validateLayerReferences ensures all layers referenced in images exist
func validateLayerReferences(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for _, layerRef := range img.Layers {
			layerName := BareRef(layerRef)
			if _, ok := layers[layerName]; !ok {
				if IsRemoteLayerRef(layerRef) {
					parsed := ParseRemoteRef(layerRef)
					errs.Add("image %q: remote layer %q not found (layer %q doesn't exist in %s)", imageName, layerRef, parsed.Name, parsed.RepoPath)
				} else {
					suggestion := findSimilarName(layerName, LayerNames(layers))
					if suggestion != "" {
						errs.Add("image %q: layer %q not found (did you mean %q?)", imageName, layerName, suggestion)
					} else {
						errs.Add("image %q: layer %q not found", imageName, layerName)
					}
				}
			}
		}
	}
}


// validateLayerContents validates each layer has required files
func validateLayerContents(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		// Layer must have at least one install file, a layers: field (composition), or data declarations
		if !layer.HasInstallFiles() && len(layer.IncludedLayers) == 0 && !layer.HasData {
			errs.Add("layer %q: must have at least one install file (layer.yml rpm/deb packages, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml) or a layers: field", name)
		}

		// Cargo.toml requires src/ directory
		if layer.HasCargoToml && !layer.HasSrcDir {
			errs.Add("layer %q: Cargo.toml requires src/ directory", name)
		}

		// Validate depends references
		for _, dep := range layer.Depends {
			resolved := dep
			// Within a remote repo, short-name depends resolve to siblings in the same repo
			if layer.Remote && !IsRemoteLayerRef(dep) {
				resolved = layer.RepoPath + "/" + layer.SubPathPrefix + dep
			}
			if _, ok := layers[resolved]; !ok {
				// Try original name too (for cross-repo deps using full paths)
				if _, ok := layers[dep]; !ok {
					suggestion := findSimilarName(dep, LayerNames(layers))
					if suggestion != "" {
						errs.Add("layer %q depends: unknown layer %q (did you mean %q?)", name, dep, suggestion)
					} else {
						errs.Add("layer %q depends: unknown layer %q", name, dep)
					}
				}
			}
		}

		// Validate extract field
		for _, ext := range layer.Extract() {
			if ext.Source == "" {
				errs.Add("layer %q: extract source cannot be empty", name)
			}
			if ext.Path == "" {
				errs.Add("layer %q: extract path cannot be empty", name)
			}
			if ext.Dest == "" {
				errs.Add("layer %q: extract dest cannot be empty", name)
			}
			if ext.Path != "" && !strings.HasPrefix(ext.Path, "/") {
				errs.Add("layer %q: extract path must be absolute (got %q)", name, ext.Path)
			}
			if ext.Dest != "" && !strings.HasPrefix(ext.Dest, "/") {
				errs.Add("layer %q: extract dest must be absolute (got %q)", name, ext.Dest)
			}
		}
	}
}

// validateLayerIncludes validates layer composition (layers: field in layer.yml)
func validateLayerIncludes(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.IncludedLayers) == 0 {
			continue
		}

		depSet := make(map[string]bool)
		for _, d := range layer.Depends {
			depSet[d] = true
		}

		for _, ref := range layer.IncludedLayers {
			// Self-inclusion
			if ref == name {
				errs.Add("layer %q layers: cannot include itself", name)
				continue
			}

			// Check ref exists
			resolved := ref
			if layer.Remote && !IsRemoteLayerRef(ref) {
				resolved = layer.RepoPath + "/" + layer.SubPathPrefix + ref
			}
			if _, ok := layers[resolved]; !ok {
				if _, ok := layers[ref]; !ok {
					suggestion := findSimilarName(ref, LayerNames(layers))
					if suggestion != "" {
						errs.Add("layer %q layers: unknown layer %q (did you mean %q?)", name, ref, suggestion)
					} else {
						errs.Add("layer %q layers: unknown layer %q", name, ref)
					}
				}
			}

			// Overlap with depends
			if depSet[ref] {
				errs.Add("layer %q: %q appears in both 'layers' and 'depends'", name, ref)
			}
		}
	}

	// Check for circular composition
	for name, layer := range layers {
		if len(layer.IncludedLayers) == 0 {
			continue
		}
		if err := checkIncludeCycle(name, layers, nil); err != nil {
			errs.Add("layer %q: %v", name, err)
		}
	}
}

// checkIncludeCycle detects circular layer composition
func checkIncludeCycle(name string, layers map[string]*Layer, visited map[string]bool) error {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[name] {
		return fmt.Errorf("circular layer composition involving %q", name)
	}
	layer, ok := layers[name]
	if !ok || len(layer.IncludedLayers) == 0 {
		return nil
	}
	visited[name] = true
	for _, ref := range layer.IncludedLayers {
		if err := checkIncludeCycle(ref, layers, visited); err != nil {
			return err
		}
	}
	delete(visited, name)
	return nil
}

// validateEnvFiles validates env config from layer.yml
func validateEnvFiles(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasEnv {
			continue
		}

		cfg, _ := layer.EnvConfig()
		if cfg == nil {
			continue
		}

		// PATH must not be set directly (use path_append in layer.yml)
		if _, hasPath := cfg.Vars["PATH"]; hasPath {
			errs.Add("layer %q layer.yml: use path_append instead of setting PATH in env", name)
		}
	}
}

// validatePkgConfig validates format-specific config in layer.yml.
// Uses generic FormatSection access — no format-specific code.
func validatePkgConfig(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		for formatName, section := range layer.formatSections {
			if section.Raw == nil {
				continue
			}
			// Validate repos entries have required fields
			if repos := toMapSlice(section.Raw["repos"]); len(repos) > 0 {
				if len(section.Packages) == 0 {
					errs.Add("layer %q layer.yml: %s.repos requires %s.packages", name, formatName, formatName)
				}
				for _, repo := range repos {
					repoName := fmt.Sprint(repo["name"])
					if repoName == "" || repoName == "<nil>" {
						errs.Add("layer %q layer.yml: %s.repos entry requires name", name, formatName)
					}
				}
			}
			// Validate copr/modules require packages
			if copr := toStringSlice(section.Raw["copr"]); len(copr) > 0 && len(section.Packages) == 0 {
				errs.Add("layer %q layer.yml: %s.copr requires %s.packages", name, formatName, formatName)
			}
			if modules := toStringSlice(section.Raw["modules"]); len(modules) > 0 && len(section.Packages) == 0 {
				errs.Add("layer %q layer.yml: %s.modules requires %s.packages", name, formatName, formatName)
			}
		}
	}
}

// validateBaseReferences ensures base references resolve
func validateBaseReferences(cfg *Config, errs *ValidationError) {
	// Base references can be:
	// 1. External OCI images (always valid)
	// 2. Names of other images in images.yml (validated by image DAG check)
	// No additional validation needed here
}

// validateImageDAG checks for circular image dependencies
func validateImageDAG(cfg *Config, layers map[string]*Layer, dir string, errs *ValidationError) {
	calverTag := "test"
	// Try to resolve images — some fields may be missing during basic validation
	images := make(map[string]*ResolvedImage)
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		ri, err := cfg.ResolveImage(name, calverTag, dir)
		if err != nil {
			// Skip images that can't resolve (e.g., missing build: field)
			continue
		}
		images[name] = ri
	}
	if len(images) == 0 {
		return
	}

	_, orderErr := ResolveImageOrder(images, layers)
	if orderErr != nil {
		if cycleErr, ok := orderErr.(*CycleError); ok {
			errs.Add("image dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			errs.Add("image DAG error: %v", orderErr)
		}
	}
}

// validateLayerDAG checks for circular layer dependencies
func validateLayerDAG(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Check each image's layers for cycles
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		// Convert raw refs to bare refs for layer map lookup
		bareLayers := make([]string, len(img.Layers))
		for i, ref := range img.Layers {
			bareLayers[i] = BareRef(ref)
		}
		_, err := ResolveLayerOrder(bareLayers, layers, nil)
		if err != nil {
			if cycleErr, ok := err.(*CycleError); ok {
				errs.Add("image %q: layer dependency cycle: %s", imageName, strings.Join(cycleErr.Cycle, " -> "))
			} else {
				errs.Add("image %q: layer resolution error: %v", imageName, err)
			}
		}
	}
}

// validatePorts validates port declarations in layers and images
func validatePorts(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate layer ports from layer.yml
	for name, layer := range layers {
		if !layer.HasPorts {
			continue
		}
		ports, _ := layer.Ports()
		for _, port := range ports {
			if !isValidPort(port) {
				errs.Add("layer %q layer.yml ports: %q is not a valid port number (1-65535)", name, port)
			}
		}
	}

	// Validate image port mappings
	validatePortMappings := func(name string, ports []string) {
		for _, mapping := range ports {
			parts := strings.Split(mapping, ":")
			switch len(parts) {
			case 1:
				if !isValidPort(parts[0]) {
					errs.Add("image %q ports: %q is not a valid port number (1-65535)", name, parts[0])
				}
			case 2:
				if !isValidPort(parts[0]) {
					errs.Add("image %q ports: host port %q in %q is not valid (1-65535)", name, parts[0], mapping)
				}
				if !isValidPort(parts[1]) {
					errs.Add("image %q ports: container port %q in %q is not valid (1-65535)", name, parts[1], mapping)
				}
			default:
				errs.Add("image %q ports: %q must be \"port\" or \"host:container\" format", name, mapping)
			}
		}
	}

	if len(cfg.Defaults.Ports) > 0 {
		validatePortMappings("defaults", cfg.Defaults.Ports)
	}
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if len(img.Ports) > 0 {
			validatePortMappings(name, img.Ports)
		}
	}
}

// validateRoutes validates route file declarations in layers
func validateRoutes(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate route config from layer.yml
	for name, layer := range layers {
		if !layer.HasRoute {
			continue
		}
		route, _ := layer.Route()
		if route == nil {
			continue
		}
		if route.Host == "" {
			errs.Add("layer %q layer.yml route: missing required \"host\" field", name)
		}
		if route.Port == "" {
			errs.Add("layer %q layer.yml route: missing required \"port\" field", name)
		} else if !isValidPort(route.Port) {
			errs.Add("layer %q layer.yml route: %q is not a valid port number (1-65535)", name, route.Port)
		}
	}

	// Route is generic service metadata consumed by traefik, tunnel, or both.
	// No validation requiring traefik — images may use tunnels instead.
}

// validateMergeConfig validates merge configuration
func validateMergeConfig(cfg *Config, errs *ValidationError) {
	check := func(name string, m *MergeConfig) {
		if m == nil {
			return
		}
		if m.MaxMB < 0 {
			errs.Add("%s: merge max_mb must be > 0, got %d", name, m.MaxMB)
		}
	}

	check("defaults", cfg.Defaults.Merge)
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		check(fmt.Sprintf("image %q", name), img.Merge)
	}
}

// volumeNameRe matches valid volume names: lowercase alphanumeric + hyphens
var volumeNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validateVolumes validates volume declarations in layers
func validateVolumes(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasVolumes {
			continue
		}
		seen := make(map[string]bool)
		for _, vol := range layer.Volumes() {
			if vol.Name == "" {
				errs.Add("layer %q layer.yml volumes: missing required \"name\" field", name)
			} else if !volumeNameRe.MatchString(vol.Name) {
				errs.Add("layer %q layer.yml volumes: name %q must be lowercase alphanumeric with hyphens", name, vol.Name)
			} else if seen[vol.Name] {
				errs.Add("layer %q layer.yml volumes: duplicate volume name %q", name, vol.Name)
			} else {
				seen[vol.Name] = true
			}
			if vol.Path == "" {
				errs.Add("layer %q layer.yml volumes: missing required \"path\" field", name)
			}
		}
	}
}

// validateAliases validates alias declarations in layers and images
func validateAliases(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate layer aliases
	for name, layer := range layers {
		if !layer.HasAliases {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range layer.Aliases() {
			if a.Name == "" {
				errs.Add("layer %q layer.yml aliases: missing required \"name\" field", name)
			} else if !aliasNameRe.MatchString(a.Name) {
				errs.Add("layer %q layer.yml aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", name, a.Name)
			} else if seen[a.Name] {
				errs.Add("layer %q layer.yml aliases: duplicate alias name %q", name, a.Name)
			} else {
				seen[a.Name] = true
			}
			if a.Command == "" {
				errs.Add("layer %q layer.yml aliases: missing required \"command\" field for alias %q", name, a.Name)
			}
		}
	}

	// Validate image-level aliases
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if len(img.Aliases) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range img.Aliases {
			if a.Name == "" {
				errs.Add("image %q aliases: missing required \"name\" field", imageName)
			} else if !aliasNameRe.MatchString(a.Name) {
				errs.Add("image %q aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", imageName, a.Name)
			} else if seen[a.Name] {
				errs.Add("image %q aliases: duplicate alias name %q", imageName, a.Name)
			} else {
				seen[a.Name] = true
			}
		}
	}
}

// validateBuilders validates builders and builds configuration
func validateBuilders(cfg *Config, layers map[string]*Layer, builderCfg *BuilderConfig, errs *ValidationError) {
	// Validate defaults.builders entries
	for typ, builder := range cfg.Defaults.Builders {
		if !builderCfg.ValidBuilderType(typ) {
			errs.Add("defaults.builders: build type %q is not valid (known builders: %s)", typ, strings.Join(builderCfg.BuilderNames(), ", "))
		}
		if builder != "" {
			builderImg, exists := cfg.Images[builder]
			if !exists {
				errs.Add("defaults.builders.%s: image %q not found in images.yml", typ, builder)
			} else if !builderImg.IsEnabled() {
				errs.Add("defaults.builders.%s: image %q is disabled", typ, builder)
			}
		}
	}

	// Validate each enabled image
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}

		// Validate builds: entries (capability declarations on builder images)
		for _, b := range img.Builds {
			if !builderCfg.ValidBuilderType(b) {
				errs.Add("image %q: builds entry %q is not valid (known builders: %s)", imageName, b, strings.Join(builderCfg.BuilderNames(), ", "))
			}
		}

		// Validate builders: entries (per-type builder assignments)
		for typ, builder := range img.Builders {
			if !builderCfg.ValidBuilderType(typ) {
				errs.Add("image %q: builders.%s is not a valid build type (known builders: %s)", imageName, typ, strings.Join(builderCfg.BuilderNames(), ", "))
			}
			if builder == imageName {
				errs.Add("image %q: builders.%s cannot reference self", imageName, typ)
				continue
			}
			if builder != "" {
				builderImg, exists := cfg.Images[builder]
				if !exists {
					errs.Add("image %q: builders.%s references %q which is not found in images.yml", imageName, typ, builder)
					continue
				}
				if !builderImg.IsEnabled() {
					errs.Add("image %q: builders.%s references %q which is disabled", imageName, typ, builder)
					continue
				}
				// Check builder declares this capability
				hasCapability := false
				for _, b := range builderImg.Builds {
					if b == typ {
						hasCapability = true
						break
					}
				}
				if len(builderImg.Builds) > 0 && !hasCapability {
					errs.Add("image %q: builders.%s references %q which does not declare builds: [%s]", imageName, typ, builder, typ)
				}
			}
		}

		// Resolve effective builders (image -> base -> defaults) to check needs
		resolved := make(BuildersMap)
		for typ, builder := range cfg.Defaults.Builders {
			resolved[typ] = builder
		}
		if baseImg, ok := cfg.Images[img.Base]; ok && baseImg.IsEnabled() {
			for typ, builder := range baseImg.Builders {
				resolved[typ] = builder
			}
		}
		for typ, builder := range img.Builders {
			resolved[typ] = builder
		}
		// Filter self-references
		for typ, builder := range resolved {
			if builder == imageName {
				delete(resolved, typ)
			}
		}

		// Check if layers need builders that aren't configured.
		// Detection is fully config-driven from builder.yml:
		//   detect_files: layer has any of these files
		//   detect_config: layer has this format section with packages
		layerOrder, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue
		}
		for _, layerName := range layerOrder {
			layer, ok := layers[layerName]
			if !ok {
				continue
			}
			for builderName, builderDef := range builderCfg.Builders {
				needed := false
				for _, f := range builderDef.DetectFiles {
					if layerHasFile(layer, f) {
						needed = true
						break
					}
				}
				if builderDef.DetectConfig != "" && layerHasFormatConfig(layer, builderDef.DetectConfig) {
					needed = true
				}
				if needed && !resolved.HasBuilder(builderName) {
					errs.Add("image %q: layer %q needs builder %q but no builders.%s configured", imageName, layerName, builderName, builderName)
				}
			}
		}
	}
}

// validateDNS validates DNS and ACME email fields
func validateDNS(cfg *Config, errs *ValidationError) {
	// Validate defaults.dns if set
	if cfg.Defaults.DNS != "" {
		if !strings.Contains(cfg.Defaults.DNS, ".") {
			errs.Add("defaults.dns: must be a valid domain name (got %q)", cfg.Defaults.DNS)
		}
		if strings.HasPrefix(cfg.Defaults.DNS, ".") || strings.HasSuffix(cfg.Defaults.DNS, ".") {
			errs.Add("defaults.dns: cannot start or end with a dot (got %q)", cfg.Defaults.DNS)
		}
	}

	// Validate defaults.acme_email if set
	if cfg.Defaults.AcmeEmail != "" && !strings.Contains(cfg.Defaults.AcmeEmail, "@") {
		errs.Add("defaults.acme_email: must be a valid email address (got %q)", cfg.Defaults.AcmeEmail)
	}

	// Validate each enabled image's DNS and ACME email
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}

		if img.DNS != "" {
			if !strings.Contains(img.DNS, ".") {
				errs.Add("image %q: dns must be a valid domain name (got %q)", imageName, img.DNS)
			}
			if strings.HasPrefix(img.DNS, ".") || strings.HasSuffix(img.DNS, ".") {
				errs.Add("image %q: dns cannot start or end with a dot (got %q)", imageName, img.DNS)
			}
		}

		if img.AcmeEmail != "" && !strings.Contains(img.AcmeEmail, "@") {
			errs.Add("image %q: acme_email must be a valid email address (got %q)", imageName, img.AcmeEmail)
		}
	}
}

// tunnelNameRe matches valid cloudflare tunnel names
var tunnelNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// validateTunnel validates tunnel configuration in defaults and images
func validateTunnel(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	check := func(name string, t *TunnelYAML, dns string, imagePorts []string, portProtos map[int]string) {
		if t == nil {
			return
		}
		if t.Provider != "tailscale" && t.Provider != "cloudflare" {
			errs.Add("%s: tunnel provider must be \"tailscale\" or \"cloudflare\", got %q", name, t.Provider)
			return
		}

		// Must specify at least public or private
		if t.Public.IsZero() && t.Private.IsZero() {
			errs.Add("%s: tunnel must specify public, private, or both", name)
			return
		}

		// public: all + private: all = conflict
		if t.Public.All && t.Private.All {
			errs.Add("%s: tunnel cannot have both public: all and private: all", name)
		}

		// Same port in both public and private = error
		pubPorts := make(map[int]bool)
		for _, p := range t.Public.Ports {
			pubPorts[p] = true
		}
		for p := range t.Public.PortMap {
			pubPorts[p] = true
		}
		for _, p := range t.Private.Ports {
			if pubPorts[p] {
				errs.Add("%s: port %d appears in both public and private", name, p)
			}
		}
		for p := range t.Private.PortMap {
			if pubPorts[p] {
				errs.Add("%s: port %d appears in both public and private", name, p)
			}
		}

		// Cloudflare + private: in any form = error
		if t.Provider == "cloudflare" && !t.Private.IsZero() {
			errs.Add("%s: cloudflare tunnels are always public; use tailscale for private ports", name)
		}

		// Tailscale + public: map form = error
		if t.Provider == "tailscale" && len(t.Public.PortMap) > 0 {
			errs.Add("%s: tailscale doesn't use hostnames; use public: [port_list]", name)
		}

		// private: map form in any provider = error
		if len(t.Private.PortMap) > 0 {
			errs.Add("%s: private ports don't use hostnames", name)
		}

		// Build set of image host ports for existence checks
		hostPortSet := make(map[int]bool)
		for _, hp := range parseHostPorts(imagePorts) {
			hostPortSet[hp] = true
		}

		// Tailscale public port list validation
		if t.Provider == "tailscale" {
			hostToContainer := buildPortMapping(imagePorts)

			for _, p := range t.Public.Ports {
				if !ValidPublicPorts[p] {
					errs.Add("%s: tailscale public port %d must be 443, 8443, or 10000", name, p)
				}
				// TCP ports can't be public
				if portProtos != nil {
					cp := p
					if c, ok := hostToContainer[p]; ok {
						cp = c
					}
					if portProtos[cp] == "tcp" {
						errs.Add("%s: TCP port %d cannot be public (only HTTP ports can be internet-accessible)", name, p)
					}
				}
			}

			// Tailscale public: all — validate each non-TCP image port is a valid public port
			if t.Public.All {
				for _, hp := range parseHostPorts(imagePorts) {
					cp := hp
					if c, ok := hostToContainer[hp]; ok {
						cp = c
					}
					proto := "http"
					if portProtos != nil {
						if pp, ok := portProtos[cp]; ok {
							proto = pp
						}
					}
					if proto == "tcp" {
						continue // TCP ports are skipped in public: all
					}
					if !ValidPublicPorts[hp] {
						errs.Add("%s: tailscale public: all includes port %d which is not a valid public port (443, 8443, 10000)", name, hp)
					}
				}
			}

			// Tailscale private port list: each must satisfy isValidServePort
			for _, p := range t.Private.Ports {
				if !isValidServePort(p) {
					errs.Add("%s: tailscale private port %d must be 80, 443, 3000-10000, 4443, 5432, 6443, or 8443", name, p)
				}
			}
		}

		// All public/private ports must exist in image ports
		if len(hostPortSet) > 0 {
			for _, p := range t.Public.Ports {
				if !hostPortSet[p] {
					errs.Add("%s: public port %d not found in image ports", name, p)
				}
			}
			for p := range t.Public.PortMap {
				if !hostPortSet[p] {
					errs.Add("%s: public port %d not found in image ports", name, p)
				}
			}
			for _, p := range t.Private.Ports {
				if !hostPortSet[p] {
					errs.Add("%s: private port %d not found in image ports", name, p)
				}
			}
		}

		// Cloudflare-specific validation
		if t.Provider == "cloudflare" {
			publicCount := len(t.Public.Ports) + len(t.Public.PortMap)
			if t.Public.All {
				publicCount = len(imagePorts)
			}
			if publicCount > 1 && len(t.Public.PortMap) == 0 {
				errs.Add("%s: multiple cloudflare ports need per-port hostnames; use map form", name)
			}
			// Cloudflare without map form and without dns = error
			if len(t.Public.PortMap) == 0 && dns == "" {
				errs.Add("%s: cloudflare requires dns or per-port hostnames", name)
			}
			// Cloudflare tunnel name validation
			if t.Tunnel != "" && !tunnelNameRe.MatchString(t.Tunnel) {
				errs.Add("%s: tunnel name must match [a-zA-Z0-9][a-zA-Z0-9-]*, got %q", name, t.Tunnel)
			}
		}
	}

	check("defaults", cfg.Defaults.Tunnel, cfg.Defaults.DNS, cfg.Defaults.Ports, nil)
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		// Resolve DNS for the image (image -> defaults)
		dns := img.DNS
		if dns == "" {
			dns = cfg.Defaults.DNS
		}
		var portProtos map[int]string
		if layers != nil {
			portProtos = collectPortProtos(layers, img.Layers)
		}
		check(fmt.Sprintf("image %q", imageName), img.Tunnel, dns, img.Ports, portProtos)
	}

	// Cross-image tailscale public port conflict check
	portUsers := make(map[int][]string) // public port -> image names
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		// Resolve effective tunnel: image -> defaults
		tunnel := img.Tunnel
		if tunnel == nil {
			tunnel = cfg.Defaults.Tunnel
		}
		if tunnel == nil || tunnel.Provider != "tailscale" {
			continue
		}
		// Collect all tailscale public ports (from Ports list and PortMap keys)
		if tunnel.Public.All {
			for _, hp := range parseHostPorts(img.Ports) {
				portUsers[hp] = append(portUsers[hp], imageName)
			}
		}
		for _, p := range tunnel.Public.Ports {
			portUsers[p] = append(portUsers[p], imageName)
		}
		for p := range tunnel.Public.PortMap {
			portUsers[p] = append(portUsers[p], imageName)
		}
	}
	for port, names := range portUsers {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		errs.Add("images %s: tailscale public port %d used by multiple images (each needs a unique port)", formatImageList(names), port)
	}
}

// validateRemoteLayers checks remote layer consistency
func validateRemoteLayers(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Check version conflicts (same repo referenced with different versions)
	_, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		errs.Add("%v", err)
	}

	// Check for naming conflicts between remote layers from different repos
	for _, layer := range layers {
		if !layer.Remote {
			continue
		}
		for _, other := range layers {
			if !other.Remote || other == layer {
				continue
			}
			if other.Name == layer.Name && other.RepoPath != layer.RepoPath {
				errs.Add("remote layer name conflict: %q provided by both %s and %s", layer.Name, layer.RepoPath, other.RepoPath)
			}
		}
	}
}

// validateSystemdServices validates systemd .service files in layers
func validateSystemdServices(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.ServiceFiles()) == 0 {
			continue
		}
		for _, svcPath := range layer.ServiceFiles() {
			info, err := os.Stat(svcPath)
			if err != nil {
				errs.Add("layer %q: systemd service file %q not readable: %v", name, filepath.Base(svcPath), err)
				continue
			}
			if info.Size() == 0 {
				errs.Add("layer %q: systemd service file %q is empty", name, filepath.Base(svcPath))
			}
		}
	}
}

// validateSystemServices validates system_services entries in layers
func validateSystemServices(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.SystemServiceUnits()) == 0 {
			continue
		}
		for _, unit := range layer.SystemServiceUnits() {
			if unit == "" {
				errs.Add("layer %q: system_services entry cannot be empty", name)
			}
			if strings.Contains(unit, "/") || strings.Contains(unit, " ") {
				errs.Add("layer %q: system_services entry %q must be a unit name (no paths or spaces)", name, unit)
			}
		}
		if !layer.HasFormatPackages() {
			errs.Add("layer %q: system_services requires system packages that provide those units", name)
		}
	}

	// Warn if system_services are used in non-bootc images
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if img.Bootc {
			continue
		}
		for _, layerRef := range img.Layers {
			bare := BareRef(layerRef)
			layer, ok := layers[bare]
			if !ok || len(layer.SystemServiceUnits()) == 0 {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: image %q includes layer %q with system_services, but is not a bootc image (systemd units will be ignored)\n", imageName, bare)
		}
	}
}

// isValidPort checks if a string is a valid port number (1-65535).
// Handles /udp and /tcp suffixes: "47998/udp" is valid.
func isValidPort(s string) bool {
	clean, _ := stripPortSuffix(s)
	n, err := strconv.Atoi(clean)
	if err != nil {
		return false
	}
	return n >= 1 && n <= 65535
}

// findSimilarName finds a similar name for typo suggestions
func findSimilarName(target string, candidates []string) string {
	// Simple Levenshtein-like check for close matches
	for _, candidate := range candidates {
		if levenshteinDistance(target, candidate) <= 2 {
			return candidate
		}
	}
	return ""
}

// levenshteinDistance calculates the edit distance between two strings
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

// validateLibvirt validates libvirt XML snippets in layers and images
func validateLibvirt(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate layer-level snippets
	for name, layer := range layers {
		if !layer.HasLibvirt {
			continue
		}
		for i, snippet := range layer.Libvirt() {
			if err := ValidateLibvirtSnippet(snippet); err != nil {
				errs.Add("layer %q libvirt[%d]: %v", name, i, err)
			}
		}
	}

	// Validate image-level snippets
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for i, snippet := range img.Libvirt {
			if err := ValidateLibvirtSnippet(snippet); err != nil {
				errs.Add("image %q libvirt[%d]: %v", imageName, i, err)
			}
		}

		// Warn if libvirt snippets used in non-bootc images
		if !img.Bootc {
			hasLibvirt := len(img.Libvirt) > 0
			if !hasLibvirt {
				for _, layerRef := range img.Layers {
					bare := BareRef(layerRef)
					layer, ok := layers[bare]
					if ok && layer.HasLibvirt {
						hasLibvirt = true
						break
					}
				}
			}
			if hasLibvirt {
				fmt.Fprintf(os.Stderr, "Warning: image %q has libvirt snippets but is not a bootc image (snippets will be ignored)\n", imageName)
			}
		}
	}
}

// validateEngineConfig validates engine declarations in layers and images
func validateEngineConfig(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	validEngines := map[string]bool{"docker": true, "podman": true}

	// Validate layer engine declarations
	for name, layer := range layers {
		if e := layer.Engine(); e != "" && !validEngines[e] {
			errs.Add("layer %q: engine must be \"docker\" or \"podman\", got %q", name, e)
		}
	}

	// Validate defaults engine
	if e := cfg.Defaults.Engine; e != "" && !validEngines[e] {
		errs.Add("defaults: engine must be \"docker\" or \"podman\", got %q", e)
	}

	// Validate image engine declarations and check for conflicting layer requirements
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if e := img.Engine; e != "" && !validEngines[e] {
			errs.Add("image %q: engine must be \"docker\" or \"podman\", got %q", imageName, e)
		}

		// Check for conflicting layer engine requirements within the image
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue
		}

		engineSources := make(map[string]string) // engine value -> first layer declaring it
		for _, layerName := range resolved {
			layer, ok := layers[layerName]
			if !ok {
				continue
			}
			if e := layer.Engine(); e != "" {
				if _, exists := engineSources[e]; !exists {
					engineSources[e] = layerName
				}
			}
		}
		if len(engineSources) > 1 {
			var conflicts []string
			for e, l := range engineSources {
				conflicts = append(conflicts, fmt.Sprintf("%s (from layer %s)", e, l))
			}
			sort.Strings(conflicts)
			errs.Add("image %q: conflicting engine requirements: %s", imageName, strings.Join(conflicts, ", "))
		}
	}
}

// validatePortRelay validates port_relay declarations in layers
func validatePortRelay(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.PortRelayPorts) == 0 {
			continue
		}
		// Validate each port
		portSet := make(map[int]bool)
		for _, port := range layer.PortRelayPorts {
			if port < 1 || port > 65535 {
				errs.Add("layer %q port_relay: %d is not a valid port number (1-65535)", name, port)
			}
			if portSet[port] {
				errs.Add("layer %q port_relay: duplicate port %d", name, port)
			}
			portSet[port] = true
		}

		// Warn if relay port isn't declared in the layer's ports
		if layer.HasPorts {
			layerPorts := make(map[int]bool)
			for _, ps := range layer.PortSpecs() {
				layerPorts[ps.Port] = true
			}
			for _, port := range layer.PortRelayPorts {
				if !layerPorts[port] {
					errs.Add("layer %q port_relay: port %d is not declared in the layer's ports", name, port)
				}
			}
		} else {
			errs.Add("layer %q port_relay: layer has no ports declared", name)
		}
	}

	// Validate that images with port_relay layers include the socat layer
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue
		}
		hasRelay := false
		hasSocat := false
		for _, layerName := range resolved {
			layer, ok := layers[layerName]
			if !ok {
				continue
			}
			if len(layer.PortRelayPorts) > 0 {
				hasRelay = true
			}
			if layerName == "socat" {
				hasSocat = true
			}
		}
		if hasRelay && !hasSocat {
			errs.Add("image %q: has port_relay layers but missing \"socat\" layer (add it to the image layers or as a dependency)", imageName)
		}
	}
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// validatePortOverlap warns when multiple enabled images share the same host port.
func validatePortOverlap(cfg *Config, errs *ValidationError) {
	portUsers := make(map[int][]string) // host port -> image names
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for _, portMapping := range img.Ports {
			hostPort, err := ParseHostPort(portMapping)
			if err != nil {
				continue
			}
			portUsers[hostPort] = append(portUsers[hostPort], imageName)
		}
	}
	for port, names := range portUsers {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		fmt.Fprintf(os.Stderr, "Note: images %s share host port %d (only one can run at a time, or use deploy.yml to remap)\n", formatImageList(names), port)
	}
}

// formatImageList formats a list of image names for display.
func formatImageList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
}

// validStatuses lists the allowed status values (empty string also accepted as "testing").
var validStatuses = map[string]bool{
	"":        true,
	"working": true,
	"testing": true,
	"broken":  true,
}

// calverRe matches CalVer format: YYYY.DDD.HHMM (3 dot-separated non-negative integers)
var calverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// validateStatus validates status fields in layers and images.
func validateStatus(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !validStatuses[layer.Status] {
			errs.Add("layer %q: status must be \"working\", \"testing\", or \"broken\", got %q", name, layer.Status)
		}
	}
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if !validStatuses[img.Status] {
			errs.Add("image %q: status must be \"working\", \"testing\", or \"broken\", got %q", name, img.Status)
		}
	}
}

// validateVersionFields validates version fields in layers and images.
func validateVersionFields(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if layer.Version != "" && !calverRe.MatchString(layer.Version) {
			errs.Add("layer %q: version must be CalVer format (YYYY.DDD.HHMM), got %q", name, layer.Version)
		}
	}
	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if img.Version != "" && !calverRe.MatchString(img.Version) {
			errs.Add("image %q: version must be CalVer format (YYYY.DDD.HHMM), got %q", name, img.Version)
		}
	}
}

// layerHasFile checks if a layer has a specific file (used for builder detection).
func layerHasFile(layer *Layer, filename string) bool {
	switch filename {
	case "pixi.toml":
		return layer.HasPixiToml
	case "pyproject.toml":
		return layer.HasPyprojectToml
	case "environment.yml":
		return layer.HasEnvironmentYml
	case "package.json":
		return layer.HasPackageJson
	case "Cargo.toml":
		return layer.HasCargoToml
	default:
		return fileExists(filepath.Join(layer.Path, filename))
	}
}

// layerHasFormatConfig checks if a layer has a non-empty config section for a format.
// Fully generic — uses the FormatSection accessor which checks both typed and dynamic sections.
func layerHasFormatConfig(layer *Layer, formatName string) bool {
	section := layer.FormatSection(formatName)
	return section != nil && len(section.Packages) > 0
}

// validateDataLayers checks data layer declarations and data image constraints.
func validateDataLayers(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate data src directories exist
	for name, layer := range layers {
		if !layer.HasData {
			continue
		}
		for _, d := range layer.Data() {
			srcPath := filepath.Join(layer.Path, d.Src)
			if !dirExists(srcPath) {
				errs.Add("layer %s: data src %q does not exist or is not a directory", name, d.Src)
			}
		}
	}

	// Validate per-image constraints
	for imgName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}

		// Resolve layers for this image
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue // layer resolution errors are caught elsewhere
		}

		// Collect all volume names declared in this image's layer chain
		volumeNames := make(map[string]bool)
		for _, layerName := range resolved {
			layer, ok := layers[layerName]
			if !ok {
				continue
			}
			for _, v := range layer.Volumes() {
				volumeNames[v.Name] = true
			}
		}

		// Check that data volume references resolve
		hasData := false
		for _, layerName := range resolved {
			layer, ok := layers[layerName]
			if !ok || !layer.HasData {
				continue
			}
			hasData = true
			for _, d := range layer.Data() {
				if !volumeNames[d.Volume] {
					errs.Add("image %s: layer %s data references volume %q which is not declared by any layer in the image", imgName, layerName, d.Volume)
				}
			}
		}

		// Data image specific validations
		if img.DataImage {
			if img.Base != "" {
				errs.Add("image %s: data_image cannot specify base (always FROM scratch)", imgName)
			}
			if !hasData {
				errs.Add("image %s: data_image has no layers with data declarations", imgName)
			}
			// Check for incompatible features
			for _, layerName := range resolved {
				layer, ok := layers[layerName]
				if !ok {
					continue
				}
				if layer.serviceConf != "" {
					errs.Add("image %s: data_image includes layer %s which has a service declaration", imgName, layerName)
				}
				if layer.HasPorts {
					errs.Add("image %s: data_image includes layer %s which has port declarations", imgName, layerName)
				}
				if len(layer.PortRelayPorts) > 0 {
					errs.Add("image %s: data_image includes layer %s which has port_relay declarations", imgName, layerName)
				}
				if len(layer.systemServices) > 0 {
					errs.Add("image %s: data_image includes layer %s which has system_services declarations", imgName, layerName)
				}
			}
		}
	}
}

// validateServiceEnv checks service_env declarations in layers.
func validateServiceEnv(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasServiceEnv {
			continue
		}
		for key, tmpl := range layer.ServiceEnv() {
			// Validate env var key format
			if key == "" {
				errs.Add("layer %s: service_env has empty key", name)
				continue
			}

			// Check for valid template variables (only {{.ContainerName}} is allowed)
			// Strip valid template vars, then check for remaining {{ }}
			stripped := strings.ReplaceAll(tmpl, "{{.ContainerName}}", "")
			if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
				errs.Add("layer %s: service_env[%s] contains unknown template variable (only {{.ContainerName}} is supported): %s", name, key, tmpl)
			}

			// Note: service_env key may intentionally overlap with env key in the same layer.
			// env is baked into the service's own image (e.g., OLLAMA_HOST="0.0.0.0" for binding).
			// service_env is injected into OTHER containers (e.g., OLLAMA_HOST="http://ov-ollama:11434").
		}
	}
}
