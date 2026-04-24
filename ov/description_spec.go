package main

// Gherkin-shaped `description:` schema — the canonical self-description for
// every `kind:` entity (layer, image, pod, vm, k8s, host, deployment).
//
// Design decisions (see the plan file):
//   - YAML-only. No `.feature` text files, no vendored Gherkin parser. The
//     Gherkin shape (Feature / Narrative / Scenarios / Given/When/Then/And/But,
//     Scenario Outline via `examples:`) is expressed as a YAML schema parsed
//     with gopkg.in/yaml.v3.
//   - No `background:` (redundant with scenario Givens for infrastructure
//     BDD — the system is mostly static between scenarios, so re-asserting
//     the same static facts per-scenario adds no value). Shared static
//     invariants belong in build-scope `tests:` or a build-scope scenario
//     that runs once against the disposable image.
//   - No `before:` / `after:` — those are Cucumber *code hooks*, not
//     Gherkin feature-file syntax. Cross-scenario deploy-level setup is
//     a deploy concern, not a test concern.
//   - Each scenario Step embeds Check inline, so every existing verb /
//     matcher / modifier from ov/testspec.go works in scenarios unchanged.

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Description is the Gherkin-shaped self-description of a single entity.
// Authoring shape:
//
//	description:
//	  feature: Redis in-memory data store
//	  narrative: |
//	    The redis layer installs redis-server and exposes a Redis-protocol
//	    endpoint on port 6379.
//	  tags: [cache, service]
//	  scenarios: [...]
type Description struct {
	Feature   string     `yaml:"feature"              json:"feature"`
	Narrative string     `yaml:"narrative,omitempty"  json:"narrative,omitempty"`
	Tags      []string   `yaml:"tags,omitempty"       json:"tags,omitempty"`
	Scenarios []Scenario `yaml:"scenarios,omitempty"  json:"scenarios,omitempty"`
}

// UnmarshalYAML accepts both the canonical mapping form (feature +
// narrative + scenarios) AND a scalar shorthand (a single-line
// description that populates Feature only, leaving Scenarios empty).
//
// The scalar form preserves pre-existing `description: "..."` usage
// across layer.yml files — same polymorphic pattern MatcherList uses.
// It is NOT a compatibility shim in the forbidden sense: it is how a
// structured type handles its own shorthand, idiomatic with the rest
// of the codebase's YAML surfaces.
func (d *Description) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		d.Feature = node.Value
		return nil
	case yaml.MappingNode:
		// Plain decode into an auxiliary type so we don't recurse.
		type raw Description
		var r raw
		if err := node.Decode(&r); err != nil {
			return err
		}
		*d = Description(r)
		return nil
	default:
		return fmt.Errorf("description: unsupported YAML kind %d (expected scalar or mapping)", node.Kind)
	}
}

// Scenario is a single BDD scenario. A scenario with a non-empty Examples
// list is a parameterized "Scenario Outline" in Gherkin parlance and fans
// out to one execution per row at collection / run time.
//
// OnFail steps run exactly once when any Step in this scenario fails;
// they are author-deliberate (no implicit defaults) and each OnFail step
// can carry its own On: target for multi-service diagnostics.
type Scenario struct {
	Name     string              `yaml:"name"                 json:"name"`
	Tags     []string            `yaml:"tags,omitempty"       json:"tags,omitempty"`
	Steps    []Step              `yaml:"steps"                json:"steps,omitempty"`
	Examples []map[string]string `yaml:"examples,omitempty"   json:"examples,omitempty"`
	OnFail   []Step              `yaml:"on_fail,omitempty"    json:"on_fail,omitempty"`
}

// Step is a single BDD step. Exactly one of Given/When/Then/And/But must
// be non-empty (validated by StepKeyword()). The embedded Check carries
// the verb, matchers, and modifiers for execution — a step with a keyword
// but no verb is "pending" (narrative only, advisory unless --strict).
//
// YAML inline-embed: `yaml:",inline"` on the embedded Check promotes every
// Check field to the Step's top level at parse time, so authors write
//
//	- then: the server replies with PONG
//	  command: redis-cli ping
//	  stdout: PONG
//
// rather than having to nest Check fields under a sub-map.
type Step struct {
	Given string `yaml:"given,omitempty" json:"given,omitempty"`
	When  string `yaml:"when,omitempty"  json:"when,omitempty"`
	Then  string `yaml:"then,omitempty"  json:"then,omitempty"`
	And   string `yaml:"and,omitempty"   json:"and,omitempty"`
	But   string `yaml:"but,omitempty"   json:"but,omitempty"`

	Check `yaml:",inline"  json:",inline"`
}

// StepKeywords lists valid keyword discriminator keys in document order.
// The ordering doubles as a semantic hint for reporters: Given → When → Then.
var StepKeywords = []string{"given", "when", "then", "and", "but"}

// StepKeyword returns the step's keyword name and an error if zero or
// multiple keyword discriminators are set. Parallel to Check.Kind().
func (s *Step) StepKeyword() (string, error) {
	set := s.keywordsSet()
	if len(set) == 0 {
		return "", fmt.Errorf("step has no keyword (expected exactly one of: %s)", strings.Join(StepKeywords, ", "))
	}
	if len(set) > 1 {
		return "", fmt.Errorf("step has multiple keywords (%s); exactly one is required", strings.Join(set, ", "))
	}
	return set[0], nil
}

// keywordsSet returns the list of keyword discriminators currently non-zero.
func (s *Step) keywordsSet() []string {
	var set []string
	if s.Given != "" {
		set = append(set, "given")
	}
	if s.When != "" {
		set = append(set, "when")
	}
	if s.Then != "" {
		set = append(set, "then")
	}
	if s.And != "" {
		set = append(set, "and")
	}
	if s.But != "" {
		set = append(set, "but")
	}
	return set
}

// KeywordText returns the populated keyword's text regardless of which
// discriminator holds it. Used by reporters and by `ov feature pending`.
func (s *Step) KeywordText() string {
	switch {
	case s.Given != "":
		return s.Given
	case s.When != "":
		return s.When
	case s.Then != "":
		return s.Then
	case s.And != "":
		return s.And
	case s.But != "":
		return s.But
	}
	return ""
}

// IsPending reports whether the step has no verb attached. Pending steps
// are narrative-only (no check executes) and are reported as "pending" —
// advisory by default, fail under --strict.
func (s *Step) IsPending() bool {
	// Walk the exact same verb list Check.verbsSet uses but stop at the first.
	c := &s.Check
	if c.File != "" || c.Package != "" || c.Service != "" || c.Port != 0 ||
		c.Process != "" || c.Command != "" || c.HTTP != "" || c.DNS != "" ||
		c.User != "" || c.Group != "" || c.Interface != "" || c.KernelParam != "" ||
		c.Mount != "" || c.Addr != "" || c.Matching != nil ||
		c.Cdp != "" || c.Wl != "" || c.Dbus != "" || c.Vnc != "" || c.Mcp != "" ||
		c.Record != "" || c.Spice != "" || c.Libvirt != "" || c.K8s != "" {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// OCI label shape: org.overthinkos.description
// ---------------------------------------------------------------------------

// LabelDescriptionSet is the three-section structure embedded in the
// org.overthinkos.description OCI label: layer-contributed descriptions
// (one per layer), image-level description (one), deploy-default
// description (one — usually from deploy.yml overlays).
//
// Mirrors LabelTestSet's shape so the collection + merge pipeline and
// the reporting format can share a mental model.
type LabelDescriptionSet struct {
	Layer  []LabeledDescription `json:"layer,omitempty"`
	Image  []LabeledDescription `json:"image,omitempty"`
	Deploy []LabeledDescription `json:"deploy,omitempty"`
}

// LabeledDescription is a Description with its collection-time origin
// annotation. Origin follows the `layer:<name>` / `image:<name>` /
// `deploy-default` / `deploy-local` convention already in use by
// LabelTestSet entries' Origin field.
type LabeledDescription struct {
	Origin      string      `json:"origin"`
	Description Description `json:"description"`
}

// IsEmpty returns true if no section has any descriptions. Used by label
// emission to omit the label entirely when there are none.
func (s *LabelDescriptionSet) IsEmpty() bool {
	if s == nil {
		return true
	}
	return len(s.Layer) == 0 && len(s.Image) == 0 && len(s.Deploy) == 0
}

// ---------------------------------------------------------------------------
// Scenario Outline expansion
// ---------------------------------------------------------------------------

// ExpandedScenario is a flattened scenario: the original scenario with
// outline placeholders substituted from a single Examples row (or the
// original scenario verbatim if there were no Examples). Used by the
// runner to emit one TestResult per row without re-coding outline-aware
// logic at every dispatch site.
//
// RowIndex is 0-based and stable across runs (deterministic ordering);
// RowLabel is a short human-readable key-value summary used in report
// output (`[port=6379, host=loopback]`) and in SCENARIO_ID suffixes.
type ExpandedScenario struct {
	Scenario             // the materialized scenario (post-substitution)
	ParentName  string   // original scenario name prior to row suffix
	RowIndex    int      // 0-based row index within the outline; -1 when not an outline
	RowLabel    string   // key=value pairs from the Examples row
	Placeholder []string // placeholder names consumed (for validation reporting)
}

// ExpandScenario returns a list of ExpandedScenarios: for a regular
// scenario it's a single-element list; for an outline it's one element
// per Examples row with `<placeholder>` substituted in step keyword
// text AND every Check string field.
//
// The substitution uses angle-bracket syntax (`<port>`, not `${port}`)
// to match Gherkin's outline convention and to avoid collision with the
// ${VAR} grammar that resolves at runtime from the variable resolver.
func ExpandScenario(s Scenario) []ExpandedScenario {
	if len(s.Examples) == 0 {
		return []ExpandedScenario{{
			Scenario: s,
			RowIndex: -1,
		}}
	}

	out := make([]ExpandedScenario, 0, len(s.Examples))
	for rowIdx, row := range s.Examples {
		materialized := cloneScenario(s)
		materialized.Examples = nil // already consumed
		placeholders := sortedExampleKeys(row)

		// Substitute in step keyword text + every string field on the embedded Check
		// for both regular Steps and OnFail steps.
		for i := range materialized.Steps {
			applyOutlineSubs(&materialized.Steps[i], row)
		}
		for i := range materialized.OnFail {
			applyOutlineSubs(&materialized.OnFail[i], row)
		}

		materialized.Name = s.Name + " [" + rowLabelFor(row, placeholders) + "]"

		out = append(out, ExpandedScenario{
			Scenario:    materialized,
			ParentName:  s.Name,
			RowIndex:    rowIdx,
			RowLabel:    rowLabelFor(row, placeholders),
			Placeholder: placeholders,
		})
	}
	return out
}

// rowLabelFor renders a row as `k1=v1, k2=v2` in key-sorted order so two
// runs produce the same label for the same row data.
func rowLabelFor(row map[string]string, keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+row[k])
	}
	return strings.Join(parts, ", ")
}

// applyOutlineSubs substitutes <placeholder> in a step's keyword text
// and every string field on its embedded Check, using the row's values.
func applyOutlineSubs(s *Step, row map[string]string) {
	// Keyword text substitution.
	s.Given = substitutePlaceholders(s.Given, row)
	s.When = substitutePlaceholders(s.When, row)
	s.Then = substitutePlaceholders(s.Then, row)
	s.And = substitutePlaceholders(s.And, row)
	s.But = substitutePlaceholders(s.But, row)

	// String fields on the embedded Check.
	for _, p := range s.Check.StringFields() {
		if *p == "" {
			continue
		}
		*p = substitutePlaceholders(*p, row)
	}

	// Also substitute in matcher values where strings appear. MatcherList
	// entries may carry strings, slices of strings, or numbers; we touch
	// only the string-typed ones.
	substituteMatchers(s.Check.Contains, row)
	substituteMatchers(s.Check.Stdout, row)
	substituteMatchers(s.Check.Stderr, row)
	substituteMatchers(s.Check.Body, row)
	substituteMatchers(s.Check.Headers, row)
	substituteMatchers(s.Check.Opts, row)
	substituteMatchers(s.Check.Value, row)
}

// substitutePlaceholders replaces every `<name>` with the row's value for
// that key. Unknown placeholders are left alone for the validator to flag.
func substitutePlaceholders(in string, row map[string]string) string {
	if in == "" || !strings.ContainsRune(in, '<') {
		return in
	}
	out := in
	for k, v := range row {
		out = strings.ReplaceAll(out, "<"+k+">", v)
	}
	return out
}

// substituteMatchers walks a MatcherList and substitutes placeholders
// inside any string-valued Matcher.Value. Non-string values are untouched.
func substituteMatchers(ml MatcherList, row map[string]string) {
	for i := range ml {
		switch v := ml[i].Value.(type) {
		case string:
			ml[i].Value = substitutePlaceholders(v, row)
		case []any:
			for j := range v {
				if s, ok := v[j].(string); ok {
					v[j] = substitutePlaceholders(s, row)
				}
			}
		}
	}
}

// cloneScenario returns a deep copy suitable for per-row materialization.
// Only the slices that get mutated by applyOutlineSubs are deep-copied;
// the rest is shallow since the source scenario is never mutated in place.
func cloneScenario(s Scenario) Scenario {
	dup := s
	dup.Steps = append([]Step(nil), s.Steps...)
	dup.OnFail = append([]Step(nil), s.OnFail...)
	// Each step's MatcherList slices are shared with the source; we deep-copy
	// them here so applyOutlineSubs's in-place mutation doesn't leak.
	for i := range dup.Steps {
		cloneStepMatchers(&dup.Steps[i])
	}
	for i := range dup.OnFail {
		cloneStepMatchers(&dup.OnFail[i])
	}
	return dup
}

// cloneStepMatchers deep-copies the MatcherList fields on a step so that
// per-row substitution doesn't poison sibling scenarios.
func cloneStepMatchers(s *Step) {
	s.Check.Contains = cloneMatcherList(s.Check.Contains)
	s.Check.Stdout = cloneMatcherList(s.Check.Stdout)
	s.Check.Stderr = cloneMatcherList(s.Check.Stderr)
	s.Check.Body = cloneMatcherList(s.Check.Body)
	s.Check.Headers = cloneMatcherList(s.Check.Headers)
	s.Check.Opts = cloneMatcherList(s.Check.Opts)
	s.Check.Value = cloneMatcherList(s.Check.Value)
}

func cloneMatcherList(ml MatcherList) MatcherList {
	if len(ml) == 0 {
		return nil
	}
	out := make(MatcherList, len(ml))
	copy(out, ml)
	return out
}

func sortedExampleKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Scenario-ID / Step-ID synthesis
// ---------------------------------------------------------------------------

// ScenarioID returns the stable identifier used both for deploy.yml
// overlay merge lookups and for ${SCENARIO_ID} substitution in step
// text and artifact paths.
//
// Shape: `desc:<origin>:<scenario-idx>[:row<n>]`.
func ScenarioID(origin string, scenarioIdx int, rowIdx int) string {
	if rowIdx >= 0 {
		return fmt.Sprintf("desc:%s:%d:row%d", origin, scenarioIdx, rowIdx)
	}
	return fmt.Sprintf("desc:%s:%d", origin, scenarioIdx)
}

// StepID extends ScenarioID with the step's position.
// Shape: `desc:<origin>:<scenario-idx>:<step-idx>[:row<n>]`.
func StepID(origin string, scenarioIdx, stepIdx int, rowIdx int) string {
	if rowIdx >= 0 {
		return fmt.Sprintf("desc:%s:%d:%d:row%d", origin, scenarioIdx, stepIdx, rowIdx)
	}
	return fmt.Sprintf("desc:%s:%d:%d", origin, scenarioIdx, stepIdx)
}

// ---------------------------------------------------------------------------
// Tag-set helpers
// ---------------------------------------------------------------------------

// EffectiveTags returns the union of a scenario's tags with a step's
// tags. Used by the --tag / --tag-exclude / --tags filters so a step
// inherits its enclosing scenario's tags without repetition in YAML.
func EffectiveTags(scenarioTags, stepTags []string) []string {
	if len(scenarioTags) == 0 && len(stepTags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(scenarioTags)+len(stepTags))
	var out []string
	for _, t := range scenarioTags {
		t = normalizeTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	for _, t := range stepTags {
		t = normalizeTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// normalizeTag strips a single leading '@' so `@smoke` and `smoke` are
// treated identically — authors commonly write `@smoke` from Gherkin
// habit but the leading sigil is optional in our YAML surface.
func normalizeTag(t string) string {
	t = strings.TrimSpace(t)
	if strings.HasPrefix(t, "@") {
		return t[1:]
	}
	return t
}
