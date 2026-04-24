package main

// benchmark_score.go — scoring primitives for `ov benchmark`.
//
// Exported surface:
//   - ParseOvTestOutput(yaml) (*TestRunResults, error)
//       Parses the YAML emitted by `ov image test --format yaml`.
//   - FingerprintScenario(s Scenario) string
//       Canonical SHA256 of a Scenario. Whitespace/ordering insensitive.
//   - FingerprintSet(set *LabelDescriptionSet) map[string]string
//       Convenience: ScenarioID → fingerprint for every scenario in the set.
//   - Classify(pre, post ScenarioState) Verdict
//       Seven-way classifier comparing pre-AI baseline to post-iteration.
//
// All types are YAML-tagged so they can be persisted verbatim into
// `.benchmark/<run-id>/iter<k>/score.yml` and `report.yml` without a
// parallel JSON shape.
//
// Dependencies: Scenario / LabelDescriptionSet / ScenarioID from
// description_spec.go; nothing else from the ov package. Stays standalone
// so the rest of the benchmark files can be written incrementally.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// `ov image test --format yaml` parser
// ---------------------------------------------------------------------------

// TestRunResults is the structured result of one `ov image test` run,
// as produced by `--format yaml`. The YAML shape is documented in
// description_report.go; this type is the Go-side mirror.
//
// The zero value is a usable empty result (no scenarios, no summary).
type TestRunResults struct {
	Image     string                `yaml:"image,omitempty"`
	Mode      string                `yaml:"mode,omitempty"` // "image" | "run"
	Scenarios []ScenarioTestResult  `yaml:"scenarios,omitempty"`
	Summary   TestRunSummary        `yaml:"summary"`
}

// ScenarioTestResult is the evaluator's verdict for a single scenario
// (one row of a LabelDescriptionSet after outline expansion). The caller
// keys results by ScenarioID across iterations for plateau tracking.
type ScenarioTestResult struct {
	ID           string             `yaml:"id"`
	Origin       string             `yaml:"origin,omitempty"`
	Name         string             `yaml:"name,omitempty"`
	Tags         []string           `yaml:"tags,omitempty"`
	Status       string             `yaml:"status"` // "pass" | "fail" | "skip"
	PendingSteps int                `yaml:"pending_steps"`
	Steps        []StepTestResult   `yaml:"steps,omitempty"`
}

// StepTestResult is the per-step detail inside a ScenarioTestResult.
// Only the fields the benchmark needs are modeled; richer fields (elapsed,
// message, captured_value) can be added without breaking the parser.
type StepTestResult struct {
	Keyword string `yaml:"keyword,omitempty"`
	Text    string `yaml:"text,omitempty"`
	StepID  string `yaml:"step_id,omitempty"`
	Status  string `yaml:"status"`
	Verb    string `yaml:"verb,omitempty"`
	Pending bool   `yaml:"pending,omitempty"`
}

// TestRunSummary mirrors the summary block emitted by `ov image test
// --format yaml`. Totals are authoritative; the benchmark re-derives
// per-status counts from Scenarios but relies on Summary.Total as a
// sanity check.
type TestRunSummary struct {
	Total int `yaml:"total"`
	Pass  int `yaml:"pass"`
	Fail  int `yaml:"fail"`
	Skip  int `yaml:"skip"`
}

// ParseOvTestOutput parses the byte slice emitted by
//
//	ov image test <tag> --format yaml
//
// into a *TestRunResults. The parser is strict on shape (unknown
// top-level keys produce errors) so drift in the producer's YAML shape
// is caught early. The parser is permissive on unknown step/scenario
// attributes — the producer may add more detail later without breaking
// this reader.
//
// An empty byte slice is a usable "no scenarios" result, not an error.
func ParseOvTestOutput(b []byte) (*TestRunResults, error) {
	if len(b) == 0 {
		return &TestRunResults{}, nil
	}
	var r TestRunResults
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true) // strict on top-level keys
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("parse ov image test --format yaml: %w", err)
	}
	// The producer MAY omit summary; re-derive so downstream callers see
	// consistent counts. When present, trust the producer.
	if r.Summary.Total == 0 && len(r.Scenarios) > 0 {
		r.Summary = deriveSummary(r.Scenarios)
	}
	return &r, nil
}

// deriveSummary computes TestRunSummary from scenario-level statuses.
// Used only when the producer omitted the summary block; normally
// Summary is emitted verbatim by the producer.
func deriveSummary(scenarios []ScenarioTestResult) TestRunSummary {
	var s TestRunSummary
	for _, sc := range scenarios {
		s.Total++
		switch sc.Status {
		case "pass":
			s.Pass++
		case "fail":
			s.Fail++
		case "skip":
			s.Skip++
		}
	}
	return s
}

// ScenarioByID builds a map from ScenarioID to its result. Returns an
// empty map for a nil or empty TestRunResults.
func (r *TestRunResults) ScenarioByID() map[string]ScenarioTestResult {
	out := make(map[string]ScenarioTestResult)
	if r == nil {
		return out
	}
	for _, sc := range r.Scenarios {
		out[sc.ID] = sc
	}
	return out
}

// ---------------------------------------------------------------------------
// Fingerprinting
// ---------------------------------------------------------------------------

// FingerprintScenario returns a stable SHA256 fingerprint of a Scenario's
// semantic content. The fingerprint is deterministic and insensitive to
// whitespace, key ordering, or tag ordering; it IS sensitive to scenario
// name, step keyword, step text, every embedded Check field, and the
// outline `examples:` rows.
//
// The output format is "sha256:<64 hex chars>" — a string suitable for
// embedding verbatim into YAML report files.
func FingerprintScenario(s Scenario) string {
	canonical := canonicalizeScenario(s)
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalizeScenario serializes a Scenario in a whitespace-normalized,
// tag-sorted form so the resulting bytes uniquely identify its semantic
// content. It uses yaml.Marshal for the primary shape (preserves every
// Check field's value via the inline embed) and then post-processes the
// output to enforce tag sort order.
func canonicalizeScenario(s Scenario) string {
	// Sort tags in place for deterministic output. We intentionally do
	// NOT sort step keyword order — Given/When/Then ordering is
	// semantic (changing it would change scenario meaning), so a
	// reordering MUST change the fingerprint.
	clone := deepCloneScenario(s)
	sort.Strings(clone.Tags)
	for i := range clone.Steps {
		// Check.Matching tags etc. are not part of the Check struct, so
		// there is no nested tag list to sort here. Step text is canonical
		// as-authored.
		_ = i
	}
	// Sort Examples row keys for deterministic output. Row ORDER is
	// semantic (examples drive scenario outline expansion order) so
	// rows themselves stay in document order.
	for i, row := range clone.Examples {
		if row == nil {
			continue
		}
		clone.Examples[i] = sortedCopy(row)
	}

	out, err := yaml.Marshal(clone)
	if err != nil {
		// yaml.Marshal cannot realistically fail on a well-formed
		// Scenario — every field is a basic type. Fall back to a
		// bespoke formatting that still hashes deterministically.
		return fmt.Sprintf("MARSHAL_ERR:%s:%d:%d", clone.Name, len(clone.Tags), len(clone.Steps))
	}
	return string(out)
}

// sortedCopy returns a new map where iteration order follows the sorted
// key order under yaml.Marshal. Go maps have nondeterministic iteration,
// but yaml.Marshal sorts map keys alphabetically by default, so a plain
// clone is sufficient.
func sortedCopy(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// deepCloneScenario returns a deep copy of a Scenario for fingerprinting.
// Distinct from description_spec.go's cloneScenario (which keeps
// Examples shallow); this variant deep-copies the Examples rows too
// because we sort them by key.
func deepCloneScenario(s Scenario) Scenario {
	c := Scenario{
		Name:     s.Name,
		Tags:     append([]string(nil), s.Tags...),
		Steps:    append([]Step(nil), s.Steps...),
		OnFail:   append([]Step(nil), s.OnFail...),
		Examples: nil,
	}
	if len(s.Examples) > 0 {
		c.Examples = make([]map[string]string, len(s.Examples))
		for i, row := range s.Examples {
			if row == nil {
				continue
			}
			c.Examples[i] = make(map[string]string, len(row))
			for k, v := range row {
				c.Examples[i][k] = v
			}
		}
	}
	return c
}

// FingerprintSet returns every scenario in the set keyed by ScenarioID.
// Outline scenarios contribute one fingerprint per expanded row, matching
// the ScenarioID shape `desc:<origin>:<idx>[:row<n>]`.
//
// A nil or empty set returns an empty map.
func FingerprintSet(set *LabelDescriptionSet) map[string]string {
	out := make(map[string]string)
	if set == nil {
		return out
	}
	for _, sec := range [][]LabeledDescription{set.Layer, set.Image, set.Deploy} {
		for _, ld := range sec {
			for sIdx, scenario := range ld.Description.Scenarios {
				expanded := ExpandScenario(scenario)
				for _, es := range expanded {
					id := ScenarioID(ld.Origin, sIdx, es.RowIndex)
					out[id] = FingerprintScenario(es.Scenario)
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Verdict classification
// ---------------------------------------------------------------------------

// Verdict is the seven-way classification of a scenario's trajectory
// from pre-AI baseline to post-iteration state.
//
// The values are also the YAML string representation (lowercase).
type Verdict string

const (
	// VerdictSolved — baseline failed/errored; post-iteration passes;
	// fingerprint unchanged; no pending steps. Counts toward score.
	VerdictSolved Verdict = "solved"

	// VerdictPartial — progress without full green. Pending step count
	// decreased, OR some steps went pass that were failing, but the
	// scenario as a whole does not pass yet. Does NOT count toward
	// plateau score but is reported for authoring feedback.
	VerdictPartial Verdict = "partial"

	// VerdictUnchanged — no delta between baseline and post. Does not
	// count; primary driver of plateau.
	VerdictUnchanged Verdict = "unchanged"

	// VerdictRegressed — baseline passed; post-iteration fails. A "surprise
	// pass" in baseline that the AI broke. Reported loudly; plateau
	// counter increments (score_k ≤ score_{k-1}).
	VerdictRegressed Verdict = "regressed"

	// VerdictTampered — post-iteration passes AND fingerprint changed.
	// The AI modified the scenario text itself (weakened a check, added
	// a skip tag, …). Does NOT count as solved.
	VerdictTampered Verdict = "tampered"

	// VerdictRetagged — fingerprint unchanged in body but tags changed
	// (or other tag-only edit). Soft warning; treated as Unchanged for
	// scoring purposes.
	VerdictRetagged Verdict = "retagged"

	// VerdictAdded — scenario ID is present post-iteration but not in the
	// pre-AI baseline set. The AI added a new scenario. Reported in a
	// separate section of the report; contributes 0 to the score.
	VerdictAdded Verdict = "added"
)

// AllVerdicts lists every verdict in reporting order. Useful for test
// matrices and for emitting YAML summaries with stable key order.
var AllVerdicts = []Verdict{
	VerdictSolved, VerdictPartial, VerdictUnchanged, VerdictRegressed,
	VerdictTampered, VerdictRetagged, VerdictAdded,
}

// ScenarioState summarizes one scenario's state at a point in time
// (baseline or post-iteration). The benchmark builds one ScenarioState
// per scenario at baseline, and one per scenario at every iteration;
// Classify compares the two.
//
// Present==false means the scenario was absent from the baseline (or
// absent from this iteration's description collection). An absent
// scenario post-iteration where it was present in baseline is a clear
// signal the AI deleted it; that maps to VerdictTampered.
type ScenarioState struct {
	Present      bool
	Fingerprint  string // "" when !Present
	Status       string // "pass" | "fail" | "skip" | "" (when not run)
	PendingSteps int
	// TagFingerprint is a separate fingerprint of ONLY the tag set,
	// so we can distinguish "body unchanged, tags edited" (retagged)
	// from "body changed entirely" (tampered). Computed by the caller
	// via FingerprintTags(s.Tags).
	TagFingerprint string
}

// Classify compares a scenario's baseline state to its post-iteration
// state and returns the verdict per the plan's seven-outcome matrix.
//
// Priority order (first match wins):
//  1. Post-only scenario        -> VerdictAdded
//  2. Body fingerprint changed + post pass -> VerdictTampered
//  3. Tags-only changed         -> VerdictRetagged
//  4. Baseline pass + post fail -> VerdictRegressed
//  5. Baseline fail + post pass + no pending + same fingerprint -> VerdictSolved
//  6. Pending-step count decreased, or status improved but not fully green -> VerdictPartial
//  7. Otherwise                 -> VerdictUnchanged
func Classify(pre, post ScenarioState) Verdict {
	// 1. Added: present post-iteration only. pre.Present == false.
	if !pre.Present && post.Present {
		return VerdictAdded
	}
	// A scenario that vanished post-iteration (AI deleted it) is
	// treated as tampered — the author definition is gone, so its
	// verdict is no longer meaningful. We can't fingerprint something
	// that doesn't exist, so this short-circuits.
	if pre.Present && !post.Present {
		return VerdictTampered
	}
	// Both present beyond this point.

	bodyChanged := pre.Fingerprint != "" && post.Fingerprint != "" && pre.Fingerprint != post.Fingerprint
	tagsChanged := pre.TagFingerprint != "" && post.TagFingerprint != "" && pre.TagFingerprint != post.TagFingerprint

	// 2. Tampered: body (or body+tags) changed AND post passes.
	// The "post passes" guard matters because a tampering attempt that
	// does NOT achieve a pass is just a regression-in-progress.
	if bodyChanged {
		if post.Status == "pass" {
			return VerdictTampered
		}
		// Body changed but does not pass → still unchanged-or-partial.
		// Fall through to status-based classification below.
	}

	// 3. Retagged: only tags changed.
	if !bodyChanged && tagsChanged {
		return VerdictRetagged
	}

	// 4. Regressed: baseline passed; post fails.
	if pre.Status == "pass" && post.Status == "fail" {
		return VerdictRegressed
	}

	// 5. Solved: baseline failed/errored; post passes; no pending steps;
	//    fingerprint unchanged.
	baselineNotPassing := pre.Status == "fail" || pre.Status == "skip" || pre.Status == ""
	if baselineNotPassing && post.Status == "pass" && post.PendingSteps == 0 && !bodyChanged {
		return VerdictSolved
	}

	// 6. Partial: some form of progress without full green.
	//    (a) pending-step count strictly decreased, OR
	//    (b) baseline was fail and some steps pass now (status still fail
	//        but pending reduced is the proxy for this at scenario level).
	if post.PendingSteps < pre.PendingSteps {
		return VerdictPartial
	}

	// 7. Unchanged default.
	return VerdictUnchanged
}

// FingerprintTags returns a canonical SHA256 over the sorted tag set.
// Used by ScenarioState.TagFingerprint to distinguish body vs. tag edits.
//
// The output format matches FingerprintScenario: "sha256:<64 hex chars>".
func FingerprintTags(tags []string) string {
	sorted := append([]string(nil), tags...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}
