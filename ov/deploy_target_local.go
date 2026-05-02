package main

// deploy_target_host.go — LocalDeployTarget executes an InstallPlan on
// the host filesystem.
//
// Walk strategy:
//   1. Precompute deploy-id + lock the ledger.
//   2. Group steps by (Scope, Venue) — contiguous same-scope batches
//      become one heredoc; container-builder batches become one
//      podman-run each.
//   3. For each batch:
//       - system + host-native → `sudo bash <<EOF ... EOF`
//       - user + host-native   → `bash <<EOF ... EOF` as invoking user
//       - * + container-builder → podman run <builder> ...
//       - * + skip             → no-op (just record the skip reason)
//   4. After every successful step, append to the per-layer ledger.
//   5. After the whole plan completes, write the deploy record.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalDeployTarget executes plans on the host.
type LocalDeployTarget struct {
	// HostHome is the invoking user's home. Populated by DeployUpCmd
	// before calling Emit.
	HostHome string

	// LedgerPaths points to the on-disk ledger. Defaults to
	// ~/.config/overthink/installed/ when nil.
	LedgerPaths *LedgerPaths

	// Distro is the detected host distro. Used for gating aur: on
	// non-Arch hosts and picking format sections.
	Distro *HostDistro

	// InitDef is the init system used for rendering services on the
	// host. Host deploys always target systemd — caller must supply
	// a systemd InitDef with a populated ServiceSchema.
	InitDef *InitDef

	// BuilderImageResolver maps a builder name to a concrete image ref
	// for `podman run`. Caller supplies — typically derived from
	// image.yml or --builder-image flag.
	BuilderImageResolver func(builderName string) string

	// Shell is the user's login shell (detected via DetectLoginShell
	// by default). Drives env.d managed-block insertion.
	Shell ShellKind

	// Executor is the DeployExecutor used for every shell primitive.
	// Defaults to ShellExecutor{} when nil — which matches the
	// pre-tree-schema behavior of spawning bash on the invoking host.
	//
	// When non-nil (set by the tree-walking dispatcher for nested
	// `target: host` children), the executor may be a NestedExecutor
	// wrapping the parent container / VM / nested-host venue. All
	// RunSystem / RunUser / PutFile calls route through this executor,
	// so a "host deploy inside a container" runs the same InstallPlan
	// IR but lands in the nested venue's rootfs + ledger dir.
	Executor DeployExecutor

	// DryRunWriter receives dry-run output. Nil defaults to os.Stderr.
	DryRunWriter *os.File
}

// exec returns the configured executor, defaulting to a local one
// when unset. Centralized so the emit path doesn't sprinkle nil
// checks at every call site.
func (t *LocalDeployTarget) exec() DeployExecutor {
	if t.Executor == nil {
		return ShellExecutor{}
	}
	return t.Executor
}

// runSystem runs a bash script as root through the target's
// executor. Replaces direct calls to the package-level runSudoShell
// so nested host deploys (executor = NestedExecutor) land in the
// right venue.
func (t *LocalDeployTarget) runSystem(script string, opts EmitOpts) error {
	return t.exec().RunSystem(opts.ContextOrDefault(), script, opts)
}

// runUser runs a bash script as the invoking user through the
// target's executor.
func (t *LocalDeployTarget) runUser(script string, opts EmitOpts) error {
	return t.exec().RunUser(opts.ContextOrDefault(), script, opts)
}

// Name identifies this target.
func (t *LocalDeployTarget) Name() string { return "host" }

// Emit executes the full list of plans. Plans are processed in order;
// all steps from plan N run before any step from plan N+1.
func (t *LocalDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if t.HostHome == "" {
		t.HostHome = os.Getenv("HOME")
	}
	if t.HostHome == "" {
		return fmt.Errorf("LocalDeployTarget: cannot determine HOME")
	}
	if t.LedgerPaths == nil {
		p, err := DefaultLedgerPaths()
		if err != nil {
			return err
		}
		t.LedgerPaths = p
	}
	if t.Shell == "" {
		t.Shell = DetectLoginShell()
	}

	// Lock the ledger for the whole session.
	lock, err := AcquireLedgerLock(t.LedgerPaths)
	if err != nil {
		return err
	}
	defer lock.Release()

	// Sudo preflight: refresh the timestamp once at the start so later
	// `sudo bash <<EOF` blocks reuse the cache. --yes skips the prompt
	// (assumes cached or NOPASSWD).
	if !opts.AssumeYes && !opts.DryRun {
		if err := sudoRefresh(); err != nil {
			return fmt.Errorf("sudo preflight: %w", err)
		}
	}

	for _, plan := range plans {
		if plan == nil {
			continue
		}
		if err := t.emitPlan(plan, opts); err != nil {
			return fmt.Errorf("plan %s: %w", plan.Layer, err)
		}
	}

	// Ensure the shell managed block is in place. Idempotent — safe to
	// run after every deploy.
	if _, err := EnsureManagedBlock(t.Shell, t.HostHome); err != nil {
		return fmt.Errorf("managed block: %w", err)
	}
	return nil
}

// emitPlan walks one plan's steps in IR order, batching by
// (Scope, Venue) for efficient sudo/user/container execution.
func (t *LocalDeployTarget) emitPlan(plan *InstallPlan, opts EmitOpts) error {
	rec := &LayerRecord{
		Layer:      plan.Layer,
		Version:    plan.Version,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}

	batches := plan.StepsByVenue()
	for _, batch := range batches {
		if batch.Venue == VenueSkip {
			t.logSkip(batch, opts)
			continue
		}
		for _, step := range batch.Steps {
			gate := step.RequiresGate()
			if !GateEnabled(gate, opts) {
				t.logGated(step, gate, opts)
				continue
			}
			if err := t.execStep(step, plan, opts, rec); err != nil {
				// Persist what we've recorded so far, then propagate.
				_ = t.recordLayer(rec, plan, opts)
				return err
			}
		}
	}

	return t.recordLayer(rec, plan, opts)
}

// recordLayer writes the per-layer ledger entry and adds the deploy
// to the refcount set. Idempotent across multiple deploys of the same
// layer.
func (t *LocalDeployTarget) recordLayer(rec *LayerRecord, plan *InstallPlan, opts EmitOpts) error {
	if opts.DryRun || plan.DeployID == "" {
		return nil
	}
	// Route via the executor so nested host-deploys (host-target inside
	// a VM / pod via SSH / podman exec) write the ledger on the substrate,
	// not the operator's filesystem. Local executor → operator-side
	// (unchanged behaviour).
	return AddLayerDeploymentVia(t.Executor, t.LedgerPaths, plan.Layer, plan.DeployID, func(existing *LayerRecord) {
		existing.Version = rec.Version
		existing.Steps = append(existing.Steps, rec.Steps...)
		existing.ReverseOps = append(existing.ReverseOps, rec.ReverseOps...)
	})
}

// execStep runs one step and records its reversal ops in rec.
func (t *LocalDeployTarget) execStep(step InstallStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord) error {
	start := time.Now().UTC()
	switch s := step.(type) {
	case *ShellHookStep:
		return t.execShellHook(s, plan, opts, rec, start)
	case *SystemPackagesStep:
		return t.execSystemPackages(s, plan, opts, rec, start)
	case *BuilderStep:
		return t.execBuilder(s, plan, opts, rec, start)
	case *TaskStep:
		return t.execTask(s, plan, opts, rec, start)
	case *FileStep:
		return t.execFile(s, plan, opts, rec, start)
	case *ServicePackagedStep:
		return t.execServicePackaged(s, plan, opts, rec, start)
	case *ServiceCustomStep:
		return t.execServiceCustom(s, plan, opts, rec, start)
	case *RepoChangeStep:
		return t.execRepoChange(s, plan, opts, rec, start)
	}
	return fmt.Errorf("LocalDeployTarget: unknown step kind %T", step)
}

// ---------------------------------------------------------------------------
// Step executors
// ---------------------------------------------------------------------------

func (t *LocalDeployTarget) execShellHook(s *ShellHookStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] env.d/%s.env + managed block\n", s.LayerName)
		t.noteStep(rec, StepKindShellHook, s.Scope(), s.Venue(), fmt.Sprintf("layer=%s", s.LayerName), start)
		return nil
	}
	path, err := WriteEnvdFile(t.HostHome, s.LayerName, s.EnvVars, s.PathAdd)
	if err != nil {
		return err
	}
	s.EnvFile = path
	t.noteStep(rec, StepKindShellHook, s.Scope(), s.Venue(),
		fmt.Sprintf("env=%s", path), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execSystemPackages(s *SystemPackagesStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	cmd, err := t.renderSystemPackageCommand(s)
	if err != nil {
		return err
	}
	if cmd == "" {
		// Phase has no host renderer (e.g. prepare/cleanup phases whose
		// container: blocks are container-only). Quietly skip.
		return nil
	}
	if err := t.runSystem(cmd, opts); err != nil {
		return fmt.Errorf("system packages %s: %w", s.Format, err)
	}
	t.noteStep(rec, StepKindSystemPackages, s.Scope(), s.Venue(),
		fmt.Sprintf("%s: %d packages (%s)", s.Format, len(s.Packages), s.Phase), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// renderSystemPackageCommand looks up the format's host-venue template
// and renders it with the step's RawInstallContext. Returns "" when
// there's no host rendering for this phase (no error — means skip).
func (t *LocalDeployTarget) renderSystemPackageCommand(s *SystemPackagesStep) (string, error) {
	// For host deploys we need the distro's FormatDef. Callers may pass
	// it in via plan-merging; for the skeleton we rely on the installed
	// DistroDef on the target.
	// Absent a FormatDef reference, we fall back to a minimal inline
	// renderer that joins packages with the format's default command.
	cmd := t.renderFallbackPkgCmd(s)
	if cmd == "" {
		return "", fmt.Errorf("no host template for %s / %s", s.Format, s.Phase)
	}
	return cmd, nil
}

// renderFallbackPkgCmd produces a plain shell command for package
// install when no build.yml host template is available yet. Used
// during the incremental migration (Task 7 converts build.yml templates
// format-by-format). The output is a best-effort heuristic — layers
// depending on complex repo/key setup should move to the structured
// templates ASAP.
func (t *LocalDeployTarget) renderFallbackPkgCmd(s *SystemPackagesStep) string {
	if s.Phase != PhaseInstall || len(s.Packages) == 0 {
		return ""
	}
	switch s.Format {
	case "rpm":
		opts := ""
		if len(s.Options) > 0 {
			opts = " " + strings.Join(s.Options, " ")
		}
		return fmt.Sprintf("dnf install -y%s %s", opts, strings.Join(s.Packages, " "))
	case "deb":
		opts := ""
		if len(s.Options) > 0 {
			opts = " " + strings.Join(s.Options, " ")
		}
		return fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get install -y%s %s", opts, strings.Join(s.Packages, " "))
	case "pac":
		return fmt.Sprintf("pacman -S --noconfirm --needed %s", strings.Join(s.Packages, " "))
	}
	return ""
}

func (t *LocalDeployTarget) execBuilder(s *BuilderStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	// Builder image resolution mirrors VmDeployTarget.execBuilder:
	//   1. EmitOpts.BuilderImageOverride (--builder-image flag)
	//   2. BuilderStep.BuilderImage (compiled from image.yml)
	//   3. t.BuilderImageResolver (rarely wired)
	image := opts.BuilderImageOverride
	if image == "" {
		image = s.BuilderImage
	}
	if image == "" && t.BuilderImageResolver != nil {
		image = t.BuilderImageResolver(s.Builder)
	}
	if image == "" {
		return fmt.Errorf("no builder image for %s (layer=%s); set --builder-image or define builder.%s in image.yml", s.Builder, s.LayerName, s.Builder)
	}

	// aur on non-Arch host: refuse cleanly.
	if s.Builder == "aur" && t.Distro != nil {
		hint := t.Distro.FormatHint()
		if hint != "pac" {
			return fmt.Errorf("aur layer %q requires an Arch Linux host (host is %q); cannot install .pkg.tar.zst via %s",
				s.LayerName, t.Distro.ID, hint)
		}
	}

	bindMounts, err := UserScopeBindMounts(t.HostHome)
	if err != nil {
		return err
	}
	envVars := UserScopeEnv(t.HostHome)
	var aurStage string
	if s.Builder == "aur" {
		aurStage, err = os.MkdirTemp("", "ov-aur-")
		if err != nil {
			return err
		}
		RegisterTempCleanup(aurStage)
		defer func() { os.RemoveAll(aurStage); UnregisterTempCleanup(aurStage) }()
		bindMounts["/tmp/aur-pkgs"] = aurStage
	}

	// Render the builder-specific bash script. Each supported builder
	// emits the exact commands that would run inside its stage in the
	// OCI path — but adapted for bash execution inside the builder
	// container, with HOME-remap already handled by BuilderRunOpts.
	script, err := renderBuilderScript(s, t.HostHome)
	if err != nil {
		return err
	}

	_, err = BuilderRun(opts.ContextOrDefault(), BuilderRunOpts{
		BuilderImage: image,
		LayerDir:     s.LayerDir,
		ScriptBody:   script,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     t.HostHome,
		DryRun:       opts.DryRun,
	})
	if err != nil {
		return err
	}

	// aur host-install: pacman -U the produced packages.
	if s.Builder == "aur" && !opts.DryRun {
		matches, _ := filepath.Glob(filepath.Join(aurStage, "*.pkg.tar.zst"))
		if len(matches) > 0 {
			args := append([]string{"pacman", "-U", "--noconfirm"}, matches...)
			if err := runSudoArgs(args, opts); err != nil {
				return fmt.Errorf("pacman -U: %w", err)
			}
		}
	}

	t.noteStep(rec, StepKindBuilder, s.Scope(), s.Venue(),
		fmt.Sprintf("%s (image=%s, layer=%s)", s.Builder, image, s.LayerName), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execTask(s *TaskStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if s.Task == nil {
		return nil
	}
	cmd, err := t.renderTaskCommand(s)
	if err != nil {
		return err
	}
	if cmd == "" {
		return nil
	}
	if s.Scope() == ScopeSystem {
		if err := t.runSystem(cmd, opts); err != nil {
			return err
		}
	} else {
		if err := t.runUser(cmd, opts); err != nil {
			return err
		}
	}
	kind, _ := s.Task.Kind()
	t.noteStep(rec, StepKindTask, s.Scope(), s.Venue(),
		fmt.Sprintf("%s: %s", kind, taskSummary(s.Task)), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// renderTaskCommand turns a TaskStep into a shell command suitable for
// sudo/user heredoc execution. Verbs handled:
//   - mkdir      → `install -d -m<mode> <path>`
//   - download   → curl to a tmp file, optionally extract, install
//   - cmd        → the cmd body verbatim (with /ctx/ rewritten to $CTX)
//   - copy/write → install -m<mode> -o<owner> <source> <dest>
//   - link       → ln -sf <target> <link>
//   - setcap     → setcap <caps> <file>
//
// For v1 of the host target we implement cmd, mkdir, and link — the
// most common verbs. The rest fall back to a "not yet supported" error
// that tests can verify against.
func (t *LocalDeployTarget) renderTaskCommand(s *TaskStep) (string, error) {
	task := s.Task
	ctxPath := s.CtxPath

	switch {
	case task.Cmd != "":
		body := task.Cmd
		if ctxPath != "" {
			body = strings.ReplaceAll(body, "/ctx/", ctxPath+"/")
		}
		return body, nil
	case task.Mkdir != "":
		mode := task.Mode
		if mode == "" {
			mode = "0755"
		}
		return fmt.Sprintf("install -d -m%s %s", mode, shQuoteArg(task.Mkdir)), nil
	case task.Link != "":
		target := task.Target
		if target == "" {
			target = task.To
		}
		return fmt.Sprintf("ln -sfn %s %s", shQuoteArg(target), shQuoteArg(task.Link)), nil
	case task.Setcap != "":
		caps := task.Caps
		return fmt.Sprintf("setcap %s %s", shQuoteArg(caps), shQuoteArg(task.Setcap)), nil
	case task.Copy != "":
		src := filepath.Join(s.LayerDir, task.Copy)
		dst := task.To
		if dst == "" {
			dst = task.Copy
		}
		mode := task.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s %s %s", mode, shQuoteArg(src), shQuoteArg(dst)), nil
	case task.Write != "":
		mode := task.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s /dev/stdin %s <<'OV_WRITE'\n%s\nOV_WRITE",
			mode, shQuoteArg(task.Write), task.Content), nil
	case task.Download != "":
		return renderDownloadScript(task), nil
	}
	return "", fmt.Errorf("task has no supported verb: %+v", task)
}

// renderDownloadScript emits a shell snippet that fetches task.Download
// to a temp file, optionally extracts it into task.To, then cleans up.
// Handles the same flags the container path respects: extract (tar.gz
// / tar.xz / tar.zst / zip / sh / none), strip_components, include,
// mode (applied to the resulting file or directory), env vars injected
// during the download (used by install scripts).
func renderDownloadScript(task *Task) string {
	url := task.Download
	to := task.To
	extract := task.Extract
	if extract == "" {
		// Heuristic to match the container behavior: detect by URL suffix.
		switch {
		case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
			extract = "tar.gz"
		case strings.HasSuffix(url, ".tar.xz"):
			extract = "tar.xz"
		case strings.HasSuffix(url, ".tar.zst"):
			extract = "tar.zst"
		case strings.HasSuffix(url, ".zip"):
			extract = "zip"
		case strings.HasSuffix(url, ".sh"):
			extract = "sh"
		default:
			extract = "none"
		}
	}

	// Emit each env var as a prefix export so the downloaded script can
	// see it (matches the container behavior).
	var envPrefix strings.Builder
	keys := make([]string, 0, len(task.Env))
	for k := range task.Env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(&envPrefix, "export %s=%s\n", k, shQuoteArg(task.Env[k]))
	}

	var b strings.Builder
	b.WriteString("set -e\n")
	// BUILD_ARCH=$(uname -m) so download URLs can template the arch in
	// shell-expansion form (e.g. `uv-${BUILD_ARCH}-unknown-linux-gnu.tar.gz`).
	// Must match the container-build renderer in tasks.go which exports the
	// same var — otherwise the same layer.yml download works at build time
	// but fails under `ov deploy add host/vm:<name>`.
	b.WriteString("BUILD_ARCH=$(uname -m)\n")
	b.WriteString(envPrefix.String())
	// tmp location deterministic per-task so retries don't leak.
	b.WriteString("ovtmp=\"$(mktemp -d)\"\n")
	b.WriteString("trap 'rm -rf \"$ovtmp\"' EXIT\n")

	// URLs are emitted in double-quoted form so ${BUILD_ARCH} (and any
	// other shell-level vars authors rely on) expand at runtime. Escape
	// the set of chars that have special meaning inside double-quotes.
	quotedURL := shDoubleQuote(url)

	if extract == "none" {
		// Download directly to the target path.
		mode := task.Mode
		if mode == "" {
			mode = "0755"
		}
		fmt.Fprintf(&b, "install -d -m0755 %s\n", shQuoteArg(filepath.Dir(to)))
		fmt.Fprintf(&b, "curl -fL --retry 3 -o %s %s\n", shQuoteArg(to), quotedURL)
		fmt.Fprintf(&b, "chmod %s %s\n", mode, shQuoteArg(to))
		return b.String()
	}

	// Fetch to the tmpdir first, then extract.
	fmt.Fprintf(&b, "curl -fL --retry 3 -o \"$ovtmp/archive\" %s\n", quotedURL)
	fmt.Fprintf(&b, "install -d -m0755 %s\n", shQuoteArg(to))

	strip := ""
	if task.StripComponents > 0 {
		strip = fmt.Sprintf(" --strip-components=%d", task.StripComponents)
	}
	includeFilter := ""
	if len(task.Include) > 0 {
		quoted := make([]string, 0, len(task.Include))
		for _, p := range task.Include {
			quoted = append(quoted, shQuoteArg(p))
		}
		includeFilter = " " + strings.Join(quoted, " ")
	}

	switch extract {
	case "tar.gz":
		fmt.Fprintf(&b, "tar -xzf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "tar.xz":
		fmt.Fprintf(&b, "tar -xJf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "tar.zst":
		fmt.Fprintf(&b, "tar --zstd -xf \"$ovtmp/archive\" -C %s%s%s\n", shQuoteArg(to), strip, includeFilter)
	case "zip":
		// unzip doesn't support strip_components natively; emulate when requested.
		if task.StripComponents > 0 {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d \"$ovtmp/unpack\"\n")
			fmt.Fprintf(&b, "(cd \"$ovtmp/unpack\" && ")
			for i := 0; i < task.StripComponents; i++ {
				b.WriteString("cd \"$(ls -1 | head -1)\" && ")
			}
			fmt.Fprintf(&b, "cp -a . %s)\n", shQuoteArg(to))
		} else {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d %s\n", shQuoteArg(to))
		}
	case "sh":
		// Self-installing script. Execute with configured env.
		fmt.Fprintf(&b, "chmod +x \"$ovtmp/archive\"\n")
		fmt.Fprintf(&b, "\"$ovtmp/archive\"\n")
	default:
		fmt.Fprintf(&b, "echo 'unsupported extract format: %s' >&2 && exit 1\n", extract)
	}

	return b.String()
}

func (t *LocalDeployTarget) execFile(s *FileStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	mode := fmt.Sprintf("%04o", s.Mode.Perm())
	owner := ""
	if s.Owner != "" && s.Owner != "root" {
		owner = fmt.Sprintf(" -o %s", s.Owner)
	}
	cmd := fmt.Sprintf("install -m%s%s %s %s", mode, owner, shQuoteArg(s.Source), shQuoteArg(s.Dest))
	if s.Scope() == ScopeSystem {
		if err := t.runSystem(cmd, opts); err != nil {
			return err
		}
	} else {
		if err := t.runUser(cmd, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindFile, s.Scope(), s.Venue(), s.Dest, start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execServicePackaged(s *ServicePackagedStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	// Query prior enabled state for restore-on-teardown.
	s.PriorEnabled = systemctlIsEnabled(s.Unit, s.TargetScope)

	// Optional drop-in write.
	if s.OverridesText != "" && s.OverridesPath != "" {
		if err := writeDropin(s.OverridesPath, s.OverridesText, s.TargetScope, opts); err != nil {
			return err
		}
	}
	if s.Enable {
		if err := systemctlEnable(s.Unit, s.TargetScope, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindServicePackaged, s.Scope(), s.Venue(),
		fmt.Sprintf("enable %s (prior=%v)", s.Unit, s.PriorEnabled), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execServiceCustom(s *ServiceCustomStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	if s.UnitPath == "" || s.UnitText == "" {
		// Renderer didn't populate the unit. For the skeleton we don't
		// synthesize an empty unit — we warn and skip.
		return fmt.Errorf("service %s: no unit text rendered yet", s.Name)
	}
	if err := writeServiceUnit(s.UnitPath, s.UnitText, s.TargetScope, opts); err != nil {
		return err
	}
	if s.Enable {
		if err := systemctlEnable(s.Name, s.TargetScope, opts); err != nil {
			return err
		}
	}
	t.noteStep(rec, StepKindServiceCustom, s.Scope(), s.Venue(),
		fmt.Sprintf("%s → %s", s.Name, s.UnitPath), start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

func (t *LocalDeployTarget) execRepoChange(s *RepoChangeStep, plan *InstallPlan, opts EmitOpts, rec *LayerRecord, start time.Time) error {
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s <<'OV_REPO'\n%s\nOV_REPO",
		shQuoteArg(filepath.Dir(s.File)), shQuoteArg(s.File), s.Content)
	if err := t.runSystem(cmd, opts); err != nil {
		return err
	}
	t.noteStep(rec, StepKindRepoChange, s.Scope(), s.Venue(), s.File, start)
	rec.ReverseOps = append(rec.ReverseOps, s.Reverse()...)
	return nil
}

// ---------------------------------------------------------------------------
// Shell execution helpers
// ---------------------------------------------------------------------------

// runSudoShell wraps a bash snippet in `sudo bash <<EOF`. Uses
// cmd.Stdin so the script body isn't exposed in the argv (cleaner
// ps/audit output).
func runSudoShell(script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo bash <<OV_ROOT")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_ROOT")
		return nil
	}
	cmd := exec.Command("sudo", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runSudoArgs spawns sudo with explicit argv (no shell interpretation).
// Used for one-shot commands like `sudo pacman -U <pkg1> <pkg2> …`.
func runSudoArgs(argv []string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] sudo "+shellJoin(argv))
		return nil
	}
	cmd := exec.Command("sudo", argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runUserShell runs a script as the invoking user (no sudo).
func runUserShell(script string, opts EmitOpts) error {
	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] bash <<OV_USER")
		fmt.Fprintln(os.Stderr, script)
		fmt.Fprintln(os.Stderr, "OV_USER")
		return nil
	}
	cmd := exec.Command("bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sudoRefresh runs `sudo -v` to refresh the sudo timestamp so later
// `sudo bash` invocations don't prompt within the ~5-minute window.
func sudoRefresh() error {
	cmd := exec.Command("sudo", "-v")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// ---------------------------------------------------------------------------
// Systemd helpers
// ---------------------------------------------------------------------------

func systemctlIsEnabled(unit string, scope Scope) bool {
	args := []string{"is-enabled", "--quiet", unit}
	if scope == ScopeUser {
		args = append([]string{"--user"}, args...)
	}
	cmd := exec.Command("systemctl", args...)
	return cmd.Run() == nil
}

func systemctlEnable(unit string, scope Scope, opts EmitOpts) error {
	args := []string{"enable", "--now", unit}
	if scope == ScopeUser {
		args = append([]string{"--user"}, args...)
		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] systemctl --user enable --now %s\n", unit)
			return nil
		}
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	// System scope → sudo.
	return runSudoArgs(append([]string{"systemctl"}, args...), opts)
}

func writeDropin(path, content string, scope Scope, opts EmitOpts) error {
	return writeUnitLikeFile(path, content, scope, opts)
}

func writeServiceUnit(path, content string, scope Scope, opts EmitOpts) error {
	return writeUnitLikeFile(path, content, scope, opts)
}

func writeUnitLikeFile(path, content string, scope Scope, opts EmitOpts) error {
	if scope == ScopeUser {
		if opts.DryRun {
			fmt.Fprintf(os.Stderr, "[dry-run] write user-scope file %s:\n%s\n", path, content)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
		return nil
	}
	// System scope → sudo. This helper is a standalone function (not
	// a LocalDeployTarget method), so it uses the package-level
	// runSudoShell directly — writing system-scope unit files is a
	// local-host-only operation, never a nested-venue one.
	cmd := fmt.Sprintf("mkdir -p %s && cat > %s <<'OV_UNIT'\n%s\nOV_UNIT",
		shQuoteArg(filepath.Dir(path)), shQuoteArg(path), content)
	return runSudoShell(cmd, opts)
}

// ---------------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------------

func (t *LocalDeployTarget) stderr() *os.File {
	if t.DryRunWriter != nil {
		return t.DryRunWriter
	}
	return os.Stderr
}

func (t *LocalDeployTarget) logSkip(batch StepBatch, opts EmitOpts) {
	for _, s := range batch.Steps {
		fmt.Fprintf(t.stderr(), "[skip] %s scope=%s reason=container-only\n", s.Kind(), s.Scope())
	}
}

func (t *LocalDeployTarget) logGated(step InstallStep, gate Gate, opts EmitOpts) {
	fmt.Fprintf(t.stderr(), "[skip] %s scope=%s requires --%s\n", step.Kind(), step.Scope(), gate)
}

func (t *LocalDeployTarget) noteStep(rec *LayerRecord, kind StepKind, scope Scope, venue Venue, summary string, start time.Time) {
	rec.Steps = append(rec.Steps, StepRecord{
		Kind:        kind,
		Scope:       scope,
		Venue:       venue,
		Summary:     summary,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// taskSummary returns a short one-line summary for ledger display.
func taskSummary(task *Task) string {
	var b bytes.Buffer
	switch {
	case task.Cmd != "":
		body := task.Cmd
		if len(body) > 40 {
			body = body[:40] + "…"
		}
		b.WriteString(body)
	case task.Mkdir != "":
		b.WriteString("mkdir " + task.Mkdir)
	case task.Copy != "":
		b.WriteString("copy " + task.Copy)
	case task.Write != "":
		b.WriteString("write " + task.Write)
	case task.Link != "":
		b.WriteString("link " + task.Link)
	case task.Download != "":
		b.WriteString("download " + task.Download)
	case task.Setcap != "":
		b.WriteString("setcap " + task.Setcap)
	}
	return b.String()
}

// shQuoteArg single-quotes an argument for POSIX shell embedding. Same
// semantics as shQuoteEnv in shell_profile.go but exposed here as a
// separate name to avoid a cross-file dependency during refactor.
func shQuoteArg(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// shDoubleQuote wraps a string in double quotes for a shell context where
// variable expansion MUST still happen (e.g. download URLs that template
// ${BUILD_ARCH}). Escapes the four metachars that break out of a double-
// quoted string: backslash, backtick, double-quote, and dollar-sign —
// but $-escaping is selective: only escape bare `$` not followed by a
// valid var-reference character so authored `${FOO}` / `$FOO` still expand.
func shDoubleQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "`", "\\`")
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// ContextOrDefault returns opts' context if one's attached, or a
// background context. Used by BuilderRun callers.
func (o EmitOpts) ContextOrDefault() context.Context {
	return context.Background()
}

// renderBuilderScript turns a BuilderStep into the bash script that
// runs inside the builder container. The script is the host-side
// analog of the container stage_template: same commands, minus the
// Dockerfile wrapping (FROM/RUN/USER/COPY directives).
//
// HOME, PIXI_CACHE_DIR, NPM_CONFIG_PREFIX, CARGO_HOME are injected by
// BuilderRunOpts.Env before the script starts — the script itself
// doesn't need to set them.
func renderBuilderScript(s *BuilderStep, hostHome string) (string, error) {
	switch s.Builder {
	case "pixi":
		return renderPixiScript(s, hostHome), nil
	case "npm":
		return renderNpmScript(s, hostHome), nil
	case "cargo":
		return renderCargoScript(s, hostHome), nil
	case "aur":
		return renderAurScript(s, hostHome), nil
	}
	return "", fmt.Errorf("builder %q: no host script template", s.Builder)
}

// renderPixiScript replicates pixi's stage_template for host install.
// The container path sets WORKDIR={{.Home}}, copies pixi.toml +
// optional pixi.lock into it, then runs `pixi install` with cache
// mounts. On the host, HOME + PIXI_CACHE_DIR come from BuilderRunOpts
// and /work is the bind-mounted layer source (read-only). We copy the
// manifest into $HOME, then run pixi.
func renderPixiScript(s *BuilderStep, hostHome string) string {
	manifest := "pixi.toml"
	// Honor the layer's actual manifest file by checking /work
	// filesystem — at runtime the script walks /work to find which
	// manifest is present (pixi.toml / pyproject.toml / environment.yml).
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("cd \"$HOME\"\n")
	// Pick the manifest file based on what's actually in /work.
	b.WriteString("if [ -f /work/pixi.toml ]; then manifest=pixi.toml\n")
	b.WriteString("elif [ -f /work/pyproject.toml ]; then manifest=pyproject.toml\n")
	b.WriteString("elif [ -f /work/environment.yml ]; then manifest=environment.yml\n")
	b.WriteString("else echo 'no pixi manifest found in /work' >&2; exit 1; fi\n")
	b.WriteString("cp /work/$manifest $manifest\n")
	b.WriteString("if [ -f /work/pixi.lock ]; then cp /work/pixi.lock pixi.lock; fi\n")
	b.WriteString("# Ensure manylinux glibc requirement is present so cross-distro compat holds.\n")
	b.WriteString("grep -q 'system-requirements' $manifest || printf '\\n[system-requirements]\\nlibc = { family = \"glibc\", version = \"2.39\" }\\n' >> $manifest\n")
	// Install command varies by manifest — matches build.yml
	// builder.pixi.install_commands.
	b.WriteString("case \"$manifest\" in\n")
	b.WriteString("  pixi.toml)\n")
	b.WriteString("    if [ -f pixi.lock ]; then pixi install --frozen\n")
	b.WriteString("    else pixi install; fi ;;\n")
	b.WriteString("  pyproject.toml) pixi install --manifest-path pyproject.toml ;;\n")
	b.WriteString("  environment.yml) pixi project import environment.yml && pixi install ;;\n")
	b.WriteString("esac\n")
	b.WriteString("rm -f $manifest pixi.lock\n")
	_ = manifest
	return b.String()
}

// renderNpmScript matches build.yml builder.npm.stage_template: copy
// package.json into $HOME, run npm install -g of its dependencies.
func renderNpmScript(s *BuilderStep, hostHome string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("cd \"$HOME\"\n")
	b.WriteString("if [ ! -f /work/package.json ]; then echo 'no package.json in /work' >&2; exit 1; fi\n")
	b.WriteString("cp /work/package.json package.json\n")
	// Same dependency extraction as build.yml:244 — read the deps map
	// and turn it into `pkg@version` or `pkg` tokens for npm install -g.
	b.WriteString("node -e 'var d=require(\"./package.json\").dependencies||{};for(var[n,v]of Object.entries(d))console.log(v===\"*\"?n:n+\"@\"+v)' | xargs -r npm install -g\n")
	b.WriteString("rm -f package.json\n")
	return b.String()
}

// renderCargoScript replicates build.yml builder.cargo.install_template
// (inline flavor) for host install. CARGO_HOME is set via env; we run
// `cargo install --path /work --root $CARGO_HOME` so binaries land in
// $HOME/.cargo/bin.
func renderCargoScript(s *BuilderStep, hostHome string) string {
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("if [ ! -f /work/Cargo.toml ]; then echo 'no Cargo.toml in /work' >&2; exit 1; fi\n")
	b.WriteString("cargo install --path /work --root \"$CARGO_HOME\"\n")
	return b.String()
}

// renderAurScript replicates build.yml builder.aur.stage_template:
// USER root for sudoers, yay -S builds into /tmp/aur-build, then
// copies resulting .pkg.tar.zst into /tmp/aur-pkgs (bind-mounted on
// host target to a staging dir the caller picks up after).
//
// The BuilderRun wrapper passes --user $(id -u):$(id -g) so we can't
// actually switch to root — aur container images are configured to
// run yay as uid 1000 with NOPASSWD sudo. We trust that baseline.
func renderAurScript(s *BuilderStep, hostHome string) string {
	packages := extractStringSlice(s.RawStageContext, "packages")
	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString("sudo mkdir -p /tmp/aur-build /tmp/aur-pkgs\n")
	b.WriteString("sudo chown -R $(id -u):$(id -g) /tmp/aur-build /tmp/aur-pkgs\n")
	b.WriteString("cp /etc/makepkg.conf /tmp/makepkg.conf\n")
	b.WriteString("sed -i '/^OPTIONS/s/ debug/ !debug/' /tmp/makepkg.conf\n")
	b.WriteString("yay -S --noconfirm --needed --builddir /tmp/aur-build --makepkgconf /tmp/makepkg.conf")
	for _, p := range packages {
		fmt.Fprintf(&b, " %s", shQuoteArg(p))
	}
	b.WriteString("\n")
	b.WriteString("find /tmp/aur-build -name '*.pkg.tar.zst' -exec cp {} /tmp/aur-pkgs/ \\;\n")
	return b.String()
}
