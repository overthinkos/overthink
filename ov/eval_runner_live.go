package main

// harness_score_live.go — score the merged scenario set against the
// live deployments inside the eval-pod's nested podman.
//
// Post the 2026-04 pod-cutover, EVERY recipe scenario carries a
// `pod:` field naming the container its steps probe. The harness:
//
//  1. AI sees the scenarios via ${SCENARIOS}/${RECIPES} in its prompt.
//  2. AI builds whatever images each pod needs and `ov deploy add`s
//     each pod by name inside eval-pod's nested podman.
//  3. After the AI exits, this code groups scenarios by their `Pod`
//     field and runs each group's scenarios against `ov-<pod>` via
//     a fresh ContainerExecutor.
//  4. The result feeds the same 7-way Classify pipeline as today.
//
// One field, one container per scenario. No defaults, no fallbacks.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// RunScoringOpts carries optional knobs into RunEvalLive.
// Zero value preserves pre-2026-04-27 behaviour (re-probe everything,
// no freshness gate). Populated by the harness orchestrator at iter
// scoring time to:
//
//   - Activate AI-artifact validation mode for the seven state-
//     dependent capture probes (cdp/wl/vnc/libvirt/spice screenshot,
//     spice cursor, record stop) when score.validate_ai_artifacts is
//     true. ALL OTHER probes still re-run independently.
//
//   - Provide a benchmark-start floor for the freshness mtime gate.
//     Artifacts must have mtime ≥ IterStartTime or the probe fails
//     with a clear stale-artifact error — this is the load-bearing
//     anti-deception mechanism that prevents the AI from pre-staging
//     or carrying-forward doctored artifact files. The harness
//     populates IterStartTime with the BENCHMARK start (NOT per-iter
//     start) so artifacts produced legitimately in earlier phases
//     survive scoring through later phases. The field name is
//     historical; semantically this is the run/benchmark start.
type RunScoringOpts struct {
	ValidateAiArtifacts bool
	IterStartTime       time.Time
}

// RunEvalLive scores `scenarios` against the live containers
// they target via their `Pod` field. Returns a EvalRunResults shaped
// like ParseOvTestOutput's, so the existing scorer (Classify,
// fingerprints, summary) consumes it unchanged.
//
// Scenarios are grouped by their `Pod` field; each group runs against
// a separate `ContainerExecutor{ContainerName: "ov-" + pod}`. If a
// pod's container isn't reachable, every scenario in that bucket
// returns a "not reachable" verdict (status: fail, no steps); the
// remaining buckets are still scored.
//
// `scoreName` and `deployment` parameters are kept on the signature
// for callsite stability but only `scoreName` is used (for narrative
// labelling). The legacy `deployment` argument is now ignored —
// scoring follows scenario.Pod, never score.deployment.
func RunEvalLive(ctx context.Context, deployment, scoreName string, scenarios []Scenario, opts RunScoringOpts) (*EvalRunResults, error) {
	_ = deployment // legacy parameter, no longer consulted

	if len(scenarios) == 0 {
		return &EvalRunResults{}, nil
	}

	// Defensive pod check (validator catches this earlier; double-tap).
	for _, sc := range scenarios {
		if sc.Pod == "" {
			return nil, fmt.Errorf("scenario %q has empty pod field — validator should have rejected this; refusing to score", sc.Name)
		}
	}

	// Topologically sort by depends_on, tie-break by declaration order,
	// then group consecutive same-pod runs into execution buckets. This
	// preserves the one-`podman exec`-per-bucket efficiency in the
	// common case (no cross-pod deps) while honoring cross-pod
	// dependencies when they exist (a single pod may legitimately span
	// multiple buckets).
	//
	// On *CycleError: rather than wiping the entire phase score (the
	// pre-2026-04 behavior), score the non-cyclic subset normally and
	// emit a per-scenario fail verdict for each cyclic scenario at the
	// end. Preserves Section-5 invariant (1 Check = 1 ScenarioID = 1
	// point — cyclic scenarios just get a deterministic fail verdict
	// instead of being invisibly subsumed by a phase-wide BuildFailure).
	// Non-cycle errors still propagate up.
	sorted, err := topoSortByDeclarationOrder(scenarios)
	cyclicKeys := map[scenarioKey]bool{}
	if err != nil {
		var ce *CycleError
		if errors.As(err, &ce) {
			for _, n := range ce.Cycle {
				for _, sc := range scenarios {
					if sc.Name == n {
						cyclicKeys[keyOf(sc)] = true
					}
				}
			}
			fmt.Fprintf(os.Stderr,
				"score live: dependency cycle detected (%d scenarios) — scoring non-cyclic scenarios; cyclic scenarios will be reported as fail verdicts\n",
				len(cyclicKeys))
			sorted = filterOutCyclic(scenarios, cyclicKeys)
		} else {
			return nil, fmt.Errorf("scoring scenarios: %w", err)
		}
	}
	buckets := groupConsecutiveByPod(sorted)

	// IDs used by ScenarioEvalResult must match the IDs that
	// synthesizeScoreBaseline emits — those use a per-pod sIdx counter
	// indexed against scenario declaration order in the recipe (NOT
	// topo-sort order). Compute a stable (SourceRecipe, Name) → ID map
	// up front so the runtime path can attach the matching ID
	// regardless of the post-topo execution order. Keyed by
	// scenarioKey so cross-recipe duplicate names (e.g. multiple
	// recipes importing "sshd-binary") don't collide.
	idByKey := stableScenarioIDsByKey(scenarios)

	out := &EvalRunResults{
		Image: "score:" + scoreName,
		Mode:  "run",
	}

	// verdictByKey tracks the live status of each scenario as buckets
	// run, so cross-bucket depends_on cascades work. Values: "pass" |
	// "fail" | "skipped". Anything other than "pass" blocks dependents.
	// Keyed by (SourceRecipe, Name) so cross-recipe duplicate names
	// don't fight over a single map entry — each recipe sees its own
	// scenario's verdict for depends_on resolution.
	verdictByKey := make(map[scenarioKey]string, len(scenarios))

	// Pre-resolve the deployment tree once per call. Dotted scenario.Pod
	// values (e.g. "bench-vm.inner.nested-redis") are walked through this
	// tree by ResolveDeployChain to build multi-hop executor chains. Flat
	// pod names (e.g. "redis") that aren't in the tree fall through to
	// ContainerChain — same single-hop semantics as the pre-cutover code.
	// A nil tree (no project deploy.yml in cwd) is fine: the fallback path
	// kicks in for every scenario.
	cwd, _ := os.Getwd()
	deployRoots, _ := resolveTreeRoot(cwd)

	for _, bucket := range buckets {
		if len(bucket) == 0 {
			continue
		}
		pod := bucket[0].Pod

		// Resolve scenario.Pod to a DeployExecutor chain. Dotted paths
		// route through ResolveDeployChain (multi-hop); flat names
		// fall back to ContainerChain (single-hop into "ov-<pod>"),
		// preserving the pre-cutover harness behaviour exactly.
		chainExec, chainErr := resolveScoringChain(deployRoots, pod)

		// Reachability check via the chain itself: a tiny `echo ok`
		// proves the executor stack actually reaches the leaf venue.
		// For multi-hop chains this exercises every hop the real
		// probes will use.
		reachableErr := chainErr
		if reachableErr == nil {
			out, _, exit, err := chainExec.RunCapture(ctx, "echo ok")
			if err != nil {
				reachableErr = fmt.Errorf("chain %q unreachable: %w", chainExec.Venue(), err)
			} else if exit != 0 {
				reachableErr = fmt.Errorf("chain %q probe non-zero (%d): %s", chainExec.Venue(), exit, strings.TrimSpace(out))
			}
		}
		if reachableErr != nil {
			fmt.Fprintf(os.Stderr, "score live: pod %q unreachable: %v\n", pod, reachableErr)
		}

		var runner *Runner
		if reachableErr == nil {
			resolver := &EvalVarResolver{}
			runner = NewRunner(chainExec, resolver, RunModeLive)
			runner.Image = pod
			// Propagate score-level AI-artifact validation policy +
			// iter-start freshness floor into the runner. The runner
			// uses these in runOvVerb's artifact-producing branch to
			// decide whether to re-execute the probe (default: yes,
			// always) or validate the AI's iteration artifact (when
			// the flag is set AND the verb/method is in the narrow
			// state-dependent allowlist artifactValidatableMethods).
			runner.ValidateAiArtifacts = opts.ValidateAiArtifacts
			runner.IterStartTime = opts.IterStartTime
		}

		// Process scenarios sequentially within the bucket. The dep
		// check uses the LIVE verdictByKey, which is updated after
		// every probe — so an intra-bucket dep (handwritten depends_on
		// imported sshd-binary in the same composition-app pod) is
		// satisfied as soon as the dep scenario passes earlier in the
		// loop. Pre-batching the dep check (the prior shape) caused
		// false dep-unmet skips for every intra-bucket dep because
		// verdictByKey was empty when the bucket loop started.
		for _, sc := range bucket {
			blocked := firstUnmetDep(sc, verdictByKey)
			if blocked != "" {
				out.Scenario = append(out.Scenario, skippedResult(sc, idByKey[keyOf(sc)], blocked))
				out.Summary.Total++
				out.Summary.Skip++
				verdictByKey[keyOf(sc)] = "skipped"
				continue
			}

			if reachableErr != nil {
				// Container missing → record per-scenario "not reachable"
				// fail-status verdicts with no steps; continue.
				expanded := ExpandScenario(sc)
				for _, es := range expanded {
					tr := ScenarioEvalResult{
						ID:           idByKey[keyOf(sc)],
						Origin:       "pod:" + pod,
						Name:         es.Name,
						Tag:          append([]string(nil), es.Tag...),
						Status:       "fail",
						PendingSteps: 0,
					}
					out.Scenario = append(out.Scenario, tr)
					out.Summary.Total++
					out.Summary.Fail++
				}
				verdictByKey[keyOf(sc)] = "fail"
				continue
			}

			set := &LabelDescriptionSet{
				Layer: []LabeledDescription{{
					Origin: "pod:" + pod,
					Description: Description{
						Feature:  fmt.Sprintf("Score scenarios for pod %s", pod),
						Scenario: []Scenario{sc},
					},
				}},
			}
			results := RunScenarios(ctx, runner, set, nil, false)
			if len(results) == 0 {
				tr := ScenarioEvalResult{
					ID:     idByKey[keyOf(sc)],
					Origin: "pod:" + pod,
					Name:   sc.Name,
					Status: "fail",
				}
				out.Scenario = append(out.Scenario, tr)
				out.Summary.Total++
				out.Summary.Fail++
				verdictByKey[keyOf(sc)] = "fail"
				continue
			}
			sr := results[0]
			tr := ScenarioEvalResult{
				ID:           idByKey[keyOf(sc)],
				Origin:       sr.Origin,
				Name:         sr.Name,
				Tag:          append([]string(nil), sr.Tag...),
				Status:       sr.Status.String(),
				PendingSteps: sr.Pending,
			}
			for _, sp := range sr.Steps {
				step := StepEvalResult{
					Keyword: sp.Keyword,
					Text:    sp.Text,
					StepID:  sp.StepID,
					Status:  sp.Result.Status.String(),
					Verb:    sp.Result.Verb,
				}
				if sp.Result.Verb == "" {
					step.Pending = true
				}
				tr.Steps = append(tr.Steps, step)
			}
			out.Scenario = append(out.Scenario, tr)
			out.Summary.Total++
			switch tr.Status {
			case "pass":
				out.Summary.Pass++
				verdictByKey[keyOf(sc)] = "pass"
			case "fail":
				out.Summary.Fail++
				verdictByKey[keyOf(sc)] = "fail"
			case "skip":
				out.Summary.Skip++
				verdictByKey[keyOf(sc)] = "fail" // skipped-by-runner ≠ depends-skipped
			}
		}
	}

	// After the bucket loop: emit per-scenario fail verdicts for any
	// scenarios excluded from sorting because they belong to a
	// dependency cycle. Done in declaration order so the result slice
	// is deterministic. Section-5 invariant preserved: each Check still
	// contributes exactly one ScenarioID; cyclic scenarios just get a
	// deterministic fail verdict instead of disappearing into a
	// phase-wide BuildFailure.
	if len(cyclicKeys) > 0 {
		for _, sc := range scenarios {
			if !cyclicKeys[keyOf(sc)] {
				continue
			}
			out.Scenario = append(out.Scenario, ScenarioEvalResult{
				ID:            idByKey[keyOf(sc)],
				Origin:        "pod:" + sc.Pod,
				Name:          sc.Name,
				Tag:           append([]string(nil), sc.Tag...),
				Status:        "fail",
				PendingSteps:  0,
				SkippedReason: "cycle: scenario is part of a depends_on cycle",
			})
			out.Summary.Total++
			out.Summary.Fail++
			verdictByKey[keyOf(sc)] = "fail"
		}
	}
	return out, nil
}

// filterOutCyclic returns the input slice with every scenario whose
// scenarioKey is in cyclicKeys removed. Order is preserved. Used by
// RunEvalLive when topoSortByDeclarationOrder reports a
// *CycleError to score the non-cyclic subset normally.
func filterOutCyclic(scenarios []Scenario, cyclicKeys map[scenarioKey]bool) []Scenario {
	if len(cyclicKeys) == 0 {
		return scenarios
	}
	out := make([]Scenario, 0, len(scenarios))
	for _, sc := range scenarios {
		if cyclicKeys[keyOf(sc)] {
			continue
		}
		out = append(out, sc)
	}
	return out
}

// skippedResult builds a depends-on-cascade skip result for one
// scenario. Status "skipped" (distinct from runner-side "skip") is
// recognized by Classify and surfaces as VerdictSkipped.
func skippedResult(sc Scenario, id, blockedBy string) ScenarioEvalResult {
	return ScenarioEvalResult{
		ID:            id,
		Origin:        "pod:" + sc.Pod,
		Name:          sc.Name,
		Tag:           append([]string(nil), sc.Tag...),
		Status:        "skipped",
		PendingSteps:  0,
		SkippedReason: "dep-unmet: " + blockedBy,
	}
}

// stableScenarioIDsByKey returns a (SourceRecipe, Name) → scenario-ID
// map computed from the scenarios in declaration order, using the
// SAME bucketing scheme as synthesizeScoreBaseline (per-pod sIdx
// counter + ExpandScenario for row index). Topo-sort and
// bucket-grouping reorder execution but MUST NOT shift IDs —
// otherwise baseline IDs (computed pre-AI) would not match runtime
// IDs and every scenario would mis-classify as Tampered
// (pre.Present=true, post.Present=false).
//
// Keying by scenarioKey (rather than just Name) handles the
// composition case where two recipes import a scenario with the same
// name targeting the same pod — without the SourceRecipe scope, the
// second scenario's ID would overwrite the first's and runtime
// verdicts would attach to the wrong scenario.
//
// For non-outline scenarios, ExpandScenario returns a single entry
// with RowIndex=-1 → ScenarioID produces "desc:<origin>:<sIdx>" (no
// :row<n> suffix), which is what synthesizeScoreBaseline emits.
// Outline scenarios produce one ID per expanded row.
func stableScenarioIDsByKey(scenarios []Scenario) map[scenarioKey]string {
	out := make(map[scenarioKey]string, len(scenarios))
	bucketIdx := map[string]int{}
	for _, sc := range scenarios {
		origin := "pod:" + sc.Pod
		sIdx := bucketIdx[origin]
		bucketIdx[origin]++
		expanded := ExpandScenario(sc)
		if len(expanded) == 0 {
			// Non-expandable scenario; use RowIndex=-1 to match the
			// non-outline path in synthesizeScoreBaseline.
			out[keyOf(sc)] = ScenarioID(origin, sIdx, -1)
			continue
		}
		// First-row ID (sufficient for non-outline scenarios where
		// expanded[0].RowIndex == -1; outline scenarios emit only the
		// first row's ID under the scenario name — outline expansions
		// are handled within RunScenarios at probe time and matched by
		// scenario Name).
		out[keyOf(sc)] = ScenarioID(origin, sIdx, expanded[0].RowIndex)
	}
	return out
}

// resolveScoringChain returns the DeployExecutor chain that reaches
// `pod`. Selection rules:
//
//   - pod contains a dot AND `roots` resolves it → ResolveDeployChain
//     (multi-hop chain through the deployment tree).
//   - pod is a flat name OR not in the tree → ContainerChain
//     (single-hop into "ov-<pod>"); preserves the pre-cutover behaviour.
//
// A nil tree (no deploy.yml in cwd) always falls through to ContainerChain
// — the harness loop running inside eval-pod with no nested infra still
// scores flat pods exactly like before.
func resolveScoringChain(roots map[string]DeploymentNode, pod string) (DeployExecutor, error) {
	if strings.Contains(pod, ".") && roots != nil {
		_, chain, err := ResolveDeployChain(roots, pod, LocalDeployExecutor{})
		if err == nil {
			return chain, nil
		}
		// Dotted but unresolvable — surface a hard error so the
		// recipe author sees the chain construction failure rather
		// than a silent fall-back to a non-existent ov-foo.bar
		// container.
		return nil, fmt.Errorf("scenario.pod %q is dotted but does not resolve through deploy tree: %w", pod, err)
	}
	// Flat pod or no tree — single-hop into "ov-<pod>", same shape
	// as the pre-cutover hardcoded ContainerExecutor.
	return ContainerChain("podman", "ov-"+pod), nil
}

// containerRunningForScoring confirms <containerName> is running.
// Retained for callers that need a single-container reachability probe
// outside the chain-driven scoring loop.
func containerRunningForScoring(ctx context.Context, containerName string) error {
	out, err := exec.CommandContext(ctx, "podman", "inspect", "--format", "{{.State.Running}}", containerName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("container %q not reachable: %w\n%s",
			containerName, err, string(out))
	}
	if !strings.Contains(string(out), "true") {
		return fmt.Errorf("container %q exists but is not running", containerName)
	}
	return nil
}

// synthesizeScoreBaseline builds the pre-AI baseline from the merged
// scenario set, bucketing scenarios by their `Pod` field. Each scenario
// is marked status: fail at baseline (nothing's deployed yet); IDs use
// per-pod sIdx so they match the runtime IDs produced by RunScenarios
// inside RunEvalLive (which also buckets by Pod).
//
// Without per-pod bucketing the baseline IDs would index against the
// flat merged list (sIdx 0..N) and never match runtime IDs (sIdx
// 0..M per pod) — every non-first-bucket scenario would mis-classify
// as Tampered (pre.Present=true, post.Present=false), triggering a
// false solved-all exit.
func synthesizeScoreBaseline(scoreName string, scenarios []Scenario) ([]ScenarioEvalResult, map[string]string, map[string]string) {
	_ = scoreName // legacy parameter, kept for callsite stability

	var out []ScenarioEvalResult
	fps := make(map[string]string)
	tagFps := make(map[string]string)

	// Per-pod sIdx counter — must match the bucketing in
	// RunEvalLive so baseline IDs == runtime IDs.
	bucketIdx := map[string]int{}
	for _, scenario := range scenarios {
		if scenario.Pod == "" {
			// Validator should reject; defensive skip.
			continue
		}
		origin := "pod:" + scenario.Pod
		sIdx := bucketIdx[origin]
		bucketIdx[origin]++

		expanded := ExpandScenario(scenario)
		for _, es := range expanded {
			id := ScenarioID(origin, sIdx, es.RowIndex)
			pending := 0
			for _, step := range es.Steps {
				if step.IsPending() {
					pending++
				}
			}
			out = append(out, ScenarioEvalResult{
				ID:           id,
				Origin:       origin,
				Name:         es.Name,
				Tag:          append([]string(nil), es.Tag...),
				Status:       "fail",
				PendingSteps: pending,
			})
			fps[id] = FingerprintScenario(es.Scenario)
			tagFps[id] = FingerprintTags(es.Tag)
		}
	}
	return out, fps, tagFps
}

// RenderRecipeScenariosYAML returns the merged scenario list as a
// YAML block, suitable for ${SCENARIOS} substitution in the prompt.
// (Function name retained for callsite stability.)
func RenderRecipeScenariosYAML(scenarios []Scenario) string {
	if len(scenarios) == 0 {
		return ""
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(scenarios); err != nil {
		return fmt.Sprintf("# error rendering scenarios: %v", err)
	}
	_ = enc.Close()
	return buf.String()
}
