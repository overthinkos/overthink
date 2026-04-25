package main

// harness_cmd.go — `ov harness` command tree (host-side dispatcher).
//
// Stub during the harness cutover. Sub-verbs progressively wired in
// follow-up commits:
//   - list-ai, list-recipe — read-only catalog inspection (functional now)
//   - run / run-local / sync-credential — iteration drivers (stubbed)
//   - scope / last-test-tag / self-evaluate — AI-facing helpers (stubbed)
//   - note read / note append — NOTES.md memory subsystem (functional now)
//   - report / list — past-run inspection (stubbed)

import (
	"fmt"
	"os"
	"os/exec"
)

// HarnessCmd is the top-level `ov harness` command tree.
type HarnessCmd struct {
	ListAI     HarnessListAICmd     `cmd:"list-ai" help:"List configured AIs from harness.yml"`
	ListRecipe HarnessListRecipeCmd `cmd:"list-recipe" help:"List configured recipes from harness.yml"`
	Run        HarnessRunCmd        `cmd:"" help:"Run a harness recipe (drives an AI through iteration cycles)"`
	RunLocal   HarnessRunLocalCmd   `cmd:"run-local" hidden:"" help:"Pod/VM-side iteration driver (not invoked directly)"`
	SyncCred   HarnessSyncCredCmd   `cmd:"sync-credential" help:"Copy AI credentials into the recipe's target"`
	Scope      HarnessScopeCmd      `cmd:"scope" help:"AI-facing: print current iteration scope"`
	LastTag    HarnessLastTagCmd    `cmd:"last-test-tag" help:"AI-facing: print prior iteration's image tag"`
	SelfEval   HarnessSelfEvalCmd   `cmd:"self-evaluate" help:"AI-facing: rebuild current clone + run ov image test"`
	List       HarnessListRunsCmd   `cmd:"list" help:"List past harness runs under .harness/<recipe>/"`
	Report     HarnessReportCmd     `cmd:"report" help:"Render a past result-<calver>.yml"`
	Note       HarnessNoteCmd       `cmd:"note" help:"Read/append the persistent NOTES.md memory for a recipe"`
}

// ---------------------------------------------------------------------------
// list-ai / list-recipe — functional inspection
// ---------------------------------------------------------------------------

// HarnessListAICmd implements `ov harness list-ai`.
type HarnessListAICmd struct{}

func (c *HarnessListAICmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No overthink.yml found in current directory.")
		return nil
	}
	PrintAIs(os.Stdout, uf.AI)
	return nil
}

// HarnessListRecipeCmd implements `ov harness list-recipe`.
type HarnessListRecipeCmd struct{}

func (c *HarnessListRecipeCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if uf == nil {
		fmt.Fprintln(os.Stdout, "No overthink.yml found in current directory.")
		return nil
	}
	PrintRecipes(os.Stdout, uf.Recipe)
	return nil
}

// ---------------------------------------------------------------------------
// Stubs — wired in follow-up implementation phases
// ---------------------------------------------------------------------------

const harnessNotImpl = "ov harness: this verb is not yet wired in the harness cutover (migration command works; iteration loop pending)"

// HarnessRunCmd is `ov harness run <recipe>`.
type HarnessRunCmd struct {
	Recipe string `arg:"" help:"Recipe name (from harness.yml)"`
	AI     string `name:"ai" help:"Pick which AI to run (required if recipe.ai has more than one entry)"`

	// Mutually-exclusive target overrides. Field names avoid collision
	// with the top-level --host flag (which re-execs over SSH); we use
	// --on-pod / --on-vm / --on-host as the user-facing surface.
	Pod  string `name:"on-pod" xor:"target" help:"Override recipe target with this pod deployment"`
	VM   string `name:"on-vm" xor:"target" help:"Override recipe target with this VM"`
	Host bool   `name:"on-host" xor:"target" help:"Override recipe target to run on the host directly"`

	PlateauIteration int    `name:"plateau-iteration" help:"Override recipe.plateau_iteration"`
	MaxIteration     int    `name:"max-iteration" help:"Override recipe.max_iteration"`
	MaxScenario      int    `name:"max-scenario" help:"Cap the pending input set"`
	Tag              string `name:"tag" help:"Override recipe.tag (Gherkin tag expression)"`
	DryRun           bool   `name:"dry-run" help:"Render scope+prompt without rebuild"`
	SkipRebuild      bool   `name:"skip-rebuild" help:"Source-only scenarios"`
	Format           string `name:"format" enum:"text,yaml" default:"text" help:"Output format"`
}

func (c *HarnessRunCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	uf, ok, err := LoadUnified(cwd)
	if err != nil {
		return err
	}
	if !ok || uf == nil {
		return fmt.Errorf("ov harness run: no overthink.yml in %s", cwd)
	}
	recipe, err := ResolveRecipe(uf.Recipe, c.Recipe)
	if err != nil {
		return err
	}
	// Apply CLI overrides to a recipe copy.
	if c.Pod != "" {
		recipe.Pod = c.Pod
		recipe.VM = ""
		recipe.Host = false
	} else if c.VM != "" {
		recipe.VM = c.VM
		recipe.Pod = ""
		recipe.Host = false
	} else if c.Host {
		recipe.Host = true
		recipe.Pod = ""
		recipe.VM = ""
		// host: requires disposable: true
		recipe.Disposable = true
	}
	tk, tn, err := ResolveRecipeTarget(recipe)
	if err != nil {
		return err
	}

	runID := GenerateRunID()
	args := []string{"harness", "run-local", c.Recipe, "--run-id", runID}
	if c.AI != "" {
		args = append(args, "--ai", c.AI)
	}
	if c.PlateauIteration > 0 {
		args = append(args, "--plateau-iteration", fmt.Sprintf("%d", c.PlateauIteration))
	}
	if c.MaxIteration > 0 {
		args = append(args, "--max-iteration", fmt.Sprintf("%d", c.MaxIteration))
	}
	if c.MaxScenario > 0 {
		args = append(args, "--max-scenario", fmt.Sprintf("%d", c.MaxScenario))
	}
	if c.Tag != "" {
		args = append(args, "--tag", c.Tag)
	}
	if c.DryRun {
		args = append(args, "--dry-run")
	}
	if c.SkipRebuild {
		args = append(args, "--skip-rebuild")
	}
	if c.Format != "" {
		args = append(args, "--format", c.Format)
	}

	switch tk {
	case TargetKindHost:
		// In-process invocation — no subprocess dispatch.
		return runLocalInProcess(args, c.Recipe, runID, recipe, uf, cwd)
	case TargetKindPod:
		return dispatchToPod(tn, args)
	case TargetKindVM:
		return dispatchToVM(tn, args)
	}
	return fmt.Errorf("unsupported target kind: %s", tk)
}

// runLocalInProcess invokes HarnessRunLocalCmd in-process for host targets.
func runLocalInProcess(args []string, recipeName, runID string, _ *HarnessRecipe, _ *UnifiedFile, cwd string) error {
	// Re-exec our own ov binary so flag parsing matches the dispatched
	// argv exactly. This keeps the dispatch shape identical for host
	// and non-host targets and avoids duplicating Kong flag handling.
	exe, err := os.Executable()
	if err != nil {
		exe = "ov"
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = cwd
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

// dispatchToPod re-execs `ov harness run-local ...` inside a running
// pod via `podman exec`.
func dispatchToPod(podName string, args []string) error {
	containerName := "ov-" + podName
	full := append([]string{"exec", "-i", containerName, "ov"}, args...)
	cmd := exec.Command("podman", full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman exec %s: %w", containerName, err)
	}
	// Mirror artifacts back to the host.
	return mirrorPodHarnessDir(containerName)
}

// mirrorPodHarnessDir copies .harness/ back from the pod to the host.
// Best-effort — failure is logged non-fatally.
func mirrorPodHarnessDir(containerName string) error {
	cmd := exec.Command("podman", "cp", containerName+":/workspace/.harness/.", "./.harness/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "harness: mirror artifacts back: %v (non-fatal)\n", err)
	}
	return nil
}

// dispatchToVM re-execs `ov harness run-local ...` inside a VM via SSH.
func dispatchToVM(vmName string, args []string) error {
	full := append([]string{"vm", "ssh", vmName, "--", "ov"}, args...)
	cmd := exec.Command("ov", full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// HarnessRunLocalCmd lives in harness_runlocal_cmd.go (the in-target
// iteration driver). The Kong tag here registers the subcommand;
// the full struct + Run method are defined in that file.

// HarnessSyncCredCmd is `ov harness sync-credential <recipe>`.
type HarnessSyncCredCmd struct {
	Recipe string `arg:"" help:"Recipe name"`
	AI     string `name:"ai" help:"Sync credentials for this AI only (default: all configured)"`
}

func (c *HarnessSyncCredCmd) Run() error { return c.RunActual() }

// HarnessScopeCmd reads the active iteration's scope.yml. AI-facing.
type HarnessScopeCmd struct{}

func (c *HarnessScopeCmd) Run() error {
	recipe := os.Getenv("OV_HARNESS_RECIPE")
	runID := os.Getenv("OV_HARNESS_RUN_ID")
	iter := os.Getenv("OV_HARNESS_ITERATION")
	if recipe == "" || runID == "" || iter == "" {
		return fmt.Errorf("ov harness scope: must run inside an iteration (OV_HARNESS_RECIPE/RUN_ID/ITERATION env required)")
	}
	cwd, _ := os.Getwd()
	path := fmt.Sprintf("%s/.harness/%s/runs/%s/iter%s/scope.yml", cwd, recipe, runID, iter)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = os.Stdout.Write(data)
	return err
}

// HarnessLastTagCmd prints the prior iteration's image tag.
type HarnessLastTagCmd struct{}

func (c *HarnessLastTagCmd) Run() error {
	runID := os.Getenv("OV_HARNESS_RUN_ID")
	iter := os.Getenv("OV_HARNESS_ITERATION")
	if runID == "" || iter == "" {
		return fmt.Errorf("ov harness last-test-tag: must run inside an iteration")
	}
	var k int
	fmt.Sscanf(iter, "%d", &k)
	if k <= 1 {
		return fmt.Errorf("ov harness last-test-tag: no prior iteration (k=%d)", k)
	}
	fmt.Printf("ovharness-%s-iter%d\n", runID, k-1)
	return nil
}

// HarnessSelfEvalCmd is the AI-facing self-evaluation verb. Stubbed for now.
type HarnessSelfEvalCmd struct{}

func (c *HarnessSelfEvalCmd) Run() error {
	return fmt.Errorf("ov harness self-evaluate: rebuilds the current per-run clone and runs `ov image test` (deferred)")
}

// HarnessListRunsCmd lists past runs across all recipes.
type HarnessListRunsCmd struct{}

func (c *HarnessListRunsCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	runs, err := ListRuns(nil, cwd)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No harness runs found under .harness/.")
		return nil
	}
	fmt.Printf("%-20s  %-25s  %-10s  %s\n", "RECIPE", "RUN_ID", "STATUS", "STARTED")
	for _, r := range runs {
		started := r.StartedUTC.Format("2006-01-02 15:04:05Z")
		fmt.Printf("%-20s  %-25s  %-10s  %s\n", r.Recipe, r.RunID, r.Status, started)
	}
	return nil
}

// HarnessReportCmd prints a past result-<calver>.yml.
type HarnessReportCmd struct {
	Recipe string `arg:"" optional:"" help:"Recipe name (default: latest)"`
	Calver string `arg:"" optional:"" help:"Calver of the result to display (default: latest)"`
}

func (c *HarnessReportCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	resultsRoot := fmt.Sprintf("%s/.harness", cwd)
	if c.Recipe == "" {
		// Pick latest recipe by mtime of its results dir.
		entries, err := os.ReadDir(resultsRoot)
		if err != nil {
			return fmt.Errorf("no .harness directory in %s", cwd)
		}
		var newest os.DirEntry
		var newestT int64
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Unix() > newestT {
				newestT = info.ModTime().Unix()
				newest = e
			}
		}
		if newest == nil {
			return fmt.Errorf("no recipes under %s", resultsRoot)
		}
		c.Recipe = newest.Name()
	}
	resultsDir := fmt.Sprintf("%s/%s/results", resultsRoot, c.Recipe)
	if c.Calver == "" {
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			return fmt.Errorf("no results directory: %s", resultsDir)
		}
		var latest string
		for _, e := range entries {
			n := e.Name()
			if len(n) > 7 && n[:7] == "result-" && n[len(n)-4:] == ".yml" {
				if n > latest {
					latest = n
				}
			}
		}
		if latest == "" {
			return fmt.Errorf("no result files under %s", resultsDir)
		}
		// Strip "result-" prefix and ".yml" suffix.
		c.Calver = latest[7 : len(latest)-4]
	}
	path := fmt.Sprintf("%s/result-%s.yml", resultsDir, c.Calver)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// HarnessNoteCmd groups the note read/append verbs.
type HarnessNoteCmd struct {
	Read   HarnessNoteReadCmd   `cmd:"" help:"Print the persistent NOTES.md for a recipe"`
	Append HarnessNoteAppendCmd `cmd:"" help:"Atomically append a note to a recipe's NOTES.md"`
}

type HarnessNoteReadCmd struct {
	Recipe string `arg:"" optional:"" help:"Recipe name (default: $OV_HARNESS_RECIPE)"`
}

func (c *HarnessNoteReadCmd) Run() error {
	recipe := c.Recipe
	if recipe == "" {
		recipe = os.Getenv("OV_HARNESS_RECIPE")
	}
	if recipe == "" {
		return fmt.Errorf("ov harness note read: recipe name required (pass as arg or set OV_HARNESS_RECIPE)")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	body, err := ReadNote(dir, recipe)
	if err != nil {
		return err
	}
	if body == "" {
		fmt.Fprintln(os.Stdout, "(empty)")
		return nil
	}
	fmt.Fprint(os.Stdout, body)
	return nil
}

type HarnessNoteAppendCmd struct {
	Recipe string `arg:"" help:"Recipe name (or skip and pass --recipe)"`
	Text   string `arg:"" help:"Note text (one paragraph)"`
}

func (c *HarnessNoteAppendCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := os.Getenv("OV_HARNESS_RUN_ID")
	iter := os.Getenv("OV_HARNESS_ITERATION")
	ai := os.Getenv("OV_HARNESS_AI")
	if iter == "" {
		iter = "0"
	}
	return AppendNote(dir, c.Recipe, runID, iter, ai, c.Text)
}
