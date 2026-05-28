package main

import "testing"

// These tests pin the resolver-unification cutover: every command's
// image/local name resolution must descend import namespaces through the ONE
// namespace-aware mechanism (splitNamespaceRef / resolveImageRef), instead of a
// flat root-only `c.Image[name]` lookup that silently misses (or truncates at)
// an imported namespace. Each test FAILS against the pre-cutover code.

// fixtureNamespacedProject writes a root project that imports a `sub`
// namespace, with `app` (root, external fedora base) and `sub.widget`
// (namespaced, external fedora base). `app` does NOT base off `sub.widget`, so
// `sub.widget` is NOT reachable as a base — exercising the explicit-target and
// direct-resolve paths rather than the base-reachability pull.
func fixtureNamespacedProject(t *testing.T) (string, *Config) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.144.1443
import:
  - sub: ./sub.yml
image:
  app:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
    layer: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.144.1443
image:
  widget:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
    layer: []
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	return root, uf.ProjectConfig()
}

// TestResolveImage_QualifiedDelegates is the central-chokepoint guard:
// ResolveImage must resolve a namespace-qualified name by delegating into the
// owning namespace Config. Pre-fix, `c.Image["sub.widget"]` missed and this
// returned "image \"sub.widget\" not found".
func TestResolveImage_QualifiedDelegates(t *testing.T) {
	root, cfg := fixtureNamespacedProject(t)

	ri, err := cfg.ResolveImage("sub.widget", "test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveImage(\"sub.widget\") must resolve via namespace delegation: %v", err)
	}
	if ri.Name != "widget" {
		t.Errorf("resolved name = %q, want %q (leaf, resolved in the namespace context)", ri.Name, "widget")
	}
	if ri.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("resolved base = %q, want the namespace image's base", ri.Base)
	}

	// Bare names still resolve in root, unchanged.
	if _, err := cfg.ResolveImage("app", "test", root, ResolveOpts{}); err != nil {
		t.Errorf("bare ResolveImage(\"app\") regressed: %v", err)
	}
	// A genuinely-missing namespace still errors clearly.
	if _, err := cfg.ResolveImage("nope.widget", "test", root, ResolveOpts{}); err == nil {
		t.Error("ResolveImage(\"nope.widget\") should error: no such namespace")
	}
}

// TestFindImageByLeaf covers the discovery dual used by ensure-image's
// build-fallback: a bare basename (extracted from a full registry ref) must be
// found wherever it lives — root or an imported namespace — and returned as the
// qualified ref the build/resolve paths accept.
func TestFindImageByLeaf(t *testing.T) {
	_, cfg := fixtureNamespacedProject(t)

	if got, ok := cfg.findImageByLeaf("app"); !ok || got != "app" {
		t.Errorf("findImageByLeaf(\"app\") = %q,%v; want \"app\",true (root hit, bare)", got, ok)
	}
	if got, ok := cfg.findImageByLeaf("widget"); !ok || got != "sub.widget" {
		t.Errorf("findImageByLeaf(\"widget\") = %q,%v; want \"sub.widget\",true (namespaced hit, qualified)", got, ok)
	}
	if got, ok := cfg.findImageByLeaf("absent"); ok {
		t.Errorf("findImageByLeaf(\"absent\") = %q,true; want \"\",false", got)
	}
}

// TestResolveAllImage_RequestedQualifiedTarget guards the build-target path:
// an explicitly-requested qualified image that is NOT a base/builder of any
// root image must still land in the resolved set (so filterImage / the build
// graph accept `ov image build sub.widget` and the ensure-image build-fallback
// for a namespaced builder). Pre-fix it was absent.
func TestResolveAllImage_RequestedQualifiedTarget(t *testing.T) {
	root, cfg := fixtureNamespacedProject(t)

	// Without RequestedImages, sub.widget is not reachable, so not pulled.
	base, err := cfg.ResolveAllImage("test", root, ResolveOpts{})
	if err != nil {
		t.Fatalf("ResolveAllImage: %v", err)
	}
	if _, present := base["sub.widget"]; present {
		t.Fatal("sub.widget should NOT be in the resolved set without an explicit request (it is not a base of any root image)")
	}

	// With it requested, it is pulled under its fully-qualified key.
	withReq, err := cfg.ResolveAllImage("test", root, ResolveOpts{RequestedImages: []string{"sub.widget"}})
	if err != nil {
		t.Fatalf("ResolveAllImage(RequestedImages): %v", err)
	}
	if _, present := withReq["sub.widget"]; !present {
		t.Errorf("requested qualified target sub.widget absent from resolved set (keys: %v)", keysOf(withReq))
	}
}

// TestWalkBaseChain_RootInternalOnly guards the shared collector iterator's
// semantics: it follows ROOT-internal bases (so the 5 collectors keep walking
// the full same-repo chain) but STOPS at a namespace-qualified base. A
// namespaced base is a separately-built image that owns its own labels;
// descending into it would double-count layers the consumer also lists directly
// (the regression the id-uniqueness validator caught). Namespace descent is a
// name-resolution concern, not a per-image collection concern.
func TestWalkBaseChain_RootInternalOnly(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "overthink.yml", `version: 2026.144.1443
import:
  - sub: ./sub.yml
image:
  parent:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
    layer: []
  child:
    base: parent
    distro: [fedora]
    build: [rpm]
    layer: []
  nschild:
    base: sub.widget
    distro: [fedora]
    build: [rpm]
    layer: []
`)
	writeFixture(t, root, "sub.yml", `version: 2026.144.1443
image:
  widget:
    base: quay.io/fedora/fedora:43
    distro: [fedora]
    build: [rpm]
    layer: []
`)
	uf, _, err := LoadUnified(root)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	cfg := uf.ProjectConfig()

	// Root-internal base IS followed.
	if got := chainNames(cfg.walkBaseChain("child")); len(got) != 2 || got[0] != "child" || got[1] != "parent" {
		t.Errorf("walkBaseChain(\"child\") = %v; want [child, parent] (root-internal base followed)", got)
	}
	// Namespace-qualified base is NOT descended — the walk stops at the boundary
	// so per-image collection doesn't double-count the separately-built base.
	if got := chainNames(cfg.walkBaseChain("nschild")); len(got) != 1 || got[0] != "nschild" {
		t.Errorf("walkBaseChain(\"nschild\") = %v; want [nschild] (stops at namespaced base)", got)
	}
}

func chainNames(nodes []baseChainNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
