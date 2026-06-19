package main

// Egress validation — gate the config artifacts charly WRITES to a system
// (cloud-init, k8s manifests, traefik routes, runtime config, ledger JSON,
// systemd/quadlet units, ssh_config, …) against a CUE schema BEFORE the bytes
// hit disk. The egress counterpart to the CUE INGRESS validation in
// cue_schema.go: ingress proves the input config; egress proves the output.
//
// Two schema sources, one validator:
//   - charly's OWN egress kinds live in the concatenated, package-less
//     sharedCueSchema (cue_schema.go) and resolve via cueKindDef.
//   - VENDORED schemas (an upstream JSON-Schema run through `cue import
//     jsonschema:`) carry a `package` clause + CUE-stdlib `import (...)`, so they
//     CANNOT be string-concatenated into sharedCueSchema's blob. Each compiles as
//     its OWN cue.Value via CompileBytes (the CUE-stdlib imports resolve in the
//     bare cueSchemaCtx with no module loader — proven on cloud-init's schema) and
//     registers into egressKindDefs.
//
// This file is grown per cutover: ValidateEgress (validate serialized bytes) is
// the foundation; GenerateEgress (Go value -> CUE generate+validate -> YAML) and
// validateTextEgress (string-constraint check for non-data text) arrive with the
// cutovers that first consume them (k8s; the text-format pre-images).

import (
	"embed"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/xml/koala"
	cueyaml "cuelang.org/go/encoding/yaml"
)

//go:embed schema/vendor/*.cue
var vendorSchemaFS embed.FS

// egressKindDefs maps an egress kind to its def cue.Value, compiled from a
// vendored (package+import) schema file as its own instance. charly's own kinds
// are NOT here — they resolve through cueKindDef against sharedCueSchema.
var egressKindDefs = map[string]cue.Value{}

// registerVendoredEgressKind compiles a vendored schema file on its own (it has
// a `package` clause + imports, so it can't join sharedCueSchema) and records
// the kind -> def mapping. Panics on a read/compile/lookup failure — fail-fast
// at process start, mirroring registerCueKind.
func registerVendoredEgressKind(kind, file, defPath string) {
	if _, dup := egressKindDefs[kind]; dup {
		panic(fmt.Sprintf("duplicate vendored egress kind registration: %q", kind))
	}
	data, err := vendorSchemaFS.ReadFile("schema/vendor/" + file)
	if err != nil {
		panic(fmt.Sprintf("read vendored schema %s: %v", file, err))
	}
	v := cueSchemaCtx.CompileBytes(data)
	if v.Err() != nil {
		panic(fmt.Sprintf("vendored schema %s failed to compile: %v", file, errors.Details(v.Err(), nil)))
	}
	def := v.LookupPath(cue.ParsePath(defPath))
	if def.Err() != nil {
		panic(fmt.Sprintf("vendored egress kind %q: definition %s not found in %s: %v", kind, defPath, file, def.Err()))
	}
	egressKindDefs[kind] = def
}

// egressDef returns the schema def for an egress kind: the vendored registry
// first, then charly's own shared-scope kinds (so an egress kind may reuse an
// already-registered ingress #Kind).
func egressDef(kind string) (cue.Value, bool) {
	if d, ok := egressKindDefs[kind]; ok {
		return d, true
	}
	return cueKindDef(kind)
}

// ValidateEgress validates already-serialized YAML or JSON bytes against the
// egress kind's schema before they are written. JSON is a YAML subset, so one
// ingest path covers both. label identifies the artifact in errors.
func ValidateEgress(kind, label string, data []byte) error {
	def, ok := egressDef(kind)
	if !ok {
		return fmt.Errorf("%s: no egress schema registered for kind %q", label, kind)
	}
	af, err := cueyaml.Extract(label, data)
	if err != nil {
		return fmt.Errorf("%s: egress ingest: %w", label, err)
	}
	v := cueSchemaCtx.BuildFile(af)
	if v.Err() != nil {
		return fmt.Errorf("%s: egress build: %w", label, v.Err())
	}
	if err := v.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: egress validation failed:\n%s", label, errors.Details(err, nil))
	}
	return nil
}

// ValidateEgressValue validates an in-memory Go value (a manifest map[string]any,
// a record struct) against the egress kind's schema — no marshal roundtrip. Used
// where the writer holds the artifact as a Go value just before serialization
// (k8s manifests, ledger records). label identifies the artifact in errors.
func ValidateEgressValue(kind, label string, v any) error {
	def, ok := egressDef(kind)
	if !ok {
		return fmt.Errorf("%s: no egress schema registered for kind %q", label, kind)
	}
	val := cueSchemaCtx.Encode(v)
	if val.Err() != nil {
		return fmt.Errorf("%s: egress encode value: %w", label, val.Err())
	}
	if err := val.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: egress validation failed:\n%s", label, errors.Details(err, nil))
	}
	return nil
}

// validateTextEgress validates a rendered NON-DATA text artifact (Containerfile,
// systemd/supervisord unit, …) by unifying it as a CUE string with the
// rendered_text string-constraint def — #RenderedText rejects the Go text/template
// nil-field marker "<no value>", catching a render failure before the text hits
// disk. The def MUST be a string schema; no concreteness requirement.
func validateTextEgress(label, text string) error {
	def, ok := egressDef("rendered_text")
	if !ok {
		return fmt.Errorf("%s: no egress schema registered for kind \"rendered_text\"", label)
	}
	v := cueSchemaCtx.Encode(text)
	if v.Err() != nil {
		return fmt.Errorf("%s: text egress encode: %w", label, v.Err())
	}
	if err := v.Unify(def).Validate(); err != nil {
		return fmt.Errorf("%s: text egress validation failed:\n%s", label, errors.Details(err, nil))
	}
	return nil
}

// ValidateXMLEgress validates a rendered XML artifact (the libvirt domain XML) by
// koala-decoding it (cuelang.org/go/encoding/xml/koala — elements→structs,
// attributes→`$`-fields, text→`$$`) and unifying with the kind's koala-shaped
// schema. koala is EXPERIMENTAL and best-effort: if it cannot DECODE the XML (a
// koala limitation, not a malformed domain), this returns nil and defers to the
// authoritative downstream gate (libvirt's DomainDefineXML). A genuine schema
// violation on a SUCCESSFULLY-decoded document IS returned as a hard error.
func ValidateXMLEgress(kind, label, xmlStr string) error {
	def, ok := egressDef(kind)
	if !ok {
		return fmt.Errorf("%s: no egress schema registered for kind %q", label, kind)
	}
	expr, err := koala.NewDecoder(label, strings.NewReader(xmlStr)).Decode()
	if err != nil {
		return nil // best-effort: experimental koala couldn't parse it; defer to libvirt
	}
	v := cueSchemaCtx.BuildExpr(expr)
	if v.Err() != nil {
		return nil // best-effort
	}
	// Concrete(true): koala maps an empty element (<name></name>) to an absent
	// `$$` field, so a non-concrete check would pass it. Concreteness requires the
	// schema-constrained fields ($type / name.$$ / memory.$$) to be present and
	// non-empty — exactly the "malformed domain" signal. The decoded XML is all
	// concrete strings, so a valid domain unifies concretely.
	if err := v.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: XML egress validation failed:\n%s", label, errors.Details(err, nil))
	}
	return nil
}
