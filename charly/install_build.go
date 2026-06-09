package main

// install_build.go — the InstallPlan compiler.
//
// BuildDeployPlan walks a resolved image plus its layer set and produces
// an InstallPlan (charly/install_plan.go) — the IR that both the OCI target
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
func BuildDeployPlan(layer *Layer, img *ResolvedBox, hostCtx HostContext) (*InstallPlan, error) {
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

	// 2. Multi-stage builders triggered by layer manifest files. Emitted
	// BEFORE the layer's tasks so a task that consumes the builder's home
	// artifacts (e.g. selkies' web-copy reads the pixi/build.sh output at
	// ~/.local/share/selkies-build) finds them already in place. This
	// matches the image build, where every builder stage's /home is COPYed
	// into the main stage up front (before any layer install step) — the
	// builder runs in an isolated stage/image and never depends on the
	// layer's own tasks, so running it first is always safe. On a cross-host
	// deploy the BuilderStep extracts the home artifacts into the guest home,
	// which the subsequent TaskStep then relocates; the OCI target hoists the
	// builder stages regardless of step order, so its Containerfile is
	// unchanged.
	builderSteps := compileBuilderSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, builderSteps...)

	// 2.5. Bundled PKGBUILD (`localpkg:`) — build on the host + pacman -U on a
	// pac deploy target. Emitted BEFORE the layer's tasks so the package is
	// already installed when the layer's own `cmd:` task runs: the charly layer's
	// task is pacman-aware (it does nothing when opencharly-git is present and
	// only curls a binary otherwise), so the package must land first or the
	// curl branch shadows the proper /usr/bin/charly with a stale /usr/local/bin/charly.
	// Skipped on image build (no makepkg in a container) and on non-pac targets.
	if pkgStep := compileLocalPkgStep(layer, img, hostCtx); pkgStep != nil {
		plan.Steps = append(plan.Steps, pkgStep)
	}

	// 3. Inline tasks (cmd / mkdir / copy / write / link / download / setcap).
	taskSteps := compileTaskSteps(layer, img)
	plan.Steps = append(plan.Steps, taskSteps...)

	// 4. Services: both legacy `service:` (supervisord INI fragments) and
	// `system_services:` (systemd unit names). After the Task 6 migration
	// lands these both flow through a unified `services:` schema; for now
	// we read the two fields independently.
	svcSteps := compileServiceSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, svcSteps...)

	// 5. Shell-init snippets: the candy manifest `shell:` block. Generic body +
	// per-shell sub-blocks (bash/zsh/fish/sh). Selection rule: per-shell
	// wins over generic with ${SHELL_NAME} substitution.
	shellSteps := compileShellSnippetSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, shellSteps...)

	// 6. Android apps: the candy manifest `apk:` package format. Compiled into ONE
	// ApkInstallStep regardless of target; only AndroidDeployTarget executes
	// it (every other target records a skip). See ApkInstallStep.
	if apkStep := compileApkStep(layer); apkStep != nil {
		plan.Steps = append(plan.Steps, apkStep)
	}

	// 7. Reboot: the candy manifest `reboot: true`. Emitted LAST so the reboot
	// follows every install step of this layer. Only VmDeployTarget acts
	// on it (reboots the guest + waits); OCI/pod/k8s skip it (no machine
	// at build time); LocalDeployTarget skips + warns (never reboots the
	// operator host unattended). See RebootStep.
	if layer.reboot {
		plan.Steps = append(plan.Steps, &RebootStep{LayerName: layer.Name})
	}

	return plan, nil
}

// compileApkStep turns a layer's `apk:` package list into a single
// ApkInstallStep, or nil if the layer declares no apk packages. The `apk`
// format is target-agnostic at compile time — the step carries the specs and
// each DeployTarget decides whether to execute (android) or skip (everything
// else), exactly as the IR intends for venue-specific work.
func compileApkStep(layer *Layer) InstallStep {
	apks := layer.Apk()
	if len(apks) == 0 {
		return nil
	}
	return &ApkInstallStep{
		Packages:  append([]ApkPackageSpec(nil), apks...),
		LayerName: layer.Name,
		LayerDir:  layer.SourceDir,
	}
}

// compileLocalPkgStep turns a layer's `localpkg:` field into a single
// LocalPkgInstallStep, or nil if the layer declares none. Like compileApkStep
// it is target-agnostic at compile time — the step carries the author's
// PKGBUILD hint plus the layer dir AND the deploy project dir (os.Getwd, the
// same handle compileServiceSteps reads) as the two anchors the emit-time
// walk-up search uses to locate the PKGBUILD. Each DeployTarget decides whether
// to build+install (localpkg-capable host/guest), skip (image build, non-pac
// targets, android, k8s).
//
// The localpkg mechanism is fully config-driven: the format's `local_pkg:`
// block (resolved here via DistroDef.LocalPkgFormat, the SAME DistroDef the
// system-package steps read) supplies the build/install templates, package
// glob, foreign-deps query, probe, and dependency-builder name. BuilderImage is
// resolved for that block's DepBuilder the same way builder steps resolve theirs
// (resolveBuilderImage) so the executor can build the package's dependency
// closure through the EXISTING builder before installing. When the distro
// declares no localpkg-capable format, LocalPkg is nil and the executor skips;
// when no dep builder resolves, BuilderImage is "" and the dep-build is skipped
// with a clear log (the layer's own curl/COPY fallback still covers
// non-localpkg targets).
func compileLocalPkgStep(layer *Layer, img *ResolvedBox, hostCtx HostContext) InstallStep {
	// The target distro must declare a localpkg-capable package format, AND the
	// layer must point that format at a source dir. Resolve the format FIRST so
	// the per-format `localpkg:` map picks the matching source (pac→pkg/arch,
	// rpm→pkg/fedora, deb→pkg/debian). Either missing → no step (the layer's own
	// curl/COPY task is the fallback on formats with no native package).
	if img.DistroDef == nil {
		return nil
	}
	fmtName, lp := img.DistroDef.LocalPkgFormat(img.Pkg)
	if lp == nil {
		return nil
	}
	ref := layer.LocalPkg(fmtName)
	if ref == "" {
		return nil
	}
	projectDir, _ := os.Getwd()
	return &LocalPkgInstallStep{
		PkgbuildRef: ref,
		LayerName:   layer.Name,
		LayerDir:    layer.SourceDir,
		ProjectDir:  projectDir,
		Format:      fmtName,
		LocalPkg:    lp,
	}
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
func primaryDistroTag(img *ResolvedBox, hostCtx HostContext) string {
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
// path_append: fields, or nil if the layer contributes neither.
//
// Home resolution is DEFERRED: `~`/`$HOME` are rewritten to the literal
// `{{.Home}}` token rather than expanded against img.Home. Each DeployTarget
// resolves the token at emit time against the home of the actual deploy
// destination — img.Home for the OCI/pod-overlay build, the host home for
// LocalDeployTarget, and the GUEST home (via the SSH executor's ResolveHome)
// for VmDeployTarget. Baking img.Home here was wrong for VM deploys: the
// synthetic plan's Home was the host operator's home, so env.d on the guest
// pointed at /home/<operator> instead of /home/<guest-user>. See
// InstallPlan.ResolveHome.
func compileShellHookStep(layer *Layer, img *ResolvedBox) *ShellHookStep {
	envCfg, _ := layer.EnvConfig()
	if envCfg == nil {
		return nil
	}
	if len(envCfg.Vars) == 0 && len(envCfg.PathAppend) == 0 {
		return nil
	}
	vars := make(map[string]string, len(envCfg.Vars))
	for k, v := range envCfg.Vars {
		vars[k] = ExpandPath(v, HomeToken)
	}
	paths := make([]string, 0, len(envCfg.PathAppend))
	for _, p := range envCfg.PathAppend {
		paths = append(paths, ExpandPath(p, HomeToken))
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
//  1. If layer.Shell.ByShell[shell] exists, render it.
//  2. Else if layer.Shell.Init is non-empty, render the generic body with
//     ${SHELL_NAME} substituted to the active shell name.
//  3. Else this shell gets nothing from this layer.
//
// path_append entries are rendered into the snippet body using
// shell-appropriate syntax (PATH=... for bash/zsh/sh, fish_add_path for
// fish) before the step is emitted, so each DeployTarget emitter can
// write the snippet bytes verbatim.
//
// Destination resolution is target-aware:
//   - Container build (hostCtx.Target empty / "oci"): system-wide
//     drop-in (/etc/profile.d/charly-<layer>-<shell>.sh, /etc/fish/conf.d/
//     charly-<layer>.fish). UseDropin=true.
//   - target:local, target:vm (hostCtx.Target = "host" or "vm"):
//     bash/zsh/sh → managed-block append in the user's rc file (UseDropin
//     =false); fish → per-layer drop-in in ~/.config/fish/conf.d/
//     (UseDropin=true).
//
// Returns nil when the layer has no shell: block.
func compileShellSnippetSteps(layer *Layer, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	cfg := layer.Shell()
	if cfg == nil {
		return nil
	}
	out := make([]InstallStep, 0, len(ShellAllowlist))
	// Home resolution is deferred for DEPLOY targets (host/vm): emit the
	// `{{.Home}}` token so each target resolves it at emit time against the
	// real destination home (host home for local, GUEST home for vm). The
	// container BUILD path (empty/oci target) keeps img.Home — there the
	// image's resolved Home IS the runtime home. See InstallPlan.ResolveHome.
	snippetHome := img.Home
	if hostCtx.Target == "host" || hostCtx.Target == "vm" {
		snippetHome = HomeToken
	}
	// Stable iteration: walk the allowlist in fixed order so plan output
	// is deterministic across runs.
	for _, shell := range []string{"bash", "zsh", "fish", "sh"} {
		spec, body, paths, ok := resolveShellSpec(cfg, shell)
		if !ok {
			continue
		}
		dest, useDropin := shellSnippetDestination(layer.Name, shell, hostCtx, snippetHome, spec.Path)
		if dest == "" {
			continue
		}
		// Render path_append into the body using shell-appropriate syntax.
		body = appendShellPathLines(body, paths, shell, snippetHome)
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
			return ExpandPath(fmt.Sprintf("~/.config/fish/conf.d/charly-%s.fish", layerName), home), true
		}
		return "", false
	}
	// Container build (oci/empty target): system-wide drop-in files.
	switch shell {
	case "bash", "zsh", "sh":
		return fmt.Sprintf("/etc/profile.d/charly-%s-%s.sh", layerName, shell), true
	case "fish":
		return fmt.Sprintf("/etc/fish/conf.d/charly-%s.fish", layerName), true
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
// compileSystemPackageSteps resolves a layer's package surface for an image via
// the distro-specificity CASCADE and emits ONE SystemPackagesStep for the
// image's primary format. img.Distro is the most-specific-first tag chain
// (e.g. [debian:13, debian] or [ubuntu:24.04, ubuntu]); the layer's top-level
// package: list (layer.TopPackages) is the always-included BASE.
//
//   - packages = UNION of the base + every matching tag section's packages
//     (dedup), so a shared package at one level plus extras at another accumulate.
//   - repo/copr/options/exclude/module = the MOST-SPECIFIC matching level that
//     declares the field wins (a version's repo overrides the bare distro's).
//
// img.Distro is most-specific-first, so iterating it in REVERSE (least → most
// specific) and letting later writes win yields most-specific-wins for the Raw
// extras while packages union regardless of order. fedora/arch reach their
// packages via the bare-distro tag (img.Distro = [fedora] / [arch]); there is no
// format-section fallback — the deb collapse that caused non-deterministic repo
// selection is gone (per-distro tag sections never share a mutable section).
func compileSystemPackageSteps(layer *Layer, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	if img.DistroDef == nil {
		return nil
	}
	formatDef := img.DistroDef.Format[img.Pkg]
	if formatDef == nil {
		return nil
	}
	pkgs, raw, matched := resolveCascadePackages(layer, img)
	if !matched && len(pkgs) == 0 {
		return nil
	}
	return []InstallStep{buildSystemPackagesStep(img.Pkg, PhaseInstall, pkgs, raw, formatDef.CacheMount)}
}

// resolveCascadePackages is THE single distro-specificity cascade resolver,
// shared by EVERY package-emitting path — the deploy compiler
// (compileSystemPackageSteps) AND the image-build Containerfile emitter
// (generate.go writeLayerSteps). There is exactly one resolution so build and
// deploy can never diverge.
//
// It computes the primary-format package set for a layer on an image: the
// layer's top-level `package:` BASE, UNION every matching distro tag section
// walked most-specific-first over img.Distro (deduped), with the Raw extras
// repo/copr/options/exclude/module resolved MOST-SPECIFIC-WINS. Returns the
// package list, the rendered Raw install context (including the unioned
// `package` list), and whether any tag section matched.
// cascadeTagChain returns the full per-layer cascade tag chain MOST-SPECIFIC
// FIRST: the image's distro chain (e.g. [debian:13, debian]) followed by the
// package-format FAMILY tag (img.Pkg = deb/pac/rpm) as the LEAST-specific level.
// A `distro: deb:` layer block therefore applies to EVERY deb-format distro
// (debian + ubuntu + their versions), `pac:` to arch + cachyos, `rpm:` to
// fedora — the family-generic level of the YAML-configured
// deb/pac/rpm → distro → version hierarchy. img.Pkg is the build.yml-declared
// primary package format, so the hierarchy lives entirely in YAML.
//
// Distro INHERITANCE is the complementary YAML mechanism: img.Distro is already
// expanded (at resolve time, expandPackageInheritance) to include any
// `inherit_packages: true` ancestor, so a cachyos image/VM carries [cachyos, …,
// arch] and a `distro: arch:` block DOES reach cachyos — while ubuntu (no flag)
// stays isolated from debian. Both knobs live entirely in build.yml.
func cascadeTagChain(img *ResolvedBox) []string {
	chain := append([]string(nil), img.Distro...)
	if img.Pkg != "" {
		chain = append(chain, img.Pkg)
	}
	return chain
}

func resolveCascadePackages(layer *Layer, img *ResolvedBox) (pkgs []string, raw map[string]interface{}, matched bool) {
	seen := map[string]bool{}
	add := func(in []string) {
		for _, p := range in {
			if p != "" && !seen[p] {
				pkgs = append(pkgs, p)
				seen[p] = true
			}
		}
	}
	// Always-included base first (stable ordering).
	add(layer.TopPackages())

	raw = map[string]interface{}{}
	chain := cascadeTagChain(img)
	for i := len(chain) - 1; i >= 0; i-- { // least → most specific: format → distro → version
		cfg := layer.TagSection(chain[i])
		if cfg == nil {
			continue
		}
		matched = true
		add(cfg.Package)
		// Most-specific level writes last → wins for each Raw extra.
		for _, k := range []string{"repo", "copr", "options", "exclude", "module"} {
			if v, ok := cfg.Raw[k]; ok && v != nil {
				raw[k] = v
			}
		}
	}
	raw["package"] = pkgs
	return pkgs, raw, matched
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
	// Repos live under the canonical "repo" key — the schema YAML tag
	// (DistroPackages.Repo `yaml:"repo"`), what derivePackageSectionsFromCalamares
	// writes, and what NewInstallContext reads on the build path. Use toMapSlice
	// so a []map[string]any value (the Calamares Repo shape) converts correctly:
	// the prior `raw["repos"].([]interface{})` was wrong on BOTH the key (plural)
	// and the type assertion, so step.Repos stayed empty and the deploy path never
	// added a layer's apt/dnf repo (e.g. the charly layer's tailscale repo on deb).
	for _, m := range toMapSlice(raw["repo"]) {
		step.Repos = append(step.Repos, RepoSpec{Raw: m})
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
func compileTaskSteps(layer *Layer, img *ResolvedBox) []InstallStep {
	if !layer.HasTasks() || len(layer.tasks) == 0 {
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
		// Tokenize a home-relative copy/download dest so each DeployTarget
		// resolves it against the real destination home at emit (the guest
		// home for vm, the host home for local) — leaving the literal
		// "${HOME}" out of the PutFile dest. Empty `to:` stays empty.
		var resolvedTo string
		if task.To != "" {
			resolvedTo = ExpandPath(task.To, HomeToken)
		}
		out = append(out, &TaskStep{
			Task:         task,
			LayerName:    layer.Name,
			LayerDir:     layer.SourceDir,
			CtxPath:      layer.SourceDir,
			ResolvedUser: userDir,
			LayerVars:    layerVars,
			To:           resolvedTo,
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
func compileBuilderSteps(layer *Layer, img *ResolvedBox, hostCtx HostContext) []InstallStep {
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
			Builder:    bName,
			LayerName:  layer.Name,
			LayerDir:   layer.SourceDir,
			Phase:      PhaseInstall,
			BuilderDef: bDef,
		}
		step.BuilderImage = resolveBuilderImage(bName, img, hostCtx)
		step.RawStageContext = collectBuilderContext(layer, bName, bDef, img)

		// aur produces .pkg.tar.zst files in the container at /tmp/aur-pkgs/
		// and we need to pull them out on the host target. The container
		// (OCI) target ignores Artifacts — it uses COPY --from directly.
		// The host/VM targets install those package files via the SAME
		// config-driven transfer+install leg as the localpkg step, so carry
		// the package format's localpkg contract (install command + glob)
		// resolved from build.yml — no hardcoded pacman/glob in the executor.
		if bName == "aur" {
			step.Artifacts = []ArtifactRef{{
				ContainerPath: "/tmp/aur-pkgs/",
				HostPath:      "", // host-target populates at emit time (tmpdir)
				Chown:         false,
			}}
			if img.DistroDef != nil {
				if _, lp := img.DistroDef.LocalPkgFormat(img.Pkg); lp != nil {
					step.LocalPkg = lp
				}
			}
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
func resolveBuilderImage(name string, img *ResolvedBox, hostCtx HostContext) string {
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
// derivable from the candy manifest alone; the host target refines these at
// execution time.
func collectBuilderContext(layer *Layer, builderName string, bDef *BuilderDef, img *ResolvedBox) map[string]interface{} {
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
// gone — external layers must run `charly migrate`.
//
// **Init-system polymorphism filter (2026-05).** When a layer declares
// the same service `name:` twice — once with `use_packaged:` and once
// with custom `exec:` (the mixed-entry polymorphism pattern documented
// in CLAUDE.md "Init-system polymorphism via mixed `service:` entries"
// and `/charly-build:layer` "Service Declaration") — the compiler picks ONE
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
func compileServiceSteps(layer *Layer, img *ResolvedBox, hostCtx HostContext) []InstallStep {
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
		// Service home, like shell-snippet home, must be the DESTINATION user's
		// home — not the build host's. For host/vm deploys defer it via the
		// {{.Home}} token (InstallPlan.ResolveHome substitutes the real guest /
		// host home at emit); for a container-systemd build the image's resolved
		// Home is the runtime home. (os.UserHomeDir() — the operator's home — was
		// the service-side instance of the VM $HOME bug.)
		svcHome := img.Home
		if hostCtx.Target == "host" || hostCtx.Target == "vm" {
			svcHome = HomeToken
		}
		if svcHome != "" {
			renderCtx.Home = svcHome
			renderCtx.UserUnitDir = svcHome + "/.config/systemd/user"
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
			Name:        fmt.Sprintf("charly-%s-%s", layer.Name, entry.Name),
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
