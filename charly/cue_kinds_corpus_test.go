package main

// Generic corpus validation for the collection kinds: for each root-shape
// charly.yml, extract each kind's map and validate every entity against its
// #<Kind>. Proves the registered schemas accept the whole real corpus.

import (
	"os"
	"path/filepath"
	"testing"

	"cuelang.org/go/cue"
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
		box := doc.LookupPath(cue.ParsePath("box"))
		if !box.Exists() {
			continue
		}
		if verr := validateEntityCUE("box", f, box); verr != nil {
			t.Errorf("FAIL %s", verr)
			continue
		}
		ok++
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
		"charly.yml", // the binary-embedded default (sidecar library), relative to charly/
	}
}

func TestCueKinds_Corpus(t *testing.T) {
	kinds := []string{
		"pod", "local", "android", "k8s", "sidecar",
		"distro", "builder", "init", "agent", "resource",
		"group", "target", "module", "deploy", "check", "vm",
	}
	counts := map[string]int{}
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
		for _, kind := range kinds {
			m := doc.LookupPath(cue.ParsePath(kind))
			if !m.Exists() {
				continue
			}
			it, err := m.Fields()
			if err != nil {
				continue // not a map at this position
			}
			for it.Next() {
				label := f + ":" + kind + "." + it.Selector().String()
				if verr := validateEntityCUE(kind, label, it.Value()); verr != nil {
					t.Errorf("FAIL %s", verr)
				} else {
					counts[kind]++
				}
			}
		}
	}
	for _, kind := range kinds {
		t.Logf("kind %-9s: %d real entities validated", kind, counts[kind])
	}
}
