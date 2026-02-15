package main

import (
	"archive/tar"
	"bytes"
	"io"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

const mb = 1024 * 1024

// TestPlanMerge_AllLarge verifies no merging when all layers exceed min_mb.
func TestPlanMerge_AllLarge(t *testing.T) {
	sizes := []int64{200 * mb, 150 * mb, 300 * mb}
	steps := planMerge(sizes, 100*mb, 1024*mb)

	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	for i, step := range steps {
		if !step.Keep {
			t.Errorf("step %d: expected Keep=true", i)
		}
		if len(step.Layers) != 1 || step.Layers[0] != i {
			t.Errorf("step %d: expected Layers=[%d], got %v", i, i, step.Layers)
		}
	}
}

// TestPlanMerge_AllSmall verifies grouping of all-small layers.
func TestPlanMerge_AllSmall(t *testing.T) {
	sizes := []int64{10 * mb, 20 * mb, 30 * mb, 15 * mb}
	steps := planMerge(sizes, 100*mb, 1024*mb)

	if len(steps) != 1 {
		t.Fatalf("expected 1 step (merged group), got %d", len(steps))
	}
	if steps[0].Keep {
		t.Error("expected Keep=false for merge group")
	}
	if len(steps[0].Layers) != 4 {
		t.Errorf("expected 4 layers in group, got %d", len(steps[0].Layers))
	}
}

// TestPlanMerge_Mixed verifies large layers break merge runs.
func TestPlanMerge_Mixed(t *testing.T) {
	sizes := []int64{10 * mb, 20 * mb, 200 * mb, 5 * mb, 15 * mb}
	steps := planMerge(sizes, 100*mb, 1024*mb)

	// Expected: merge(0,1), keep(2), merge(3,4)
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}

	// First: merge group [0, 1]
	if steps[0].Keep {
		t.Error("step 0: expected Keep=false")
	}
	if len(steps[0].Layers) != 2 || steps[0].Layers[0] != 0 || steps[0].Layers[1] != 1 {
		t.Errorf("step 0: expected Layers=[0,1], got %v", steps[0].Layers)
	}

	// Second: keep layer 2
	if !steps[1].Keep {
		t.Error("step 1: expected Keep=true")
	}
	if steps[1].Layers[0] != 2 {
		t.Errorf("step 1: expected Layers=[2], got %v", steps[1].Layers)
	}

	// Third: merge group [3, 4]
	if steps[2].Keep {
		t.Error("step 2: expected Keep=false")
	}
	if len(steps[2].Layers) != 2 || steps[2].Layers[0] != 3 || steps[2].Layers[1] != 4 {
		t.Errorf("step 2: expected Layers=[3,4], got %v", steps[2].Layers)
	}
}

// TestPlanMerge_MaxMBLimit verifies group split when max_mb is exceeded.
func TestPlanMerge_MaxMBLimit(t *testing.T) {
	sizes := []int64{40 * mb, 40 * mb, 40 * mb, 40 * mb}
	steps := planMerge(sizes, 100*mb, 100*mb) // max=100MB

	// First three: 40+40=80 fits, 80+40=120 doesn't -> group [0,1], then [2,3]
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Keep || len(steps[0].Layers) != 2 {
		t.Errorf("step 0: expected merge of 2 layers, got Keep=%v Layers=%v", steps[0].Keep, steps[0].Layers)
	}
	if steps[1].Keep || len(steps[1].Layers) != 2 {
		t.Errorf("step 1: expected merge of 2 layers, got Keep=%v Layers=%v", steps[1].Keep, steps[1].Layers)
	}
}

// TestPlanMerge_SingleSmall verifies a single small layer is kept as-is (no single-layer merge).
func TestPlanMerge_SingleSmall(t *testing.T) {
	sizes := []int64{200 * mb, 10 * mb, 300 * mb}
	steps := planMerge(sizes, 100*mb, 1024*mb)

	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}
	// Layer 1 (10MB) is small but alone between large layers -> kept
	for _, step := range steps {
		if !step.Keep {
			t.Errorf("expected all steps to be Keep=true, got merge for %v", step.Layers)
		}
	}
}

// makeTarLayer creates a synthetic layer containing the given files.
func makeTarLayer(files map[string]string) (v1.Layer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Size: int64(len(content)),
			Mode: 0644,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return nil, err
		}
	}
	tw.Close()

	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// readTarEntries reads all tar entries from a layer into a map.
func readTarEntries(layer v1.Layer) (map[string]string, error) {
	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	entries := make(map[string]string)
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var content bytes.Buffer
		if hdr.Size > 0 {
			io.Copy(&content, tr)
		}
		entries[hdr.Name] = content.String()
	}
	return entries, nil
}

// TestMergeLayers verifies tar entries from multiple layers are combined.
func TestMergeLayers(t *testing.T) {
	layer1, err := makeTarLayer(map[string]string{
		"a.txt": "hello",
		"b.txt": "world",
	})
	if err != nil {
		t.Fatal(err)
	}

	layer2, err := makeTarLayer(map[string]string{
		"c.txt": "foo",
		"b.txt": "overwritten", // should override b.txt from layer1
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	// All entries should be present (tar allows duplicates; later wins at extract time)
	if len(entries) < 3 {
		t.Errorf("expected at least 3 entries, got %d: %v", len(entries), entries)
	}
	if entries["c.txt"] != "foo" {
		t.Errorf("expected c.txt=foo, got %q", entries["c.txt"])
	}
}

// TestMergeLayers_Whiteout verifies whiteout files are preserved.
func TestMergeLayers_Whiteout(t *testing.T) {
	layer1, err := makeTarLayer(map[string]string{
		"usr/bin/app": "binary",
	})
	if err != nil {
		t.Fatal(err)
	}

	layer2, err := makeTarLayer(map[string]string{
		"usr/bin/.wh.app": "", // whiteout
		"usr/bin/app2":    "new binary",
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := mergeLayers([]v1.Layer{layer1, layer2})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := readTarEntries(merged)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := entries["usr/bin/.wh.app"]; !ok {
		t.Error("whiteout file usr/bin/.wh.app not preserved")
	}
	if _, ok := entries["usr/bin/app2"]; !ok {
		t.Error("new file usr/bin/app2 not preserved")
	}
}

// TestHistoryAlignment verifies empty-layer history entries are preserved correctly.
func TestHistoryAlignment(t *testing.T) {
	// Build a synthetic image with layers and mixed history
	layer1, _ := makeTarLayer(map[string]string{"a": "1"})
	layer2, _ := makeTarLayer(map[string]string{"b": "2"})
	layer3, _ := makeTarLayer(map[string]string{"c": "3"})

	img := empty.Image
	img, _ = mutate.Append(img,
		mutate.Addendum{
			Layer:   layer1,
			History: v1.History{CreatedBy: "RUN step1"},
		},
		mutate.Addendum{
			History: v1.History{CreatedBy: "ENV FOO=bar", EmptyLayer: true},
		},
		mutate.Addendum{
			Layer:   layer2,
			History: v1.History{CreatedBy: "RUN step2"},
		},
		mutate.Addendum{
			Layer:   layer3,
			History: v1.History{CreatedBy: "RUN step3"},
		},
		mutate.Addendum{
			History: v1.History{CreatedBy: "USER 1000", EmptyLayer: true},
		},
	)

	layers, _ := img.Layers()
	sizes := make([]int64, len(layers))
	for i, l := range layers {
		sizes[i], _ = l.Size()
	}

	// All layers are tiny, so they should all merge
	steps := planMerge(sizes, 100*mb, 1024*mb)

	// All 3 layers are small -> one merge group
	if len(steps) != 1 || steps[0].Keep {
		t.Fatalf("expected 1 merge step, got %d steps", len(steps))
	}

	newImg, err := executeMerge(img, layers, steps)
	if err != nil {
		t.Fatal(err)
	}

	cf, _ := newImg.ConfigFile()

	// Should have: merged-layer history, ENV empty history, USER empty history
	// The empty-layer entries should be preserved
	emptyCount := 0
	nonEmptyCount := 0
	for _, h := range cf.History {
		if h.EmptyLayer {
			emptyCount++
		} else {
			nonEmptyCount++
		}
	}

	if nonEmptyCount != 1 {
		t.Errorf("expected 1 non-empty history entry (merged), got %d", nonEmptyCount)
	}
	if emptyCount != 2 {
		t.Errorf("expected 2 empty history entries (ENV + USER), got %d", emptyCount)
	}

	newLayers, _ := newImg.Layers()
	if len(newLayers) != 1 {
		t.Errorf("expected 1 layer after merge, got %d", len(newLayers))
	}
}
