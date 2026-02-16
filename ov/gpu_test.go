package main

import "testing"

func TestGPUFlagsMode(t *testing.T) {
	tests := []struct {
		name string
		flags GPUFlags
		want  GPUMode
	}{
		{"default", GPUFlags{}, GPUAuto},
		{"gpu on", GPUFlags{GPU: true}, GPUOn},
		{"gpu off", GPUFlags{NoGPU: true}, GPUOff},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.flags.Mode()
			if got != tt.want {
				t.Errorf("GPUFlags%+v.Mode() = %d, want %d", tt.flags, got, tt.want)
			}
		})
	}
}

func TestResolveGPU(t *testing.T) {
	// Override DetectGPU for testing
	orig := DetectGPU
	defer func() { DetectGPU = orig }()

	t.Run("on ignores detection", func(t *testing.T) {
		DetectGPU = func() bool { return false }
		if !ResolveGPU(GPUOn) {
			t.Error("ResolveGPU(GPUOn) should return true regardless of detection")
		}
	})

	t.Run("off ignores detection", func(t *testing.T) {
		DetectGPU = func() bool { return true }
		if ResolveGPU(GPUOff) {
			t.Error("ResolveGPU(GPUOff) should return false regardless of detection")
		}
	})

	t.Run("auto uses detection true", func(t *testing.T) {
		DetectGPU = func() bool { return true }
		if !ResolveGPU(GPUAuto) {
			t.Error("ResolveGPU(GPUAuto) should return true when DetectGPU returns true")
		}
	})

	t.Run("auto uses detection false", func(t *testing.T) {
		DetectGPU = func() bool { return false }
		if ResolveGPU(GPUAuto) {
			t.Error("ResolveGPU(GPUAuto) should return false when DetectGPU returns false")
		}
	})
}
