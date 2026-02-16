package main

import (
	"reflect"
	"testing"
)

func TestCollectImageVolumesSimple(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"myapp": {Layers: []string{"svc"}},
		},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:       "svc",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.myapp"}},
		},
	}

	mounts, err := CollectImageVolumes(cfg, layers, "myapp", "/home/user")
	if err != nil {
		t.Fatalf("CollectImageVolumes() error = %v", err)
	}

	want := []VolumeMount{
		{VolumeName: "ov-myapp-data", ContainerPath: "/home/user/.myapp"},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("CollectImageVolumes() =\n  %v\nwant\n  %v", mounts, want)
	}
}

func TestCollectImageVolumesChain(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"base": {Layers: []string{"store"}},
			"child": {Base: "base", Layers: []string{"app"}},
		},
	}
	layers := map[string]*Layer{
		"store": {
			Name:       "store",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "models", Path: "~/.models"}},
		},
		"app": {
			Name:       "app",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.app"}},
		},
	}

	mounts, err := CollectImageVolumes(cfg, layers, "child", "/home/user")
	if err != nil {
		t.Fatalf("CollectImageVolumes() error = %v", err)
	}

	// Should have volumes from both child and base image layers
	want := []VolumeMount{
		{VolumeName: "ov-child-data", ContainerPath: "/home/user/.app"},
		{VolumeName: "ov-child-models", ContainerPath: "/home/user/.models"},
	}
	if !reflect.DeepEqual(mounts, want) {
		t.Errorf("CollectImageVolumes() =\n  %v\nwant\n  %v", mounts, want)
	}
}

func TestCollectImageVolumesDedup(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"base":  {Layers: []string{"store"}},
			"child": {Base: "base", Layers: []string{"override"}},
		},
	}
	layers := map[string]*Layer{
		"store": {
			Name:       "store",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.base-data"}},
		},
		"override": {
			Name:       "override",
			HasUserYml: true,
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.child-data"}},
		},
	}

	mounts, err := CollectImageVolumes(cfg, layers, "child", "/home/user")
	if err != nil {
		t.Fatalf("CollectImageVolumes() error = %v", err)
	}

	// First declaration wins (outermost image)
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d: %v", len(mounts), mounts)
	}
	if mounts[0].ContainerPath != "/home/user/.child-data" {
		t.Errorf("expected child override to win, got path %q", mounts[0].ContainerPath)
	}
}

func TestCollectImageVolumesNoVolumes(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"base": {Layers: []string{"plain"}},
		},
	}
	layers := map[string]*Layer{
		"plain": {Name: "plain", HasUserYml: true},
	}

	mounts, err := CollectImageVolumes(cfg, layers, "base", "/home/user")
	if err != nil {
		t.Fatalf("CollectImageVolumes() error = %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts, got %v", mounts)
	}
}

func TestExpandHome(t *testing.T) {
	tests := []struct {
		path string
		home string
		want string
	}{
		{"~/.openclaw", "/home/user", "/home/user/.openclaw"},
		{"~", "/home/user", "/home/user"},
		{"$HOME/.config", "/home/user", "/home/user/.config"},
		{"/absolute/path", "/home/user", "/absolute/path"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := expandHome(tt.path, tt.home)
			if got != tt.want {
				t.Errorf("expandHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestSortVolumeMounts(t *testing.T) {
	mounts := []VolumeMount{
		{VolumeName: "ov-app-z", ContainerPath: "/z"},
		{VolumeName: "ov-app-a", ContainerPath: "/a"},
		{VolumeName: "ov-app-m", ContainerPath: "/m"},
	}
	sortVolumeMounts(mounts)
	if mounts[0].VolumeName != "ov-app-a" || mounts[1].VolumeName != "ov-app-m" || mounts[2].VolumeName != "ov-app-z" {
		t.Errorf("sortVolumeMounts() result: %v", mounts)
	}
}
