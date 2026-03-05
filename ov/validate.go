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
func Validate(cfg *Config, layers map[string]*Layer) error {
	errs := &ValidationError{}

	// Validate pkg values
	validatePkgValues(cfg, errs)

	// Validate layers referenced in images
	validateLayerReferences(cfg, layers, errs)

	// Validate layer contents
	validateLayerContents(layers, errs)

	// Validate env files
	validateEnvFiles(layers, errs)

	// Validate package config (rpm/deb sections in layer.yml)
	validatePkgConfig(layers, errs)

	// Validate image base references
	validateBaseReferences(cfg, errs)

	// Validate no circular dependencies in images
	validateImageDAG(cfg, layers, errs)

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

	// Validate builder
	validateBuilder(cfg, layers, errs)

	// Validate FQDN and ACME email
	validateFQDN(cfg, errs)

	// Validate tunnel configuration
	validateTunnel(cfg, errs)

	// Validate bind mounts
	validateBindMounts(cfg, layers, errs)

	// Validate no circular dependencies in layers
	validateLayerDAG(cfg, layers, errs)

	// Validate inline @version consistency (no version conflicts)
	validateInlineVersions(cfg, layers, errs)

	// Validate remote module consistency
	validateModules(cfg, layers, errs)

	// Validate systemd service files
	validateSystemdServices(cfg, layers, errs)

	// Validate system_services entries
	validateSystemServices(cfg, layers, errs)

	// Validate libvirt snippets
	validateLibvirt(cfg, layers, errs)

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// validatePkgValues ensures pkg is "rpm" or "deb"
func validatePkgValues(cfg *Config, errs *ValidationError) {
	if cfg.Defaults.Pkg != "" && cfg.Defaults.Pkg != "rpm" && cfg.Defaults.Pkg != "deb" {
		errs.Add("defaults: pkg must be \"rpm\" or \"deb\", got %q", cfg.Defaults.Pkg)
	}

	for name, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if img.Pkg != "" && img.Pkg != "rpm" && img.Pkg != "deb" {
			errs.Add("image %q: pkg must be \"rpm\" or \"deb\", got %q", name, img.Pkg)
		}
	}
}

// validateLayerReferences ensures all layers referenced in images exist
func validateLayerReferences(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for _, layerRef := range img.Layers {
			// Strip @version before lookup (layers are keyed by bare ref)
			layerName, _ := StripVersion(layerRef)
			if _, ok := layers[layerName]; !ok {
				if IsRemoteLayerRef(layerRef) {
					modPath, name := SplitRemoteLayerRef(layerRef)
					errs.Add("image %q: remote layer %q not found (module %s may need 'ov mod get' or layer %q doesn't exist in it)", imageName, layerRef, modPath, name)
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

// validateInlineVersions checks that the same module is not referenced with
// different versions across images.yml and layer.yml depends.
func validateInlineVersions(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	_, err := CollectRequiredModulesVersioned(cfg, layers)
	if err != nil {
		errs.Add("%v", err)
	}
}

// validateLayerContents validates each layer has required files
func validateLayerContents(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		// Layer must have at least one install file
		if !layer.HasInstallFiles() {
			errs.Add("layer %q: must have at least one install file (layer.yml rpm/deb packages, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml)", name)
		}

		// Cargo.toml requires src/ directory
		if layer.HasCargoToml && !layer.HasSrcDir {
			errs.Add("layer %q: Cargo.toml requires src/ directory", name)
		}

		// Validate depends references
		for _, dep := range layer.Depends {
			resolved := dep
			// Within a remote module, short-name depends resolve to siblings in the same module
			if layer.Remote && !IsRemoteLayerRef(dep) {
				resolved = layer.ModulePath + "/" + dep
			}
			if _, ok := layers[resolved]; !ok {
				// Try original name too (for cross-module deps using full paths)
				if _, ok := layers[dep]; !ok {
					if IsRemoteLayerRef(dep) {
						modPath, lname := SplitRemoteLayerRef(dep)
						errs.Add("layer %q depends: unknown remote layer %q (module %s, layer %q)", name, dep, modPath, lname)
					} else {
						suggestion := findSimilarName(dep, LayerNames(layers))
						if suggestion != "" {
							errs.Add("layer %q depends: unknown layer %q (did you mean %q?)", name, dep, suggestion)
						} else {
							errs.Add("layer %q depends: unknown layer %q", name, dep)
						}
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

// validatePkgConfig validates rpm/deb config in layer.yml
func validatePkgConfig(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		rpm := layer.RpmConfig()
		if rpm != nil {
			// copr without packages is an error
			if len(rpm.Copr) > 0 && len(rpm.Packages) == 0 {
				errs.Add("layer %q layer.yml: rpm.copr requires rpm.packages", name)
			}
			// repos without packages is an error
			if len(rpm.Repos) > 0 && len(rpm.Packages) == 0 {
				errs.Add("layer %q layer.yml: rpm.repos requires rpm.packages", name)
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
func validateImageDAG(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	calverTag := "test"
	images, err := cfg.ResolveAllImages(calverTag)
	if err != nil {
		errs.Add("resolving images: %v", err)
		return
	}

	_, err = ResolveImageOrder(images, layers)
	if err != nil {
		if cycleErr, ok := err.(*CycleError); ok {
			errs.Add("image dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			errs.Add("image DAG error: %v", err)
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
		_, err := ResolveLayerOrder(img.Layers, layers, nil)
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

// validateBuilder validates the builder configuration
func validateBuilder(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate defaults.builder if set
	if cfg.Defaults.Builder != "" {
		builderImg, exists := cfg.Images[cfg.Defaults.Builder]
		if !exists {
			errs.Add("defaults.builder: image %q not found in images.yml", cfg.Defaults.Builder)
		} else if !builderImg.IsEnabled() {
			errs.Add("defaults.builder: image %q is disabled", cfg.Defaults.Builder)
		}
	}

	// Check each enabled image's builder
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}

		// Resolve builder: image -> defaults -> ""
		resolvedBuilder := img.Builder
		if resolvedBuilder == "" {
			resolvedBuilder = cfg.Defaults.Builder
		}

		// Self-reference check (only for explicitly set builder, not inherited from defaults)
		if img.Builder == imageName {
			errs.Add("image %q: cannot be its own builder", imageName)
			continue
		}

		// Skip images where resolved builder is self (inherited from defaults — not an error)
		if resolvedBuilder == imageName {
			continue
		}

		// Validate per-image builder reference (if set on the image itself)
		if img.Builder != "" {
			builderImg, exists := cfg.Images[img.Builder]
			if !exists {
				errs.Add("image %q: builder %q not found in images.yml", imageName, img.Builder)
				continue
			}
			if !builderImg.IsEnabled() {
				errs.Add("image %q: builder %q is disabled", imageName, img.Builder)
				continue
			}
		}

		// Skip builder image itself
		if imageName == resolvedBuilder {
			continue
		}

		// Check if this image needs a builder
		needsBuilder := false
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err == nil {
			for _, layerName := range resolved {
				layer, ok := layers[layerName]
				if !ok {
					continue
				}
				if layer.PixiManifest() != "" || layer.HasPackageJson {
					needsBuilder = true
					break
				}
			}
		}

		if needsBuilder && resolvedBuilder == "" {
			errs.Add("image %q: has pixi/npm layers but no builder configured (set defaults.builder or image builder in images.yml)", imageName)
		}
	}
}

// validateFQDN validates FQDN and ACME email fields
func validateFQDN(cfg *Config, errs *ValidationError) {
	// Validate defaults.fqdn if set
	if cfg.Defaults.FQDN != "" {
		if !strings.Contains(cfg.Defaults.FQDN, ".") {
			errs.Add("defaults.fqdn: must be a valid domain name (got %q)", cfg.Defaults.FQDN)
		}
		if strings.HasPrefix(cfg.Defaults.FQDN, ".") || strings.HasSuffix(cfg.Defaults.FQDN, ".") {
			errs.Add("defaults.fqdn: cannot start or end with a dot (got %q)", cfg.Defaults.FQDN)
		}
	}

	// Validate defaults.acme_email if set
	if cfg.Defaults.AcmeEmail != "" && !strings.Contains(cfg.Defaults.AcmeEmail, "@") {
		errs.Add("defaults.acme_email: must be a valid email address (got %q)", cfg.Defaults.AcmeEmail)
	}

	// Validate each enabled image's FQDN and ACME email
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}

		if img.FQDN != "" {
			if !strings.Contains(img.FQDN, ".") {
				errs.Add("image %q: fqdn must be a valid domain name (got %q)", imageName, img.FQDN)
			}
			if strings.HasPrefix(img.FQDN, ".") || strings.HasSuffix(img.FQDN, ".") {
				errs.Add("image %q: fqdn cannot start or end with a dot (got %q)", imageName, img.FQDN)
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
func validateTunnel(cfg *Config, errs *ValidationError) {
	check := func(name string, t *TunnelYAML, fqdn string) {
		if t == nil {
			return
		}
		if t.Provider != "tailscale" && t.Provider != "cloudflare" {
			errs.Add("%s: tunnel provider must be \"tailscale\" or \"cloudflare\", got %q", name, t.Provider)
			return
		}
		if t.Port != 0 && (t.Port < 1 || t.Port > 65535) {
			errs.Add("%s: tunnel port must be 1-65535, got %d", name, t.Port)
		}
		if t.Provider == "tailscale" {
			if t.Funnel {
				// Funnel (public): restricted to 443, 8443, 10000
				if t.HTTPS != 0 && !ValidFunnelPorts[t.HTTPS] {
					errs.Add("%s: tunnel https must be 443, 8443, or 10000 for funnel, got %d", name, t.HTTPS)
				}
			} else {
				// Serve (tailnet-private): wider port range
				if t.HTTPS != 0 && !isValidServePort(t.HTTPS) {
					errs.Add("%s: tunnel https must be 80, 443, 3000-10000, 4443, 5432, 6443, or 8443 for serve, got %d", name, t.HTTPS)
				}
			}
		}
		if t.Provider == "cloudflare" {
			if fqdn == "" {
				errs.Add("%s: tunnel provider \"cloudflare\" requires fqdn to be set", name)
			}
			if t.Tunnel != "" && !tunnelNameRe.MatchString(t.Tunnel) {
				errs.Add("%s: tunnel name must match [a-zA-Z0-9][a-zA-Z0-9-]*, got %q", name, t.Tunnel)
			}
		}
	}

	check("defaults", cfg.Defaults.Tunnel, cfg.Defaults.FQDN)
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		// Resolve FQDN for the image (image -> defaults)
		fqdn := img.FQDN
		if fqdn == "" {
			fqdn = cfg.Defaults.FQDN
		}
		check(fmt.Sprintf("image %q", imageName), img.Tunnel, fqdn)
	}

	// Cross-image tailscale HTTPS port conflict check
	portUsers := make(map[int][]string) // https port -> image names
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
		httpsPort := tunnel.HTTPS
		if httpsPort == 0 {
			httpsPort = 443
		}
		portUsers[httpsPort] = append(portUsers[httpsPort], imageName)
	}
	for port, names := range portUsers {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		errs.Add("images %q and %q: both use tailscale tunnel https port %d (set different https ports to avoid conflict)", names[0], names[1], port)
	}
}

// validateBindMounts validates bind mount declarations in images
func validateBindMounts(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for imageName, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		if len(img.BindMounts) == 0 {
			continue
		}

		seen := make(map[string]bool)
		for _, bm := range img.BindMounts {
			// Name is required and must match pattern
			if bm.Name == "" {
				errs.Add("image %q bind_mounts: missing required \"name\" field", imageName)
			} else if !volumeNameRe.MatchString(bm.Name) {
				errs.Add("image %q bind_mounts: name %q must be lowercase alphanumeric with hyphens", imageName, bm.Name)
			} else if seen[bm.Name] {
				errs.Add("image %q bind_mounts: duplicate name %q", imageName, bm.Name)
			} else {
				seen[bm.Name] = true
			}

			// Path is required
			if bm.Path == "" {
				errs.Add("image %q bind_mounts: missing required \"path\" field for %q", imageName, bm.Name)
			}

			// Encrypted vs plain rules
			if bm.Encrypted {
				if bm.Host != "" {
					errs.Add("image %q bind_mounts: encrypted mount %q must not have \"host\" (ov manages storage)", imageName, bm.Name)
				}
			} else {
				if bm.Host == "" {
					errs.Add("image %q bind_mounts: plain mount %q requires \"host\" path", imageName, bm.Name)
				}
			}
		}

		// Check for name collisions with layer volumes
		if len(img.Layers) > 0 {
			resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
			if err == nil {
				volNames := make(map[string]bool)
				for _, layerName := range resolved {
					layer, ok := layers[layerName]
					if !ok || !layer.HasVolumes {
						continue
					}
					for _, vol := range layer.Volumes() {
						volNames[vol.Name] = true
					}
				}
				for _, bm := range img.BindMounts {
					if bm.Name != "" && volNames[bm.Name] {
						fmt.Fprintf(os.Stderr, "Note: image %q bind mount %q overrides layer volume with same name\n", imageName, bm.Name)
					}
				}
			}
		}
	}

	// Non-fatal warning if gocryptfs not in PATH when encrypted mounts exist
	hasEncrypted := false
	for _, img := range cfg.Images {
		if !img.IsEnabled() {
			continue
		}
		for _, bm := range img.BindMounts {
			if bm.Encrypted {
				hasEncrypted = true
				break
			}
		}
		if hasEncrypted {
			break
		}
	}
	if hasEncrypted {
		if _, err := exec_LookPath("gocryptfs"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: gocryptfs not found in PATH (required for encrypted bind mounts)\n")
		}
	}
}

// validateModules checks remote module consistency
func validateModules(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Find all modules referenced by remote layer refs in images
	usedModules := CollectRequiredModules(cfg)

	// Also collect from layer dependencies
	for _, layer := range layers {
		for _, dep := range layer.Depends {
			if IsRemoteLayerRef(dep) {
				modPath, _ := SplitRemoteLayerRef(dep)
				usedModules[modPath] = true
			}
		}
	}

	if len(usedModules) == 0 {
		return // No remote layers, no validation needed
	}

	// If remote layers are used, check for naming conflicts
	// (ScanAllLayers would have already errored if module not cached)
	for _, layer := range layers {
		if !layer.Remote {
			continue
		}
		// Remote layer exists in the map, so its module was found.
		// Just verify no naming conflicts between remote layers from different modules.
		for _, other := range layers {
			if !other.Remote || other == layer {
				continue
			}
			if other.Name == layer.Name && other.ModulePath != layer.ModulePath {
				errs.Add("remote layer name conflict: %q provided by both %s and %s", layer.Name, layer.ModulePath, other.ModulePath)
			}
		}
	}
}

// validateSystemdServices validates systemd .service files in layers
func validateSystemdServices(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasSystemdServices {
			continue
		}
		for _, svcPath := range layer.SystemdServices {
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
	// system_services requires rpm packages to provide the units
	for name, layer := range layers {
		if !layer.HasSystemServices {
			continue
		}
		for _, unit := range layer.SystemServiceUnits {
			if unit == "" {
				errs.Add("layer %q: system_services entry cannot be empty", name)
			}
			if strings.Contains(unit, "/") || strings.Contains(unit, " ") {
				errs.Add("layer %q: system_services entry %q must be a unit name (no paths or spaces)", name, unit)
			}
		}
		// Warn if layer has system_services but no RPM packages
		rpm := layer.RpmConfig()
		if rpm == nil || len(rpm.Packages) == 0 {
			errs.Add("layer %q: system_services requires rpm packages that provide those units", name)
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
			layerName, _ := StripVersion(layerRef)
			layer, ok := layers[layerName]
			if !ok || !layer.HasSystemServices {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: image %q includes layer %q with system_services, but is not a bootc image (system_services will be ignored)\n", imageName, layerName)
		}
	}
}

// isValidPort checks if a string is a valid port number (1-65535)
func isValidPort(s string) bool {
	n, err := strconv.Atoi(s)
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
					layerName, _ := StripVersion(layerRef)
					layer, ok := layers[layerName]
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
