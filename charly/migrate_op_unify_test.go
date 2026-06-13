package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A candy with the legacy shape (description.scenario nested, a flat eval:
// check list, task cmd:/user:) migrates to the unified shape: top-level
// scenario:, no eval:, description without scenario:, task command:/run_as:.
// An eval check that twins a description scenario step is de-duplicated.
func TestMigrateOpUnify_Candy(t *testing.T) {
	dir := t.TempDir()
	candyDir := filepath.Join(dir, "candy", "oracle")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `candy:
    name: oracle
    description:
        feature: Oracle CLI
        scenario:
            - name: oracle-binary-on-path
              step:
                - then: the oracle executable exists
                  file: /usr/bin/oracle
                  exists: true
    task:
        - cmd: npm install -g oracle
          user: ${USER}
    eval:
        - id: oracle-binary
          file: /usr/bin/oracle
          exists: true
        - id: oracle-cfg
          scope: deploy
          file: /etc/oracle.conf
          exists: true
`
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateOpUnify(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	out, _ := os.ReadFile(filepath.Join(candyDir, "charly.yml"))
	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	candy := findMappingValue(rootMappingNode(&doc), "candy")

	// eval: is gone; description.scenario hoisted away.
	if findMappingValue(candy, "eval") != nil {
		t.Errorf("eval: key should be deleted:\n%s", out)
	}
	if desc := findMappingValue(candy, "description"); findMappingValue(desc, "scenario") != nil {
		t.Errorf("description.scenario should be hoisted away:\n%s", out)
	}
	// task: cmd→command, user→run_as.
	task := findMappingValue(candy, "task")
	if task == nil || len(task.Content) == 0 {
		t.Fatalf("task missing:\n%s", out)
	}
	te := task.Content[0]
	if findMappingValue(te, "cmd") != nil || findMappingValue(te, "command") == nil {
		t.Errorf("task cmd: should become command::\n%s", out)
	}
	if findMappingValue(te, "user") != nil || findMappingValue(te, "run_as") == nil {
		t.Errorf("task run-as user: should become run_as::\n%s", out)
	}
	// scenario: now top-level; EVERY eval check folds in, preserving its id: as
	// the scenario name so name-based references (recipe select:/depends_on:)
	// stay valid. The hoisted description scenario, the twin check, and the
	// deploy-scope check all survive.
	sc := findMappingValue(candy, "scenario")
	if sc == nil || sc.Kind != yaml.SequenceNode {
		t.Fatalf("top-level scenario: missing:\n%s", out)
	}
	names := scenarioNamesFromNode(sc)
	for _, want := range []string{"oracle-binary-on-path", "oracle-binary", "oracle-cfg"} {
		if !hasStr(names, want) {
			t.Errorf("scenario %q should be present after the fold, got %v", want, names)
		}
	}
	if !strings.Contains(string(out), "context:") || !strings.Contains(string(out), "runtime") {
		t.Errorf("deploy-scope check should map to context: [runtime] (eval-live = a running target):\n%s", out)
	}

	// Idempotent: a second run changes nothing.
	rew, err := MigrateOpUnify(dir, false)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(rew) != 0 {
		t.Errorf("migration not idempotent: re-ran on %v", rew)
	}
}

// The ROOT harness eval: block (a MAPPING of bed name → deploy node) is NOT a
// check list: its KEY must survive, while a bed node's OWN eval: check list
// folds into that bed's scenario:.
func TestMigrateOpUnify_RootHarnessEvalUntouched(t *testing.T) {
	dir := t.TempDir()
	src := `version: "2026.164.0002"
eval:
    eval-redis-pod:
        target: pod
        box: redis
        disposable: true
        eval:
            - id: redis-up
              scope: deploy
              command: redis-cli ping
              stdout: PONG
`
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateOpUnify(dir, false); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	out, _ := os.ReadFile(filepath.Join(dir, "charly.yml"))
	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	root := rootMappingNode(&doc)
	harness := findMappingValue(root, "eval")
	if harness == nil || harness.Kind != yaml.MappingNode {
		t.Fatalf("root harness eval: map must survive as a mapping:\n%s", out)
	}
	bed := findMappingValue(harness, "eval-redis-pod")
	if bed == nil {
		t.Fatalf("bed node lost:\n%s", out)
	}
	// The bed's OWN eval: check list folded into the bed's scenario:.
	if findMappingValue(bed, "eval") != nil {
		t.Errorf("bed's own eval: check list should fold away:\n%s", out)
	}
	if findMappingValue(bed, "scenario") == nil {
		t.Errorf("bed's folded check should appear under scenario::\n%s", out)
	}
}

func scenarioNamesFromNode(sc *yaml.Node) []string {
	var out []string
	for _, scen := range sc.Content {
		if n := findMappingValue(scen, "name"); n != nil {
			out = append(out, n.Value)
		}
	}
	return out
}

func hasStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
