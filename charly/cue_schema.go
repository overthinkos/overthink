package main

// CUE-validation Core (lead-owned). One compiled schema instance (all
// schema/*.cue unified — shared #Step lives once in _common.cue, R3), a kind
// registry populated by each cue_kind_<name>.go via init(), and a per-entity
// validator. Validation is PER ENTITY: extract an entity (the `candy:` value of
// a kind-keyed file, or each value of a `pod:`/`k8s:`/… collection map) and
// unify it with #<Kind>. Runs ALONGSIDE the existing Go loader during the
// cutover; the old shape-routing + hand-written validators are deleted once
// every kind reaches corpus parity.

import (
	"embed"
	"fmt"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	cueyaml "cuelang.org/go/encoding/yaml"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// cueSchemaCtx is the process-wide CUE context (schemas compile once, reuse).
var cueSchemaCtx = cuecontext.New()

// sharedCueSchema is every schema/*.cue file unified into one value (no package
// clauses → one shared scope, so kind defs reference the shared #Step/#Context).
var sharedCueSchema = func() cue.Value {
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		panic(fmt.Sprintf("read embedded schema dir: %v", err))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".cue") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic concatenation
	var b strings.Builder
	for _, n := range names {
		data, err := schemaFS.ReadFile("schema/" + n)
		if err != nil {
			panic(fmt.Sprintf("read embedded schema %s: %v", n, err))
		}
		b.Write(data)
		b.WriteString("\n")
	}
	v := cueSchemaCtx.CompileString(b.String())
	if v.Err() != nil {
		panic(fmt.Sprintf("CUE schema failed to compile: %v", errors.Details(v.Err(), nil)))
	}
	return v
}()

// cueKindDefs maps a kind name to its entity definition path (e.g. "#Candy").
var cueKindDefs = map[string]string{}

// registerCueKind records that `kind` is validated by the CUE def at defPath.
// Panics on a duplicate name or a def absent from the compiled schema —
// fail-fast at process start (mirrors mustCalVer).
func registerCueKind(kind, defPath string) {
	if _, dup := cueKindDefs[kind]; dup {
		panic(fmt.Sprintf("duplicate CUE kind registration: %q", kind))
	}
	if d := sharedCueSchema.LookupPath(cue.ParsePath(defPath)); d.Err() != nil {
		panic(fmt.Sprintf("CUE kind %q: definition %s not found: %v", kind, defPath, d.Err()))
	}
	cueKindDefs[kind] = defPath
}

// cueKindDef returns the compiled entity definition for a kind.
func cueKindDef(kind string) (cue.Value, bool) {
	dp, ok := cueKindDefs[kind]
	if !ok {
		return cue.Value{}, false
	}
	return sharedCueSchema.LookupPath(cue.ParsePath(dp)), true
}

// validateEntityCUE unifies a single already-parsed entity value with #<Kind>
// and validates it concretely. label identifies the entity in errors.
func validateEntityCUE(kind, label string, entity cue.Value) error {
	def, ok := cueKindDef(kind)
	if !ok {
		return fmt.Errorf("%s: no CUE schema registered for kind %q", label, kind)
	}
	if err := entity.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: %s", label, errors.Details(err, nil))
	}
	return nil
}

// validateEntityClosedCUE unifies a single entity with #<Kind> and validates it
// WITHOUT requiring concreteness — it catches closedness violations (unknown
// keys) and type/enum/regex conflicts, but not missing-required fields. This is
// the LOAD-time check (restores the deleted unmarshalers' typo-detection); full
// concrete validation stays in `charly box validate` via validateEntityCUE.
func validateEntityClosedCUE(kind, label string, entity cue.Value) error {
	def, ok := cueKindDef(kind)
	if !ok {
		return fmt.Errorf("%s: no CUE schema registered for kind %q", label, kind)
	}
	if err := entity.Unify(def).Validate(); err != nil {
		return fmt.Errorf("%s: %s", label, errors.Details(err, nil))
	}
	return nil
}

// validateCandyManifestCUE extracts the `candy:` entity from a kind-keyed candy
// manifest and validates it against #Candy (per-entity model).
func validateCandyManifestCUE(path string, data []byte) error {
	doc, err := cueDocFromYAML(path, data)
	if err != nil {
		return err
	}
	return validateEntityCUE("candy", path, doc.LookupPath(cue.ParsePath("candy")))
}

// cueDocFromYAML ingests one YAML document into a cue.Value (the whole doc).
func cueDocFromYAML(path string, data []byte) (cue.Value, error) {
	af, err := cueyaml.Extract(path, data)
	if err != nil {
		return cue.Value{}, fmt.Errorf("%s: yaml ingest: %w", path, err)
	}
	v := cueSchemaCtx.BuildFile(af)
	if v.Err() != nil {
		return cue.Value{}, fmt.Errorf("%s: build: %w", path, v.Err())
	}
	return v, nil
}
