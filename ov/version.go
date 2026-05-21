package main

import (
	"fmt"
	"strconv"
	"strings"
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

// CalVer is a parsed YYYY.DDD.HHMM calendar version. The same format that
// ComputeCalVer emits for image tags is, since the 2026-05 schema-versioning
// cutover, the schema-version stamp carried by every versioned YAML config.
// The migration chain (migrate_registry.go) is ordered by CalVer, and the
// load-time gate compares a file's CalVer against LatestSchemaVersion.
type CalVer struct {
	Year int // calendar year (e.g. 2026)
	Day  int // day of year, 1-366
	HHMM int // hour*100 + minute, 0-2359
}

// ParseCalVer parses a "YYYY.DDD.HHMM" string. It returns ok=false for any
// value that is not a well-formed CalVer — including the legacy integer
// schema version ("4"), an empty string, or non-numeric junk. Callers
// (the schema gate and the migration runner) treat a false result as
// "older than every real CalVer", which is exactly what carries a pre-CalVer
// `version: 4` file into the chain with no special case.
func ParseCalVer(s string) (CalVer, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return CalVer{}, false
	}
	year, err1 := strconv.Atoi(parts[0])
	day, err2 := strconv.Atoi(parts[1])
	hhmm, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return CalVer{}, false
	}
	if year < 1970 || day < 1 || day > 366 || hhmm < 0 || hhmm > 2359 {
		return CalVer{}, false
	}
	return CalVer{Year: year, Day: day, HHMM: hhmm}, true
}

// String renders the CalVer back to "YYYY.DDD.HHMM" (no leading zeros,
// matching ComputeCalVerAt's output).
func (c CalVer) String() string {
	return fmt.Sprintf("%d.%d.%d", c.Year, c.Day, c.HHMM)
}

// Less reports whether c is chronologically before o.
func (c CalVer) Less(o CalVer) bool {
	if c.Year != o.Year {
		return c.Year < o.Year
	}
	if c.Day != o.Day {
		return c.Day < o.Day
	}
	return c.HHMM < o.HHMM
}
