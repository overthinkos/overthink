package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// BuildCalVer is the CalVer build identity of THIS binary, injected at compile
// time via `-ldflags "-X main.BuildCalVer=<calver>"` (see taskfiles/Build.yml +
// pkg/arch/PKGBUILD, both of which derive it from the git commit date through
// pkg/arch/calver.sh — the same value `pacman -Q opencharly-git` reports). Empty
// for an unstamped build (`go build` / `go test` without the ldflag).
//
// This is the binary's TRUE identity — frozen at build time — as opposed to
// ComputeCalVer() below, which is a wall-clock readout of the current moment.
// `charly version` reports BuildCalVer, never the clock: two different binaries must
// never claim the same version, and a newer build must sort higher so a CalVer
// comparison is a RELIABLE freshness signal (a content checksum tells you
// "different" but never "newer" — useless for deciding which charly to keep).
var BuildCalVer string

// CharlyVersion returns the CalVer identity of this `charly` binary. It is the stamped
// BuildCalVer when present; otherwise "unknown" (an unstamped dev/test build —
// ParseCalVer rejects it, so freshness comparisons treat it as older than every
// real CalVer). It NEVER falls back to the wall clock: the clock identifies the
// moment of invocation, not the binary, and that conflation is exactly the
// defect this replaces.
func CharlyVersion() string {
	if BuildCalVer != "" {
		return BuildCalVer
	}
	return "unknown"
}

// hostCharlyIsNewer reports whether the host charly (identified by hostVer, normally
// CharlyVersion()) is STRICTLY newer than a venue's charly, where venueVerOut is the raw
// stdout of `charly version` run inside that venue (pod/VM). It is the single
// CalVer-comparison arbiter shared by EnsureCharlyInGuest (boot-time install) and
// the host→nested delegation path (R3), so both agree on "is the venue's charly
// stale?".
//
// Semantics:
//   - venue version unparseable / absent (empty, "unknown", junk) → host wins
//     (true): a venue charly that can't state a CalVer is treated as older.
//   - both parse → strict CalVer compare. host newer → true; venue equal-or-newer
//     → false. Equal-or-newer is deliberately NOT overwritten: never downgrade a
//     venue charly that is ahead of (or matches) the host.
//   - host version unparseable → false: we cannot prove the host is newer, so we
//     do NOT clobber a venue charly on an unprovable claim.
func hostCharlyIsNewer(hostVer, venueVerOut string) bool {
	host, hostOK := ParseCalVer(strings.TrimSpace(hostVer))
	if !hostOK {
		return false
	}
	venue, venueOK := ParseCalVer(strings.TrimSpace(venueVerOut))
	if !venueOK {
		return true
	}
	return venue.Less(host)
}

// ComputeCalVer computes a CalVer version in the format YYYY.DDD.HHMM
// where:
//   - YYYY = year (e.g., 2026)
//   - DDD  = day of year (1-366)
//   - HHMM = hour and minute in UTC (0000-2359)
//
// This produces valid semver: all three components are non-negative integers.
// Versions sort correctly both lexically and numerically.
//
// NB: this is "what time is it NOW", used to TAG an artifact created at this
// moment (image build tag, check-run dir, deploy alias). It is NOT the identity
// of the charly binary — that is CharlyVersion()/BuildCalVer. Never use ComputeCalVer()
// to report the running binary's version.
func ComputeCalVer() string {
	return ComputeCalVerAt(time.Now().UTC())
}

// ComputeCalVerAt computes CalVer for a specific time (for testing)
func ComputeCalVerAt(t time.Time) string {
	year := t.Year()
	dayOfYear := t.YearDay()
	// HHMM as a single integer: hour*100 + minute (08:30 -> 0830, 00:00 -> 0000).
	hhmm := t.Hour()*100 + t.Minute()

	// Canonical CalVer: 4-digit year, 3-digit zero-padded day-of-year, 4-digit
	// zero-padded HHMM. Every component is fixed-width, so a plain alphanumeric
	// (lexicographic) sort of CalVer strings is chronological.
	return fmt.Sprintf("%04d.%03d.%04d", year, dayOfYear, hhmm)
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

// ParseCalVer parses the CANONICAL CalVer string "YYYY.DDD.HHMM" — exactly a
// 4-digit year, a 3-digit zero-padded day-of-year, and a 4-digit zero-padded
// HHMM, separated by dots. It is EXTREMELY STRICT and has NO backward
// compatibility: every component must be the exact width, pure ASCII digits
// (no sign, no inner whitespace), within range (day 1-366, hour 0-23, minute
// 0-59). Anything else — the legacy integer "4", a non-padded "2026.45.830",
// an empty string, junk — returns ok=false. (Surrounding whitespace, a
// transport artifact of e.g. a `charly version` trailing newline, is trimmed
// before the format check.)
//
// A false result is exactly what the schema gate and migration runner treat as
// "older than every real CalVer", so a non-canonical config flows into
// `charly migrate` and is re-stamped canonical — one clean migration forward.
//
// Because the canonical form is fixed-width zero-padded, a plain alphanumeric
// (lexicographic) sort of CalVer strings is chronological (see CalVer.Less).
func ParseCalVer(s string) (CalVer, bool) {
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return CalVer{}, false
	}
	if len(parts[0]) != 4 || len(parts[1]) != 3 || len(parts[2]) != 4 {
		return CalVer{}, false
	}
	if !allDigits(parts[0]) || !allDigits(parts[1]) || !allDigits(parts[2]) {
		return CalVer{}, false
	}
	year, _ := strconv.Atoi(parts[0])
	day, _ := strconv.Atoi(parts[1])
	hhmm, _ := strconv.Atoi(parts[2])
	if year < 1970 || day < 1 || day > 366 || hhmm/100 > 23 || hhmm%100 > 59 {
		return CalVer{}, false
	}
	return CalVer{Year: year, Day: day, HHMM: hhmm}, true
}

// String renders the canonical CalVer "YYYY.DDD.HHMM" — 4-digit year, 3-digit
// zero-padded day, 4-digit zero-padded HHMM. This is the ONLY form ParseCalVer
// accepts, so String∘Parse is the identity and a plain alphanumeric sort of
// these strings is chronological.
func (c CalVer) String() string {
	return fmt.Sprintf("%04d.%03d.%04d", c.Year, c.Day, c.HHMM)
}

// Less reports whether c is chronologically before o. Because the canonical
// string form is fixed-width zero-padded, chronological order IS lexicographic
// order, so this is a plain string comparison.
func (c CalVer) Less(o CalVer) bool {
	return c.String() < o.String()
}
