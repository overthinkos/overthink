package main

import (
	"testing"
	"time"
)

func TestOvVersion(t *testing.T) {
	// OvVersion reports the binary's STAMPED identity (BuildCalVer), never the
	// wall clock. Save/restore the package var around the test.
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()

	BuildCalVer = "2026.154.943"
	if got := OvVersion(); got != "2026.154.943" {
		t.Errorf("stamped OvVersion() = %q, want 2026.154.943", got)
	}

	BuildCalVer = ""
	if got := OvVersion(); got != "unknown" {
		t.Errorf("unstamped OvVersion() = %q, want %q", got, "unknown")
	}
	// "unknown" must be rejected by ParseCalVer so freshness treats it as oldest.
	if _, ok := ParseCalVer(OvVersion()); ok {
		t.Errorf("ParseCalVer(%q) parsed ok; an unstamped build must sort as oldest", OvVersion())
	}
}

func TestHostOvIsNewer(t *testing.T) {
	// hostOvIsNewer is the CalVer arbiter EnsureOvInVenue uses to decide whether
	// the EnsureOvInGuest auto/scp strategy adopts the guest's system ov or scp's
	// a host copy. Strictly newer → true; equal-or-newer venue → false (never
	// downgrade); unparseable venue → true; unparseable host → false.
	tests := []struct {
		name     string
		host     string
		venue    string
		expected bool
	}{
		{"host strictly newer", "2026.154.1027", "2026.154.943", true},
		{"venue strictly newer (pod ahead of host — DO NOT downgrade)", "2026.154.943", "2026.155.10", false},
		{"equal — not newer (no downgrade, no needless push)", "2026.154.943", "2026.154.943", false},
		{"venue absent → host wins", "2026.154.943", "", true},
		{"venue 'unknown' (unstamped) → host wins", "2026.154.943", "unknown", true},
		{"venue junk → host wins", "2026.154.943", "not-a-calver", true},
		{"host unparseable → cannot prove newer → false", "unknown", "2026.154.943", false},
		{"host newer across day boundary", "2026.155.1", "2026.154.2359", true},
		{"venue whitespace padded, equal → false", "2026.154.943", "  2026.154.943\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hostOvIsNewer(tt.host, tt.venue); got != tt.expected {
				t.Errorf("hostOvIsNewer(%q, %q) = %v, want %v", tt.host, tt.venue, got, tt.expected)
			}
		})
	}
}

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

func TestParseCalVer(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		year int
		day  int
		hhmm int
	}{
		{"2026.141.1530", true, 2026, 141, 1530},
		{"2026.1.0", true, 2026, 1, 0},
		{"  2026.366.2359  ", true, 2026, 366, 2359}, // trimmed
		{"4", false, 0, 0, 0},                        // legacy integer schema version
		{"", false, 0, 0, 0},
		{"2026.141", false, 0, 0, 0},       // too few parts
		{"2026.141.15.30", false, 0, 0, 0}, // too many parts
		{"x.y.z", false, 0, 0, 0},
		{"2026.0.0", false, 0, 0, 0},      // day < 1
		{"2026.367.0", false, 0, 0, 0},    // day > 366
		{"2026.141.2400", false, 0, 0, 0}, // hhmm > 2359
		{"1969.1.0", false, 0, 0, 0},      // year < 1970
	}
	for _, c := range cases {
		got, ok := ParseCalVer(c.in)
		if ok != c.ok {
			t.Errorf("ParseCalVer(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Year != c.year || got.Day != c.day || got.HHMM != c.hhmm {
			t.Errorf("ParseCalVer(%q) = %+v, want {%d %d %d}", c.in, got, c.year, c.day, c.hhmm)
		}
	}
}

func TestCalVerRoundTrip(t *testing.T) {
	for _, s := range []string{"2026.141.1530", "2026.1.0", "2026.366.2359"} {
		v, ok := ParseCalVer(s)
		if !ok {
			t.Fatalf("ParseCalVer(%q) failed", s)
		}
		if v.String() != s {
			t.Errorf("round-trip %q -> %q", s, v.String())
		}
	}
}

func TestCalVerLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"2026.112.522", "2026.114.1558", true},   // earlier day
		{"2026.114.1558", "2026.114.2207", true},  // same day, earlier time
		{"2026.141.1326", "2026.141.1530", true},  // drop-kdbx < calver-schema (HEAD)
		{"2026.141.1530", "2026.141.1530", false}, // equal is not less
		{"2026.141.1530", "2026.141.1326", false}, // reverse
		{"2025.366.2359", "2026.1.0", true},       // year boundary
	}
	for _, c := range cases {
		a, _ := ParseCalVer(c.a)
		b, _ := ParseCalVer(c.b)
		if got := a.Less(b); got != c.less {
			t.Errorf("(%s).Less(%s) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}
