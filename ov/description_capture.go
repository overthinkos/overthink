package main

// ScenarioContext carries per-scenario mutable state across the
// execution of that scenario's steps — principally the capture store
// populated by checks with `capture: <name>`. Instantiated fresh per
// scenario (and per outline row) so cross-scenario state never leaks.
//
// The struct also threads the scenario- and step-level identifiers
// through variable expansion (${SCENARIO_ID}, ${STEP_ID}) so artifact
// paths and narrative text can embed stable references without the
// runner having to know about them.

// A ScenarioContext is OWNED by one scenario's execution pass. When
// that pass completes, the context is discarded; the next scenario
// gets a fresh one. `capture:` values never survive the scenario
// that produced them.
type ScenarioContext struct {
	// ScenarioID is the stable identifier assigned by the collector
	// (desc:<origin>:<scenario-idx>[:row<n>]). Resolves as ${SCENARIO_ID}.
	ScenarioID string

	// CurrentStepID is rewritten for each step as the scenario executes,
	// so ${STEP_ID} references resolve to the currently-running step's
	// identifier. Reporters surface this on failures.
	CurrentStepID string

	// Captures is the scenario-local stash populated by checks with
	// `capture:` set. Keys are the unprefixed capture names (e.g.
	// `login_tab`); the variable resolver looks them up under
	// `CAPTURED:<name>` so the existing ${NAME:arg} grammar in
	// testspec.go does the substitution for free.
	Captures map[string]string
}

// NewScenarioContext returns an empty context bound to the given
// scenario identifier. Outline-row executions each get their own
// context so captured values from row-0 don't leak into row-1.
func NewScenarioContext(scenarioID string) *ScenarioContext {
	return &ScenarioContext{
		ScenarioID: scenarioID,
		Captures:   map[string]string{},
	}
}

// Capture stores a value produced by a passing check under `capture:`.
// Called only on PASS (failing `eventually:` attempts must not pollute).
// Empty name / empty value is a no-op — authoring ergonomics: writing
// `capture:` on a check that didn't actually produce output shouldn't
// crash the run.
func (s *ScenarioContext) Capture(name, value string) {
	if s == nil || name == "" || value == "" {
		return
	}
	if s.Captures == nil {
		s.Captures = map[string]string{}
	}
	s.Captures[name] = value
}

// Get returns the captured value for a name, or ("", false) if unset.
// Used by the variable resolver's CAPTURED:<name> branch.
func (s *ScenarioContext) Get(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	v, ok := s.Captures[name]
	return v, ok
}

// ApplyToEnv merges scenario-scope variables into an env map for
// variable expansion. Called by the runner immediately before
// `Check.ExpandVars(env)` so the existing `${NAME[:arg]}` grammar
// picks up captures, scenario id, and step id without knowing about
// the ScenarioContext type.
//
// Keys populated:
//
//   - SCENARIO_ID          → ctx.ScenarioID
//   - STEP_ID              → ctx.CurrentStepID
//   - CAPTURED:<name>      → value stored via Capture(name, value)
//
// ApplyToEnv overlays — it never overwrites existing keys so a
// host-level `${SCENARIO_ID}` override (if ever introduced for
// testing) continues to win. The runner builds env by copying its
// resolver's base env first and then calling ApplyToEnv on the copy.
func (s *ScenarioContext) ApplyToEnv(env map[string]string) {
	if s == nil || env == nil {
		return
	}
	if _, exists := env["SCENARIO_ID"]; !exists && s.ScenarioID != "" {
		env["SCENARIO_ID"] = s.ScenarioID
	}
	if _, exists := env["STEP_ID"]; !exists && s.CurrentStepID != "" {
		env["STEP_ID"] = s.CurrentStepID
	}
	for name, value := range s.Captures {
		key := "CAPTURED:" + name
		if _, exists := env[key]; !exists {
			env[key] = value
		}
	}
}

// ExtractCaptureValue picks the natural captured value from a check's
// runtime output. Each verb has an obvious capture target (command
// stdout, http body, cdp method output, etc.); this function
// centralises the mapping so capture semantics stay consistent across
// verbs.
//
// Today's heuristic: use the TestResult's Message field. Verb handlers
// overwrite Message with their primary output on PASS (for example
// `runCommand` includes stdout in the Message; `runCDP` in
// `testrun_ov_verbs.go` puts captured subprocess stdout there). The
// caller (Runner) holds the stdout verbatim for verbs where Message
// is a summary rather than raw output and supplies that instead.
//
// Callers should prefer CaptureFromResult when they have a TestResult
// and Captures already knows the raw value (e.g. command stdout); it
// falls back to Result.Message for verbs that don't carry the raw
// value separately.
func CaptureFromResult(rawValue, resultMessage string) string {
	if rawValue != "" {
		return rawValue
	}
	return resultMessage
}
