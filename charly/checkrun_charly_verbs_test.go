package main

import (
	"context"
	"strings"
	"testing"
)

// --- §F resolveLocalImageRef tests ---

// withLocalImages swaps ListLocalImages for the duration of the test.
func withLocalImages(t *testing.T, images []LocalImageInfo) {
	t.Helper()
	orig := ListLocalImages
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		return images, nil
	}
	t.Cleanup(func() { ListLocalImages = orig })
}

// withLocalImageExists swaps LocalImageExists for the duration of the test.
func withLocalImageExists(t *testing.T, match func(engine, ref string) bool) {
	t.Helper()
	orig := LocalImageExists
	LocalImageExists = match
	t.Cleanup(func() { LocalImageExists = orig })
}

func TestResolveLocalImageRef_FullRefPresent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool {
		return ref == "ghcr.io/overthinkos/jupyter:latest"
	})
	got, err := resolveLocalImageRef("podman", "ghcr.io/overthinkos/jupyter:latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/overthinkos/jupyter:latest" {
		t.Errorf("full ref should pass through; got %q", got)
	}
}

func TestResolveLocalImageRef_FullRefAbsent(t *testing.T) {
	withLocalImageExists(t, func(engine, ref string) bool { return false })
	_, err := resolveLocalImageRef("podman", "ghcr.io/acme/missing:latest")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected ErrImageNotLocal, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameLabelMatch(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{
			Names:  []string{"ghcr.io/overthinkos/jupyter:latest"},
			Labels: map[string]string{LabelBox: "jupyter"},
		},
		{
			Names:  []string{"ghcr.io/overthinkos/filebrowser:latest"},
			Labels: map[string]string{LabelBox: "filebrowser"},
		},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/overthinkos/jupyter:latest" {
		t.Errorf("label match should return full ref; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameNameMatchFallback(t *testing.T) {
	// No charly label → falls back to repo-name trailing component match.
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/someone-else/jupyter:latest"}},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/someone-else/jupyter:latest" {
		t.Errorf("name fallback should match trailing component; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameLabelPreferredOverName(t *testing.T) {
	// Both a label-matched image AND a name-matched image exist; label wins.
	withLocalImages(t, []LocalImageInfo{
		{
			Names:  []string{"ghcr.io/someone-else/jupyter:latest"},
			Labels: map[string]string{}, // name-only
		},
		{
			Names:  []string{"ghcr.io/overthinkos/jupyter:v2"},
			Labels: map[string]string{LabelBox: "jupyter"},
		},
	})
	got, err := resolveLocalImageRef("podman", "jupyter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ghcr.io/overthinkos/jupyter:v2" {
		t.Errorf("label-matched image should win; got %q", got)
	}
}

func TestResolveLocalImageRef_ShortNameAmbiguousError(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/one/jupyter:latest"}},
		{Names: []string{"ghcr.io/two/jupyter:latest"}},
	})
	_, err := resolveLocalImageRef("podman", "jupyter")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestResolveLocalImageRef_ShortNameNoMatch(t *testing.T) {
	withLocalImages(t, []LocalImageInfo{
		{Names: []string{"ghcr.io/overthinkos/jupyter:latest"}},
	})
	_, err := resolveLocalImageRef("podman", "filebrowser")
	if err == nil || !strings.Contains(err.Error(), "image not found in local storage") {
		t.Errorf("expected ErrImageNotLocal, got: %v", err)
	}
}

// --- runCharlyVerb skip behavior + method allowlist tests ---

func TestRunCharlyVerb_SkipsUnderImageTest(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeBox)
	r.Box = "jupyter"
	res := r.Run(context.Background(), []Op{{Wl: "status"}})
	if len(res) != 1 || res[0].Status != TestSkip {
		t.Fatalf("expected skip under RunModeBox, got %+v", res[0])
	}
	// A runtime-context verb (wl) is skipped in box mode by the context-vs-mode
	// gate (the unified-Op replacement for the per-verb "needs a running
	// container" skip).
	if !strings.Contains(res[0].Message, "not active in box mode") {
		t.Errorf("expected context-not-active skip message, got %q", res[0].Message)
	}
}

func TestRunCharlyVerb_UnknownMethodFails(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	r.Box = "jupyter"
	res := r.Run(context.Background(), []Op{{Wl: "not-a-real-method"}})
	if res[0].Status != TestFail || !strings.Contains(res[0].Message, "unknown method") {
		t.Errorf("expected unknown-method failure, got %+v", res[0])
	}
}

// --- §D validation tests ---

// Unknown live-verb method rejection is now a CUE concern (the per-verb #*Method
// enums) — see TestCueTightening_RejectsAndAccepts "candy cdp bogus method rejected"
// and the mcp/spice/libvirt bogus-method cases.

func TestValidateCharlyVerb_MissingRequiredModifier(t *testing.T) {
	errs := &ValidationError{}
	// wl: click requires X + Y — neither set.
	c := &Op{Wl: "click"}
	validateCharlyVerb(c, "wl", "loc", errs)
	joined := strings.Join(errs.Errors, "\n")
	if !strings.Contains(joined, `modifier "x"`) || !strings.Contains(joined, `modifier "y"`) {
		t.Errorf("expected missing x+y modifier errors, got: %v", errs.Errors)
	}
}

func TestValidateCharlyVerb_BuildContextRejected(t *testing.T) {
	errs := &ValidationError{}
	// A live-container verb pinned to build context must be rejected.
	c := &Op{Wl: "status", Context: []string{"build"}}
	validateCharlyVerb(c, "wl", "loc", errs)
	if !errs.HasErrors() || !strings.Contains(strings.Join(errs.Errors, "\n"), "runtime-context only") {
		t.Errorf("expected runtime-context-only error, got: %+v", errs.Errors)
	}
}

func TestValidateCharlyVerb_ArtifactMethodMissingPath(t *testing.T) {
	errs := &ValidationError{}
	// wl: screenshot requires Artifact.
	c := &Op{Wl: "screenshot"}
	validateCharlyVerb(c, "wl", "loc", errs)
	if !errs.HasErrors() || !strings.Contains(strings.Join(errs.Errors, "\n"), "artifact") {
		t.Errorf("expected artifact-required error, got: %+v", errs.Errors)
	}
}

func TestValidateCharlyVerb_ValidCheckNoErrors(t *testing.T) {
	errs := &ValidationError{}
	c := &Op{Wl: "click", X: 10, Y: 20}
	validateCharlyVerb(c, "wl", "loc", errs)
	if errs.HasErrors() {
		t.Errorf("expected no errors for valid check, got: %+v", errs.Errors)
	}
}

// --- Check.Kind() recognizes the new verbs ---

func TestCheckKind_NewVerbsDispatched(t *testing.T) {
	cases := []struct {
		name string
		c    Op
		verb string
	}{
		{"cdp", Op{Cdp: "status"}, "cdp"},
		{"wl", Op{Wl: "screenshot", Artifact: "/tmp/x"}, "wl"},
		{"dbus", Op{Dbus: "list"}, "dbus"},
		{"vnc", Op{Vnc: "status"}, "vnc"},
		{"record", Op{Record: "list"}, "record"},
		{"spice", Op{Spice: "status"}, "spice"},
		{"libvirt", Op{Libvirt: "info"}, "libvirt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.Kind()
			if err != nil {
				t.Fatalf("Kind() error: %v", err)
			}
			if got != tc.verb {
				t.Errorf("expected verb %q, got %q", tc.verb, got)
			}
		})
	}
}

// --- shortNameMatchesRef edge cases ---

func TestShortNameMatchesRef(t *testing.T) {
	cases := []struct {
		fullRef string
		short   string
		want    bool
	}{
		{"ghcr.io/overthinkos/jupyter:latest", "jupyter", true},
		{"ghcr.io/overthinkos/jupyter", "jupyter", true}, // no tag
		{"localhost/jupyter:v2", "jupyter", true},
		{"jupyter:latest", "jupyter", true}, // no registry
		{"ghcr.io/overthinkos/jupyter:latest", "filebrowser", false},
		{"ghcr.io/overthinkos/something-jupyter:latest", "jupyter", false}, // not a trailing match
	}
	for _, tc := range cases {
		got := shortNameMatchesRef(tc.fullRef, tc.short)
		if got != tc.want {
			t.Errorf("shortNameMatchesRef(%q, %q) = %v, want %v", tc.fullRef, tc.short, got, tc.want)
		}
	}
}

// TestPosKubeRaw_JsonFlagThreaded was removed in the kube → external-plugin
// dep-shed: posKubeRaw (the `charly check kube raw` argv builder) left charly's core
// with the rest of the kube verb. The `json: true` step modifier is now read
// directly off the Op by candy/plugin-kube's runRaw (op.JSON → the full JSON List
// document vs the `<namespace>/<name>` line form).
