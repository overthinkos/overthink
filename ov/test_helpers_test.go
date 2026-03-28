package main

// testDistroConfig returns the default DistroConfig from testdata fixtures for tests.
func testDistroConfig() *DistroConfig {
	refs := &FormatConfigRefs{
		Distro:  "testdata/defaults/distro.yml",
		Builder: "testdata/defaults/builder.yml",
	}
	distroCfg, _, err := LoadFormatConfigsForImage(nil, refs, ".")
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
	refs := &FormatConfigRefs{
		Distro:  "testdata/defaults/distro.yml",
		Builder: "testdata/defaults/builder.yml",
	}
	_, builderCfg, err := LoadFormatConfigsForImage(nil, refs, ".")
	if err != nil {
		panic("failed to load builder config from testdata: " + err.Error())
	}
	return builderCfg
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
