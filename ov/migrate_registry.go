package main

// migrate_registry.go — the single, ordered migration chain behind `ov migrate`.
//
// Every schema cutover the project has ever shipped is one MigrationStep here,
// stamped with the CalVer of the date it landed and ordered chronologically —
// the order in which the cutovers must replay against an arbitrarily-old config.
// `ov migrate` runs the whole chain to HEAD; each step's existing idempotency
// guard makes running the chain whole safe (already-current files are no-ops).
//
// This replaces the former ~16 hand-invoked `ov migrate <name>` sub-verbs. The
// version representation moved from an integer (`version: 4`) to a CalVer
// (`version: 2026.141.1600`): LatestSchemaVersion is the HEAD step's CalVer, the
// curated constant every versioned file is stamped to and the load-time gate
// compares against.
//
// Adding a future cutover = append ONE MigrationStep with a CalVer strictly
// greater than the current HEAD, just before nothing — the calver-schema stamp
// step always stays last so it picks up the new HEAD automatically.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// mustCalVer parses a compile-time-constant CalVer literal, panicking on a
// malformed value. Used only for the registry's hardcoded step versions, so a
// bad literal fails fast at startup rather than silently mis-ordering the chain.
func mustCalVer(s string) CalVer {
	v, ok := ParseCalVer(s)
	if !ok {
		panic("migrate_registry: invalid CalVer literal " + s)
	}
	return v
}

// MigrateContext carries the paths and flags every migration step needs. The
// unified `ov migrate` runner builds one (NewMigrateContext) and passes it to
// each step's Apply.
type MigrateContext struct {
	Dir            string // project directory (overthink.yml + per-kind files + layers/)
	HostDeployPath string // ~/.config/ov/deploy.yml
	HostConfigPath string // ~/.config/ov/config.yml
	SecretsFile    string // <Dir>/.secrets
	QuadletDir     string // ~/.config/containers/systemd
	DryRun         bool
	Out            io.Writer // progress reporting; defaults to os.Stderr when nil
}

// MigrationStep is one ordered transform in the chain. Version is the schema
// CalVer this step lands files at; the registry is sorted ascending by Version.
// Name is for progress / dry-run reporting only — it is no longer a CLI verb.
// TouchesHost marks steps that mutate per-host state (~/.config/ov, quadlets,
// .secrets); the project-only runner (remote-cache auto-migration) skips them.
// Apply runs the transform and reports whether it changed anything, reusing the
// existing Migrate* functions as its body.
type MigrationStep struct {
	Version     CalVer
	Name        string
	TouchesHost bool
	Apply       func(ctx *MigrateContext) (changed bool, err error)
}

// latestSchemaVersion is the curated HEAD CalVer — the schema-generation
// constant every versioned file is stamped to and the value the load-time gate
// requires. It is defined as a standalone var (not derived from the registry's
// last element) to avoid an initialization cycle: the calver-schema step's
// closure references it, and the registry's last entry uses it as its Version,
// so the two are guaranteed equal (asserted by TestRegistryHeadMatchesLatest).
// Bump it — and append the matching MigrationStep — for each future cutover.
var latestSchemaVersion = mustCalVer("2026.155.1801")

// migrationSteps is the ordered registry. Chronological by git landing date
// (see `git log --diff-filter=A` on each migrate_*.go), which is the order the
// cutovers were authored in and therefore the only correct replay order.
func migrationSteps() []MigrationStep {
	return []MigrationStep{
		{mustCalVer("2026.112.522"), "unified", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateUnified(MigrateUnifiedOpts{Dir: c.Dir, RewriteLayers: true, DryRun: c.DryRun})
			return len(w) > 0, err
		}},
		{mustCalVer("2026.114.1558"), "schema-v4", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateSchemaV4Files(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.114.2207"), "description", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDescription(MigrateDescriptionOpts{Dir: c.Dir, DryRun: c.DryRun})
			return len(w) > 0, err
		}},
		{mustCalVer("2026.123.114"), "target-local", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateTargetLocal(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.123.1351"), "calamares", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCalamares(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.124.1942"), "shell-schema", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateShellSchema(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.125.702"), "ov-cachyos", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateOvCachyos(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.125.1107"), "local-images", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateLocalImage(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.125.2355"), "kind-files", false, func(c *MigrateContext) (bool, error) {
			if _, err := os.Stat(filepath.Join(c.Dir, "overthink.yml")); err != nil {
				return false, nil // pre-unified tree; `unified` runs first and creates it
			}
			r, err := MigrateKindFiles(c.Dir, c.DryRun)
			return !r.NoChanges, err
		}},
		{mustCalVer("2026.128.255"), "local-deploy", true, func(c *MigrateContext) (bool, error) {
			changed, _, err := MigrateLocalDeploy(c.HostDeployPath, c.DryRun)
			return changed, err
		}},
		{mustCalVer("2026.128.306"), "quadlets", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateQuadlets(c.QuadletDir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.130.1530"), "field-singular", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateFieldSingular(MigrateFieldSingularOpts{Dir: c.Dir, DryRun: c.DryRun})
			return len(r.Rewritten) > 0, err
		}},
		{mustCalVer("2026.131.857"), "marimo-rename", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateMarimoRename(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.132.1009"), "require-image", true, func(c *MigrateContext) (bool, error) {
			results, warnings, err := MigrateRequireImage(c.Dir, c.DryRun, true)
			for _, w := range warnings {
				fmt.Fprintf(migrateOut(c), "require-image: %s\n", w)
			}
			return len(results) > 0, err
		}},
		{mustCalVer("2026.132.2311"), "tailscale-secrets", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateTailscaleSecretsAuto(c.SecretsFile, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.141.1326"), "drop-kdbx", true, func(c *MigrateContext) (bool, error) {
			w, err := MigrateDropKdbx(c.HostConfigPath, c.DryRun)
			return len(w) > 0, err
		}},
		{mustCalVer("2026.141.1559"), "arch-rename", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateArchRename(c.Dir, c.HostDeployPath, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-05 import-namespace cutover: the `include:` composition key was
		// deleted in favor of the single `import:` statement (flat + namespaced
		// `alias: ref` items). This step renames include: → import: in every
		// project YAML; repo-specific reshaping (base.yml merge, cachyos
		// namespace, deploy→eval beds) is hand-authored. See CHANGELOG.md.
		{mustCalVer("2026.143.843"), "import-namespace", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateImportNamespace(c.Dir, c.DryRun)
			return len(w) > 0, err
		}},
		// 2026-05 per-kind versioning cutover: the per-entity `version:` field
		// became load-bearing (image content-stable label + cross-repo layer
		// resolution). Backfills every layer.yml + bare-base image entry with the
		// HEAD CalVer. TouchesHost is false so remote-cache auto-migration also
		// backfills fetched remote layers (the runtime then hard-errors on an
		// unversioned fetched layer rather than carrying a fallback). See CHANGELOG.md.
		{mustCalVer("2026.144.1442"), "entity-version", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateEntityVersion(c.Dir, latestSchemaVersion.String(), c.DryRun)
			return len(r) > 0, err
		}},
		// 2026-06 singular-label cutover: the OCI label contract + the layer
		// authoring keys went singular (hooks→hook, capabilities→capability,
		// services→service, ports→port, …). The layer KEY renames are handled
		// by the field-singular table (extended this cutover); this step
		// rewrites the remaining label-STRING references a config can carry —
		// build.yml init `label_key: org.overthinkos.service.<init>`, plus any
		// forked `oci_label:` / eval label inspection. Baked image labels are
		// re-emitted singular on the next `ov image build` (hard-cutover
		// rebuild), not by config migration. See CHANGELOG.md.
		{mustCalVer("2026.155.1800"), "singular-label", false, func(c *MigrateContext) (bool, error) {
			r, err := MigrateSingularLabel(c.Dir, c.DryRun)
			return len(r) > 0, err
		}},
		// HEAD — the schema stamp. Must stay LAST so LatestSchemaVersion picks it up
		// and every versioned file lands on this CalVer. This is the integer→CalVer
		// transition step (version: 4 → version: <HEAD>) and the universal stamper.
		// TouchesHost is false so it ALSO runs in project-only mode (remote-cache
		// auto-migration); its host-file stamping is gated on ctx.HostDeployPath,
		// which the project-only runner leaves empty.
		{latestSchemaVersion, "calver-schema", false, func(c *MigrateContext) (bool, error) {
			w, err := MigrateCalverSchema(c.Dir, c.HostDeployPath, latestSchemaVersion, c.DryRun)
			return len(w) > 0, err
		}},
	}
}

// LatestSchemaVersion is the HEAD step's CalVer — the curated schema-generation
// constant every versioned file is stamped to, and the value the load-time gate
// requires. Bumped per future cutover by appending a step (calver-schema stays last).
func LatestSchemaVersion() CalVer {
	return latestSchemaVersion
}

func migrateOut(ctx *MigrateContext) io.Writer {
	if ctx.Out != nil {
		return ctx.Out
	}
	return os.Stderr
}

// NewMigrateContext builds a context for project dir, resolving the per-host
// file paths from the user config dir.
func NewMigrateContext(dir string, dryRun bool) (*MigrateContext, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	hostConfig, err := RuntimeConfigPath()
	if err != nil {
		return nil, err
	}
	return &MigrateContext{
		Dir:            dir,
		HostDeployPath: filepath.Join(cfgDir, "ov", "deploy.yml"),
		HostConfigPath: hostConfig,
		SecretsFile:    filepath.Join(dir, ".secrets"),
		QuadletDir:     filepath.Join(cfgDir, "containers", "systemd"),
		DryRun:         dryRun,
		Out:            os.Stderr,
	}, nil
}

// RunMigrations runs the full chain (project + per-host + quadlets + secrets) to
// HEAD. Returns whether anything changed. Each step is idempotent, so a config
// already at HEAD produces zero changes.
func RunMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, false)
}

// RunProjectMigrations runs only the steps that stay inside the project dir —
// used by the remote-cache auto-migration (refs.go) so fetching a remote repo
// never mutates the user's per-host state.
func RunProjectMigrations(ctx *MigrateContext) (bool, error) {
	return runMigrations(ctx, true)
}

func runMigrations(ctx *MigrateContext, projectOnly bool) (bool, error) {
	out := migrateOut(ctx)
	anyChanged := false
	for _, step := range migrationSteps() {
		if projectOnly && step.TouchesHost {
			continue
		}
		changed, err := step.Apply(ctx)
		if err != nil {
			return anyChanged, fmt.Errorf("migrate step %s (schema %s): %w", step.Name, step.Version, err)
		}
		if changed {
			anyChanged = true
			verb := "applied"
			if ctx.DryRun {
				verb = "would apply"
			}
			fmt.Fprintf(out, "%s %s (schema %s)\n", verb, step.Name, step.Version)
		}
	}
	if !anyChanged {
		fmt.Fprintf(out, "nothing to migrate (already at schema %s)\n", LatestSchemaVersion())
	}
	return anyChanged, nil
}
