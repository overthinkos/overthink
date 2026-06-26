package main

import "testing"

// TestDeployShellQuote_CanonicalPOSIX asserts deployShellQuote emits the canonical POSIX
// single-quoted form for a corpus of adversarial inputs (shell metachars, embedded quotes,
// command-substitution, newlines). It is the equivalence + regression guard for FU-13's
// consolidation of the former byte-builder deployShellQuote onto kit.ShellQuote: this corpus
// passes against the byte-builder AND against the kit.ShellQuote alias, proving the two POSIX
// single-quoters are behaviorally identical (inside '...' the ONLY special char is ', which both
// escape as '\”; every other char — $, `, ;, ", \n — is literal). Security-sensitive: these are
// the quoters for SSH/podman/dbus command construction.
func TestDeployShellQuote_CanonicalPOSIX(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"abc", "'abc'"},
		{"a'b", `'a'\''b'`},
		{"'", `''\'''`},
		{"a'b'c", `'a'\''b'\''c'`},
		{"$(rm -rf /)", "'$(rm -rf /)'"},
		{"`whoami`", "'`whoami`'"},
		{"a b; c && d", "'a b; c && d'"},
		{`a"b`, `'a"b'`},
		{"a\nb", "'a\nb'"},
	}
	for _, c := range cases {
		if got := deployShellQuote(c.in); got != c.want {
			t.Errorf("deployShellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
