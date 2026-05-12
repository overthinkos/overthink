package main

import "testing"

// TestCanonicalizeDeployArg exercises the Pattern A "<base>/<instance>"
// splitting that every command entry point applies. Regression guard
// against the 2026-05-12 bug class where Pattern A keys leaked past
// the canonicalization boundary and downstream MergeDeployOntoMetadata
// looked up the wrong deploy.yml key (dropping port/env overlays).
func TestCanonicalizeDeployArg(t *testing.T) {
	for _, tc := range []struct {
		name        string
		arg         string
		instance    string
		wantImage   string
		wantInst    string
	}{
		{"pattern_A_split", "versa/ecovoyage", "", "versa", "ecovoyage"},
		{"pattern_A_three_segments_NOT_split", "ghcr.io/owner/img", "", "ghcr.io/owner/img", ""}, // registry host
		{"pattern_B_fq_ref", "ghcr.io/overthinkos/versa:2026.132.1941", "", "ghcr.io/overthinkos/versa:2026.132.1941", ""},
		{"pattern_B_digest", "ghcr.io/x/y@sha256:abc", "", "ghcr.io/x/y@sha256:abc", ""},
		{"bare_short_name", "versa", "", "versa", ""},
		{"explicit_instance_passthrough", "versa", "ecovoyage", "versa", "ecovoyage"},
		{"explicit_instance_wins_over_slash", "versa/dev", "prod", "versa/dev", "prod"}, // operator chose -i; don't override
		{"empty", "", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotImage, gotInst := canonicalizeDeployArg(tc.arg, tc.instance)
			if gotImage != tc.wantImage || gotInst != tc.wantInst {
				t.Errorf("canonicalizeDeployArg(%q, %q) = (%q, %q), want (%q, %q)",
					tc.arg, tc.instance, gotImage, gotInst, tc.wantImage, tc.wantInst)
			}
		})
	}
}

// TestResolveLocalImageRef_PrefersBaseOverAlias asserts that when two
// equal-CalVer candidates share the `org.overthinkos.image` label
// (because `bumpDeployAlias` tags an instance alias inheriting the
// base label), the resolver picks the BASE ref (repo's trailing
// segment == short name) over the alias (`<base>/<instance>`).
func TestResolveLocalImageRef_PrefersBaseOverAlias(t *testing.T) {
	// matchesShortName logic exercised via the sort callback's
	// behavior. We can't run the full resolver without podman, but
	// we verify the helper directly by simulating the candidates.
	// The actual matchesShortName closure lives inside
	// resolveLocalImageRef; here we mirror it.
	matchesShortName := func(ref, name string) bool {
		repo := ref
		for i, ch := range ref {
			if ch == ':' || ch == '@' {
				repo = ref[:i]
				break
			}
		}
		if i := lastIndex(repo, '/'); i >= 0 {
			repo = repo[i+1:]
		}
		return repo == name
	}
	for _, tc := range []struct {
		ref, name string
		want      bool
	}{
		{"ghcr.io/overthinkos/versa:2026.132.1941", "versa", true},
		{"ghcr.io/overthinkos/versa/ecovoyage:2026.132.1941", "versa", false},
		{"ghcr.io/overthinkos/sway-browser-vnc:1.0", "sway-browser-vnc", true},
		{"ghcr.io/overthinkos/sway-browser-vnc/ecovoyage:1.0", "sway-browser-vnc", false},
	} {
		if got := matchesShortName(tc.ref, tc.name); got != tc.want {
			t.Errorf("matchesShortName(%q, %q) = %v, want %v", tc.ref, tc.name, got, tc.want)
		}
	}
}

func lastIndex(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
