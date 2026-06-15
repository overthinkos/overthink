package main

import (
	"strings"
	"testing"
)

// IsPreemptible is independent of disposable/ephemeral: a node may be both, and
// neither derives from the other.
func TestDeploymentNode_PreemptibleOrthogonal(t *testing.T) {
	tru := true
	both := DeploymentNode{
		Disposable:  &tru,
		Preemptible: &PreemptibleConfig{Holds: []string{"gpu"}},
	}
	if !both.IsDisposable() {
		t.Error("explicit disposable: true should be disposable")
	}
	if !both.IsPreemptible() {
		t.Error("preemptible with holds should be preemptible")
	}

	// Preemptible does NOT make a node disposable.
	holderOnly := DeploymentNode{Preemptible: &PreemptibleConfig{Holds: []string{"gpu"}}}
	if holderOnly.IsDisposable() {
		t.Error("preemptible must not imply disposable")
	}

	// Disposable does NOT make a node preemptible.
	dispOnly := DeploymentNode{Disposable: &tru}
	if dispOnly.IsPreemptible() {
		t.Error("disposable must not imply preemptible")
	}

	// Empty holds → not preemptible.
	empty := DeploymentNode{Preemptible: &PreemptibleConfig{}}
	if empty.IsPreemptible() {
		t.Error("preemptible with no holds must not count as preemptible")
	}

	// nil → not preemptible.
	if (DeploymentNode{}).IsPreemptible() {
		t.Error("absent preemptible must not be preemptible")
	}
}

func TestPreemptibleConfig_UnmarshalYAML(t *testing.T) {
	// List shorthand → Holds, default stop/restore.
	var listForm DeploymentNode
	if err := decodeViaCUEForTest(t, "preemptible: [gpu, tpu]\n", &listForm); err != nil {
		t.Fatalf("list-shorthand unmarshal: %v", err)
	}
	if got := listForm.PreemptionHolds(); len(got) != 2 || got[0] != "gpu" || got[1] != "tpu" {
		t.Fatalf("list shorthand holds = %v, want [gpu tpu]", got)
	}
	if listForm.Preemptible.EffectiveStop() != PreemptStopShutdown {
		t.Errorf("default stop = %q, want shutdown", listForm.Preemptible.EffectiveStop())
	}
	if listForm.Preemptible.EffectiveRestore() != PreemptRestoreAlways {
		t.Errorf("default restore = %q, want always", listForm.Preemptible.EffectiveRestore())
	}

	// Block form.
	var blockForm DeploymentNode
	blockYAML := "preemptible:\n  holds: [gpu]\n  stop: shutdown\n  restore: on-success\n"
	if err := decodeViaCUEForTest(t, blockYAML, &blockForm); err != nil {
		t.Fatalf("block unmarshal: %v", err)
	}
	if blockForm.Preemptible.EffectiveRestore() != PreemptRestoreSuccess {
		t.Errorf("block restore = %q, want on-success", blockForm.Preemptible.EffectiveRestore())
	}

	// Scalar (e.g. `preemptible: true`) is rejected — a holder must name what
	// it holds. The normalizer leaves a scalar unchanged, so CUE Decode of a
	// scalar into the PreemptibleConfig struct fails.
	var scalarForm DeploymentNode
	if err := decodeViaCUEForTest(t, "preemptible: true\n", &scalarForm); err == nil {
		t.Fatal("scalar preemptible should be rejected")
	}
}

func TestValidatePreemptibleOnNode(t *testing.T) {
	cases := []struct {
		name     string
		node     DeploymentNode
		wantErr  bool
		contains string
	}{
		{
			name: "valid holder",
			node: DeploymentNode{Preemptible: &PreemptibleConfig{Holds: []string{"gpu"}}},
		},
		{
			name: "valid claimant",
			node: DeploymentNode{RequiresExclusive: []string{"gpu"}},
		},
		{
			name:     "empty holds",
			node:     DeploymentNode{Preemptible: &PreemptibleConfig{}},
			wantErr:  true,
			contains: "must list at least one",
		},
		{
			name:     "bad stop",
			node:     DeploymentNode{Preemptible: &PreemptibleConfig{Holds: []string{"gpu"}, Stop: "pause"}},
			wantErr:  true,
			contains: "not supported",
		},
		{
			name:     "bad restore",
			node:     DeploymentNode{Preemptible: &PreemptibleConfig{Holds: []string{"gpu"}, Restore: "maybe"}},
			wantErr:  true,
			contains: "is invalid",
		},
		{
			name:     "empty requires token",
			node:     DeploymentNode{RequiresExclusive: []string{""}},
			wantErr:  true,
			contains: "empty token",
		},
		{
			name: "self-contention",
			node: DeploymentNode{
				Preemptible:       &PreemptibleConfig{Holds: []string{"gpu"}},
				RequiresExclusive: []string{"gpu"},
			},
			wantErr:  true,
			contains: "cannot both hold and require",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := &ValidationError{}
			node := tc.node
			ValidatePreemptibleOnNode(tc.name, &node, errs)
			if errs.HasErrors() != tc.wantErr {
				t.Fatalf("HasErrors=%v want %v (errs=%s)", errs.HasErrors(), tc.wantErr, errs.Error())
			}
			if tc.contains != "" && !strings.Contains(errs.Error(), tc.contains) {
				t.Fatalf("error %q does not contain %q", errs.Error(), tc.contains)
			}
		})
	}
}
