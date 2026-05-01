package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadVmInstanceOverride_Missing covers the common case: no
// override file → (nil, nil).
func TestLoadVmInstanceOverride_Missing(t *testing.T) {
	// Use a domain name very unlikely to exist in the test runner's
	// home dir. If it does exist, the test is invalid and we'd want
	// to know.
	domain := "test-instance-override-missing-domain-" + t.Name()
	got, err := LoadVmInstanceOverride(domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing domain, got %v", got)
	}
}

// TestLoadVmInstanceOverride_Disposable covers the disposable: false
// override pulling from a known on-disk YAML.
func TestLoadVmInstanceOverride_Disposable(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	domain := "ov-archtest"
	dir := filepath.Join(tmpHome, ".local", "share", "ov", "vm", domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instance.yml"), []byte("disposable: false\nlifecycle: long-running\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadVmInstanceOverride(domain)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil override")
	}
	if got.Disposable == nil {
		t.Fatal("expected Disposable to be non-nil pointer")
	}
	if *got.Disposable != false {
		t.Errorf("Disposable = %v, want false", *got.Disposable)
	}
	if got.Lifecycle != "long-running" {
		t.Errorf("Lifecycle = %q, want %q", got.Lifecycle, "long-running")
	}
}

// TestApplyToVmClassification covers all four merge branches.
func TestApplyToVmClassification(t *testing.T) {
	// nil override returns upstream verbatim
	{
		var ov *VmInstanceOverride
		d, life := ov.ApplyToVmClassification(true, "dev")
		if d != true || life != "dev" {
			t.Errorf("nil override: got (%v,%q), want (true,dev)", d, life)
		}
	}
	// empty override returns upstream verbatim
	{
		ov := &VmInstanceOverride{}
		d, life := ov.ApplyToVmClassification(true, "dev")
		if d != true || life != "dev" {
			t.Errorf("empty override: got (%v,%q), want (true,dev)", d, life)
		}
	}
	// disposable: false flips
	{
		f := false
		ov := &VmInstanceOverride{Disposable: &f}
		d, life := ov.ApplyToVmClassification(true, "dev")
		if d != false || life != "dev" {
			t.Errorf("disposable override: got (%v,%q), want (false,dev)", d, life)
		}
	}
	// lifecycle replaces
	{
		ov := &VmInstanceOverride{Lifecycle: "prod"}
		d, life := ov.ApplyToVmClassification(true, "dev")
		if d != true || life != "prod" {
			t.Errorf("lifecycle override: got (%v,%q), want (true,prod)", d, life)
		}
	}
	// both fields override
	{
		f := false
		ov := &VmInstanceOverride{Disposable: &f, Lifecycle: "prod"}
		d, life := ov.ApplyToVmClassification(true, "dev")
		if d != false || life != "prod" {
			t.Errorf("both override: got (%v,%q), want (false,prod)", d, life)
		}
	}
}
