package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// node_childform_test.go — check-coverage for the unified "everything is a node"
// child-node model: a legacy entity migrates to `<name>: {<kind>: <scalars>,
// <child-nodes>}` (every non-scalar field + plan step a child), and the parser +
// assembler reconstruct the complete entity. Each test FAILS without the
// corresponding child-node code path.

// parseDocNodes migrates a legacy YAML doc to node-form and parses its top-level
// nodes (the loader's pipeline up to normalize).
func parseDocNodes(t *testing.T, legacy string) []*genericNode {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(legacy), &doc); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	migrateUnifiedNodeDoc(&doc)
	_, nodes, err := parseNodeTree(&doc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
	}
	return nodes
}

// TestChildForm_MigrateParseAssemble: a legacy candy with scalars + composition +
// collections + a plan migrates to child nodes and the assembler folds them back
// into the COMPLETE Candy (scalars in the value, everything else reconstructed).
func TestChildForm_MigrateParseAssemble(t *testing.T) {
	legacy := "candy:\n" +
		"  name: redis\n  version: \"2026.150.0000\"\n  status: working\n" +
		"  candy: [supervisord]\n  package: [redis, redis-cli]\n" +
		"  env: {REDIS_PORT: \"6379\"}\n" +
		"  plan:\n    - check: binary exists\n      file: /usr/bin/redis-server\n"
	nodes := parseDocNodes(t, legacy)
	if len(nodes) != 1 || nodes[0].disc != "candy" {
		t.Fatalf("want one candy node, got %d (disc %q)", len(nodes), nodes[0].disc)
	}
	_, ic, err := buildCandy(nodes[0])
	if err != nil {
		t.Fatalf("buildCandy: %v", err)
	}
	c := ic.CandyYAML
	if c.Version != "2026.150.0000" || c.Status != "working" {
		t.Errorf("scalars lost: version=%q status=%q", c.Version, c.Status)
	}
	if len(c.Package) != 2 {
		t.Errorf("package collection not folded: %v", c.Package)
	}
	if len(c.Candy) != 1 {
		t.Errorf("composition not folded: %v", c.Candy)
	}
	if c.Env["REDIS_PORT"] != "6379" {
		t.Errorf("env map not folded: %v", c.Env)
	}
	if len(c.Plan) != 1 {
		t.Errorf("plan step not folded: %d", len(c.Plan))
	}
}

// TestChildForm_WrongKindChild: a non-deployable kind (candy) may NOT nest a
// sub-entity (a pod) — only deployable kinds do. The Go gate rejects it (the CUE
// document gate accepts children as `_` for performance).
func TestChildForm_WrongKindChild(t *testing.T) {
	doc := "redis:\n  candy: {version: \"2026.150.0000\"}\n  inner:\n    pod: {box: x}\n"
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	_, _, err := parseNodeTree(&n)
	if err == nil || !strings.Contains(err.Error(), "sub-entity child") {
		t.Fatalf("expected wrong-kind-child rejection, got %v", err)
	}
}

// TestChildForm_UniqueChildName: a candy whose `package` field AND a plan step's id
// both resolve to `<name>-package` must NOT collide into one merged YAML key.
func TestChildForm_UniqueChildName(t *testing.T) {
	legacy := "candy:\n  name: nodejs\n  version: \"2026.150.0000\"\n" +
		"  package: [nodejs]\n" +
		"  plan:\n    - check: package installed\n      id: nodejs-package\n      package: nodejs\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(legacy), &doc); err != nil {
		t.Fatal(err)
	}
	migrateUnifiedNodeDoc(&doc)
	// The migrated node must have NO duplicate top-level child key, AND must parse
	// + assemble cleanly (a collision merged the list `package` with the scalar
	// probe `package` → a decode conflict).
	root := rootMappingNode(&doc)
	nodeContent := root.Content[1] // redis/nodejs node value
	seen := map[string]bool{}
	for i := 0; i+1 < len(nodeContent.Content); i += 2 {
		k := nodeContent.Content[i].Value
		if seen[k] {
			t.Errorf("duplicate child key %q after migration (uniqueChildName failed)", k)
		}
		seen[k] = true
	}
	_, nodes, err := parseNodeTree(&doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, _, err := buildCandy(nodes[0]); err != nil {
		t.Fatalf("buildCandy after dedup: %v", err)
	}
}

// TestChildForm_StepVerbPrecedence: a check step carrying a `port:` Op field (also a
// data-key name) is ONE step node, not two discriminators.
func TestChildForm_StepVerbPrecedence(t *testing.T) {
	gn, err := parseNode("probe", mustYAMLNode(t, "check: port 6443 listening\nport: 6443\n"), true)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	if gn.discClass != "step" || gn.disc != "check" {
		t.Errorf("want step/check, got %s/%s", gn.discClass, gn.disc)
	}
}

// TestChildForm_CompositionChildIsData: a `{candy: [refs]}` child classifies as DATA
// (composition), not a sub-entity — `candy` is both a kind keyword and a data field,
// and as a child the data sense wins.
func TestChildForm_CompositionChildIsData(t *testing.T) {
	gn, err := parseNode("comp", mustYAMLNode(t, "candy: [supervisord, redis]\n"), true)
	if err != nil {
		t.Fatalf("parseNode: %v", err)
	}
	if gn.discClass != "data" || gn.disc != "candy" {
		t.Errorf("want data/candy, got %s/%s", gn.discClass, gn.disc)
	}
}

func mustYAMLNode(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatal(err)
	}
	return n.Content[0]
}
