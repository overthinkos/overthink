package main

import (
	"fmt"
	"time"
)

// ComputeCalVer computes a CalVer version in the format YYYY.DDD.HHMM
// where:
//   - YYYY = year (e.g., 2026)
//   - DDD  = day of year (1-366)
//   - HHMM = hour and minute in UTC (0000-2359)
//
// This produces valid semver: all three components are non-negative integers.
// Versions sort correctly both lexically and numerically.
func ComputeCalVer() string {
	return ComputeCalVerAt(time.Now().UTC())
}

// ComputeCalVerAt computes CalVer for a specific time (for testing)
func ComputeCalVerAt(t time.Time) string {
	year := t.Year()
	dayOfYear := t.YearDay()
	// HHMM as a single integer: hour*100 + minute
	// 08:30 -> 830, 14:15 -> 1415, 00:00 -> 0
	hhmm := t.Hour()*100 + t.Minute()

	// Format: YYYY.DDD.HHMM
	// No leading zeros on components (valid semver)
	return fmt.Sprintf("%d.%d.%d", year, dayOfYear, hhmm)
}
