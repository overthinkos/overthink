package main

import (
	"strings"
	"testing"
)

func TestCheckTmuxInstalled(t *testing.T) {
	err := checkTmuxInstalled("nonexistent-engine", "nonexistent-container")
	if err == nil {
		t.Error("expected error for nonexistent engine")
	}
	if err != nil && strings.Contains(err.Error(), "tmux is not installed") {
		// Expected error message when tmux check fails
		t.Logf("got expected tmux-not-installed error")
	}
}

func TestTmuxHasSession(t *testing.T) {
	result := tmuxHasSession("nonexistent-engine", "nonexistent-container", "test")
	if result {
		t.Error("expected false for nonexistent session")
	}
}
