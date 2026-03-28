package main

// testBuildCfg returns the embedded default BuildConfig for tests.
func testBuildCfg() *BuildConfig {
	cfg, err := loadBuildConfig("/nonexistent")
	if err != nil {
		panic("failed to load embedded build config: " + err.Error())
	}
	return cfg
}

// testBuilderCfg returns the embedded default BuilderConfig for tests.
func testBuilderCfg() *BuilderConfig {
	cfg, err := loadBuilderConfig("/nonexistent")
	if err != nil {
		panic("failed to load embedded builder config: " + err.Error())
	}
	return cfg
}
