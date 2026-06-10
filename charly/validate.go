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

// Validate validates the configuration and candies. opts.IncludeDisabled
// extends the validation pass to images marked enabled: false (the
// validator pre-filters them out by default to keep its working-set
// aligned with the build verb).
func Validate(cfg *Config, layers map[string]*Candy, dir string, opts ResolveOpts) error {
	errs := &ValidationError{}

	// Load default build config for global validation. Unconditional — the
	// caller is required to pass a project dir containing charly.yml.
	// Tests that need in-memory-only validation must use testProjectDir(t).
	var defaultDistroCfg *DistroConfig
	var defaultBuilderCfg *BuilderConfig
	var defaultInitCfg *InitConfig
	{
		dc, blc, ic, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			errs.Add("loading default build config: %v", err)
		} else {
			defaultDistroCfg = dc
			defaultBuilderCfg = blc
			defaultInitCfg = ic
		}
	}

	// Validate build and distro values
	if defaultDistroCfg != nil {
		validateBuildAndDistro(cfg, defaultDistroCfg, errs)
	}

	// Validate candies referenced in images
	validateCandyReferences(cfg, layers, errs)

	// Validate candy contents
	validateCandyContents(layers, errs)

	// Validate tasks: field (replaces root.yml/user.yml)
	validateCandyTasks(layers, errs)

	// Validate env files
	validateEnvFiles(layers, errs)

	// Validate package config (rpm/deb/pac/aur sections in the candy manifest)
	validatePkgConfig(layers, errs)

	// Validate image base references
	validateBaseReferences(cfg, errs)

	// Validate no circular dependencies in images
	validateBoxDAG(cfg, layers, dir, opts, errs)

	// Validate ports
	validatePort(cfg, layers, errs)

	// Validate routes
	validateRoutes(cfg, layers, errs)

	// Validate volumes
	validateVolume(layers, errs)

	// Validate merge config
	validateMergeConfig(cfg, errs)

	// Validate build-speed tunables (defaults.jobs / podman_jobs / cache / …)
	validateBuildTunables(cfg, errs)

	// Validate aliases
	validateAliases(cfg, layers, errs)

	// Validate builders and builds
	if defaultBuilderCfg != nil {
		validateBuilders(cfg, layers, defaultBuilderCfg, dir, opts, errs)
	}

	// Validate DNS and ACME email
	validateDNS(cfg, errs)

	// Tunnel is a deploy-time concern (charly.yml only) — not validated here.

	// Validate candy composition (candy: field)
	validateCandyIncludes(layers, errs)

	// Validate no circular dependencies in candies
	validateCandyDAG(cfg, layers, errs)

	// Validate remote candy consistency
	validateRemoteCandies(cfg, layers, errs)

	// Validate systemd service files
	validateSystemdServices(cfg, layers, errs)

	// Validate system_services entries
	validatePackagedServices(cfg, layers, errs)

	// Validate libvirt snippets
	validateLibvirt(cfg, layers, errs)

	// Validate engine declarations
	validateEngineConfig(cfg, layers, errs)

	// Validate port_relay declarations
	validatePortRelay(cfg, layers, errs)

	// Validate status fields
	validateStatus(cfg, layers, errs)

	// Validate version fields
	validateVersionFields(cfg, layers, errs)

	// Validate env_provides declarations
	validateEnvProvides(layers, errs)

	// Validate env_requires and env_accepts declarations (also enforces cross-section
	// collisions with secret_requires / secret_accepts via a unified seen map)
	validateEnvDeps(layers, errs)

	// Validate secret_requires and secret_accepts declarations (slug form, Key format,
	// collision with env_provides)
	validateSecretDeps(layers, errs)

	// Validate mcp_provides declarations
	validateMCPProvides(layers, errs)

	// Validate mcp_requires and mcp_accepts declarations
	validateMCPDeps(layers, errs)

	// Validate data candies and data images
	validateDataCandies(cfg, layers, errs)

	// Validate init system dependencies (driven by build.yml init: section)
	if defaultInitCfg != nil {
		validateInitDependencies(cfg, defaultInitCfg, layers, errs)
	}

	// Validate declarative test specs (the candy manifest, charly.yml, charly.yml
	// tests: + charly.yml deploy_eval:)
	validateTests(cfg, layers, errs)

	// Validate kind:local templates and target:local deployments.
	validateLocalTemplates(dir, layers, errs)
	validateLocalDeployments(dir, errs)

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// validateLocalTemplates checks each kind:local entity for: candy
// references that resolve, an empty `candy: []` (warning, allowed for
// stub placeholders), and a missing `candy:` field entirely (error).
// Also hard-errors on legacy `images:` fields surviving the 2026-05
// deploy-fetch-narrowing cutover (see validateLegacyLocalImagesField).
func validateLocalTemplates(dir string, layers map[string]*Candy, errs *ValidationError) {
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return
	}
	for name, spec := range uf.Local {
		if spec == nil {
			continue
		}
		if spec.Candy == nil {
			errs.Add("kind:local %q: missing required field `candy:` (use `candy: []` for an explicit placeholder)", name)
			continue
		}
		for _, candyRef := range spec.Candy {
			// Normalize remote @-refs to their bare key form (mirror of the
			// image candy path in validateCandyReferences) — remote candies are
			// stored in the map keyed by bare ref.
			candyName := BareRef(candyRef)
			if _, ok := layers[candyName]; !ok {
				if IsRemoteCandyRef(candyRef) {
					parsed := ParseRemoteRef(candyRef)
					errs.Add("kind:local %q: remote candy %q not found (candy %q doesn't exist in %s)", name, candyRef, parsed.Name, parsed.RepoPath)
				} else {
					errs.Add("kind:local %q: candy %q not found", name, candyName)
				}
			}
		}
	}
	validateLegacyLocalImagesField(dir, errs)
}

// validateLegacyLocalImagesField hard-errors on any surviving
// `images:` key nested under `local.<name>` in YAML. The field was
// removed from kind:local in the 2026-05 deploy-fetch-narrowing
// cutover; legacy configs must be migrated via `charly migrate
// local-images`. We walk raw YAML (not the typed struct) because the
// typed struct no longer carries the field — a soft `LoadUnified`
// would silently ignore it. Surfacing it here gives the operator the
// migration breadcrumb at config-load time.
func validateLegacyLocalImagesField(dir string, errs *ValidationError) {
	walkYAMLForLegacyLocalImages(dir, errs)
}

// validateLocalDeployments checks every target:local deployment:
//   - `local: <name>` references must resolve to a kind:local template.
//   - `host:` non-`local` values must parse via ParseSSHTarget.
//   - `user:` and `ssh_args:` are only meaningful when `host:` is non-`local`.
//   - `user:` redundancy when `host: <inline-user>@<machine>` is set.
func validateLocalDeployments(dir string, errs *ValidationError) {
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return
	}
	dc := uf.ProjectDeployConfig()
	if dc == nil {
		return
	}
	for name, node := range dc.Deploy {
		if node.Target != "local" && node.Target != "" {
			continue
		}
		if node.Target == "" && !strings.HasSuffix(name, "-local") && name != "local" {
			// Default target is "pod"; only validate when explicit.
			continue
		}
		if node.Local != "" {
			if uf.Local == nil || uf.Local[node.Local] == nil {
				errs.Add("deployment %q: kind:local template %q not found", name, node.Local)
			}
		}
		hostField := strings.TrimSpace(node.Host)
		isLocalDest := hostField == "" || hostField == "local"
		if !isLocalDest {
			if _, perr := ParseSSHTarget(hostField); perr != nil {
				errs.Add("deployment %q: invalid host %q: %v", name, hostField, perr)
			}
			if node.User != "" && strings.Contains(hostField, "@") {
				inlineUser := strings.SplitN(hostField, "@", 2)[0]
				if inlineUser != node.User {
					errs.Add("deployment %q: ambiguous user — host: %q has inline user %q but user: field is %q (remove one)",
						name, hostField, inlineUser, node.User)
				}
			}
		} else {
			if node.User != "" {
				errs.Add("deployment %q: user: field only meaningful when host: is non-local", name)
			}
			if len(node.SSHArgs) > 0 {
				errs.Add("deployment %q: ssh_args: field only meaningful when host: is non-local", name)
			}
		}
	}
}

// validateInitDependencies checks that images using an init system have the
// required dependency candy in their resolved dependency chain.
// For example, images with supervisord services must include the "supervisord" candy.
func validateInitDependencies(cfg *Config, initCfg *InitConfig, layers map[string]*Candy, errs *ValidationError) {
	if initCfg == nil {
		return
	}

	for imgName, img := range cfg.Box {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}

		// Resolve all candies for this image (own + transitive deps)
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue // other validators handle resolution errors
		}

		// For each init system with a depends_candy, check if it's needed and present.
		// Candy-derived caps replace the prior img.Bootc magic flag.
		caps, _ := AggregateCandyCapabilities(layers, resolved)
		if caps == nil {
			caps = &AggregatedCandyCaps{Provided: map[string]bool{}}
		}
		isBootcFlavored := caps.PreserveUser
		for initName, def := range initCfg.Init {
			if def.DependsCandy == "" {
				continue // no dependency requirement (e.g., systemd is provided by base OS)
			}

			// Skip init systems whose RequiresCapabilities aren't met by the
			// current composition.
			if !initDefRequirementsMet(def, caps) {
				continue
			}
			// For bootc-flavored compositions with dual-init candies
			// (service: + system_services:), skip supervisord depends_candy
			// check when systemd is also triggered.
			if len(def.RequiresCapability) == 0 && isBootcFlavored {
				hasSystemdCandy := false
				for _, candyName := range resolved {
					if layer, ok := layers[candyName]; ok && layer.HasInit("systemd") {
						hasSystemdCandy = true
						break
					}
				}
				if hasSystemdCandy {
					continue
				}
			}

			// Check if any candy requires this init system
			var needsInit []string
			for _, candyName := range resolved {
				layer, ok := layers[candyName]
				if !ok {
					continue
				}
				if layer.HasInit(initName) {
					needsInit = append(needsInit, candyName+" ("+initName+")")
				}
				// port_relay triggers init systems with relay_template
				if def.HasRelayTemplate() && len(layer.PortRelayPorts) > 0 {
					needsInit = append(needsInit, candyName+" (port_relay)")
				}
			}

			if len(needsInit) == 0 {
				continue
			}

			// Check if the depends_candy is in the resolved candies
			hasDepCandy := false
			for _, candyName := range resolved {
				// candyName is the resolved-order map KEY — for a remotely
				// consumed @github candy that's the qualified path, not the bare
				// name. Compare the candy's identity (layer.Name), per the idiom
				// documented at generate.go ("for REMOTE candies candyName is the
				// full @github map key; local: candyName == layer.Name").
				if l, ok := layers[candyName]; ok && l.Name == def.DependsCandy {
					hasDepCandy = true
					break
				}
			}

			// Also check base chain — dependency may be provided by a parent image
			if !hasDepCandy {
				images, resolveErr := cfg.ResolveAllBox("unused", ".", ResolveOpts{})
				if resolveErr == nil {
					allCandies := collectAllBoxCandies(imgName, images, layers)
					for _, l := range allCandies {
						if l == def.DependsCandy {
							hasDepCandy = true
							break
						}
					}
				}
			}

			if !hasDepCandy {
				// For dual-init candies (e.g., sshd with both service: and system_services:),
				// skip the error if ALL triggering candies also support another init system.
				// The candy is designed to use whichever init system the image provides.
				allDualInit := true
				for _, candyName := range resolved {
					layer, ok := layers[candyName]
					if !ok || !layer.HasInit(initName) {
						continue
					}
					hasAlternativeInit := false
					for altName := range initCfg.Init {
						if altName != initName && layer.HasInit(altName) {
							hasAlternativeInit = true
							break
						}
					}
					if !hasAlternativeInit {
						allDualInit = false
						break
					}
				}
				if !allDualInit {
					errs.Add("box %q has candies requiring %s (%s) but missing the %q candy in its dependency chain; add %q to the box's candies or a base image",
						imgName, initName, strings.Join(needsInit, ", "), def.DependsCandy, def.DependsCandy)
				}
			}
		}
	}
}

// validateBuildAndDistro validates build: and distro: entries.
// build: entries are checked against build.yml distro format definitions.
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
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		validateBuild(fmt.Sprintf("box %q", name), img.Build)
	}
}

// validateCandyReferences ensures all candies referenced in images exist
func validateCandyReferences(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		for _, candyRef := range img.Candy {
			candyName := BareRef(candyRef)
			if _, ok := layers[candyName]; !ok {
				if IsRemoteCandyRef(candyRef) {
					parsed := ParseRemoteRef(candyRef)
					errs.Add("box %q: remote candy %q not found (candy %q doesn't exist in %s)", boxName, candyRef, parsed.Name, parsed.RepoPath)
				} else {
					suggestion := findSimilarName(candyName, CandyNames(layers))
					if suggestion != "" {
						errs.Add("box %q: candy %q not found (did you mean %q?)", boxName, candyName, suggestion)
					} else {
						errs.Add("box %q: candy %q not found", boxName, candyName)
					}
				}
			}
		}
	}
}

// validateCandyContents validates each candy has required files
func validateCandyContents(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		// Candy must have at least one install file, a candy: field (composition), or data declarations
		if !layer.HasInstallFiles() && len(layer.IncludedCandy) == 0 && !layer.HasData() {
			errs.Add("candy %q: must have at least one install file (candy manifest rpm/deb packages, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml) or a candy: field", name)
		}

		// `version:` is MANDATORY for the candy kind (optional for every other
		// kind). It is the authoritative per-entity version that drives both the
		// image's content-stable ai.opencharly.version label and cross-repo
		// candy resolution (pickCandyVersion). Scoped to local candies — a fetched
		// remote candy with no version is already a hard error at scan time
		// (ScanAllCandyWithConfigOpts). Run `charly migrate` to backfill.
		if !layer.Remote && layer.Version == "" {
			errs.Add("candy %q: missing required `version:` (CalVer YYYY.DDD.HHMM). Run: charly migrate", name)
		}

		// If `directory:` redirected the source anchor, SourceDir must exist.
		// (For the default case SourceDir == Path, which is guaranteed to exist.)
		if layer.SourceDir != layer.Path && !dirExists(layer.SourceDir) {
			errs.Add("candy %q: directory %q does not exist (resolved to %q)", name, layer.SourceDir, layer.SourceDir)
		}

		// Cargo.toml requires src/ directory
		if layer.HasCargoToml && !layer.HasSrcDir {
			errs.Add("candy %q: Cargo.toml requires src/ directory", name)
		}

		// Validate depends references. Remote candies' deps were qualified to
		// fully-qualified map keys at scan time (qualifyRemoteSiblingDeps), so a
		// direct lookup covers both local short names and remote sibling refs.
		for _, depRef := range layer.Require {
			dep := depRef.Bare()
			if _, ok := layers[dep]; !ok {
				suggestion := findSimilarName(dep, CandyNames(layers))
				if suggestion != "" {
					errs.Add("candy %q depends: unknown candy %q (did you mean %q?)", name, dep, suggestion)
				} else {
					errs.Add("candy %q depends: unknown candy %q", name, dep)
				}
			}
		}

		// Validate extract field
		for _, ext := range layer.Extract() {
			if ext.Source == "" {
				errs.Add("candy %q: extract source cannot be empty", name)
			}
			if ext.Path == "" {
				errs.Add("candy %q: extract path cannot be empty", name)
			}
			if ext.Dest == "" {
				errs.Add("candy %q: extract dest cannot be empty", name)
			}
			if ext.Path != "" && !strings.HasPrefix(ext.Path, "/") {
				errs.Add("candy %q: extract path must be absolute (got %q)", name, ext.Path)
			}
			if ext.Dest != "" && !strings.HasPrefix(ext.Dest, "/") {
				errs.Add("candy %q: extract dest must be absolute (got %q)", name, ext.Dest)
			}
		}

		// Validate shell:-schema declarations (2026-05 cutover).
		validateCandyShell(name, layer.Shell(), errs)

		// Validate apk:-package-format entries (Android app installs).
		validateCandyApk(name, layer.Apk(), errs)
	}
}

// validateCandyApk enforces the `apk:` package-format invariants:
//   - exactly one of package: / apk: per entry (download by id XOR a
//     committed local APK),
//   - source: (when set) is one of the apkeep sources.
func validateCandyApk(candyName string, apks []ApkPackageSpec, errs *ValidationError) {
	for i, a := range apks {
		switch {
		case a.Package == "" && a.Apk == "":
			errs.Add("candy %q apk[%d]: must set either `package:` (apkeep download) or `apk:` (committed local APK)", candyName, i)
		case a.Package != "" && a.Apk != "":
			errs.Add("candy %q apk[%d]: set only ONE of `package:` or `apk:`, not both", candyName, i)
		}
		if a.Source != "" && !apkSourceAllowlist[a.Source] {
			errs.Add("candy %q apk[%d]: source %q is not valid (want apk-pure, google-play, f-droid, or huawei-app-gallery)", candyName, i, a.Source)
		}
		if a.Apk != "" && a.Source != "" {
			errs.Add("candy %q apk[%d]: `source:` applies only to `package:` (apkeep) installs, not committed `apk:` files", candyName, i)
		}
	}
}

// validateCandyShell enforces the shell:-schema invariants:
//   - Per-shell sub-block keys are restricted to the ShellAllowlist
//     (bash/zsh/fish/sh). Anything else is a hard error.
//   - When `init:` is declared (intrinsic OR per-shell), it must be
//     non-empty.
//   - `path:` overrides must not contain `..` traversal sequences and,
//     when present, must be either absolute or `~/`-prefixed.
//   - `path_append` entries follow the same path-traversal guard.
//   - Empty config (no init, no path_append, no per-shell subs, no
//     intrinsic path) is allowed and produces no warning — the parser
//     would have rejected anything genuinely malformed.
func validateCandyShell(candyName string, cfg *ShellConfig, errs *ValidationError) {
	if cfg == nil {
		return
	}
	checkSpec := func(label string, spec *ShellSpec) {
		if spec == nil {
			return
		}
		if spec.Init != "" && strings.TrimSpace(spec.Init) == "" {
			errs.Add("candy %q: shell.%s.init must not be whitespace-only", candyName, label)
		}
		if spec.Path != "" {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path", label), spec.Path, errs)
		}
		for _, p := range spec.PathAppend {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path_append", label), p, errs)
		}
	}
	// Intrinsic body — same checks as a per-shell spec.
	if cfg.Init != "" && strings.TrimSpace(cfg.Init) == "" {
		errs.Add("candy %q: shell.init must not be whitespace-only", candyName)
	}
	if cfg.Path != "" {
		validateShellPath(candyName, "shell.path", cfg.Path, errs)
	}
	for _, p := range cfg.PathAppend {
		validateShellPath(candyName, "shell.path_append", p, errs)
	}
	// Per-shell sub-blocks — UnmarshalYAML already enforced the
	// allowlist, but defend in depth in case ByShell is populated by
	// hand-rolled label-side data.
	for shell, spec := range cfg.ByShell {
		if !ShellAllowlist[shell] {
			errs.Add("candy %q: shell.%s: unknown shell name (must be one of bash/zsh/fish/sh)", candyName, shell)
			continue
		}
		checkSpec(shell, spec)
	}
}

// validateShellPath rejects path-traversal sequences and accepts only
// absolute paths or `~/`-prefixed paths (which ExpandPath resolves at
// install time).
func validateShellPath(candyName, field, p string, errs *ValidationError) {
	if p == "" {
		return
	}
	if strings.Contains(p, "..") {
		errs.Add("candy %q: %s contains traversal sequence (got %q)", candyName, field, p)
		return
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") && p != "~" {
		errs.Add("candy %q: %s must be absolute or ~/-prefixed (got %q)", candyName, field, p)
	}
}

// validateCandyIncludes validates candy composition (candy: field in the candy manifest)
func validateCandyIncludes(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.IncludedCandy) == 0 {
			continue
		}

		depSet := make(map[string]bool)
		for _, d := range layer.Require {
			depSet[d.Bare()] = true
		}

		for _, includedRef := range layer.IncludedCandy {
			ref := includedRef.Bare()
			// Self-inclusion
			if ref == name {
				errs.Add("candy %q candy: cannot include itself", name)
				continue
			}

			// Check ref exists. Deps are pre-qualified at scan time, so a direct
			// lookup covers both local short names and remote sibling refs.
			if _, ok := layers[ref]; !ok {
				suggestion := findSimilarName(ref, CandyNames(layers))
				if suggestion != "" {
					errs.Add("candy %q candy: unknown candy %q (did you mean %q?)", name, ref, suggestion)
				} else {
					errs.Add("candy %q candy: unknown candy %q", name, ref)
				}
			}

			// Overlap with depends
			if depSet[ref] {
				errs.Add("candy %q: %q appears in both 'candy' and 'depends'", name, ref)
			}
		}
	}

	// Check for circular composition
	for name, layer := range layers {
		if len(layer.IncludedCandy) == 0 {
			continue
		}
		if err := checkIncludeCycle(name, layers, nil); err != nil {
			errs.Add("candy %q: %v", name, err)
		}
	}
}

// checkIncludeCycle detects circular candy composition
func checkIncludeCycle(name string, layers map[string]*Candy, visited map[string]bool) error {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[name] {
		return fmt.Errorf("circular candy composition involving %q", name)
	}
	layer, ok := layers[name]
	if !ok || len(layer.IncludedCandy) == 0 {
		return nil
	}
	visited[name] = true
	for _, includedRef := range layer.IncludedCandy {
		if err := checkIncludeCycle(includedRef.Bare(), layers, visited); err != nil {
			return err
		}
	}
	delete(visited, name)
	return nil
}

// validateEnvFiles validates env config from the candy manifest
func validateEnvFiles(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasEnv() {
			continue
		}

		cfg, _ := layer.EnvConfig()
		if cfg == nil {
			continue
		}

		// PATH must not be set directly (use path_append in the candy manifest)
		if _, hasPath := cfg.Vars["PATH"]; hasPath {
			errs.Add("candy %q candy manifest: use path_append instead of setting PATH in env", name)
		}
	}
}

// validatePkgConfig validates per-distro/format package config in the candy
// manifest. Packages live in per-distro tag sections (debian, ubuntu, debian:13,
// …) plus the always-included top-level base; the only format section is `aur`.
// repo/copr/module entries are meaningful only when the candy installs packages,
// but a repo may sit on one distro level while the package it serves sits at the
// top-level base (the nodesource pattern) — so the "requires packages" gate is
// the WHOLE-CANDY union (HasAnyPackages), not the single section. Repo entries
// must carry a name. The canonical repo key is `repo` (singular) — what
// derivePackageSectionsFromCalamares writes and DistroPackages.Repo unmarshals.
func validatePkgConfig(layers map[string]*Candy, errs *ValidationError) {
	validateRaw := func(name, label string, raw map[string]interface{}, candyHasPkgs bool) {
		if raw == nil {
			return
		}
		if repos := toMapSlice(raw["repo"]); len(repos) > 0 {
			if !candyHasPkgs {
				errs.Add("candy %q candy manifest: %s.repo requires packages (none declared anywhere in the candy)", name, label)
			}
			for _, repo := range repos {
				repoName := fmt.Sprint(repo["name"])
				if repoName == "" || repoName == "<nil>" {
					errs.Add("candy %q candy manifest: %s.repo entry requires name", name, label)
				}
			}
		}
		if copr := toStringSlice(raw["copr"]); len(copr) > 0 && !candyHasPkgs {
			errs.Add("candy %q candy manifest: %s.copr requires packages", name, label)
		}
		if modules := toStringSlice(raw["modules"]); len(modules) > 0 && !candyHasPkgs {
			errs.Add("candy %q candy manifest: %s.modules requires packages", name, label)
		}
	}
	for name, layer := range layers {
		hasPkgs := layer.HasAnyPackages()
		for tag, cfg := range layer.tagSections {
			validateRaw(name, "distro."+tag, cfg.Raw, hasPkgs)
		}
		for formatName, section := range layer.formatSections {
			validateRaw(name, formatName, section.Raw, hasPkgs)
		}
	}
}

// validateBaseReferences ensures base references resolve
func validateBaseReferences(cfg *Config, errs *ValidationError) {
	// Base references can be:
	// 1. External OCI images (always valid)
	// 2. Names of other images in charly.yml (validated by image DAG check)
	// No additional validation needed here
}

// validateBoxDAG checks for circular image dependencies
func validateBoxDAG(cfg *Config, layers map[string]*Candy, dir string, opts ResolveOpts, errs *ValidationError) {
	calverTag := "test"
	// Try to resolve images — some fields may be missing during basic validation
	images := make(map[string]*ResolvedBox)
	for name, img := range cfg.Box {
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		ri, err := cfg.ResolveBox(name, calverTag, dir, opts)
		if err != nil {
			// Skip images that can't resolve (e.g., missing build: field)
			continue
		}
		images[name] = ri
	}
	if len(images) == 0 {
		return
	}
	// Pull in namespace-qualified base images (e.g. `versa: base: cachyos.cachyos`)
	// so the DAG's base edges don't dangle — same enrichment ResolveAllImages does.
	// SURFACE a namespace-resolution error: an enabled image references a
	// namespaced base — or that base's builder / bootstrap builder — that does
	// not resolve. This is the automatic guard that catches namespace-ref leaks
	// (e.g. a pulled base's `builder: charly.arch-builder` not re-qualified to
	// `selkies.charly.arch-builder`) at `charly box validate` time, before a build hits
	// it. The DAG check below can't run with dangling bases, so report + return.
	if err := cfg.resolveNamespacedBases(images, calverTag, dir, opts); err != nil {
		errs.Add("namespaced base resolution: %v", err)
		return
	}

	_, orderErr := ResolveBoxOrder(images, layers)
	if orderErr != nil {
		if cycleErr, ok := orderErr.(*CycleError); ok {
			errs.Add("box dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			errs.Add("box DAG error: %v", orderErr)
		}
		// A cyclic / broken image DAG was already reported; the global candy
		// order over it is meaningless, so stop here.
		return
	}

	// Resolve the GLOBAL candy order over the enriched image set (locals PLUS the
	// namespace-pulled bases/builders above) — the SAME computation that
	// `charly box generate`/`build` run (ComputeIntermediates → GlobalCandyOrder).
	// validateCandyDAG only iterates cfg.Box, so a candy missing from a PULLED
	// builder (e.g. an imported charly.fedora-builder's rpmfusion) slipped past
	// validate yet failed generate; running it here restores validate↔generate
	// agreement so the gap is caught at validate time, not only at build time.
	// Reached only on an acyclic DAG (the cycle guard above returned otherwise).
	if _, glErr := GlobalCandyOrder(images, layers); glErr != nil {
		errs.Add("global candy order: %v", glErr)
	}
}

// validateCandyDAG checks for circular candy dependencies
func validateCandyDAG(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Check each image's candies for cycles
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		// Convert raw refs to bare refs for candy map lookup
		bareCandies := make([]string, len(img.Candy))
		for i, ref := range img.Candy {
			bareCandies[i] = BareRef(ref)
		}
		_, err := ResolveCandyOrder(bareCandies, layers, nil)
		if err != nil {
			if cycleErr, ok := err.(*CycleError); ok {
				errs.Add("box %q: candy dependency cycle: %s", boxName, strings.Join(cycleErr.Cycle, " -> "))
			} else {
				errs.Add("box %q: candy resolution error: %v", boxName, err)
			}
		}
	}
}

// validatePort validates port declarations in candies and images
func validatePort(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate candy ports from the candy manifest
	for name, layer := range layers {
		if !layer.HasPorts() {
			continue
		}
		ports, _ := layer.Port()
		for _, port := range ports {
			if !isValidPort(port) {
				errs.Add("candy %q candy manifest ports: %q is not a valid port number (1-65535)", name, port)
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
					errs.Add("box %q ports: %q is not a valid port number (1-65535)", name, parts[0])
				}
			case 2:
				if !isValidPort(parts[0]) {
					errs.Add("box %q ports: host port %q in %q is not valid (1-65535)", name, parts[0], mapping)
				}
				if !isValidPort(parts[1]) {
					errs.Add("box %q ports: container port %q in %q is not valid (1-65535)", name, parts[1], mapping)
				}
			default:
				errs.Add("box %q ports: %q must be \"port\" or \"host:container\" format", name, mapping)
			}
		}
	}

	if len(cfg.Defaults.Port) > 0 {
		validatePortMappings("defaults", cfg.Defaults.Port)
	}
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		if len(img.Port) > 0 {
			validatePortMappings(name, img.Port)
		}
	}
}

// validateRoutes validates route file declarations in candies
func validateRoutes(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate route config from the candy manifest
	for name, layer := range layers {
		if !layer.HasRoute() {
			continue
		}
		route, _ := layer.Route()
		if route == nil {
			continue
		}
		if route.Host == "" {
			errs.Add("candy %q candy manifest route: missing required \"host\" field", name)
		}
		if route.Port == "" {
			errs.Add("candy %q candy manifest route: missing required \"port\" field", name)
		} else if !isValidPort(route.Port) {
			errs.Add("candy %q candy manifest route: %q is not a valid port number (1-65535)", name, route.Port)
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
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		check(fmt.Sprintf("box %q", name), img.Merge)
	}
}

// validBuildCacheModes is the allow-list for defaults.cache / image.cache.
// Empty string means "auto" (resolved at build time in cacheArgs).
var validBuildCacheModes = map[string]bool{
	"": true, "image": true, "registry": true, "gha": true, "none": true,
}

// validateBuildTunables validates the build-speed knobs on defaults: and any
// image entry: jobs >= 1, podman_jobs >= 0, podman_jobs_cap >= 1, cache in
// the allow-list, and no empty context_ignore entries. These are project-wide
// defaults; values are validated wherever they appear so a typo surfaces at
// `charly box validate` rather than silently mis-driving a build.
func validateBuildTunables(cfg *Config, errs *ValidationError) {
	check := func(name string, ic BoxConfig) {
		if ic.Jobs != nil && *ic.Jobs < 1 {
			errs.Add("%s: jobs must be >= 1, got %d", name, *ic.Jobs)
		}
		if ic.PodmanJobs != nil && *ic.PodmanJobs < 0 {
			errs.Add("%s: podman_jobs must be >= 0, got %d", name, *ic.PodmanJobs)
		}
		if ic.PodmanJobsCap != nil && *ic.PodmanJobsCap < 1 {
			errs.Add("%s: podman_jobs_cap must be >= 1, got %d", name, *ic.PodmanJobsCap)
		}
		if !validBuildCacheModes[ic.Cache] {
			errs.Add("%s: cache must be one of image|registry|gha|none, got %q", name, ic.Cache)
		}
		for i, p := range ic.ContextIgnore {
			if strings.TrimSpace(p) == "" {
				errs.Add("%s: context_ignore[%d] must not be empty", name, i)
			}
		}
		if ic.KeepImages != nil && *ic.KeepImages < 0 {
			errs.Add("%s: keep_images must be >= 0 (0 = disabled), got %d", name, *ic.KeepImages)
		}
		if ic.KeepEvalRuns != nil && *ic.KeepEvalRuns < 0 {
			errs.Add("%s: keep_eval_runs must be >= 0 (0 = disabled), got %d", name, *ic.KeepEvalRuns)
		}
	}

	check("defaults", cfg.Defaults)
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		check(fmt.Sprintf("box %q", name), img)
	}
}

// volumeNameRe matches valid volume names: lowercase alphanumeric + hyphens
var volumeNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// validateVolume validates volume declarations in candies
func validateVolume(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasVolumes() {
			continue
		}
		seen := make(map[string]bool)
		for _, vol := range layer.Volume() {
			if vol.Name == "" {
				errs.Add("candy %q candy manifest volumes: missing required \"name\" field", name)
			} else if !volumeNameRe.MatchString(vol.Name) {
				errs.Add("candy %q candy manifest volumes: name %q must be lowercase alphanumeric with hyphens", name, vol.Name)
			} else if seen[vol.Name] {
				errs.Add("candy %q candy manifest volumes: duplicate volume name %q", name, vol.Name)
			} else {
				seen[vol.Name] = true
			}
			if vol.Path == "" {
				errs.Add("candy %q candy manifest volumes: missing required \"path\" field", name)
			}
		}
	}
}

// validateAliases validates alias declarations in candies and images
func validateAliases(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate candy aliases
	for name, layer := range layers {
		if !layer.HasAliases() {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range layer.Alias() {
			if a.Name == "" {
				errs.Add("candy %q candy manifest aliases: missing required \"name\" field", name)
			} else if !aliasNameRe.MatchString(a.Name) {
				errs.Add("candy %q candy manifest aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", name, a.Name)
			} else if seen[a.Name] {
				errs.Add("candy %q candy manifest aliases: duplicate alias name %q", name, a.Name)
			} else {
				seen[a.Name] = true
			}
			if a.Command == "" {
				errs.Add("candy %q candy manifest aliases: missing required \"command\" field for alias %q", name, a.Name)
			}
		}
	}

	// Validate image-level aliases
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		if len(img.Alias) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range img.Alias {
			if a.Name == "" {
				errs.Add("box %q aliases: missing required \"name\" field", boxName)
			} else if !aliasNameRe.MatchString(a.Name) {
				errs.Add("box %q aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", boxName, a.Name)
			} else if seen[a.Name] {
				errs.Add("box %q aliases: duplicate alias name %q", boxName, a.Name)
			} else {
				seen[a.Name] = true
			}
		}
	}
}

// validateBuilders validates builder and builds configuration
func validateBuilders(cfg *Config, layers map[string]*Candy, builderCfg *BuilderConfig, dir string, opts ResolveOpts, errs *ValidationError) {
	// Validate defaults.builder entries
	for typ, builder := range cfg.Defaults.Builder {
		if !builderCfg.ValidBuilderType(typ) {
			errs.Add("defaults.builder: build type %q is not valid (known builders: %s)", typ, strings.Join(builderCfg.BuilderNames(), ", "))
		}
		if builder != "" {
			// Namespace-aware: a defaults builder ref may be qualified (e.g.
			// `charly.fedora-builder`), resolving through an import namespace.
			builderImg, _, exists := cfg.resolveBoxRef(builder)
			if !exists {
				errs.Add("defaults.builder.%s: box %q not found", typ, builder)
			} else if !builderImg.IsEnabled() {
				errs.Add("defaults.builder.%s: box %q is disabled", typ, builder)
			}
		}
	}

	// Validate each enabled image
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}

		// Validate builds: entries (capability declarations on builder images)
		for _, b := range img.Produce {
			if !builderCfg.ValidBuilderType(b) {
				errs.Add("box %q: builds entry %q is not valid (known builders: %s)", boxName, b, strings.Join(builderCfg.BuilderNames(), ", "))
			}
		}

		// Validate builder: entries (per-type builder assignments)
		for typ, builder := range img.Builder {
			if !builderCfg.ValidBuilderType(typ) {
				errs.Add("box %q: builder.%s is not a valid build type (known builders: %s)", boxName, typ, strings.Join(builderCfg.BuilderNames(), ", "))
			}
			if builder == boxName {
				errs.Add("box %q: builder.%s cannot reference self", boxName, typ)
				continue
			}
			if builder != "" {
				// Namespace-aware: a builder ref may be qualified (e.g.
				// `charly.arch-builder`), resolving through an import namespace.
				builderImg, _, exists := cfg.resolveBoxRef(builder)
				if !exists {
					errs.Add("box %q: builder.%s references %q which is not found", boxName, typ, builder)
					continue
				}
				if !builderImg.IsEnabled() {
					errs.Add("box %q: builder.%s references %q which is disabled", boxName, typ, builder)
					continue
				}
				// Check builder declares this capability
				hasCapability := false
				for _, b := range builderImg.Produce {
					if b == typ {
						hasCapability = true
						break
					}
				}
				if len(builderImg.Produce) > 0 && !hasCapability {
					errs.Add("box %q: builder.%s references %q which does not declare builds: [%s]", boxName, typ, builder, typ)
				}
			}
		}

		// Resolve the image through the SINGLE canonical path (ResolveBox) —
		// the SAME resolution `charly box build` / `generate` / `inspect` use — so
		// the effective builder map and build formats checked here are exactly
		// what the build will see. This block previously RE-IMPLEMENTED builder +
		// build-format resolution inline, which silently diverged from
		// ResolveBox (it missed the distro-keyed builder default, so a cachyos
		// image's auto-resolved arch-builder — including aur — was invisible to
		// validation and produced a false "no builder.aur configured" error).
		// One code path, no drift.
		ri, rerr := cfg.ResolveBox(boxName, "test", dir, opts)
		if rerr != nil {
			// Can't resolve (e.g. a namespaced base unavailable during basic
			// validation) — validateBoxDAG surfaces resolution errors; skip
			// the builder-needed check rather than false-positive on a
			// half-resolved map.
			continue
		}
		resolved := ri.Builder
		buildFmtSet := make(map[string]bool, len(ri.BuildFormats))
		for _, f := range ri.BuildFormats {
			buildFmtSet[f] = true
		}

		// Check if candies need builders that aren't configured.
		// Detection is fully config-driven from build.yml builder: section:
		//   detect_files: candy has any of these files
		//   detect_config: candy has this format section with packages
		candyOrder, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}
		for _, candyName := range candyOrder {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			for builderName, builderDef := range builderCfg.Builder {
				fileMatched := false
				for _, f := range builderDef.DetectFiles {
					if candyHasFile(layer, f) {
						fileMatched = true
						break
					}
				}
				configMatched := false
				if builderDef.DetectConfig != "" && candyHasFormatConfig(layer, builderDef.DetectConfig) {
					configMatched = true
				}
				if !fileMatched && !configMatched {
					continue
				}
				// Distro-aware gate: when detection fired ONLY via a
				// format-section (DetectConfig, e.g. aur:) and not via files,
				// skip the check if the image's resolved BuildFormats doesn't
				// include that format. The IR compiler
				// (install_build.go:236-249) iterates only img.BuildFormats, so
				// the section is unreachable for this image — the builder is
				// not actually needed even though the candy has the section.
				// Multi-distro candies can carry rpm: + pac: + aur:
				// simultaneously without forcing every Fedora consumer to
				// declare an arch-builder.
				//
				// detect_files-based detection is NOT gated: pixi/npm/cargo
				// artifacts are produced once at build time and copied into
				// the final stage regardless of distro, so the builder is
				// required for any image consuming the candy.
				if !fileMatched && configMatched && !buildFmtSet[builderDef.DetectConfig] {
					continue
				}
				if !resolved.HasBuilder(builderName) {
					errs.Add("box %q: candy %q needs builder %q but no builder.%s configured", boxName, candyName, builderName, builderName)
				}
			}
		}
	}
}

// validateDNS is a no-op in schema v4. DNS and AcmeEmail moved off
// BoxConfig to DeploymentNode (they're deployment choices). Deploy-side
// validation of these fields is handled by validateDeployConfig.
func validateDNS(cfg *Config, errs *ValidationError) {
	// intentionally empty — schema v4 removed image-level dns/acme_email
}

// tunnelNameRe matches valid cloudflare tunnel names
var tunnelNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// validateTunnel validates tunnel configuration in defaults and images
func validateTunnel(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	check := func(name string, t *TunnelYAML, dns string, boxPorts []string, portProtos map[int]string) {
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
		for _, hp := range parseHostPorts(boxPorts) {
			hostPortSet[hp] = true
		}

		// Tailscale public port list validation
		if t.Provider == "tailscale" {
			hostToContainer := buildPortMapping(boxPorts)

			for _, p := range t.Public.Ports {
				if !ValidPublicPorts[p] {
					errs.Add("%s: tailscale public port %d must be 443, 8443, or 10000", name, p)
				}
				// TCP-family ports can't be public
				if portProtos != nil {
					cp := p
					if c, ok := hostToContainer[p]; ok {
						cp = c
					}
					if isTCPFamily(resolveProto(cp, portProtos)) {
						errs.Add("%s: TCP port %d cannot be public (only HTTP ports can be internet-accessible)", name, p)
					}
				}
			}

			// Tailscale public: all — validate each non-TCP image port is a valid public port
			if t.Public.All {
				for _, hp := range parseHostPorts(boxPorts) {
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
					if isTCPFamily(proto) {
						continue // TCP-family ports are skipped in public: all
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
					errs.Add("%s: public port %d not found in box ports", name, p)
				}
			}
			for p := range t.Public.PortMap {
				if !hostPortSet[p] {
					errs.Add("%s: public port %d not found in box ports", name, p)
				}
			}
			for _, p := range t.Private.Ports {
				if !hostPortSet[p] {
					errs.Add("%s: private port %d not found in box ports", name, p)
				}
			}
		}

		// Cloudflare-specific validation
		if t.Provider == "cloudflare" {
			publicCount := len(t.Public.Ports) + len(t.Public.PortMap)
			if t.Public.All {
				publicCount = len(boxPorts)
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

		// Validate port schemes against provider capabilities
		if portProtos != nil {
			hostToContainer := buildPortMapping(boxPorts)
			for _, hp := range parseHostPorts(boxPorts) {
				cp := hp
				if c, ok := hostToContainer[hp]; ok {
					cp = c
				}
				proto := resolveProto(cp, portProtos)
				if proto == "http" {
					continue // default scheme, always valid
				}
				if t.Provider == "tailscale" && !validTailscaleSchemes[proto] {
					errs.Add("%s: port %d has scheme %q which is not supported by tailscale (supported: http, https, https+insecure, tcp, tls-terminated-tcp)", name, hp, proto)
				}
				if t.Provider == "cloudflare" && !validCloudflareSchemes[proto] {
					errs.Add("%s: port %d has scheme %q which is not supported by cloudflare (supported: http, https, tcp, ssh, rdp, smb)", name, hp, proto)
				}
			}
		}
	}

	// Schema v4: Tunnel / DNS moved off BoxConfig. Tunnel validation +
	// cross-image port conflict detection now apply at deploy-side (see
	// validateDeployConfig), not at image-side. This image-side check is
	// a no-op in v4 — the `check` helper is retained for deploy validation
	// to call with DeploymentNode.Tunnel values.
	_ = check
	_ = layers
}

// validateRemoteCandies checks remote candy consistency
func validateRemoteCandies(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Check version conflicts (same repo referenced with different versions)
	_, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		errs.Add("%v", err)
	}

	// Check for naming conflicts between remote candies from different repos
	for _, layer := range layers {
		if !layer.Remote {
			continue
		}
		for _, other := range layers {
			if !other.Remote || other == layer {
				continue
			}
			if other.Name == layer.Name && other.RepoPath != layer.RepoPath {
				errs.Add("remote candy name conflict: %q provided by both %s and %s", layer.Name, layer.RepoPath, other.RepoPath)
			}
		}
	}
}

// validateSystemdServices validates systemd .service files in candies
func validateSystemdServices(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.ServiceFiles()) == 0 {
			continue
		}
		for _, svcPath := range layer.ServiceFiles() {
			info, err := os.Stat(svcPath)
			if err != nil {
				errs.Add("candy %q: systemd service file %q not readable: %v", name, filepath.Base(svcPath), err)
				continue
			}
			if info.Size() == 0 {
				errs.Add("candy %q: systemd service file %q is empty", name, filepath.Base(svcPath))
			}
		}
	}
}

// validatePackagedServices validates use_packaged: entries in each candy's
// service: list. Use_packaged names the distro-shipped systemd unit that the
// entry reuses (e.g. "sshd.service", "postgresql.service"). Rules:
//   - use_packaged name cannot contain paths or spaces (must be a unit name).
//   - The candy must declare packages that provide those units.
//   - Packaged entries only work on bootc / systemd images; warn otherwise.
func validatePackagedServices(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	candyHasPackaged := func(l *Candy) bool {
		for i := range l.Service() {
			if l.Service()[i].IsPackaged() {
				return true
			}
		}
		return false
	}
	for name, layer := range layers {
		for i := range layer.Service() {
			entry := &layer.Service()[i]
			if !entry.IsPackaged() {
				continue
			}
			unit := entry.UsePackaged
			if unit == "" {
				errs.Add("candy %q: service[%d] use_packaged cannot be empty", name, i)
			}
			if strings.Contains(unit, "/") || strings.Contains(unit, " ") {
				errs.Add("candy %q: service[%d] use_packaged %q must be a unit name (no paths or spaces)", name, i, unit)
			}
		}
		if candyHasPackaged(layer) && !layer.HasAnyPackages() {
			errs.Add("candy %q: use_packaged entries require candy packages (distro tag sections or top-level package:) that provide those units", name)
		}
	}

	// Warn if use_packaged is used on a composition that doesn't preserve
	// the candy USER (i.e. non-bootc-flavored images). Only systemd-based
	// compositions consume packaged units; supervisord, the container
	// default init, can't.
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		// Resolve candy order to aggregate caps for this image.
		var imgCandies []string
		for _, ref := range img.Candy {
			imgCandies = append(imgCandies, BareRef(ref))
		}
		caps, _ := AggregateCandyCapabilities(layers, imgCandies)
		if caps != nil && caps.PreserveUser {
			continue
		}
		for _, candyRef := range img.Candy {
			bare := BareRef(candyRef)
			layer, ok := layers[bare]
			if !ok || !candyHasOrphanPackaged(layer) {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: box %q includes candy %q with a packaged-only service (no custom-exec sibling), but composition does not preserve_user (its systemd unit will be ignored)\n", boxName, bare)
		}
	}
}

// candyHasOrphanPackaged reports whether the candy has a use_packaged service
// entry with NO custom-exec sibling of the same name. Only such "orphan"
// packaged units are genuinely dropped under supervisord (the container default
// init can't consume packaged systemd units), so only they warrant the
// preserve_user warning. When a same-name exec sibling exists — the canonical
// mixed-form init polymorphism pattern (e.g. sshd: use_packaged sshd.service +
// exec sshd-wrapper) — supervisord renders the exec form, nothing is lost, and
// warning would be a false positive.
func candyHasOrphanPackaged(l *Candy) bool {
	if l == nil {
		return false
	}
	svcs := l.Service()
	customNames := make(map[string]bool)
	for i := range svcs {
		if !svcs[i].IsPackaged() && svcs[i].Exec != "" && svcs[i].Name != "" {
			customNames[svcs[i].Name] = true
		}
	}
	for i := range svcs {
		if svcs[i].IsPackaged() && !customNames[svcs[i].Name] {
			return true
		}
	}
	return false
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

// validateLibvirt validates libvirt XML snippets in candies and images
func validateLibvirt(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate candy-level snippets
	for name, layer := range layers {
		if !layer.HasLibvirt() {
			continue
		}
		for i, snippet := range layer.Libvirt() {
			if err := ValidateLibvirtSnippet(snippet); err != nil {
				errs.Add("candy %q libvirt[%d]: %v", name, i, err)
			}
		}
	}

	// Image-level `libvirt:` field was removed in the VM hard-cutover.
	// Raw XML snippets live on candy `libvirt:` fields (validated above)
	// and on `kind: vm` entity `spec.libvirt.snippets:` lists (validated
	// by ValidateLibvirtDomain in libvirt_validate.go).
	_ = cfg
	_ = layers
}

// validateEngineConfig validates engine declarations in candies and images
func validateEngineConfig(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	validEngines := map[string]bool{"docker": true, "podman": true}

	// Validate candy engine declarations
	for name, layer := range layers {
		if e := layer.Engine(); e != "" && !validEngines[e] {
			errs.Add("candy %q: engine must be \"docker\" or \"podman\", got %q", name, e)
		}
	}

	// Schema v4: BoxConfig.Engine removed (deploy-only choice).
	// Defaults.Engine + per-image Engine no longer exist. Candy-level
	// conflict detection still applies below.

	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}

		// Check for conflicting candy engine requirements within the image
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}

		engineSources := make(map[string]string) // engine value -> first candy declaring it
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			if e := layer.Engine(); e != "" {
				if _, exists := engineSources[e]; !exists {
					engineSources[e] = candyName
				}
			}
		}
		if len(engineSources) > 1 {
			var conflicts []string
			for e, l := range engineSources {
				conflicts = append(conflicts, fmt.Sprintf("%s (from candy %s)", e, l))
			}
			sort.Strings(conflicts)
			errs.Add("box %q: conflicting engine requirements: %s", boxName, strings.Join(conflicts, ", "))
		}
	}
}

// validatePortRelay validates port_relay declarations in candies
func validatePortRelay(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.PortRelayPorts) == 0 {
			continue
		}
		// Validate each port
		portSet := make(map[int]bool)
		for _, port := range layer.PortRelayPorts {
			if port < 1 || port > 65535 {
				errs.Add("candy %q port_relay: %d is not a valid port number (1-65535)", name, port)
			}
			if portSet[port] {
				errs.Add("candy %q port_relay: duplicate port %d", name, port)
			}
			portSet[port] = true
		}

		// Warn if relay port isn't declared in the candy's ports
		if layer.HasPorts() {
			candyPorts := make(map[int]bool)
			for _, ps := range layer.PortSpecs() {
				candyPorts[ps.Port] = true
			}
			for _, port := range layer.PortRelayPorts {
				if !candyPorts[port] {
					errs.Add("candy %q port_relay: port %d is not declared in the candy's ports", name, port)
				}
			}
		} else {
			errs.Add("candy %q port_relay: candy has no ports declared", name)
		}
	}

	// Validate that images with port_relay candies include the socat candy
	for boxName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}
		hasRelay := false
		hasSocat := false
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			if len(layer.PortRelayPorts) > 0 {
				hasRelay = true
			}
			if layer.Name == "socat" {
				hasSocat = true
			}
		}
		if hasRelay && !hasSocat {
			errs.Add("box %q: has port_relay candies but missing \"socat\" candy (add it to the box candies or as a dependency)", boxName)
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

// validStatuses lists the allowed status values (empty string also accepted as "testing").
var validStatuses = map[string]bool{
	"":        true,
	"working": true,
	"testing": true,
	"broken":  true,
}

// calverRe matches CalVer format: YYYY.DDD.HHMM (3 dot-separated non-negative integers)
var calverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// validateStatus validates status tags in description.tag for candies
// and images. Accepts empty (defaults to testing) plus working/testing/
// broken. The status word is one of many possible Description.Tag
// entries — others are free-form and not policed here.
func validateStatus(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if layer.Description == nil {
			continue
		}
		for _, t := range layer.Description.Tag {
			if (t == "working" || t == "testing" || t == "broken") && !validStatuses[t] {
				errs.Add("candy %q: description.tag includes unknown status %q", name, t)
			}
		}
	}
	for name, img := range cfg.Box {
		if !img.IsEnabled() || img.Description == nil {
			continue
		}
		for _, t := range img.Description.Tag {
			if (t == "working" || t == "testing" || t == "broken") && !validStatuses[t] {
				errs.Add("box %q: description.tag includes unknown status %q", name, t)
			}
		}
	}
}

// validateVersionFields validates version fields in candies and images.
func validateVersionFields(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if layer.Version != "" && !calverRe.MatchString(layer.Version) {
			errs.Add("candy %q: version must be CalVer format (YYYY.DDD.HHMM), got %q", name, layer.Version)
		}
	}
	for name, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}
		if img.Version != "" && !calverRe.MatchString(img.Version) {
			errs.Add("box %q: version must be CalVer format (YYYY.DDD.HHMM), got %q", name, img.Version)
		}
	}
}

// candyHasFile checks if a candy has a specific file (used for builder detection).
func candyHasFile(layer *Candy, filename string) bool {
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
		return fileExists(filepath.Join(layer.SourceDir, filename))
	}
}

// candyHasFormatConfig checks if a candy has a non-empty config section for a format.
// Fully generic — uses the FormatSection accessor which checks both typed and dynamic sections.
func candyHasFormatConfig(layer *Candy, formatName string) bool {
	section := layer.FormatSection(formatName)
	return section != nil && len(section.Packages) > 0
}

// validateDataCandies checks data candy declarations and data image constraints.
func validateDataCandies(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate data src directories exist
	for name, layer := range layers {
		if !layer.HasData() {
			continue
		}
		for _, d := range layer.Data() {
			srcPath := filepath.Join(layer.SourceDir, d.Src)
			if !dirExists(srcPath) {
				errs.Add("candy %s: data src %q does not exist or is not a directory", name, d.Src)
			}
		}
	}

	// Validate per-image constraints
	for imgName, img := range cfg.Box {
		if !img.IsEnabled() {
			continue
		}

		// Resolve candies for this image
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue // candy resolution errors are caught elsewhere
		}

		// Collect all volume names declared in this image's candy chain
		volumeNames := make(map[string]bool)
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			for _, v := range layer.Volume() {
				volumeNames[v.Name] = true
			}
		}

		// Check that data volume references resolve
		hasData := false
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok || !layer.HasData() {
				continue
			}
			hasData = true
			for _, d := range layer.Data() {
				if !volumeNames[d.Volume] {
					errs.Add("box %s: candy %s data references volume %q which is not declared by any candy in the box", imgName, candyName, d.Volume)
				}
			}
		}

		// Data image specific validations
		if img.DataImage {
			if img.Base != "" {
				errs.Add("box %s: data_image cannot specify base (always FROM scratch)", imgName)
			}
			if !hasData {
				errs.Add("box %s: data_image has no candies with data declarations", imgName)
			}
			// Check for incompatible features
			for _, candyName := range resolved {
				layer, ok := layers[candyName]
				if !ok {
					continue
				}
				if layer.HasService() {
					errs.Add("box %s: data_image includes candy %s which has service: declarations", imgName, candyName)
				}
				if layer.HasPorts() {
					errs.Add("box %s: data_image includes candy %s which has port declarations", imgName, candyName)
				}
				if len(layer.PortRelayPorts) > 0 {
					errs.Add("box %s: data_image includes candy %s which has port_relay declarations", imgName, candyName)
				}
			}
		}
	}
}

// validateEnvProvides checks env_provides declarations in candies.
func validateEnvProvides(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasEnvProvides() {
			continue
		}
		for key, tmpl := range layer.EnvProvides() {
			if key == "" {
				errs.Add("candy %s: env_provides has empty key", name)
				continue
			}

			// Check for valid template variables. Allowed: {{.ContainerName}},
			// {{.HostPort <N>}}, {{.ContainerPort <N>}} (where N is a numeric
			// container port). Routes through validateProvidesTemplate so this
			// validator and the runtime resolver agree on the allowlist.
			if !validateProvidesTemplate(tmpl) {
				errs.Add("candy %s: env_provides[%s] contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, key, tmpl)
			}

			// Note: env_provides key may intentionally overlap with env key in the same candy.
			// env is baked into the service's own image (e.g., OLLAMA_HOST="0.0.0.0" for binding).
			// env_provides is injected into OTHER containers (e.g., OLLAMA_HOST="http://charly-ollama:11434").
		}
	}
}

// validateEnvDeps checks env_requires, env_accepts, secret_requires, and
// secret_accepts declarations in candies. A single `seen` map enforces the rule
// that an env var name cannot appear in more than one of these four sections
// within the same candy — each entry is either a plaintext-safe accept/require
// or a credential-backed accept/require, never both.
func validateEnvDeps(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		seen := make(map[string]string) // name -> originating section label

		validateDepEntries(name, "env_requires", layer.EnvRequire(), seen, errs)
		validateDepEntries(name, "env_accepts", layer.EnvAccept(), seen, errs)
		validateDepEntries(name, "secret_requires", layer.SecretRequire(), seen, errs)
		validateDepEntries(name, "secret_accepts", layer.SecretAccept(), seen, errs)
	}
}

// validateDepEntries validates a single env/secret dependency list against the
// shared `seen` map, reporting collisions with whichever section claimed the
// name first. Used by validateEnvDeps for all four env/secret sections.
func validateDepEntries(candyName, section string, entries []EnvDependency, seen map[string]string, errs *ValidationError) {
	for _, dep := range entries {
		if dep.Name == "" {
			errs.Add("candy %s: %s has entry with empty name", candyName, section)
			continue
		}
		if !isValidEnvVarName(dep.Name) {
			errs.Add("candy %s: %s[%s] is not a valid environment variable name", candyName, section, dep.Name)
		}
		if dep.Description == "" {
			errs.Add("candy %s: %s[%s] has no description", candyName, section, dep.Name)
		}
		if prev, ok := seen[dep.Name]; ok && prev != section {
			errs.Add("candy %s: env var %s appears in both %s and %s — an env var belongs to exactly one section", candyName, dep.Name, prev, section)
		}
		seen[dep.Name] = section
	}
}

// secretKeyPattern matches the optional Key field on secret_accepts /
// secret_requires entries. Enforces <service>/<key> with an "charly/" prefix to
// prevent candies from exfiltrating unrelated user credentials (e.g.,
// "aws/access-key") into a podman secret. Plan §2.7 / §4.4 rule 5.
var secretKeyPattern = regexp.MustCompile(`^charly/[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9_-]*$`)

// podmanSecretSlugPattern matches the lowercase-kebab slug form used for
// per-image podman secret names (charly-<image>-<slug>). Plan §4.4 rule 4.
var podmanSecretSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// envVarNameToPodmanSecretSlug converts an env var name to the slug used in
// the podman secret name. Mirrors what CollectCandySecretAccepts will do at
// `charly config` time (to be added in Step 4). Lowercase + underscores → hyphens.
func envVarNameToPodmanSecretSlug(envVarName string) string {
	return strings.ReplaceAll(strings.ToLower(envVarName), "_", "-")
}

// validateSecretDeps enforces the secret-specific rules that validateEnvDeps
// cannot express: credential store `key:` format, podman secret slug validity,
// and the prohibition on credential-backed entries overlapping with the
// plaintext `env_provides` map. Plan §4.4.
func validateSecretDeps(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasSecretAccepts() && !layer.HasSecretRequires() {
			continue
		}

		// Build a set of env_provides keys for the collision check (rule 2).
		// env_provides values use {{.ContainerName}} templating and land in
		// charly.yml plaintext — a credential-backed env var with the same
		// name would be ambiguous and defeat the point of the split.
		envProvidesKeys := map[string]bool{}
		for key := range layer.EnvProvides() {
			envProvidesKeys[key] = true
		}

		checkOne := func(section string, entries []EnvDependency) {
			for _, dep := range entries {
				if dep.Name == "" {
					continue // already reported by validateEnvDeps
				}
				// Rule 2: cannot collide with env_provides.
				if envProvidesKeys[dep.Name] {
					errs.Add("candy %s: %s[%s] also appears in env_provides — credential-backed secrets and plaintext env_provides are mutually exclusive for the same variable", name, section, dep.Name)
				}
				// Rule 4: the podman secret slug must be valid.
				slug := envVarNameToPodmanSecretSlug(dep.Name)
				if !podmanSecretSlugPattern.MatchString(slug) {
					errs.Add("candy %s: %s[%s] would produce invalid podman secret slug %q (must match %s)", name, section, dep.Name, slug, podmanSecretSlugPattern.String())
				}
				// Rule 5: optional Key override must match <service>/<key>
				// with an charly/ prefix.
				if dep.Key != "" && !secretKeyPattern.MatchString(dep.Key) {
					errs.Add("candy %s: %s[%s] key %q must match %s — must start with \"charly/\" and be <service>/<key>", name, section, dep.Name, dep.Key, secretKeyPattern.String())
				}
			}
		}

		checkOne("secret_requires", layer.SecretRequire())
		checkOne("secret_accepts", layer.SecretAccept())
	}
}

// validateMCPProvides checks mcp_provides declarations in candies.
func validateMCPProvides(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasMCPProvides() {
			continue
		}
		seen := make(map[string]bool)
		for _, mcp := range layer.MCPProvide() {
			if mcp.Name == "" {
				errs.Add("candy %s: mcp_provides has entry with empty name", name)
				continue
			}
			if seen[mcp.Name] {
				errs.Add("candy %s: mcp_provides has duplicate name %q", name, mcp.Name)
			}
			seen[mcp.Name] = true

			if mcp.URL == "" {
				errs.Add("candy %s: mcp_provides[%s] has empty url", name, mcp.Name)
				continue
			}

			// Check for valid template variables. Allowed: {{.ContainerName}},
			// {{.HostPort <N>}}, {{.ContainerPort <N>}}.
			if !validateProvidesTemplate(mcp.URL) {
				errs.Add("candy %s: mcp_provides[%s] url contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, mcp.Name, mcp.URL)
			}

			// Validate transport if specified
			if mcp.Transport != "" && mcp.Transport != "http" && mcp.Transport != "sse" {
				errs.Add("candy %s: mcp_provides[%s] has invalid transport %q (must be http, sse, or empty)", name, mcp.Name, mcp.Transport)
			}
		}
	}
}

// validateMCPDeps checks mcp_requires and mcp_accepts declarations in candies.
func validateMCPDeps(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		seen := make(map[string]string) // name -> "requires" or "accepts"

		for _, dep := range layer.MCPRequire() {
			if dep.Name == "" {
				errs.Add("candy %s: mcp_requires has entry with empty name", name)
				continue
			}
			if dep.Description == "" {
				errs.Add("candy %s: mcp_requires[%s] has no description", name, dep.Name)
			}
			if prev, ok := seen[dep.Name]; ok {
				errs.Add("candy %s: MCP server %s appears in both mcp_%s and mcp_requires", name, dep.Name, prev)
			}
			seen[dep.Name] = "requires"
		}

		for _, dep := range layer.MCPAccept() {
			if dep.Name == "" {
				errs.Add("candy %s: mcp_accepts has entry with empty name", name)
				continue
			}
			if dep.Description == "" {
				errs.Add("candy %s: mcp_accepts[%s] has no description", name, dep.Name)
			}
			if prev, ok := seen[dep.Name]; ok {
				errs.Add("candy %s: MCP server %s appears in both mcp_%s and mcp_accepts", name, dep.Name, prev)
			}
			seen[dep.Name] = "accepts"
		}
	}
}

// isValidEnvVarName checks if s is a valid environment variable name (uppercase alphanumeric + underscore, not starting with digit).
func isValidEnvVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, c := range s {
		if c >= 'A' && c <= 'Z' || c == '_' {
			continue
		}
		if c >= '0' && c <= '9' && i > 0 {
			continue
		}
		// Allow lowercase too — some env vars use mixed case
		if c >= 'a' && c <= 'z' {
			continue
		}
		return false
	}
	return true
}

// --- Task validation (replaces root.yml / user.yml) ---

var (
	taskModePattern        = regexp.MustCompile(`^0[0-7]{3,4}$`)
	taskVarKeyPattern      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	taskUserLiteralPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	taskUserUIDGIDPattern  = regexp.MustCompile(`^\d+:\d+$`)
	taskCapsPattern        = regexp.MustCompile(`^cap_[a-z_]+[=+][a-z]+(,cap_[a-z_]+[=+][a-z]+)*$`)
	taskExtractValid       = map[string]bool{
		"":        true, // auto-detect
		"tar.gz":  true,
		"tar.xz":  true,
		"tar.zst": true,
		"zip":     true,
		"none":    true,
		"sh":      true,
	}
)

// validateCandyTasks enforces the tasks: schema:
//   - exactly-one-verb per task
//   - per-verb required modifier presence
//   - path / mode / caps format checks
//   - vars: key rules (pattern, no auto-export collision, no env: collision)
//   - ${VAR} references resolve against vars ∪ auto-exports
//   - dual-config: tasks: AND root.yml/user.yml in same candy → error
//   - build: value restricted to "all" in initial implementation
func validateCandyTasks(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		// vars: validation
		for k, v := range layer.vars {
			if !taskVarKeyPattern.MatchString(k) {
				errs.Add("candy %q: vars: key %q is not a valid shell identifier (expected ^[A-Z_][A-Z0-9_]*$)", name, k)
			}
			if taskAutoExports[k] {
				errs.Add("candy %q: vars: key %q collides with a reserved auto-export (USER, UID, GID, HOME, ARCH, BUILD_ARCH)", name, k)
			}
			if layer.envConfig != nil {
				if _, exists := layer.envConfig.Vars[k]; exists {
					errs.Add("candy %q: vars: key %q also declared in env: — pick one", name, k)
				}
			}
			_ = v // value is free-form; no further pattern enforced
		}

		if !layer.HasTasks() {
			continue
		}

		known := taskKnownNames(layer.vars)
		for i, t := range layer.tasks {
			// Exactly-one-verb
			verb, err := t.Kind()
			if err != nil {
				errs.Add("candy %q: tasks[%d]: %v", name, i, err)
				continue
			}

			validateSingleTask(name, i, verb, &t, known, errs)
		}
	}
}

// validateSingleTask runs per-verb modifier and field validation for a single
// task. Errors accumulate in errs. known is the set of ${VAR} names that
// resolve (auto-exports ∪ candy.Vars keys).
func validateSingleTask(candyName string, idx int, verb string, t *Task, known map[string]bool, errs *ValidationError) {
	// user: format check
	if t.User != "" {
		u := t.User
		if !isValidTaskUser(u) {
			errs.Add("candy %q: tasks[%d]: user: %q is not valid (expected root, ${USER}, a name matching ^[a-z_][a-z0-9_-]*$, or <uid>:<gid>)", candyName, idx, u)
		}
	}

	// mode: format check (applies to mkdir/copy/write/download)
	if t.Mode != "" && !taskModePattern.MatchString(t.Mode) {
		errs.Add("candy %q: tasks[%d]: mode: %q is not valid octal (expected ^0[0-7]{3,4}$)", candyName, idx, t.Mode)
	}

	// cache: additional buildkit cache mounts — only meaningful on the
	// RUN-emitting verbs (cmd/download); each path must be absolute (or
	// ~/ / ${HOME}, which resolve to absolute mount points).
	if len(t.Cache) > 0 {
		if verb != "cmd" && verb != "download" {
			errs.Add("candy %q: tasks[%d]: cache: is only valid on cmd: or download: tasks (got %s)", candyName, idx, verb)
		}
		for _, p := range t.Cache {
			if !isAbsOrHomePath(p) {
				errs.Add("candy %q: tasks[%d]: cache: %q must be an absolute path (or start with ~/ / ${HOME})", candyName, idx, p)
			}
		}
	}

	// Per-verb required modifiers
	switch verb {
	case "cmd":
		if strings.TrimSpace(t.Cmd) == "" {
			errs.Add("candy %q: tasks[%d]: cmd: must be non-empty", candyName, idx)
		}

	case "mkdir":
		if !isAbsOrHomePath(t.Mkdir) {
			errs.Add("candy %q: tasks[%d]: mkdir: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Mkdir)
		}

	case "copy":
		if t.Copy == "" {
			errs.Add("candy %q: tasks[%d]: copy: requires a non-empty source", candyName, idx)
		} else if strings.HasPrefix(t.Copy, "/") {
			errs.Add("candy %q: tasks[%d]: copy: %q must be a relative path (candy-dir file)", candyName, idx, t.Copy)
		} else if strings.Contains(t.Copy, "..") {
			errs.Add("candy %q: tasks[%d]: copy: %q may not contain .. (no traversal)", candyName, idx, t.Copy)
		}
		if t.To == "" {
			errs.Add("candy %q: tasks[%d]: copy: requires to: destination", candyName, idx)
		} else if !isAbsOrHomePath(t.To) {
			errs.Add("candy %q: tasks[%d]: copy to: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.To)
		}

	case "write":
		if !isAbsOrHomePath(t.Write) {
			errs.Add("candy %q: tasks[%d]: write: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Write)
		}
		if t.Content == "" {
			errs.Add("candy %q: tasks[%d]: write: requires non-empty content:", candyName, idx)
		}

	case "link":
		if !isAbsOrHomePath(t.Link) {
			errs.Add("candy %q: tasks[%d]: link: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Link)
		}
		if t.Target == "" {
			errs.Add("candy %q: tasks[%d]: link: requires target: (what the symlink points to)", candyName, idx)
		}

	case "download":
		if t.Download == "" {
			errs.Add("candy %q: tasks[%d]: download: requires a URL", candyName, idx)
		}
		if !taskExtractValid[t.Extract] {
			errs.Add("candy %q: tasks[%d]: download extract: %q not valid (expected one of tar.gz, tar.xz, tar.zst, zip, none, sh)", candyName, idx, t.Extract)
		}
		// to: required unless extract=sh (script typically decides own install path)
		if t.Extract != "sh" && t.To == "" {
			errs.Add("candy %q: tasks[%d]: download requires to: destination (unless extract: sh)", candyName, idx)
		}

	case "setcap":
		if !strings.HasPrefix(t.Setcap, "/") {
			errs.Add("candy %q: tasks[%d]: setcap: %q must be an absolute path", candyName, idx, t.Setcap)
		}
		if t.Caps != "" && !taskCapsPattern.MatchString(t.Caps) {
			errs.Add("candy %q: tasks[%d]: setcap caps: %q not valid (expected cap_name=flags[,cap_name=flags])", candyName, idx, t.Caps)
		}

	case "build":
		if t.Build != "all" {
			errs.Add("candy %q: tasks[%d]: build: %q not supported (initial implementation accepts only \"all\")", candyName, idx, t.Build)
		}
	}

	// ${VAR} reference validation in all non-shell fields.
	// cmd: and write: content are passed verbatim to shell / filesystem,
	// so unresolved ${BAR} is legal there (shell handles, or literal bytes).
	nonShellFields := map[string]string{
		"mkdir":    t.Mkdir,
		"copy":     t.Copy,
		"write":    t.Write,
		"link":     t.Link,
		"target":   t.Target,
		"to":       t.To,
		"download": t.Download,
		"setcap":   t.Setcap,
	}
	for field, val := range nonShellFields {
		if val == "" {
			continue
		}
		if unresolved := taskUnresolvedRefs(val, known); len(unresolved) > 0 {
			errs.Add("candy %q: tasks[%d]: %s references unknown ${VAR}: %s (declare in vars: or use an auto-export)", candyName, idx, field, strings.Join(unresolved, ", "))
		}
	}
}

// isValidTaskUser returns true for accepted user: values: "root", "${USER}",
// a name matching lowercase-alphanum-hyphen, a numeric "<uid>:<gid>", or a
// string containing ${VAR} references (which resolve at generate time).
func isValidTaskUser(u string) bool {
	if u == "root" || u == "${USER}" || u == "${UID}:${GID}" {
		return true
	}
	if taskUserUIDGIDPattern.MatchString(u) {
		return true
	}
	if taskUserLiteralPattern.MatchString(u) {
		return true
	}
	// Allow bare numeric uid
	if _, err := strconv.Atoi(u); err == nil {
		return true
	}
	return false
}

// isAbsOrHomePath returns true for absolute paths or paths starting with
// ~/ or ${HOME}/. Empty paths return false (required-field check).
func isAbsOrHomePath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return true
	}
	if strings.HasPrefix(p, "~/") {
		return true
	}
	if strings.HasPrefix(p, "${HOME}") {
		return true
	}
	return false
}
