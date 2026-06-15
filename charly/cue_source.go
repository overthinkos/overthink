package main

// cue_source.go — the CUE-source FRONT-END for the unified loader.
//
// The loader has ONE document-interpretation path (mergeUnifiedDocs →
// classifyDoc → mergeUnified/mergeKindDoc → the CUE decode in cue_loader.go). A
// config can be authored in YAML (decoded by yaml.v3) OR in CUE: compileCUEToYAML
// compiles a CUE source document and re-encodes it to the SAME YAML byte stream
// the rest of the loader already consumes, so the two source formats converge
// immediately onto one pipeline (R3 — no parallel CUE routing/normalize/merge).
//
// Used by the binary-embedded default config (embed_defaults.go), which is now
// authored as charly/charly.cue. Project on-disk config stays YAML-only.

import (
	"fmt"

	"cuelang.org/go/cue"
	cueyaml "cuelang.org/go/encoding/yaml"
)

// compileCUEToYAML compiles a CUE source document to YAML bytes for ingestion by
// mergeUnifiedDocs. The compiled value MUST be concrete (a config is data, not a
// schema) — an incomplete value fails HERE rather than exporting nulls. CUE
// definitions (#Foo) and hidden fields (_foo) used to factor out repetition
// resolve at compile time and are NOT exported, so the emitted YAML is pure
// concrete data, parsed downstream EXACTLY like any charly.yml.
func compileCUEToYAML(src []byte, label string) ([]byte, error) {
	v := cueSchemaCtx.CompileBytes(src)
	if v.Err() != nil {
		return nil, fmt.Errorf("%s: cue compile: %w", label, v.Err())
	}
	if err := v.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("%s: cue config is not concrete: %w", label, err)
	}
	y, err := cueyaml.Encode(v)
	if err != nil {
		return nil, fmt.Errorf("%s: cue->yaml encode: %w", label, err)
	}
	return y, nil
}
