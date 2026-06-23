package main

import (
	"strings"
	"testing"
)

// A run: package step lowers into a SystemPackagesStep whose Reverse() removes
// the package — so `charly bundle del` undoes it. The keyword (run:) supplies
// the act intent the deleted Op.Do axis used to carry.
func TestCompileRunStep_PackageLowersToSystemPackages(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "install redis", Op: Op{Package: "redis"}}}}
	steps := compileOpSteps(layer, testResolvedBox())

	var sp *SystemPackagesStep
	for _, s := range steps {
		if v, ok := s.(*SystemPackagesStep); ok {
			sp = v
		}
	}
	if sp == nil {
		t.Fatalf("package run: step did not lower to SystemPackagesStep; got %#v", steps)
	}
	if len(sp.Packages) != 1 || sp.Packages[0] != "redis" {
		t.Errorf("SystemPackagesStep.Packages = %v, want [redis]", sp.Packages)
	}
	rev := sp.Reverse()
	if len(rev) != 1 || rev[0].Kind != ReverseOpPackageRemove {
		t.Errorf("package run: step did not reverse to package-remove: %+v", rev)
	}
}

// A run: service step lowers into a ServicePackagedStep (enable the unit).
func TestCompileRunStep_ServiceLowersToServicePackaged(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "enable sshd", Op: Op{Service: "sshd"}}}}
	steps := compileOpSteps(layer, testResolvedBox())
	var found bool
	for _, s := range steps {
		if v, ok := s.(*ServicePackagedStep); ok && v.Unit == "sshd" && v.Enable {
			found = true
		}
	}
	if !found {
		t.Fatalf("service run: step did not lower to an enabling ServicePackagedStep; got %#v", steps)
	}
}

// A build-context run: step folds into the install plan; a sibling check: step in
// the same plan does NOT (it is a runtime probe, not an install step).
func TestCompileOpSteps_FoldsBuildContextRunStepNotCheck(t *testing.T) {
	tr := true
	layer := &Candy{Name: "x", plan: []Step{
		{Run: "install vim", Op: Op{Package: "vim", Context: []string{"build"}}},
		{Check: "vim present", Op: Op{File: "/usr/bin/vim", Exists: &tr}}, // a check: step → not folded
	}}
	steps := compileOpSteps(layer, testResolvedBox())
	pkgCount := 0
	for _, s := range steps {
		if _, ok := s.(*SystemPackagesStep); ok {
			pkgCount++
		}
	}
	if pkgCount != 1 {
		t.Fatalf("want exactly 1 folded package step (the check: probe excluded), got %d (%#v)", pkgCount, steps)
	}
}

// A runtime-only run: step (context: [runtime]) is NOT folded into the build
// plan — the check Runner executes it live, so folding would double-run.
func TestCompileOpSteps_DoesNotFoldRuntimeOnlyRunStep(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{
		{Run: "run", Op: Op{Plugin: "command", PluginInput: map[string]any{"command": "echo hi"}, Context: []string{"runtime"}}},
	}}
	if steps := compileOpSteps(layer, testResolvedBox()); len(steps) != 0 {
		t.Fatalf("runtime-only run: step must not be folded into the build plan, got %d steps", len(steps))
	}
}

// A run: command step (the install timeline; the former task: list) lowers to an
// OpStep — it must NOT be dropped, and the run-as user drives scope.
func TestCompileOpSteps_RunCommandLowersToOpStep(t *testing.T) {
	layer := &Candy{Name: "x", plan: []Step{{Run: "run cmd", Op: Op{Plugin: "command", PluginInput: map[string]any{"command": "echo hi"}, RunAs: "root"}}}}
	steps := compileOpSteps(layer, testResolvedBox())
	if len(steps) != 1 {
		t.Fatalf("run: command dropped: %d steps", len(steps))
	}
	op, ok := steps[0].(*OpStep)
	if !ok || op.Op.Plugin != "command" || op.Op.PluginInput["command"] != "echo hi" {
		t.Fatalf("run: command not compiled as an OpStep: %#v", steps[0])
	}
	// The run-as user (not the user verb) drives scope — root → system.
	if op.ResolvedUser != "root" && op.ResolvedUser != "0" {
		t.Errorf("OpStep.ResolvedUser = %q, want root (from RunAs)", op.ResolvedUser)
	}
}

// The validator rejects a build-context run: step whose verb has no build/deploy
// install path (file creation is the write/copy verbs) — the compiler would
// otherwise silently drop it. The run: keyword stamps the act intent.
func TestValidateOps_RejectsRuntimeOnlyActInBuild(t *testing.T) {
	layers := map[string]*Candy{
		"l": {Name: "l", plan: []Step{{Run: "create x", Op: Op{File: "/x", Context: []string{"build"}}}}},
	}
	got := runValidateOps(t, &Config{Box: map[string]BoxConfig{}}, layers)
	if !strings.Contains(got, "cannot act") {
		t.Errorf("expected a 'cannot act in build context' rejection, got: %s", got)
	}
}
