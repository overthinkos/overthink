package main

import (
	"strings"
	"testing"
)

// Tests for renderBuilderScript — the bash scripts that run inside
// builder containers during host deploys.

func TestRenderPixiScript(t *testing.T) {
	s := &BuilderStep{Builder: "pixi", LayerName: "pre-commit"}
	out, err := renderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"set -e",
		`cd "$HOME"`,
		"pixi install",
		"system-requirements",
		`cp /work/$manifest $manifest`,
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("missing %q in pixi script:\n%s", m, out)
		}
	}
}

func TestRenderNpmScript(t *testing.T) {
	s := &BuilderStep{Builder: "npm", LayerName: "claude-code"}
	out, err := renderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "npm install -g") {
		t.Errorf("missing npm install -g: %s", out)
	}
	if !strings.Contains(out, "package.json") {
		t.Errorf("missing package.json handling: %s", out)
	}
}

func TestRenderCargoScript(t *testing.T) {
	s := &BuilderStep{Builder: "cargo", LayerName: "mytool"}
	out, err := renderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, `cargo install --path /work --root "$CARGO_HOME"`) {
		t.Errorf("missing cargo install line: %s", out)
	}
}

func TestRenderAurScriptPackages(t *testing.T) {
	s := &BuilderStep{
		Builder:   "aur",
		LayerName: "weird-aur",
		RawStageContext: map[string]interface{}{
			"packages": []string{"some-pkg", "another-pkg"},
		},
	}
	out, err := renderBuilderScript(s, "/home/user")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	mustContain := []string{
		"yay -S --noconfirm --needed",
		"some-pkg",
		"another-pkg",
		"/tmp/aur-pkgs",
		"*.pkg.tar.zst",
		// The DB refresh that keeps the (cached, stale) builder DB from
		// resolving a makedepend to a mirror-rotated version (the go-1.26.3
		// .sig 404). Mirrors build.yml's aur stage_template (R3).
		"pacman -Syu --noconfirm",
	}
	for _, m := range mustContain {
		if !strings.Contains(out, m) {
			t.Errorf("aur script missing %q:\n%s", m, out)
		}
	}
	// The refresh MUST precede yay's makedepend resolution, or it's useless.
	if syncIdx, yayIdx := strings.Index(out, "pacman -Syu --noconfirm"), strings.Index(out, "yay -S"); syncIdx < 0 || yayIdx < 0 || syncIdx > yayIdx {
		t.Errorf("pacman -Syu must come BEFORE yay -S (sync=%d yay=%d):\n%s", syncIdx, yayIdx, out)
	}
}

func TestRenderBuilderScriptUnknownBuilder(t *testing.T) {
	s := &BuilderStep{Builder: "nonexistent"}
	_, err := renderBuilderScript(s, "/home/user")
	if err == nil {
		t.Fatalf("expected error for unknown builder")
	}
}
