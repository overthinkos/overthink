package migrate

// test_support.go — exported entries to a few migrator internals that charly core's
// RELOCATED integration tests call. Those tests verify migrator OUTPUT through the
// CORE loader (LoadUnified / parseNode / buildBundleNode / …, all package-main), so
// they cannot live in this candy; they live in charly/*_test.go and reach the
// migrator's node-form doc-transforms through these thin exported wrappers (C13a).
// Not part of the runtime plugin surface.

import "gopkg.in/yaml.v3"

// MigrationSteps exposes the ordered migration registry (for the host-overlay
// integration test's step lookup).
func MigrationSteps() []MigrationStep { return migrationSteps() }

// MigrateUnifiedNodeDoc applies the unified-node transform to one document.
func MigrateUnifiedNodeDoc(doc *yaml.Node) bool { return migrateUnifiedNodeDoc(doc) }

// EdgeInheritDoc applies the edge-inherit transform to one document.
func EdgeInheritDoc(doc *yaml.Node) bool { return edgeInheritDoc(doc) }

// StepVenueDoc applies the step-venue transform to one document.
func StepVenueDoc(doc *yaml.Node) bool { return stepVenueDoc(doc) }

// RootMappingNode returns the top-level mapping node of a parsed document.
func RootMappingNode(doc *yaml.Node) *yaml.Node { return rootMappingNode(doc) }

// RewriteBoxCandyFile applies the box/candy-rename rewrite to one file.
func RewriteBoxCandyFile(path string, dryRun bool) (bool, error) {
	return rewriteBoxCandyFile(path, dryRun)
}

// MigrateSingleFilenameWithEmbed is the prelift-aware single-filename migrator,
// exposed so the relocated integration test can exercise the build.yml drop path
// (matchesEmbed = the host-prelifted localBuildMatchesEmbeddedVocab verdict).
func MigrateSingleFilenameWithEmbed(dir, hostDeployPath string, dryRun, matchesEmbed bool) ([]string, error) {
	return migrateSingleFilename(dir, hostDeployPath, dryRun, matchesEmbed)
}
