package main

import (
	"context"
	"encoding/json"

	httpplugin "github.com/overthinkos/overthink/charly/plugin/builtins/http"
	"github.com/overthinkos/overthink/charly/plugin/builtins/http/params"
)

// httpVerb is the BUILT-IN `http` plugin: it provides the `http` check verb (host-side
// request under live mode, in-container `curl` under box mode) as a CheckVerbProvider,
// so runPluginVerb dispatches it IN-PROCESS via RunVerb — keeping the live *Runner
// (r.HTTPClient / r.Mode / r.Exec) the request needs and that cannot cross the wire. The
// verb left the closed #Op/spec.OpVerbs and is now authored as `plugin: http` +
// `plugin_input: {http, status, body, header, allow_insecure, no_follow_redirects,
// ca_file}`, dispatched through the provider registry, after the host has validated its
// plugin_input against the unit's served schema (charly/plugin/builtins/http). Mirrors
// the process/port/dns extraction; the impl stays in r.runHTTP.
//
// The http-EXCLUSIVE fields ride plugin_input; the SHARED modifiers `method`/
// `request_body` (also read by cdp/dbus/libvirt) and the GENERAL `timeout` stay base
// #Op fields, so r.runHTTP reads them off the step Op directly (see httpCheck).
//
// It embeds builtinVerbBase, which supplies Class()=ClassVerb and the in-proc-only
// Invoke stub (an in-proc verb never serves itself over the wire — Invoke errors
// loudly rather than silently dropping the *Runner).
type httpVerb struct{ builtinVerbBase }

func (httpVerb) Reserved() string { return "http" }

// RunVerb decodes the typed plugin_input (params.HttpInput, generated from the unit's
// schema/http.cue) and runs the request via the live *Runner. gengotypes degrades the
// self-contained body/header matcher disjunction to `any`, so each is re-decoded through
// the SHARED matcher codec (MatcherList.UnmarshalJSON — R3) into the typed []Matcher the
// runner consumes (mirrors plugin_matching.go).
func (httpVerb) RunVerb(ctx context.Context, r *Runner, op *Op) CheckResult {
	var in params.HttpInput
	decodePluginInput(op.PluginInput, &in)
	return r.runHTTP(ctx, op, httpCheck{
		URL:           in.HTTP,
		Status:        in.Status,
		Body:          decodeMatcherList(in.Body),
		Headers:       decodeMatcherList(in.Headers),
		AllowInsecure: in.AllowInsecure,
		NoFollowRedir: in.NoFollowRedir,
		CAFile:        in.CAFile,
	})
}

// decodeMatcherList re-decodes a gengotypes-degraded matcher value (`any`) through the
// shared MatcherList JSON codec into the typed []Matcher the runner consumes. A nil /
// unparseable value yields a nil list (no matchers to assert).
func decodeMatcherList(v any) MatcherList {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var ml MatcherList
	if err := json.Unmarshal(raw, &ml); err != nil {
		return nil
	}
	return ml
}

func init() {
	RegisterBuiltinPluginUnit(PluginUnit{
		Providers: []Provider{httpVerb{}},
		Schema:    PluginSchema{CueSource: httpplugin.Schema(), InputDefs: httpplugin.InputDefs},
	})
}
