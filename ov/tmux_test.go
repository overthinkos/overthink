package main

import (
	"strings"
	"testing"
)

func TestCheckTmuxInstalled(t *testing.T) {
	// A chain into a nonexistent container makes every probe fail, so the
	// availability check returns the not-installed error.
	ex := ContainerChain("nonexistent-engine", "nonexistent-container")
	err := checkTmuxInstalled(ex)
	if err == nil {
		t.Error("expected error for nonexistent engine")
	}
	if err != nil && strings.Contains(err.Error(), "tmux is not installed") {
		// Expected error message when tmux check fails
		t.Logf("got expected tmux-not-installed error")
	}
}

func TestTmuxHasSession(t *testing.T) {
	ex := ContainerChain("nonexistent-engine", "nonexistent-container")
	result := tmuxHasSession(ex, "test")
	if result {
		t.Error("expected false for nonexistent session")
	}
}
