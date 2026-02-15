package main

import (
	"testing"
)

func TestInspectRemoteImage(t *testing.T) {
	// Skip if no network - this is an integration test
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	// Test with a well-known public image
	info, err := InspectRemoteImage("alpine:latest")
	if err != nil {
		t.Fatalf("InspectRemoteImage() error = %v", err)
	}

	if info.Digest == "" {
		t.Error("expected non-empty digest")
	}

	if info.Ref == "" {
		t.Error("expected non-empty ref")
	}
}

func TestImageExists(t *testing.T) {
	// Skip if no network - this is an integration test
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	// Test with a well-known public image
	exists, err := ImageExists("alpine:latest")
	if err != nil {
		t.Fatalf("ImageExists() error = %v", err)
	}

	if !exists {
		t.Error("expected alpine:latest to exist")
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"hello world", "world", true},
		{"hello world", "foo", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
	}

	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}
