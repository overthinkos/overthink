package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	fileplugin "github.com/overthinkos/overthink/charly/plugin/builtins/file"
	"github.com/overthinkos/overthink/charly/plugin/builtins/file/params"
)

// fileVerb is the BUILT-IN `file` plugin: it provides the `file` verb, an extracted
// STATE-PROVISION verb. It is DUAL-NATURED:
//
//   - CheckVerbProvider (do:assert) — RunVerb dispatches the stat probe IN-PROCESS via
//     the live *Runner (r.Exec), which cannot cross the wire. Authored as
//     `check: … / plugin: file / plugin_input: {file, exists, mode, owner, group_of,
//     filetype, contains, sha256}`, dispatched via runPluginVerb after the host
//     validates plugin_input against the served #FileInput.
//   - ProvisionActor (do:act) — RenderProvisionScript renders the RUNTIME file-creation
//     (mkdir/touch/cat-heredoc + chmod), distinct from the BUILD-time COPY directives the
//     write:/copy: verbs emit. Reached at install COMPILE+EMIT (a `run: {plugin: file}`
//     step → emitTasks' `case "plugin"` for the box/OCI build, renderOpCommand for the
//     local/vm deploy, both via resolveProvisionScript) AND at runtime act
//     (runProvisionAct). Mirrors unix_group/user/mount/kernel-param.
//
// The seven file-EXCLUSIVE fields (file/exists/owner/group_of/filetype/contains/sha256,
// read ONLY by the `file` verb) LEFT the closed #Op/spec.OpVerbs into #FileInput. `mode`
// is the SHARED companion — it STAYS in base #Op (the copy/write install verbs read
// Op.Mode at deploy) yet is reproduced in #FileInput, so the file verb reads its OWN
// plugin_input.mode (the migrator moves mode into a file step's plugin_input while
// leaving it on #Op for copy/write — exactly how gid is shared between unix_group and
// user). `content` stays a SHARED base #Op modifier (the write verb reads it too), so the
// act half reads it off the step Op.
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only Invoke
// stub (an in-proc verb never serves itself over the wire — Invoke errors loudly rather
// than silently dropping the *Runner).
type fileVerb struct{ builtinVerbBase }

func (fileVerb) Reserved() string { return "file" }

// RunVerb (the do:assert half) decodes the typed plugin_input (params.FileInput,
// generated from the unit's schema/file.cue) and runs the stat probe via the live
// *Runner; the impl stays in r.runFile (checkrun.go). gengotypes degrades the
// self-contained `contains` matcher disjunction to `any`, so it is re-decoded through
// decodeContainsList — the codec that defaults a BARE scalar to the `contains` operator
// (not `equals`), preserving the file verb's contains semantic that the base #Op load
// normalizer applied for this field before extraction.
func (fileVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.FileInput
	decodePluginInput(op.PluginInput, &in)
	return r.runFile(ctx, op, fileCheck{
		Path:     in.File,
		Exists:   in.Exists,
		Mode:     in.Mode,
		Owner:    in.Owner,
		GroupOf:  in.GroupOf,
		Filetype: in.Filetype,
		Contains: decodeContainsList(in.Contains),
		Sha256:   in.Sha256,
	})
}

// RenderProvisionScript (the do:act half) renders the idempotent RUNTIME file-creation:
// `mkdir -p $(dirname) && cat > FILE <<EOF … ` (with content) or `… && touch FILE`, then
// optionally `chmod MODE`. ok is always true — a file act always has a create form;
// distros are unused (the path is distro-agnostic). `file`/`mode` come from plugin_input
// (the file-exclusive + shared-companion fields that moved); `content` is read off the
// step Op (a SHARED base #Op modifier the write verb also reads, so it never moved).
func (fileVerb) RenderProvisionScript(op *Op, _ []string) (string, bool) {
	var in params.FileInput
	decodePluginInput(op.PluginInput, &in)
	path := shellSingleQuote(in.File)
	var b strings.Builder
	if op.Content != "" {
		// Heredoc with a collision-resistant marker; content is verbatim.
		fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && cat > %s <<'CHARLY_ACT_EOF'\n%s\nCHARLY_ACT_EOF", path, path, op.Content)
	} else {
		fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && touch %s", path, path)
	}
	if in.Mode != "" {
		fmt.Fprintf(&b, " && chmod %s %s", shellSingleQuote(in.Mode), path)
	}
	return b.String(), true
}

// fileCheck carries r.runFile's decoded plugin_input — the package-main analogue of
// the http candy's httpCheck, keeping the file params import out of checkrun.go.
type fileCheck struct {
	Path     string
	Exists   *bool
	Mode     string
	Owner    string
	GroupOf  string
	Filetype string
	Contains MatcherList
	Sha256   string
}

// decodeContainsList re-decodes a gengotypes-degraded `contains` value (`any`) into a
// MatcherList with the goss contains-default: a BARE scalar element defaults to the
// `contains` operator (substring match), while a single-operator map ({equals: …},
// {matches: …}, {not_contains: …}, …) keeps its explicit operator. This mirrors the base
// #Op load normalizer that applied the same default to the file verb's `contains:` field
// before extraction, so a migrated `plugin_input.contains` (the
// authored bare-scalar / list / operator-map shorthand) keeps meaning "contents CONTAIN
// X" — never the equals-default decodeMatcherList would impose. A nil value yields nil.
func decodeContainsList(v any) MatcherList {
	if v == nil {
		return nil
	}
	// A scalar or single operator-map is a one-element list (the contains-default shape).
	elems, ok := v.([]any)
	if !ok {
		elems = []any{v}
	}
	var ml MatcherList
	for _, e := range elems {
		if m, isMap := e.(map[string]any); isMap && len(m) == 1 {
			// An explicit single-operator map → decode through the shared Matcher codec.
			raw, err := json.Marshal(m)
			if err != nil {
				continue
			}
			var matcher Matcher
			if err := matcher.UnmarshalJSON(raw); err != nil {
				continue
			}
			ml = append(ml, matcher)
			continue
		}
		// A bare scalar (or any non-single-operator value) → the contains default.
		ml = append(ml, Matcher{Op: "contains", Value: e})
	}
	return ml
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{fileVerb{}},
		Schema:    PluginSchema{CueSource: fileplugin.Schema(), InputDefs: fileplugin.InputDefs},
	})
}
