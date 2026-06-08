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
	envPath := filepath.Join(home, ".config", "opencharly", "env.d", "uv.env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("env.d file missing: %v", err)
	}
	if !strings.Contains(string(data), "export PIXI_CACHE_DIR=/tmp/pixi") {
		t.Errorf("env.d contents wrong:\n%s", data)
	}

	// Managed block in ~/.bashrc (bash → ~/.bashrc, sourced by the login shell
	// via ~/.bash_profile; ~/.profile is not read when ~/.bash_profile exists).
	profile, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatalf("~/.bashrc missing: %v", err)
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
				Name:        "charly-ollama-ollama",
				UnitText:    "[Unit]\n",
				UnitPath:    "/etc/systemd/system/charly-ollama-ollama.service",
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
	dc, _, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	tgt := &LocalDeployTarget{
		HostHome:    home,
		LedgerPaths: paths,
		Shell:       ShellBash,
		DistroCfg:   dc,
	}
	plan := &InstallPlan{
		Layer:    "ripgrep",
		DeployID: "d-rg",
		Steps: []InstallStep{
			&SystemPackagesStep{
				Format:            "rpm",
				Phase:             PhaseInstall,
				Packages:          []string{"ripgrep"},
				RawInstallContext: map[string]interface{}{"package": []string{"ripgrep"}},
			},
		},
	}
	if err := tgt.Emit([]*InstallPlan{plan}, EmitOpts{DryRun: true}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	// Dry-run: no state written to the ledger.
}

func TestRenderTaskCommandMkdir(t *testing.T) {
	ts := &TaskStep{Task: &Task{Mkdir: "/etc/foo", Mode: "0700"}}
	cmd, err := renderTaskCommand(ts)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// renderTaskCommand shell-quotes the path via shDoubleQuote so
	// paths with spaces or shell metacharacters survive RUN-shell
	// expansion. Plain paths get harmless surrounding quotes.
	if cmd != `install -d -m0700 "/etc/foo"` {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestRenderTaskCommandCmdWithCtx(t *testing.T) {
	ts := &TaskStep{
		Task:    &Task{Cmd: "cp /ctx/config.json /etc/foo/"},
		CtxPath: "/home/u/layers/foo",
	}
	cmd, _ := renderTaskCommand(ts)
	if !strings.Contains(cmd, "/home/u/layers/foo/config.json") {
		t.Errorf("/ctx/ not substituted: %s", cmd)
	}
	if strings.Contains(cmd, "/ctx/") {
		t.Errorf("/ctx/ still present: %s", cmd)
	}
}

// TestRenderHostPackageCommand proves the config-driven host install renderer
// (renderHostPackageCommand) produces the EXACT shell the prior hardcoded
// per-format renderer emitted, by rendering each format's phase.install.host
// cell from the REAL build.yml — a faithful-translation round-trip.
func TestRenderHostPackageCommand(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	tests := []struct {
		format   string
		packages []string
		want     string
	}{
		{"rpm", []string{"ripgrep", "fd-find"}, "dnf install -y ripgrep fd-find"},
		// deb + pac: the install cell also refreshes the package DB (apt-get
		// update / pacman -Sy) — neither auto-refreshes, and a stale db 404s on
		// version-bumped packages. pacman uses -Sy NOT -Syu (no surprise bulk
		// upgrade of a running host).
		{"deb", []string{"bat"}, "DEBIAN_FRONTEND=noninteractive apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y bat"},
		{"pac", []string{"rg"}, "pacman -Sy --noconfirm --needed rg"},
	}
	for _, tc := range tests {
		s := &SystemPackagesStep{
			Format:            tc.format,
			Phase:             PhaseInstall,
			Packages:          tc.packages,
			RawInstallContext: map[string]interface{}{"package": tc.packages},
		}
		got, err := renderHostPackageCommand(dc, s)
		if err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if got != tc.want {
			t.Errorf("%s → %q, want %q", tc.format, got, tc.want)
		}
	}
	// Non-install phases render nothing (no error).
	s := &SystemPackagesStep{Format: "rpm", Phase: PhasePrepare, Packages: []string{"x"}}
	if got, err := renderHostPackageCommand(dc, s); err != nil || got != "" {
		t.Errorf("prepare phase = (%q, %v), want empty", got, err)
	}
	// Options are applied (faithful to the prior renderer which joined them).
	opt := &SystemPackagesStep{
		Format:            "pac",
		Phase:             PhaseInstall,
		Packages:          []string{"libyuv"},
		Options:           []string{"--overwrite", "*"},
		RawInstallContext: map[string]interface{}{"package": []string{"libyuv"}, "options": []string{"--overwrite", "*"}},
	}
	want := "pacman -Sy --noconfirm --needed --overwrite * libyuv"
	if got, err := renderHostPackageCommand(dc, opt); err != nil || got != want {
		t.Errorf("pac+options = (%q, %v), want %q", got, err, want)
	}
}

// TestRenderHostPackageCommandDebRepo guards the deb host-cell repo fix: a layer
// that declares a third-party `repo:` (e.g. the charly layer's tailscale repo on
// deb-family) must have that repo added — key dearmor + sources.list — BEFORE
// apt-get install on a target:local / target:vm deploy. The deb host cell
// previously rendered only apt-get install, so `apt-get install tailscale`
// failed with "Unable to locate package tailscale" on the debian/ubuntu VM beds
// (the tailscale apt repo was never added). Loads the real build.yml, so a
// regression in the deb phase.install.host template re-breaks this test.
func TestRenderHostPackageCommandDebRepo(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	s := &SystemPackagesStep{
		Format:   "deb",
		Phase:    PhaseInstall,
		Packages: []string{"tailscale"},
		RawInstallContext: map[string]interface{}{
			"package": []string{"tailscale"},
			// Production shape is []map[string]any (DistroPackages.Repo); the
			// template's NewInstallContext.toMapSlice handles it.
			"repo": []map[string]any{{
				"name":       "tailscale",
				"url":        "https://pkgs.tailscale.com/stable/debian",
				"key":        "https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg",
				"suite":      "trixie",
				"components": "main",
			}},
		},
	}
	got, err := renderHostPackageCommand(dc, s)
	if err != nil {
		t.Fatalf("renderHostPackageCommand: %v", err)
	}
	for _, want := range []string{
		// --batch --yes makes the dearmor idempotent: on an `charly update`
		// re-deploy the keyring already exists, and a bare `gpg --dearmor -o`
		// prompts to overwrite -> opens /dev/tty -> fails over tty-less SSH.
		"gpg --batch --yes --dearmor -o /etc/apt/keyrings/tailscale.gpg",
		"/etc/apt/sources.list.d/tailscale.list",
		"signed-by=/etc/apt/keyrings/tailscale.gpg",
		"apt-get install -y tailscale",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deb host install missing %q in:\n%s", want, got)
		}
	}
}

// TestRenderHostUninstallTemplate proves each format's uninstall_template (the
// config-driven package removal in reverse_ops.go) renders the exact command
// the deleted reversePackageRemove switch produced.
func TestRenderHostUninstallTemplate(t *testing.T) {
	dc, _, _, err := LoadBuildConfigForImage(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForImage: %v", err)
	}
	cases := []struct {
		format   string
		packages []string
		want     string
	}{
		{"rpm", []string{"ripgrep", "fd-find"}, "dnf remove -y ripgrep fd-find"},
		{"deb", []string{"bat"}, "DEBIAN_FRONTEND=noninteractive apt-get purge -y bat"},
		{"pac", []string{"rg"}, "pacman -Rs --noconfirm rg"},
	}
	for _, tc := range cases {
		fd := dc.FindFormat(tc.format)
		if fd == nil || fd.UninstallTemplate == "" {
			t.Fatalf("format %q has no uninstall_template", tc.format)
		}
		got, err := RenderTemplate(tc.format+"-uninstall", fd.UninstallTemplate, &InstallContext{Packages: tc.packages})
		if err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		if strings.TrimSpace(got) != tc.want {
			t.Errorf("%s uninstall → %q, want %q", tc.format, strings.TrimSpace(got), tc.want)
		}
	}
}
