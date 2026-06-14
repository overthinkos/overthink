package main

// RDD spike (S-ingest + plan-order) — runs INSIDE the real charly module so the
// CUE imports resolve in-workspace (no throwaway /tmp package). Proves on the
// REAL candy corpus that CUE can ingest the on-disk YAML and that the plan-step
// order survives ingest+decode identical to the authored order (yaml.v3 ground
// truth). Throwaway: delete or fold into the real cutover tests.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	cueyaml "cuelang.org/go/encoding/yaml"
	goyaml "gopkg.in/yaml.v3"
)

func spikeCandyFiles(t *testing.T) []string {
	t.Helper()
	const root = "../candy"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(root, e.Name(), "charly.yml")
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	return files
}

// planSig returns the ordered per-step signatures (keyword=prose) of a candy's plan.
func spikePlanSig(doc map[string]any) []string {
	candy, _ := doc["candy"].(map[string]any)
	if candy == nil {
		return nil
	}
	raw, _ := candy["plan"].([]any)
	kws := []string{"run", "check", "agent-run", "agent-check", "include"}
	sigs := make([]string, 0, len(raw))
	for _, e := range raw {
		m, _ := e.(map[string]any)
		s := "?keys"
		for _, k := range kws {
			if v, ok := m[k]; ok {
				s = k + "=" + spikeTrunc(v)
				break
			}
		}
		sigs = append(sigs, s)
	}
	return sigs
}

func spikeTrunc(v any) string {
	s := strings.ReplaceAll(fmt.Sprint(v), "\n", " ")
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}

func TestCueSpike_IngestAllCandies(t *testing.T) {
	ctx := cuecontext.New()
	files := spikeCandyFiles(t)
	if len(files) < 50 {
		t.Fatalf("expected the full candy corpus, got only %d files", len(files))
	}
	var ingested, withPlan int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: read: %v", f, err)
			continue
		}
		af, err := cueyaml.Extract(f, data)
		if err != nil {
			t.Errorf("%s: cue yaml.Extract: %v", f, err)
			continue
		}
		v := ctx.BuildFile(af)
		if v.Err() != nil {
			t.Errorf("%s: cue BuildFile: %v", f, v.Err())
			continue
		}
		var doc map[string]any
		if err := v.Decode(&doc); err != nil {
			t.Errorf("%s: cue Decode: %v", f, err)
			continue
		}
		ingested++
		if len(spikePlanSig(doc)) > 0 {
			withPlan++
		}
	}
	t.Logf("CUE-ingested %d/%d real candy charly.yml files; %d carry a plan", ingested, len(files), withPlan)
	if ingested != len(files) {
		t.Fatalf("not all candy files ingested cleanly: %d/%d", ingested, len(files))
	}
}

func spikeLoadAllBytes(tb testing.TB) [][]byte {
	tb.Helper()
	files := spikeCandyFiles(&testing.T{})
	out := make([][]byte, 0, len(files))
	for _, f := range files {
		if d, err := os.ReadFile(f); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// S2 perf gate: CUE ingest+decode vs yaml.v3 decode over the full candy corpus
// (a representative per-invocation load). Fresh context per iteration ~ one process load.
func BenchmarkS2_CueParseCorpus(b *testing.B) {
	data := spikeLoadAllBytes(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx := cuecontext.New()
		for j, d := range data {
			af, err := cueyaml.Extract(fmt.Sprintf("f%d", j), d)
			if err != nil {
				b.Fatal(err)
			}
			var m map[string]any
			if err := ctx.BuildFile(af).Decode(&m); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkS2_YamlParseCorpus(b *testing.B) {
	data := spikeLoadAllBytes(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, d := range data {
			var m map[string]any
			if err := goyaml.Unmarshal(d, &m); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestCueSpike_PlanOrderAllCandies(t *testing.T) {
	ctx := cuecontext.New()
	files := spikeCandyFiles(t)
	var checked, totalSteps, maxSteps int
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("%s: read: %v", f, err)
			continue
		}
		// authored order (ground truth)
		var goDoc map[string]any
		if err := goyaml.Unmarshal(data, &goDoc); err != nil {
			t.Errorf("%s: yaml.v3: %v", f, err)
			continue
		}
		goSig := spikePlanSig(goDoc)
		if len(goSig) == 0 {
			continue
		}
		// CUE-decoded order
		af, err := cueyaml.Extract(f, data)
		if err != nil {
			t.Errorf("%s: cue extract: %v", f, err)
			continue
		}
		var cueDoc map[string]any
		if err := ctx.BuildFile(af).Decode(&cueDoc); err != nil {
			t.Errorf("%s: cue decode: %v", f, err)
			continue
		}
		cueSig := spikePlanSig(cueDoc)

		if len(goSig) != len(cueSig) {
			t.Errorf("%s: plan length differs (steps dropped/added): yaml=%d cue=%d", f, len(goSig), len(cueSig))
			continue
		}
		for i := range goSig {
			if goSig[i] != cueSig[i] {
				t.Errorf("%s: step %d ORDER/IDENTITY differs\n  yaml.v3: %s\n  cue    : %s", f, i, goSig[i], cueSig[i])
				break
			}
		}
		checked++
		totalSteps += len(goSig)
		if len(goSig) > maxSteps {
			maxSteps = len(goSig)
		}
	}
	t.Logf("plan-order IDENTICAL (CUE vs authored yaml.v3) for %d candies, %d total steps, longest plan %d steps",
		checked, totalSteps, maxSteps)
	if checked == 0 {
		t.Fatal("no candy plans were checked")
	}
}
