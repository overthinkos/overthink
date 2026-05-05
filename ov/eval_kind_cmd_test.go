package main

import (
	"reflect"
	"sort"
	"testing"
)

// TestEvalKindCmd_BedTable_Coverage asserts the dispatch table covers
// the seven non-"all" kinds advertised by the EvalKindCmd's `enum:`
// tag, with no extras and no gaps.
func TestEvalKindCmd_BedTable_Coverage(t *testing.T) {
	want := []string{"image", "layer", "pod", "vm", "k8s", "local", "deploy"}
	sort.Strings(want)

	got := validKinds()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("validKinds() = %v, want %v", got, want)
	}
}

// TestEvalKindCmd_BedTable_Specs asserts the bed name + image short
// name + IsVM / IsLocal flags for every kind match the spec laid down
// in the cutover plan.
func TestEvalKindCmd_BedTable_Specs(t *testing.T) {
	cases := []struct {
		kind      string
		wantBed   string
		wantImage string
		wantVM    bool
		wantLocal bool
	}{
		{"image", "eval-image-pod", "eval-image", false, false},
		{"layer", "eval-layer-pod", "eval-layer", false, false},
		{"pod", "eval-pod-pod", "eval-pod", false, false},
		{"vm", "arch-vm", "", true, false},
		{"k8s", "k3s-vm", "", true, false},
		{"local", "eval-local-deploy", "", false, true},
		{"deploy", "eval-deploy-pod", "eval-deploy", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			spec, ok := bedSpecFor(tc.kind)
			if !ok {
				t.Fatalf("bedSpecFor(%q) not found", tc.kind)
			}
			if spec.Bed != tc.wantBed {
				t.Errorf("kind=%s: Bed=%q, want %q", tc.kind, spec.Bed, tc.wantBed)
			}
			if spec.Image != tc.wantImage {
				t.Errorf("kind=%s: Image=%q, want %q", tc.kind, spec.Image, tc.wantImage)
			}
			if spec.IsVM != tc.wantVM {
				t.Errorf("kind=%s: IsVM=%t, want %t", tc.kind, spec.IsVM, tc.wantVM)
			}
			if spec.IsLocal != tc.wantLocal {
				t.Errorf("kind=%s: IsLocal=%t, want %t", tc.kind, spec.IsLocal, tc.wantLocal)
			}
		})
	}
}

// TestEvalKindCmd_BedTable_NoCrossKindCollision asserts every bed
// name + image short name is unique within the table — a duplicate
// would mean two kinds share the same disposable target, which would
// make the per-kind cleanup step in `kind: all` racy.
func TestEvalKindCmd_BedTable_NoCrossKindCollision(t *testing.T) {
	seenBeds := map[string]string{}
	seenImages := map[string]string{}
	for _, e := range bedTable {
		if prev, dup := seenBeds[e.Spec.Bed]; dup {
			t.Errorf("bed name %q used by both kind=%s and kind=%s",
				e.Spec.Bed, prev, e.Kind)
		}
		seenBeds[e.Spec.Bed] = e.Kind

		if e.Spec.Image == "" {
			continue
		}
		if prev, dup := seenImages[e.Spec.Image]; dup {
			t.Errorf("image short name %q used by both kind=%s and kind=%s",
				e.Spec.Image, prev, e.Kind)
		}
		seenImages[e.Spec.Image] = e.Kind
	}
}

// TestEvalKindCmd_KindList_All asserts kindList("all") returns every
// non-"all" kind in declaration order.
func TestEvalKindCmd_KindList_All(t *testing.T) {
	got := kindList("all")
	want := []string{"image", "layer", "pod", "vm", "k8s", "local", "deploy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kindList(all) = %v, want %v", got, want)
	}
}

// TestEvalKindCmd_KindList_Single asserts kindList for a single kind
// returns just that kind.
func TestEvalKindCmd_KindList_Single(t *testing.T) {
	for _, k := range []string{"image", "layer", "pod", "vm", "k8s", "local", "deploy"} {
		got := kindList(k)
		if len(got) != 1 || got[0] != k {
			t.Errorf("kindList(%q) = %v, want [%q]", k, got, k)
		}
	}
}
