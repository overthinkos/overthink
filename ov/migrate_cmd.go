package main

import (
	"fmt"
	"os"
)

// MigrateCmdGroup groups `ov migrate` subcommands.
type MigrateCmdGroup struct {
	Unified        MigrateUnifiedCmd        `cmd:"" help:"Migrate build.yml/image.yml/deploy.yml/layer.yml to the unified overthink.yml format"`
	VmSpec         MigrateVmSpecCmd         `cmd:"vm-spec" help:"Migrate legacy bootc:true + image.vm: + image.libvirt: fields to kind:vm entities"`
	MergeVms       MigrateMergeVmsCmd       `cmd:"merge-vms" help:"Merge vms.yml into deploy.yml, rename vms:→vm:, rename arch-cloud-base→arch, bump schema v1→v2"`
	DeploySchemaV3 MigrateDeploySchemaV3Cmd `cmd:"deploy-v3" help:"Migrate deploy.yml to schema v3: rename vm:X→X-vm, container→pod, kubernetes→k8s, vm_source:→vm:, bump version 2→3"`
	SchemaV4       MigrateSchemaV4Cmd       `cmd:"schema-v4" help:"Migrate schema v3 → v4: flatten deployments.images→deployment, rename plurals to singular, children:→nested:, remove deploy-choice fields from images, bump version 3→4"`
	Description    MigrateDescriptionCmd    `cmd:"description" help:"Scaffold a Gherkin-shaped description: block on every kind-keyed entity that has legacy info:/status: text but no description:"`
	Harness        MigrateHarnessCmd        `cmd:"harness" help:"Migrate legacy benchmark: block in overthink.yml to harness.yml (kind:ai + kind:recipe entities); rewrite description.scenarios:→scenario: and tags:→tag: project-wide"`
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
