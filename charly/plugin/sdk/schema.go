package sdk

import (
	"fmt"
	"io/fs"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/overthinkos/overthink/charly/internal/schemaconcat"
	pb "github.com/overthinkos/overthink/charly/plugin/proto"
)

// ProvidedCapability is one capability a plugin serves plus the CUE def that
// validates its plugin_input — the SDK-facing form of the proto ProvidedCapability.
// An external plugin lists these in its Describe; the host validates authored
// plugin_input for each word against its def in the served schema.
type ProvidedCapability struct {
	Class    string // "verb" / "kind" / "deploy" / "step" / "builder"
	Word     string // the reserved word, e.g. "externalprobe"
	InputDef string // the CUE def for this word's plugin_input, e.g. "#ExternalprobeInput"
}

// BuildCapabilities is the serve-side half of the "every plugin ships its own CUE
// schema" contract. It concatenates the plugin's embedded schema/*.cue via the SAME
// schemaconcat contract charly uses for its base (R3 — one concat loop, no
// duplicate), compiles it STANDALONE to fail loudly on a broken or empty schema
// (a self-contained schema must compile alone — the same property that lets
// `cue exp gengotypes` generate the plugin's Go params), and assembles the Describe
// reply carrying the raw .cue source the host splices onto its base.
//
// schemaFS is the plugin's `//go:embed schema/*.cue` FS; dir is the embedded
// subdirectory ("schema"). Both the SDK and charly's base reach the same internal
// schemaconcat because the SDK lives under charly/ — an external module imports
// only this SDK, never charly/internal directly.
func BuildCapabilities(calver string, provided []ProvidedCapability, schemaFS fs.FS, dir string) (*pb.Capabilities, error) {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, dir, nil)
	if err != nil {
		return nil, fmt.Errorf("plugin schema: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("plugin ships no CUE schema (every plugin MUST ship its own schema)")
	}
	if v := cuecontext.New().CompileString(body); v.Err() != nil {
		return nil, fmt.Errorf("plugin schema does not compile: %w", v.Err())
	}
	out := make([]*pb.ProvidedCapability, 0, len(provided))
	for _, c := range provided {
		out = append(out, &pb.ProvidedCapability{Class: c.Class, Word: c.Word, InputDef: c.InputDef})
	}
	return &pb.Capabilities{
		Calver:          calver,
		ProtocolVersion: ProtocolVersion,
		Provided:        out,
		SchemaCue:       body,
	}, nil
}
