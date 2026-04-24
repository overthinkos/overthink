package main

import (
	"strings"
	"testing"
)

// Note: the former TestRenderDomainXML_ClassificationMetadata was
// deleted in the schema-v3 cutover. Disposability is now a DEPLOY
// property, not a VM-spec property, so libvirt XML no longer encodes
// <ov:classification> metadata sourced from VmSpec. Authors who want
// the flag visible in `virsh dumpxml` can inject it via the spec's
// xml_passthrough: field; the renderer deliberately doesn't bake it
// in from deploy-level state.

// TestRebuildCmd_RefuseMessageContent — sanity check that the
// refusal error message names the offending resource, the current
// lifecycle, and the remediation path.
func TestRebuildCmd_RefuseMessageContent(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		lifecycle string
		wantSubs  []string
	}{
		{
			name:      "vm refusal cites vms.yml",
			kind:      "vm",
			lifecycle: "dev",
			wantSubs:  []string{"vms.yml", "`disposable: true`", "current lifecycle: dev"},
		},
		{
			name:      "deploy refusal cites deploy.yml + CLI hint",
			kind:      "deploy",
			lifecycle: "",
			wantSubs:  []string{"deploy.yml", "ov deploy add", "--disposable", "current lifecycle: (unset)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &RebuildCmd{Name: "testname"}
			err := c.refuseMessage(tc.kind, tc.lifecycle)
			if err == nil {
				t.Fatal("expected error")
			}
			msg := err.Error()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(msg, sub) {
					t.Errorf("refusal message missing %q; full message:\n%s", sub, msg)
				}
			}
			// Critical: every refusal must mention "testname" so the
			// user can tell which resource was refused.
			if !strings.Contains(msg, "testname") {
				t.Errorf("refusal message doesn't name the resource; message:\n%s", msg)
			}
		})
	}
}
