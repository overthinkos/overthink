package main

import "testing"

// TestMergeKindDoc_Android verifies a standalone `kind: android` document
// routes into UnifiedFile.Android, and that a missing name is rejected.
func TestMergeKindDoc_Android(t *testing.T) {
	merged := &UnifiedFile{}
	kd := &kindKeyedDoc{Android: &AndroidDoc{
		Name:        "pixel9a-36",
		AndroidSpec: AndroidSpec{Box: "android-emulator", Device: "pixel_9a", ApiLevel: 36},
	}}
	if err := mergeKindDoc(merged, kd, "/tmp"); err != nil {
		t.Fatalf("mergeKindDoc(android) err: %v", err)
	}
	got := merged.Android["pixel9a-36"]
	if got == nil {
		t.Fatal("kind:android doc not registered in merged.Android")
	}
	if got.Box != "android-emulator" || got.Device != "pixel_9a" || got.ApiLevel != 36 {
		t.Errorf("android spec round-trip wrong: %+v", got)
	}

	// Missing name is an error.
	if err := mergeKindDoc(&UnifiedFile{}, &kindKeyedDoc{Android: &AndroidDoc{}}, "/tmp"); err == nil {
		t.Error("kind:android with empty name should error")
	}
}

// TestMergeAndroidMap verifies the root-wins merge semantics for android: maps.
func TestMergeAndroidMap(t *testing.T) {
	dst := map[string]*AndroidSpec{"a": {Box: "keep"}}
	src := map[string]*AndroidSpec{"a": {Box: "drop"}, "b": {Box: "add"}}
	mergeAndroidMap(&dst, src)
	if dst["a"].Box != "keep" {
		t.Errorf("existing entry should win: got %q", dst["a"].Box)
	}
	if dst["b"] == nil || dst["b"].Box != "add" {
		t.Errorf("new entry should be added: %+v", dst["b"])
	}
}

// TestValidateCheckBeds_Android covers the kind:check bed validation for a
// top-level target: android bed.
func TestValidateCheckBeds_Android(t *testing.T) {
	// android bed without an android: ref → error.
	uf := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"bed": {Target: "android", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf); err == nil {
		t.Error("target:android bed without android: should fail validation")
	}

	// android bed referencing an undefined device → error.
	uf2 := &UnifiedFile{
		Check: map[string]DeploymentNode{
			"bed": {Target: "android", Android: "ghost", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf2); err == nil {
		t.Error("target:android bed referencing an undefined device should fail")
	}

	// android bed referencing a defined device → ok.
	uf3 := &UnifiedFile{
		Android: map[string]*AndroidSpec{"dev": {Box: "android-emulator"}},
		Check: map[string]DeploymentNode{
			"bed": {Target: "android", Android: "dev", Disposable: new(true)},
		},
	}
	if err := validateCheckBeds(uf3); err != nil {
		t.Errorf("valid target:android bed should pass, got: %v", err)
	}
}
