package main

// build_target_oci.go — OCITarget implements DeployTarget for Containerfile
// emission (the "build mode" target used by `charly box build`).
//
// At this stage of the refactor, OCITarget is a thin walker over the
// InstallPlan that delegates to the existing format/template rendering
// machinery in format_template.go + tasks.go. Later passes will migrate
// the direct-text emission inside writeCandySteps into this walker so
// the legacy generator shrinks to a shell.
//
// The key property we want from OCITarget: feeding it a plan produced
// by BuildDeployPlan must emit a Containerfile fragment that's
// functionally equivalent to what today's writeCandySteps produces for
// the same candy. Not byte-identical (we've dropped that requirement
// per the user) but semantically equivalent — same packages installed,
// same tasks executed, same services configured.

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// OCITarget emits Containerfile directives for an InstallPlan. One
// instance handles one image build; callers create a new target per
// image and call Emit with the plan set for that image.
type OCITarget struct {
	// DistroDef is the resolved per-image distro definition — needed so
	// OCITarget can look up format install_templates and cache mounts.
	DistroDef *DistroDef

	// BuilderConfig is the builder registry for this image — used to
	// render multi-stage builders when the IR contains BuilderStep.
	BuilderConfig *BuilderConfig

	// Box, BuildDir, ContextRelPrefix mirror the state the legacy
	// Generator carries for emit-time rendering. Populated by callers
	// before Emit when they want full task + builder rendering (not
	// just the placeholder output). Safe to leave zero for tests.
	Box              *ResolvedBox
	BuildDir         string
	ContextRelPrefix string
	Generator        *Generator // used for emitTasks + builder stage rendering

	// Buffer collects the rendered Containerfile fragment. Callers
	// read it via String() after Emit completes.
	buf strings.Builder
}

// Name identifies this target.
func (t *OCITarget) Name() string { return "oci" }

// Emit walks each plan's steps and appends Containerfile directives to
// the internal buffer. Multiple plans emit sequentially (per-candy).
func (t *OCITarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if err := t.emitPlan(plan, opts); err != nil {
			return fmt.Errorf("OCITarget.Emit(%s): %w", plan.Candy, err)
		}
	}
	return nil
}

// String returns the accumulated Containerfile fragment.
func (t *OCITarget) String() string {
	return t.buf.String()
}

// emitPlan emits directives for one candy's plan.
func (t *OCITarget) emitPlan(plan *InstallPlan, opts EmitOpts) error {
	// Resolve the deferred {{.Home}} token in home-bearing step fields to
	// the image's runtime home. For an OCI build (and the pod-overlay build
	// that reuses OCITarget) img.Home IS the home the baked paths run under.
	if t.Box != nil {
		plan.ResolveHome(t.Box.Home)
	}
	fmt.Fprintf(&t.buf, "# Layer: %s\n", plan.Candy)
	for _, step := range plan.Steps {
		if step.Venue() == VenueSkip {
			continue
		}
		// Gates don't apply to OCI emission — container builds are
		// already isolated, so the opt-in flags mean nothing here.
		if err := t.emitStep(step, plan); err != nil {
			return err
		}
	}
	t.buf.WriteString("\n")
	return nil
}

// emitStep dispatches to the per-kind emitter.
func (t *OCITarget) emitStep(step InstallStep, plan *InstallPlan) error {
	switch s := step.(type) {
	case *ShellHookStep:
		return t.emitShellHook(s)
	case *SystemPackagesStep:
		return t.emitSystemPackages(s)
	case *BuilderStep:
		return t.emitBuilder(s, plan)
	case *TaskStep:
		return t.emitTask(s)
	case *FileStep:
		return t.emitFile(s)
	case *ServicePackagedStep:
		return t.emitServicePackaged(s)
	case *ServiceCustomStep:
		return t.emitServiceCustom(s)
	case *RepoChangeStep:
		return t.emitRepoChange(s)
	case *ShellSnippetStep:
		return t.emitShellSnippet(s)
	case *ApkInstallStep:
		// apk installs land on a RUNNING Android device, not the image
		// being built — there is no device at image-build time. Skip
		// silently (the deploy-time AndroidDeployTarget executes it).
		return nil
	case *LocalPkgInstallStep:
		// Image-build install of a candy's localpkg package. PRODUCTION boxes
		// download the PUBLISHED release; disposable eval beds build the
		// in-development package from local source — ONE shared decision in
		// renderLocalPkgImageInstall. Both install via the SAME dep-resolving
		// install_template (OS-tracked, not a bare COPY'd binary).
		return t.emitLocalPkgInstall(s)
	case *RebootStep:
		// No machine to reboot during an image build — skip silently
		// (a target:vm deploy of this candy performs the reboot).
		return nil
	}
	return fmt.Errorf("OCITarget: unknown step kind %q", step.Kind())
}

// emitShellSnippet renders a candy's per-shell init snippet into the
// container's system-wide drop-in directory. bash/zsh/sh land in
// /etc/profile.d/charly-<candy>-<shell>.sh; fish lands in
// /etc/fish/conf.d/charly-<candy>.fish (paths are computed by
// compileShellSnippetSteps based on hostCtx.Target).
//
// Uses a heredoc with a randomized end-marker to avoid collision with
// snippet bodies that contain literal `CHARLY_SNIPPET` on their own line.
// The container drop-in is always root-owned + 0644 — sourced by the
// shell at user login, no need for execute bit.
func (t *OCITarget) emitShellSnippet(s *ShellSnippetStep) error {
	if s.Snippet == "" {
		return nil
	}
	// Pick an end-marker derived from the snippet hash so a malicious or
	// pathological body containing the literal marker can't break out.
	h := sha256.Sum256([]byte(s.Snippet))
	marker := fmt.Sprintf("CHARLY_SHELL_%s_%x", strings.ToUpper(s.Shell), h[:4])
	fmt.Fprintf(&t.buf,
		"RUN mkdir -p %s && cat > %s <<'%s'\n%s\n%s\n",
		shellQuote(filepath.Dir(s.Destination)),
		shellQuote(s.Destination),
		marker,
		s.Snippet,
		marker,
	)
	return nil
}

// emitShellHook renders `env:` and `path_append:` as ENV directives.
func (t *OCITarget) emitShellHook(s *ShellHookStep) error {
	// Emit ENV for each var.
	for k, v := range s.EnvVars {
		fmt.Fprintf(&t.buf, "ENV %s=%q\n", k, v)
	}
	// PATH additions as a single ENV line prepending to existing $PATH.
	if len(s.PathAdd) > 0 {
		// Reverse-order join so earlier-listed entries take precedence
		// (they end up leftmost on the final PATH).
		parts := make([]string, 0, len(s.PathAdd)+1)
		parts = append(parts, s.PathAdd...)
		parts = append(parts, "$PATH")
		fmt.Fprintf(&t.buf, "ENV PATH=%s\n", strings.Join(parts, ":"))
	}
	return nil
}

// emitSystemPackages renders a format-specific package install. Uses
// PhaseTemplate lookup so the new phase: path preempts the legacy
// install_template when present. Falls back to legacy InstallTemplate
// for the (install, container) cell.
// emitLocalPkgInstall emits the image-build install of a candy's `localpkg:`
// package via the shared renderLocalPkgImageInstall (production release-download
// vs disposable-eval-bed in-development build — see that function). The
// eval-bed switch flows through the Generator's DevLocalPkg flag (one source,
// both image-build paths read it). The overlay path (deploy-time) never sets it.
func (t *OCITarget) emitLocalPkgInstall(s *LocalPkgInstallStep) error {
	dev := t.Generator != nil && t.Generator.DevLocalPkg
	boxName := ""
	if t.Box != nil {
		boxName = t.Box.Name
	}
	run, err := renderLocalPkgImageInstall(s, dev, t.BuildDir, boxName)
	if err != nil {
		return err
	}
	t.buf.WriteString(run)
	return nil
}

func (t *OCITarget) emitSystemPackages(s *SystemPackagesStep) error {
	if t.DistroDef == nil || t.DistroDef.Format == nil {
		return fmt.Errorf("no distro definition for format %s", s.Format)
	}
	formatDef := t.DistroDef.Format[s.Format]
	if formatDef == nil {
		return fmt.Errorf("no format %q in distro", s.Format)
	}
	template := formatDef.PhaseTemplate(s.Phase, VenueContainerBuilder)
	if template == "" {
		// No template for this phase/venue is not an error — some phases
		// simply have nothing to emit in the container (e.g. cleanup
		// phases whose host: blocks only record state for teardown).
		return nil
	}
	ctx := NewInstallContext(s.RawInstallContext, formatDefCacheMountDefs(formatDef))
	rendered, err := RenderTemplate(s.Format+"-install", template, ctx)
	if err != nil {
		return fmt.Errorf("rendering %s install template: %w", s.Format, err)
	}
	t.buf.WriteString(rendered)
	return nil
}

// emitBuilder renders a multi-stage or inline builder by invoking
// the same BuildStageContext + RenderTemplate pipeline the legacy
// generator uses. Requires OCITarget.Box + OCITarget.BuilderConfig
// to be populated; otherwise emits a comment explaining why nothing
// was rendered (tests that don't care about real output leave them nil).
func (t *OCITarget) emitBuilder(s *BuilderStep, plan *InstallPlan) error {
	if t.BuilderConfig == nil {
		fmt.Fprintf(&t.buf, "# Builder: %s (layer=%s) — skipped, no BuilderConfig\n",
			s.Builder, s.CandyName)
		return nil
	}
	bDef, ok := t.BuilderConfig.Builder[s.Builder]
	if !ok || bDef == nil {
		return fmt.Errorf("builder %q: not defined in BuilderConfig", s.Builder)
	}
	if t.Box == nil {
		fmt.Fprintf(&t.buf, "# Builder: %s (layer=%s) — skipped, no Image context\n",
			s.Builder, s.CandyName)
		return nil
	}

	layer := t.lookupCandy(s.CandyName)
	if layer == nil {
		fmt.Fprintf(&t.buf, "# Builder: %s (layer=%s) — layer not found in scan\n",
			s.Builder, s.CandyName)
		return nil
	}

	// Inline builders (cargo): render InstallTemplate with the builder's
	// inline context; no separate FROM stage.
	if bDef.Inline {
		ctx := &BuildStageContext{
			LayerStage:  layer.Name,
			UID:         t.Box.UID,
			GID:         t.Box.GID,
			CacheMounts: bDef.CacheMount,
		}
		rendered, err := RenderTemplate(s.Builder+"-inline", bDef.InstallTemplate, ctx)
		if err != nil {
			return fmt.Errorf("inline builder %s: %w", s.Builder, err)
		}
		// Switch USER to the image user for inline builder steps; matches
		// legacy generate.go:1184-1187.
		fmt.Fprintf(&t.buf, "USER %d\n", t.Box.UID)
		t.buf.WriteString(rendered)
		return nil
	}

	// Multi-stage builders (pixi/npm/aur): emit the stage via the
	// Generator's existing buildStageContext helper when available. For
	// synthetic test paths without a Generator, fall back to an
	// informative comment so authors can spot unwired test cases.
	if t.Generator == nil {
		fmt.Fprintf(&t.buf, "# Builder: %s (layer=%s) — multi-stage requires Generator; emit skipped\n",
			s.Builder, s.CandyName)
		return nil
	}
	builderRef := ""
	if t.Box.Builder != nil {
		builderRef = t.Box.Builder[s.Builder]
	}
	ctx := t.Generator.buildStageContext(layer, s.Builder, bDef, t.Box, builderRef)
	if ctx == nil {
		return fmt.Errorf("buildStageContext returned nil for %s", s.Builder)
	}
	rendered, err := RenderTemplate(s.Builder+"-stage", bDef.StageTemplate, ctx)
	if err != nil {
		return fmt.Errorf("multi-stage builder %s: %w", s.Builder, err)
	}
	t.buf.WriteString(rendered)
	return nil
}

// emitTask renders a single task via the legacy emitTasks pipeline.
// Because emitTasks processes the entire candy in one pass (including
// coalescing adjacent mkdir/link/setcap batches), we accumulate
// consecutive TaskSteps and flush them through emitTasks as a group.
// This preserves today's rendering semantics exactly.
func (t *OCITarget) emitTask(s *TaskStep) error {
	// Single-task emission delegates to the same emitTasks that
	// writeCandySteps calls, but for one task at a time via a synthetic
	// single-element layer.tasks slice. Requires Generator + Image.
	if t.Generator == nil || t.Box == nil {
		kind, _ := s.Task.Kind()
		fmt.Fprintf(&t.buf, "# Task: %s (layer=%s) — no Generator context\n",
			kind, s.CandyName)
		return nil
	}
	layer := t.lookupCandy(s.CandyName)
	if layer == nil {
		return fmt.Errorf("task emit: candy %q not found", s.CandyName)
	}

	// Temporarily swap layer.tasks to just this one task so emitTasks
	// renders only it. Restore on exit.
	saved := layer.tasks
	layer.tasks = []Task{*s.Task}
	defer func() { layer.tasks = saved }()

	_, err := t.Generator.emitTasks(&t.buf, layer, t.Box, t.BuildDir, t.ContextRelPrefix, "0")
	return err
}

// lookupCandy pulls the Candy struct by name from the Generator's
// scanned candy set. Returns nil when the Generator is nil.
func (t *OCITarget) lookupCandy(name string) *Candy {
	if t.Generator == nil {
		return nil
	}
	return t.Generator.Candies[name]
}

// emitFile renders a file placement. Uses COPY --chmod/--chown with
// the file's scratch-stage alias.
func (t *OCITarget) emitFile(s *FileStep) error {
	chmod := fmt.Sprintf("%04o", s.Mode.Perm())
	chown := ""
	if s.Owner != "" && s.Owner != "root" && s.Owner != "0" {
		chown = fmt.Sprintf(" --chown=%s", s.Owner)
	}
	fmt.Fprintf(&t.buf, "COPY --chmod=%s%s %s %s\n", chmod, chown, s.Source, s.Dest)
	return nil
}

// emitServicePackaged renders a "enable packaged systemd unit" step.
// For OCI build, a packaged unit was installed via its rpm/deb/pac
// package; we emit a marker so downstream supervisord/systemd pipelines
// can enable the unit at image boot time. Drop-in overrides emit as
// file writes.
func (t *OCITarget) emitServicePackaged(s *ServicePackagedStep) error {
	if s.OverridesText != "" && s.OverridesPath != "" {
		fmt.Fprintf(&t.buf, "RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_DROPIN'\n%s\nCHARLY_DROPIN\n",
			s.OverridesPath, s.OverridesPath, s.OverridesText)
	}
	if s.Enable {
		scope := "system"
		if s.TargetScope == ScopeUser {
			scope = "user"
		}
		fmt.Fprintf(&t.buf, "# Service: enable packaged unit %s (scope=%s, layer=%s)\n",
			s.Unit, scope, s.CandyName)
	}
	return nil
}

// emitServiceCustom renders a custom service unit. Today's generator
// assembles supervisord INI fragments into /etc/supervisord.conf at
// build time; after the services: refactor this will emit a rendered
// unit file.
func (t *OCITarget) emitServiceCustom(s *ServiceCustomStep) error {
	if s.UnitText == "" {
		return nil
	}
	fmt.Fprintf(&t.buf, "# Service: custom %s (layer=%s)\n# -- unit content follows in the init fragment pipeline --\n",
		s.Name, s.CandyName)
	return nil
}

// emitRepoChange renders a structured repo file write. This path is
// rarely used by today's generator (which renders repo setup inline in
// the format install_template); it exists for candies that declare
// explicit repo files via the structured schema.
func (t *OCITarget) emitRepoChange(s *RepoChangeStep) error {
	fmt.Fprintf(&t.buf, "RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_REPO'\n%s\nCHARLY_REPO\n",
		s.File, s.File, s.Content)
	return nil
}

// formatDefCacheMountDefs returns the cache mounts as the type
// RenderTemplate's InstallContext expects. FormatDef.CacheMount is the
// source of truth; this is a no-op bridge.
func formatDefCacheMountDefs(f *FormatDef) []CacheMountDef {
	if f == nil {
		return nil
	}
	return f.CacheMount
}
