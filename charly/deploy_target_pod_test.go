package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPodOverlayInlineCopyResolvesUnderContext guards the add_candy-on-pod overlay
// build: a write: step's inline content is staged to <BuildDir>/_inline/<candy>/<hash>
// and the matching Containerfile COPY references it relative to the build context.
// The overlay OCITarget must set ContextRelPrefix == BuildDir (the overlay build dir,
// relative to the build-context root); with an empty ContextRelPrefix the COPY drops
// the build-dir prefix and resolves to a non-existent path, failing the overlay build
// with `COPY … _inline/<candy>/<hash>: stat: no such file or directory`. Regression
// for that failure; mirrors the full build's contextRelPrefix = .build/<boxName>.
func TestPodOverlayInlineCopyResolvesUnderContext(t *testing.T) {
	ctxRoot := t.TempDir() // the build-context root (the project dir)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(ctxRoot); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	relBuildDir := filepath.Join(".build", "overlay-test")

	gen := &Generator{Dir: ctxRoot, Candies: map[string]*Candy{"marker": {Name: "marker"}}}
	pdt := &PodDeployTarget{Generator: gen, Box: &ResolvedBox{Name: "base"}}
	oci := pdt.overlayOCITarget(relBuildDir)

	// Invariant: inline COPY paths resolve only when ContextRelPrefix == BuildDir.
	if oci.ContextRelPrefix != oci.BuildDir {
		t.Fatalf("overlay OCITarget: ContextRelPrefix=%q != BuildDir=%q — inline COPY paths will not resolve",
			oci.ContextRelPrefix, oci.BuildDir)
	}

	op := &Op{Write: "/etc/marker", Content: "POD-ADDCANDY-MARKER-OK v1\n", Mode: "0644", RunAs: "root"}
	plans := []*InstallPlan{{Candy: "marker", Steps: []InstallStep{&OpStep{Op: op, CandyName: "marker", ResolvedUser: "root"}}}}
	if err := oci.Emit(plans, EmitOpts{}); err != nil {
		t.Fatalf("overlay emit: %v", err)
	}

	src := inlineCopySrc(t, oci.String())
	// src is relative to the build context (ctxRoot); the staged file must exist there.
	if _, err := os.Stat(filepath.Join(ctxRoot, src)); err != nil {
		t.Fatalf("inline COPY src %q does not resolve to a staged file under the build context: %v", src, err)
	}
}

// inlineCopySrc extracts the COPY source token (the _inline/... path) from a
// rendered Containerfile fragment containing a single inline write COPY.
func inlineCopySrc(t *testing.T, containerfile string) string {
	t.Helper()
	for _, line := range strings.Split(containerfile, "\n") {
		if !strings.HasPrefix(line, "COPY ") {
			continue
		}
		for _, tok := range strings.Fields(line) {
			if strings.Contains(tok, "_inline/") {
				return tok
			}
		}
	}
	t.Fatalf("no inline COPY directive found in:\n%s", containerfile)
	return ""
}
