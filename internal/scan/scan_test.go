// Tests for the stream scanner: format autodetection and locking, delta
// arithmetic, and the deterministic completion of yearless and dateless
// timestamps (midnight and New Year rollovers).
package scan

import (
	"testing"
	"time"

	"github.com/JaydenCJ/gapline/internal/tstamp"
)

// feed runs a fresh autodetecting scanner over lines and returns entries.
func feed(t *testing.T, lines ...string) []Entry {
	t.Helper()
	s := New(nil)
	out := make([]Entry, 0, len(lines))
	for _, l := range lines {
		out = append(out, s.Next(l))
	}
	return out
}

func TestFirstTimestampedLineIsFirstWithZeroDelta(t *testing.T) {
	es := feed(t, "2026-07-12T14:03:22Z boot")
	e := es[0]
	if !e.HasTS || !e.First || e.Delta != 0 || e.Elapsed != 0 || e.Line != 1 {
		t.Fatalf("unexpected first entry: %+v", e)
	}
	if e.FormatName != "rfc3339" {
		t.Fatalf("format = %q, want rfc3339", e.FormatName)
	}
}

func TestDeltaBetweenConsecutiveTimestampedLines(t *testing.T) {
	es := feed(t,
		"2026-07-12T14:03:22.120Z a",
		"2026-07-12T14:03:26.140Z b",
	)
	if want := 4020 * time.Millisecond; es[1].Delta != want {
		t.Fatalf("delta = %v, want %v", es[1].Delta, want)
	}
	if es[1].Elapsed != es[1].Delta {
		t.Fatalf("elapsed = %v, want %v", es[1].Elapsed, es[1].Delta)
	}
}

func TestContinuationLinesCarryNoTimestampButAdvanceNumbers(t *testing.T) {
	es := feed(t,
		"2026-07-12T14:03:22Z panic: boom",
		"    at handler.go:42",
		"2026-07-12T14:03:23Z recovered",
	)
	if es[1].HasTS {
		t.Fatalf("stack-trace line got a timestamp: %+v", es[1])
	}
	if es[1].Line != 2 || es[2].Line != 3 {
		t.Fatalf("line numbers wrong: %d, %d", es[1].Line, es[2].Line)
	}
	// The delta on line 3 skips the continuation line entirely.
	if want := time.Second; es[2].Delta != want {
		t.Fatalf("delta = %v, want %v", es[2].Delta, want)
	}
}

func TestSmallBackwardsJumpStaysNegative(t *testing.T) {
	// Interleaved writers produce small backwards jumps; they are real
	// and must survive as negative deltas, not be "fixed" by rollover.
	es := feed(t,
		"14:03:22 writer A",
		"14:03:20 writer B",
	)
	if want := -2 * time.Second; es[1].Delta != want {
		t.Fatalf("delta = %v, want %v", es[1].Delta, want)
	}
}

func TestMidnightRolloverOnBareWallClock(t *testing.T) {
	es := feed(t,
		"23:59:59 closing shop",
		"00:00:01 next day",
		"00:00:05 and it sticks",
	)
	if want := 2 * time.Second; es[1].Delta != want {
		t.Fatalf("rollover delta = %v, want %v", es[1].Delta, want)
	}
	// The day bump persists: later lines complete against the new day.
	if want := 4 * time.Second; es[2].Delta != want {
		t.Fatalf("post-rollover delta = %v, want %v", es[2].Delta, want)
	}
}

func TestBackwardsJumpIsNotMistakenForMidnight(t *testing.T) {
	es := feed(t,
		"23:59:59 day one ends",
		"23:59:58 day two almost over",
		"00:00:02 day three",
	)
	// 23:59:59 -> 23:59:58 is a 1s backwards jump: NOT a rollover.
	if want := -time.Second; es[1].Delta != want {
		t.Fatalf("delta 2 = %v, want %v", es[1].Delta, want)
	}
	// 23:59:58 -> 00:00:02 crosses midnight: +4s.
	if want := 4 * time.Second; es[2].Delta != want {
		t.Fatalf("delta 3 = %v, want %v", es[2].Delta, want)
	}
}

func TestNewYearRolloverOnSyslog(t *testing.T) {
	es := feed(t,
		"Dec 31 23:59:59 host app: closing the year",
		"Jan  1 00:00:01 host app: happy new year",
	)
	if want := 2 * time.Second; es[1].Delta != want {
		t.Fatalf("delta = %v, want %v", es[1].Delta, want)
	}
}

func TestScannerLocksOntoFirstDetectedFormat(t *testing.T) {
	// Line 2 contains an epoch earlier in the line than the rfc3339
	// stamp; the lock must keep preferring rfc3339.
	es := feed(t,
		"2026-07-12T14:03:22Z start",
		"1752328402 2026-07-12T14:03:25Z payload quotes an epoch",
	)
	if es[1].FormatName != "rfc3339" {
		t.Fatalf("lock lost: format = %q", es[1].FormatName)
	}
	if want := 3 * time.Second; es[1].Delta != want {
		t.Fatalf("delta = %v, want %v", es[1].Delta, want)
	}
}

func TestDetectPrefersLeftmostMatchWithoutALock(t *testing.T) {
	f, m, ok := Detect("ts=1752328402 at 2026-07-12T14:03:22Z")
	if !ok || f.Name != "epoch" {
		t.Fatalf("got %v (ok=%v), want epoch", f, ok)
	}
	if m.Start != 3 {
		t.Fatalf("start = %d, want 3", m.Start)
	}
}

func TestDetectBreaksPositionTiesByPriority(t *testing.T) {
	// syslog and time-only both live in "Jul 12 14:03:22"; syslog starts
	// earlier and is also higher priority.
	f, _, ok := Detect("Jul 12 14:03:22 host app: hello")
	if !ok || f.Name != "syslog" {
		t.Fatalf("got %v (ok=%v), want syslog", f, ok)
	}
}

func TestScannerFallsBackWhenLockedFormatDisappears(t *testing.T) {
	// A supervisor that prefixes rfc3339 hands off to a child that logs
	// syslog style. Year completion snaps the yearless timestamp onto
	// the established stream, so the delta stays sane.
	es := feed(t,
		"2026-07-12T14:03:22Z supervisor: starting child",
		"Jul 12 14:03:25 child: ready",
	)
	if es[1].FormatName != "syslog" {
		t.Fatalf("format = %q, want syslog", es[1].FormatName)
	}
	if want := 3 * time.Second; es[1].Delta != want {
		t.Fatalf("delta = %v, want %v", es[1].Delta, want)
	}
}

func TestForcedFormatDisablesAutodetection(t *testing.T) {
	f, _ := tstamp.ByName("time-only")
	s := New(f)
	e1 := s.Next("2026-07-12T14:03:22Z ignored date")
	e2 := s.Next("2026-07-12T14:03:25Z ignored date")
	if e1.FormatName != "time-only" || e2.FormatName != "time-only" {
		t.Fatalf("forced format not used: %q, %q", e1.FormatName, e2.FormatName)
	}
	if want := 3 * time.Second; e2.Delta != want {
		t.Fatalf("delta = %v, want %v", e2.Delta, want)
	}
	// A forced format never falls back: lines it cannot parse stay bare.
	clf, _ := tstamp.ByName("clf")
	if e := New(clf).Next("2026-07-12T14:03:22Z not an access log"); e.HasTS {
		t.Fatalf("forced clf matched a non-clf line: %+v", e)
	}
}

func TestFormatNameEmptyUntilDetectionThenLocked(t *testing.T) {
	s := New(nil)
	if got := s.FormatName(); got != "" {
		t.Fatalf("FormatName before input = %q, want empty", got)
	}
	s.Next("no timestamp here")
	if got := s.FormatName(); got != "" {
		t.Fatalf("FormatName after miss = %q, want empty", got)
	}
	s.Next("I0712 14:03:22.000001 1 main.go:1] up")
	if got := s.FormatName(); got != "klog" {
		t.Fatalf("FormatName = %q, want klog", got)
	}
}

func TestDmesgDeltasFromSecondsSinceBoot(t *testing.T) {
	es := feed(t,
		"[    0.000000] Linux version 6.1.0",
		"[    2.500000] usb 1-1: new device",
		"[   12.500000] EXT4-fs mounted",
	)
	if want := 2500 * time.Millisecond; es[1].Delta != want {
		t.Fatalf("delta 2 = %v, want %v", es[1].Delta, want)
	}
	if want := 10 * time.Second; es[2].Delta != want {
		t.Fatalf("delta 3 = %v, want %v", es[2].Delta, want)
	}
}

func TestElapsedMeasuresFromStreamStart(t *testing.T) {
	es := feed(t,
		"14:00:00 t0",
		"14:00:01 t1",
		"14:00:04 t2",
	)
	if want := 4 * time.Second; es[2].Elapsed != want {
		t.Fatalf("elapsed = %v, want %v", es[2].Elapsed, want)
	}
}
