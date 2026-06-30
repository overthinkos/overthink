package main

// migrate_testsupport_test.go — test-only shims for the migrate-chain externalization
// (C13a). The package-main `latestSchemaVersion` var moved to charly/plugin/kit with
// the migration registry; several existing core tests still reference the bare
// identifier, so this test-only alias keeps them compiling against the ONE kit copy
// (the production code uses the LatestSchemaVersion() shim).
var latestSchemaVersion = LatestSchemaVersion()
