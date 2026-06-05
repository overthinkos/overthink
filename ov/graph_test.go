package main

import (
	"reflect"
	"testing"
)

func TestResolveLayerOrder(t *testing.T) {
	// Create test layers
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Require: nil},
		"python":  {Name: "python", Require: toLayerRefs([]string{"pixi"})},
		"ml-libs": {Name: "ml-libs", Require: toLayerRefs([]string{"python"})},
		"nodejs":  {Name: "nodejs", Require: nil},
		"web-ui":  {Name: "web-ui", Require: toLayerRefs([]string{"nodejs"})},
	}

	tests := []struct {
		name         string
		requested    []string
		parentLayers map[string]bool
		wantOrder    []string
		wantErr      bool
	}{
		{
			name:         "single layer no deps",
			requested:    []string{"pixi"},
			parentLayers: nil,
			wantOrder:    []string{"pixi"},
		},
		{
			name:         "layer with deps",
			requested:    []string{"python"},
			parentLayers: nil,
			wantOrder:    []string{"pixi", "python"},
		},
		{
			name:         "transitive deps",
			requested:    []string{"ml-libs"},
			parentLayers: nil,
			wantOrder:    []string{"pixi", "python", "ml-libs"},
		},
		{
			name:         "multiple independent layers",
			requested:    []string{"pixi", "nodejs"},
			parentLayers: nil,
			wantOrder:    []string{"nodejs", "pixi"}, // sorted alphabetically
		},
		{
			name:         "mixed deps",
			requested:    []string{"ml-libs", "web-ui"},
			parentLayers: nil,
			wantOrder:    []string{"nodejs", "pixi", "python", "ml-libs", "web-ui"},
		},
		{
			name:         "parent provides dep",
			requested:    []string{"python"},
			parentLayers: map[string]bool{"pixi": true},
			wantOrder:    []string{"python"}, // pixi excluded
		},
		{
			name:         "unknown layer",
			requested:    []string{"unknown"},
			parentLayers: nil,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := ResolveLayerOrder(tt.requested, layers, tt.parentLayers)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(order, tt.wantOrder) {
				t.Errorf("order = %v, want %v", order, tt.wantOrder)
			}
		})
	}
}

func TestResolveLayerOrderCycle(t *testing.T) {
	// Create layers with a cycle: a -> b -> c -> a
	layers := map[string]*Layer{
		"a": {Name: "a", Require: toLayerRefs([]string{"b"})},
		"b": {Name: "b", Require: toLayerRefs([]string{"c"})},
		"c": {Name: "c", Require: toLayerRefs([]string{"a"})},
	}

	_, err := ResolveLayerOrder([]string{"a"}, layers, nil)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}

	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Errorf("expected CycleError, got %T", err)
	} else if len(cycleErr.Cycle) == 0 {
		t.Error("CycleError.Cycle is empty")
	}
}

func TestResolveImageOrder(t *testing.T) {
	// Create test images
	images := map[string]*ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"cuda": {
			Name:           "cuda",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
		},
		"inference": {
			Name:           "inference",
			Base:           "ml-cuda",
			IsExternalBase: false,
		},
	}

	order, err := ResolveImageOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveImageOrder() error = %v", err)
	}

	// Check that dependencies come before dependents
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// cuda must come before ml-cuda
	if indexOf("cuda") > indexOf("ml-cuda") {
		t.Errorf("cuda should come before ml-cuda, got order %v", order)
	}

	// ml-cuda must come before inference
	if indexOf("ml-cuda") > indexOf("inference") {
		t.Errorf("ml-cuda should come before inference, got order %v", order)
	}
}

func TestResolveImageOrderWithBuilder(t *testing.T) {
	images := map[string]*ResolvedBox{
		"builder": {
			Name:           "builder",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
			Builder:        BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
			Builder:        BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	order, err := ResolveImageOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveImageOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// builder must come before fedora and app
	if indexOf("builder") > indexOf("fedora") {
		t.Errorf("builder should come before fedora, got order %v", order)
	}
	if indexOf("builder") > indexOf("app") {
		t.Errorf("builder should come before app, got order %v", order)
	}
	// fedora must come before app
	if indexOf("fedora") > indexOf("app") {
		t.Errorf("fedora should come before app, got order %v", order)
	}
}

func TestResolveImageOrderWithBootstrapBuilder(t *testing.T) {
	// Mirrors the cachyos / cachyos-pacstrap-builder pair (relocated to the
	// overthinkos/cachyos submodule's overthink.yml in the 2026-05 CachyOS migration).
	// `cachyos` is built `from: builder:pacstrap` with
	// `bootstrap_builder_image: cachyos-pacstrap-builder`. A downstream
	// image `app` consumes cachyos via `base: cachyos`. Without the
	// bootstrap-builder edge, the topo-sort would schedule cachyos before
	// cachyos-pacstrap-builder and runPrivilegedBootstrap would fail at
	// resolveLocalImageRef (build.go:294).
	images := map[string]*ResolvedBox{
		"arch": {
			Name:           "arch",
			Base:           "docker.io/library/archlinux:latest",
			IsExternalBase: true,
		},
		"cachyos-pacstrap-builder": {
			Name:           "cachyos-pacstrap-builder",
			Base:           "arch",
			IsExternalBase: false,
		},
		"cachyos": {
			Name:                  "cachyos",
			Base:                  "",
			IsExternalBase:        true,
			BootstrapBuilderImage: "cachyos-pacstrap-builder",
		},
		"app": {
			Name:           "app",
			Base:           "cachyos",
			IsExternalBase: false,
		},
	}

	order, err := ResolveImageOrder(images, nil)
	if err != nil {
		t.Fatalf("ResolveImageOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("cachyos-pacstrap-builder") > indexOf("cachyos") {
		t.Errorf("cachyos-pacstrap-builder must come before cachyos (bootstrap_builder_image dep), got order %v", order)
	}
	if indexOf("cachyos") > indexOf("app") {
		t.Errorf("cachyos must come before app (base dep), got order %v", order)
	}
	if indexOf("arch") > indexOf("cachyos-pacstrap-builder") {
		t.Errorf("arch must come before cachyos-pacstrap-builder (base dep), got order %v", order)
	}

	// Same property must hold for ResolveImageLevels (concurrent-build mode).
	levels, err := ResolveImageLevels(images, nil)
	if err != nil {
		t.Fatalf("ResolveImageLevels() error = %v", err)
	}
	levelOf := func(name string) int {
		for i, level := range levels {
			for _, n := range level {
				if n == name {
					return i
				}
			}
		}
		return -1
	}
	if levelOf("cachyos-pacstrap-builder") >= levelOf("cachyos") {
		t.Errorf("cachyos-pacstrap-builder must be in an earlier level than cachyos, got levels %v", levels)
	}
}

func TestResolveImageOrderCycle(t *testing.T) {
	// Create images with a cycle
	images := map[string]*ResolvedBox{
		"a": {Name: "a", Base: "b", IsExternalBase: false},
		"b": {Name: "b", Base: "c", IsExternalBase: false},
		"c": {Name: "c", Base: "a", IsExternalBase: false},
	}

	_, err := ResolveImageOrder(images, nil)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestLayersProvidedByImage(t *testing.T) {
	images := map[string]*ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
			Layer:          []string{"pixi"},
		},
		"cuda": {
			Name:           "cuda",
			Base:           "base",
			IsExternalBase: false,
			Layer:          []string{"cuda"},
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
			Layer:          []string{"python", "ml-libs"},
		},
	}

	layers := map[string]*Layer{} // not used, just for type

	tests := []struct {
		name      string
		imageName string
		want      map[string]bool
	}{
		{
			name:      "base image",
			imageName: "base",
			want:      map[string]bool{"pixi": true},
		},
		{
			name:      "cuda inherits from base",
			imageName: "cuda",
			want:      map[string]bool{"pixi": true, "cuda": true},
		},
		{
			name:      "ml-cuda inherits from cuda",
			imageName: "ml-cuda",
			want:      map[string]bool{"pixi": true, "cuda": true, "python": true, "ml-libs": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LayerProvidedByImage(tt.imageName, images, layers)
			if err != nil {
				t.Fatalf("LayerProvidedByImage() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LayerProvidedByImage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpandLayers(t *testing.T) {
	layers := map[string]*Layer{
		"pipewire":     {Name: "pipewire", tasks: []Task{{Cmd: "true"}}},
		"wayvnc":       {Name: "wayvnc", tasks: []Task{{Cmd: "true"}}},
		"chrome":       {Name: "chrome", tasks: []Task{{Cmd: "true"}}},
		"waybar":       {Name: "waybar", tasks: []Task{{Cmd: "true"}}},
		"sway-desktop": {Name: "sway-desktop", IncludedLayer: toLayerRefs([]string{"pipewire", "wayvnc", "chrome", "waybar"})},
		"openclaw":     {Name: "openclaw", tasks: []Task{{Cmd: "true"}}},
	}

	// Basic expansion
	result, err := ExpandLayer([]string{"openclaw", "sway-desktop"}, layers)
	if err != nil {
		t.Fatalf("ExpandLayer() error: %v", err)
	}
	want := []string{"openclaw", "pipewire", "wayvnc", "chrome", "waybar"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ExpandLayer() = %v, want %v", result, want)
	}
}

func TestExpandLayersDedup(t *testing.T) {
	layers := map[string]*Layer{
		"pipewire":     {Name: "pipewire", tasks: []Task{{Cmd: "true"}}},
		"wayvnc":       {Name: "wayvnc", tasks: []Task{{Cmd: "true"}}},
		"sway-desktop": {Name: "sway-desktop", IncludedLayer: toLayerRefs([]string{"pipewire", "wayvnc"})},
	}

	// pipewire referenced directly AND via sway-desktop — should appear once
	result, err := ExpandLayer([]string{"pipewire", "sway-desktop"}, layers)
	if err != nil {
		t.Fatalf("ExpandLayer() error: %v", err)
	}
	want := []string{"pipewire", "wayvnc"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ExpandLayer() = %v, want %v", result, want)
	}
}

func TestExpandLayersNested(t *testing.T) {
	layers := map[string]*Layer{
		"pipewire":     {Name: "pipewire", tasks: []Task{{Cmd: "true"}}},
		"wayvnc":       {Name: "wayvnc", tasks: []Task{{Cmd: "true"}}},
		"chrome":       {Name: "chrome", tasks: []Task{{Cmd: "true"}}},
		"vnc-stack":    {Name: "vnc-stack", IncludedLayer: toLayerRefs([]string{"pipewire", "wayvnc"})},
		"browser-desk": {Name: "browser-desk", IncludedLayer: toLayerRefs([]string{"vnc-stack", "chrome"})},
	}

	result, err := ExpandLayer([]string{"browser-desk"}, layers)
	if err != nil {
		t.Fatalf("ExpandLayer() error: %v", err)
	}
	want := []string{"pipewire", "wayvnc", "chrome"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ExpandLayer() = %v, want %v", result, want)
	}
}

func TestExpandLayersCycle(t *testing.T) {
	layers := map[string]*Layer{
		"a": {Name: "a", IncludedLayer: toLayerRefs([]string{"b"})},
		"b": {Name: "b", IncludedLayer: toLayerRefs([]string{"a"})},
	}

	_, err := ExpandLayer([]string{"a"}, layers)
	if err == nil {
		t.Error("expected circular composition error, got nil")
	}
}

func TestExpandLayersWithContent(t *testing.T) {
	layers := map[string]*Layer{
		"pipewire": {Name: "pipewire", tasks: []Task{{Cmd: "true"}}},
		"wayvnc":   {Name: "wayvnc", tasks: []Task{{Cmd: "true"}}},
		// Composing layer that also has its own install content
		"desktop": {Name: "desktop", tasks: []Task{{Cmd: "true"}}, IncludedLayer: toLayerRefs([]string{"pipewire", "wayvnc"})},
	}

	result, err := ExpandLayer([]string{"desktop"}, layers)
	if err != nil {
		t.Fatalf("ExpandLayer() error: %v", err)
	}
	// desktop should stay because it has content
	want := []string{"pipewire", "wayvnc", "desktop"}
	if !reflect.DeepEqual(result, want) {
		t.Errorf("ExpandLayer() = %v, want %v", result, want)
	}
}

func TestResolveLayerOrderWithComposition(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":        {Name: "pixi", tasks: []Task{{Cmd: "true"}}},
		"python":      {Name: "python", tasks: []Task{{Cmd: "true"}}, Require: toLayerRefs([]string{"pixi"})},
		"supervisord": {Name: "supervisord", tasks: []Task{{Cmd: "true"}}, Require: toLayerRefs([]string{"python"})},
		"svc-stack":   {Name: "svc-stack", IncludedLayer: toLayerRefs([]string{"python", "supervisord"})},
	}

	order, err := ResolveLayerOrder([]string{"svc-stack"}, layers, nil)
	if err != nil {
		t.Fatalf("ResolveLayerOrder() error: %v", err)
	}
	// pixi (dep of python) → python → supervisord
	want := []string{"pixi", "python", "supervisord"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestDependsOnComposingLayer(t *testing.T) {
	layers := map[string]*Layer{
		"pipewire":     {Name: "pipewire", tasks: []Task{{Cmd: "true"}}},
		"wayvnc":       {Name: "wayvnc", tasks: []Task{{Cmd: "true"}}},
		"sway-desktop": {Name: "sway-desktop", IncludedLayer: toLayerRefs([]string{"pipewire", "wayvnc"})},
		"myapp":        {Name: "myapp", tasks: []Task{{Cmd: "true"}}, Require: toLayerRefs([]string{"sway-desktop"})},
	}

	order, err := ResolveLayerOrder([]string{"myapp"}, layers, nil)
	if err != nil {
		t.Fatalf("ResolveLayerOrder() error: %v", err)
	}
	// pipewire and wayvnc should be pulled in via sway-desktop dependency
	want := []string{"pipewire", "wayvnc", "myapp"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestTopoLevels(t *testing.T) {
	tests := []struct {
		name    string
		graph   map[string][]string
		want    [][]string
		wantErr bool
	}{
		{
			name: "linear chain",
			graph: map[string][]string{
				"a": nil,
				"b": {"a"},
				"c": {"b"},
			},
			want: [][]string{{"a"}, {"b"}, {"c"}},
		},
		{
			name: "two independent roots",
			graph: map[string][]string{
				"a": nil,
				"b": nil,
				"c": {"a"},
				"d": {"b"},
			},
			want: [][]string{{"a", "b"}, {"c", "d"}},
		},
		{
			name: "diamond dependency",
			graph: map[string][]string{
				"a": nil,
				"b": {"a"},
				"c": {"a"},
				"d": {"b", "c"},
			},
			want: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name: "single node",
			graph: map[string][]string{
				"a": nil,
			},
			want: [][]string{{"a"}},
		},
		{
			name: "cycle",
			graph: map[string][]string{
				"a": {"b"},
				"b": {"a"},
			},
			wantErr: true,
		},
		{
			name: "wide first level",
			graph: map[string][]string{
				"a": nil,
				"b": nil,
				"c": nil,
				"d": {"a", "b"},
				"e": {"c"},
			},
			want: [][]string{{"a", "b", "c"}, {"d", "e"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			levels, err := topoLevels(tt.graph)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(levels, tt.want) {
				t.Errorf("topoLevels() = %v, want %v", levels, tt.want)
			}
		})
	}
}

func TestResolveImageLevels(t *testing.T) {
	images := map[string]*ResolvedBox{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"cuda": {
			Name:           "cuda",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"app": {
			Name:           "app",
			Base:           "base",
			IsExternalBase: false,
		},
		"ml": {
			Name:           "ml",
			Base:           "cuda",
			IsExternalBase: false,
		},
		"inference": {
			Name:           "inference",
			Base:           "ml",
			IsExternalBase: false,
		},
	}

	levels, err := ResolveImageLevels(images, nil)
	if err != nil {
		t.Fatalf("ResolveImageLevels() error = %v", err)
	}

	// Level 0: base, cuda (no deps)
	// Level 1: app (depends on base), ml (depends on cuda)
	// Level 2: inference (depends on ml)
	if len(levels) != 3 {
		t.Fatalf("expected 3 levels, got %d: %v", len(levels), levels)
	}
	if !reflect.DeepEqual(levels[0], []string{"base", "cuda"}) {
		t.Errorf("level 0 = %v, want [base cuda]", levels[0])
	}
	if !reflect.DeepEqual(levels[1], []string{"app", "ml"}) {
		t.Errorf("level 1 = %v, want [app ml]", levels[1])
	}
	if !reflect.DeepEqual(levels[2], []string{"inference"}) {
		t.Errorf("level 2 = %v, want [inference]", levels[2])
	}
}

func TestTopoSortDeterministic(t *testing.T) {
	// Run multiple times to ensure deterministic output
	graph := map[string][]string{
		"a": nil,
		"b": nil,
		"c": {"a"},
		"d": {"a", "b"},
	}

	first, err := topoSort(graph)
	if err != nil {
		t.Fatalf("topoSort() error = %v", err)
	}

	for i := 0; i < 10; i++ {
		result, err := topoSort(graph)
		if err != nil {
			t.Fatalf("topoSort() error = %v", err)
		}
		if !reflect.DeepEqual(result, first) {
			t.Errorf("non-deterministic output: got %v, first was %v", result, first)
		}
	}
}
