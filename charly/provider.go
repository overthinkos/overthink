package main

import (
	"context"
	"encoding/json"
)

// Provider is the ONE extension abstraction. Every reserved word — every kind,
// every verb, and the deploy-target / step / builder classes — is served by a
// Provider, which may be IN-PROCESS (a built-in, registered from init() wrapping
// today's handler funcs) or OUT-OF-PROCESS (a plugin, served over go-plugin gRPC).
// The registry, the bijection gate, and every call site treat both identically —
// the transport is invisible above the registry (see registry.go).
//
// The unified shape mirrors the CUE-single-source model: a reserved word maps to
// a handler taking CUE-generated params. `Invoke` is the wire-aligned form (it is
// exactly the proto Provider.Invoke envelope), so an out-of-proc plugin and an
// in-proc built-in are the same shape. Built-ins MAY additionally satisfy a typed
// fast-path adapter per class so an in-proc call skips JSON (added per class as
// the switch cutovers C1–C5 migrate each handler).
type Provider interface {
	// Reserved is the reserved word this provider serves ("exampleprobe","cdp",
	// "box","local","pixi"). It is the registry key within the provider's Class.
	Reserved() string
	// Class is which extension family the reserved word belongs to. A word may
	// exist in two classes (e.g. "k8s" is both a kind and a verb), so the registry
	// keys on (Class, Reserved), never Reserved alone.
	Class() ProviderClass
	// Invoke runs one operation. op.Op selects the operation for the class
	// (run/load/validate/emit/render/resolve); op.Params carries the CUE-typed
	// params; op.Env carries the serializable invocation context. The returned
	// Result.JSON is the class-appropriate payload (a CheckResult for a verb, an
	// InstallPlan for a deploy, Diagnostics for a kind validate, …).
	Invoke(ctx context.Context, op *Operation) (*Result, error)
}

// ProviderClass is the extension family a reserved word belongs to. A plugin's
// `provides:` lists capabilities as "<class>:<word>" (e.g. "verb:exampleprobe").
type ProviderClass string

const (
	ClassKind         ProviderClass = "kind"
	ClassVerb         ProviderClass = "verb"
	ClassDeployTarget ProviderClass = "deploy"
	ClassStep         ProviderClass = "step"
	ClassBuilder      ProviderClass = "builder"
)

// providerClasses is the closed set, used by the loader to validate a plugin's
// `provides:` entries and by the bijection gate.
var providerClasses = map[ProviderClass]bool{
	ClassKind: true, ClassVerb: true, ClassDeployTarget: true, ClassStep: true, ClassBuilder: true,
}

// Operation is the uniform invocation envelope (wire-aligned with proto
// InvokeRequest). Params and Env are JSON so the generated `spec` structs stay
// the single source of truth — there is no parallel proto type system (R3).
type Operation struct {
	Reserved string          `json:"reserved"` // the reserved word
	Op       string          `json:"op"`       // operation selector for the word's class
	Params   json.RawMessage `json:"params"`   // CUE-typed params (Op for verbs/steps; entity for kinds)
	Env      json.RawMessage `json:"env"`      // snapshotCheckEnv / venue descriptor
}

// Result is the uniform invocation result (wire-aligned with proto InvokeReply /
// Frame). JSON is the class-appropriate payload, decoded by the call site.
type Result struct {
	JSON json.RawMessage `json:"json"`
}

// Operation selectors (op.Op). Each class uses the subset it needs.
const (
	OpRun      = "run"      // verb: run a check / live-container probe → CheckResult
	OpLoad     = "load"     // kind: decode a node into its typed entity
	OpValidate = "validate" // kind: closed/concrete CUE validation → Diagnostics
	OpEmit     = "emit"     // deploy/step: emit an InstallPlan / Containerfile fragment
	OpExecute  = "execute"  // deploy/step: execute against a venue (streamed)
	OpResolve  = "resolve"  // builder: resolve a builder image + steps
)

// marshalParams / unmarshalResult are the small helpers the in-proc adapters and
// the gRPC stubs share so the envelope is built one way (R3).
func marshalJSON(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
