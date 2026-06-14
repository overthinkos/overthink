package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// unknownYAMLKeys returns the "field X not found in type Y" diagnostics for a
// single classified document — every key that matches no struct field and
// would therefore be SILENTLY DROPPED by the lenient decode. It re-decodes
// the already-parsed node with KnownFields(true) into the same target type
// the loader uses. Types with a custom UnmarshalYAML (the candy types) opt
// out of KnownFields and self-decode, so they are never falsely flagged.
// Only unknown-key diagnostics are returned; any other strict-mode complaint
// (type coercion) is suppressed — the loader's lenient decode is the source
// of truth for everything except dropped keys.
func unknownYAMLKeys(node *yaml.Node, shape docShape) []string {
	var probe any
	switch shape {
	case docShapeRoot:
		probe = &UnifiedFile{}
	case docShapeKind:
		probe = &kindKeyedDoc{}
	default:
		return nil
	}
	raw, err := yaml.Marshal(node)
	if err != nil {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(probe); err == nil {
		return nil
	} else {
		var out []string
		for line := range strings.SplitSeq(err.Error(), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "not found in type") {
				out = append(out, line)
			}
		}
		return out
	}
}

// warnUnknownYAMLKeys surfaces key misalignments that yaml.v3 would otherwise
// SILENTLY DROP, as non-fatal Warnings to stderr (the key is still ignored —
// this only makes the drop visible). Catches footguns like singular
// `port_forward`/`device`/`channel`/`cpu` vs the canonical
// `port_forwards`/`devices`/`channels`/`cpu` — which used to vanish with no
// signal. A WARNING, not a hard error, so a genuinely forward-compatible
// config (a newer field on an older binary) still loads.
func warnUnknownYAMLKeys(node *yaml.Node, shape docShape, srcLabel string) {
	for _, msg := range unknownYAMLKeys(node, shape) {
		fmt.Fprintf(os.Stderr,
			"Warning: %s: %s — key ignored; check spelling/pluralization "+
				"(e.g. cpu, devices, channels, port_forwards)\n",
			srcLabel, msg)
	}
}
