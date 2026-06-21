package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

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
  box:
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
		"child-under-childless-kind": `
redis:
  candy:
    version: "2026.150.0000"
    description: x
  inner:
    box:
      base: fedora
`,
		"wrong-kind-member-under-group": `
shop:
  group:
    disposable: true
  inner:
    box:
      base: fedora
`,
		"two-discriminators": `
db:
  vm:
    source: {kind: cloud_image, url: "http://x"}
  pod:
    box: y
`,
	}
	for name, doc := range bad {
		if !nodeFormRejected(doc) {
			t.Errorf("%s: expected a strictness rejection (CUE gate or parser), but the document was accepted", name)
		}
	}
}
