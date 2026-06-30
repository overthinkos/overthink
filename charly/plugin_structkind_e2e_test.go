package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExternalStructKind_StructuralDecode proves F5 END-TO-END: a STRUCTURAL external kind
// (candy/plugin-example-structkind, NOT compiled in) is recognized + connected by the F4 prescan,
// and its OpLoad returns a spec.Deploy MEMBER TREE the host folds into uf.Bundle — the SAME map a
// builtin pod/group decoder populates — with the member surviving the wire round-trip. This is the
// HIGHEST-risk F5 assumption (a plugin's decode output drives uf.Bundle); proving it unblocks M3's
// externalization of the seven builtin structural kind decoders. Builds the real plugin OOP, so
// -short-gated. Reuses copyCandyFixReplace from plugin_kind_prescan_e2e_test.go (same package).
func TestExternalStructKind_StructuralDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the external structural kind plugin binary (slow)")
	}
	charlyDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	srcCandy, err := filepath.Abs("../candy/plugin-example-structkind")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcCandy, "go.mod")); err != nil {
		t.Fatalf("example structkind plugin module not found at %s: %v", srcCandy, err)
	}

	dir := t.TempDir()
	dstCandy := filepath.Join(dir, "candy", "plugin-example-structkind")
	if err := copyCandyFixReplace(srcCandy, dstCandy, charlyDir); err != nil {
		t.Fatalf("stage candy: %v", err)
	}
	rootYAML := `version: ` + LatestSchemaVersion().String() + `
discover:
    - path: candy
      recursive: true
my-structkind:
    examplestructkind:
        marker: spikemember
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(rootYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	uf, _, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified must parse+decode a structural kind via F5: %v", err)
	}

	// F5: a STRUCTURAL kind folds into uf.Bundle (NOT uf.PluginKinds).
	dn, ok := uf.Bundle["my-structkind"]
	if !ok {
		t.Fatalf("structural kind not folded into uf.Bundle; have bundle keys %v", bundleKeysFor(uf))
	}
	member, ok := dn.Members["spikemember"]
	if !ok {
		t.Fatalf("member tree did not survive the wire; members=%v", dn.Members)
	}
	if member == nil || member.Target != "pod" {
		t.Fatalf("member.Target = %v, want pod (the plugin-built member)", member)
	}
	// A structural kind must NOT ALSO land in the flat uf.PluginKinds (structural ≠ flat).
	if _, dup := uf.PluginKinds["examplestructkind"]; dup {
		t.Fatal("structural kind also landed in uf.PluginKinds — it must be uf.Bundle ONLY")
	}
}

func bundleKeysFor(uf *UnifiedFile) []string {
	out := []string{}
	for k := range uf.Bundle {
		out = append(out, k)
	}
	return out
}
