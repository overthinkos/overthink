package main

import (
	"testing"
	"time"
)

func TestComputeCalVerAt(t *testing.T) {
	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "morning build on Feb 14, 2026 (day 45)",
			time:     time.Date(2026, 2, 14, 8, 30, 0, 0, time.UTC),
			expected: "2026.45.830",
		},
		{
			name:     "afternoon build on Feb 14, 2026",
			time:     time.Date(2026, 2, 14, 14, 15, 0, 0, time.UTC),
			expected: "2026.45.1415",
		},
		{
			name:     "evening build on Feb 14, 2026",
			time:     time.Date(2026, 2, 14, 22, 1, 0, 0, time.UTC),
			expected: "2026.45.2201",
		},
		{
			name:     "midnight build",
			time:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: "2026.1.0",
		},
		{
			name:     "end of year",
			time:     time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
			expected: "2026.365.2359",
		},
		{
			name:     "leap year end of year",
			time:     time.Date(2024, 12, 31, 12, 0, 0, 0, time.UTC),
			expected: "2024.366.1200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeCalVerAt(tt.time)
			if got != tt.expected {
				t.Errorf("ComputeCalVerAt(%v) = %q, want %q", tt.time, got, tt.expected)
			}
		})
	}
}

func TestComputeCalVer(t *testing.T) {
	// Just verify it returns something in the right format
	version := ComputeCalVer()
	if version == "" {
		t.Error("ComputeCalVer() returned empty string")
	}
	// Should contain two dots (three parts)
	dots := 0
	for _, c := range version {
		if c == '.' {
			dots++
		}
	}
	if dots != 2 {
		t.Errorf("ComputeCalVer() = %q, expected format YYYY.DDD.HHMM with 2 dots", version)
	}
}
