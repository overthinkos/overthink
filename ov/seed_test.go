package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsDirEmpty(t *testing.T) {
	// Non-existent directory
	if !isDirEmpty("/nonexistent/path") {
		t.Error("expected true for nonexistent path")
	}

	// Empty directory
	emptyDir := t.TempDir()
	if !isDirEmpty(emptyDir) {
		t.Error("expected true for empty directory")
	}

	// Directory with a file
	nonEmptyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "test.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if isDirEmpty(nonEmptyDir) {
		t.Error("expected false for non-empty directory")
	}

	// Directory with a subdirectory
	dirWithSubdir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirWithSubdir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if isDirEmpty(dirWithSubdir) {
		t.Error("expected false for directory with subdirectory")
	}
}

func TestCollectLayerVolumePaths(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"base": {Layers: []string{"store"}},
			"child": {Base: "base", Layers: []string{"app"}},
		},
	}
	layers := map[string]*Layer{
		"store": {
			Name:       "store",
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "models", Path: "~/.models"}},
		},
		"app": {
			Name:       "app",
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.app"}},
		},
	}

	result := collectLayerVolumePaths(cfg, layers, "child", "/home/user")

	if len(result) != 2 {
		t.Fatalf("expected 2 volume paths, got %d: %v", len(result), result)
	}
	if result["data"] != "/home/user/.app" {
		t.Errorf("data = %q, want /home/user/.app", result["data"])
	}
	if result["models"] != "/home/user/.models" {
		t.Errorf("models = %q, want /home/user/.models", result["models"])
	}
}

func TestCollectLayerVolumePathsDedup(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"base":  {Layers: []string{"store"}},
			"child": {Base: "base", Layers: []string{"override"}},
		},
	}
	layers := map[string]*Layer{
		"store": {
			Name:       "store",
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.base-data"}},
		},
		"override": {
			Name:       "override",
			HasVolumes: true,
			volumes:    []VolumeYAML{{Name: "data", Path: "~/.child-data"}},
		},
	}

	result := collectLayerVolumePaths(cfg, layers, "child", "/home/user")

	if len(result) != 1 {
		t.Fatalf("expected 1 volume path (dedup), got %d: %v", len(result), result)
	}
	// First declaration wins (outermost image's layers processed first)
	if result["data"] != "/home/user/.child-data" {
		t.Errorf("data = %q, want /home/user/.child-data", result["data"])
	}
}

func TestCollectLayerVolumePathsNoVolumes(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"plain": {Layers: []string{"svc"}},
		},
	}
	layers := map[string]*Layer{
		"svc": {Name: "svc"},
	}

	result := collectLayerVolumePaths(cfg, layers, "plain", "/home/user")

	if len(result) != 0 {
		t.Errorf("expected 0 volume paths, got %d: %v", len(result), result)
	}
}

func TestSeedSummary(t *testing.T) {
	volPaths := map[string]string{
		"data":   "/home/user/.myapp",
		"models": "/home/user/.models",
	}
	bindMounts := []ResolvedBindMount{
		{Name: "data", HostPath: "/tmp/empty", ContPath: "/home/user/.myapp"},
		{Name: "other", HostPath: "/tmp/other", ContPath: "/home/user/.other"},
	}

	summary := seedSummary(volPaths, bindMounts)

	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(summary, "data") {
		t.Errorf("expected summary to mention 'data', got: %s", summary)
	}
}
