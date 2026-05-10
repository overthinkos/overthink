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
	"encoding/json"
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
//	  tag: [cache, service]
//	  scenario: [...]
//
// Singular-key convention: post-2026-04 cutover, every YAML key under
// description and every collection field on Description / Scenario uses
// the singular form (`scenario:`, `tag:`). The UnmarshalYAML shim below
// also accepts the legacy plural keys (`scenarios:`, `tags:`) for one
// release of overlap; layer.yml files in this repo are migrated by
// `ov migrate harness`.
type Description struct {
	Feature   string     `yaml:"feature"              json:"feature"`
	Narrative string     `yaml:"narrative,omitempty"  json:"narrative,omitempty"`
	Tag       []string   `yaml:"tag,omitempty"        json:"tag,omitempty"`
	Scenario  []Scenario `yaml:"scenario,omitempty"   json:"scenario,omitempty"`
}

// UnmarshalYAML accepts:
//
//   - the canonical mapping form (feature + narrative + scenario)
//   - a scalar shorthand (a single-line description that populates
//     Feature only, leaving Scenario empty); preserves pre-existing
//     `description: "..."` usage across layer.yml files
//   - the legacy plural keys (`scenarios:`, `tags:`) — accept-both
//     transitional shim removed at the close of the harness cutover
//     once `ov migrate harness` has rewritten every consumer.
func (d *Description) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		d.Feature = node.Value
		return nil
	case yaml.MappingNode:
		// Walk the mapping by hand so we can map BOTH `scenario:` and
		// `scenarios:` (and `tag:` / `tags:`) into the singular fields
		// during the migration window. The map walk also gives us the
		// node positions for clearer error messages.
		if len(node.Content)%2 != 0 {
			return fmt.Errorf("description: malformed mapping node (odd content count)")
		}
		for i := 0; i < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				return fmt.Errorf("description: non-scalar mapping key at line %d", k.Line)
			}
			switch k.Value {
			case "feature":
				if err := v.Decode(&d.Feature); err != nil {
					return fmt.Errorf("description.feature: %w", err)
				}
			case "narrative":
				if err := v.Decode(&d.Narrative); err != nil {
					return fmt.Errorf("description.narrative: %w", err)
				}
			case "tag", "tags":
				if err := v.Decode(&d.Tag); err != nil {
					return fmt.Errorf("description.%s: %w", k.Value, err)
				}
			case "scenario", "scenarios":
				if err := v.Decode(&d.Scenario); err != nil {
					return fmt.Errorf("description.%s: %w", k.Value, err)
				}
			default:
				return fmt.Errorf("description: unknown key %q at line %d (expected: feature, narrative, tag, scenario)", k.Value, k.Line)
			}
		}
		return nil
	default:
		return fmt.Errorf("description: unsupported YAML kind %d (expected scalar or mapping)", node.Kind)
	}
}

// UnmarshalJSON mirrors UnmarshalYAML's accept-both behavior for the
// `org.overthinkos.description` OCI label. Images built BEFORE the
// 2026-04 singular cutover carry the old plural keys (`scenarios`,
// `tags`) in their JSON-encoded label payload; this shim lets the
// harness read them without requiring every image to be rebuilt
// before it can be scored.
func (d *Description) UnmarshalJSON(data []byte) error {
	// Use a string→RawMessage view so we can map both plural and
	// singular keys into the same target field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch k {
		case "feature":
			if err := json.Unmarshal(v, &d.Feature); err != nil {
				return fmt.Errorf("description.feature: %w", err)
			}
		case "narrative":
			if err := json.Unmarshal(v, &d.Narrative); err != nil {
				return fmt.Errorf("description.narrative: %w", err)
			}
		case "tag", "tags":
			if err := json.Unmarshal(v, &d.Tag); err != nil {
				return fmt.Errorf("description.%s: %w", k, err)
			}
		case "scenario", "scenarios":
			if err := json.Unmarshal(v, &d.Scenario); err != nil {
				return fmt.Errorf("description.%s: %w", k, err)
			}
		}
	}
	return nil
}

// Scenario is a single BDD scenario. A scenario with a non-empty Examples
// list is a parameterized "Scenario Outline" in Gherkin parlance and fans
// out to one execution per row at collection / run time.
//
// OnFail steps run exactly once when any Step in this scenario fails;
// they are author-deliberate (no implicit defaults) and each OnFail step
// can carry its own On: target for multi-service diagnostics.
//
// YAML keys are singular post-2026-04 (`tag:` not `tags:`) — the custom
// UnmarshalYAML below accepts the legacy plural form for the duration of
// the harness cutover migration window.
type Scenario struct {
	Name     string              `yaml:"name"                 json:"name"`
	Tag      []string            `yaml:"tag,omitempty"        json:"tag,omitempty"`
	Step     []Step              `yaml:"step"                 json:"step,omitempty"`
	Example  []map[string]string `yaml:"example,omitempty"    json:"example,omitempty"`
	OnFail   []Step              `yaml:"on_fail,omitempty"    json:"on_fail,omitempty"`

	// Setup steps run before Steps. A Setup failure aborts the scenario
	// without running Steps but Teardown still runs. Use for fixture
	// install (seed notebook copy, MCP open_notebook_session, etc.).
	Setup []Step `yaml:"setup,omitempty" json:"setup,omitempty"`

	// Teardown steps ALWAYS run, even if Setup or Steps failed. Use for
	// cleanup that should never be skipped (close session, kill watcher).
	// Teardown failures are logged but do NOT escalate the scenario verdict.
	Teardown []Step `yaml:"teardown,omitempty" json:"teardown,omitempty"`

	// Pod is the container name this scenario's steps probe. Used by
	// kind:recipe scenarios in the harness; required at validation time
	// for any scenario inside a `recipe:` block. Layer- and image-baked
	// scenarios do not set this (their target is the image-baked test
	// runner). The harness scoring code does
	// `containerName := "ov-" + scenario.Pod` and dispatches every step
	// in this scenario through `podman exec ov-<pod>`.
	Pod string `yaml:"pod,omitempty" json:"pod,omitempty"`

	// SourceRecipe is populated by ResolveScoreRecipe when the scenario
	// is concatenated from a recipe referenced by a score's `recipes:`
	// list. Internal-only — never written to YAML or JSON; consumed by
	// the ${RECIPES} renderer to group rendering by source recipe.
	SourceRecipe string `yaml:"-" json:"-"`

	// DependsOn names other scenarios within the same recipe that must
	// have passed before this scenario runs. Used by the harness scorer
	// to topologically order execution across pod buckets — a scenario
	// in pod B depending on a scenario in pod A forces A's bucket to
	// run first. If any dep is fail/skipped at scoring time, this
	// scenario is marked status="skipped" with no probes executed.
	// Scope is intra-recipe only; cross-recipe references are rejected
	// by validateHarnessSemantics.
	DependsOn []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

// UnmarshalYAML accepts both `tag:` and the legacy `tags:` key during
// the cutover migration window. Removed once every layer.yml has been
// rewritten by `ov migrate harness`.
func (s *Scenario) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("scenario: expected mapping, got YAML kind %d", node.Kind)
	}
	if len(node.Content)%2 != 0 {
		return fmt.Errorf("scenario: malformed mapping node (odd content count)")
	}
	for i := 0; i < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return fmt.Errorf("scenario: non-scalar mapping key at line %d", k.Line)
		}
		switch k.Value {
		case "name":
			if err := v.Decode(&s.Name); err != nil {
				return fmt.Errorf("scenario.name: %w", err)
			}
		case "tag", "tags":
			if err := v.Decode(&s.Tag); err != nil {
				return fmt.Errorf("scenario.%s: %w", k.Value, err)
			}
		case "step":
			if err := v.Decode(&s.Step); err != nil {
				return fmt.Errorf("scenario.step: %w", err)
			}
		case "example":
			if err := v.Decode(&s.Example); err != nil {
				return fmt.Errorf("scenario.example: %w", err)
			}
		case "on_fail":
			if err := v.Decode(&s.OnFail); err != nil {
				return fmt.Errorf("scenario.on_fail: %w", err)
			}
		case "pod":
			if err := v.Decode(&s.Pod); err != nil {
				return fmt.Errorf("scenario.pod: %w", err)
			}
		case "depends_on":
			if err := v.Decode(&s.DependsOn); err != nil {
				return fmt.Errorf("scenario.depends_on: %w", err)
			}
		case "setup":
			if err := v.Decode(&s.Setup); err != nil {
				return fmt.Errorf("scenario.setup: %w", err)
			}
		case "teardown":
			if err := v.Decode(&s.Teardown); err != nil {
				return fmt.Errorf("scenario.teardown: %w", err)
			}
		default:
			return fmt.Errorf("scenario: unknown key %q at line %d (expected: name, tag, pod, depends_on, step, example, on_fail, setup, teardown)", k.Value, k.Line)
		}
	}
	return nil
}

// UnmarshalJSON mirrors UnmarshalYAML's accept-both behavior for OCI
// label payloads built before the 2026-04 singular cutover. Reads
// both `tag` (singular) and `tags` (legacy plural) into Scenario.Tag.
func (s *Scenario) UnmarshalJSON(data []byte) error {
	// Avoid infinite recursion via a local alias type.
	type rawScenario struct {
		Name     string              `json:"name"`
		Tag      []string            `json:"tag,omitempty"`
		Tags     []string            `json:"tags,omitempty"`
		Pod      string              `json:"pod,omitempty"`
		Step     []Step              `json:"step,omitempty"`
		Example  []map[string]string `json:"example,omitempty"`
		OnFail   []Step              `json:"on_fail,omitempty"`
		Setup    []Step              `json:"setup,omitempty"`
		Teardown []Step              `json:"teardown,omitempty"`
	}
	var r rawScenario
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	s.Name = r.Name
	if len(r.Tag) > 0 {
		s.Tag = r.Tag
	} else if len(r.Tags) > 0 {
		s.Tag = r.Tags
	}
	s.Pod = r.Pod
	s.Step = r.Step
	s.Example = r.Example
	s.OnFail = r.OnFail
	s.Setup = r.Setup
	s.Teardown = r.Teardown
	return nil
}

// Step is a single BDD step. Exactly one of Given/When/Then/And/But must
// be non-empty (validated by StepKeyword()). The embedded Check carries
// the verb, matchers, and modifiers for execution — a step with a keyword
// but no verb is "pending" (narrative only, advisory unless --strict).
//
// YAML inline-embed: `yaml:",inline"` on the embedded Check promotes every
// Check field to the Step's top level at parse time, so authors write
//
//   - then: the server replies with PONG
//     command: redis-cli ping
//     stdout: PONG
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
//
// NOTE: this list mirrors Check.verbsSet but is INTENTIONALLY a separate
// hand-maintained list rather than a delegation. Reason: the
// `summarize:` verb is currently broken in some scenarios (over_ids
// glob matching against recorded step IDs needs separate work), and
// flipping its IsPending status from true to false would surface
// pre-existing failures unrelated to whatever verb's being added.
// When summarize lands a real fix, this list and verbsSet should be
// unified. New verbs added in the meantime: include them here AND in
// CheckVerbs / verbsSet.
func (s *Step) IsPending() bool {
	c := &s.Check
	if c.File != "" || c.Package != "" || c.Service != "" || c.Port != 0 ||
		c.Process != "" || c.Command != "" || c.HTTP != "" || c.DNS != "" ||
		c.User != "" || c.Group != "" || c.Interface != "" || c.KernelParam != "" ||
		c.Mount != "" || c.Addr != "" || c.Matching != nil ||
		c.Cdp != "" || c.Wl != "" || c.Dbus != "" || c.Vnc != "" || c.Mcp != "" ||
		c.Record != "" || c.Spice != "" || c.Libvirt != "" || c.K8s != "" ||
		c.Kill != "" {
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
// Mirrors LabelEvalSet's shape so the collection + merge pipeline and
// the reporting format can share a mental model.
// LabelDescriptionSet and LabeledDescription were relocated to
// labelset.go in the 2026-04 BDD/test/harness surface-cleanup cutover,
// alongside the new LabelSet aggregate that wraps both LabelEvalSet and
// LabelDescriptionSet. See labelset.go for the type definitions and
// IsEmpty method.

// ---------------------------------------------------------------------------
// Scenario Outline expansion
// ---------------------------------------------------------------------------

// ExpandedScenario is a flattened scenario: the original scenario with
// outline placeholders substituted from a single Examples row (or the
// original scenario verbatim if there were no Examples). Used by the
// runner to emit one EvalResult per row without re-coding outline-aware
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
	if len(s.Example) == 0 {
		return []ExpandedScenario{{
			Scenario: s,
			RowIndex: -1,
		}}
	}

	out := make([]ExpandedScenario, 0, len(s.Example))
	for rowIdx, row := range s.Example {
		materialized := cloneScenario(s)
		materialized.Example = nil // already consumed
		placeholders := sortedExampleKeys(row)

		// Substitute in step keyword text + every string field on the embedded Check
		// for both regular Steps and OnFail steps.
		for i := range materialized.Step {
			applyOutlineSubs(&materialized.Step[i], row)
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

// substituteMatchers walks a matcher slice and substitutes placeholders
// inside any string-valued Matcher.Value. Non-string values are untouched.
//
// Takes []Matcher rather than MatcherList so callers can pass any named slice
// type whose underlying element is Matcher (MatcherList, ContainsList) without
// an explicit conversion at every call site. Mutations are visible to the
// caller since slice headers are shared.
func substituteMatchers(ml []Matcher, row map[string]string) {
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
	dup.Step = append([]Step(nil), s.Step...)
	dup.OnFail = append([]Step(nil), s.OnFail...)
	// Each step's MatcherList slices are shared with the source; we deep-copy
	// them here so applyOutlineSubs's in-place mutation doesn't leak.
	for i := range dup.Step {
		cloneStepMatchers(&dup.Step[i])
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

// cloneMatcherList returns a deep copy of a matcher slice. Returns the
// underlying type ([]Matcher) so callers can assign back to any named slice
// type whose underlying type is []Matcher (MatcherList, ContainsList) — the
// type-literal return is assignable to either named type per Go's
// assignability rules.
func cloneMatcherList(ml []Matcher) []Matcher {
	if len(ml) == 0 {
		return nil
	}
	out := make([]Matcher, len(ml))
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
