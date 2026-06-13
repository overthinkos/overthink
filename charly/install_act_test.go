package main

import (
	"strings"
	"testing"
)

// A do:act package op lowers into a SystemPackagesStep whose Reverse() removes
// the package — so `charly deploy del` undoes it. This is the RDD step-0
// assumption made executable: act-mode reuses the typed step's reversal.
func TestCompileActOp_PackageLowersToSystemPackages(t *testing.T) {
	layer := &Candy{Name: "x", tasks: []Op{{Package: "redis", Do: "act"}}}
	steps := compileOpSteps(layer, testResolvedBox())

	var sp *SystemPackagesStep
	for _, s := range steps {
		if v, ok := s.(*SystemPackagesStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("package do:act did not lower to SystemPackagesStep; got %#v", steps)
	}
	if len(sp.Packages) != 1 || sp.Packages[0] != "redis" {
		t.Errorf("SystemPackagesStep.Packages = %v, want [redis]", sp.Packages)
	}
	rev := sp.Reverse()
	if len(rev) != 1 || rev[0].Kind != ReverseOpPackageRemove {
		t.Errorf("package act did not reverse to package-remove: %+v", rev)
	}
}

// A do:act service op lowers into a ServicePackagedStep (enable the unit).
func TestCompileActOp_ServiceLowersToServicePackaged(t *testing.T) {
	layer := &Candy{Name: "x", tasks: []Op{{Service: "sshd", Do: "act"}}}
	steps := compileOpSteps(layer, testResolvedBox())
	var found bool
	for _, s := range steps {
		if v, ok := s.(*ServicePackagedStep); ok && v.Unit == "sshd" && v.Enable {
			found = true
		}
	}
	if !found {
		t.Fatalf("service do:act did not lower to an enabling ServicePackagedStep; got %#v", steps)
	}
}

// A build-scoped do:act op folded from the scenario list lands in the install
// plan; a sibling do:assert probe in the same scenario does NOT (it is a
// runtime check, not an install step).
func TestCompileOpSteps_FoldsBuildScopedScenarioActOp(t *testing.T) {
	tr := true
	layer := &Candy{Name: "x", scenario: []Scenario{{
		Name: "s",
		Step: []Step{
			{Then: "install vim", Op: Op{Package: "vim", Do: "act", Context: []string{"build"}}},
			{Then: "vim present", Op: Op{File: "/usr/bin/vim", Exists: &tr}}, // do:assert default → not folded
		},
	}}}
	steps := compileOpSteps(layer, testResolvedBox())
	pkgCount := 0
	for _, s := range steps {
		if _, ok := s.(*SystemPackagesStep); ok {
			pkgCount++
		}
	}
	if pkgCount != 1 {
		t.Fatalf("want exactly 1 folded package step (the do:act probe excluded), got %d (%#v)", pkgCount, steps)
	}
}

// A runtime-capable do:act op (default multi-context) is NOT folded into the
// build plan — the check Runner executes it live, so folding would double-run.
func TestCompileOpSteps_DoesNotFoldRuntimeCapableActOp(t *testing.T) {
	layer := &Candy{Name: "x", scenario: []Scenario{{
		Name: "s",
		Step: []Step{
			// command defaults to [build,deploy,runtime] → runtime-capable → not folded.
			{Then: "run", Op: Op{Command: "echo hi", Do: "act"}},
		},
	}}}
	if steps := compileOpSteps(layer, testResolvedBox()); len(steps) != 0 {
		t.Fatalf("runtime-capable scenario act op must not be folded into the build plan, got %d steps", len(steps))
	}
}

// A bare task: command op (DoAssert default in scenario context) is still an
// install step in the task: timeline — it must NOT be dropped.
func TestCompileOpSteps_TaskCommandNotDropped(t *testing.T) {
	layer := &Candy{Name: "x", tasks: []Op{{Command: "echo hi", RunAs: "root"}}}
	steps := compileOpSteps(layer, testResolvedBox())
	if len(steps) != 1 {
		t.Fatalf("task command dropped: %d steps", len(steps))
	}
	op, ok := steps[0].(*OpStep)
	if !ok || op.Op.Command != "echo hi" {
		t.Fatalf("task command not compiled as an OpStep: %#v", steps[0])
	}
	// The run-as user (not the user verb) drives scope — root → system.
	if op.ResolvedUser != "root" && op.ResolvedUser != "0" {
		t.Errorf("OpStep.ResolvedUser = %q, want root (from RunAs)", op.ResolvedUser)
	}
}

// The validator rejects a build-scoped do:act op whose verb has no build/deploy
// install path (the compiler would otherwise silently drop it).
func TestValidateOps_RejectsRuntimeOnlyActInBuild(t *testing.T) {
	layers := map[string]*Candy{
		"l": opsCandy("l", Op{File: "/x", Do: "act", Context: []string{"build"}}),
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "cannot act") {
		t.Errorf("expected a 'cannot act in build context' rejection, got: %s", got)
	}
}
