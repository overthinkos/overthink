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

// --- runOvVerb skip behavior + method allowlist tests ---

func TestRunOvVerb_SkipsUnderImageTest(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeImage)
	r.Image = "jupyter"
	res := r.Run(context.Background(), []Check{{Cdp: "status"}})
	if len(res) != 1 || res[0].Status != TestSkip {
		t.Fatalf("expected skip under RunModeImage, got %+v", res[0])
	}
	if !strings.Contains(res[0].Message, "requires a running container") {
		t.Errorf("expected message mentioning running container, got %q", res[0].Message)
	}
}

func TestRunOvVerb_UnknownMethodFails(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	r.Image = "jupyter"
	res := r.Run(context.Background(), []Check{{Cdp: "not-a-real-method"}})
	if res[0].Status != TestFail || !strings.Contains(res[0].Message, "unknown method") {
		t.Errorf("expected unknown-method failure, got %+v", res[0])
	}
}

// --- §D validation tests ---

func TestValidateOvVerb_UnknownMethodReportsError(t *testing.T) {
	errs := &ValidationError{}
	c := &Check{Cdp: "bogus"}
	validateOvVerb(c, "cdp", "loc", "deploy", errs)
	if !errs.HasErrors() || !strings.Contains(strings.Join(errs.Errors, "\n"), "unknown method") {
		t.Errorf("expected unknown-method error, got: %+v", errs.Errors)
	}
}

func TestValidateOvVerb_MissingRequiredModifier(t *testing.T) {
	errs := &ValidationError{}
	// cdp: eval requires Tab + Expression — neither set.
	c := &Check{Cdp: "eval"}
	validateOvVerb(c, "cdp", "loc", "deploy", errs)
	joined := strings.Join(errs.Errors, "\n")
	if !strings.Contains(joined, "tab") || !strings.Contains(joined, "expression") {
		t.Errorf("expected missing tab+expression errors, got: %v", errs.Errors)
	}
}

func TestValidateOvVerb_BuildScopeRejected(t *testing.T) {
	errs := &ValidationError{}
	c := &Check{Cdp: "status"}
	validateOvVerb(c, "cdp", "loc", "build", errs)
	if !errs.HasErrors() || !strings.Contains(strings.Join(errs.Errors, "\n"), "scope:\"deploy\"") {
		t.Errorf("expected deploy-scope-required error, got: %+v", errs.Errors)
	}
}

func TestValidateOvVerb_ArtifactMethodMissingPath(t *testing.T) {
	errs := &ValidationError{}
	// wl: screenshot requires Artifact.
	c := &Check{Wl: "screenshot"}
	validateOvVerb(c, "wl", "loc", "deploy", errs)
	if !errs.HasErrors() || !strings.Contains(strings.Join(errs.Errors, "\n"), "artifact") {
		t.Errorf("expected artifact-required error, got: %+v", errs.Errors)
	}
}

func TestValidateOvVerb_ValidCheckNoErrors(t *testing.T) {
	errs := &ValidationError{}
	c := &Check{Cdp: "eval", Tab: "1", Expression: "document.title"}
	validateOvVerb(c, "cdp", "loc", "deploy", errs)
	if errs.HasErrors() {
		t.Errorf("expected no errors for valid check, got: %+v", errs.Errors)
	}
}

// --- Check.Kind() recognizes the new verbs ---

func TestCheckKind_NewVerbsDispatched(t *testing.T) {
	cases := []struct {
		name string
		c    Check
		verb string
	}{
		{"cdp", Check{Cdp: "status"}, "cdp"},
		{"wl", Check{Wl: "screenshot", Artifact: "/tmp/x"}, "wl"},
		{"dbus", Check{Dbus: "list"}, "dbus"},
		{"vnc", Check{Vnc: "status"}, "vnc"},
		{"record", Check{Record: "list"}, "record"},
		{"spice", Check{Spice: "status"}, "spice"},
		{"libvirt", Check{Libvirt: "info"}, "libvirt"},
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

// TestPosK8sRaw_JsonFlagThreaded covers the 2026-04-27 cutover's
// `json: true` recipe modifier passthrough into the underlying
// `charly eval k8s raw --json` invocation. List-mode default emits
// `<namespace>/<name>` per line; --json emits the full JSON List
// document for recipes that author `stdout: { contains: kind }`.
func TestPosK8sRaw_JsonFlagThreaded(t *testing.T) {
	withJSON := posK8sRaw(&Check{K8sResource: "nodes", JSON: true})
	foundJSON := false
	for _, a := range withJSON {
		if a == "--json" {
			foundJSON = true
		}
	}
	if !foundJSON {
		t.Errorf("expected `--json` flag in argv when Check.JSON=true; got %v", withJSON)
	}
	withoutJSON := posK8sRaw(&Check{K8sResource: "nodes", JSON: false})
	for _, a := range withoutJSON {
		if a == "--json" {
			t.Errorf("expected NO `--json` flag when Check.JSON=false; got %v", withoutJSON)
		}
	}
}
