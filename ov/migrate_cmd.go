package main

import (
	"fmt"
	"os"
)

// MigrateCmdGroup groups `ov migrate` subcommands.
type MigrateCmdGroup struct {
	Unified  MigrateUnifiedCmd  `cmd:"" help:"Migrate build.yml/image.yml/deploy.yml/layer.yml to the unified overthink.yml format"`
	VmSpec   MigrateVmSpecCmd   `cmd:"vm-spec" help:"Migrate legacy bootc:true + image.vm: + image.libvirt: fields to kind:vm entities"`
	MergeVms MigrateMergeVmsCmd `cmd:"merge-vms" help:"Merge vms.yml into deploy.yml, rename vms:→vm:, rename arch-cloud-base→arch, bump schema v1→v2"`
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
