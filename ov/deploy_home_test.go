package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// recordingExec is a DeployExecutor that records PutFile calls (destination +
// staged content) and returns configurable values for RunCapture/ResolveHome.
// GetFile reports not-found so managed-block writes start from empty.
type recordingExec struct {
	homeReturn       string
	runCaptureReturn string
	putDest          string
	putContent       string
}

func (e *recordingExec) Venue() string                                     { return "rec://test" }
func (e *recordingExec) RunSystem(context.Context, string, EmitOpts) error { return nil }
func (e *recordingExec) RunUser(context.Context, string, EmitOpts) error   { return nil }
func (e *recordingExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExec) PutFile(_ context.Context, localPath, remotePath string, _ uint32, _ bool, _ EmitOpts) error {
	e.putDest = remotePath
	b, _ := os.ReadFile(localPath)
	e.putContent = string(b)
	return nil
}
func (e *recordingExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, os.ErrNotExist
}
func (e *recordingExec) RunCapture(context.Context, string) (string, string, int, error) {
	return e.runCaptureReturn, "", 0, nil
}
func (e *recordingExec) Kind() string { return "rec" }
func (e *recordingExec) ResolveHome(context.Context, string) (string, error) {
	return e.homeReturn, nil
}

// D1: the compiler defers home — env.d values carry the {{.Home}} token, not a
// baked image home, so each deploy target resolves them against the real
// destination home at emit.
func TestCompileShellHookStepDefersHome(t *testing.T) {
	layer := &Layer{
		Name: "nodejs",
		envConfig: &EnvConfig{
			Vars:       map[string]string{"NPM_CONFIG_PREFIX": "~/.npm-global"},
			PathAppend: []string{"$HOME/.npm-global/bin"},
		},
	}
	img := &ResolvedImage{Home: "/home/operator"}
	step := compileShellHookStep(layer, img)
	if step == nil {
		t.Fatal("compileShellHookStep returned nil")
	}
	if got := step.EnvVars["NPM_CONFIG_PREFIX"]; got != "{{.Home}}/.npm-global" {
		t.Errorf("env value = %q, want token-deferred {{.Home}}/.npm-global (NOT baked img.Home)", got)
	}
	if got := step.PathAdd[0]; got != "{{.Home}}/.npm-global/bin" {
		t.Errorf("path_append = %q, want {{.Home}}/.npm-global/bin", got)
	}
	if strings.Contains(step.EnvVars["NPM_CONFIG_PREFIX"], "/home/operator") {
		t.Error("compile baked the image home into env.d — that's the VM $HOME bug")
	}
}

// D1: ResolveHome substitutes the token in every home-bearing field but leaves
// TaskStep cmd bodies alone (those shell-expand $HOME at runtime as the deploy
// user, already correct on every venue). Idempotent.
func TestResolveHomeSubstitutesAcrossSteps(t *testing.T) {
	plan := &InstallPlan{Steps: []InstallStep{
		&ShellHookStep{EnvVars: map[string]string{"P": "{{.Home}}/.npm-global"}, PathAdd: []string{"{{.Home}}/bin"}},
		&ShellSnippetStep{Snippet: "export X={{.Home}}/y", Destination: "{{.Home}}/.bashrc", PathAppend: []string{"{{.Home}}/bin"}},
		&FileStep{Dest: "{{.Home}}/.config/foo"},
		&TaskStep{Task: &Task{Cmd: "echo {{.Home}}", Copy: "wrapper"}, To: "{{.Home}}/.local/bin/wrapper"},
	}}
	plan.ResolveHome("/home/cachy")

	sh := plan.Steps[0].(*ShellHookStep)
	if sh.EnvVars["P"] != "/home/cachy/.npm-global" || sh.PathAdd[0] != "/home/cachy/bin" {
		t.Errorf("ShellHookStep not resolved: %+v", sh)
	}
	sn := plan.Steps[1].(*ShellSnippetStep)
	if sn.Snippet != "export X=/home/cachy/y" || sn.Destination != "/home/cachy/.bashrc" || sn.PathAppend[0] != "/home/cachy/bin" {
		t.Errorf("ShellSnippetStep not resolved: %+v", sn)
	}
	fs := plan.Steps[2].(*FileStep)
	if fs.Dest != "/home/cachy/.config/foo" {
		t.Errorf("FileStep.Dest = %q", fs.Dest)
	}
	ts := plan.Steps[3].(*TaskStep)
	if ts.Task.Cmd != "echo {{.Home}}" {
		t.Errorf("TaskStep.Cmd should be untouched (runtime $HOME), got %q", ts.Task.Cmd)
	}
	// The copy/download dest IS resolved — it's the PutFile target (single-quoted
	// under sudo, so it can't shell-expand). A literal "${HOME}" dest would make
	// PutFile create a "/home/cachy/${HOME}/..." dir under sudo (HOME=/root).
	if ts.To != "/home/cachy/.local/bin/wrapper" {
		t.Errorf("TaskStep.To (copy dest) = %q, want /home/cachy/.local/bin/wrapper", ts.To)
	}

	// Idempotent: a second call (token already gone) is a no-op.
	plan.ResolveHome("/home/other")
	if sh.EnvVars["P"] != "/home/cachy/.npm-global" {
		t.Errorf("ResolveHome not idempotent: %q", sh.EnvVars["P"])
	}
}

// D2: the env.d-sourcing managed block is written via the executor to the
// DESTINATION user's home — so a VM deploy writes /home/<guest-user>/.profile,
// not the host operator's home. The block sources the guest's env.d dir.
func TestEnsureManagedBlockViaUsesGuestHome(t *testing.T) {
	rec := &recordingExec{}
	path, err := EnsureManagedBlockVia(context.Background(), rec, ShellBash, "/home/cachy", EmitOpts{})
	if err != nil {
		t.Fatalf("EnsureManagedBlockVia: %v", err)
	}
	// bash → ~/.bashrc (a bash login prefers ~/.bash_profile → ~/.bashrc over
	// ~/.profile, so the env.d block must land in ~/.bashrc to load).
	if path != "/home/cachy/.bashrc" {
		t.Errorf("managed block path = %q, want /home/cachy/.bashrc", path)
	}
	if rec.putDest != "/home/cachy/.bashrc" {
		t.Errorf("PutFile dest = %q, want /home/cachy/.bashrc", rec.putDest)
	}
	if !strings.Contains(rec.putContent, "/home/cachy/.config/overthink/env.d") {
		t.Errorf("managed block doesn't source the guest env.d dir:\n%s", rec.putContent)
	}
	if !strings.Contains(rec.putContent, "overthink:begin") {
		t.Errorf("managed block fence missing:\n%s", rec.putContent)
	}
}

// D2: the guest's login shell is detected from the guest /etc/passwd, not the
// host operator's $SHELL — CachyOS ships fish as the interactive default, so
// the env.d block must land in fish's conf.d, not ~/.profile.
func TestDetectGuestShell(t *testing.T) {
	for _, tc := range []struct {
		passwdShell string
		want        ShellKind
	}{
		{"/usr/bin/fish", ShellFish},
		{"/bin/zsh", ShellZsh},
		{"/bin/bash", ShellBash},
		{"/usr/bin/nonexistent", ShellBash}, // unknown → bash
		{"", ShellBash},                     // detection failure → bash
	} {
		tgt := &VmDeployTarget{Exec: &recordingExec{runCaptureReturn: tc.passwdShell}}
		if got := tgt.detectGuestShell(context.Background()); got != tc.want {
			t.Errorf("detectGuestShell(%q) = %q, want %q", tc.passwdShell, got, tc.want)
		}
	}
}
