package main

// install_build.go — the InstallPlan compiler.
//
// BuildDeployPlan walks a resolved image plus its layer set and produces
// an InstallPlan (ov/install_plan.go) — the IR that both the OCI target
// (Containerfile emission) and the host target (shell + podman execution)
// consume.
//
// This function is intentionally pure: given the same inputs, it produces
// the same InstallPlan regardless of filesystem or environment. Side
// effects happen later, inside DeployTarget.Emit implementations.
//
// Logic here replaces the per-layer walk inside writeLayerSteps
// (generate.go:1075-1208). Instead of emitting Containerfile text
// directly, we emit structured InstallSteps that know *what* to do —
// leaving *how* to render up to each target.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

// HostContext carries host-side information the compiler needs to decide
// (a) which format section to pick for the host target, (b) which
// builder image to run user-scope builders in, (c) whether to gate
// AUR-specific steps, etc. For the OCI target, the caller passes a
// zero-value HostContext (the compiler ignores host-only choices when
// Target is "oci").
type HostContext struct {
	// Target selects compilation mode. "" or "oci" means "compile for
	// container build"; "host" means "compile for direct host execution".
	// Primarily affects which steps get VenueSkip (container-only fields
	// like ports:/volumes: are skipped on "host").
	Target string

	// Distro is the resolved host distro tag, e.g. "fedora:43". Used to
	// pick the right format section when compiling for a host target
	// whose distro differs from the image's primary distro.
	Distro string

	// GlibcVersion is the host's glibc major.minor as reported by
	// `ldd --version`. Used by the host target's preflight check against
	// the selected builder image. Optional; empty means skip the check.
	GlibcVersion string

	// BuilderImage overrides the default builder-image selection for
	// VenueContainerBuilder steps. Populated from --builder-image. ""
	// means "use build.yml default".
	BuilderImage string
}

// BuildDeployPlan compiles one Layer into an InstallPlan.
//
// For whole-image deploys, the caller iterates the ordered layer list
// (from ResolveLayerOrder) and builds one plan per layer, then merges
// them. Keeping one plan per layer makes refcounting trivial in the
// ledger: each layer's teardown is independent.
func BuildDeployPlan(layer *Layer, img *ResolvedImage, hostCtx HostContext) (*InstallPlan, error) {
	if layer == nil {
		return nil, fmt.Errorf("BuildDeployPlan: nil layer")
	}
	if img == nil {
		return nil, fmt.Errorf("BuildDeployPlan: nil image %q", layer.Name)
	}

	plan := &InstallPlan{
		Image:          img.Name,
		Version:        layer.Version,
		Distro:         primaryDistroTag(img, hostCtx),
		Layer:          layer.Name,
		LayersIncluded: []string{layer.Name},
	}

	// 0. Shell-env hook: vars + env + path_append — applies regardless of
	// target. OCI target will emit as ENV directives; host target as
	// env.d file + managed-block PATH.
	if hook := compileShellHookStep(layer, img); hook != nil {
		plan.Steps = append(plan.Steps, hook)
	}

	// 1. System packages — distro tag wins over build format sections.
	// When compiling for the host target, pick the format matching the
	// host distro instead of the image's primary format.
	pkgSteps := compileSystemPackageSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, pkgSteps...)

	// 2. Inline tasks (cmd / mkdir / copy / write / link / download / setcap).
	taskSteps := compileTaskSteps(layer, img)
	plan.Steps = append(plan.Steps, taskSteps...)

	// 3. Multi-stage builders triggered by layer manifest files.
	builderSteps := compileBuilderSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, builderSteps...)

	// 4. Services: both legacy `service:` (supervisord INI fragments) and
	// `system_services:` (systemd unit names). After the Task 6 migration
	// lands these both flow through a unified `services:` schema; for now
	// we read the two fields independently.
	svcSteps := compileServiceSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, svcSteps...)

	// 5. Shell-init snippets: layer.yml `shell:` block. Generic body +
	// per-shell sub-blocks (bash/zsh/fish/sh). Selection rule: per-shell
	// wins over generic with ${SHELL_NAME} substitution.
	shellSteps := compileShellSnippetSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, shellSteps...)

	return plan, nil
}

// MergePlan combines a list of per-layer plans into one whole-image
// plan. Used by deploy targets that want to see all steps in one
// sequence (for sudo batching, overall dry-run display, etc.) while
// preserving per-layer provenance for the ledger.
func MergePlan(plans []*InstallPlan, image string, addLayers []string) *InstallPlan {
	out := &InstallPlan{
		Image:     image,
		AddLayers: append([]string(nil), addLayers...),
	}
	if len(plans) == 0 {
		return out
	}
	out.Distro = plans[0].Distro
	for _, p := range plans {
		if p == nil {
			continue
		}
		out.Steps = append(out.Steps, p.Steps...)
		out.LayersIncluded = append(out.LayersIncluded, p.Layer)
	}
	out.DeployID = computeDeployID(image, out.LayersIncluded, addLayers)
	return out
}

// computeDeployID returns a deterministic hex hash that identifies a
// specific deploy (image + ordered layer set + add_layers). Used as the
// ledger key so re-deploys of the same config are recognizable and
// layer-refcount bookkeeping is stable.
func computeDeployID(image string, layers, addLayers []string) string {
	h := sha256.New()
	h.Write([]byte(image))
	h.Write([]byte{0})
	for _, l := range layers {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	h.Write([]byte{1})
	for _, l := range addLayers {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// primaryDistroTag picks the distro tag this plan is materialized against.
// For host compilation we use the host's detected distro; otherwise we
// fall back to the image's first distro entry.
func primaryDistroTag(img *ResolvedImage, hostCtx HostContext) string {
	if hostCtx.Target == "host" && hostCtx.Distro != "" {
		return hostCtx.Distro
	}
	if len(img.Distro) > 0 {
		return img.Distro[0]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Shell hook compilation
// ---------------------------------------------------------------------------

// compileShellHookStep returns a ShellHookStep for the layer's env: and
// path_append: fields, or nil if the layer contributes neither. Path
// entries are {{.Home}}-substituted using the image's resolved Home so
// targets can emit them literally.
func compileShellHookStep(layer *Layer, img *ResolvedImage) *ShellHookStep {
	envCfg, _ := layer.EnvConfig()
	if envCfg == nil {
		return nil
	}
	if len(envCfg.Vars) == 0 && len(envCfg.PathAppend) == 0 {
		return nil
	}
	vars := make(map[string]string, len(envCfg.Vars))
	for k, v := range envCfg.Vars {
		vars[k] = ExpandPath(v, img.Home)
	}
	paths := make([]string, 0, len(envCfg.PathAppend))
	for _, p := range envCfg.PathAppend {
		paths = append(paths, ExpandPath(p, img.Home))
	}
	return &ShellHookStep{
		LayerName: layer.Name,
		EnvVars:   vars,
		PathAdd:   paths,
	}
}

// ---------------------------------------------------------------------------
// Shell-init snippet compilation
// ---------------------------------------------------------------------------

// compileShellSnippetSteps returns one ShellSnippetStep per (layer, shell)
// pair the layer contributes. Selection rule (mirrors TagPkgConfig):
//   1. If layer.Shell.ByShell[shell] exists, render it.
//   2. Else if layer.Shell.Init is non-empty, render the generic body with
//      ${SHELL_NAME} substituted to the active shell name.
//   3. Else this shell gets nothing from this layer.
//
// path_append entries are rendered into the snippet body using
// shell-appropriate syntax (PATH=... for bash/zsh/sh, fish_add_path for
// fish) before the step is emitted, so each DeployTarget emitter can
// write the snippet bytes verbatim.
//
// Destination resolution is target-aware:
//   - Container build (hostCtx.Target empty / "oci"): system-wide
//     drop-in (/etc/profile.d/ov-<layer>-<shell>.sh, /etc/fish/conf.d/
//     ov-<layer>.fish). UseDropin=true.
//   - target:local, target:vm (hostCtx.Target = "host" or "vm"):
//     bash/zsh/sh → managed-block append in the user's rc file (UseDropin
//     =false); fish → per-layer drop-in in ~/.config/fish/conf.d/
//     (UseDropin=true).
//
// Returns nil when the layer has no shell: block.
func compileShellSnippetSteps(layer *Layer, img *ResolvedImage, hostCtx HostContext) []InstallStep {
	cfg := layer.Shell()
	if cfg == nil {
		return nil
	}
	out := make([]InstallStep, 0, len(ShellAllowlist))
	// Stable iteration: walk the allowlist in fixed order so plan output
	// is deterministic across runs.
	for _, shell := range []string{"bash", "zsh", "fish", "sh"} {
		spec, body, paths, ok := resolveShellSpec(cfg, shell)
		if !ok {
			continue
		}
		dest, useDropin := shellSnippetDestination(layer.Name, shell, hostCtx, img.Home, spec.Path)
		if dest == "" {
			continue
		}
		// Render path_append into the body using shell-appropriate syntax.
		body = appendShellPathLines(body, paths, shell, img.Home)
		if body == "" {
			continue
		}
		out = append(out, &ShellSnippetStep{
			LayerName:   layer.Name,
			Origin:      layer.Name,
			Shell:       shell,
			Snippet:     body,
			PathAppend:  paths,
			Destination: dest,
			Marker:      layer.Name,
			UseDropin:   useDropin,
			Priority:    cfg.Priority,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveShellSpec applies the per-shell-wins-over-generic selection rule.
// Returns the resolved spec, the rendered init body (with ${SHELL_NAME}
// substituted for the generic case), the path_append slice, and whether
// the shell has anything to contribute. Generic init wins when no
// per-shell override exists; per-shell wins otherwise.
func resolveShellSpec(cfg *ShellConfig, shell string) (*ShellSpec, string, []string, bool) {
	if cfg == nil {
		return nil, "", nil, false
	}
	if spec, ok := cfg.ByShell[shell]; ok && spec != nil {
		// Per-shell override. We do NOT substitute ${SHELL_NAME} here —
		// the override is meant to be shell-specific, so the author wrote
		// the literal shell syntax already.
		body := spec.Init
		paths := append([]string(nil), spec.PathAppend...)
		if body == "" && len(paths) == 0 {
			return spec, "", nil, false
		}
		return spec, body, paths, true
	}
	// Fall back to generic body. Substitute ${SHELL_NAME}.
	if cfg.Init == "" && len(cfg.PathAppend) == 0 {
		return nil, "", nil, false
	}
	body := strings.ReplaceAll(cfg.Init, "${SHELL_NAME}", shell)
	paths := append([]string(nil), cfg.PathAppend...)
	// Synthesize a spec view for the caller (carries Path override if set
	// at the generic level).
	view := &ShellSpec{Init: body, PathAppend: paths, Path: cfg.Path}
	return view, body, paths, true
}

// shellSnippetDestination resolves the destination file path for a layer's
// snippet given the shell and target. When pathOverride is non-empty, it
// takes precedence (with ~/ expansion via ExpandPath). Returns the
// resolved path and a UseDropin discriminator (true: full-file write;
// false: managed-block append).
func shellSnippetDestination(layerName, shell string, hostCtx HostContext, home, pathOverride string) (string, bool) {
	isHost := hostCtx.Target == "host" || hostCtx.Target == "vm"
	if pathOverride != "" {
		expanded := ExpandPath(pathOverride, home)
		// Author override implies they know what they want; fish + drop-in
		// only differ from rc-append by where the path points, so treat
		// any override as drop-in (whole-file write) for predictability.
		return expanded, true
	}
	if isHost {
		switch shell {
		case "bash":
			return ExpandPath("~/.bashrc", home), false
		case "zsh":
			return ExpandPath("~/.zshrc", home), false
		case "sh":
			return ExpandPath("~/.profile", home), false
		case "fish":
			return ExpandPath(fmt.Sprintf("~/.config/fish/conf.d/ov-%s.fish", layerName), home), true
		}
		return "", false
	}
	// Container build (oci/empty target): system-wide drop-in files.
	switch shell {
	case "bash", "zsh", "sh":
		return fmt.Sprintf("/etc/profile.d/ov-%s-%s.sh", layerName, shell), true
	case "fish":
		return fmt.Sprintf("/etc/fish/conf.d/ov-%s.fish", layerName), true
	}
	return "", false
}

// appendShellPathLines appends path_append entries to the snippet body
// using shell-appropriate syntax. bash/zsh/sh use POSIX `PATH=$PATH:X`
// exports; fish uses `fish_add_path X`. Idempotent: if body already ends
// with a newline, no extra blank line is inserted.
func appendShellPathLines(body string, paths []string, shell, home string) string {
	if len(paths) == 0 {
		return body
	}
	var sb strings.Builder
	sb.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	for _, p := range paths {
		expanded := ExpandPath(p, home)
		switch shell {
		case "fish":
			sb.WriteString(fmt.Sprintf("fish_add_path -gP %s\n", shellQuote(expanded)))
		default:
			sb.WriteString(fmt.Sprintf("export PATH=\"$PATH:%s\"\n", expanded))
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// System package compilation
// ---------------------------------------------------------------------------

// compileSystemPackageSteps emits SystemPackagesStep(s) for the layer's
// package sections, honoring the distro-tag-wins-over-build-format rule
// from today's writeLayerSteps.
//
// Phase 1: walk img.Distro tags in order; first match wins. If a tag
// section is found, emit ONE install step using that section and stop.
//
// Phase 2: if no tag matched, walk img.BuildFormats in order; emit one
// install step per format section that has packages. (Today's
// writeLayerSteps does this to support images that install both rpm and
// aur packages, for example.)
//
// For the IR we additionally break each install into the three-phase
// structure (prepare/install/cleanup) so the host target can gate
// PhasePrepare on --allow-repo-changes. Current build.yml only has one
// phase per format (the monolithic install_template); Task 4 will split
// templates into phases. Until then, we emit everything as PhaseInstall.
func compileSystemPackageSteps(layer *Layer, img *ResolvedImage, hostCtx HostContext) []InstallStep {
	if img.DistroDef == nil {
		return nil
	}

	// Phase 1: distro tag section
	for _, tag := range img.Distro {
		tagCfg := layer.TagSection(tag)
		if tagCfg == nil || len(tagCfg.Package) == 0 {
			continue
		}
		formatDef := img.DistroDef.Format[img.Pkg]
		if formatDef == nil {
			return nil
		}
		return []InstallStep{buildSystemPackagesStep(img.Pkg, PhaseInstall, tagCfg.Package, tagCfg.Raw, formatDef.CacheMount)}
	}

	// Phase 2: format sections in build_formats order
	var steps []InstallStep
	for _, format := range img.BuildFormats {
		section := layer.FormatSection(format)
		if section == nil || len(section.Packages) == 0 {
			continue
		}
		formatDef := img.DistroDef.Format[format]
		if formatDef == nil {
			continue
		}
		steps = append(steps, buildSystemPackagesStep(format, PhaseInstall, section.Packages, section.Raw, formatDef.CacheMount))
	}
	return steps
}

// buildSystemPackagesStep constructs a SystemPackagesStep from a
// PackageSection's Raw map, extracting the well-known structured fields
// (repos, options, copr, modules, exclude, keys) for gate evaluation
// while also preserving the full Raw map for template rendering later.
func buildSystemPackagesStep(format string, phase Phase, packages []string, raw map[string]interface{}, cacheMounts []CacheMountDef) *SystemPackagesStep {
	step := &SystemPackagesStep{
		Format:            format,
		Phase:             phase,
		Packages:          append([]string(nil), packages...),
		Options:           extractStringSlice(raw, "options"),
		Copr:              extractStringSlice(raw, "copr"),
		Modules:           extractStringSlice(raw, "modules"),
		Exclude:           extractStringSlice(raw, "exclude"),
		Keys:              extractStringSlice(raw, "keys"),
		RawInstallContext: raw,
	}
	if repoList, ok := raw["repos"].([]interface{}); ok {
		for _, r := range repoList {
			if m, ok := r.(map[string]interface{}); ok {
				step.Repos = append(step.Repos, RepoSpec{Raw: m})
			}
		}
	}
	for _, cm := range cacheMounts {
		step.CacheMount = append(step.CacheMount, CacheMountSpec{Dst: cm.Dst, Sharing: cm.Sharing})
	}
	return step
}

// ---------------------------------------------------------------------------
// Task compilation
// ---------------------------------------------------------------------------

// compileTaskSteps turns the layer's tasks: list into TaskSteps. The
// resolved user is captured at compile time so the host target doesn't
// need to re-resolve ${USER} later; CtxPath carries the layer's absolute
// directory for /ctx/ substitution on the host.
func compileTaskSteps(layer *Layer, img *ResolvedImage) []InstallStep {
	if !layer.HasTasks || len(layer.tasks) == 0 {
		return nil
	}
	out := make([]InstallStep, 0, len(layer.tasks))
	for i := range layer.tasks {
		task := &layer.tasks[i]
		userDir, _ := resolveUserSpec(task.User, img)
		// Snapshot layer.vars per task so the host/local-deploy renderer
		// can emit `export K=V` lines. Build-time gets these via
		// Containerfile ENV (emitVarsEnv); deploy-time has no such
		// mechanism, and references like ${K3D_VERSION} in a download
		// URL would otherwise expand to empty.
		var layerVars map[string]string
		if len(layer.vars) > 0 {
			layerVars = make(map[string]string, len(layer.vars))
			for k, v := range layer.vars {
				layerVars[k] = v
			}
		}
		out = append(out, &TaskStep{
			Task:         task,
			LayerName:    layer.Name,
			LayerDir:     layer.SourceDir,
			CtxPath:      layer.SourceDir,
			ResolvedUser: userDir,
			LayerVars:    layerVars,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Builder compilation
// ---------------------------------------------------------------------------

// compileBuilderSteps emits one BuilderStep per triggered multi-stage or
// inline builder. Detection matches today's Generator.layerNeedsBuilder
// logic: DetectFiles (pixi/npm/cargo) and DetectConfig (aur).
func compileBuilderSteps(layer *Layer, img *ResolvedImage, hostCtx HostContext) []InstallStep {
	if img.BuilderConfig == nil {
		return nil
	}
	// Deterministic builder iteration order — matches today's BuilderNames.
	names := img.BuilderConfig.BuilderNames()
	var out []InstallStep
	for _, bName := range names {
		bDef := img.BuilderConfig.Builder[bName]
		if bDef == nil {
			continue
		}
		if !layerNeedsBuilderStep(layer, bDef) {
			continue
		}
		step := &BuilderStep{
			Builder:   bName,
			LayerName: layer.Name,
			LayerDir:  layer.SourceDir,
			Phase:     PhaseInstall,
		}
		step.BuilderImage = resolveBuilderImage(bName, img, hostCtx)
		step.RawStageContext = collectBuilderContext(layer, bName, bDef, img)

		// aur produces .pkg.tar.zst files in the container at /tmp/aur-pkgs/
		// and we need to pull them out on the host target. The container
		// (OCI) target ignores Artifacts — it uses COPY --from directly.
		if bName == "aur" {
			step.Artifacts = []ArtifactRef{{
				ContainerPath: "/tmp/aur-pkgs/",
				HostPath:      "", // host-target populates at emit time (tmpdir)
				Chown:         false,
			}}
		}
		out = append(out, step)
	}
	return out
}

// layerNeedsBuilderStep mirrors Generator.layerNeedsBuilder without
// requiring a Generator receiver — the compiler doesn't have one.
func layerNeedsBuilderStep(layer *Layer, bDef *BuilderDef) bool {
	if bDef == nil {
		return false
	}
	for _, f := range bDef.DetectFiles {
		if layerHasFile(layer, f) {
			return true
		}
	}
	if bDef.DetectConfig != "" {
		section := layer.FormatSection(bDef.DetectConfig)
		if section != nil && len(section.Packages) > 0 {
			return true
		}
	}
	return false
}

// resolveBuilderImage selects the builder image for a given builder name.
// Priority: hostCtx override > image.Builder map > "" (caller must error
// or fall back to a sensible default).
func resolveBuilderImage(name string, img *ResolvedImage, hostCtx HostContext) string {
	if hostCtx.BuilderImage != "" {
		return hostCtx.BuilderImage
	}
	if img.Builder != nil {
		if ref, ok := img.Builder[name]; ok {
			return ref
		}
	}
	return ""
}

// collectBuilderContext extracts the per-builder ledger/teardown context.
// Populates the "env_name"/"binaries"/"packages" keys the BuilderStep's
// Reverse() method reads — best-effort; accurate env/binary detection
// typically happens after the builder runs (binaries come from the
// layer's Cargo.toml [[bin]] section, etc.). For now we capture names
// derivable from layer.yml alone; the host target refines these at
// execution time.
func collectBuilderContext(layer *Layer, builderName string, bDef *BuilderDef, img *ResolvedImage) map[string]interface{} {
	ctx := map[string]interface{}{
		"layer":   layer.Name,
		"builder": builderName,
		"home":    img.Home,
	}
	switch builderName {
	case "pixi":
		// Default pixi env name. A layer with pixi.toml using a
		// [workspace] or [project] name overrides this; the host target
		// can read pixi.toml at install time and amend.
		ctx["env_name"] = pixiDefaultEnvName(layer)
	case "cargo":
		// Cargo binaries are knowable only by reading Cargo.toml's
		// [[bin]] entries. For the skeleton we record the layer name as
		// a placeholder — the host target will read the real names
		// after `cargo install` and update the ledger. Empty list means
		// "best-effort uninstall via cargo uninstall <layer-name>".
	case "npm":
		// npm packages come from package.json dependencies; the host
		// target reads those at install time.
	case "aur":
		if section := layer.FormatSection("aur"); section != nil {
			ctx["packages"] = append([]string(nil), section.Packages...)
			// `replaces:` lists distro-repo packages that conflict with
			// the AUR build artifact and must be removed (`pacman -Rs
			// --noconfirm`) before `pacman -U`. Idempotent — host-side
			// removal silently skips entries that aren't installed.
			if raw, ok := section.Raw["replaces"]; ok {
				if list, ok := stringSliceFromYAML(raw); ok {
					ctx["replaces"] = list
				}
			}
		}
	}
	return ctx
}

// stringSliceFromYAML coerces a YAML-decoded value into []string. The
// decoder produces []interface{} for sequences; we tolerate already-
// stringified slices for callers that pre-process.
func stringSliceFromYAML(v interface{}) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...), true
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out, true
	}
	return nil, false
}

// pixiDefaultEnvName returns the default pixi env name for a layer.
// Pixi uses "default" unless the manifest declares otherwise; we keep it
// simple and let the host target refine at install time.
func pixiDefaultEnvName(layer *Layer) string {
	return "default"
}

// ---------------------------------------------------------------------------
// Service compilation
// ---------------------------------------------------------------------------

// compileServiceSteps turns the layer's service declarations into IR
// steps. Preference order:
//
//  1. Unified `services:` list (Task 6) — preferred when present.
//     Each entry becomes either a ServicePackagedStep (use_packaged:)
//     or a ServiceCustomStep (full spec).
//
// compileServiceSteps — unified schema only. Each ServiceEntry becomes
// either a ServicePackagedStep (use_packaged:) or a ServiceCustomStep
// (custom exec). Legacy fields (raw-INI service:, system_services:) are
// gone — external layers must run `ov migrate unified --rewrite-layers`.
//
// **Init-system polymorphism filter (2026-05).** When a layer declares
// the same service `name:` twice — once with `use_packaged:` and once
// with custom `exec:` (the mixed-entry polymorphism pattern documented
// in CLAUDE.md "Init-system polymorphism via mixed `service:` entries"
// and `/ov-build:layer` "Service Declaration") — the compiler picks ONE
// based on the target's init system:
//
//   - systemd target (host / vm) → emit the packaged form, SKIP the
//     custom-exec sibling (the packaged unit handles the daemon).
//   - supervisord target (oci/pod build) → SKIP the packaged form
//     (supervisord can't consume systemd units), emit the custom form.
//
// For singleton entries (no mixed pair), each entry renders as-is on
// the matching init system; mismatched singletons (e.g. a lone
// `use_packaged:` entry on a supervisord target) are silently skipped.
//
// For systemd targets, the compiler ALSO pre-populates UnitText/UnitPath
// on the ServiceCustomStep by calling RenderService, so executors don't
// need a runtime lazy-render step. This consolidates what used to live
// in three different places (the deleted VmDeployTarget lazy fallback,
// the OCI build's per-entry routing, and the legacy nothing-rendered
// path on LocalDeployTarget) into ONE compile-time filter.
func compileServiceSteps(layer *Layer, img *ResolvedImage, hostCtx HostContext) []InstallStep {
	var out []InstallStep
	initIsSystemd := hostCtx.Target == "host" || hostCtx.Target == "vm"

	// Detect mixed-entry pairs: which names have a use_packaged form?
	namesWithPackaged := map[string]bool{}
	for i := range layer.Service() {
		if layer.Service()[i].IsPackaged() {
			namesWithPackaged[layer.Service()[i].Name] = true
		}
	}

	// Lazy-loaded systemd InitDef + render context — only loaded if the
	// target is systemd AND at least one custom entry needs rendering.
	var systemdDef *InitDef
	var renderCtx ServiceRenderContext
	loadedSystemd := false
	loadSystemd := func() bool {
		if loadedSystemd {
			return systemdDef != nil
		}
		loadedSystemd = true
		dir, err := os.Getwd()
		if err != nil {
			return false
		}
		_, _, initCfg, err := LoadBuildConfigForImage(dir)
		if err != nil || initCfg == nil {
			return false
		}
		def, ok := initCfg.Init["systemd"]
		if !ok || def == nil {
			return false
		}
		systemdDef = def
		renderCtx = ServiceRenderContext{
			Layer:         layer.Name,
			SystemUnitDir: "/etc/systemd/system",
		}
		if homeDir, _ := os.UserHomeDir(); homeDir != "" {
			renderCtx.Home = homeDir
			renderCtx.UserUnitDir = homeDir + "/.config/systemd/user"
		}
		return true
	}

	for i := range layer.Service() {
		entry := &layer.Service()[i]
		scope := ScopeSystem
		if entry.EffectiveScope() == "user" {
			scope = ScopeUser
		}

		if entry.IsPackaged() {
			// supervisord can't consume systemd packaged units.
			if !initIsSystemd {
				continue
			}
			out = append(out, &ServicePackagedStep{
				Unit:        ensureServiceSuffix(entry.UsePackaged),
				TargetScope: scope,
				Enable:      entry.Enable,
				LayerName:   layer.Name,
			})
			continue
		}

		// Custom-exec entry. On systemd targets, if a same-name
		// use_packaged sibling exists, the packaged form wins —
		// skip the custom entry entirely (mixed-pair polymorphism).
		if initIsSystemd && namesWithPackaged[entry.Name] {
			continue
		}

		step := &ServiceCustomStep{
			Name:        fmt.Sprintf("ov-%s-%s", layer.Name, entry.Name),
			TargetScope: scope,
			Enable:      entry.Enable,
			LayerName:   layer.Name,
		}

		// On systemd targets, pre-render the unit text now so the
		// executor doesn't need a lazy fallback. On supervisord
		// targets, the supervisord init pipeline renders its own
		// fragment — leave UnitText empty.
		if initIsSystemd && loadSystemd() {
			entryClone := *entry
			entryClone.Name = step.Name
			rendered, rerr := RenderService(&entryClone, systemdDef, renderCtx)
			if rerr == nil && rendered != nil {
				step.UnitText = rendered.UnitText
				step.UnitPath = rendered.UnitPath
			}
		}

		out = append(out, step)
	}
	return out
}

// ensureServiceSuffix adds `.service` if missing. Distinguishes unit
// types: if the caller already included a suffix (e.g. "foo.timer",
// "foo.socket"), leave it alone.
func ensureServiceSuffix(unit string) string {
	if unit == "" {
		return unit
	}
	known := []string{".service", ".timer", ".socket", ".path", ".target", ".mount", ".slice"}
	for _, s := range known {
		if strings.HasSuffix(unit, s) {
			return unit
		}
	}
	return unit + ".service"
}

// ---------------------------------------------------------------------------
// Debug helpers
// ---------------------------------------------------------------------------

// DescribePlan returns a short human-readable summary of a plan. Used by
// --dry-run table output and by TestDescribePlan for golden comparison.
func DescribePlan(p *InstallPlan) string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: layer=%s image=%s distro=%s steps=%d\n",
		p.Layer, p.Image, p.Distro, len(p.Steps))
	counts := map[StepKind]int{}
	for _, s := range p.Steps {
		counts[s.Kind()]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "  %s: %d\n", k, counts[StepKind(k)])
	}
	return b.String()
}
