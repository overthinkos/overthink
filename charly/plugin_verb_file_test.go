package main

import (
	"reflect"
	"testing"
)

// TestDecodeContainsList covers the file plugin's contains-default codec — the
// replacement for the base #Op `contains` load normalizer that left #Op with the `file`
// verb when it was extracted into a plugin. A BARE scalar
// element defaults to Op="contains" (substring match), while an explicit single-operator
// map keeps its authored operator. The input is the gengotypes-degraded `any` shape a
// migrated `plugin_input.contains` decodes to (a scalar, a list, or an operator map).
func TestDecodeContainsList(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		wantOps   []string
		wantValue []any
	}{
		{
			name:      "bare scalar promotes to contains",
			in:        "foo",
			wantOps:   []string{"contains"},
			wantValue: []any{"foo"},
		},
		{
			name:      "bare sequence promotes each element to contains",
			in:        []any{"foo", "bar"},
			wantOps:   []string{"contains", "contains"},
			wantValue: []any{"foo", "bar"},
		},
		{
			name:      "explicit equals map keeps Op=equals",
			in:        map[string]any{"equals": "foo"},
			wantOps:   []string{"equals"},
			wantValue: []any{"foo"},
		},
		{
			name:      "explicit matches map keeps Op=matches",
			in:        map[string]any{"matches": "^prefix"},
			wantOps:   []string{"matches"},
			wantValue: []any{"^prefix"},
		},
		{
			name:      "explicit not_contains map keeps Op=not_contains",
			in:        []any{map[string]any{"not_contains": "nope"}},
			wantOps:   []string{"not_contains"},
			wantValue: []any{"nope"},
		},
		{
			name:      "mixed sequence: explicit equals + bare scalar",
			in:        []any{map[string]any{"equals": "foo"}, "bar"},
			wantOps:   []string{"equals", "contains"},
			wantValue: []any{"foo", "bar"},
		},
		{
			// The real-world harness probe shape that motivated the contains-default
			// (a file probe asking for substring containment of a marker, never equality).
			name:      "real-world marker list defaults to contains",
			in:        []any{"charly-fixture-web-content-marker"},
			wantOps:   []string{"contains"},
			wantValue: []any{"charly-fixture-web-content-marker"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeContainsList(tc.in)
			if len(got) != len(tc.wantOps) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.wantOps), got)
			}
			for i := range got {
				if got[i].Op != tc.wantOps[i] {
					t.Errorf("[%d].Op = %q, want %q", i, got[i].Op, tc.wantOps[i])
				}
				if !reflect.DeepEqual(got[i].Value, tc.wantValue[i]) {
					t.Errorf("[%d].Value = %v (%T), want %v (%T)",
						i, got[i].Value, got[i].Value, tc.wantValue[i], tc.wantValue[i])
				}
			}
		})
	}
}

// TestDecodeContainsList_Nil ensures an absent contains decodes to a nil list (no
// matchers to assert), matching decodeMatcherList's nil handling.
func TestDecodeContainsList_Nil(t *testing.T) {
	if got := decodeContainsList(nil); got != nil {
		t.Errorf("decodeContainsList(nil) = %v, want nil", got)
	}
}
