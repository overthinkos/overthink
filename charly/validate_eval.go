package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lowercaseEvalVarPattern matches a ${name} token whose identifier begins with a
// lowercase letter. The eval-var expander (testVarRefPattern in evalspec.go) only
// recognizes UPPERCASE names, so such a token is never substituted — it reaches
// the verb literally. Used by validateCheck to reject it in pure-identifier eval
// fields (k8s/resource modifiers), catching the k3s-server `${deploy_name}` class.
var lowercaseEvalVarPattern = regexp.MustCompile(`\$\{[a-z][a-zA-Z0-9_]*\}`)

// validateTests checks every declarative test spec in the project for
// authoring errors before build time. Hooked into charly box validate.
//
// Scope:
//   - Layer-level tests (LayerYAML.Eval).
//   - Image-level tests (ImageConfig.Eval, ImageConfig.DeployEval).
//   - Deploy.yml tests are out of scope here because deploy.yml is loaded
//     per-operator at runtime, not part of the charly.yml validation pass.
//
// Validation rules:
//  1. Exactly one verb discriminator is set per Check (enforced via Kind()).
//  2. Numeric port in range 1..65535 when the port verb is used.
//  3. Timeout parses as time.Duration when present.
//  4. All ${NAME[:arg]} references are legitimate: scope:"build" checks
//     may not reference runtime-only variables.
//  5. ID uniqueness within a single section (layer/image/deploy) so
//     deploy.yml overrides are unambiguous.
//  6. ExitStatus / Port / UID / GID sanity (non-negative).
//  7. Matcher operator is a known one.
func validateTests(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Layer-level. Each Check may opt into deploy scope; default is build.
	for name, layer := range layers {
		for i := range layer.tests {
			scope := layer.tests[i].Scope
			if scope == "" {
				scope = "build"
			}
			validateCheck(&layer.tests[i], fmt.Sprintf("layer %q tests[%d]", name, i), scope, errs)
		}
	}

	// Image-level
	for name, img := range cfg.Image {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}
		for i := range img.Eval {
			scope := img.Eval[i].Scope
			if scope == "" {
				scope = "build"
			}
			validateCheck(&img.Eval[i], fmt.Sprintf("image %q tests[%d]", name, i), scope, errs)
		}
		for i := range img.DeployEval {
			// DeployTests always carry implicit scope:"deploy".
			validateCheck(&img.DeployEval[i], fmt.Sprintf("image %q deploy_tests[%d]", name, i), "deploy", errs)
		}

		// ID uniqueness: collect IDs seen per effective section.
		validateTestIDUniqueness(img, name, errs)

		// Layer-contributed checks per image also need section-unique IDs. We
		// emulate CollectEval bucketing to catch collisions across layer +
		// image sources.
		validateCollectedIDUniqueness(cfg, layers, name, errs)
	}
}

// validateCheck runs per-check rules. effectiveScope is "build" or "deploy"
// — for candy manifest tests: the Check's own Scope field (defaulting to
// build); for charly.yml deploy_tests: always "deploy".
func validateCheck(c *Check, loc, effectiveScope string, errs *ValidationError) {
	verb, err := c.Kind()
	if err != nil {
		errs.Add("%s: %v", loc, err)
		return
	}
	if c.Port < 0 || c.Port > 65535 {
		errs.Add("%s: port %d out of range (1-65535)", loc, c.Port)
	}
	if verb == "port" && c.Port == 0 {
		errs.Add("%s: port verb requires a non-zero port number", loc)
	}
	if c.Timeout != "" {
		if _, err := time.ParseDuration(c.Timeout); err != nil {
			errs.Add("%s: timeout %q: %v", loc, c.Timeout, err)
		}
	}
	if c.Scope != "" && c.Scope != "build" && c.Scope != "deploy" {
		errs.Add("%s: scope %q must be \"build\" or \"deploy\"", loc, c.Scope)
	}
	if c.UID != nil && *c.UID < 0 {
		errs.Add("%s: uid %d must be non-negative", loc, *c.UID)
	}
	if c.GID != nil && *c.GID < 0 {
		errs.Add("%s: gid %d must be non-negative", loc, *c.GID)
	}

	// Runtime-only variable references — only allowed in deploy-scope checks.
	if effectiveScope == "build" {
		refs := collectCheckRefs(c)
		for _, r := range refs {
			if IsRuntimeOnlyVar(r) {
				errs.Add("%s: references runtime-only variable ${%s} but scope is build — mark as scope: deploy or use scope:deploy-only attributes", loc, r)
			}
		}
	}

	// Lowercase ${...} in a pure-identifier eval field is NEVER an eval variable:
	// the eval-var expander (testVarRefPattern) only recognizes UPPERCASE names,
	// so a lowercase token is silently passed through literally and reaches the
	// verb as the string "${...}" (the k3s-server `cluster: "${deploy_name}"`
	// class of bug — it passed both validate and runtime, resolving to no cluster).
	// Scoped to the k8s/resource-identity modifiers, which are CLI-arg identifiers
	// passed to `charly eval k8s` — never a shell body (cmd:) or JS (cdp expression:),
	// where a lowercase ${var} is legitimate. Did you mean an UPPERCASE eval var
	// (e.g. ${DEPLOY_NAME})?
	for _, f := range []struct{ label, val string }{
		{"cluster", c.Cluster}, {"name", c.Name}, {"namespace", c.Namespace},
		{"label", c.Label}, {"kubeconfig", c.Kubeconfig}, {"k8s_context", c.K8sContext},
		{"k8s_resource", c.K8sResource}, {"k8s_group", c.K8sGroup},
		{"k8s_version", c.K8sVersion}, {"manifest", c.Manifest},
	} {
		if m := lowercaseEvalVarPattern.FindString(f.val); m != "" {
			errs.Add("%s: %s contains %s — eval variables are UPPERCASE (e.g. ${DEPLOY_NAME}); a lowercase ${...} never resolves and is passed through literally", loc, f.label, m)
		}
	}

	// Matcher operator sanity check.
	for _, name := range []string{"contains", "stdout", "stderr", "body", "headers", "opts"} {
		_ = name // placeholder — full iteration below
	}
	validateMatchers(c.Contains, loc+" contains", errs)
	validateMatchers(c.Stdout, loc+" stdout", errs)
	validateMatchers(c.Stderr, loc+" stderr", errs)
	validateMatchers(c.Body, loc+" body", errs)
	validateMatchers(c.Headers, loc+" headers", errs)
	validateMatchers(c.Opts, loc+" opts", errs)
	validateMatchers(c.Value, loc+" value", errs)

	// cdp/wl/dbus/vnc verbs: validate method allowlist + required modifiers
	// + scope enforcement (all four are deploy-scope-only since they need a
	// running container with port mappings). The allowlists live in
	// testrun_ov_verbs.go next to the dispatch logic so adding a new method
	// means touching one file.
	validateCharlyVerb(c, verb, loc, effectiveScope, errs)
}

// validateCharlyVerb checks method-name allowlists, required modifiers, and
// deploy-scope enforcement for the cdp/wl/dbus/vnc/mcp verbs. No-op for
// other verbs.
func validateCharlyVerb(c *Check, verb, loc, effectiveScope string, errs *ValidationError) {
	var (
		method    string
		allowlist map[string]methodSpec
	)
	switch verb {
	case "cdp":
		method, allowlist = c.Cdp, cdpMethods
	case "wl":
		method, allowlist = c.Wl, wlMethods
	case "dbus":
		method, allowlist = c.Dbus, dbusMethods
	case "vnc":
		method, allowlist = c.Vnc, vncMethods
	case "mcp":
		method, allowlist = c.Mcp, mcpMethods
	case "record":
		method, allowlist = c.Record, recordMethods
	case "spice":
		method, allowlist = c.Spice, spiceMethods
	case "libvirt":
		method, allowlist = c.Libvirt, libvirtMethods
	case "adb":
		method, allowlist = c.Adb, adbMethods
	case "appium":
		method, allowlist = c.Appium, appiumMethods
	default:
		return
	}

	spec, ok := allowlist[method]
	if !ok {
		methods := make([]string, 0, len(allowlist))
		for k := range allowlist {
			methods = append(methods, k)
		}
		sort.Strings(methods)
		errs.Add("%s: %s: unknown method %q (allowed: %s)", loc, verb, method, strings.Join(methods, ", "))
		return
	}

	// Deploy-scope only — these verbs need a running container with port
	// mappings; build-scope (charly eval box) correctly skips them at runtime.
	if effectiveScope == "build" {
		errs.Add("%s: %s: verb requires scope:\"deploy\" (needs a running container with port mappings)", loc, verb)
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
func collectCheckRefs(c *Check) []string {
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

// validateMatchers checks each Matcher in the list for a supported operator.
// Uses a stable small allowlist; verbs that need numeric operators will be
// added as Phase 7 lands those verbs.
var validMatcherOps = map[string]bool{
	"equals": true, "not_equals": true,
	"contains": true, "not_contains": true,
	"matches": true, "not_matches": true,
	"lt": true, "le": true, "gt": true, "ge": true,
}

// Takes []Matcher rather than MatcherList so callers can pass any named slice
// type whose underlying element is Matcher (MatcherList, ContainsList) without
// an explicit conversion at every call site.
func validateMatchers(ml []Matcher, loc string, errs *ValidationError) {
	for i, m := range ml {
		if m.Op == "" {
			continue // UnmarshalYAML guarantees Op is set; defensive only.
		}
		if !validMatcherOps[m.Op] {
			errs.Add("%s[%d]: unsupported matcher op %q (allowed: %s)", loc, i, m.Op, strings.Join(validMatcherOpList(), ", "))
		}
	}
}

func validMatcherOpList() []string {
	out := make([]string, 0, len(validMatcherOps))
	for k := range validMatcherOps {
		out = append(out, k)
	}
	return out
}

// validateTestIDUniqueness ensures IDs don't collide within Tests, within
// DeployTests, or between Tests and DeployTests of the same image.
func validateTestIDUniqueness(img BoxConfig, imgName string, errs *ValidationError) {
	seen := map[string]string{} // id → first location
	for i, c := range img.Eval {
		if c.ID == "" {
			continue
		}
		section := "tests"
		loc := fmt.Sprintf("image %q %s[%d]", imgName, section, i)
		if prev, dup := seen[c.ID]; dup {
			errs.Add("%s: duplicate id %q (previously defined at %s)", loc, c.ID, prev)
		} else {
			seen[c.ID] = loc
		}
	}
	for i, c := range img.DeployEval {
		if c.ID == "" {
			continue
		}
		loc := fmt.Sprintf("image %q deploy_tests[%d]", imgName, i)
		if prev, dup := seen[c.ID]; dup {
			errs.Add("%s: duplicate id %q (previously defined at %s)", loc, c.ID, prev)
		} else {
			seen[c.ID] = loc
		}
	}
}

// validateCollectedIDUniqueness runs CollectEval to see the post-merge
// per-section ID layout and flags cross-layer collisions.
func validateCollectedIDUniqueness(cfg *Config, layers map[string]*Layer, imgName string, errs *ValidationError) {
	set := CollectEval(cfg, layers, imgName)
	if set == nil {
		return
	}
	checkSection := func(sectionName string, list []Check) {
		seen := map[string]string{}
		for _, c := range list {
			if c.ID == "" {
				continue
			}
			loc := fmt.Sprintf("%s (from %s)", sectionName, c.Origin)
			if prev, dup := seen[c.ID]; dup {
				errs.Add("image %q: duplicate id %q in %s section — %s collides with %s", imgName, c.ID, sectionName, loc, prev)
			} else {
				seen[c.ID] = loc
			}
		}
	}
	checkSection("layer", set.Layer)
	checkSection("image", set.Image)
	checkSection("deploy", set.Deploy)
}
