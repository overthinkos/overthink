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

// D3: npm/pixi/cargo are now routed to the cross-host home-artifact builder
// (no longer skipped). Verified via the dry-run path so no podman is spawned.
func TestVmExecBuilderRoutesHomeBuilders(t *testing.T) {
	for _, b := range []string{"npm", "pixi", "cargo"} {
		tgt := &VmDeployTarget{
			Exec:                 &recordingExec{},
			BuilderImageResolver: func(string) string { return "test-builder:latest" },
		}
		s := &BuilderStep{Builder: b, LayerName: "x", LayerDir: "/tmp/x"}
		if err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{DryRun: true}); err != nil {
			t.Errorf("execBuilder(%s) dry-run routed to home-artifact builder errored: %v", b, err)
		}
	}
}

// D3: an unknown builder honors --skip-incompatible, and hard-errors otherwise
// with the supported-builder list.
func TestVmExecBuilderUnknown(t *testing.T) {
	tgt := &VmDeployTarget{Exec: &recordingExec{}}
	s := &BuilderStep{Builder: "bogus", LayerName: "x"}

	if err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{SkipIncompatible: true}); err != nil {
		t.Errorf("unknown builder with --skip-incompatible should be skipped, got %v", err)
	}
	err := tgt.execBuilder(context.Background(), s, &InstallPlan{}, EmitOpts{})
	if err == nil || !strings.Contains(err.Error(), "aur, npm, pixi, cargo") {
		t.Errorf("unknown builder without skip should error listing supported builders, got %v", err)
	}
}
