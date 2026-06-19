package main

// Generic corpus validation for the collection kinds: for each root-shape
// charly.yml, extract each kind's map and validate every entity against its
// #<Kind>. Proves the registered schemas accept the whole real corpus.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
)

// TestCueBox_Corpus validates every discovered box entity (kind-keyed
// box/<distro>/box/<name>/charly.yml) against #Box.
func TestCueBox_Corpus(t *testing.T) {
	matches, err := filepath.Glob("../box/*/box/*/charly.yml")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var ok int
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		doc, err := cueDocFromYAML(f, data)
		if err != nil {
			t.Errorf("%s: ingest: %v", f, err)
			continue
		}
		// Unified node-form: `<name>: {box: {…}}` — iterate the top-level nodes
		// and validate each node's `box` discriminator value against #Box.
		it, ferr := doc.Fields()
		if ferr != nil {
			continue
		}
		for it.Next() {
			box := it.Value().LookupPath(cue.ParsePath("box"))
			if !box.Exists() {
				continue
			}
			if verr := validateEntityCUE("box", f, box); verr != nil {
				t.Errorf("FAIL %s", verr)
				continue
			}
			ok++
		}
	}
	t.Logf("box CUE validation: %d/%d discovered box entities validated", ok, len(matches))
	if ok == 0 {
		t.Fatal("no box entities validated (glob/path wrong?)")
	}
}

func rootShapeFiles() []string {
	return []string{
		"../charly.yml",          // repo root (pod/local/k8s/vm/check/android collections)
		"../box/arch/charly.yml", // box submodule stacks
		"../box/fedora/charly.yml",
		"../box/debian/charly.yml",
		"../box/ubuntu/charly.yml",
		"../box/cachyos/charly.yml",
		"charly.yml", // the binary-embedded default (distro/builder/init/resource/sidecar vocabulary), relative to charly/
	}
}

func TestCueKinds_Corpus(t *testing.T) {
	// Unified node-form discovery: a top-level `<name>: {<kind>: …}` node IS an
	// entity of <kind> (vs the legacy kind-keyed `<kind>: {<name>: …}` map). Each
	// entity node is validated through #NodeDoc's per-entity pattern constraint
	// (`{[!~dir]: #Node}`) via FillPath — the SAME non-concrete, closedness-only
	// gate validateNodeDocCUE (the loader's validate-before-execute) uses, so the
	// per-kind #<Kind> def types each kind-value while the vm `source` disjunction
	// stays lazy (no spurious concrete "incomplete value" artifact).
	docDef := sharedCueSchema.LookupPath(cue.ParsePath("#NodeDoc"))
	if docDef.Err() != nil {
		t.Fatalf("#NodeDoc schema not found: %v", docDef.Err())
	}
	// The recognized entity discriminators — the production set (kept in lockstep
	// with schema/node.cue), sorted for deterministic discovery + logging.
	kinds := make([]string, 0, len(nodeEntityKinds))
	for k := range nodeEntityKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	counts := map[string]int{}
	total := 0
	for _, f := range rootShapeFiles() {
		data, err := os.ReadFile(f)
		if err != nil {
			continue // layout may omit a file
		}
		doc, err := cueDocFromYAML(f, data)
		if err != nil {
			t.Errorf("%s: ingest: %v", f, err)
			continue
		}
		it, ierr := doc.Fields()
		if ierr != nil {
			t.Errorf("%s: fields: %v", f, ierr)
			continue
		}
		for it.Next() {
			name := it.Selector().Unquoted()
			if nodeDocDirectives[name] {
				continue // version/repo/import/discover/defaults/provides — not entities
			}
			node := it.Value()
			// A node's kind = the single reserved ENTITY discriminator it carries.
			kind := ""
			for _, k := range kinds {
				if node.LookupPath(cue.ParsePath(k)).Exists() {
					kind = k
					break
				}
			}
			if kind == "" {
				t.Errorf("FAIL %s:%s: no entity discriminator found in node-form node", f, name)
				continue
			}
			filled := docDef.FillPath(cue.MakePath(cue.Str(name)), node)
			if verr := filled.Validate(); verr != nil {
				t.Errorf("FAIL %s:%s.%s: %s", f, kind, name, errors.Details(verr, nil))
				continue
			}
			counts[kind]++
			total++
		}
	}
	for _, kind := range kinds {
		t.Logf("kind %-9s: %d real entities validated", kind, counts[kind])
	}
	if total == 0 {
		t.Fatal("no real entities validated (node-form discovery wrong?)")
	}
}
