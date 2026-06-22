package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ensures Kind() returns the correct verb for each single-verb Check and
// reports zero/multiple verbs as errors. The list-of-discriminators pattern
// mirrors Task.Kind() at charly/layers.go.
func TestCheck_Kind(t *testing.T) {
	tests := []struct {
		name    string
		check   Op
		wantKey string
		wantErr string // substring
	}{
		{"file", Op{File: "/usr/bin/redis"}, "file", ""},
		{"http", Op{HTTP: "http://x"}, "http", ""},
		{"command", Op{Command: "redis-cli ping"}, "command", ""},
		{"plugin", Op{Plugin: "matching"}, "plugin", ""},
		{"none", Op{}, "", "no verb"},
		{"two", Op{File: "/x", HTTP: "http://x"}, "", "multiple verbs"},
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
- plugin: port
  plugin_input:
    port: 6379
    listening: true
- command: redis-cli ping
  stdout: PONG
- http: http://127.0.0.1:8888/api
  status: 200
  body:
    - contains: "ready"
- id: redis-responds
  context: [deploy]
  command: redis-cli -h ${CONTAINER_IP} -p ${HOST_PORT:6379} ping
  stdout: PONG
  in_container: false
`
	var got []Op
	if err := decodeViaCUEForTest(t, src, &got); err != nil {
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

	// 1: port listening — now the `port` plugin verb (authored as plugin: port +
	// plugin_input, validated against the port plugin's spliced #PortInput schema).
	if got[1].Plugin != "port" {
		t.Errorf("plugin = %q, want port", got[1].Plugin)
	}
	if got[1].PluginInput == nil || got[1].PluginInput["listening"] != true {
		t.Errorf("plugin_input = %v, want a map with listening:true", got[1].PluginInput)
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

	// 4: deploy-context command with runtime variable references
	if got[4].ID != "redis-responds" {
		t.Errorf("id = %q", got[4].ID)
	}
	if len(got[4].Context) != 1 || got[4].Context[0] != "deploy" {
		t.Errorf("context = %v, want [deploy]", got[4].Context)
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
	if err := decodeViaCUEForTest(t, src, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(w.M) != 5 {
		t.Fatalf("got %d matchers, want 5", len(w.M))
	}
	cases := []struct {
		op    string
		value any
	}{
		// numeric matcher values decode as float64 via the CUE/JSON path
		// (Matcher.UnmarshalJSON), the live loader's decode mechanism.
		{"equals", "PONG"},
		{"equals", float64(42)},
		{"contains", []any{"ok", "ready"}},
		{"matches", "^[a-z]+$"},
		{"equals", []any{float64(1), float64(2), float64(3)}},
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
	if err := decodeViaCUEForTest(t, src, &m); err == nil {
		t.Fatal("expected error for multi-key matcher map, got nil")
	}
}

// Covers both plain ${NAME} and parameterized ${NAME:arg} grammar, plus the
// unresolved-refs report used by the validator.
func TestExpandTestVars(t *testing.T) {
	env := map[string]string{
		"HOME":           "/home/user",
		"HOST_PORT:6379": "16379",
		"VOLUME_PATH:ws": "/tmp/ws",
		"CONTAINER_IP":   "10.88.0.12",
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
	c := Op{
		File:    "${HOME}/.redis",
		Command: "redis-cli -p ${HOST_PORT:6379}",
		HTTP:    "http://${CONTAINER_IP}:8080/health",
		Owner:   "${MISSING}",
	}
	env := map[string]string{
		"HOME":           "/home/user",
		"HOST_PORT:6379": "16379",
		"CONTAINER_IP":   "10.0.0.5",
	}
	missing := opExpandVars(&c, env)

	if c.File != "/home/user/.redis" {
		t.Errorf("File = %q", c.File)
	}
	if c.Command != "redis-cli -p 16379" {
		t.Errorf("Command = %q", c.Command)
	}
	if c.HTTP != "http://10.0.0.5:8080/health" {
		t.Errorf("HTTP = %q", c.HTTP)
	}
	if c.Owner != "${MISSING}" {
		t.Errorf("Owner should remain unresolved: %q", c.Owner)
	}
	wantMissing := []string{"MISSING"}
	if !reflect.DeepEqual(missing, wantMissing) {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
}

// JSON-side scalar shorthand for Matcher / MatcherList: mirrors the YAML
// shorthand so hand-crafted OCI labels with `"stdout":"OK"` parse the
// same way as `stdout: OK` in the candy manifest.
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
		name    string
		input   string
		wantLen int
		wantOp  string
		wantVal any
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

// TestContainsList_BareSequenceDefaultsToContains validates the field-semantic
// fix for `contains: [...]` on file probes. Pre-2026-04-27, MatcherList's
// SequenceNode branch unconditionally promoted bare scalars to Op="equals",
// which made `contains: ["X"]` ask "does the entire content EQUAL X" — wrong
// for a field literally named contains. ContainsList preserves the
// authored Op for explicit operator-map elements while defaulting bare
// scalars/sequences to Op="contains".
func TestContainsList_BareSequenceDefaultsToContains(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantLen   int
		wantOps   []string
		wantValue []any
	}{
		{
			name:      "bare scalar promotes to contains",
			yaml:      `contains: foo`,
			wantLen:   1,
			wantOps:   []string{"contains"},
			wantValue: []any{"foo"},
		},
		{
			name:      "bare sequence promotes each element to contains",
			yaml:      `contains: [foo, bar]`,
			wantLen:   2,
			wantOps:   []string{"contains", "contains"},
			wantValue: []any{"foo", "bar"},
		},
		{
			name:      "explicit equals map keeps Op=equals",
			yaml:      "contains:\n  equals: foo",
			wantLen:   1,
			wantOps:   []string{"equals"},
			wantValue: []any{"foo"},
		},
		{
			name:      "explicit matches map keeps Op=matches",
			yaml:      `contains: {matches: "^prefix"}`,
			wantLen:   1,
			wantOps:   []string{"matches"},
			wantValue: []any{"^prefix"},
		},
		{
			name:      "mixed sequence: explicit equals + bare scalar",
			yaml:      `contains: [{equals: foo}, bar]`,
			wantLen:   2,
			wantOps:   []string{"equals", "contains"},
			wantValue: []any{"foo", "bar"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c Op
			if err := decodeViaCUEForTest(t, tc.yaml, &c); err != nil {
				t.Fatalf("yaml unmarshal: %v", err)
			}
			if len(c.Contains) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(c.Contains), tc.wantLen)
			}
			for i := range c.Contains {
				if c.Contains[i].Op != tc.wantOps[i] {
					t.Errorf("[%d].Op = %q, want %q", i, c.Contains[i].Op, tc.wantOps[i])
				}
				if !reflect.DeepEqual(c.Contains[i].Value, tc.wantValue[i]) {
					t.Errorf("[%d].Value = %v (%T), want %v (%T)",
						i, c.Contains[i].Value, c.Contains[i].Value,
						tc.wantValue[i], tc.wantValue[i])
				}
			}
		})
	}
}

// TestContainsList_RealWorldHarnessProbe simulates the check.yml:248 form
// that triggered the 2026-04-27 incident. Before the fix, file probes with
// `contains: ["marker"]` silently asked for content EQUALITY — passing
// coincidentally when the file's TrimRight'd content equaled the marker
// (phase 1 fixture-os) and failing whenever the file had any other content
// such as wrapping HTML (phase 2 fixture-web). Post-fix the probe correctly
// asks for substring containment.
func TestContainsList_RealWorldHarnessProbe(t *testing.T) {
	yamlSrc := `
file: /srv/fixture/index.html
exists: true
contains: ["charly-fixture-web-content-marker"]
`
	var c Op
	if err := decodeViaCUEForTest(t, yamlSrc, &c); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if len(c.Contains) != 1 {
		t.Fatalf("len(Contains) = %d, want 1", len(c.Contains))
	}
	if c.Contains[0].Op != "contains" {
		t.Errorf("Op = %q, want %q (the field-name semantic intent)",
			c.Contains[0].Op, "contains")
	}
	if c.Contains[0].Value != "charly-fixture-web-content-marker" {
		t.Errorf("Value = %v, want \"charly-fixture-web-content-marker\"", c.Contains[0].Value)
	}
}

// TestContainsList_DoesNotAffectMatcherList ensures the fix is field-scoped:
// stdout/body/etc. (typed MatcherList) keep Op="equals" as the default for
// bare scalars, since `stdout: PONG` should mean "stdout EQUALS PONG", not
// "stdout CONTAINS PONG". Only Contains was field-semantically broken.
func TestContainsList_DoesNotAffectMatcherList(t *testing.T) {
	yamlSrc := `
command: echo PONG
stdout: PONG
`
	var c Op
	if err := decodeViaCUEForTest(t, yamlSrc, &c); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if len(c.Stdout) != 1 {
		t.Fatalf("len(Stdout) = %d, want 1", len(c.Stdout))
	}
	if c.Stdout[0].Op != "equals" {
		t.Errorf("MatcherList default = %q, want %q (regression: ContainsList logic must not leak to MatcherList)",
			c.Stdout[0].Op, "equals")
	}
}

// TestCheck_CaptureExtract_YAMLDecode covers the YAML surface for the
// 2026-05 capture_extract: regex modifier — paired with capture: it
// pulls a submatch from the value before storing in the
// ScenarioContext.Captures stash.
func TestCheck_CaptureExtract_YAMLDecode(t *testing.T) {
	yamlSrc := `
command: "echo backgrounded pid=42"
capture: writer_pid
capture_extract: "pid=([0-9]+)"
`
	var c Op
	if err := decodeViaCUEForTest(t, yamlSrc, &c); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if c.Capture != "writer_pid" {
		t.Errorf("Capture = %q, want %q", c.Capture, "writer_pid")
	}
	if c.CaptureExtract != "pid=([0-9]+)" {
		t.Errorf("CaptureExtract = %q, want %q", c.CaptureExtract, "pid=([0-9]+)")
	}
}

// TestApplyCaptureExtract covers the regex-extraction helper itself:
// submatch wins over whole match; missing group → whole match;
// no match → error; invalid pattern → error.
func TestApplyCaptureExtract(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		pattern string
		want    string
		wantErr bool
	}{
		{"submatch group", "backgrounded pid=4567", "pid=([0-9]+)", "4567", false},
		{"whole match no group", "tab abc-123", `[a-z]+-[0-9]+`, "abc-123", false},
		{"no match", "no number here", `[0-9]+`, "", true},
		{"invalid regex", "anything", `(unclosed`, "", true},
		{"empty pattern passes through", "raw value", "", "raw value", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ApplyCaptureExtract(tt.value, tt.pattern)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got got=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestCheck_KillVerb covers the YAML surface for the 2026-05 kill:
// step verb — paired with capture'd PIDs from background commands,
// it sends SIGTERM (default) or SIGKILL to the captured PID.
func TestCheck_KillVerb(t *testing.T) {
	yamlSrc := `
kill: "${CAPTURED:writer_pid}"
signal: KILL
`
	var c Op
	if err := decodeViaCUEForTest(t, yamlSrc, &c); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if c.Kill != "${CAPTURED:writer_pid}" {
		t.Errorf("Kill = %q, want %q", c.Kill, "${CAPTURED:writer_pid}")
	}
	if c.Signal != "KILL" {
		t.Errorf("Signal = %q, want %q", c.Signal, "KILL")
	}
	kind, err := c.Kind()
	if err != nil {
		t.Fatalf("Kind() error: %v", err)
	}
	if kind != "kill" {
		t.Errorf("Kind() = %q, want %q", kind, "kill")
	}
}

// DEPLOY_NAME is deploy-scope (resolved only against a live deployment), so a
// build-scope check referencing it must be rejected by the validator.
func TestIsRuntimeOnlyVar_DeployName(t *testing.T) {
	if !IsRuntimeOnlyVar("DEPLOY_NAME") {
		t.Error("DEPLOY_NAME must be runtime-only")
	}
}
