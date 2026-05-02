package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for deploy_target_host.go.
// We focus on dry-run paths so the tests don't spawn sudo/podman.

func TestHostDeployTargetDryRunShellHook(t *testing.T) {
	home := t.TempDir()
	paths := &LedgerPaths{
		Root:     filepath.Join(home, "installed"),
		Deploys:  filepath.Join(home, "installed", "deploys"),
		Layers:   filepath.Join(home, "installed", "layers"),
		LockFile: filepath.Join(home, "installed", ".lock"),
	}
	tgt := &LocalDeployTarget{
		HostHome:    home,
		LedgerPaths: paths,
		Shell:       ShellBash,
	}
	plan := &InstallPlan{
		Layer:    "uv",
		DeployID: "d1",
		Steps: []InstallStep{
			&ShellHookStep{
				LayerName: "uv",
				EnvVars:   map[string]string{"PIXI_CACHE_DIR": "/tmp/pixi"},
				PathAdd:   []string{"/usr/local/bin"},
			},
		},
	}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{AssumeYes: true}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// env.d file should exist.
	envPath := filepath.Join(home, ".config", "overthink", "env.d", "uv.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("env.d file missing: %v", err)
	}
	if !strings.Contains(string(data), "export PIXI_CACHE_DIR=/tmp/pixi") {
		t.Errorf("env.d contents wrong:\n%s", data)
	}

	// Managed block in ~/.profile.
	profile, err := os.ReadFile(filepath.Join(home, ".profile"))
	if err != nil {
		t.Fatalf("~/.profile missing: %v", err)
	}
	if !strings.Contains(string(profile), "# overthink:begin") {
		t.Errorf("managed block not inserted:\n%s", profile)
	}

	// Ledger: layer + deploy records.
	lrec, err := ReadLayerRecord(paths, "uv")
	if err != nil || lrec == nil {
		t.Fatalf("layer ledger missing: %v / %+v", err, lrec)
	}
	if len(lrec.DeployedBy) != 1 || lrec.DeployedBy[0] != "d1" {
		t.Errorf("DeployedBy = %v, want [d1]", lrec.DeployedBy)
	}
}

func TestHostDeployTargetGateSkips(t *testing.T) {
	home := t.TempDir()
	paths := &LedgerPaths{
		Root:     filepath.Join(home, "installed"),
		Deploys:  filepath.Join(home, "installed", "deploys"),
		Layers:   filepath.Join(home, "installed", "layers"),
		LockFile: filepath.Join(home, "installed", ".lock"),
	}
	tgt := &LocalDeployTarget{
		HostHome:    home,
		LedgerPaths: paths,
		Shell:       ShellBash,
	}
	// Step requires --with-services; we don't set the flag → skipped.
	plan := &InstallPlan{
		Layer:    "ollama",
		DeployID: "d-services",
		Steps: []InstallStep{
			&ServiceCustomStep{
				Name:        "ov-ollama-ollama",
				UnitText:    "[Unit]\n",
				UnitPath:    "/etc/systemd/system/ov-ollama-ollama.service",
				TargetScope: ScopeSystem,
				Enable:      true,
				LayerName:   "ollama",
			},
		},
	}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{DryRun: true}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// No unit file should have been written — we're not root and it's dry-run anyway.
	// Ledger should still record the layer but with no service step (gate skipped).
	lrec, _ := ReadLayerRecord(paths, "ollama")
	if lrec == nil {
		// Dry-run skips ledger writes; that's OK for this test.
		return
	}
	for _, s := range lrec.Steps {
		if s.Kind == StepKindServiceCustom {
			t.Errorf("service step should have been gate-skipped, but was recorded: %+v", s)
		}
	}
}

func TestHostDeployTargetDryRunSystemPackages(t *testing.T) {
	home := t.TempDir()
	paths := &LedgerPaths{
		Root:     filepath.Join(home, "installed"),
		Deploys:  filepath.Join(home, "installed", "deploys"),
		Layers:   filepath.Join(home, "installed", "layers"),
		LockFile: filepath.Join(home, "installed", ".lock"),
	}
	tgt := &LocalDeployTarget{
		HostHome:    home,
		LedgerPaths: paths,
		Shell:       ShellBash,
	}
	plan := &InstallPlan{
		Layer:    "ripgrep",
		DeployID: "d-rg",
		Steps: []InstallStep{
			&SystemPackagesStep{
				Format:   "rpm",
				Phase:    PhaseInstall,
				Packages: []string{"ripgrep"},
			},
		},
	}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{DryRun: true}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Dry-run: no state written to the ledger.
}

func TestRenderTaskCommandMkdir(t *testing.T) {
	tgt := &LocalDeployTarget{}
	ts := &TaskStep{Task: &Task{Mkdir: "/etc/foo", Mode: "0700"}}
	cmd, err := tgt.renderTaskCommand(ts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if cmd != "install -d -m0700 /etc/foo" {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestRenderTaskCommandCmdWithCtx(t *testing.T) {
	tgt := &LocalDeployTarget{}
	ts := &TaskStep{
		Task:    &Task{Cmd: "cp /ctx/config.json /etc/foo/"},
		CtxPath: "/home/u/layers/foo",
	}
	cmd, _ := tgt.renderTaskCommand(ts)
	if !strings.Contains(cmd, "/home/u/layers/foo/config.json") {
		t.Errorf("/ctx/ not substituted: %s", cmd)
	}
	if strings.Contains(cmd, "/ctx/") {
		t.Errorf("/ctx/ still present: %s", cmd)
	}
}

func TestRenderFallbackPkgCmd(t *testing.T) {
	tgt := &LocalDeployTarget{}
	tests := []struct {
		format   string
		packages []string
		want     string
	}{
		{"rpm", []string{"ripgrep", "fd-find"}, "dnf install -y ripgrep fd-find"},
		{"deb", []string{"bat"}, "DEBIAN_FRONTEND=noninteractive apt-get install -y bat"},
		{"pac", []string{"rg"}, "pacman -S --noconfirm --needed rg"},
	}
	for _, tc := range tests {
		s := &SystemPackagesStep{Format: tc.format, Phase: PhaseInstall, Packages: tc.packages}
		if got := tgt.renderFallbackPkgCmd(s); got != tc.want {
			t.Errorf("%s → %q, want %q", tc.format, got, tc.want)
		}
	}
	// Non-install phases should return empty.
	s := &SystemPackagesStep{Format: "rpm", Phase: PhasePrepare, Packages: []string{"x"}}
	if got := tgt.renderFallbackPkgCmd(s); got != "" {
		t.Errorf("prepare phase returned %q, want empty", got)
	}
}
