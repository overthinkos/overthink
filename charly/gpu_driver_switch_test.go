package main

import (
	"strings"
	"testing"
)

func TestGpuModeFromDriver(t *testing.T) {
	cases := map[string]string{
		"nvidia":   gpuModeNvidia,
		"vfio-pci": gpuModeVfio,
		"":         gpuModeVfio, // unbound is the vfio/default side
		"nouveau":  gpuModeVfio,
	}
	for driver, want := range cases {
		if got := gpuModeFromDriver(driver); got != want {
			t.Errorf("gpuModeFromDriver(%q) = %q, want %q", driver, got, want)
		}
	}
}

func TestCurrentGPUMode(t *testing.T) {
	orig := gpuDisplayDriver
	defer func() { gpuDisplayDriver = orig }()
	gpuDisplayDriver = func(addr string) string { return "nvidia" }
	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if got := currentGPUMode(gpu); got != gpuModeNvidia {
		t.Errorf("currentGPUMode = %q, want nvidia", got)
	}
}

func TestSwitchGPUDriverMode_IdempotentNoOp(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(addr string) string { return "vfio-pci" } // already vfio
	called := false
	runGPUSwitchScript = func(script string) ([]byte, error) { called = true; return nil, nil }

	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if err := switchGPUDriverMode(gpu, gpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if called {
		t.Fatal("idempotent no-op must NOT invoke the sudo sysfs script when already in mode")
	}
}

func TestSwitchGPUDriverMode_ToNvidiaScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(addr string) string { return "vfio-pci" } // flip needed
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if err := switchGPUDriverMode(gpu, gpuModeNvidia); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// The proven recipe: force nvidia via driver_override + nvidia-modprobe + bind.
	for _, want := range []string{"driver_override", "nvidia", "nvidia-modprobe", "0000:01:00.0"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-nvidia script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_ToVfioScript(t *testing.T) {
	origDrv, origRun := gpuDisplayDriver, runGPUSwitchScript
	defer func() { gpuDisplayDriver, runGPUSwitchScript = origDrv, origRun }()

	gpuDisplayDriver = func(addr string) string { return "nvidia" } // flip needed
	var gotScript string
	runGPUSwitchScript = func(script string) ([]byte, error) { gotScript = script; return nil, nil }

	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if err := switchGPUDriverMode(gpu, gpuModeVfio); err != nil {
		t.Fatalf("switch: %v", err)
	}
	// The proven recipe: modprobe vfio-pci (it may have deregistered) + bind.
	for _, want := range []string{"modprobe vfio-pci", "driver_override", "vfio-pci/bind"} {
		if !strings.Contains(gotScript, want) {
			t.Errorf("to-vfio script missing %q:\n%s", want, gotScript)
		}
	}
}

func TestSwitchGPUDriverMode_UnknownMode(t *testing.T) {
	origDrv := gpuDisplayDriver
	defer func() { gpuDisplayDriver = origDrv }()
	gpuDisplayDriver = func(addr string) string { return "vfio-pci" }
	gpu := VFIOGpu{VFIOPCIDevice: VFIOPCIDevice{Addr: "0000:01:00.0"}}
	if err := switchGPUDriverMode(gpu, "bogus"); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestGpuSwitchModeTolerant_AbsentCardIsNoError(t *testing.T) {
	origDetect, origRun := DetectVFIO, runGPUSwitchScript
	defer func() { DetectVFIO, runGPUSwitchScript = origDetect, origRun }()

	DetectVFIO = func() VFIOReport { return VFIOReport{} } // no GPUs
	called := false
	runGPUSwitchScript = func(script string) ([]byte, error) { called = true; return nil, nil }

	// Tolerant: a missing card is NOT an error (keeps requires_exclusive beds
	// portable on no-GPU hosts) and never invokes the sysfs script.
	if err := gpuSwitchModeTolerant("0x10de", gpuModeVfio); err != nil {
		t.Fatalf("absent card must be tolerated, got %v", err)
	}
	if called {
		t.Fatal("must not run the switch script when no matching card exists")
	}
}

func TestDeployNodeSharesGPU(t *testing.T) {
	resources := map[string]*ResourceDef{
		"nvidia-gpu": {Gpu: &GpuSelector{Vendor: "0x10de"}},
		"abstract":   {}, // no gpu selector
	}
	cases := []struct {
		name string
		node DeploymentNode
		want bool
	}{
		{"gpu-backed shared token", DeploymentNode{RequiresShared: []string{"nvidia-gpu"}}, true},
		{"selector-less shared token", DeploymentNode{RequiresShared: []string{"abstract"}}, false},
		{"no shared claim", DeploymentNode{}, false},
		{"exclusive is not shared", DeploymentNode{RequiresExclusive: []string{"nvidia-gpu"}}, false},
	}
	for _, tc := range cases {
		if got := deployNodeSharesGPU(tc.node, resources); got != tc.want {
			t.Errorf("%s: deployNodeSharesGPU = %v, want %v", tc.name, got, tc.want)
		}
	}
}
