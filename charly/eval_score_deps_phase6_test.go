package main

import (
	"reflect"
	"testing"
)

// TestTopoSortByDeclarationOrder_DuplicateNamesAcrossRecipes verifies the
// phase-6 from-composition-selftest scenario: two recipes import a
// scenario named "sshd-binary" from the same sshd candy, plus a
// hand-written probe in one of them depends_on "sshd-binary". Without
// SourceRecipe-scoped name resolution, the merged topo-sort would lose
// one of the two scenarios and fail with a false CycleError.
func TestTopoSortByDeclarationOrder_DuplicateNamesAcrossRecipes(t *testing.T) {
	scenarios := []Scenario{
		{Name: "sshd-binary", Pod: "selftest-layer", SourceRecipe: "from-single-kind-selftest"},
		{Name: "sshd-wrapper", Pod: "selftest-layer", SourceRecipe: "from-single-kind-selftest"},
		{Name: "arch-coder-charly", Pod: "selftest-image", SourceRecipe: "from-single-kind-selftest"},
		{Name: "sshd-binary", Pod: "composition-app", SourceRecipe: "from-composition-selftest"},
		{Name: "img-arch-coder-charly", Pod: "composition-app", SourceRecipe: "from-composition-selftest"},
		{Name: "composition-handwritten-probe", Pod: "composition-app", SourceRecipe: "from-composition-selftest", DependsOn: []string{"sshd-binary"}},
	}
	got, err := topoSortByDeclarationOrder(scenarios)
	if err != nil {
		t.Fatalf("unexpected error on cross-recipe duplicate names: %v", err)
	}
	if len(got) != len(scenarios) {
		t.Fatalf("got %d scenarios out, want %d", len(got), len(scenarios))
	}
	// composition-handwritten-probe must appear AFTER its same-recipe
	// sshd-binary (the composition-app one), not the selftest-layer one.
	idxOf := func(name, recipe string) int {
		for i, sc := range got {
			if sc.Name == name && sc.SourceRecipe == recipe {
				return i
			}
		}
		return -1
	}
	probeIdx := idxOf("composition-handwritten-probe", "from-composition-selftest")
	depIdx := idxOf("sshd-binary", "from-composition-selftest")
	if depIdx < 0 || probeIdx < 0 || depIdx >= probeIdx {
		t.Errorf("composition-handwritten-probe must follow its same-recipe sshd-binary; got dep@%d probe@%d", depIdx, probeIdx)
	}
	// Both sshd-binary scenarios must be present in the output.
	count := 0
	for _, sc := range got {
		if sc.Name == "sshd-binary" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 sshd-binary scenarios in output, got %d (names lost across recipe boundary)", count)
	}
	// Names of all scenarios should be preserved.
	wantNames := map[string]int{
		"sshd-binary":                   2,
		"sshd-wrapper":                  1,
		"arch-coder-charly":             1,
		"img-arch-coder-charly":         1,
		"composition-handwritten-probe": 1,
	}
	gotNames := map[string]int{}
	for _, sc := range got {
		gotNames[sc.Name]++
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Errorf("scenario name multiset mismatch: got %v, want %v", gotNames, wantNames)
	}
}
