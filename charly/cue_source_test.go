package main

import (
	"strings"
	"testing"
)

// A concrete CUE source using a hidden field + a reference (the DRY idiom)
// exports ONLY the concrete regular data — the hidden helper vanishes.
func TestCompileCUEToYAML_ExportsConcreteDataDropsHidden(t *testing.T) {
	src := []byte(`
_shared: {install_cmd: "dnf install -y"}
version: "2026.165.1048"
distro: {
	fedora: {bootstrap: _shared}
}
`)
	out, err := compileCUEToYAML(src, "test.cue")
	if err != nil {
		t.Fatalf("compileCUEToYAML: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "install_cmd: dnf install -y") {
		t.Errorf("reference not resolved into exported data:\n%s", s)
	}
	if strings.Contains(s, "_shared") {
		t.Errorf("hidden field leaked into exported YAML:\n%s", s)
	}
	// Re-ingest proves it flows through the unified document core unchanged.
	var uf UnifiedFile
	if _, err := mergeUnifiedDocs(&uf, out, "test.cue", ""); err != nil {
		t.Fatalf("mergeUnifiedDocs on exported YAML: %v", err)
	}
	if uf.Version != "2026.165.1048" {
		t.Errorf("version not preserved: %q", uf.Version)
	}
	if d := uf.Distro["fedora"]; d == nil || d.Bootstrap.InstallCmd != "dnf install -y" {
		t.Errorf("distro.fedora.bootstrap.install_cmd not preserved: %+v", uf.Distro["fedora"])
	}
}

// A non-concrete CUE source (an open constraint) is rejected — a config must be
// data, never a schema.
func TestCompileCUEToYAML_RejectsNonConcrete(t *testing.T) {
	src := []byte(`version: string`) // open: not concrete
	if _, err := compileCUEToYAML(src, "bad.cue"); err == nil {
		t.Fatal("expected non-concrete CUE to be rejected, got nil error")
	}
}
