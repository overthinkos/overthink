package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRenderDomainXML_ClassificationMetadata — disposable + lifecycle
// appear as <ov:disposable> / <ov:lifecycle> in <metadata> when set.
func TestRenderDomainXML_ClassificationMetadata(t *testing.T) {
	cases := []struct {
		name       string
		yamlStr    string
		wantFrag   []string
		notWant    []string
	}{
		{
			name: "disposable true + lifecycle dev",
			yamlStr: `
source:
  kind: cloud_image
firmware: bios
disposable: true
lifecycle: dev
`,
			wantFrag: []string{
				`<metadata>`,
				`<ov:classification`,
				`xmlns:ov="https://overthinkos.org/ns/ov/1.0"`,
				`disposable="true"`,
				`lifecycle="dev"`,
			},
		},
		{
			name: "disposable false + lifecycle tag → metadata still emitted",
			yamlStr: `
source:
  kind: cloud_image
firmware: bios
lifecycle: qa
`,
			wantFrag: []string{
				`<ov:classification`,
				`disposable="false"`,
				`lifecycle="qa"`,
			},
		},
		{
			name: "neither field set → no metadata element at all",
			yamlStr: `
source:
  kind: cloud_image
firmware: bios
`,
			notWant: []string{
				`<metadata>`,
				`ov:classification`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s VmSpec
			if err := yaml.Unmarshal([]byte(tc.yamlStr), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			out, err := RenderDomainXML(&s, VmRuntimeParams{
				Name:     "ov-test",
				RamMB:    512,
				Cpus:     1,
				HostArch: "x86_64",
			})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, frag := range tc.wantFrag {
				if !strings.Contains(out, frag) {
					t.Errorf("missing fragment %q in output:\n%s", frag, out)
				}
			}
			for _, frag := range tc.notWant {
				if strings.Contains(out, frag) {
					t.Errorf("unexpected fragment %q in output:\n%s", frag, out)
				}
			}
		})
	}
}

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
