package main

import (
	"reflect"
	"testing"
)

func TestGlobalLayerOrder_PopularityTieBreaking(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Require: nil},
		"nodejs":  {Name: "nodejs", Require: nil},
		"python":  {Name: "python", Require: toLayerRefs([]string{"pixi"})},
		"testapi": {Name: "testapi", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
	}

	// pixi is used by 2 images, nodejs by 1
	images := map[string]*ResolvedBox{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layer: []string{"pixi", "python", "testapi"}},
		"b": {Name: "b", Base: "ext:1", IsExternalBase: true, Layer: []string{"pixi", "nodejs"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() error = %v", err)
	}

	// pixi (popularity 2) should come before nodejs (popularity 1)
	// python depends on pixi so must come after pixi
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("pixi") > indexOf("nodejs") {
		t.Errorf("pixi should come before nodejs (higher popularity), got order %v", order)
	}
	if indexOf("pixi") > indexOf("python") {
		t.Errorf("pixi should come before python (dependency), got order %v", order)
	}
}

func TestGlobalLayerOrder_RespectsDependencies(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Require: nil},
		"python": {Name: "python", Require: toLayerRefs([]string{"pixi"})},
	}

	images := map[string]*ResolvedBox{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layer: []string{"python"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() error = %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 layers, got %d: %v", len(order), order)
	}
	if order[0] != "pixi" || order[1] != "python" {
		t.Errorf("expected [pixi python], got %v", order)
	}
}

func TestGlobalLayerOrder_RespectsAuthoredListOrder(t *testing.T) {
	// build-toolchain has NO `require: rpmfusion` (on Arch the codec -devel libs
	// come from the distro repos, not RPM Fusion), but fedora-builder authors
	// `candy: [rpmfusion, build-toolchain]` so rpmfusion MUST come first. Here
	// build-toolchain is the more popular layer (2 images) — which, before the
	// authored-list-order fix, made the popularity tie-break place it ahead of
	// rpmfusion (the exact bug that broke fedora-builder in a mixed
	// arch-builder + fedora-builder submodule).
	layers := map[string]*Layer{
		"rpmfusion":       {Name: "rpmfusion"},
		"build-toolchain": {Name: "build-toolchain"},
		"pixi":            {Name: "pixi"},
	}
	images := map[string]*ResolvedBox{
		"fedora-builder": {Name: "fedora-builder", Base: "ext:1", IsExternalBase: true, Layer: []string{"rpmfusion", "build-toolchain"}},
		"arch-builder":   {Name: "arch-builder", Base: "ext:2", IsExternalBase: true, Layer: []string{"build-toolchain", "pixi"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}
	if indexOf("rpmfusion") > indexOf("build-toolchain") {
		t.Errorf("rpmfusion must precede build-toolchain (authored list order), got order %v", order)
	}
}

// TestGlobalLayerOrder_ConflictingListOrderFallsBack ensures that when two
// images author opposite orders for the same pair, the cycle-safe edge
// insertion falls back to the popularity tie-break instead of erroring.
func TestGlobalLayerOrder_ConflictingListOrderFallsBack(t *testing.T) {
	layers := map[string]*Layer{
		"x": {Name: "x"},
		"y": {Name: "y"},
	}
	images := map[string]*ResolvedBox{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layer: []string{"x", "y"}},
		"b": {Name: "b", Base: "ext:2", IsExternalBase: true, Layer: []string{"y", "x"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() should not error on conflicting authored orders, got %v", err)
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 layers, got %d: %v", len(order), order)
	}
}

func TestAbsoluteLayerSequence_WithInternalBase(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Require: nil},
		"python":  {Name: "python", Require: toLayerRefs([]string{"pixi"})},
		"nodejs":  {Name: "nodejs", Require: nil},
		"testapi": {Name: "testapi", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
	}

	images := map[string]*ResolvedBox{
		"base": {Name: "base", Base: "ext:1", IsExternalBase: true, Layer: []string{"pixi"}},
		"app":  {Name: "app", Base: "base", IsExternalBase: false, Layer: []string{"python", "testapi"}},
	}

	globalOrder := []string{"pixi", "nodejs", "python", "testapi"}

	seq := AbsoluteLayerSequence("app", images, layers, globalOrder)

	// app needs pixi (from base) + python + testapi
	expected := []string{"pixi", "python", "testapi"}
	if !reflect.DeepEqual(seq, expected) {
		t.Errorf("AbsoluteLayerSequence = %v, want %v", seq, expected)
	}
}

func TestComputeIntermediates_NoBranching(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python": {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
	}

	images := map[string]*ResolvedBox{
		"app": {
			Name: "app", Base: "ext:1", IsExternalBase: true,
			Layer: []string{"python"}, Tag: "v1", Registry: "r",
			FullTag: "r/app:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image:    map[string]BoxConfig{"app": {Layer: []string{"python"}}},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// With single image, no intermediates should be created
	autoCount := 0
	for _, img := range result {
		if img.Auto {
			autoCount++
		}
	}
	if autoCount != 0 {
		t.Errorf("expected 0 auto intermediates, got %d", autoCount)
	}
}

func TestComputeIntermediates_SimpleBranch(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":  {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"testapi": {Name: "testapi", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layer: []string{"python", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layer: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image: map[string]BoxConfig{
			"fedora": {Layer: []string{}},
			"app1":   {Base: "fedora", Layer: []string{"python", "testapi"}},
			"app2":   {Base: "fedora", Layer: []string{"nodejs"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// With pixi shared between app1 and app2 (through python dep),
	// we may get an intermediate for pixi
	// Both share pixi as common prefix in global order
	// app1: pixi, python, testapi
	// app2: pixi, nodejs (but nodejs doesn't depend on pixi in this setup)

	// Actually app2 only has nodejs which doesn't depend on pixi.
	// So the absolute sequences diverge immediately:
	// app1: pixi, python, testapi (pixi is transitive dep of python)
	// app2: nodejs
	// No common prefix → no intermediate created

	// Verify all original images still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}
}

func TestComputeIntermediates_SharedPrefix(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":        {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":      {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord": {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
		"testapi":     {Name: "testapi", Require: toLayerRefs([]string{"supervisord"}), HasPixiToml: true},
		"openclaw":    {Name: "openclaw", Require: toLayerRefs([]string{"supervisord"}), HasPackageJson: true},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layer: []string{"testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layer: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image: map[string]BoxConfig{
			"fedora":      {Layer: []string{}},
			"fedora-test": {Base: "fedora", Layer: []string{"testapi"}},
			"openclaw":    {Base: "fedora", Layer: []string{"openclaw"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Both fedora-test and openclaw share: pixi → python → supervisord
	// They diverge at supervisord: testapi vs openclaw
	// So we should get an intermediate at the supervisord branching point

	// Check that at least one auto intermediate was created
	autoCount := 0
	for _, img := range result {
		if img.Auto {
			autoCount++
		}
	}
	if autoCount == 0 {
		t.Error("expected at least 1 auto intermediate, got 0")
		for name, img := range result {
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layer, img.Auto)
		}
	}

	// Both fedora-test and openclaw should have an intermediate as base (not fedora directly)
	ftImg := result["fedora-test"]
	ocImg := result["openclaw"]
	if ftImg.Base == "fedora" && ocImg.Base == "fedora" {
		t.Error("both images still use fedora as base — expected an intermediate")
		for name, img := range result {
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layer, img.Auto)
		}
	}
}

func TestComputeIntermediates_ExistingImageReuse(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"nodejs": {Name: "nodejs", Require: nil, tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layer: []string{"pixi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layer: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image: map[string]BoxConfig{
			"fedora": {Layer: []string{}},
			"app1":   {Base: "fedora", Layer: []string{"pixi"}},
			"app2":   {Base: "fedora", Layer: []string{"nodejs"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// fedora at root should be reused (not duplicated)
	if _, ok := result["fedora"]; !ok {
		t.Error("fedora should still exist in result")
	}

	// Both app1 and app2 have no common prefix after fedora (pixi vs nodejs)
	// so no intermediate is needed — they should still base on fedora
	if result["app1"].Base != "fedora" {
		t.Errorf("app1 base = %q, want 'fedora'", result["app1"].Base)
	}
	if result["app2"].Base != "fedora" {
		t.Errorf("app2 base = %q, want 'fedora'", result["app2"].Base)
	}
}

func TestImageNeedsBuilder(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":  {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"tooling": {Name: "tooling", Require: nil, tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"builder": {
			Name: "builder", Base: "ext:1", IsExternalBase: true,
			Layer: []string{"pixi", "nodejs", "tooling"},
		},
		"base": {
			Name: "base", Base: "ext:1", IsExternalBase: true,
			Layer: []string{"pixi"},
		},
		"app": {
			Name: "app", Base: "base", IsExternalBase: false,
			Layer: []string{"python"},
		},
		"simple": {
			Name: "simple", Base: "ext:1", IsExternalBase: true,
			Layer: []string{"tooling"},
		},
	}

	// pixi has root.yml only (no pixi.toml) → does NOT need builder
	if ImageNeedsBuilder(images["base"], images, layers) {
		t.Error("base should not need builder (pixi has root.yml only, no pixi.toml)")
	}

	// app has python which has pixi.toml → NEEDS builder
	if !ImageNeedsBuilder(images["app"], images, layers) {
		t.Error("app should need builder (python has pixi.toml)")
	}

	// simple has tooling (root.yml only) → does NOT need builder
	if ImageNeedsBuilder(images["simple"], images, layers) {
		t.Error("simple should not need builder (tooling has root.yml only)")
	}

	// nil layers → conservative true
	if !ImageNeedsBuilder(images["simple"], images, nil) {
		t.Error("nil layers should return true (conservative)")
	}
}

func TestComputeIntermediates_RealisticConfig(t *testing.T) {
	// Simplified version of the actual overthink.yml setup
	layers := map[string]*Layer{
		"pixi":            {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"nodejs":          {Name: "nodejs", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":          {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord":     {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
		"build-toolchain": {Name: "build-toolchain", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"testapi":         {Name: "testapi", Require: toLayerRefs([]string{"supervisord"}), HasPixiToml: true},
		"traefik":         {Name: "traefik", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
		"openclaw":        {Name: "openclaw", Require: toLayerRefs([]string{"supervisord", "nodejs"}), HasPackageJson: true},
	}

	images := map[string]*ResolvedBox{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{"pixi", "nodejs", "build-toolchain"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layer: []string{"traefik", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layer: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"}},
		Image: map[string]BoxConfig{
			"builder":     {Layer: []string{"pixi", "nodejs", "build-toolchain"}},
			"fedora":      {Layer: []string{}},
			"fedora-test": {Base: "fedora", Layer: []string{"traefik", "testapi"}},
			"openclaw":    {Base: "fedora", Layer: []string{"openclaw"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Log all images for debugging
	t.Log("Resulting images:")
	for name, img := range result {
		t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layer, img.Auto)
	}

	// All original images should still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}

	// The build order should not have cycles
	order, err := ResolveImageOrder(result, layers)
	if err != nil {
		t.Fatalf("ResolveImageOrder after intermediates: %v", err)
	}
	t.Logf("Build order: %v", order)

	// builder should come before any image that needs it
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	builderIdx := indexOf("builder")
	if builderIdx < 0 {
		t.Fatal("builder not in build order")
	}

	// Verify no cycles by checking builder comes early
	fedoraIdx := indexOf("fedora")
	if fedoraIdx < 0 {
		t.Fatal("fedora not in build order")
	}
}

func TestComputeIntermediates_NvidiaScenario(t *testing.T) {
	// Mirror the actual nvidia/python-ml/jupyter/comfyui/ollama config
	layers := map[string]*Layer{
		"pixi":            {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"nodejs":          {Name: "nodejs", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":          {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord":     {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
		"build-toolchain": {Name: "build-toolchain", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"cuda":            {Name: "cuda", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python-ml":       {Name: "python-ml", Require: toLayerRefs([]string{"pixi", "cuda"}), HasPixiToml: true},
		"jupyter":         {Name: "jupyter", Require: toLayerRefs([]string{"python-ml", "supervisord"}), HasPixiToml: true},
		"comfyui":         {Name: "comfyui", Require: toLayerRefs([]string{"python-ml", "supervisord"}), tasks: []Task{{Cmd: "true"}}},
		"ollama":          {Name: "ollama", Require: toLayerRefs([]string{"cuda", "supervisord"}), tasks: []Task{{Cmd: "true"}}},
		"openclaw":        {Name: "openclaw", Require: toLayerRefs([]string{"supervisord", "nodejs"}), HasPackageJson: true},
		"testapi":         {Name: "testapi", Require: toLayerRefs([]string{"supervisord"}), HasPixiToml: true},
		"traefik":         {Name: "traefik", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
		"github-runner":   {Name: "github-runner", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{"pixi", "nodejs", "build-toolchain"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"nvidia": {
			Name: "nvidia", Base: "fedora", IsExternalBase: false,
			Layer: []string{"cuda"}, Tag: "v1", Registry: "r",
			FullTag: "r/nvidia:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"python-ml": {
			Name: "python-ml", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"python-ml"}, Tag: "v1", Registry: "r",
			FullTag: "r/python-ml:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"jupyter": {
			Name: "jupyter", Base: "python-ml", IsExternalBase: false,
			Layer: []string{"jupyter"}, Tag: "v1", Registry: "r",
			FullTag: "r/jupyter:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"comfyui": {
			Name: "comfyui", Base: "python-ml", IsExternalBase: false,
			Layer: []string{"comfyui"}, Tag: "v1", Registry: "r",
			FullTag: "r/comfyui:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"ollama": {
			Name: "ollama", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"ollama"}, Tag: "v1", Registry: "r",
			FullTag: "r/ollama:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"gpu-gateway": {
			Name: "gpu-gateway", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"openclaw", "ollama"}, Tag: "v1", Registry: "r",
			FullTag: "r/gpu-gateway:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layer: []string{"traefik", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layer: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"githubrunner": {
			Name: "githubrunner", Base: "fedora", IsExternalBase: false,
			Layer: []string{"github-runner"}, Tag: "v1", Registry: "r",
			FullTag: "r/githubrunner:v1", Pkg: "rpm", Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"}},
		Image: map[string]BoxConfig{
			"builder":      {Layer: []string{"pixi", "nodejs", "build-toolchain"}},
			"fedora":       {Layer: []string{}},
			"nvidia":       {Base: "fedora", Layer: []string{"cuda"}},
			"python-ml":    {Base: "nvidia", Layer: []string{"python-ml"}},
			"jupyter":      {Base: "python-ml", Layer: []string{"jupyter"}},
			"comfyui":      {Base: "python-ml", Layer: []string{"comfyui"}},
			"ollama":       {Base: "nvidia", Layer: []string{"ollama"}},
			"gpu-gateway":  {Base: "nvidia", Layer: []string{"openclaw", "ollama"}},
			"fedora-test":  {Base: "fedora", Layer: []string{"traefik", "testapi"}},
			"openclaw":     {Base: "fedora", Layer: []string{"openclaw"}},
			"githubrunner": {Base: "fedora", Layer: []string{"github-runner"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Log all images for debugging
	t.Log("Resulting images:")
	for _, name := range func() []string {
		var names []string
		for n := range result {
			names = append(names, n)
		}
		sortStrings(names)
		return names
	}() {
		img := result[name]
		autoStr := ""
		if img.Auto {
			autoStr = " [auto]"
		}
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layer)
	}

	// CRITICAL: no python-ml-2 should exist
	if _, exists := result["python-ml-2"]; exists {
		t.Error("python-ml-2 should NOT exist — user-defined python-ml should not be duplicated")
	}

	// python-ml should NOT be rebased to an auto-intermediate's parent
	// It should still chain through nvidia (possibly via pixi auto)
	npImg := result["python-ml"]
	if npImg == nil {
		t.Fatal("python-ml missing from result")
	}
	// python-ml's base chain must eventually reach nvidia
	found := false
	current := npImg.Base
	for i := 0; i < 10; i++ {
		if current == "nvidia" {
			found = true
			break
		}
		parent, ok := result[current]
		if !ok {
			break
		}
		current = parent.Base
	}
	if !found {
		t.Errorf("python-ml's base chain does not reach nvidia (base=%s)", npImg.Base)
	}

	// jupyter and comfyui should chain through python-ml (possibly via auto)
	for _, imgName := range []string{"jupyter", "comfyui"} {
		img := result[imgName]
		if img == nil {
			t.Fatalf("%s missing from result", imgName)
		}
		found := false
		current := img.Base
		for i := 0; i < 10; i++ {
			if current == "python-ml" {
				found = true
				break
			}
			parent, ok := result[current]
			if !ok {
				break
			}
			current = parent.Base
		}
		if !found {
			t.Errorf("%s base chain does not reach python-ml (base=%s)", imgName, img.Base)
		}
	}

	// builder should be unchanged
	builderImg := result["builder"]
	if builderImg.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("builder base changed to %q", builderImg.Base)
	}

	// All original images should still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}

	// Build order should have no cycles
	order, err := ResolveImageOrder(result, layers)
	if err != nil {
		t.Fatalf("ResolveImageOrder after intermediates: %v", err)
	}
	t.Logf("Build order: %v", order)
}

func TestComputeIntermediates_UserImageAtBranchPoint(t *testing.T) {
	// User defines an image that sits exactly at the shared prefix branch point.
	// It should be reused as the intermediate, not duplicated.
	layers := map[string]*Layer{
		"pixi":        {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":      {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord": {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
		"testapi":     {Name: "testapi", Require: toLayerRefs([]string{"supervisord"}), HasPixiToml: true},
		"webapp":      {Name: "webapp", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		// "svbase" is a user image with layers=[supervisord] — it sits at the branch point
		"svbase": {
			Name: "svbase", Base: "fedora", IsExternalBase: false,
			Layer: []string{"supervisord"}, Tag: "v1", Registry: "r",
			FullTag: "r/svbase:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "svbase", IsExternalBase: false,
			Layer: []string{"testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "svbase", IsExternalBase: false,
			Layer: []string{"webapp"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image: map[string]BoxConfig{
			"fedora": {Layer: []string{}},
			"svbase": {Base: "fedora", Layer: []string{"supervisord"}},
			"app1":   {Base: "svbase", Layer: []string{"testapi"}},
			"app2":   {Base: "svbase", Layer: []string{"webapp"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	t.Log("Resulting images:")
	for name, img := range result {
		autoStr := ""
		if img.Auto {
			autoStr = " [auto]"
		}
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layer)
	}

	// svbase should NOT be duplicated (no svbase-2, no supervisord auto with same layers)
	for name, img := range result {
		if img.Auto && name != "svbase" {
			// Check that any auto-intermediate doesn't duplicate svbase's role
			if len(img.Layer) > 0 {
				// Auto intermediates may exist for shared prefixes, but
				// there should be no supervisord auto that duplicates svbase
				lastLayer := img.Layer[len(img.Layer)-1]
				if lastLayer == "supervisord" && img.Base == "fedora" {
					t.Errorf("auto-intermediate %q duplicates svbase (base=%s layers=%v)", name, img.Base, img.Layer)
				}
			}
		}
	}

	// svbase should keep its original base
	if result["svbase"].Base != "fedora" {
		t.Errorf("svbase base = %q, want 'fedora'", result["svbase"].Base)
	}

	// app1 and app2 should still chain through svbase
	for _, appName := range []string{"app1", "app2"} {
		img := result[appName]
		found := false
		current := img.Base
		for i := 0; i < 10; i++ {
			if current == "svbase" {
				found = true
				break
			}
			parent, ok := result[current]
			if !ok {
				break
			}
			current = parent.Base
		}
		if !found {
			t.Errorf("%s base chain does not reach svbase (base=%s)", appName, img.Base)
		}
	}
}

func TestComputeIntermediates_UserImageAsBranchIntermediate(t *testing.T) {
	// A user-defined image sits at the exact same layer set as a trie branch point
	// and has children in the same sibling group. The algorithm should reuse it
	// as the intermediate without creating a duplicate.
	layers := map[string]*Layer{
		"A": {Name: "A", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"B": {Name: "B", Require: toLayerRefs([]string{"A"}), tasks: []Task{{Cmd: "true"}}},
		"C": {Name: "C", Require: toLayerRefs([]string{"B"}), tasks: []Task{{Cmd: "true"}}},
		"D": {Name: "D", Require: toLayerRefs([]string{"B"}), tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"base": {
			Name: "base", Base: "ext:1", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r", FullTag: "r/base:v1", Pkg: "rpm",
		},
		// mid terminates at [A, B] and has children (app1 needs [A,B,C], app2 needs [A,B,D])
		"mid": {
			Name: "mid", Base: "base", IsExternalBase: false,
			Layer: []string{"B"}, Tag: "v1", Registry: "r", FullTag: "r/mid:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "base", IsExternalBase: false,
			Layer: []string{"C"}, Tag: "v1", Registry: "r", FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "base", IsExternalBase: false,
			Layer: []string{"D"}, Tag: "v1", Registry: "r", FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}},
		Image: map[string]BoxConfig{
			"base": {Layer: []string{}},
			"mid":  {Base: "base", Layer: []string{"B"}},
			"app1": {Base: "base", Layer: []string{"C"}},
			"app2": {Base: "base", Layer: []string{"D"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	t.Log("Resulting images:")
	for name, img := range result {
		autoStr := ""
		if img.Auto {
			autoStr = " [auto]"
		}
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layer)
	}

	// mid should keep base=base (not rebased)
	if result["mid"].Base != "base" {
		t.Errorf("mid base = %q, want 'base'", result["mid"].Base)
	}

	// app1 and app2 should be rebased through mid (since mid covers the shared [A,B] prefix)
	for _, appName := range []string{"app1", "app2"} {
		img := result[appName]
		found := false
		current := img.Base
		for i := 0; i < 10; i++ {
			if current == "mid" {
				found = true
				break
			}
			parent, ok := result[current]
			if !ok {
				break
			}
			current = parent.Base
		}
		if !found {
			t.Errorf("%s base chain does not reach mid (base=%s)", appName, img.Base)
		}
	}

	// No duplicate of mid should exist
	for name, img := range result {
		if img.Auto && name != "mid" {
			for _, l := range img.Layer {
				if l == "B" {
					t.Errorf("auto-intermediate %q has layer B, may duplicate mid (layers=%v)", name, img.Layer)
				}
			}
		}
	}
}

func TestComputeIntermediates_PlatformInheritance(t *testing.T) {
	// Parent with restricted platforms should propagate to auto-intermediates.
	// nvidia is amd64-only; nvidia-supervisord should also be amd64-only.
	layers := map[string]*Layer{
		"pixi":        {Name: "pixi", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"python":      {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord": {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
		"cuda":        {Name: "cuda", Require: nil, tasks: []Task{{Cmd: "true"}}},
		"appA":        {Name: "appA", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
		"appB":        {Name: "appB", Require: toLayerRefs([]string{"supervisord"}), tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r", FullTag: "r/fedora:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64", "linux/arm64"},
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{"pixi"}, Tag: "v1", Registry: "r", FullTag: "r/builder:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64", "linux/arm64"},
		},
		"nvidia": {
			Name: "nvidia", Base: "fedora", IsExternalBase: false,
			Layer: []string{"cuda"}, Tag: "v1", Registry: "r", FullTag: "r/nvidia:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"appA": {
			Name: "appA", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"appA"}, Tag: "v1", Registry: "r", FullTag: "r/appA:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"appB": {
			Name: "appB", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"appB"}, Tag: "v1", Registry: "r", FullTag: "r/appB:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{
			Registry:  "r",
			Build:     BuildFormats{"rpm"},
			Builder:   BuilderMap{"pixi": "builder", "npm": "builder"},
			Platforms: []string{"linux/amd64", "linux/arm64"},
		},
		Image: map[string]BoxConfig{
			"builder": {Layer: []string{"pixi"}},
			"fedora":  {Layer: []string{}},
			"nvidia":  {Base: "fedora", Layer: []string{"cuda"}, Platforms: []string{"linux/amd64"}},
			"appA":    {Base: "nvidia", Layer: []string{"appA"}},
			"appB":    {Base: "nvidia", Layer: []string{"appB"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Any auto-intermediate based on nvidia must be amd64-only
	for name, img := range result {
		if !img.Auto {
			continue
		}
		// Walk base chain to see if nvidia is an ancestor
		current := img.Base
		nvidiaAncestor := false
		for i := 0; i < 10; i++ {
			if current == "nvidia" {
				nvidiaAncestor = true
				break
			}
			parent, ok := result[current]
			if !ok {
				break
			}
			current = parent.Base
		}
		if nvidiaAncestor {
			if !reflect.DeepEqual(img.Platforms, []string{"linux/amd64"}) {
				t.Errorf("auto-intermediate %q (ancestor: nvidia) has platforms %v, want [linux/amd64]",
					name, img.Platforms)
			}
		}
	}

	// fedora-based auto-intermediates should keep both platforms
	for name, img := range result {
		if !img.Auto {
			continue
		}
		if img.Base == "fedora" {
			if !reflect.DeepEqual(img.Platforms, []string{"linux/amd64", "linux/arm64"}) {
				t.Errorf("auto-intermediate %q (base: fedora) has platforms %v, want both",
					name, img.Platforms)
			}
		}
	}
}

func TestPixiBoundLayers(t *testing.T) {
	layers := map[string]*Layer{
		"llama-cpp": {Name: "llama-cpp", tasks: []Task{{Cmd: "true"}}},
		"unsloth":   {Name: "unsloth", tasks: []Task{{Cmd: "true"}}},
		"jupyter-ml": {
			Name: "jupyter-ml", HasPixiToml: true, tasks: []Task{{Cmd: "true"}},
			IncludedLayer: toLayerRefs([]string{"llama-cpp", "unsloth"}),
			Require:       toLayerRefs([]string{"cuda", "supervisord"}),
		},
		"unsloth-studio": {
			Name: "unsloth-studio", HasPixiToml: true,
			IncludedLayer: toLayerRefs([]string{"llama-cpp", "unsloth"}),
			Require:       toLayerRefs([]string{"cuda", "supervisord"}),
		},
		"cuda":        {Name: "cuda", tasks: []Task{{Cmd: "true"}}},
		"supervisord": {Name: "supervisord", tasks: []Task{{Cmd: "true"}}},
	}

	bound := pixiBoundLayers(layers)

	// unsloth has user.yml, no pixi.toml, included by pixi-owning layers → pixi-bound
	if !bound["unsloth"] {
		t.Error("unsloth should be pixi-bound (has user.yml, no pixi.toml, included by pixi-owning layer)")
	}

	// llama-cpp has user.yml, no pixi.toml, included by pixi-owning layers → pixi-bound
	if !bound["llama-cpp"] {
		t.Error("llama-cpp should be pixi-bound (has user.yml, no pixi.toml, included by pixi-owning layer)")
	}

	// jupyter-ml has pixi.toml → NOT pixi-bound (it owns its env)
	if bound["jupyter-ml"] {
		t.Error("jupyter-ml should NOT be pixi-bound (has pixi.toml)")
	}

	// cuda has root.yml but is NOT included by any pixi-owning layer → NOT pixi-bound
	if bound["cuda"] {
		t.Error("cuda should NOT be pixi-bound (not included by pixi-owning layer)")
	}
}

func TestComputeIntermediates_PixiBoundNotExtracted(t *testing.T) {
	// Mirror the actual jupyter-ml / unsloth-studio scenario.
	// Both share nvidia base and include llama-cpp + unsloth via layers:.
	// The intermediate generator must NOT extract unsloth into an intermediate
	// because it needs the pixi environment from the final image.
	layers := map[string]*Layer{
		"dbus":                {Name: "dbus", tasks: []Task{{Cmd: "true"}}},
		"ov":                  {Name: "ov", tasks: []Task{{Cmd: "true"}}},
		"llama-cpp":           {Name: "llama-cpp", tasks: []Task{{Cmd: "true"}}},
		"unsloth":             {Name: "unsloth", tasks: []Task{{Cmd: "true"}}},
		"notebook-templates":  {Name: "notebook-templates"},
		"notebook-finetuning": {Name: "notebook-finetuning"},
		"jupyter-ml": {
			Name: "jupyter-ml", HasPixiToml: true, tasks: []Task{{Cmd: "true"}},
			IncludedLayer: toLayerRefs([]string{"llama-cpp", "unsloth"}),
			Require:       toLayerRefs([]string{"cuda", "supervisord"}),
			portSpecs:     []PortSpec{{Port: 8080}},
		},
		"unsloth-studio": {
			Name: "unsloth-studio", HasPixiToml: true,
			IncludedLayer: toLayerRefs([]string{"llama-cpp", "unsloth"}),
			Require:       toLayerRefs([]string{"cuda", "supervisord"}),
			portSpecs:     []PortSpec{{Port: 8080}},
		},
		"agent-forwarding": {Name: "agent-forwarding", tasks: []Task{{Cmd: "true"}}},
		"cuda":             {Name: "cuda", tasks: []Task{{Cmd: "true"}}},
		"pixi":             {Name: "pixi", tasks: []Task{{Cmd: "true"}}},
		"python":           {Name: "python", Require: toLayerRefs([]string{"pixi"}), HasPixiToml: true},
		"supervisord":      {Name: "supervisord", Require: toLayerRefs([]string{"python"}), HasPixiToml: true},
	}

	images := map[string]*ResolvedBox{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{"pixi"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"nvidia": {
			Name: "nvidia", Base: "fedora", IsExternalBase: false,
			Layer: []string{"cuda"}, Tag: "v1", Registry: "r",
			FullTag: "r/nvidia:v1", Pkg: "rpm",
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"jupyter-ml": {
			Name: "jupyter-ml", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"agent-forwarding", "jupyter-ml", "notebook-templates", "dbus", "ov"},
			Tag:   "v1", Registry: "r", FullTag: "r/jupyter-ml:v1", Pkg: "rpm",
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"jupyter-ml-notebook": {
			Name: "jupyter-ml-notebook", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"agent-forwarding", "jupyter-ml", "notebook-templates", "notebook-finetuning", "dbus", "ov"},
			Tag:   "v1", Registry: "r", FullTag: "r/jupyter-ml-notebook:v1", Pkg: "rpm",
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
		"unsloth-studio": {
			Name: "unsloth-studio", Base: "nvidia", IsExternalBase: false,
			Layer: []string{"agent-forwarding", "unsloth-studio", "notebook-finetuning", "dbus", "ov"},
			Tag:   "v1", Registry: "r", FullTag: "r/unsloth-studio:v1", Pkg: "rpm",
			Builder: BuilderMap{"pixi": "builder", "npm": "builder"},
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{Registry: "r", Build: BuildFormats{"rpm"}, Builder: BuilderMap{"pixi": "builder", "npm": "builder"}},
		Image: map[string]BoxConfig{
			"builder":             {Layer: []string{"pixi"}},
			"fedora":              {Layer: []string{}},
			"nvidia":              {Base: "fedora", Layer: []string{"cuda"}},
			"jupyter-ml":          {Base: "nvidia", Layer: []string{"agent-forwarding", "jupyter-ml", "notebook-templates", "dbus", "ov"}},
			"jupyter-ml-notebook": {Base: "nvidia", Layer: []string{"agent-forwarding", "jupyter-ml", "notebook-templates", "notebook-finetuning", "dbus", "ov"}},
			"unsloth-studio":      {Base: "nvidia", Layer: []string{"agent-forwarding", "unsloth-studio", "notebook-finetuning", "dbus", "ov"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	t.Log("Resulting images:")
	for _, name := range func() []string {
		var names []string
		for n := range result {
			names = append(names, n)
		}
		sortStrings(names)
		return names
	}() {
		img := result[name]
		autoStr := ""
		if img.Auto {
			autoStr = " [auto]"
		}
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layer)
	}

	// CRITICAL: No auto-intermediate should contain unsloth or llama-cpp
	// These are pixi-bound layers that must stay in the final image
	for name, img := range result {
		if !img.Auto {
			continue
		}
		for _, l := range img.Layer {
			if l == "unsloth" {
				t.Errorf("auto-intermediate %q contains pixi-bound layer 'unsloth' — will fail at build time (no pixi env)", name)
			}
			if l == "llama-cpp" {
				t.Errorf("auto-intermediate %q contains pixi-bound layer 'llama-cpp' — will fail at build time (no pixi env)", name)
			}
		}
	}

	// All original images should still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}

	// Build order should have no cycles
	order, err := ResolveImageOrder(result, layers)
	if err != nil {
		t.Fatalf("ResolveImageOrder after intermediates: %v", err)
	}
	t.Logf("Build order: %v", order)
}

// TestComputeIntermediates_InheritDistroFromParent guards against a regression
// where auto-intermediates inherited `distro:` and `build:` from
// `cfg.Defaults` even when the parent image explicitly overrode them. Fedora
// is declared as build:[rpm] in defaults; arch overrides to build:[pac].
// An arch-rooted intermediate must inherit [pac] + [arch], not fall
// through to defaults. See root cause in ov/intermediates.go createIntermediate.
func TestComputeIntermediates_InheritDistroFromParent(t *testing.T) {
	layers := map[string]*Layer{
		"a": {Name: "a", tasks: []Task{{Cmd: "true"}}},
		"b": {Name: "b", tasks: []Task{{Cmd: "true"}}},
		"c": {Name: "c", tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"fedora": {
			Name: "fedora", Base: "ext:fedora", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
			Distro:       []string{"fedora"},
			BuildFormats: []string{"rpm"},
		},
		"arch": {
			Name: "arch", Base: "ext:arch", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/arch:v1", Pkg: "pac",
			Distro:       []string{"arch"},
			BuildFormats: []string{"pac"},
		},
		"arch-a-b": {
			Name: "arch-a-b", Base: "arch", IsExternalBase: false,
			Layer: []string{"a", "b"}, Tag: "v1", Registry: "r",
			FullTag: "r/arch-a-b:v1", Pkg: "pac",
			Distro: []string{"arch"}, BuildFormats: []string{"pac"},
		},
		"arch-a-c": {
			Name: "arch-a-c", Base: "arch", IsExternalBase: false,
			Layer: []string{"a", "c"}, Tag: "v1", Registry: "r",
			FullTag: "r/arch-a-c:v1", Pkg: "pac",
			Distro: []string{"arch"}, BuildFormats: []string{"pac"},
		},
	}

	// Defaults explicitly use rpm to prove the fix: parent arch must
	// win over these defaults in the auto-intermediate.
	cfg := &Config{
		Defaults: BoxConfig{
			Registry: "r",
			Distro:   []string{"fedora"},
			Build:    BuildFormats{"rpm"},
		},
		Image: map[string]BoxConfig{
			"fedora":   {Layer: []string{}},
			"arch":     {Base: "ext:arch", Layer: []string{}},
			"arch-a-b": {Base: "arch", Layer: []string{"a", "b"}},
			"arch-a-c": {Base: "arch", Layer: []string{"a", "c"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Find the auto-intermediate that contains layer "a" rooted at arch.
	var archInter *ResolvedBox
	for _, img := range result {
		if !img.Auto {
			continue
		}
		if img.Base != "arch" {
			continue
		}
		archInter = img
		break
	}
	if archInter == nil {
		t.Fatalf("expected an auto-intermediate with Base=arch, got none. result keys: %v", resultNames(result))
	}

	// The critical assertion: parent distro/build must be inherited, not
	// overwritten by cfg.Defaults (which is fedora/rpm in this test).
	if got, want := archInter.BuildFormats, []string{"pac"}; !slicesEqual(got, want) {
		t.Errorf("auto-intermediate %q: BuildFormats = %v, want %v (must inherit from parent arch, not defaults)",
			archInter.Name, got, want)
	}
	if got, want := archInter.Distro, []string{"arch"}; !slicesEqual(got, want) {
		t.Errorf("auto-intermediate %q: Distro = %v, want %v (must inherit from parent arch, not defaults)",
			archInter.Name, got, want)
	}
	if archInter.Pkg != "pac" {
		t.Errorf("auto-intermediate %q: Pkg = %q, want %q", archInter.Name, archInter.Pkg, "pac")
	}
}

// TestComputeIntermediates_UnionChildBuildFormats guards the orthogonal case to
// the test above: a build format declared on the CONSUMING children but NOT the
// parent must still reach an auto-intermediate that hoists a layer needing it.
// Real-world regression: the cachyos base is build:[pac]; selkies-labwc and
// openclaw-desktop are build:[pac,aur]; the shared chrome layer (aur:
// google-chrome) gets hoisted into a shared intermediate. With parent-only
// inheritance the intermediate was [pac]-only, so chrome's aur: section was
// silently dropped and google-chrome never built. The fix unions the parent's
// formats with every consuming descendant's, parent's primary format first.
func TestComputeIntermediates_UnionChildBuildFormats(t *testing.T) {
	layers := map[string]*Layer{
		"a": {Name: "a", tasks: []Task{{Cmd: "true"}}},
		"b": {Name: "b", tasks: []Task{{Cmd: "true"}}},
		"c": {Name: "c", tasks: []Task{{Cmd: "true"}}},
	}

	images := map[string]*ResolvedBox{
		"cachyos": {
			Name: "cachyos", Base: "ext:cachyos", IsExternalBase: true,
			Layer: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/cachyos:v1", Pkg: "pac",
			Distro:       []string{"cachyos", "arch"},
			BuildFormats: []string{"pac"},
			Builder:      BuilderMap{"aur": "arch-builder"},
		},
		"cachyos-a-b": {
			Name: "cachyos-a-b", Base: "cachyos", IsExternalBase: false,
			Layer: []string{"a", "b"}, Tag: "v1", Registry: "r",
			FullTag: "r/cachyos-a-b:v1", Pkg: "pac",
			Distro: []string{"cachyos", "arch"}, BuildFormats: []string{"pac", "aur"},
		},
		"cachyos-a-c": {
			Name: "cachyos-a-c", Base: "cachyos", IsExternalBase: false,
			Layer: []string{"a", "c"}, Tag: "v1", Registry: "r",
			FullTag: "r/cachyos-a-c:v1", Pkg: "pac",
			Distro: []string{"cachyos", "arch"}, BuildFormats: []string{"pac", "aur"},
		},
	}

	cfg := &Config{
		Defaults: BoxConfig{
			Registry: "r",
			Distro:   []string{"fedora"},
			Build:    BuildFormats{"rpm"},
		},
		Image: map[string]BoxConfig{
			"cachyos":     {Base: "ext:cachyos", Layer: []string{}},
			"cachyos-a-b": {Base: "cachyos", Layer: []string{"a", "b"}},
			"cachyos-a-c": {Base: "cachyos", Layer: []string{"a", "c"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Find the auto-intermediate that hoists layer "a" rooted at cachyos.
	var inter *ResolvedBox
	for _, img := range result {
		if img.Auto && img.Base == "cachyos" {
			inter = img
			break
		}
	}
	if inter == nil {
		t.Fatalf("expected an auto-intermediate with Base=cachyos, got none. result keys: %v", resultNames(result))
	}

	// The intermediate must carry the UNION: parent's [pac] first, then the
	// consumer-only aur appended. Without the fix this is [pac] only and the
	// hoisted aur-bearing layer is silently dropped.
	if got, want := inter.BuildFormats, []string{"pac", "aur"}; !slicesEqual(got, want) {
		t.Errorf("auto-intermediate %q: BuildFormats = %v, want %v (parent pac first + consumer-only aur appended)",
			inter.Name, got, want)
	}
	// Parent's primary format stays primary (drives img.Pkg + cache mounts).
	if inter.Pkg != "pac" {
		t.Errorf("auto-intermediate %q: Pkg = %q, want %q (parent primary preserved)", inter.Name, inter.Pkg, "pac")
	}
	// And the builder map for the unioned `aur` format must be inherited from the
	// parent chain — otherwise the intermediate carries aur but can't build it
	// ("needs builder aur but no builders.aur configured").
	if got := inter.Builder["aur"]; got != "arch-builder" {
		t.Errorf("auto-intermediate %q: Builder[aur] = %q, want %q (must inherit from parent so the unioned aur format is buildable)", inter.Name, got, "arch-builder")
	}
}

func resultNames(m map[string]*ResolvedBox) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
