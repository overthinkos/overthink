package migrate

// migrate_plan_unify.go — `charly migrate`.
//
// The plan-unify cutover collapses the entire test/eval/benchmark surface into
// ONE flat `plan:` vocabulary. Per entity (candy / box / pod / vm / deploy):
//
//   - task: entries → run: steps prepended to plan: (each run:'s prose derived
//     from its verb via opPrimaryKey; cmd→command / user→run_as already done by
//     the op-unify step);
//   - scenario: → plan: — each scenario group flattens into the flat step list:
//     setup: steps hoist to the front as run:, teardown: steps to the end as
//     run:, and each step: classified to a keyword (table below); each step
//     gets an id: (the old scenario name + index) when absent; scenario-level
//     depends_on:[name] rewrites to the depended scenario's step ids; the
//     scenario's pod: stamps onto each step; name:/example:/on_fail: drop;
//   - description: struct → string (feature, then narrative newline-joined; tag
//     dropped);
//   - example: outlines drop (0 in corpus); on_fail: drops.
//
// Keyword classification (first match wins):
//   1. from setup:/teardown: (provisioning position) → run: (agent-* if prose)
//   2. prose-only (no Op verb)                        → agent-check:
//   3. agent: verb, or do: instruct                   → agent-check:
//   4. do: act                                        → run:
//   5. do: assert                                     → check:
//   6. no do:, VerbCatalog DefaultDo == act           → run:
//   7. no do:, DefaultDo == assert (incl. live verbs) → check:
//
// Harness transform: each kind: score's orchestration → an iterate: block on
// the target deploy + that deploy's plan: built from the score's recipe: refs
// (each recipe's from: → include: <kind>:<name>, its inline scenario: →
// flattened steps). recipe:/score: blocks are deleted.
//
// Comment-preserving (yaml.v3 node API); idempotent (a migrated entity has no
// task:/scenario:/recipe:/score:/example:/setup:/teardown:/on_fail: and a
// scalar description:).

import (
	"gopkg.in/yaml.v3"
)

// MigratePlanUnify rewrites the entire test/eval surface to the flat plan:
// vocabulary across a project's candy/ + box/ dirs and root YAML siblings.
// Returns the rewritten file paths. Idempotent.
func MigratePlanUnify(dir string, dryRun bool) ([]string, error) {
	return runDocMigration(dir, dryRun, opUnifyCandidateFiles, planUnifyDoc)
}

// planUnifyDoc transforms every entity reachable in one doc.
func planUnifyDoc(doc *yaml.Node) bool {
	root := rootMappingNode(doc)
	if root == nil {
		return false
	}
	changed := false

	// Per-entity plan transform on the standard kind containers. `check` is the
	// kind:check bed registry (the renamed eval: registry) — its beds are
	// deploy-shaped entities that carry scenario:/task: like any deploy.
	for _, k := range []string{"candy", "box", "pod", "vm", "deploy", "k8s", "local", "android", "check", "agent"} {
		m := findMappingValue(root, k)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		if findMappingValue(m, "name") != nil {
			if planUnifyEntity(m) { // single kind-keyed entity
				changed = true
			}
			continue
		}
		for i := 0; i+1 < len(m.Content); i += 2 { // map of name → entity
			if planUnifyEntity(m.Content[i+1]) {
				changed = true
			}
		}
	}

	// Harness transform: fold the root score:/recipe: blocks into deploy iterate:.
	if planUnifyHarness(root) {
		changed = true
	}
	return changed
}

// planUnifyEntity applies the per-entity plan transforms and recurses into
// nested:/peer: children.
func planUnifyEntity(m *yaml.Node) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	// 1. description struct → scalar string.
	changed := collapseDescription(m)

	// 2. task: entries → run: steps, prepended to plan:.
	if task := findMappingValue(m, "task"); task != nil && task.Kind == yaml.SequenceNode {
		var runSteps []*yaml.Node
		for _, t := range task.Content {
			if t.Kind != yaml.MappingNode {
				continue
			}
			runSteps = append(runSteps, taskToRunStep(t))
		}
		removeMappingKey(m, "task")
		prependPlanSteps(m, runSteps)
		changed = true
	}

	// 3. scenario: → plan: (flatten each scenario group).
	if sc := findMappingValue(m, "scenario"); sc != nil && sc.Kind == yaml.SequenceNode {
		var flat []*yaml.Node
		for _, scen := range sc.Content {
			flat = append(flat, flattenScenario(scen)...)
		}
		removeMappingKey(m, "scenario")
		appendPlanSteps(m, flat)
		changed = true
	}

	// Recurse into nested deployment children.
	for _, k := range []string{"nested", "peer"} {
		if child := findMappingValue(m, k); child != nil && child.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(child.Content); i += 2 {
				if planUnifyEntity(child.Content[i+1]) {
					changed = true
				}
			}
		}
	}
	return changed
}

// collapseDescription rewrites a description: MAPPING ({feature, narrative,
// tag}) into a scalar string (feature, then narrative newline-joined; tag
// dropped). A scalar description: is left as-is (idempotent).
func collapseDescription(m *yaml.Node) bool {
	desc := findMappingValue(m, "description")
	if desc == nil || desc.Kind != yaml.MappingNode {
		return false
	}
	feature := ""
	narrative := ""
	if f := findMappingValue(desc, "feature"); f != nil && f.Kind == yaml.ScalarNode {
		feature = f.Value
	}
	if n := findMappingValue(desc, "narrative"); n != nil && n.Kind == yaml.ScalarNode {
		narrative = n.Value
	}
	val := feature
	if narrative != "" {
		if val != "" {
			val += "\n"
		}
		val += narrative
	}
	// Replace the mapping value node in place with a scalar.
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "description" {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: val}
			return true
		}
	}
	return false
}

// taskToRunStep wraps a task op map as a `run:` step, deriving the run prose
// from the op's primary verb value.
func taskToRunStep(op *yaml.Node) *yaml.Node {
	prose := opPrimaryKey(op)
	if prose == "" {
		prose = "run"
	}
	step := &yaml.Node{Kind: yaml.MappingNode}
	step.Content = append(step.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "run"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: prose},
	)
	// download: extract filter `include:` → `extract_include:` (the `include:`
	// key is now the plan-composition Step.Include — a pre-cutover include: is
	// always the download filter).
	renameMappingKey(op, "include", "extract_include")
	// Inline the op's own keys after the keyword.
	step.Content = append(step.Content, op.Content...)
	return step
}

// flattenScenario flattens one scenario group into a flat list of plan steps:
// setup: → run: front, step: classified, teardown: → run: end. Drops
// name:/example:/on_fail:; stamps the scenario pod: onto each step; classifies
// each step's keyword.
func flattenScenario(scen *yaml.Node) []*yaml.Node {
	if scen == nil || scen.Kind != yaml.MappingNode {
		return nil
	}
	pod := ""
	if p := findMappingValue(scen, "pod"); p != nil && p.Kind == yaml.ScalarNode {
		pod = p.Value
	}
	var out []*yaml.Node
	for _, group := range []struct {
		key          string
		forceRun     bool
		defaultCheck bool
	}{
		{"setup", true, false},
		{"step", false, false},
		{"teardown", true, false},
	} {
		steps := findMappingValue(scen, group.key)
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		for _, step := range steps.Content {
			if step.Kind != yaml.MappingNode {
				continue
			}
			out = append(out, classifyStep(step, pod, group.forceRun))
		}
	}
	return out
}

// classifyStep rewrites one scenario step into a plan step with a single intent
// keyword. forceRun marks a setup/teardown step (provisioning position).
func classifyStep(step *yaml.Node, pod string, forceRun bool) *yaml.Node {
	// Capture + strip the legacy Gherkin keyword (prose) and do:/scope:.
	prose := ""
	for _, kw := range []string{"given", "when", "then", "and", "but"} {
		if v := findMappingValue(step, kw); v != nil && v.Kind == yaml.ScalarNode {
			if prose == "" {
				prose = v.Value
			}
			removeMappingKey(step, kw)
		}
	}
	doMode := ""
	if v := findMappingValue(step, "do"); v != nil && v.Kind == yaml.ScalarNode {
		doMode = v.Value
		removeMappingKey(step, "do")
	}
	// agent: verb → its value is the prose; drop the verb.
	isAgentVerb := false
	if v := findMappingValue(step, "agent"); v != nil && v.Kind == yaml.ScalarNode {
		isAgentVerb = true
		if prose == "" {
			prose = v.Value
		}
		removeMappingKey(step, "agent")
	}
	hasVerb := opPrimaryKey(step) != ""

	keyword := classifyKeyword(forceRun, hasVerb, isAgentVerb, doMode, step)
	if prose == "" {
		if pk := opPrimaryKey(step); pk != "" {
			prose = pk
		} else {
			prose = string(keyword)
		}
	}

	// scope: → context:.
	scopeToContext(step, "")
	// stamp the scenario pod onto check/agent-check steps.
	if pod != "" && (keyword == KwCheck || keyword == KwAgentCheck) && findMappingValue(step, "pod") == nil {
		step.Content = append(step.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "pod"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: pod})
	}

	// download: extract filter `include:` → `extract_include:` (frees `include:`
	// for the plan-composition Step.Include).
	renameMappingKey(step, "include", "extract_include")

	// Build the new step: keyword first, then the (cleaned) op fields.
	ns := &yaml.Node{Kind: yaml.MappingNode}
	ns.Content = append(ns.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: string(keyword)},
		&yaml.Node{Kind: yaml.ScalarNode, Value: prose},
	)
	ns.Content = append(ns.Content, step.Content...)
	return ns
}

// classifyKeyword applies the keyword classification table.
func classifyKeyword(forceRun, hasVerb, isAgentVerb bool, doMode string, step *yaml.Node) StepKeyword {
	if forceRun {
		if !hasVerb || isAgentVerb {
			return KwAgentRun
		}
		return KwRun
	}
	if !hasVerb || isAgentVerb {
		return KwAgentCheck
	}
	if doMode == "instruct" {
		return KwAgentCheck
	}
	if doMode == "act" {
		return KwRun
	}
	if doMode == "assert" {
		return KwCheck
	}
	// No explicit do: — consult the verb's catalog default. The act-verb set
	// (VerbCatalog DoAct keys) is INJECTED by charly core at startup
	// (SetActVerbs) — VerbCatalog is package-main, unreachable from this candy.
	if v := opPrimaryVerb(step); v != "" {
		if actVerbsSet[v] {
			return KwRun
		}
	}
	return KwCheck
}

// opPrimaryVerb returns the verb discriminator key present on a step map.
func opPrimaryVerb(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for _, verb := range opVerbKeys {
		if v := findMappingValue(node, verb); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
			return verb
		}
	}
	return ""
}

// prependPlanSteps prepends steps to the entity's plan: sequence.
func prependPlanSteps(m *yaml.Node, steps []*yaml.Node) {
	if len(steps) == 0 {
		return
	}
	plan := ensurePlanSeq(m)
	plan.Content = append(append([]*yaml.Node(nil), steps...), plan.Content...)
}

// appendPlanSteps appends steps to the entity's plan: sequence.
func appendPlanSteps(m *yaml.Node, steps []*yaml.Node) {
	if len(steps) == 0 {
		return
	}
	plan := ensurePlanSeq(m)
	plan.Content = append(plan.Content, steps...)
}

// ensurePlanSeq returns the entity's plan: sequence node, creating it if absent.
func ensurePlanSeq(m *yaml.Node) *yaml.Node {
	plan := findMappingValue(m, "plan")
	if plan == nil {
		key := &yaml.Node{Kind: yaml.ScalarNode, Value: "plan"}
		plan = &yaml.Node{Kind: yaml.SequenceNode}
		m.Content = append(m.Content, key, plan)
		return plan
	}
	if plan.Kind != yaml.SequenceNode {
		plan.Kind = yaml.SequenceNode
		plan.Tag = "!!seq"
		plan.Value = ""
		plan.Content = nil
	}
	return plan
}

// planUnifyHarness folds the root score:/recipe: blocks into kind:check beds
// carrying an iterate: block (the AI-loop orchestration). For each kind: score
// named <S>, it creates a `check:<S>` bed with an iterate: block (built from the
// score's orchestration fields, sandbox: = the score's pod/vm/host) and a plan:
// built from the score's recipe refs (each recipe's from: → deduped
// include: <kind>:<name> steps, its inline scenario: → flattened steps). The
// score:/recipe: maps are then deleted. A score whose name already exists as a
// check bed is skipped (the existing bed wins; no first-party collision exists).
func planUnifyHarness(root *yaml.Node) bool {
	scoreMap := findMappingValue(root, "score")
	recipeMap := findMappingValue(root, "recipe")
	if scoreMap == nil && recipeMap == nil {
		return false
	}

	changed := false
	if scoreMap != nil && scoreMap.Kind == yaml.MappingNode {
		checkMap := ensureCheckMap(root)
		for i := 0; i+1 < len(scoreMap.Content); i += 2 {
			name := scoreMap.Content[i].Value
			score := scoreMap.Content[i+1]
			if findMappingValue(checkMap, name) != nil {
				// A check bed already carries this name — never clobber it.
				continue
			}
			bed := &yaml.Node{Kind: yaml.MappingNode}
			carryScoreDescription(bed, score)
			attachIterate(bed, score)
			buildIteratePlan(bed, score, recipeMap)
			checkMap.Content = append(checkMap.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: name}, bed)
		}
		removeMappingKey(root, "score")
		changed = true
	}
	if recipeMap != nil {
		removeMappingKey(root, "recipe")
		changed = true
	}
	return changed
}

// ensureCheckMap returns (creating/normalizing if absent) the root `check:`
// mapping node — the kind:check bed registry the iterate beds land in.
func ensureCheckMap(root *yaml.Node) *yaml.Node {
	if m := findMappingValue(root, "check"); m != nil {
		if m.Kind != yaml.MappingNode {
			m.Kind = yaml.MappingNode
			m.Tag = "!!map"
			m.Value = ""
			m.Content = nil
		}
		return m
	}
	key := &yaml.Node{Kind: yaml.ScalarNode, Value: "check"}
	node := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content, key, node)
	return node
}

// carryScoreDescription stamps the score's description onto the bed as a scalar
// description: (feature, then narrative newline-joined; tag dropped) so the
// score's purpose survives the fold. No-op when the score has no description.
func carryScoreDescription(bed, score *yaml.Node) {
	desc := findMappingValue(score, "description")
	if desc == nil {
		return
	}
	val := ""
	switch desc.Kind {
	case yaml.MappingNode:
		feature, narrative := "", ""
		if f := findMappingValue(desc, "feature"); f != nil && f.Kind == yaml.ScalarNode {
			feature = f.Value
		}
		if n := findMappingValue(desc, "narrative"); n != nil && n.Kind == yaml.ScalarNode {
			narrative = n.Value
		}
		val = feature
		if narrative != "" {
			if val != "" {
				val += "\n"
			}
			val += narrative
		}
	case yaml.ScalarNode:
		val = desc.Value
	}
	if val == "" {
		return
	}
	bed.Content = append(bed.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "description"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val})
}

// attachIterate builds the iterate: block on a check bed from a score node.
func attachIterate(bed, score *yaml.Node) {
	iter := &yaml.Node{Kind: yaml.MappingNode}
	// sandbox: from the score's pod/vm/host discriminator.
	sandbox := ""
	for _, k := range []string{"pod", "vm"} {
		if v := findMappingValue(score, k); v != nil && v.Kind == yaml.ScalarNode && v.Value != "" {
			sandbox = v.Value
		}
	}
	if sandbox != "" {
		iter.Content = append(iter.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "sandbox"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: sandbox})
	}
	// Carry the verbatim orchestration fields the new schema keeps. The retired
	// `progressive` field is deliberately dropped (the single-phase iterate loop
	// never consulted it — IterateConfig carries no Progressive field), as is the
	// retired AI-artifact-validation flag (later removed with the in-proc live-verb
	// runtime; the drop-validate-ai-artifacts step strips any residual key).
	for _, k := range []string{"agent", "plateau_iteration", "prompt", "note", "env", "mcp_endpoint"} {
		if v := findMappingValue(score, k); v != nil {
			iter.Content = append(iter.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
		}
	}
	removeMappingKey(bed, "iterate")
	bed.Content = append(bed.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "iterate"}, iter)
}

// buildIteratePlan builds the bed's plan: from the score's recipe refs: each
// recipe's from: entries → include: <kind>:<name> steps (identical includes
// deduped), its inline scenario: → flattened steps.
func buildIteratePlan(bed, score, recipeMap *yaml.Node) {
	recipes := findMappingValue(score, "recipe")
	if recipes == nil || recipes.Kind != yaml.SequenceNode {
		return
	}
	var steps []*yaml.Node
	seenInclude := map[string]bool{}
	for _, r := range recipes.Content {
		if r.Kind != yaml.ScalarNode {
			continue
		}
		recipe := findMappingValue(recipeMap, r.Value)
		if recipe == nil || recipe.Kind != yaml.MappingNode {
			continue
		}
		if from := findMappingValue(recipe, "from"); from != nil && from.Kind == yaml.SequenceNode {
			for _, fe := range from.Content {
				if fe.Kind != yaml.MappingNode {
					continue
				}
				kind, name := "", ""
				if k := findMappingValue(fe, "kind"); k != nil {
					kind = k.Value
				}
				if n := findMappingValue(fe, "name"); n != nil {
					name = n.Value
				}
				if kind == "" || name == "" {
					continue
				}
				ref := kind + ":" + name
				if seenInclude[ref] {
					continue // dedup identical includes
				}
				seenInclude[ref] = true
				inc := &yaml.Node{Kind: yaml.MappingNode}
				inc.Content = append(inc.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "include"},
					&yaml.Node{Kind: yaml.ScalarNode, Value: ref})
				steps = append(steps, inc)
			}
		}
		if sc := findMappingValue(recipe, "scenario"); sc != nil && sc.Kind == yaml.SequenceNode {
			for _, scen := range sc.Content {
				steps = append(steps, flattenScenario(scen)...)
			}
		}
	}
	appendPlanSteps(bed, steps)
}
