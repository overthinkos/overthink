package kit

// calver.go — the parsed CalVer schema-version type + the curated HEAD schema
// version, shared by charly core (the loader version gate) AND the compiled-in
// candy/plugin-migrate (the migration chain). It lives in kit (NOT spec) because
// spec already binds `CalVer = string` (the CUE wire scalar for `version:`
// fields); the PARSED struct is a different concept that the skill notes is kept
// out of spec for exactly that name collision. Core aliases these via
// `type CalVer = kit.CalVer` + `var ParseCalVer = kit.ParseCalVer` so every
// existing core call site is unchanged.

import (
	"fmt"
	"strconv"
	"strings"
)

// CalVer is a parsed YYYY.DDD.HHMM calendar version. The same format that
// ComputeCalVer (core) emits for image tags is, since the 2026-05 schema-versioning
// cutover, the schema-version stamp carried by every versioned YAML config. The
// migration chain (candy/plugin-migrate) is ordered by CalVer, and the load-time
// gate compares a file's CalVer against LatestSchemaVersion.
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
	if !calverAllDigits(parts[0]) || !calverAllDigits(parts[1]) || !calverAllDigits(parts[2]) {
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

// calverAllDigits reports whether s is non-empty and all ASCII digits. Inlined
// here (a 3-line primitive) so kit's CalVer parser has no dependency on the
// vmshared AllDigits helper, which would risk an import cycle.
func calverAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
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

// MustCalVer parses a compile-time-constant CalVer literal, panicking on a
// malformed value. Used for the migration registry's hardcoded step versions and
// the HEAD constant, so a bad literal fails fast at startup rather than silently
// mis-ordering the chain.
func MustCalVer(s string) CalVer {
	v, ok := ParseCalVer(s)
	if !ok {
		panic("kit: invalid CalVer literal " + s)
	}
	return v
}

// latestSchemaVersion is the curated HEAD CalVer — the schema-generation
// constant every versioned file is stamped to and the value the load-time gate
// requires. The candy/plugin-migrate registry's calver-schema step stamps to it
// and its last entry uses it as the Version (asserted equal by the registry's
// TestRegistryHeadMatchesLatest). Bump it — and append the matching MigrationStep
// in the candy — for each future schema cutover.
var latestSchemaVersion = MustCalVer("2026.174.1100")

// LatestSchemaVersion is the HEAD schema CalVer — the curated constant every
// versioned file is stamped to, and the value the load-time gate requires. Core
// exposes it via a thin shim of the same name; the candy migration registry reads
// it directly.
func LatestSchemaVersion() CalVer {
	return latestSchemaVersion
}
