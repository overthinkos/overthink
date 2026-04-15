package main

import (
	"testing"
)

// TestResolvePodmanJobs verifies the --jobs capping logic that fixes the
// podman-5.7.x layer-reuse race triggered by --jobs runtime.NumCPU() on
// multi-stage builds with --cache-from. The helper must:
//   - honor an explicit override (>0) verbatim
//   - cap the default at podmanJobsDefault when NCPU is larger
//   - return NCPU when NCPU is smaller than podmanJobsDefault
//   - handle the edge case where NCPU == podmanJobsDefault
func TestResolvePodmanJobs(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()

	cases := []struct {
		name     string
		override int
		ncpu     int
		want     int
	}{
		{"override wins over large ncpu", 8, 16, 8},
		{"override wins over small ncpu", 1, 16, 1},
		{"override wins over exactly default ncpu", 2, 4, 2},
		{"override of default value", 4, 16, 4},
		{"no override, ncpu above default, caps at default", 0, 16, 4},
		{"no override, ncpu well above default, caps at default", 0, 128, 4},
		{"no override, ncpu equals default", 0, 4, 4},
		{"no override, ncpu below default, returns ncpu", 0, 2, 2},
		{"no override, ncpu is 1", 0, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			numCPU = func() int { return tc.ncpu }
			got := resolvePodmanJobs(tc.override)
			if got != tc.want {
				t.Errorf("resolvePodmanJobs(%d) with ncpu=%d = %d, want %d",
					tc.override, tc.ncpu, got, tc.want)
			}
		})
	}
}

// TestBuildLocalArgs_PodmanJobsCap verifies end-to-end that buildLocalArgs
// passes the capped value to podman (not raw NCPU) when the engine is podman.
// This is the regression test for the crash: before the fix, --jobs was
// runtime.NumCPU(), and on a 16-core host that was 16 (the crash trigger).
// After the fix, it must be podmanJobsDefault when NCPU >= default.
func TestBuildLocalArgs_PodmanJobsCap(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()
	numCPU = func() int { return 16 } // simulate 16-core host

	cmd := &BuildCmd{
		Cache: "none", // keep output predictable
	}
	args := cmd.buildLocalArgs("podman", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	// Find --jobs in args
	var jobsVal string
	for i, a := range args {
		if a == "--jobs" && i+1 < len(args) {
			jobsVal = args[i+1]
			break
		}
	}
	if jobsVal == "" {
		t.Fatal("--jobs not present in podman args")
	}
	if jobsVal != "4" {
		t.Errorf("--jobs = %q, want %q (capped at podmanJobsDefault)", jobsVal, "4")
	}
}

// TestBuildLocalArgs_PodmanJobsOverride verifies that an explicit
// --podman-jobs value wins over the default cap.
func TestBuildLocalArgs_PodmanJobsOverride(t *testing.T) {
	origNumCPU := numCPU
	defer func() { numCPU = origNumCPU }()
	numCPU = func() int { return 16 }

	cmd := &BuildCmd{
		Cache:      "none",
		PodmanJobs: 8,
	}
	args := cmd.buildLocalArgs("podman", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	var jobsVal string
	for i, a := range args {
		if a == "--jobs" && i+1 < len(args) {
			jobsVal = args[i+1]
			break
		}
	}
	if jobsVal != "8" {
		t.Errorf("--jobs = %q, want %q (override)", jobsVal, "8")
	}
}

// TestBuildLocalArgs_DockerEngineSkipsJobsFlag ensures the fix did not
// accidentally add --jobs to the docker code path (which never had it).
func TestBuildLocalArgs_DockerEngineSkipsJobsFlag(t *testing.T) {
	cmd := &BuildCmd{Cache: "none"}
	args := cmd.buildLocalArgs("docker", []string{"img:latest"}, "linux/amd64", "img", "ghcr.io/org")

	for _, a := range args {
		if a == "--jobs" {
			t.Errorf("docker args should not include --jobs, got: %v", args)
			return
		}
	}
}
