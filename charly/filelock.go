package main

// filelock.go — the ONE advisory-flock primitive for charly.
//
// Every charly subsystem that must serialize concurrent PROCESSES on a shared
// on-disk resource goes through acquireFileLock — there is deliberately a
// single implementation, not a per-subsystem copy:
//
//   - per-bed check lock      .check/<bed>/.lock                 (fail-fast)
//   - AI-harness run lock     .check/<score>/.lock              (fail-fast)
//   - deploy-config write     ~/.config/charly/charly.yml.lock  (blocking)
//   - install ledger          ~/.config/opencharly/installed/.lock  (blocking)
//
// Fail-fast (non-blocking) is the "refuse a duplicate run" semantic — a second
// `charly check run <same-bed>` returns errLockBusy instead of clobbering the
// first run's live target. Blocking is the "serialize a brief read-modify-write"
// semantic — concurrent deploy-config writers wait their turn rather than losing
// each other's edits.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// errLockBusy is returned by a NON-blocking acquireFileLock when another holder
// already owns the lock. Callers detect it with errors.Is to render a precise
// "already in progress" message instead of a generic flock error.
var errLockBusy = errors.New("file lock held by another process")

// acquireFileLock takes an advisory flock on path (creating the file and parent
// dirs on demand) and returns a release closure that unlocks + closes.
//
// blocking selects the contention behavior:
//   - true  → LOCK_EX: wait until the lock is free (serialize, never fail).
//   - false → LOCK_EX|LOCK_NB: return errLockBusy immediately when another
//     holder exists (the duplicate-run guard).
//
// The lock file is deliberately NOT unlinked on release: unlinking a held lock
// file races a waiter that already opened the prior inode (both could then
// believe they own a now-distinct file). It persists as an empty pid marker and
// is re-locked on the next acquire. flock is per-open-file-description, so two
// acquires of the same path — even within ONE process — contend, which is what
// the duplicate-run guard relies on.
func acquireFileLock(path string, blocking bool) (release func() error, err error) {
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return nil, fmt.Errorf("create lock dir %s: %w", filepath.Dir(path), mkErr)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", path, err)
	}
	how := syscall.LOCK_EX
	if !blocking {
		how |= syscall.LOCK_NB
	}
	if flockErr := syscall.Flock(int(f.Fd()), how); flockErr != nil {
		_ = f.Close()
		// On the non-blocking path the only realistic failure of a
		// freshly-opened fd is contention (EWOULDBLOCK) — surface it as the
		// detectable busy sentinel so callers can name the offender.
		if !blocking {
			return nil, fmt.Errorf("%s: %w", path, errLockBusy)
		}
		return nil, fmt.Errorf("flock %s: %w", path, flockErr)
	}
	// Best-effort pid marker for debugging a stuck lock; never load-bearing.
	_ = f.Truncate(0)
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	return func() error {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return f.Close()
	}, nil
}

// acquireImageBuildLock serializes concurrent builds of the SAME image across
// charly processes, while letting DISTINCT images build in PARALLEL — the
// "serialize the shared intermediates, parallelize the fan-out" model. Keyed by
// image name under the project dir's .build/.locks/. When many `charly check run`
// beds in one project dir start at once, the FIRST to reach a shared intermediate
// (a base like fedora, or an auto-intermediate) builds it COLD once while the
// others block on THAT image's lock, then cache-HIT it — so a shared base is
// built ONCE, never N× redundant cold builds. Meanwhile each distinct LEAF image
// takes its own lock, so the leaves' (often cold) builds run fully parallel —
// cold builds are the long pole and must never serialize. Distinct project dirs
// (worktrees / box submodules) have distinct .build/ trees → distinct locks →
// unaffected. Blocking. The shared .build/ STAGING (deterministic Containerfiles
// + _candy) is made race-free by atomic writes (build_stage_atomic.go), not by
// this lock — so the parallel leaf builds don't collide on it either.
func acquireImageBuildLock(buildDir, image string) (func() error, error) {
	return acquireFileLock(filepath.Join(buildDir, ".locks", image+".lock"), true)
}

// acquireDeployConfigLock serializes the read-modify-write of the per-host
// deploy overlay (~/.config/charly/charly.yml) across concurrent charly
// processes. saveDeployState / cleanDeployEntry are invoked by `charly config`,
// `charly start`, `charly deploy add`, and the check-bed runner — none of which
// otherwise coordinate — so two parallel bed runs would load→modify→save the
// shared file and silently drop each other's entry (the truncation class the
// loadDeployConfigForWrite docstring warns about). Blocking: a config write is
// brief, so serialize rather than fail.
func acquireDeployConfigLock() (func() error, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, fmt.Errorf("deploy-config lock path: %w", err)
	}
	return acquireFileLock(path+".lock", true)
}
