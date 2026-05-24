package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Covers all three section-assignment branches of CollectEval:
//   - layer-level tests default to scope:"build" → Layer section
//   - layer-level tests with scope:"deploy" → Deploy section
//   - image-level Tests default to build → Image section; scope:"deploy" →
//     Deploy section
//   - image-level DeployTests → Deploy section with deploy-default origin
func TestCollectTests_Sections(t *testing.T) {
	layers := map[string]*Layer{
		"redis": {
			Name: "redis",
			tests: []Check{
				{Port: 6379, Listening: ptrBool(true)},                   // build-scope default
				{HTTP: "http://${CONTAINER_IP}/health", Scope: "deploy"}, // deploy-scope
			},
		},
		"base": {
			Name:  "base",
			tests: []Check{{File: "/etc/os-release"}},
		},
	}

	cfg := &Config{
		Image: map[string]ImageConfig{
			"redis-ml": {
				Base:    "base",
				Layer:   []string{"redis"},
				Enabled: boolPtr(true),
				Eval: []Check{
					{Command: "supervisord -v"},
					{HTTP: "https://${DNS}/", Scope: "deploy"},
				},
				DeployEval: []Check{
					{Port: 6379, Reachable: ptrBool(true)},
				},
			},
			"base": {
				Enabled: boolPtr(true),
				Layer:   []string{"base"},
			},
		},
	}

	got := CollectEval(cfg, layers, "redis-ml")
	if got == nil {
		t.Fatal("expected non-nil LabelEvalSet")
	}

	// Layer section: redis (port), base (file). base comes after redis because
	// it's deeper in the chain (layer order within each level, then parent).
	if len(got.Layer) != 2 {
		t.Fatalf("layer section has %d entries, want 2: %+v", len(got.Layer), got.Layer)
	}
	if got.Layer[0].Origin != "layer:redis" || got.Layer[0].Port != 6379 {
		t.Errorf("layer[0] wrong: %+v", got.Layer[0])
	}
	if got.Layer[0].Scope != "build" {
		t.Errorf("layer[0].scope should default to build, got %q", got.Layer[0].Scope)
	}
	if got.Layer[1].Origin != "layer:base" || got.Layer[1].File != "/etc/os-release" {
		t.Errorf("layer[1] wrong: %+v", got.Layer[1])
	}

	// Image section: supervisord -v
	if len(got.Image) != 1 || got.Image[0].Origin != "image:redis-ml" || got.Image[0].Command != "supervisord -v" {
		t.Errorf("image section wrong: %+v", got.Image)
	}

	// Deploy section: layer scope-deploy, image scope-deploy, DeployTests.
	if len(got.Deploy) != 3 {
		t.Fatalf("deploy section has %d entries, want 3: %+v", len(got.Deploy), got.Deploy)
	}
	origins := []string{got.Deploy[0].Origin, got.Deploy[1].Origin, got.Deploy[2].Origin}
	// Order: layer-deploy entries first (in layer walk order), then image scope:deploy, then DeployTests.
	wantOrigins := []string{"layer:redis", "image:redis-ml", "deploy-default"}
	if !reflect.DeepEqual(origins, wantOrigins) {
		t.Errorf("deploy origins = %v, want %v", origins, wantOrigins)
	}
	// DeployTests has scope forced to "deploy".
	if got.Deploy[2].Scope != "deploy" {
		t.Errorf("DeployTests scope should be forced to deploy, got %q", got.Deploy[2].Scope)
	}
}

// No tests anywhere → nil (so the label is omitted from the image entirely).
func TestCollectTests_EmptyReturnsNil(t *testing.T) {
	layers := map[string]*Layer{"l": {Name: "l"}}
	cfg := &Config{Image: map[string]ImageConfig{
		"i": {Enabled: boolPtr(true), Layer: []string{"l"}},
	}}
	if got := CollectEval(cfg, layers, "i"); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// Regression: an image whose layer list uses RAW @github.com/...:version refs
// (the submodule git-ref composition pattern used by image/bootc, image/fedora,
// etc.) must still collect the referenced layers' eval blocks. Before the
// BareRef chokepoint fix in ExpandLayer (ov/graph.go), CollectEval walked the
// raw refs against the BareRef-keyed layer map, missed every one, silently
// swallowed the resulting "unknown layer" error, and collected ZERO
// layer-level checks — so every @github-ref-composed image shipped with
// image-level checks only (e.g. selkies-desktop-bootc: 1 instead of ~77). The
// same chokepoint feeds CollectHooks/Shell/Descriptions/Security/Volumes/Alias,
// so this single test guards the whole family.
func TestCollectEval_RemoteRefLayersResolve(t *testing.T) {
	// Remote layers are keyed by their BareRef (no @ prefix, no :version
	// suffix) — see ScanRemoteLayer in layers.go.
	const bareRef = "github.com/overthinkos/overthink/layers/tailscale"
	layers := map[string]*Layer{
		bareRef: {
			Name:  "tailscale",
			tests: []Check{{File: "/usr/bin/tailscale", Exists: ptrBool(true)}},
		},
	}
	cfg := &Config{
		Image: map[string]ImageConfig{
			"selkies-bootc": {
				Enabled: boolPtr(true),
				// RAW @github ref with :version — exactly as the submodule
				// image.yml writes it.
				Layer: []string{"@" + bareRef + ":v2026.141.1600"},
			},
		},
	}

	got := CollectEval(cfg, layers, "selkies-bootc")
	if got == nil {
		t.Fatal("expected non-nil LabelEvalSet — the @github-ref layer's eval block was dropped (BareRef regression)")
	}
	if len(got.Layer) != 1 {
		t.Fatalf("layer section has %d entries, want 1 (the tailscale check): %+v", len(got.Layer), got.Layer)
	}
	if got.Layer[0].File != "/usr/bin/tailscale" {
		t.Errorf("collected wrong check: %+v", got.Layer[0])
	}
	if got.Layer[0].Origin != "layer:"+bareRef {
		t.Errorf("origin = %q, want %q", got.Layer[0].Origin, "layer:"+bareRef)
	}
}

// Verifies each of the three MergeDeployEval rules.
func TestMergeDeployTests(t *testing.T) {
	baked := []Check{
		{ID: "redis-responds", HTTP: "http://x/", Status: 200, Origin: "deploy-default"},
		{ID: "keepalive", Command: "echo ok", Origin: "deploy-default"},
		{HTTP: "http://y/", Status: 200, Origin: "deploy-default"}, // no id
	}
	local := []Check{
		{ID: "redis-responds", HTTP: "http://z/", Status: 204}, // replaces baked[0]
		{ID: "keepalive", Skip: true},                          // replaces baked[1] with skip
		{HTTP: "http://new/", Status: 200},                     // appends (no id)
	}
	merged := MergeDeployEval(baked, local)

	if len(merged) != 4 {
		t.Fatalf("merged len=%d, want 4: %+v", len(merged), merged)
	}
	if merged[0].HTTP != "http://z/" || merged[0].Status != 204 || merged[0].Origin != "deploy-local" {
		t.Errorf("merged[0] override wrong: %+v", merged[0])
	}
	if !merged[1].Skip || merged[1].Origin != "deploy-local" {
		t.Errorf("merged[1] skip-override wrong: %+v", merged[1])
	}
	// baked[2] (no id) preserved at index 2
	if merged[2].HTTP != "http://y/" || merged[2].Origin != "deploy-default" {
		t.Errorf("merged[2] preserved wrong: %+v", merged[2])
	}
	// local new-append at index 3
	if merged[3].HTTP != "http://new/" || merged[3].Origin != "deploy-local" {
		t.Errorf("merged[3] append wrong: %+v", merged[3])
	}
}

// OCI-label contract: LabelEvalSet → JSON → ExtractMetadata parse path.
// Guards against schema drift between what generate.go emits and what
// ExtractMetadata parses — the two must stay round-trippable.
func TestLabelTests_JSONRoundTrip(t *testing.T) {
	original := &LabelEvalSet{
		Layer: []Check{
			{
				File:     "/usr/bin/redis-server",
				Exists:   ptrBool(true),
				Mode:     "0755",
				Origin:   "layer:redis",
				Scope:    "build",
				ID:       "redis-binary",
				Contains: ContainsList{{Op: "contains", Value: "ELF"}},
			},
			{
				Port:      6379,
				Listening: ptrBool(true),
				Origin:    "layer:redis",
				Scope:     "build",
			},
		},
		Image: []Check{
			{Command: "supervisord -v", Origin: "image:redis-ml", Scope: "build"},
		},
		Deploy: []Check{
			{
				HTTP:   "http://${CONTAINER_IP}:${HOST_PORT:6379}/health",
				Status: 200,
				Body:   MatcherList{{Op: "matches", Value: "^(OK|READY)$"}},
				Origin: "deploy-default",
				Scope:  "deploy",
				ID:     "routed",
			},
		},
	}

	// Emit → parse, simulating the OCI label round-trip that
	// writeJSONLabel performs on the build side and ExtractMetadata on
	// the read side.
	labelJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed LabelEvalSet
	if err := json.Unmarshal(labelJSON, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Per-section length parity.
	if len(parsed.Layer) != 2 || len(parsed.Image) != 1 || len(parsed.Deploy) != 1 {
		t.Fatalf("section lengths changed: layer=%d image=%d deploy=%d",
			len(parsed.Layer), len(parsed.Image), len(parsed.Deploy))
	}

	// Spot-check a matcher survives the round-trip intact — including the
	// canonical {op, value} shape (not the YAML scalar shorthand).
	m := parsed.Deploy[0].Body[0]
	if m.Op != "matches" || m.Value != "^(OK|READY)$" {
		t.Errorf("matcher round-trip lost info: op=%q value=%v", m.Op, m.Value)
	}

	// Origin annotation survives — critical for failure reports.
	if parsed.Layer[0].Origin != "layer:redis" || parsed.Deploy[0].Origin != "deploy-default" {
		t.Errorf("origin annotations lost: layer[0]=%q deploy[0]=%q",
			parsed.Layer[0].Origin, parsed.Deploy[0].Origin)
	}

	// Parameterized variables in strings survive verbatim (must NOT be
	// expanded by the label path).
	if !strings.Contains(parsed.Deploy[0].HTTP, "${HOST_PORT:6379}") {
		t.Errorf("parameterized var lost in HTTP URL: %q", parsed.Deploy[0].HTTP)
	}

	// Pointer fields survive.
	if parsed.Layer[0].Exists == nil || !*parsed.Layer[0].Exists {
		t.Errorf("Exists *bool lost or changed: %v", parsed.Layer[0].Exists)
	}
	if parsed.Layer[1].Listening == nil || !*parsed.Layer[1].Listening {
		t.Errorf("Listening *bool lost or changed")
	}

	// Numeric fields survive.
	if parsed.Layer[1].Port != 6379 {
		t.Errorf("Port lost: %d", parsed.Layer[1].Port)
	}
	if parsed.Deploy[0].Status != 200 {
		t.Errorf("Status lost: %d", parsed.Deploy[0].Status)
	}
}

// Nil/empty inputs produce a well-formed slice without panics.
func TestMergeDeployTests_NilInputs(t *testing.T) {
	if got := MergeDeployEval(nil, nil); len(got) != 0 {
		t.Errorf("nil+nil should be empty, got %v", got)
	}
	baked := []Check{{ID: "a", File: "/x"}}
	if got := MergeDeployEval(baked, nil); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("nil local should preserve baked, got %v", got)
	}
	local := []Check{{ID: "b", Port: 80}}
	got := MergeDeployEval(nil, local)
	if len(got) != 1 || got[0].Origin != "deploy-local" {
		t.Errorf("nil baked should stamp origin on local, got %v", got)
	}
}
