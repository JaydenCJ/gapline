// Tests for timestamp format recognition and calendar validation. Every
// case uses fixed literal inputs, so results are identical on any machine
// in any timezone — tstamp never consults the wall clock.
package tstamp

import (
	"testing"
	"time"
)

// mustFind asserts that format name finds a timestamp in line and
// returns the match.
func mustFind(t *testing.T, name, line string) Match {
	t.Helper()
	f, ok := ByName(name)
	if !ok {
		t.Fatalf("unknown format %q", name)
	}
	m, ok := f.Find(line)
	if !ok {
		t.Fatalf("%s: no match in %q", name, line)
	}
	return m
}

// mustMiss asserts that format name finds nothing in line.
func mustMiss(t *testing.T, name, line string) {
	t.Helper()
	f, ok := ByName(name)
	if !ok {
		t.Fatalf("unknown format %q", name)
	}
	if m, found := f.Find(line); found {
		t.Fatalf("%s: unexpected match %v at %d in %q", name, m.Time, m.Start, line)
	}
}

func utc(y int, mo time.Month, d, h, mi, s, ns int) time.Time {
	return time.Date(y, mo, d, h, mi, s, ns, time.UTC)
}

func TestRFC3339ParsesZuluWithMillis(t *testing.T) {
	m := mustFind(t, "rfc3339", "2026-07-12T14:03:22.123Z GET /api 200")
	if want := utc(2026, 7, 12, 14, 3, 22, 123e6); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
	if m.Start != 0 {
		t.Fatalf("start = %d, want 0", m.Start)
	}
}

func TestRFC3339OffsetAndCaseVariants(t *testing.T) {
	// 14:03:22 at +09:00 is 05:03:22 UTC; deltas depend on this.
	cases := map[string]time.Time{
		"2026-07-12T14:03:22+09:00 boot": utc(2026, 7, 12, 5, 3, 22, 0),
		"2026-07-12T14:03:22-0700 boot":  utc(2026, 7, 12, 21, 3, 22, 0),
		"2026-07-12t14:03:22z msg":       utc(2026, 7, 12, 14, 3, 22, 0),
	}
	for line, want := range cases {
		if m := mustFind(t, "rfc3339", line); !m.Time.Equal(want) {
			t.Fatalf("%q: got %v, want %v", line, m.Time.UTC(), want)
		}
	}
}

func TestRFC3339FractionVariants(t *testing.T) {
	// Java and logback default to a comma decimal separator; tracing
	// systems emit full nanoseconds. Both must survive intact.
	cases := map[string]time.Time{
		"2026-07-12T14:03:22,5Z msg":         utc(2026, 7, 12, 14, 3, 22, 5e8),
		"2026-07-12T14:03:22.123456789Z msg": utc(2026, 7, 12, 14, 3, 22, 123456789),
	}
	for line, want := range cases {
		if m := mustFind(t, "rfc3339", line); !m.Time.Equal(want) {
			t.Fatalf("%q: got %v, want %v", line, m.Time, want)
		}
	}
}

func TestRFC3339RejectsCalendarNonsense(t *testing.T) {
	mustMiss(t, "rfc3339", "2026-13-12T14:03:22Z month 13")
	mustMiss(t, "rfc3339", "2026-02-30T14:03:22Z feb 30")
	mustMiss(t, "rfc3339", "2026-07-12T25:03:22Z hour 25")
	// A leading digit glues onto the year, so this is not a timestamp.
	mustMiss(t, "rfc3339", "92026-07-12T14:03:22Z")
}

func TestRFC3339AcceptsLeapSecond(t *testing.T) {
	// :60 appears in real NTP-adjacent logs; time.Date normalizes it.
	m := mustFind(t, "rfc3339", "2026-06-30T23:59:60Z leap")
	if want := utc(2026, 7, 1, 0, 0, 0, 0); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
}

func TestFindResumesPastInvalidCandidate(t *testing.T) {
	// The first date-shaped span is calendar-invalid; scanning must not
	// stop there but continue to the real timestamp later in the line.
	m := mustFind(t, "rfc3339", "bad 2026-99-12T14:03:22Z real 2026-07-12T14:03:22Z end")
	if want := utc(2026, 7, 12, 14, 3, 22, 0); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
}

func TestISOSpaceParsesPythonLoggingStyle(t *testing.T) {
	m := mustFind(t, "iso-space", "2026-07-12 14:03:22,123 INFO ready")
	if want := utc(2026, 7, 12, 14, 3, 22, 123e6); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
}

func TestSlashParsesGoLogDefault(t *testing.T) {
	m := mustFind(t, "slash", "2026/07/12 14:03:22 listening on 127.0.0.1:8080")
	if want := utc(2026, 7, 12, 14, 3, 22, 0); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
}

func TestCLFParsesAccessLogClock(t *testing.T) {
	line := `127.0.0.1 - - [12/Jul/2026:14:03:22 +0900] "GET / HTTP/1.1" 200 512`
	m := mustFind(t, "clf", line)
	if want := utc(2026, 7, 12, 5, 3, 22, 0); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time.UTC(), want)
	}
}

func TestSyslogParsesDoubleSpaceSingleDigitDay(t *testing.T) {
	// RFC 3164 pads single-digit days with a space: "Jul  2".
	m := mustFind(t, "syslog", "Jul  2 14:03:22 host cron[312]: session opened")
	if want := utc(2000, 7, 2, 14, 3, 22, 0); !m.Time.Equal(want) {
		t.Fatalf("got %v, want reference-year %v", m.Time, want)
	}
	mustMiss(t, "syslog", "Jul 32 14:03:22 host app: impossible day")
}

func TestKlogParsesHeaderAndSeverities(t *testing.T) {
	for _, sev := range []string{"I", "W", "E", "F"} {
		m := mustFind(t, "klog", sev+"0712 14:03:22.123456    1 main.go:42] started")
		if want := utc(2000, 7, 12, 14, 3, 22, 123456000); !m.Time.Equal(want) {
			t.Fatalf("%s: got %v, want %v", sev, m.Time, want)
		}
	}
}

func TestKlogOnlyMatchesAtLineStart(t *testing.T) {
	// The klog header is positional; a mid-line lookalike is payload.
	mustMiss(t, "klog", "saw I0712 14:03:22.123456 in the payload")
}

func TestDmesgParsesSecondsSinceBoot(t *testing.T) {
	m := mustFind(t, "dmesg", "[   12.345678] usb 1-1: new high-speed device")
	if want := time.Unix(12, 345678000).UTC(); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
}

func TestEpochParsesAllWidths(t *testing.T) {
	base := utc(2025, 7, 12, 13, 53, 22, 0) // 1752328402
	cases := map[string]time.Time{
		"1752328402 s":              base,
		"1752328402123 ms":          base.Add(123 * time.Millisecond),
		"1752328402123456 us":       base.Add(123456 * time.Microsecond),
		"1752328402123456789 ns":    base.Add(123456789 * time.Nanosecond),
		"ts=1752328402.5 fraction":  base.Add(500 * time.Millisecond),
		`{"ts":1752328402} in json`: base,
	}
	for line, want := range cases {
		m := mustFind(t, "epoch", line)
		if !m.Time.Equal(want) {
			t.Fatalf("%q: got %v, want %v", line, m.Time, want)
		}
	}
}

func TestEpochRejectsOutOfRangeAndUnanchored(t *testing.T) {
	// 9999999999 is year 2286: outside the [2000, 2100) guard.
	mustMiss(t, "epoch", "counter 9999999999 overflow")
	// Digits glued to a word are an identifier, not an instant.
	mustMiss(t, "epoch", "req-id abc1752328402 retry")
	// 11 digits is no recognized epoch width.
	mustMiss(t, "epoch", "serial 17523284029 assigned")
}

func TestTimeOnlyParsesBareWallClock(t *testing.T) {
	m := mustFind(t, "time-only", "14:03:22.123 worker 3 idle")
	if want := utc(2000, 1, 1, 14, 3, 22, 123e6); !m.Time.Equal(want) {
		t.Fatalf("got %v, want %v", m.Time, want)
	}
	// Match offsets are byte positions in the original line; the render
	// layer and tests rely on them being exact.
	if m = mustFind(t, "time-only", "at 14:03:22 ok"); m.Start != 3 {
		t.Fatalf("start = %d, want 3", m.Start)
	}
}

func TestTimeOnlyRejectsBadClockAndColonChains(t *testing.T) {
	mustMiss(t, "time-only", "99:03:22 impossible hour")
	// hh:mm:ss:frame chains (SMPTE timecode, some profilers) are not
	// wall clocks and must not produce bogus deltas.
	mustMiss(t, "time-only", "14:03:22:11 timecode")
}

func TestByNameKnowsEveryRegisteredFormat(t *testing.T) {
	for _, name := range Names() {
		f, ok := ByName(name)
		if !ok || f.Name != name {
			t.Fatalf("ByName(%q) = %v, %v", name, f, ok)
		}
	}
	if _, ok := ByName("carbon-dating"); ok {
		t.Fatal("ByName accepted an unknown format")
	}
}

func TestNamesMatchesPriorityOrder(t *testing.T) {
	names := Names()
	if len(names) != len(All()) {
		t.Fatalf("Names has %d entries, All has %d", len(names), len(All()))
	}
	if names[0] != "rfc3339" || names[len(names)-1] != "time-only" {
		t.Fatalf("unexpected priority order: %v", names)
	}
}

func TestEveryFormatFindsItsOwnExample(t *testing.T) {
	// The --list-formats examples are living documentation: each must
	// actually parse under its own format.
	for _, f := range All() {
		if _, ok := f.Find(f.Example); !ok {
			t.Fatalf("%s does not match its own example %q", f.Name, f.Example)
		}
	}
}

func TestFracNanosPadsAndTruncates(t *testing.T) {
	cases := map[string]int{
		"":            0,
		".5":          5e8,
		",5":          5e8,
		".123":        123e6,
		".123456789":  123456789,
		".1234567891": 123456789, // 10th digit is below nanosecond resolution
	}
	for in, want := range cases {
		if got := fracNanos(in); got != want {
			t.Fatalf("fracNanos(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestZoneParsesOffsetsToSeconds(t *testing.T) {
	cases := map[string]int{"": 0, "Z": 0, "+09:00": 9 * 3600, "-0700": -7 * 3600, "+05:30": 5*3600 + 1800}
	for in, want := range cases {
		if _, got := time.Date(2026, 1, 1, 0, 0, 0, 0, zone(in)).Zone(); got != want {
			t.Fatalf("zone(%q) offset = %d, want %d", in, got, want)
		}
	}
}
