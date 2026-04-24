package main

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ParseOvTestOutput
// ---------------------------------------------------------------------------

func TestParseOvTestOutput_Empty(t *testing.T) {
	r, err := ParseOvTestOutput(nil)
	if err != nil {
		t.Fatalf("empty input should not error: %v", err)
	}
	if r == nil {
		t.Fatal("want non-nil result for empty input")
	}
	if len(r.Scenarios) != 0 {
		t.Errorf("want 0 scenarios, got %d", len(r.Scenarios))
	}
	if r.Summary.Total != 0 {
		t.Errorf("want total 0, got %d", r.Summary.Total)
	}
}

func TestParseOvTestOutput_WithSummary(t *testing.T) {
	in := []byte(`
image: ovbench/run-abc-iter1:fedora-ov
mode: image
scenarios:
  - id: desc:layer:sshd:0
    origin: layer:sshd
    name: sshd reachable
    tags: [smoke]
    status: pass
    pending_steps: 0
    steps:
      - keyword: given
        text: sshd is installed
        step_id: desc:layer:sshd:0:0
        status: pass
        verb: package
  - id: desc:layer:foo:1
    status: fail
    pending_steps: 2
summary:
  total: 2
  pass: 1
  fail: 1
  skip: 0
`)
	r, err := ParseOvTestOutput(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Image != "ovbench/run-abc-iter1:fedora-ov" {
		t.Errorf("image mismatch: %q", r.Image)
	}
	if len(r.Scenarios) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(r.Scenarios))
	}
	if r.Scenarios[0].ID != "desc:layer:sshd:0" {
		t.Errorf("scenario[0].ID: %q", r.Scenarios[0].ID)
	}
	if r.Scenarios[1].PendingSteps != 2 {
		t.Errorf("scenario[1].PendingSteps: %d", r.Scenarios[1].PendingSteps)
	}
	if r.Summary.Pass != 1 || r.Summary.Fail != 1 {
		t.Errorf("summary: %+v", r.Summary)
	}
}

func TestParseOvTestOutput_DerivedSummary(t *testing.T) {
	// Producer omitted summary block; parser re-derives it.
	in := []byte(`
scenarios:
  - id: a
    status: pass
  - id: b
    status: fail
  - id: c
    status: skip
  - id: d
    status: pass
`)
	r, err := ParseOvTestOutput(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Summary.Total != 4 || r.Summary.Pass != 2 || r.Summary.Fail != 1 || r.Summary.Skip != 1 {
		t.Errorf("derived summary wrong: %+v", r.Summary)
	}
}

func TestParseOvTestOutput_StrictTopLevel(t *testing.T) {
	// Unknown top-level key must error (KnownFields(true)).
	in := []byte(`
bogus_top_key: 42
scenarios: []
`)
	_, err := ParseOvTestOutput(in)
	if err == nil {
		t.Fatal("want error on unknown top-level key, got nil")
	}
	if !strings.Contains(err.Error(), "bogus_top_key") {
		t.Errorf("error should name the bogus key; got: %v", err)
	}
}

func TestTestRunResults_ScenarioByID(t *testing.T) {
	r := &TestRunResults{
		Scenarios: []ScenarioTestResult{
			{ID: "a", Status: "pass"},
			{ID: "b", Status: "fail"},
		},
	}
	m := r.ScenarioByID()
	if len(m) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m))
	}
	if m["a"].Status != "pass" || m["b"].Status != "fail" {
		t.Errorf("map lookup wrong: %+v", m)
	}
	// Nil receiver yields empty map.
	var nr *TestRunResults
	if len(nr.ScenarioByID()) != 0 {
		t.Error("nil receiver should yield empty map")
	}
}

// ---------------------------------------------------------------------------
// FingerprintScenario — stability
// ---------------------------------------------------------------------------

func TestFingerprintScenario_StableAcrossIdenticalInputs(t *testing.T) {
	s1 := Scenario{
		Name: "demo",
		Tags: []string{"smoke", "fast"},
		Steps: []Step{
			{Given: "redis is installed", Check: Check{Package: "redis"}},
		},
	}
	s2 := Scenario{
		Name: "demo",
		Tags: []string{"smoke", "fast"},
		Steps: []Step{
			{Given: "redis is installed", Check: Check{Package: "redis"}},
		},
	}
	f1 := FingerprintScenario(s1)
	f2 := FingerprintScenario(s2)
	if f1 != f2 {
		t.Errorf("identical scenarios fingerprint differently:\n  %s\n  %s", f1, f2)
	}
	if !strings.HasPrefix(f1, "sha256:") {
		t.Errorf("fingerprint missing sha256: prefix: %s", f1)
	}
	if len(f1) != len("sha256:")+64 {
		t.Errorf("fingerprint wrong length: %d", len(f1))
	}
}

func TestFingerprintScenario_TagOrderInsensitive(t *testing.T) {
	s1 := Scenario{Name: "x", Tags: []string{"b", "a", "c"}}
	s2 := Scenario{Name: "x", Tags: []string{"a", "c", "b"}}
	if FingerprintScenario(s1) != FingerprintScenario(s2) {
		t.Error("tag reordering should NOT change fingerprint")
	}
}

func TestFingerprintScenario_NameSensitive(t *testing.T) {
	s1 := Scenario{Name: "alpha"}
	s2 := Scenario{Name: "beta"}
	if FingerprintScenario(s1) == FingerprintScenario(s2) {
		t.Error("different names must produce different fingerprints")
	}
}

func TestFingerprintScenario_StepKeywordSensitive(t *testing.T) {
	// Given→When should NOT equal When→Given. Step order is semantic.
	s1 := Scenario{
		Name: "x",
		Steps: []Step{
			{Given: "a"}, {When: "b"},
		},
	}
	s2 := Scenario{
		Name: "x",
		Steps: []Step{
			{When: "b"}, {Given: "a"},
		},
	}
	if FingerprintScenario(s1) == FingerprintScenario(s2) {
		t.Error("step order is semantic; swapping MUST change fingerprint")
	}
}

func TestFingerprintScenario_CheckBodySensitive(t *testing.T) {
	s1 := Scenario{
		Name:  "x",
		Steps: []Step{{Then: "responds", Check: Check{Port: 6379}}},
	}
	s2 := Scenario{
		Name:  "x",
		Steps: []Step{{Then: "responds", Check: Check{Port: 6380}}},
	}
	if FingerprintScenario(s1) == FingerprintScenario(s2) {
		t.Error("changing Check.Port must change fingerprint")
	}
}

func TestFingerprintScenario_ExamplesKeyOrderInsensitive(t *testing.T) {
	// yaml.Marshal sorts map keys alphabetically; so maps with same
	// k/v pairs produce the same bytes regardless of insertion order.
	s1 := Scenario{
		Name:     "x",
		Examples: []map[string]string{{"a": "1", "b": "2"}},
	}
	s2 := Scenario{
		Name:     "x",
		Examples: []map[string]string{{"b": "2", "a": "1"}},
	}
	if FingerprintScenario(s1) != FingerprintScenario(s2) {
		t.Error("examples map key ordering should not affect fingerprint")
	}
}

func TestFingerprintScenario_ExamplesRowOrderSensitive(t *testing.T) {
	// Row ORDER drives outline expansion order — semantic.
	s1 := Scenario{
		Name:     "x",
		Examples: []map[string]string{{"row": "1"}, {"row": "2"}},
	}
	s2 := Scenario{
		Name:     "x",
		Examples: []map[string]string{{"row": "2"}, {"row": "1"}},
	}
	if FingerprintScenario(s1) == FingerprintScenario(s2) {
		t.Error("examples row order is semantic; swapping MUST change fingerprint")
	}
}

// ---------------------------------------------------------------------------
// FingerprintSet
// ---------------------------------------------------------------------------

func TestFingerprintSet_NilAndEmpty(t *testing.T) {
	if got := FingerprintSet(nil); len(got) != 0 {
		t.Errorf("nil set should yield empty map, got %d entries", len(got))
	}
	if got := FingerprintSet(&LabelDescriptionSet{}); len(got) != 0 {
		t.Errorf("empty set should yield empty map, got %d entries", len(got))
	}
}

func TestFingerprintSet_KeyedByScenarioID(t *testing.T) {
	set := &LabelDescriptionSet{
		Layer: []LabeledDescription{
			{
				Origin: "layer:redis",
				Description: Description{
					Feature: "redis",
					Scenarios: []Scenario{
						{Name: "a"},
						{Name: "b"},
					},
				},
			},
		},
	}
	got := FingerprintSet(set)
	if len(got) != 2 {
		t.Fatalf("want 2 fingerprints, got %d: %v", len(got), got)
	}
	if _, ok := got["desc:layer:redis:0"]; !ok {
		t.Error("expected key desc:layer:redis:0")
	}
	if _, ok := got["desc:layer:redis:1"]; !ok {
		t.Error("expected key desc:layer:redis:1")
	}
}

// ---------------------------------------------------------------------------
// FingerprintTags
// ---------------------------------------------------------------------------

func TestFingerprintTags_StableOnReorder(t *testing.T) {
	a := FingerprintTags([]string{"smoke", "fast"})
	b := FingerprintTags([]string{"fast", "smoke"})
	if a != b {
		t.Errorf("tag reorder should not change: %s vs %s", a, b)
	}
}

func TestFingerprintTags_DifferentContent(t *testing.T) {
	a := FingerprintTags([]string{"smoke"})
	b := FingerprintTags([]string{"smoke", "new"})
	if a == b {
		t.Error("adding a tag must change fingerprint")
	}
}

// ---------------------------------------------------------------------------
// Classify — 7-verdict matrix
// ---------------------------------------------------------------------------

// makeState is a test helper for building ScenarioState values compactly.
func makeState(present bool, fp, status string, pending int, tagFp string) ScenarioState {
	return ScenarioState{
		Present:        present,
		Fingerprint:    fp,
		Status:         status,
		PendingSteps:   pending,
		TagFingerprint: tagFp,
	}
}

func TestClassify_Added(t *testing.T) {
	pre := makeState(false, "", "", 0, "")
	post := makeState(true, "sha256:x", "pass", 0, "sha256:t")
	if got := Classify(pre, post); got != VerdictAdded {
		t.Errorf("want VerdictAdded, got %q", got)
	}
}

func TestClassify_VanishedIsTampered(t *testing.T) {
	pre := makeState(true, "sha256:x", "fail", 1, "sha256:t")
	post := makeState(false, "", "", 0, "")
	if got := Classify(pre, post); got != VerdictTampered {
		t.Errorf("want VerdictTampered for deleted scenario, got %q", got)
	}
}

func TestClassify_Tampered(t *testing.T) {
	// Body fingerprint changed AND post pass → tampered.
	pre := makeState(true, "sha256:A", "fail", 1, "sha256:t")
	post := makeState(true, "sha256:B", "pass", 0, "sha256:t")
	if got := Classify(pre, post); got != VerdictTampered {
		t.Errorf("want VerdictTampered, got %q", got)
	}
}

func TestClassify_BodyChangedButStillFail_NotTampered(t *testing.T) {
	// The AI tried to tamper but failed to make the scenario pass.
	// Classify should NOT mark this as tampered (no false positive that
	// would otherwise trigger plateau increment instead of legit progress).
	// Result should fall through to Partial or Unchanged depending on
	// pending-step delta.
	pre := makeState(true, "sha256:A", "fail", 2, "sha256:t")
	post := makeState(true, "sha256:B", "fail", 1, "sha256:t") // pending decreased
	if got := Classify(pre, post); got == VerdictTampered {
		t.Error("tampered should require post pass; got tampered on failing scenario")
	}
}

func TestClassify_Retagged(t *testing.T) {
	// Body same; tags changed.
	pre := makeState(true, "sha256:X", "fail", 1, "sha256:T1")
	post := makeState(true, "sha256:X", "fail", 1, "sha256:T2")
	if got := Classify(pre, post); got != VerdictRetagged {
		t.Errorf("want VerdictRetagged, got %q", got)
	}
}

func TestClassify_Regressed(t *testing.T) {
	pre := makeState(true, "sha256:X", "pass", 0, "sha256:T")
	post := makeState(true, "sha256:X", "fail", 0, "sha256:T")
	if got := Classify(pre, post); got != VerdictRegressed {
		t.Errorf("want VerdictRegressed, got %q", got)
	}
}

func TestClassify_Solved(t *testing.T) {
	pre := makeState(true, "sha256:X", "fail", 2, "sha256:T")
	post := makeState(true, "sha256:X", "pass", 0, "sha256:T")
	if got := Classify(pre, post); got != VerdictSolved {
		t.Errorf("want VerdictSolved, got %q", got)
	}
}

func TestClassify_Solved_RequiresZeroPending(t *testing.T) {
	// Pass with residual pending steps should NOT be Solved —
	// they're narrative-only but still indicate incomplete authoring.
	pre := makeState(true, "sha256:X", "fail", 2, "sha256:T")
	post := makeState(true, "sha256:X", "pass", 1, "sha256:T")
	// Fingerprint unchanged, status pass, but pending > 0 → Partial.
	if got := Classify(pre, post); got == VerdictSolved {
		t.Error("VerdictSolved requires PendingSteps == 0")
	}
}

func TestClassify_Partial_PendingDecreased(t *testing.T) {
	pre := makeState(true, "sha256:X", "fail", 3, "sha256:T")
	post := makeState(true, "sha256:X", "fail", 1, "sha256:T")
	if got := Classify(pre, post); got != VerdictPartial {
		t.Errorf("want VerdictPartial, got %q", got)
	}
}

func TestClassify_Unchanged(t *testing.T) {
	pre := makeState(true, "sha256:X", "fail", 2, "sha256:T")
	post := makeState(true, "sha256:X", "fail", 2, "sha256:T")
	if got := Classify(pre, post); got != VerdictUnchanged {
		t.Errorf("want VerdictUnchanged, got %q", got)
	}
}

func TestClassify_NoFingerprintSupplied(t *testing.T) {
	// When the caller hasn't computed fingerprints (e.g., initial bench
	// run where pre is fresh), missing fingerprints must not falsely
	// trigger tampered/retagged. Classify treats "" fingerprints as
	// "not known" and falls through to status-based classification.
	pre := makeState(true, "", "fail", 1, "")
	post := makeState(true, "", "pass", 0, "")
	if got := Classify(pre, post); got != VerdictSolved {
		t.Errorf("want VerdictSolved when fingerprints empty and scenario now passes, got %q", got)
	}
}

func TestClassify_AllVerdictsListed(t *testing.T) {
	// Catches a maintenance failure where a new Verdict is added but
	// not appended to AllVerdicts. Adding verdicts in the future requires
	// extending this test's expected count.
	if len(AllVerdicts) != 7 {
		t.Errorf("AllVerdicts: expected 7, got %d", len(AllVerdicts))
	}
	seen := make(map[Verdict]bool)
	for _, v := range AllVerdicts {
		if seen[v] {
			t.Errorf("duplicate verdict in AllVerdicts: %q", v)
		}
		seen[v] = true
	}
}
