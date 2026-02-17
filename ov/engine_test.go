package main

import (
	"reflect"
	"testing"
)

func TestEngineBinary(t *testing.T) {
	tests := []struct {
		engine string
		want   string
	}{
		{"docker", "docker"},
		{"podman", "podman"},
		{"", "docker"},
	}
	for _, tt := range tests {
		got := EngineBinary(tt.engine)
		if got != tt.want {
			t.Errorf("EngineBinary(%q) = %q, want %q", tt.engine, got, tt.want)
		}
	}
}

func TestGPURunArgs(t *testing.T) {
	tests := []struct {
		engine string
		want   []string
	}{
		{"docker", []string{"--gpus", "all"}},
		{"podman", []string{"--device", "nvidia.com/gpu=all"}},
	}
	for _, tt := range tests {
		got := GPURunArgs(tt.engine)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("GPURunArgs(%q) = %v, want %v", tt.engine, got, tt.want)
		}
	}
}
