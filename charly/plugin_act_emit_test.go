package main

import (
	"strings"
	"testing"
)

// The act-emit enabler renders a run: step whose verb is a state-provision plugin
// (plugin: <verb> + plugin_input, the provider implementing ProvisionActor) into shell at
// install emit — the gap that opened once unix_group left #Op. Both DeployTarget emit
// paths (renderOpCommand for the local/vm targets, emitOp for the OCI build) reach the
// provider's RenderProvisionScript via the shared resolveProvisionScript seam (R3).

// unixGroupActStep is the canonical exercise op: a `run:` step authoring the extracted
// unix_group verb as a plugin (groupadd checkgrp with gid 4242).
func unixGroupActStep() *OpStep {
	gid := 4242
	return &OpStep{
		Op:        &Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "checkgrp", "gid": gid}},
		CandyName: "lyr",
	}
}

// renderOpCommand (the local/vm deploy emit) turns a plugin: unix_group run-Op into the
// idempotent groupadd shell.
func TestRenderOpCommand_PluginAct_UnixGroup(t *testing.T) {
	cmd, err := renderOpCommand(unixGroupActStep())
	if err != nil {
		t.Fatalf("renderOpCommand: %v", err)
	}
	for _, want := range []string{"groupadd", "-g 4242", "checkgrp"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("renderOpCommand = %q, want substring %q", cmd, want)
		}
	}
}

// A run: step whose plugin verb is NOT a ProvisionActor (an observe-only verb) has no
// build/deploy install path — renderOpCommand errors loudly rather than silently dropping
// the step (R4: no silent drop).
func TestRenderOpCommand_PluginAct_NotActCapable(t *testing.T) {
	s := &OpStep{Op: &Op{Plugin: "process", PluginInput: map[string]any{"process": "bash"}}}
	if _, err := renderOpCommand(s); err == nil {
		t.Fatalf("renderOpCommand(plugin: process) err=nil, want a not-act-capable error")
	}
}

// rawUnixGroupOp is the RAW plan op the box build walks straight into emitTasks —
// Plugin set, NO pre-conversion (the actual shipping shape, not an OpStep wrapper).
func rawUnixGroupOp() Op {
	return Op{Plugin: "unix_group", PluginInput: map[string]any{"unix_group": "checkgrp", "gid": 4242}}
}

// emitTasks IS the real box-build emit path (writeCandySteps → g.emitTasks walks the
// candy's runOps straight here). It must render a RAW plugin: unix_group run-Op — with NO
// pre-conversion — into a Containerfile RUN carrying the groupadd. This guards the
// regression a direct-emitOp test missed: emitOp pre-converted plugin→command, but the box
// build never goes through emitOp, so a missing `case "plugin"` in emitTasks silently
// dropped the groupadd as `# unknown verb "plugin"`.
func TestEmitTasks_PluginAct_UnixGroup(t *testing.T) {
	dir := t.TempDir()
	layer := &Candy{Name: "lyr"}
	g := &Generator{BuildDir: dir}
	var b strings.Builder
	if _, err := g.emitTasks(&b, layer, testResolvedBox(), []Op{rawUnixGroupOp()}, dir, ".build/test-img"); err != nil {
		t.Fatalf("emitTasks: %v", err)
	}
	out := b.String()
	for _, want := range []string{"RUN", "groupadd", "checkgrp", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitTasks Containerfile = %q, want substring %q", out, want)
		}
	}
	if strings.Contains(out, `unknown verb "plugin"`) {
		t.Errorf("the raw plugin op was DROPPED as an unknown verb (the box-build regression):\n%s", out)
	}
}

// emitOp (the OCI pod-overlay path) delegates the same RAW plugin op to emitTasks — proving
// the pod-overlay build and the box build share the ONE `case "plugin"` seam (no
// emitOp-local pre-conversion).
func TestEmitOp_PluginAct_UnixGroup_OCI(t *testing.T) {
	dir := t.TempDir()
	layer := &Candy{Name: "lyr"}
	g := &Generator{BuildDir: dir, Candies: map[string]*Candy{"lyr": layer}}
	tgt := &OCITarget{Generator: g, Box: testResolvedBox(), BuildDir: dir, ContextRelPrefix: ".build/test-img"}
	op := rawUnixGroupOp()
	if err := tgt.emitOp(&OpStep{Op: &op, CandyName: "lyr"}); err != nil {
		t.Fatalf("emitOp: %v", err)
	}
	out := tgt.buf.String()
	for _, want := range []string{"RUN", "groupadd", "checkgrp", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitOp Containerfile = %q, want substring %q", out, want)
		}
	}
}
