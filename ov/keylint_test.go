package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeProbeNode parses a single YAML document into a *yaml.Node for the
// key-lint probe.
func decodeProbeNode(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &n
}

// TestUnknownYAMLKeys_FlagsMisalignedKeys asserts the key-lint surfaces the
// real silent-drop footguns (singular vs canonical-plural / native keys) and
// stays quiet on correctly-spelled config.
func TestUnknownYAMLKeys_FlagsMisalignedKeys(t *testing.T) {
	cases := []struct {
		name      string
		shape     docShape
		src       string
		wantKey   string // substring expected in a warning (empty = expect none)
		wantClean bool
	}{
		{
			name:    "vm singular device dropped",
			shape:   docShapeRoot,
			src:     "vm:\n  k:\n    source: {kind: cloud_image}\n    libvirt:\n      device: {}\n",
			wantKey: "field device not found",
		},
		{
			name:    "vm singular channel dropped",
			shape:   docShapeRoot,
			src:     "vm:\n  k:\n    source: {kind: cloud_image}\n    libvirt:\n      devices:\n        channel: []\n",
			wantKey: "field channel not found",
		},
		{
			name:      "vm canonical keys are clean",
			shape:     docShapeRoot,
			src:       "vm:\n  k:\n    source: {kind: cloud_image}\n    cpu: 2\n    backend: libvirt\n    libvirt:\n      devices:\n        channels: []\n",
			wantClean: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unknownYAMLKeys(decodeProbeNode(t, tc.src), tc.shape)
			if tc.wantClean {
				if len(got) != 0 {
					t.Fatalf("expected no warnings, got %v", got)
				}
				return
			}
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, tc.wantKey) {
				t.Fatalf("expected a warning containing %q, got %v", tc.wantKey, got)
			}
		})
	}
}
