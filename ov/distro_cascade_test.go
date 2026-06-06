package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// deriveLayer parses a candy YAML body and runs the Calamares bridge, returning
// the populated Layer (tagSections / formatSections / topPackages).
func deriveLayer(t *testing.T, body string) *Layer {
	t.Helper()
	var ly CandyYAML
	if err := yaml.Unmarshal([]byte(body), &ly); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	layer := &Layer{Name: "t"}
	derivePackageSectionsFromCalamares(layer, &ly)
	return layer
}

// debImg builds a minimal ResolvedBox with a deb primary format and the given
// most-specific-first distro tag chain.
func debImg(chain ...string) *ResolvedBox {
	return &ResolvedBox{
		Pkg:       "deb",
		Distro:    chain,
		DistroDef: &DistroDef{Format: map[string]*FormatDef{"deb": {}}},
	}
}

func pkgStep(t *testing.T, steps []InstallStep) *SystemPackagesStep {
	t.Helper()
	var found *SystemPackagesStep
	n := 0
	for _, s := range steps {
		if sp, ok := s.(*SystemPackagesStep); ok {
			found = sp
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 SystemPackagesStep, got %d", n)
	}
	return found
}

// --- Parser routing -------------------------------------------------------

func TestCascade_BareDistroRoutesToTagSection(t *testing.T) {
	l := deriveLayer(t, `
name: t
distro:
  debian:
    package: [foo]
  ubuntu:
    package: [bar]
`)
	// Bare distro keys land in per-distro TAG sections, NOT a shared "deb" format
	// section (the collapse that caused the non-deterministic repo bug).
	if l.FormatSection("deb") != nil {
		t.Error("bare distro keys must NOT create a deb format section")
	}
	if d := l.TagSection("debian"); d == nil || !reflect.DeepEqual(d.Package, []string{"foo"}) {
		t.Errorf("TagSection(debian).Package = %v, want [foo]", d)
	}
	if u := l.TagSection("ubuntu"); u == nil || !reflect.DeepEqual(u.Package, []string{"bar"}) {
		t.Errorf("TagSection(ubuntu).Package = %v, want [bar]", u)
	}
}

func TestCascade_VersionedAndCompoundKeys(t *testing.T) {
	l := deriveLayer(t, `
name: t
distro:
  debian-13:
    package: [v13]
  "debian,ubuntu":
    package: [shared]
`)
	if d := l.TagSection("debian:13"); d == nil || d.Package[0] != "v13" {
		t.Errorf("debian-13 must route to tag debian:13, got %v", d)
	}
	// Compound splits into one tag section per distro, sharing the content.
	for _, tag := range []string{"debian", "ubuntu"} {
		c := l.TagSection(tag)
		if c == nil || !reflect.DeepEqual(c.Package, []string{"shared"}) {
			t.Errorf("compound tag %q = %v, want [shared]", tag, c)
		}
	}
}

func TestCascade_ArchAurStaysFormatSection(t *testing.T) {
	l := deriveLayer(t, `
name: t
distro:
  arch:
    package: [base]
    aur:
      package: [aur-pkg]
`)
	if a := l.TagSection("arch"); a == nil || a.Package[0] != "base" {
		t.Errorf("arch base must be a tag section, got %v", a)
	}
	// aur is a real build format — it keeps its dedicated format section.
	if aur := l.FormatSection("aur"); aur == nil || aur.Packages[0] != "aur-pkg" {
		t.Errorf("arch.aur must stay a format section, got %v", aur)
	}
}

func TestCascade_TopPackagesNotFoldedAtParse(t *testing.T) {
	l := deriveLayer(t, `
name: t
package: [base-pkg]
distro:
  debian:
    package: [deb-pkg]
`)
	// The top-level base is recorded separately and folded at RESOLVE time —
	// folding at parse is what cross-contaminated debian/ubuntu.
	if !reflect.DeepEqual(l.TopPackages(), []string{"base-pkg"}) {
		t.Errorf("TopPackages() = %v, want [base-pkg]", l.TopPackages())
	}
	if d := l.TagSection("debian"); d == nil || reflect.DeepEqual(d.Package, []string{"base-pkg", "deb-pkg"}) {
		t.Errorf("debian tag must NOT contain the top-level base at parse time, got %v", d.Package)
	}
}

// --- Cascade resolution ---------------------------------------------------

func TestCascade_UnionAndTopBase(t *testing.T) {
	l := deriveLayer(t, `
name: t
package: [base]
distro:
  ubuntu:
    package: [u]
  ubuntu-24.04:
    package: [u2404]
`)
	step := pkgStep(t, compileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), HostContext{}))
	// base (top-level, first) ∪ ubuntu ∪ ubuntu:24.04, deduped.
	if !reflect.DeepEqual(step.Packages, []string{"base", "u", "u2404"}) {
		t.Errorf("packages = %v, want [base u u2404]", step.Packages)
	}
}

func TestCascade_MostSpecificRepoWins(t *testing.T) {
	l := deriveLayer(t, `
name: t
distro:
  ubuntu:
    package: [pkg]
    repo: [{name: r, suite: from-bare}]
  ubuntu-24.04:
    repo: [{name: r, suite: from-version}]
`)
	step := pkgStep(t, compileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), HostContext{}))
	repos := toMapSlice(step.RawInstallContext["repo"])
	if len(repos) != 1 || repos[0]["suite"] != "from-version" {
		t.Errorf("most-specific repo must win: got %v, want suite=from-version", repos)
	}
}

// TestCascade_DeterministicUnderShuffledMap is the regression guard for the
// ORIGINAL bug: debian and ubuntu both declaring a repo used to collapse into
// one mutable "deb" format section whose winner depended on Go's randomized map
// iteration. With per-distro tag sections + sorted derive, the SAME repo
// resolves every time regardless of authoring/map order.
func TestCascade_DeterministicRepoPerDistro(t *testing.T) {
	body := `
name: t
distro:
  debian:
    package: [tailscale]
    repo: [{name: tailscale, suite: trixie}]
  ubuntu:
    package: [tailscale]
    repo: [{name: tailscale, suite: noble}]
`
	for i := 0; i < 50; i++ { // many iterations to defeat any map-order flakiness
		l := deriveLayer(t, body)
		deb := pkgStep(t, compileSystemPackageSteps(l, debImg("debian:13", "debian"), HostContext{}))
		ubu := pkgStep(t, compileSystemPackageSteps(l, debImg("ubuntu:24.04", "ubuntu"), HostContext{}))
		if s := toMapSlice(deb.RawInstallContext["repo"]); len(s) != 1 || s[0]["suite"] != "trixie" {
			t.Fatalf("iter %d: debian must resolve trixie, got %v", i, s)
		}
		if s := toMapSlice(ubu.RawInstallContext["repo"]); len(s) != 1 || s[0]["suite"] != "noble" {
			t.Fatalf("iter %d: ubuntu must resolve noble, got %v", i, s)
		}
	}
}

func TestCascade_FedoraArchBareReach(t *testing.T) {
	// A bare fedora image ([fedora]) must reach the fedora tag section — there is
	// no format-section fallback anymore.
	l := deriveLayer(t, `
name: t
distro:
  fedora:
    package: [vim]
`)
	img := &ResolvedBox{Pkg: "rpm", Distro: []string{"fedora"},
		DistroDef: &DistroDef{Format: map[string]*FormatDef{"rpm": {}}}}
	step := pkgStep(t, compileSystemPackageSteps(l, img, HostContext{}))
	if !reflect.DeepEqual(step.Packages, []string{"vim"}) {
		t.Errorf("fedora bare reach: packages = %v, want [vim]", step.Packages)
	}
}

func TestCascade_TopOnlyLayerInstallsEverywhere(t *testing.T) {
	// A layer with only a top-level package: (no distro:) installs that base on
	// any image via the primary format.
	l := deriveLayer(t, "name: t\npackage: [nodejs, npm]\n")
	step := pkgStep(t, compileSystemPackageSteps(l, debImg("debian:13", "debian"), HostContext{}))
	if !reflect.DeepEqual(step.Packages, []string{"nodejs", "npm"}) {
		t.Errorf("top-only base: packages = %v, want [nodejs npm]", step.Packages)
	}
}

// --- distroTagChain -------------------------------------------------------

func TestDistroTagChain(t *testing.T) {
	cases := []struct {
		distro, version string
		want            []string
	}{
		{"ubuntu", "24.04", []string{"ubuntu:24.04", "ubuntu"}},
		{"debian", "13", []string{"debian:13", "debian"}},
		{"arch", "", []string{"arch"}}, // rolling — bare only
		{"", "", nil},
	}
	for _, c := range cases {
		if got := distroTagChain(c.distro, c.version); !reflect.DeepEqual(got, c.want) {
			t.Errorf("distroTagChain(%q,%q) = %v, want %v", c.distro, c.version, got, c.want)
		}
	}
}

func TestDistroDefVersionInherits(t *testing.T) {
	dc := &DistroConfig{Distro: map[string]*DistroDef{
		"debian": {Version: "13", Bootstrap: BootstrapDef{InstallCmd: "apt"}},
		"ubuntu": {Inherits: "debian", Version: "24.04", Bootstrap: BootstrapDef{InstallCmd: "apt"}},
		"cachy":  {Inherits: "debian", Bootstrap: BootstrapDef{InstallCmd: "apt"}}, // no own version
	}}
	if v := dc.resolveInherits(dc.Distro["ubuntu"], 10).Version; v != "24.04" {
		t.Errorf("ubuntu version = %q, want 24.04 (child wins)", v)
	}
	if v := dc.resolveInherits(dc.Distro["cachy"], 10).Version; v != "13" {
		t.Errorf("cachy version = %q, want inherited 13", v)
	}
}

// --- Migration safety net -------------------------------------------------

// TestCalamares_MigratesLegacyFormsToDistroMap proves the EXISTING calamares
// migrator converts every legacy top-level form (format keys + compound tags)
// into the `distro:` map — so the parser's hard-cutover rejection of those forms
// is backed by a real migration path (no new migrate step needed).
func TestCalamares_MigratesLegacyFormsToDistroMap(t *testing.T) {
	dir := t.TempDir()
	layerDir := filepath.Join(dir, "layers", "legacy")
	if err := os.MkdirAll(layerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// True pre-calamares legacy form: plural `packages:` under format/compound keys.
	legacy := "layer:\n  name: legacy\n  version: \"1\"\n  rpm:\n    packages: [vim-rpm]\n  deb:\n    packages: [vim-deb]\n  \"debian,ubuntu\":\n    packages: [shared]\n"
	if err := os.WriteFile(filepath.Join(layerDir, "layer.yml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateCalamares(dir, false); err != nil {
		t.Fatalf("MigrateCalamares: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(layerDir, "layer.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Legacy top-level keys are gone; a distros map carries the packages per distro.
	for _, want := range []string{"distros:", "fedora:", "debian:", "ubuntu:", "vim-rpm", "vim-deb", "shared"} {
		if !strings.Contains(s, want) {
			t.Errorf("migrated layer.yml missing %q\n--- got ---\n%s", want, s)
		}
	}
	if strings.Contains(s, "\n  rpm:\n") || strings.Contains(s, "\n  deb:\n") {
		t.Errorf("legacy top-level format keys survived migration:\n%s", s)
	}
}
