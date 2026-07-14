// Tests for the aggregate collector: nearest-rank percentiles, gap
// accounting against a threshold, deterministic top-N ordering, and the
// per-file span segmentation.
package stats

import (
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/gapline/internal/scan"
)

// entryAt builds a timestamped entry with a given line, delta and text.
func entryAt(line int, first bool, at time.Time, delta time.Duration, text string) scan.Entry {
	return scan.Entry{
		Line: line, Text: text, HasTS: true, First: first,
		Time: at, Delta: delta, FormatName: "rfc3339",
	}
}

// fill feeds a First entry followed by one entry per delta, one second of
// base spacing plus the delta, so Span and percentiles are both exercised.
func fill(c *Collector, deltas ...time.Duration) {
	at := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	c.Add(entryAt(1, true, at, 0, "first"))
	for i, d := range deltas {
		at = at.Add(d)
		c.Add(entryAt(i+2, false, at, d, "line"))
	}
}

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestPercentilesUseNearestRank(t *testing.T) {
	c := New(time.Hour, false)
	fill(c, ms(1), ms(2), ms(3), ms(4), ms(5), ms(6), ms(7), ms(8), ms(9), ms(10))
	s := c.Summary()
	if s.P50 != ms(5) || s.P90 != ms(9) || s.P99 != ms(10) {
		t.Fatalf("p50/p90/p99 = %v/%v/%v, want 5ms/9ms/10ms", s.P50, s.P90, s.P99)
	}
	// A single delta is every percentile of itself.
	c = New(time.Hour, false)
	fill(c, ms(42))
	if s = c.Summary(); s.P50 != ms(42) || s.P99 != ms(42) || s.Max != ms(42) {
		t.Fatalf("percentiles of one delta: %+v", s)
	}
}

func TestGapCountingHonorsThresholdInclusively(t *testing.T) {
	c := New(ms(100), false)
	fill(c, ms(99), ms(100), ms(101), ms(5))
	s := c.Summary()
	if s.GapCount != 2 {
		t.Fatalf("gap count = %d, want 2 (>= is inclusive)", s.GapCount)
	}
	if want := ms(201); s.GapTotal != want {
		t.Fatalf("gap total = %v, want %v", s.GapTotal, want)
	}
}

func TestMaxTracksDeltaAndLineNumber(t *testing.T) {
	c := New(time.Hour, false)
	fill(c, ms(5), ms(4020), ms(7))
	s := c.Summary()
	if s.Max != ms(4020) || s.MaxLine != 3 {
		t.Fatalf("max = %v at line %d, want 4.02s at line 3", s.Max, s.MaxLine)
	}
}

func TestLinesWithoutTimestampsCountedButExcludedFromDeltas(t *testing.T) {
	c := New(time.Hour, false)
	fill(c, ms(10))
	c.Add(scan.Entry{Line: 3, Text: "  at handler.go:42"})
	s := c.Summary()
	if s.Lines != 3 || s.Timestamped != 2 || s.Deltas != 1 {
		t.Fatalf("lines/timestamped/deltas = %d/%d/%d, want 3/2/1", s.Lines, s.Timestamped, s.Deltas)
	}
}

func TestSummaryOnEmptyInputIsAllZeros(t *testing.T) {
	s := New(time.Second, false).Summary()
	if s.Lines != 0 || s.Deltas != 0 || s.P50 != 0 || s.Span != 0 {
		t.Fatalf("empty summary not zero: %+v", s)
	}
}

func TestFormatRecordsFirstDetectedName(t *testing.T) {
	c := New(time.Second, false)
	fill(c, ms(1))
	if s := c.Summary(); s.Format != "rfc3339" {
		t.Fatalf("format = %q, want rfc3339", s.Format)
	}
}

func TestSpanSumsPerFileSegments(t *testing.T) {
	// Two files fed through one collector: 10s covered plus 5s covered
	// is 15s of span — not the years between the two files' clocks.
	c := New(time.Hour, false)
	f1 := time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)
	c.Add(entryAt(1, true, f1, 0, "a"))
	c.Add(entryAt(2, false, f1.Add(10*time.Second), 10*time.Second, "b"))
	f2 := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Add(entryAt(1, true, f2, 0, "c"))
	c.Add(entryAt(2, false, f2.Add(5*time.Second), 5*time.Second, "d"))
	if s := c.Summary(); s.Span != 15*time.Second {
		t.Fatalf("span = %v, want 15s", s.Span)
	}
}

func TestTopOrdersByDeltaThenLine(t *testing.T) {
	c := New(time.Hour, true)
	fill(c, ms(5), ms(300), ms(5), ms(300), ms(700))
	top := c.Top(3)
	if len(top) != 3 {
		t.Fatalf("top has %d entries, want 3", len(top))
	}
	if top[0].Delta != ms(700) || top[1].Line != 3 || top[2].Line != 5 {
		t.Fatalf("wrong order: %+v", top)
	}
}

func TestTopWithoutKeepTopIsEmpty(t *testing.T) {
	c := New(time.Hour, false)
	fill(c, ms(700))
	if top := c.Top(5); len(top) != 0 {
		t.Fatalf("keepTop=false but Top returned %d records", len(top))
	}
}

func TestTopClampsWhenFewerRecordsThanRequested(t *testing.T) {
	c := New(time.Hour, true)
	fill(c, ms(1), ms(2))
	if top := c.Top(10); len(top) != 2 {
		t.Fatalf("top has %d entries, want 2", len(top))
	}
}

func TestClipBoundsRetainedTextWithoutSplittingUTF8(t *testing.T) {
	// A multibyte rune straddling the clip boundary must be dropped
	// whole, never cut into an invalid byte sequence.
	long := strings.Repeat("x", textKeep-1) + "あsom tail" + strings.Repeat("y", 50)
	got := clip(long)
	if len(got) > textKeep {
		t.Fatalf("clip kept %d bytes, cap is %d", len(got), textKeep)
	}
	if !strings.HasSuffix(got, "x") {
		t.Fatalf("clip split a rune: %q", got[len(got)-4:])
	}
	if short := "short"; clip(short) != short {
		t.Fatal("clip modified a short line")
	}
}

func TestNegativeDeltasNeverCountAsGaps(t *testing.T) {
	c := New(ms(100), false)
	fill(c, ms(-500), ms(200))
	if s := c.Summary(); s.GapCount != 1 {
		t.Fatalf("gap count = %d, want 1 (negative delta is skew, not a gap)", s.GapCount)
	}
}
