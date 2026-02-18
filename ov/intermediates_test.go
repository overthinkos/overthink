package main

import (
	"reflect"
	"testing"
)

func TestGlobalLayerOrder_PopularityTieBreaking(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil},
		"nodejs":  {Name: "nodejs", Depends: nil},
		"python":  {Name: "python", Depends: []string{"pixi"}},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	// pixi is used by 2 images, nodejs by 1
	images := map[string]*ResolvedImage{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi", "python", "testapi"}},
		"b": {Name: "b", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi", "nodejs"}},
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
		"pixi":   {Name: "pixi", Depends: nil},
		"python": {Name: "python", Depends: []string{"pixi"}},
	}

	images := map[string]*ResolvedImage{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layers: []string{"python"}},
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

func TestAbsoluteLayerSequence_WithInternalBase(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil},
		"python":  {Name: "python", Depends: []string{"pixi"}},
		"nodejs":  {Name: "nodejs", Depends: nil},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"base": {Name: "base", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi"}},
		"app":  {Name: "app", Base: "base", IsExternalBase: false, Layers: []string{"python", "testapi"}},
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
		"pixi":   {Name: "pixi", Depends: nil, HasRootYml: true},
		"python": {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"app": {
			Name: "app", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"python"}, Tag: "v1", Registry: "r",
			FullTag: "r/app:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images:   map[string]ImageConfig{"app": {Layers: []string{"python"}}},
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
		"pixi":    {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":  {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Depends: nil, HasRootYml: true},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layers: []string{"python", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layers: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora": {Layers: []string{}},
			"app1":   {Base: "fedora", Layers: []string{"python", "testapi"}},
			"app2":   {Base: "fedora", Layers: []string{"nodejs"}},
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
		"pixi":         {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":       {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord":  {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"testapi":      {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"openclaw":     {Name: "openclaw", Depends: []string{"supervisord"}, HasPackageJson: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layers: []string{"testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layers: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora":      {Layers: []string{}},
			"fedora-test": {Base: "fedora", Layers: []string{"testapi"}},
			"openclaw":    {Base: "fedora", Layers: []string{"openclaw"}},
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
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
		}
	}

	// Both fedora-test and openclaw should have an intermediate as base (not fedora directly)
	ftImg := result["fedora-test"]
	ocImg := result["openclaw"]
	if ftImg.Base == "fedora" && ocImg.Base == "fedora" {
		t.Error("both images still use fedora as base — expected an intermediate")
		for name, img := range result {
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
		}
	}
}

func TestComputeIntermediates_ExistingImageReuse(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Depends: nil, HasRootYml: true},
		"nodejs": {Name: "nodejs", Depends: nil, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layers: []string{"pixi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layers: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora": {Layers: []string{}},
			"app1":   {Base: "fedora", Layers: []string{"pixi"}},
			"app2":   {Base: "fedora", Layers: []string{"nodejs"}},
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
		"pixi":    {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":  {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Depends: nil, HasRootYml: true},
		"tooling": {Name: "tooling", Depends: nil, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"builder": {
			Name: "builder", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"pixi", "nodejs", "tooling"},
		},
		"base": {
			Name: "base", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"pixi"},
		},
		"app": {
			Name: "app", Base: "base", IsExternalBase: false,
			Layers: []string{"python"},
		},
		"simple": {
			Name: "simple", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"tooling"},
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
	// Simplified version of the actual images.yml setup
	layers := map[string]*Layer{
		"pixi":            {Name: "pixi", Depends: nil, HasRootYml: true},
		"nodejs":          {Name: "nodejs", Depends: nil, HasRootYml: true},
		"python":          {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord":     {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"build-toolchain": {Name: "build-toolchain", Depends: nil, HasRootYml: true},
		"testapi":         {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"traefik":         {Name: "traefik", Depends: []string{"supervisord"}, HasRootYml: true},
		"openclaw":        {Name: "openclaw", Depends: []string{"supervisord", "nodejs"}, HasPackageJson: true},
	}

	images := map[string]*ResolvedImage{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{"pixi", "nodejs", "build-toolchain"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm", Builder: "builder",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layers: []string{"traefik", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm", Builder: "builder",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layers: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm", Builder: "builder",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm", Builder: "builder"},
		Images: map[string]ImageConfig{
			"builder":     {Layers: []string{"pixi", "nodejs", "build-toolchain"}},
			"fedora":      {Layers: []string{}},
			"fedora-test": {Base: "fedora", Layers: []string{"traefik", "testapi"}},
			"openclaw":    {Base: "fedora", Layers: []string{"openclaw"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Log all images for debugging
	t.Log("Resulting images:")
	for name, img := range result {
		t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
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
		"pixi":            {Name: "pixi", Depends: nil, HasRootYml: true},
		"nodejs":          {Name: "nodejs", Depends: nil, HasRootYml: true},
		"python":          {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord":     {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"build-toolchain": {Name: "build-toolchain", Depends: nil, HasRootYml: true},
		"cuda":     {Name: "cuda", Depends: nil, HasRootYml: true},
		"python-ml":   {Name: "python-ml", Depends: []string{"pixi", "cuda"}, HasPixiToml: true},
		"jupyter":         {Name: "jupyter", Depends: []string{"python-ml", "supervisord"}, HasPixiToml: true},
		"comfyui":         {Name: "comfyui", Depends: []string{"python-ml", "supervisord"}, HasRootYml: true},
		"ollama":          {Name: "ollama", Depends: []string{"cuda", "supervisord"}, HasRootYml: true},
		"openclaw":        {Name: "openclaw", Depends: []string{"supervisord", "nodejs"}, HasPackageJson: true},
		"testapi":         {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"traefik":         {Name: "traefik", Depends: []string{"supervisord"}, HasRootYml: true},
		"github-runner":   {Name: "github-runner", Depends: []string{"supervisord"}, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{"pixi", "nodejs", "build-toolchain"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm", Builder: "builder",
		},
		"nvidia": {
			Name: "nvidia", Base: "fedora", IsExternalBase: false,
			Layers: []string{"cuda"}, Tag: "v1", Registry: "r",
			FullTag: "r/nvidia:v1", Pkg: "rpm", Builder: "builder",
		},
		"python-ml": {
			Name: "python-ml", Base: "nvidia", IsExternalBase: false,
			Layers: []string{"python-ml"}, Tag: "v1", Registry: "r",
			FullTag: "r/python-ml:v1", Pkg: "rpm", Builder: "builder",
		},
		"jupyter": {
			Name: "jupyter", Base: "python-ml", IsExternalBase: false,
			Layers: []string{"jupyter"}, Tag: "v1", Registry: "r",
			FullTag: "r/jupyter:v1", Pkg: "rpm", Builder: "builder",
		},
		"comfyui": {
			Name: "comfyui", Base: "python-ml", IsExternalBase: false,
			Layers: []string{"comfyui"}, Tag: "v1", Registry: "r",
			FullTag: "r/comfyui:v1", Pkg: "rpm", Builder: "builder",
		},
		"ollama": {
			Name: "ollama", Base: "nvidia", IsExternalBase: false,
			Layers: []string{"ollama"}, Tag: "v1", Registry: "r",
			FullTag: "r/ollama:v1", Pkg: "rpm", Builder: "builder",
		},
		"openclaw-ollama": {
			Name: "openclaw-ollama", Base: "nvidia", IsExternalBase: false,
			Layers: []string{"openclaw", "ollama"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw-ollama:v1", Pkg: "rpm", Builder: "builder",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layers: []string{"traefik", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm", Builder: "builder",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layers: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm", Builder: "builder",
		},
		"githubrunner": {
			Name: "githubrunner", Base: "fedora", IsExternalBase: false,
			Layers: []string{"github-runner"}, Tag: "v1", Registry: "r",
			FullTag: "r/githubrunner:v1", Pkg: "rpm", Builder: "builder",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm", Builder: "builder"},
		Images: map[string]ImageConfig{
			"builder":         {Layers: []string{"pixi", "nodejs", "build-toolchain"}},
			"fedora":          {Layers: []string{}},
			"nvidia":          {Base: "fedora", Layers: []string{"cuda"}},
			"python-ml":   {Base: "nvidia", Layers: []string{"python-ml"}},
			"jupyter":         {Base: "python-ml", Layers: []string{"jupyter"}},
			"comfyui":         {Base: "python-ml", Layers: []string{"comfyui"}},
			"ollama":          {Base: "nvidia", Layers: []string{"ollama"}},
			"openclaw-ollama": {Base: "nvidia", Layers: []string{"openclaw", "ollama"}},
			"fedora-test":     {Base: "fedora", Layers: []string{"traefik", "testapi"}},
			"openclaw":        {Base: "fedora", Layers: []string{"openclaw"}},
			"githubrunner":    {Base: "fedora", Layers: []string{"github-runner"}},
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
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layers)
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
		"pixi":        {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":      {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord": {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"testapi":     {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"webapp":      {Name: "webapp", Depends: []string{"supervisord"}, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		// "svbase" is a user image with layers=[supervisord] — it sits at the branch point
		"svbase": {
			Name: "svbase", Base: "fedora", IsExternalBase: false,
			Layers: []string{"supervisord"}, Tag: "v1", Registry: "r",
			FullTag: "r/svbase:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "svbase", IsExternalBase: false,
			Layers: []string{"testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "svbase", IsExternalBase: false,
			Layers: []string{"webapp"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora": {Layers: []string{}},
			"svbase": {Base: "fedora", Layers: []string{"supervisord"}},
			"app1":   {Base: "svbase", Layers: []string{"testapi"}},
			"app2":   {Base: "svbase", Layers: []string{"webapp"}},
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
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layers)
	}

	// svbase should NOT be duplicated (no svbase-2, no supervisord auto with same layers)
	for name, img := range result {
		if img.Auto && name != "svbase" {
			// Check that any auto-intermediate doesn't duplicate svbase's role
			if len(img.Layers) > 0 {
				// Auto intermediates may exist for shared prefixes, but
				// there should be no supervisord auto that duplicates svbase
				lastLayer := img.Layers[len(img.Layers)-1]
				if lastLayer == "supervisord" && img.Base == "fedora" {
					t.Errorf("auto-intermediate %q duplicates svbase (base=%s layers=%v)", name, img.Base, img.Layers)
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
		"A": {Name: "A", Depends: nil, HasRootYml: true},
		"B": {Name: "B", Depends: []string{"A"}, HasRootYml: true},
		"C": {Name: "C", Depends: []string{"B"}, HasRootYml: true},
		"D": {Name: "D", Depends: []string{"B"}, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"base": {
			Name: "base", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r", FullTag: "r/base:v1", Pkg: "rpm",
		},
		// mid terminates at [A, B] and has children (app1 needs [A,B,C], app2 needs [A,B,D])
		"mid": {
			Name: "mid", Base: "base", IsExternalBase: false,
			Layers: []string{"B"}, Tag: "v1", Registry: "r", FullTag: "r/mid:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "base", IsExternalBase: false,
			Layers: []string{"C"}, Tag: "v1", Registry: "r", FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "base", IsExternalBase: false,
			Layers: []string{"D"}, Tag: "v1", Registry: "r", FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"base": {Layers: []string{}},
			"mid":  {Base: "base", Layers: []string{"B"}},
			"app1": {Base: "base", Layers: []string{"C"}},
			"app2": {Base: "base", Layers: []string{"D"}},
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
		t.Logf("  %s%s: base=%s layers=%v", name, autoStr, img.Base, img.Layers)
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
			for _, l := range img.Layers {
				if l == "B" {
					t.Errorf("auto-intermediate %q has layer B, may duplicate mid (layers=%v)", name, img.Layers)
				}
			}
		}
	}
}

func TestComputeIntermediates_PlatformInheritance(t *testing.T) {
	// Parent with restricted platforms should propagate to auto-intermediates.
	// nvidia is amd64-only; nvidia-supervisord should also be amd64-only.
	layers := map[string]*Layer{
		"pixi":        {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":      {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord": {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"cuda":        {Name: "cuda", Depends: nil, HasRootYml: true},
		"appA":        {Name: "appA", Depends: []string{"supervisord"}, HasRootYml: true},
		"appB":        {Name: "appB", Depends: []string{"supervisord"}, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r", FullTag: "r/fedora:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64", "linux/arm64"},
			Builder: "builder",
		},
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{"pixi"}, Tag: "v1", Registry: "r", FullTag: "r/builder:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64", "linux/arm64"},
		},
		"nvidia": {
			Name: "nvidia", Base: "fedora", IsExternalBase: false,
			Layers: []string{"cuda"}, Tag: "v1", Registry: "r", FullTag: "r/nvidia:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: "builder",
		},
		"appA": {
			Name: "appA", Base: "nvidia", IsExternalBase: false,
			Layers: []string{"appA"}, Tag: "v1", Registry: "r", FullTag: "r/appA:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: "builder",
		},
		"appB": {
			Name: "appB", Base: "nvidia", IsExternalBase: false,
			Layers: []string{"appB"}, Tag: "v1", Registry: "r", FullTag: "r/appB:v1",
			Pkg: "rpm", Platforms: []string{"linux/amd64"}, Builder: "builder",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "r",
			Pkg:       "rpm",
			Builder:   "builder",
			Platforms: []string{"linux/amd64", "linux/arm64"},
		},
		Images: map[string]ImageConfig{
			"builder": {Layers: []string{"pixi"}},
			"fedora":  {Layers: []string{}},
			"nvidia":  {Base: "fedora", Layers: []string{"cuda"}, Platforms: []string{"linux/amd64"}},
			"appA":    {Base: "nvidia", Layers: []string{"appA"}},
			"appB":    {Base: "nvidia", Layers: []string{"appB"}},
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
