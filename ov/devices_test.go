package main

import (
	"reflect"
	"testing"
)

func TestDetectHostDevicesWithGPU(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			GPU:     true,
			Devices: []string{"/dev/kvm", "/dev/dri/renderD128"},
		}
	}

	detected := DetectHostDevices()
	if !detected.GPU {
		t.Error("expected GPU=true")
	}
	want := []string{"/dev/kvm", "/dev/dri/renderD128"}
	if !reflect.DeepEqual(detected.Devices, want) {
		t.Errorf("Devices = %v, want %v", detected.Devices, want)
	}
}

func TestDetectHostDevicesNoGPU(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			GPU:     false,
			Devices: []string{"/dev/fuse"},
		}
	}

	detected := DetectHostDevices()
	if detected.GPU {
		t.Error("expected GPU=false")
	}
	if len(detected.Devices) != 1 || detected.Devices[0] != "/dev/fuse" {
		t.Errorf("Devices = %v, want [/dev/fuse]", detected.Devices)
	}
}

func TestDetectedDevicesMergeIntoSecurity(t *testing.T) {
	detected := DetectedDevices{
		GPU:     false,
		Devices: []string{"/dev/kvm", "/dev/fuse"},
	}

	sec := SecurityConfig{
		Devices: []string{"/dev/fuse"}, // already has /dev/fuse
	}
	sec.Devices = appendUnique(sec.Devices, detected.Devices...)

	want := []string{"/dev/fuse", "/dev/kvm"}
	if !reflect.DeepEqual(sec.Devices, want) {
		t.Errorf("merged Devices = %v, want %v", sec.Devices, want)
	}
}

func TestDetectedDevicesInSecurityArgs(t *testing.T) {
	sec := SecurityConfig{
		Devices: []string{"/dev/kvm", "/dev/fuse"},
	}
	args := SecurityArgs(sec)
	want := []string{
		"--device", "/dev/kvm",
		"--device", "/dev/fuse",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs = %v, want %v", args, want)
	}
}

func TestDetectedDevicesInQuadlet(t *testing.T) {
	cfg := QuadletConfig{
		ImageName:   "test",
		ImageRef:    "test:latest",
		Workspace:   "/workspace",
		GPU:         true,
		BindAddress: "127.0.0.1",
		Security: SecurityConfig{
			Devices: []string{"/dev/kvm", "/dev/fuse"},
		},
	}
	content := generateQuadlet(cfg)
	if !containsLine(content, "AddDevice=nvidia.com/gpu=all") {
		t.Error("expected AddDevice=nvidia.com/gpu=all for GPU")
	}
	if !containsLine(content, "AddDevice=/dev/kvm") {
		t.Error("expected AddDevice=/dev/kvm")
	}
	if !containsLine(content, "AddDevice=/dev/fuse") {
		t.Error("expected AddDevice=/dev/fuse")
	}
}

func TestPrivilegedSkipsDevices(t *testing.T) {
	sec := SecurityConfig{Privileged: true}
	// When privileged, auto-detected devices should not be merged
	// (privileged already grants access to all devices)
	args := SecurityArgs(sec)
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged) = %v, want %v", args, want)
	}
}
