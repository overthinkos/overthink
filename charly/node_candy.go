package main

// node_candy.go — the candy constructor for the unified node-form model. A candy
// node's discriminator value carries the scalars / composition refs / config maps;
// its children (package/env/service/… data nodes + run/check/… step nodes) fold
// into the candy body via the generic assembler, then decode through the shared
// CUE entity decoder. Proven byte-identical to the kind-first decode by
// TestBuildCandy_RoundTrip (node_candy_test.go).

import "fmt"

// buildCandy turns a `candy`-discriminator genericNode into an InlineCandy. The
// candy name is the node name (the entity-map key), returned alongside.
func buildCandy(gn *genericNode) (name string, ic *InlineCandy, err error) {
	if gn.disc != "candy" {
		return "", nil, fmt.Errorf("buildCandy: node %q is not a candy (disc %q)", gn.name, gn.disc)
	}
	// Decode-ONLY at load (fast, runs on every invocation): the full closed-schema
	// CUE validation (CalVer/enum/unknown-key checks) runs at `charly box validate`
	// (validateCandyManifestCUE), not here — matching the legacy parseCandyYAML.
	var c CandyYAML
	if err := decodeNodeValue(gn, &c); err != nil {
		return "", nil, err
	}
	// Name is the node KEY in node-form (the migration moves a legacy body
	// `name:` up to the key), so stamp it — the decoded body carries no `name:`.
	c.Name = gn.name
	return gn.name, &InlineCandy{CandyYAML: c}, nil
}
