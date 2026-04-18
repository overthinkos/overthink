package main

import (
	"fmt"
	"strings"
	"time"
)

// validateTests checks every declarative test spec in the project for
// authoring errors before build time. Hooked into ov image validate.
//
// Scope:
//   - Layer-level tests (LayerYAML.Tests).
//   - Image-level tests (ImageConfig.Tests, ImageConfig.DeployTests).
//   - Deploy.yml tests are out of scope here because deploy.yml is loaded
//     per-operator at runtime, not part of the image.yml validation pass.
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
	for name, img := range cfg.Images {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}
		for i := range img.Tests {
			scope := img.Tests[i].Scope
			if scope == "" {
				scope = "build"
			}
			validateCheck(&img.Tests[i], fmt.Sprintf("image %q tests[%d]", name, i), scope, errs)
		}
		for i := range img.DeployTests {
			// DeployTests always carry implicit scope:"deploy".
			validateCheck(&img.DeployTests[i], fmt.Sprintf("image %q deploy_tests[%d]", name, i), "deploy", errs)
		}

		// ID uniqueness: collect IDs seen per effective section.
		validateTestIDUniqueness(img, name, errs)

		// Layer-contributed checks per image also need section-unique IDs. We
		// emulate CollectTests bucketing to catch collisions across layer +
		// image sources.
		validateCollectedIDUniqueness(cfg, layers, name, errs)
	}
}

// validateCheck runs per-check rules. effectiveScope is "build" or "deploy"
// — for layer.yml tests: the Check's own Scope field (defaulting to
// build); for image.yml deploy_tests: always "deploy".
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

func validateMatchers(ml MatcherList, loc string, errs *ValidationError) {
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
func validateTestIDUniqueness(img ImageConfig, imgName string, errs *ValidationError) {
	seen := map[string]string{} // id → first location
	for i, c := range img.Tests {
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
	for i, c := range img.DeployTests {
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

// validateCollectedIDUniqueness runs CollectTests to see the post-merge
// per-section ID layout and flags cross-layer collisions.
func validateCollectedIDUniqueness(cfg *Config, layers map[string]*Layer, imgName string, errs *ValidationError) {
	set := CollectTests(cfg, layers, imgName)
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
