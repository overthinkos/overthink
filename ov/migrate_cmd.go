package main

import (
	"fmt"
	"os"
)

// MigrateCmdGroup groups `ov migrate` subcommands.
//
// Retired migration commands (deleted with the bootstrap-builder cutover):
//   - vm-spec: legacy `bootc: true` schema migration; replaced by layer
//     capability aggregation (no migration required — bootc-config layer
//     contributes preserve_user, no image-level flag exists).
//   - merge-vms: legacy vms.yml -> deploy.yml merge + arch-cloud-base
//     rename; project is on schema v4, no remaining input.
//   - deploy-v3: deploy.yml v2 -> v3 migration; project is on v4.
//   - eval: harness.yml -> eval.yml + tests:->eval: rename; project is
//     on eval.yml already.
type MigrateCmdGroup struct {
	Unified     MigrateUnifiedCmd     `cmd:"" help:"Migrate build.yml/image.yml/deploy.yml/layer.yml to the unified overthink.yml format"`
	SchemaV4    MigrateSchemaV4Cmd    `cmd:"schema-v4" help:"Migrate schema v3 → v4: flatten deployments.images→deployment, rename plurals to singular, children:→nested:, remove deploy-choice fields from images, bump version 3→4"`
	Description MigrateDescriptionCmd `cmd:"description" help:"Scaffold a Gherkin-shaped description: block on every kind-keyed entity that has legacy info:/status: text but no description:"`
	TargetLocal MigrateTargetLocalCmd `cmd:"target-local" help:"Rename kind:host → kind:local (host.yml → local.yml, target:host → target:local, host:<template> → local:<template>); drop legacy status:/info: scalars + VmDeployState.ssh_key_path; idempotent"`
	Calamares   MigrateCalamaresCmd   `cmd:"calamares" help:"Align layer.yml authoring with Calamares vocabulary: rename depends:→requires:, collapse rpm:/deb:/pac:/aur: + per-distro tag sections (debian:13:, ubuntu:24.04:, debian,ubuntu:) into top-level packages: + per-distro distros: map; AUR under distros.archlinux.aur; delete dead directory:/info: keys; idempotent"`
	ShellSchema MigrateShellSchemaCmd `cmd:"shell-schema" help:"Convert legacy cmd: shell-rc heredoc tasks (the # overthink:begin direnv-hook / ssh-auth-sock fence patterns) into the structured shell: schema; idempotent"`
	QcRename    MigrateQcRenameCmd    `cmd:"qc-rename" help:"Rename the operator-specific 'qc' deployment key to 'cachyos-dx' in overthink.yml + ~/.config/ov/deploy.yml; demonstrates cross-kind name reuse (kind:local + kind:deployment share the name); idempotent"`
}

// MigrateUnifiedCmd is `ov migrate unified`. The project directory is taken
// from the top-level --dir / -C / OV_PROJECT_DIR flag (no local --dir here to
// avoid clashing with the parent flag).
type MigrateUnifiedCmd struct {
	Monolithic    bool `long:"monolithic" help:"Emit a single flat overthink.yml instead of using includes:"`
	DryRun        bool `long:"dry-run" help:"Print files that would be written, don't touch the filesystem"`
	RewriteLayers bool `long:"rewrite-layers" help:"Rewrite layer.yml files into kind-keyed form (layer: {name, ...})"`
}

func (c *MigrateUnifiedCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	dir := cwd
	written, err := MigrateUnified(MigrateUnifiedOpts{
		Dir:           dir,
		Monolithic:    c.Monolithic,
		DryRun:        c.DryRun,
		RewriteLayers: c.RewriteLayers,
	})
	if err != nil {
		return err
	}
	prefix := ""
	if c.DryRun {
		prefix = "[dry-run] would write "
	} else {
		prefix = "wrote "
	}
	for _, p := range written {
		fmt.Println(prefix + p)
	}
	return nil
}
