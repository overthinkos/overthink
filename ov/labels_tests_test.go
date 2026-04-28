package main

import (
	"encoding/json"
	"testing"
)

// Exercises the full OCI-label read path for the tests manifest:
// InspectLabels → ExtractMetadata → ImageMetadata.Eval.
//
// This is the read-side complement to TestLabelTests_JSONRoundTrip, which
// only validates the marshaling path. Together they prove the contract
// between writeLabels (ov/generate.go) and ExtractMetadata (ov/labels.go)
// round-trips without data loss.
func TestExtractMetadata_Tests(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	testsBlob, err := json.Marshal(&LabelEvalSet{
		Layer: []Check{
			{File: "/usr/bin/redis-server", Exists: ptrBool(true), Origin: "layer:redis", Scope: "build"},
		},
		Image: []Check{
			{Command: "supervisord -v", Origin: "image:redis-ml", Scope: "build"},
		},
		Deploy: []Check{
			{
				HTTP:   "http://${CONTAINER_IP}:${HOST_PORT:6379}/health",
				Status: 200,
				Origin: "deploy-default",
				Scope:  "deploy",
				ID:     "routed",
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion: "1",
			LabelImage:   "redis-ml",
			LabelEval:   string(testsBlob),
		}, nil
	}

	meta, err := ExtractMetadata("podman", "ghcr.io/overthinkos/redis-ml:test")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if meta == nil {
		t.Fatal("meta is nil")
	}
	if meta.Eval == nil {
		t.Fatal("Tests was not parsed from label")
	}

	if len(meta.Eval.Layer) != 1 || meta.Eval.Layer[0].File != "/usr/bin/redis-server" {
		t.Errorf("layer section wrong: %+v", meta.Eval.Layer)
	}
	if len(meta.Eval.Image) != 1 || meta.Eval.Image[0].Command != "supervisord -v" {
		t.Errorf("image section wrong: %+v", meta.Eval.Image)
	}
	if len(meta.Eval.Deploy) != 1 {
		t.Fatalf("deploy section wrong: %+v", meta.Eval.Deploy)
	}
	d := meta.Eval.Deploy[0]
	if d.ID != "routed" {
		t.Errorf("deploy id = %q", d.ID)
	}
	if d.Status != 200 {
		t.Errorf("deploy status = %d", d.Status)
	}
	// Crucial: parameterized vars must survive verbatim in the label so
	// ResolveTestVars can substitute them at ov test time.
	if d.HTTP != "http://${CONTAINER_IP}:${HOST_PORT:6379}/health" {
		t.Errorf("deploy HTTP lost template: %q", d.HTTP)
	}
}

// No tests label ⇒ meta.Eval stays nil. Confirms the absence path
// doesn't spuriously create empty sections that would confuse callers.
func TestExtractMetadata_Tests_AbsentLabel(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{LabelVersion: "1", LabelImage: "x"}, nil
	}
	meta, err := ExtractMetadata("podman", "x")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if meta.Eval != nil {
		t.Errorf("Tests should be nil when label absent, got %+v", meta.Eval)
	}
}

// Malformed tests label surfaces a clear parse error.
func TestExtractMetadata_Tests_MalformedLabel(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion: "1",
			LabelImage:   "x",
			LabelEval:   "{not valid json",
		}, nil
	}
	_, err := ExtractMetadata("podman", "x")
	if err == nil {
		t.Fatal("expected parse error on malformed tests label")
	}
}
