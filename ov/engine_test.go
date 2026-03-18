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

func TestResolveImageEngine(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		layers        map[string]*Layer
		imageName     string
		globalEngine  string
		wantEngine    string
	}{
		{
			name: "image-level override",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"myimg": {Engine: "docker", Layers: []string{"a"}},
				},
			},
			layers:       map[string]*Layer{"a": {Name: "a"}},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "docker",
		},
		{
			name: "defaults-level engine",
			cfg: &Config{
				Defaults: ImageConfig{Engine: "docker"},
				Images: map[string]ImageConfig{
					"myimg": {Layers: []string{"a"}},
				},
			},
			layers:       map[string]*Layer{"a": {Name: "a"}},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "docker",
		},
		{
			name: "layer-level requirement",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"myimg": {Layers: []string{"dockerlayer"}},
				},
			},
			layers: map[string]*Layer{
				"dockerlayer": {Name: "dockerlayer", engine: "docker"},
			},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "docker",
		},
		{
			name: "transitive layer requirement",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"myimg": {Layers: []string{"top"}},
				},
			},
			layers: map[string]*Layer{
				"top":     {Name: "top", Depends: []string{"bottom"}},
				"bottom":  {Name: "bottom", engine: "docker"},
			},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "docker",
		},
		{
			name: "no override uses global",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"myimg": {Layers: []string{"a"}},
				},
			},
			layers:       map[string]*Layer{"a": {Name: "a"}},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "podman",
		},
		{
			name: "image override beats layer requirement",
			cfg: &Config{
				Images: map[string]ImageConfig{
					"myimg": {Engine: "podman", Layers: []string{"dockerlayer"}},
				},
			},
			layers: map[string]*Layer{
				"dockerlayer": {Name: "dockerlayer", engine: "docker"},
			},
			imageName:    "myimg",
			globalEngine: "podman",
			wantEngine:   "podman",
		},
		{
			name:         "unknown image uses global",
			cfg:          &Config{Images: map[string]ImageConfig{}},
			layers:       map[string]*Layer{},
			imageName:    "nonexistent",
			globalEngine: "docker",
			wantEngine:   "docker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveImageEngine(tt.cfg, tt.layers, tt.imageName, tt.globalEngine)
			if got != tt.wantEngine {
				t.Errorf("ResolveImageEngine() = %q, want %q", got, tt.wantEngine)
			}
		})
	}
}

func TestImageRuntime(t *testing.T) {
	rt := &ResolvedRuntime{BuildEngine: "docker", RunEngine: "podman"}

	// No change when imageEngine matches
	same := ImageRuntime(rt, "podman")
	if same != rt {
		t.Error("ImageRuntime should return same pointer when engine matches")
	}

	// No change when imageEngine is empty
	empty := ImageRuntime(rt, "")
	if empty != rt {
		t.Error("ImageRuntime should return same pointer when engine is empty")
	}

	// New runtime when engine differs
	diff := ImageRuntime(rt, "docker")
	if diff == rt {
		t.Error("ImageRuntime should return new runtime when engine differs")
	}
	if diff.RunEngine != "docker" {
		t.Errorf("ImageRuntime() RunEngine = %q, want %q", diff.RunEngine, "docker")
	}
	// Original should be unchanged
	if rt.RunEngine != "podman" {
		t.Errorf("Original runtime was mutated: RunEngine = %q, want %q", rt.RunEngine, "podman")
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
