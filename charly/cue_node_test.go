package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestNodeDoc_AgentOutputFormat_RejectedByCUE proves the closed CUE schema rejects an
// illegal agent output_format at LOAD (agent.cue: output_format: *"" | "stream-json",
// under the closed #NodeDoc gate). It fails LOUDLY if the CUE gate ever stops covering
// it — signalling a Go-side output_format validator would be required.
func TestNodeDoc_AgentOutputFormat_RejectedByCUE(t *testing.T) {
	valid := "myagent:\n  agent:\n    command: [\"x\"]\n    output_format: stream-json\n"
	if nodeFormRejected(valid) {
		t.Fatalf("valid agent (output_format: stream-json) was rejected by the node-form gates")
	}
	bad := "myagent:\n  agent:\n    command: [\"x\"]\n    output_format: bogus\n"
	if !nodeFormRejected(bad) {
		t.Fatal("illegal agent output_format 'bogus' was NOT rejected by the closed CUE schema — a Go-side output_format validator is required")
	}
}

// nodeFormRejected reports whether the layered node-form strictness gates reject a
// document — the CUE document gate (closed kind VALUES + two-discriminator /
// reserved-key closedness) OR the Go parser (a typo'd discriminator, a wrong-kind /
// childless-kind child, a two-discriminator node). Both are hard load errors before
// any execution; together they are the "CUE-strict, no loosening" guarantee.
func nodeFormRejected(doc string) bool {
	if validateNodeDocCUE("t", []byte(doc)) != nil {
		return true
	}
	var d yaml.Node
	if yaml.Unmarshal([]byte(doc), &d) != nil {
		return true
	}
	_, _, err := parseNodeTree(&d)
	return err != nil
}

// nodeDocValid is a realistic unified node-form document (child-node): a distro, a
// candy whose package/env/composition and a plan step are each CHILD nodes, a box
// whose composition is a child, and a bundle group with bundle members, an inline
// cross-member check (${HOST:cache}) as a step child under a member, and a
// deeply-nested deploy-into with its own check child.
const nodeDocValid = `
version: "2026.180.0000"
fedora:
  distro:
    version: "43"
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
  redis-package:
    package: [redis]
  redis-env:
    env:
      REDIS_DATA: /var/lib/redis
  redis-binary:
    check: the binary exists
    file: /usr/bin/redis-server
coder:
  candy:
    base: fedora
  coder-candy:
    candy: [redis]
shop:
  bundle:
    disposable: true
  web:
    bundle:
      box: coder
    web-reaches-cache:
      check: web reaches the cache
      command: "redis-cli -h ${HOST:cache} ping"
  cache:
    bundle:
      box: coder
    migrate:
      bundle:
        box: coder
      migrate-ran:
        check: migration ran
        command: "test -f /done"
`

// TestValidateNodeDocCUE proves the Go CUE path (not just the cue CLI) enforces
// #Node strictness on a whole document: the valid node-form passes, and every
// strictness violation is a hard error.
func TestValidateNodeDocCUE(t *testing.T) {
	if err := validateNodeDocCUE("valid", []byte(nodeDocValid)); err != nil {
		t.Fatalf("valid node-form document rejected: %v", err)
	}

	bad := map[string]string{
		"typo-discriminator": `
fedora:
  distroo:
    version: "43"
`,
		"unknown-field-in-kind-value": `
redis:
  candy:
    version: "2026.150.0000"
    description: x
    statuz: working
`,
		// A sub-ENTITY (resource-kind) child under a non-deployable kind is
		// rejected (node_parse.go wrong-kind-child gate). EDGE-INHERIT cutover D
		// merged box: into candy:, and a `candy:` CHILD is the composition DATA key
		// (an image/layer can never be a deploy member), so the gate is exercised
		// with a genuine resource-kind sub-entity (pod/vm), not a candy: mapping.
		"resource-entity-under-childless-candy": `
redis:
  candy:
    version: "2026.150.0000"
    description: x
  inner:
    pod:
      image: x
`,
		"resource-entity-under-childless-distro": `
fedora:
  distro:
    bootstrap:
      install_cmd: "true"
  inner:
    vm:
      source: {kind: cloud_image, url: "http://x"}
`,
		"two-discriminators": `
db:
  vm:
    source: {kind: cloud_image, url: "http://x"}
  pod:
    image: y
`,
	}
	for name, doc := range bad {
		if !nodeFormRejected(doc) {
			t.Errorf("%s: expected a strictness rejection (CUE gate or parser), but the document was accepted", name)
		}
	}
}
