package migrate

// aliases.go — the kit-backed aliases that let the verbatim-moved migration chain
// reference the shared types/constants/helpers it used to find in charly's package
// main. The PARSED CalVer, the schema HEAD, the plural-key map, the project path
// constants, the ledger format version, and the MigrateContext all live in
// charly/plugin/kit (the ONE copy core also imports — R3); these aliases keep every
// moved migrator's bareword references compiling without per-file edits.

import (
	"github.com/overthinkos/overthink/charly/plugin/kit"
	"github.com/overthinkos/overthink/charly/spec"
)

// MigrateContext is the migration runtime context (shared with core via kit).
type MigrateContext = kit.MigrateContext

// CUE-single-source param aliases the chain references by their charly names. Each
// is a spec type (spec/charly_names.go) — the candy imports the ONE generated copy.
type (
	BoxConfig  = spec.BoxConfig  // = spec.Box
	DistroDef  = spec.DistroDef  // = spec.Distro
	BuilderDef = spec.BuilderDef // = spec.Builder
	InitDef    = spec.InitDef    // = spec.Init
	BundleNode = spec.BundleNode // = spec.Deploy
)

// CalVer is the parsed schema version (shared with core via kit).
type CalVer = kit.CalVer

// ParseCalVer / mustCalVer / LatestSchemaVersion / latestSchemaVersion — the CalVer
// surface the registry + migrators use, backed by the ONE kit copy.
var (
	ParseCalVer         = kit.ParseCalVer
	mustCalVer          = kit.MustCalVer
	latestSchemaVersion = kit.LatestSchemaVersion()
)

// LatestSchemaVersion is the HEAD schema CalVer (kit-backed).
func LatestSchemaVersion() CalVer { return kit.LatestSchemaVersion() }

// Project path constants + the ledger format version (kit-backed; core aliases the
// same kit values).
const (
	UnifiedFileName     = kit.UnifiedFileName
	DefaultBoxDir       = kit.DefaultBoxDir
	DefaultCandyDir     = kit.DefaultCandyDir
	ledgerSchemaVersion = kit.LedgerSchemaVersion
)

// pluralToSingularYAMLKeys is the canonical plural→singular map (kit-backed; the
// load-time RejectLegacyPluralKeys gate in core reads the same kit copy — R3).
var pluralToSingularYAMLKeys = kit.PluralToSingularYAMLKeys

// Generic helper aliases — the small shared helpers the chain references, living
// ONCE in kit (core aliases the same copies in kit_aliases.go — R3).
var (
	fileExists                     = kit.FileExists
	dirExists                      = kit.DirExists
	sortStrings                    = kit.SortStrings
	firstNonEmpty                  = kit.FirstNonEmpty
	mapHasKey                      = kit.MapHasKey
	mapValue                       = kit.MapValue
	nodeShapedValue                = kit.NodeShapedValue
	firstYAMLVersionLine           = kit.FirstYAMLVersionLine
	isGitSubmoduleDir              = kit.IsGitSubmoduleDir
	hasLegacyImagesKey             = kit.HasLegacyImagesKey
	stripLegacyOverthinkBlocks     = kit.StripLegacyOverthinkBlocks
	migrateSkipDir                 = kit.MigrateSkipDir
	isNestedGitRepo                = kit.IsNestedGitRepo
	rewriteLegacyLocalImagesInFile = kit.RewriteLegacyLocalImagesInFile
	scanLegacyLocalImagesInFile    = kit.ScanLegacyLocalImagesInFile
	scalarNode                     = kit.ScalarNode
	findMappingValue               = kit.FindMappingValue
	migrateCandidateYAMLFiles      = kit.MigrateCandidateYAMLFiles
	opUnifyCandidateFiles          = kit.OpUnifyCandidateFiles
)

// EnvdDir is the per-host env.d path helper (kit-backed).
func EnvdDir(hostHome string) string { return kit.EnvdDir(hostHome) }

// LegacyImagesBlock is the legacy-images scan result type (kit-backed).
type LegacyImagesBlock = kit.LegacyImagesBlock

// StepKeyword + the plan keyword constants (the type is a spec type; the constant
// values live in kit — R3).
type StepKeyword = spec.StepKeyword

const (
	KwRun        = kit.KwRun
	KwCheck      = kit.KwCheck
	KwAgentRun   = kit.KwAgentRun
	KwAgentCheck = kit.KwAgentCheck
	KwInclude    = kit.KwInclude
)

// CUE-derived membership sets the node-form migrators consult, built from the SAME
// spec vocabulary slices core derives its sets from (reserved_registry.go) — the
// canonical source is spec, so this is a derivation, not a duplicate (R3).
var (
	stepKeywordSet = sliceToSet(spec.StepKeywords)
	dataKeySet     = sliceToSet(spec.DataKeys)
)

func sliceToSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, v := range s {
		m[v] = true
	}
	return m
}
