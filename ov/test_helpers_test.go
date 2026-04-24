package main

import (
	"os"
	"path/filepath"
)

// testdataDir is the project directory used by test fixtures. Tests read
// build config via LoadBuildConfigForImage(testdataDir) which goes through
// the unified loader (overthink.yml + includes).
const testdataDir = "testdata"

// testBuildConfigRef — retained for tests that still reference the legacy
// format_config path. After the unified cutover it's the same as testdataDir
// since the unified loader reads overthink.yml, not a format_config pointer.
const testBuildConfigRef = testdataDir

// testDistroConfig returns the default DistroConfig from testdata fixtures for tests.
func testDistroConfig() *DistroConfig {
	distroCfg, _, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		panic("failed to load distro config from testdata: " + err.Error())
	}
	return distroCfg
}

// testDistroDef returns the resolved DistroDef for the given distro tags.
func testDistroDef(tags ...string) *DistroDef {
	dc := testDistroConfig()
	return dc.ResolveDistro(tags)
}

// testBuilderCfg returns the default BuilderConfig from testdata fixtures for tests.
func testBuilderCfg() *BuilderConfig {
	_, builderCfg, _, err := LoadBuildConfigForImage(testdataDir)
	if err != nil {
		panic("failed to load builder config from testdata: " + err.Error())
	}
	return builderCfg
}

// testProjectDir writes a minimal valid overthink.yml (+ build.yml) to a
// tmpdir and returns its path. Use when a test needs a real project dir
// argument for Validate / ResolveImage calls that no longer tolerate dir="".
// The emitted project has fedora + archlinux + debian + ubuntu distros and
// a pixi builder — enough to cover most fixture Configs without error.
func testProjectDir(t interface {
	TempDir() string
	Fatalf(string, ...interface{})
	Helper()
}) string {
	t.Helper()
	tmpdir := t.TempDir()
	// Reuse testdata's build.yml (and testdata itself as the helper's dir when
	// the caller didn't need tmpdir specifically) — it's a complete fixture.
	root := []byte("version: 4\nincludes: [build.yml]\n")
	if err := os.WriteFile(filepath.Join(tmpdir, "overthink.yml"), root, 0644); err != nil {
		t.Fatalf("writing overthink.yml: %v", err)
	}
	src, err := os.ReadFile("testdata/build.yml")
	if err != nil {
		t.Fatalf("reading testdata/build.yml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpdir, "build.yml"), src, 0644); err != nil {
		t.Fatalf("writing build.yml: %v", err)
	}
	return tmpdir
}

// testFormatSection creates a PackageSection for testing.
func testFormatSection(format string, raw map[string]interface{}) *PackageSection {
	section := &PackageSection{
		FormatName: format,
		Raw:        raw,
	}
	if pkgs, ok := raw["packages"]; ok {
		section.Packages = toStringSlice(pkgs)
	}
	return section
}

// testLayerWithFormat creates a Layer with a single format section for testing.
func testLayerWithFormat(name, format string, raw map[string]interface{}) *Layer {
	section := testFormatSection(format, raw)
	return &Layer{
		Name: name,
		formatSections: map[string]*PackageSection{
			format: section,
		},
	}
}
