package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// Note: schema-v3 removed VmSpec.Disposable / VmSpec.Lifecycle and
// the IsDisposableFields helper — disposability is now a DEPLOY
// property only (see /ov-dev:disposable). The former
// TestVmSpec_DisposableRoundTrip / TestVmSpec_LifecycleAloneDoesNotAuthorize
// tests moved to the DeploymentNode-level equivalents below.

// TestDeployImageConfig_DisposableRoundTrip — same invariants for
// the container-deploy side.
func TestDeployImageConfig_DisposableRoundTrip(t *testing.T) {
	yamlStr := `
disposable: true
lifecycle: dev
`
	var c DeploymentNode
	if err := yaml.Unmarshal([]byte(yamlStr), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.IsDisposable() {
		t.Error("DeploymentNode.IsDisposable() = false; want true")
	}
	if got := c.LifecycleTag(); got != "dev" {
		t.Errorf("LifecycleTag = %q; want dev", got)
	}
}

// TestDeployImageConfig_LifecycleAloneDoesNotAuthorize — container
// mirror of the critical anti-derivation test.
func TestDeployImageConfig_LifecycleAloneDoesNotAuthorize(t *testing.T) {
	yamlStr := `lifecycle: dev`
	var c DeploymentNode
	if err := yaml.Unmarshal([]byte(yamlStr), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.IsDisposable() {
		t.Fatal("DeploymentNode{Lifecycle: dev}.IsDisposable() = true; want false.")
	}
}

// TestMultipleInstances_IndependentFlags — two deploy entries for
// the same image with different disposable values must behave
// independently (the multi-instance requirement).
func TestMultipleInstances_IndependentFlags(t *testing.T) {
	yamlStr := `
provides:
  registry: localhost
images:
  fedora-coder:
    lifecycle: prod
  fedora-coder-dev:
    lifecycle: dev
  fedora-coder-qa:
    lifecycle: qa
    disposable: true
  fedora-coder-scratch:
    disposable: true
`
	var cfg DeployConfig
	if err := yaml.Unmarshal([]byte(yamlStr), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	tests := []struct {
		key  string
		want bool
	}{
		{"fedora-coder", false},         // lifecycle: prod, no disposable → false
		{"fedora-coder-dev", false},     // lifecycle: dev, no disposable → STILL false
		{"fedora-coder-qa", true},       // explicit disposable: true
		{"fedora-coder-scratch", true},  // explicit disposable: true
	}
	for _, tc := range tests {
		e, ok := cfg.Deployment[tc.key]
		if !ok {
			t.Fatalf("image %q missing", tc.key)
		}
		if got := e.IsDisposable(); got != tc.want {
			t.Errorf("%s.IsDisposable() = %v; want %v", tc.key, got, tc.want)
		}
	}
}

