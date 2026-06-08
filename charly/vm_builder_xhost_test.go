package main

import (
	"context"
	"strings"
	"testing"
)

// D3: builder-image resolution order — override > compiled step > resolver,
// hard-error when none resolves.
func TestResolveBuilderImage(t *testing.T) {
	tgt := &VmDeployTarget{BuilderImageResolver: func(string) string { return "from-resolver" }}

	if img, _ := tgt.resolveBuilderImage(&BuilderStep{Builder: "npm", BuilderImage: "from-step"}, EmitOpts{BuilderImageOverride: "from-override"}); img != "from-override" {
		t.Errorf("override should win, got %q", img)
	}
	if img, _ := tgt.resolveBuilderImage(&BuilderStep{Builder: "npm", BuilderImage: "from-step"}, EmitOpts{}); img != "from-step" {
		t.Errorf("compiled step image should win over resolver, got %q", img)
	}
	if img, _ := tgt.resolveBuilderImage(&BuilderStep{Builder: "npm"}, EmitOpts{}); img != "from-resolver" {
		t.Errorf("resolver fallback, got %q", img)
	}
	bare := &VmDeployTarget{}
	if _, err := bare.resolveBuilderImage(&BuilderStep{Builder: "npm", LayerName: "claude-code"}, EmitOpts{}); err == nil {
		t.Error("no image resolvable → expected error")
	}
}

// D3: npm/pixi/cargo are routed to the cross-host home-artifact builder by
// OUTPUT SHAPE (no LocalPkg + a phase.install.host cell), not by builder name.
// Verified via the dry-run path so no podman is spawned. Builder defs come from
// the REAL build.yml so the routing exercises the config-driven host cells.
func TestVmExecBuilderRoutesHomeBuilders(t *testing.T) {
	_, bc, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	for _, b := range []string{"npm", "pixi", "cargo"} {
		tgt := &VmDeployTarget{
			Exec:                 &recordingExec{},
			BuilderImageResolver: func(string) string { return "test-builder:latest" },
		}
		s := &BuilderStep{Builder: b, LayerName: "x", LayerDir: "/tmp/x", BuilderDef: bc.Builder[b]}
		if err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{DryRun: true}); err != nil {
			t.Errorf("execBuilder(%s) dry-run routed to home-artifact builder errored: %v", b, err)
		}
	}
}

// D3: a builder with no phase.install.host cell (no resolved BuilderDef) honors
// --skip-incompatible, and hard-errors otherwise pointing at the missing host
// cell. Routing is by output shape (no LocalPkg → home-artifact path; no host
// cell there → unsupported), not a hardcoded builder-name list.
func TestVmExecBuilderUnknown(t *testing.T) {
	tgt := &VmDeployTarget{Exec: &recordingExec{}}
	s := &BuilderStep{Builder: "bogus", LayerName: "x"}

	if err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{SkipIncompatible: true}); err != nil {
		t.Errorf("unknown builder with --skip-incompatible should be skipped, got %v", err)
	}
	err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{})
	if err == nil || !strings.Contains(err.Error(), "phase.install.host") {
		t.Errorf("unknown builder without skip should error pointing at the missing host cell, got %v", err)
	}
}
