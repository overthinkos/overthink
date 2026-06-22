package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// lowercaseCheckVarPattern matches a ${name} token whose identifier begins with a
// lowercase letter. The check-var expander (testVarRefPattern in checkspec.go) only
// recognizes UPPERCASE names, so such a token is never substituted — it reaches
// the verb literally. Used by validateCheck to reject it in pure-identifier check
// fields (k8s/resource modifiers), catching the k3s-server `${deploy_name}` class.
var lowercaseCheckVarPattern = regexp.MustCompile(`\$\{[a-z][a-zA-Z0-9_]*\}`)

// validateTests checks every declarative test spec in the project for
// authoring errors before build time. Hooked into charly box validate.
//
// Scope:
//   - Candy-level tests (CandyYAML.Check).
//   - Box-level tests (BoxConfig.Check, BoxConfig.DeployCheck).
//   - Per-host charly.yml tests are out of scope here because that overlay is
//     loaded per-operator at runtime, not part of the project charly.yml
//     validation pass.
//
// Validation rules:
//  1. Exactly one verb discriminator is set per Check (enforced via Kind()).
//  2. Numeric port in range 1..65535 when the port verb is used.
//  3. Timeout parses as time.Duration when present.
//  4. All ${NAME[:arg]} references are legitimate: scope:"build" checks
//     may not reference runtime-only variables.
//  5. ID uniqueness within a single section (candy/box/deploy) so
//     charly.yml overrides are unambiguous.
//  6. ExitStatus / Port / UID / GID sanity (non-negative).
//  7. Matcher operator is a known one.
func validateOps(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate every Op embedded in a plan step — candy + box. Agent
	// (agent-run:/agent-check:) and include: steps carry no Op verb, so they
	// are skipped.
	validatePlanOps := func(plan []Step, who string) {
		for i := range plan {
			op := &plan[i].Op
			if len(op.VerbsSet()) == 0 {
				continue // agent / include / verb-less step
			}
			// Stamp the step keyword's do-mode onto the op so validateCheck's
			// act-form rules (e.g. a run: step whose verb has no build/deploy
			// install path) are keyword-aware — mirroring runUnit's stamping.
			op.IntentDo = string(stepDoMode(&plan[i]))
			validateCheck(op, fmt.Sprintf("%s step[%d]", who, i), errs)
		}
	}
	for name, layer := range layers {
		validatePlanOps(layer.plan, fmt.Sprintf("candy %q", name))
	}
	for name, img := range cfg.Box {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}
		validatePlanOps(img.Plan, fmt.Sprintf("box %q", name))
	}
}

// validateCheck runs per-check rules. effectiveScope is "build" or "deploy"
// — for candy manifest tests: the Check's own Scope field (defaulting to
// build); for charly.yml deploy_tests: always "deploy".
func validateCheck(c *Op, loc string, errs *ValidationError) {
	verb, err := c.Kind()
	if err != nil {
		errs.Add("%s: %v", loc, err)
		return
	}
	// port range (>0, <=65535), timeout (#Duration), and the context enum are enforced
	// by the closed #Op. The former "port verb needs a non-zero port" rule was removed
	// as structurally unreachable: VerbsSet (spec/charly_methods.go) classifies an op as
	// the `port` verb ONLY when c.Port != 0, so verb=="port" already implies Port!=0.
	if spec, ok := VerbCatalog[verb]; ok {
		for _, ctx := range opEffectiveContexts(c) {
			if !spec.HasContext(ctx) {
				errs.Add("%s: verb %q is not legal in context %q", loc, verb, ctx)
			}
		}
	}

	// A do:act op must have a real install path in each build/deploy context
	// it claims — otherwise the compiler would silently drop it. Runtime-only
	// act verbs (file/user/group/kernel-param/mount/http/dbus/cdp/…) act via
	// the check Runner's executor; in build/deploy the install verbs + command
	// + package/service are the act surface (file creation = write/copy).
	if opEffectiveDo(c) == DoAct && !ActsInBuildDeploy(verb) {
		for _, ctx := range opEffectiveContexts(c) {
			if ctx == CtxBuild || ctx == CtxDeploy {
				errs.Add("%s: verb %q cannot act (do: act) in %s context — its act form is runtime-only (use context: [runtime]); create files in build/deploy with the write/copy verbs", loc, verb, ctx)
			}
		}
	}
	// uid/gid non-negativity is enforced by #Op.

	// Runtime-only variable references — illegal in a build-legal op.
	if opInContext(c, CtxBuild) {
		refs := collectCheckRefs(c)
		for _, r := range refs {
			if IsRuntimeOnlyVar(r) {
				errs.Add("%s: references runtime-only variable ${%s} but scope is build — mark as scope: deploy or use scope:deploy-only attributes", loc, r)
			}
		}
	}

	// Lowercase ${...} in a pure-identifier check field is NEVER an check variable:
	// the check-var expander (testVarRefPattern) only recognizes UPPERCASE names,
	// so a lowercase token is silently passed through literally and reaches the
	// verb as the string "${...}" (the k3s-server `cluster: "${deploy_name}"`
	// class of bug — it passed both validate and runtime, resolving to no cluster).
	// Scoped to the kube/resource-identity modifiers, which are CLI-arg identifiers
	// passed to `charly check kube` — never a shell body (cmd:) or JS (cdp expression:),
	// where a lowercase ${var} is legitimate. Did you mean an UPPERCASE check var
	// (e.g. ${DEPLOY_NAME})?
	for _, f := range []struct{ label, val string }{
		{"cluster", c.Cluster}, {"name", c.Name}, {"namespace", c.Namespace},
		{"label", c.Label}, {"kubeconfig", c.Kubeconfig}, {"kube_context", c.KubeContext},
		{"kube_resource", c.KubeResource}, {"kube_group", c.KubeGroup},
		{"kube_version", c.KubeVersion}, {"manifest", c.Manifest},
	} {
		if m := lowercaseCheckVarPattern.FindString(f.val); m != "" {
			errs.Add("%s: %s contains %s — check variables are UPPERCASE (e.g. ${DEPLOY_NAME}); a lowercase ${...} never resolves and is passed through literally", loc, f.label, m)
		}
	}

	// Matcher operator names (equals/contains/matches/…) are enforced by #MatchOpMap.

	// cdp/wl/dbus/vnc verbs: validate method allowlist + required modifiers
	// + scope enforcement (all four are deploy-scope-only since they need a
	// running container with port mappings). The allowlists live in
	// checkrun_charly_verbs.go next to the dispatch logic so adding a new method
	// means touching one file.
	validateCharlyVerb(c, verb, loc, errs)
}

// validateCharlyVerb checks method-name allowlists, required modifiers, and
// deploy-scope enforcement for the cdp/wl/dbus/vnc/mcp verbs. No-op for
// other verbs.
func validateCharlyVerb(c *Op, verb, loc string, errs *ValidationError) {
	// E4: the verb's method contract is owned by its provider (LiveVerbProvider),
	// reached through the registry — the former central per-verb switch is gone. A
	// goss verb (file/port/…) resolves but is NOT a LiveVerbProvider (it has no
	// method contract), so there is nothing to check. Every live verb in the
	// registry is now covered uniformly — INCLUDING kube, whose required-modifier
	// specs (apply→Manifest, wait-ready→{Kind,Name}, …) the old 10-case switch
	// silently omitted.
	prov, ok := providerRegistry.ResolveVerb(verb)
	if !ok {
		return
	}
	lv, ok := prov.(LiveVerbProvider)
	if !ok {
		return
	}
	method := lv.MethodField(c)

	// The method-name allowlist is enforced by the per-verb #*Method CUE enums;
	// an unknown method is rejected there. Here we only need the spec to drive
	// the cross-field required-modifier / artifact checks below.
	spec, ok := lv.Methods()[method]
	if !ok {
		return
	}

	// Live-container verbs need a running target — they are runtime-context
	// only; reject in build context (charly check correctly skips them there).
	if opInContext(c, CtxBuild) {
		errs.Add("%s: %s: verb is runtime-context only (needs a running container); not legal in build context", loc, verb)
	}

	for _, f := range spec.required {
		if isZeroField(c, f) {
			errs.Add("%s: %s: %s requires modifier %q", loc, verb, method, strings.ToLower(f))
		}
	}

	if spec.artifact && c.Artifact == "" {
		errs.Add("%s: %s: %s is an artifact-producing method; set artifact: <path>", loc, verb, method)
	}

	if c.ArtifactMinDimensions != "" {
		if !spec.artifact {
			errs.Add("%s: %s: %s is not an artifact-producing method; artifact_min_dimensions is not applicable", loc, verb, method)
		} else if !validWxH(c.ArtifactMinDimensions) {
			errs.Add("%s: %s: %s: artifact_min_dimensions must be %q form with positive ints, got %q", loc, verb, method, "WxH", c.ArtifactMinDimensions)
		}
	}
	if c.ArtifactNotUniform && !spec.artifact {
		errs.Add("%s: %s: %s is not an artifact-producing method; artifact_not_uniform is not applicable", loc, verb, method)
	}
	if c.ArtifactMinCastEvents > 0 && !spec.artifact {
		errs.Add("%s: %s: %s is not an artifact-producing method; artifact_min_cast_events is not applicable", loc, verb, method)
	}

	// {element} substitution requires a selector to resolve the element id
	// host-side (appium execute/raw). A literal {element} with no selector would
	// reach the server unresolved — catch it at config time.
	if (strings.Contains(c.RequestBody, "{element}") || strings.Contains(c.Path, "{element}")) && c.Selector == "" {
		errs.Add("%s: %s: %s references the {element} token but no selector is set to resolve it", loc, verb, method)
	}
}

// validWxH reports whether s parses as "<int>x<int>" with both ints > 0.
// Used by validators of artifact_min_dimensions / artifact_dimensions style fields.
func validWxH(s string) bool {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return false
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	return err1 == nil && err2 == nil && w > 0 && h > 0
}

// collectCheckRefs returns every ${NAME[:arg]} key referenced across every
// string-typed field of the Check.
func collectCheckRefs(c *Op) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range c.StringFields() {
		for _, r := range TestVarRefs(*p) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	return out
}
