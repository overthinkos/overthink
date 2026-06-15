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
// the loader uses. The operator-map / shell-config types whose authored keys
// are not literal struct fields are suppressed via keylintSelfDecodingTypes
// (post-CUE-switch they no longer opt out via a custom UnmarshalYAML), so they
// are never falsely flagged.
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
			if !strings.Contains(line, "not found in type") {
				continue
			}
			if keylintSelfDecodingType(line) {
				continue // false positive — see keylintSelfDecodingTypes
			}
			out = append(out, line)
		}
		return out
	}
}

// keylintSelfDecodingTypes are types whose authored keys are NOT literal struct
// fields: the operator-map matchers self-decode via UnmarshalJSON (any operator
// key is valid), and the shell-config types carry dynamic per-shell sub-blocks
// the CUE normalizer canonicalizes into by_shell. yaml.v3 KnownFields(true)
// cannot know this (it only opts out for types with a custom UnmarshalYAML, which
// these no longer have after the CUE loader switch), so it false-flags their
// valid keys. The CUE loader decodes them correctly; closed-schema unknown-key
// detection for these is `charly box validate`'s job.
var keylintSelfDecodingTypes = []string{
	"main.Matcher", "main.MatcherList", "main.ContainsList", "main.PortScope",
	"main.ShellConfig", "main.ShellSpec", "main.DeployShellOverlay",
}

func keylintSelfDecodingType(line string) bool {
	for _, t := range keylintSelfDecodingTypes {
		if strings.Contains(line, "type "+t) {
			return true
		}
	}
	return false
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
