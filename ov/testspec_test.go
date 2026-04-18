package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Ensures Kind() returns the correct verb for each single-verb Check and
// reports zero/multiple verbs as errors. The list-of-discriminators pattern
// mirrors Task.Kind() at ov/layers.go.
func TestCheck_Kind(t *testing.T) {
	tests := []struct {
		name    string
		check   Check
		wantKey string
		wantErr string // substring
	}{
		{"file", Check{File: "/usr/bin/redis"}, "file", ""},
		{"port", Check{Port: 6379}, "port", ""},
		{"http", Check{HTTP: "http://x"}, "http", ""},
		{"command", Check{Command: "redis-cli ping"}, "command", ""},
		{"matching", Check{Matching: 42}, "matching", ""},
		{"none", Check{}, "", "no verb"},
		{"two", Check{File: "/x", Port: 6379}, "", "multiple verbs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.check.Kind()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantKey {
					t.Errorf("Kind() = %q, want %q", got, tc.wantKey)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Kind() err = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

// Round-trips a realistic tests: list through yaml.v3 to verify the struct
// tags produce the authoring shape documented in the plan.
func TestCheck_UnmarshalYAMLList(t *testing.T) {
	src := `
- file: /usr/bin/redis-server
  exists: true
  mode: "0755"
- port: 6379
  listening: true
- command: redis-cli ping
  stdout: PONG
- http: http://127.0.0.1:8888/api
  status: 200
  body:
    - contains: "ready"
- id: redis-responds
  scope: deploy
  command: redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ping
  stdout: PONG
  in_container: false
`
	var got []Check
	if err := yaml.Unmarshal([]byte(src), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d checks, want 5", len(got))
	}

	// 0: file check
	if got[0].File != "/usr/bin/redis-server" {
		t.Errorf("file = %q, want /usr/bin/redis-server", got[0].File)
	}
	if got[0].Exists == nil || !*got[0].Exists {
		t.Errorf("file[0].exists should be pointer-to-true, got %v", got[0].Exists)
	}
	if got[0].Mode != "0755" {
		t.Errorf("mode = %q, want 0755", got[0].Mode)
	}

	// 1: port listening
	if got[1].Port != 6379 {
		t.Errorf("port = %d, want 6379", got[1].Port)
	}
	if got[1].Listening == nil || !*got[1].Listening {
		t.Errorf("listening should be pointer-to-true")
	}

	// 2: command with stdout matcher
	if got[2].Command != "redis-cli ping" {
		t.Errorf("command = %q", got[2].Command)
	}
	if len(got[2].Stdout) != 1 || got[2].Stdout[0].Op != "equals" || got[2].Stdout[0].Value != "PONG" {
		t.Errorf("stdout[0] = %+v, want {equals PONG}", got[2].Stdout)
	}

	// 3: http with body matcher
	if got[3].HTTP != "http://127.0.0.1:8888/api" {
		t.Errorf("http = %q", got[3].HTTP)
	}
	if got[3].Status != 200 {
		t.Errorf("status = %d, want 200", got[3].Status)
	}
	if len(got[3].Body) != 1 || got[3].Body[0].Op != "contains" {
		t.Errorf("body[0] = %+v, want {contains ready}", got[3].Body)
	}

	// 4: scope-deploy command with runtime variable references
	if got[4].ID != "redis-responds" {
		t.Errorf("id = %q", got[4].ID)
	}
	if got[4].Scope != "deploy" {
		t.Errorf("scope = %q", got[4].Scope)
	}
	if got[4].InContainer == nil || *got[4].InContainer {
		t.Errorf("in_container should be pointer-to-false")
	}
	if !strings.Contains(got[4].Command, "${HOST_PORT:6379}") {
		t.Errorf("command should preserve parameterized var ref: %q", got[4].Command)
	}
}

// Verifies Matcher decodes scalar, sequence, and single-key map forms.
func TestMatcher_UnmarshalYAML(t *testing.T) {
	type wrap struct {
		M []Matcher `yaml:"m"`
	}
	src := `
m:
  - PONG
  - equals: 42
  - contains:
      - "ok"
      - "ready"
  - matches: "^[a-z]+$"
  - [1, 2, 3]
`
	var w wrap
	if err := yaml.Unmarshal([]byte(src), &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(w.M) != 5 {
		t.Fatalf("got %d matchers, want 5", len(w.M))
	}
	cases := []struct {
		op    string
		value any
	}{
		{"equals", "PONG"},
		{"equals", 42},
		{"contains", []any{"ok", "ready"}},
		{"matches", "^[a-z]+$"},
		{"equals", []any{1, 2, 3}},
	}
	for i, want := range cases {
		if w.M[i].Op != want.op {
			t.Errorf("m[%d].op = %q, want %q", i, w.M[i].Op, want.op)
		}
		if !reflect.DeepEqual(w.M[i].Value, want.value) {
			t.Errorf("m[%d].value = %v (%T), want %v (%T)", i, w.M[i].Value, w.M[i].Value, want.value, want.value)
		}
	}
}

// Rejects a matcher map with more than one operator key.
func TestMatcher_RejectsMultiKey(t *testing.T) {
	src := `{equals: 1, contains: [2]}`
	var m Matcher
	if err := yaml.Unmarshal([]byte(src), &m); err == nil {
		t.Fatal("expected error for multi-key matcher map, got nil")
	}
}

// Covers both plain ${NAME} and parameterized ${NAME:arg} grammar, plus the
// unresolved-refs report used by the validator.
func TestExpandTestVars(t *testing.T) {
	env := map[string]string{
		"HOME":            "/home/user",
		"HOST_PORT:6379":  "16379",
		"VOLUME_PATH:ws":  "/tmp/ws",
		"CONTAINER_IP":    "10.88.0.12",
	}
	in := "ls ${HOME} && redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ${VOLUME_PATH:ws} ${UNKNOWN} ${HOST_PORT:9999}"
	out, missing := ExpandTestVars(in, env)

	want := "ls /home/user && redis-cli -h 10.88.0.12 -p 16379 /tmp/ws ${UNKNOWN} ${HOST_PORT:9999}"
	if out != want {
		t.Errorf("out =\n  %q\nwant\n  %q", out, want)
	}
	// Missing order-preserving, deduplicated
	wantMissing := []string{"UNKNOWN", "HOST_PORT:9999"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}

// TestVarRefs returns deduplicated refs in encounter order.
func TestTestVarRefs(t *testing.T) {
	got := TestVarRefs("${A} ${B:x} ${A} ${C} ${B:y}")
	want := []string{"A", "B:x", "C", "B:y"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// IsRuntimeOnlyVar classifies deploy-only variable keys correctly.
func TestIsRuntimeOnlyVar(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		{"HOME", false},
		{"USER", false},
		{"DNS", false},
		{"HOST_PORT:6379", true},
		{"VOLUME_PATH:workspace", true},
		{"VOLUME_CONTAINER_PATH:workspace", true},
		{"CONTAINER_IP", true},
		{"CONTAINER_NAME", true},
		{"INSTANCE", true},
		{"ENV_TOKEN", true},
		{"ENV_ANYTHING", true},
	}
	for _, tc := range cases {
		if got := IsRuntimeOnlyVar(tc.key); got != tc.want {
			t.Errorf("IsRuntimeOnlyVar(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

// Full-Check in-place expansion across all string-bearing fields.
func TestCheck_ExpandVars(t *testing.T) {
	c := Check{
		File:    "${HOME}/.redis",
		Command: "redis-cli -p ${HOST_PORT:6379}",
		HTTP:    "http://${CONTAINER_IP}:8080/health",
		IP:      "${MISSING}",
	}
	env := map[string]string{
		"HOME":           "/home/user",
		"HOST_PORT:6379": "16379",
		"CONTAINER_IP":   "10.0.0.5",
	}
	missing := c.ExpandVars(env)

	if c.File != "/home/user/.redis" {
		t.Errorf("File = %q", c.File)
	}
	if c.Command != "redis-cli -p 16379" {
		t.Errorf("Command = %q", c.Command)
	}
	if c.HTTP != "http://10.0.0.5:8080/health" {
		t.Errorf("HTTP = %q", c.HTTP)
	}
	if c.IP != "${MISSING}" {
		t.Errorf("IP should remain unresolved: %q", c.IP)
	}
	wantMissing := []string{"MISSING"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}

// LabelTestSet.IsEmpty detects both nil and all-empty-sections.
func TestLabelTestSet_IsEmpty(t *testing.T) {
	var nilSet *LabelTestSet
	if !nilSet.IsEmpty() {
		t.Error("nil LabelTestSet should be empty")
	}
	empty := &LabelTestSet{}
	if !empty.IsEmpty() {
		t.Error("zero-value LabelTestSet should be empty")
	}
	populated := &LabelTestSet{Layer: []Check{{File: "/x"}}}
	if populated.IsEmpty() {
		t.Error("populated LabelTestSet should not be empty")
	}
}

// JSON-side scalar shorthand for Matcher / MatcherList: mirrors the YAML
// shorthand so hand-crafted OCI labels with `"stdout":"OK"` parse the
// same way as `stdout: OK` in layer.yml.
func TestMatcher_UnmarshalJSON_Shorthand(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  Matcher
	}{
		{"scalar string", `"OK"`, Matcher{Op: "equals", Value: "OK"}},
		{"scalar number", `42`, Matcher{Op: "equals", Value: float64(42)}},
		{"scalar bool", `true`, Matcher{Op: "equals", Value: true}},
		{"canonical map", `{"op":"contains","value":"ready"}`, Matcher{Op: "contains", Value: "ready"}},
		{"operator map", `{"matches":"^OK$"}`, Matcher{Op: "matches", Value: "^OK$"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Matcher
			if err := json.Unmarshal([]byte(tc.input), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if m.Op != tc.want.Op {
				t.Errorf("op = %q, want %q", m.Op, tc.want.Op)
			}
			if !reflect.DeepEqual(m.Value, tc.want.Value) {
				t.Errorf("value = %v (%T), want %v (%T)", m.Value, m.Value, tc.want.Value, tc.want.Value)
			}
		})
	}
}

// MatcherList JSON shorthand: array stays as-is, scalar/object becomes a
// one-element list. Closes the asymmetry with the YAML path.
func TestMatcherList_UnmarshalJSON_Shorthand(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		wantOp   string
		wantVal  any
	}{
		{"array", `[{"op":"equals","value":"A"},{"op":"contains","value":"B"}]`, 2, "equals", "A"},
		{"scalar", `"OK"`, 1, "equals", "OK"},
		{"object", `{"matches":"^go$"}`, 1, "matches", "^go$"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ml MatcherList
			if err := json.Unmarshal([]byte(tc.input), &ml); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(ml) != tc.wantLen {
				t.Errorf("len = %d, want %d", len(ml), tc.wantLen)
			}
			if ml[0].Op != tc.wantOp {
				t.Errorf("op = %q, want %q", ml[0].Op, tc.wantOp)
			}
			if !reflect.DeepEqual(ml[0].Value, tc.wantVal) {
				t.Errorf("value = %v, want %v", ml[0].Value, tc.wantVal)
			}
		})
	}
}

// Verifies the extended ${NAME[:arg]} regex does not regress plain ${NAME}
// references (backward compatibility with taskVarRefPattern consumers).
func TestTestVarRefPattern_BackwardCompatible(t *testing.T) {
	got := TestVarRefs("${HOME}/x ${USER}")
	want := []string{"HOME", "USER"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
