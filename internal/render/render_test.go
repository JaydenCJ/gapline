// Tests for the human and machine renderers: duration tiers, the aligned
// delta column, gap markers, log-scale bars, ANSI grading, JSON rows, and
// the --stats / --top blocks. All assertions are on exact strings.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JaydenCJ/gapline/internal/scan"
	"github.com/JaydenCJ/gapline/internal/stats"
)

func ts(h, m, s, msec int) time.Time {
	return time.Date(2026, 7, 12, h, m, s, msec*1e6, time.UTC)
}

func TestDurationPicksHumanUnits(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "0s",
		500 * time.Nanosecond:         "<1µs",
		842 * time.Microsecond:        "842µs",
		2310 * time.Microsecond:       "2.31ms",
		23100 * time.Microsecond:      "23.1ms",
		231 * time.Millisecond:        "231ms",
		4113 * time.Millisecond:       "4.113s",
		23100 * time.Millisecond:      "23.1s",
		4*time.Minute + 7*time.Second: "4m07s",
		2*time.Hour + 30*time.Minute:  "2h30m",
		74 * time.Hour:                "3d02h",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Fatalf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
	// Signed adds the explicit sign the delta column shows.
	if got := Signed(2 * time.Second); got != "+2.000s" {
		t.Fatalf("Signed(+2s) = %q", got)
	}
	if got := Signed(-2 * time.Second); got != "-2.000s" {
		t.Fatalf("Signed(-2s) = %q", got)
	}
}

func TestDurationPromotesAcrossTierBoundaries(t *testing.T) {
	// Values that round up past a unit boundary must be promoted, so the
	// column never shows the contradiction "1000ms".
	cases := map[time.Duration]string{
		9995 * time.Microsecond:    "10.0ms",
		999600 * time.Microsecond:  "1.000s",
		9999500 * time.Microsecond: "10.0s",
		59960 * time.Millisecond:   "1m00s",
		999999 * time.Nanosecond:   "1.00ms",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Fatalf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBarIsLogScale(t *testing.T) {
	if got := Bar(500 * time.Microsecond); got != "" {
		t.Fatalf("sub-ms bar = %q, want empty", got)
	}
	// A 10x delta difference is exactly three blocks on the 1-2-5 ladder.
	ten, hundred := Bar(10*time.Millisecond), Bar(100*time.Millisecond)
	if len([]rune(ten)) != 4 || len([]rune(hundred)) != 7 {
		t.Fatalf("bars = %q (%d), %q (%d); want 4 and 7 blocks",
			ten, len([]rune(ten)), hundred, len([]rune(hundred)))
	}
	if got := Bar(9 * time.Second); len([]rune(got)) != len(barSteps) {
		t.Fatalf("huge delta bar = %q, want full ladder", got)
	}
}

func TestPadCountsRunesNotBytes(t *testing.T) {
	// "842µs" is 6 bytes but 5 runes; the column must align on runes.
	if got := pad("842µs", 8); got != "   842µs" {
		t.Fatalf("pad = %q", got)
	}
}

func TestColorForGradesAgainstThreshold(t *testing.T) {
	th := time.Second
	cases := map[time.Duration]string{
		-time.Second:           ansiCyan,
		2 * time.Second:        ansiRedBold,
		time.Second:            ansiRedBold, // inclusive at the threshold
		600 * time.Millisecond: ansiYellow,
		100 * time.Millisecond: "",
	}
	for d, want := range cases {
		if got := colorFor(d, th); got != want {
			t.Fatalf("colorFor(%v) = %q, want %q", d, got, want)
		}
	}
}

func emit(t *testing.T, opt Options, entries ...scan.Entry) string {
	t.Helper()
	var buf bytes.Buffer
	r := New(&buf, opt)
	for _, e := range entries {
		if err := r.Emit(e); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	return buf.String()
}

func TestEmitAlignsDeltaColumn(t *testing.T) {
	out := emit(t, Options{Threshold: time.Second, Markers: true},
		scan.Entry{Line: 1, Text: "boot", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "ready", HasTS: true, Time: ts(14, 0, 0, 25), Delta: 25 * time.Millisecond},
	)
	want := "     +0s │ boot\n +25.0ms │ ready\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestEmitInsertsGapMarkerBeforeTheGapLine(t *testing.T) {
	out := emit(t, Options{Threshold: time.Second, Markers: true},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "b", HasTS: true, Time: ts(14, 0, 4, 20), Delta: 4020 * time.Millisecond},
	)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], "──── gap 4.020s ") {
		t.Fatalf("marker line = %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "│ b") || !strings.Contains(lines[2], "+4.020s") {
		t.Fatalf("gap line = %q", lines[2])
	}
	// With Markers off the same gap renders without a ruler line.
	quiet := emit(t, Options{Threshold: time.Second},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "b", HasTS: true, Time: ts(14, 0, 5, 0), Delta: 5 * time.Second},
	)
	if strings.Contains(quiet, "gap") {
		t.Fatalf("marker leaked with Markers=false: %q", quiet)
	}
}

func TestEmitBlankColumnForContinuationLines(t *testing.T) {
	out := emit(t, Options{Threshold: time.Second},
		scan.Entry{Line: 1, Text: "  at handler.go:42"},
	)
	if want := "         │   at handler.go:42\n"; out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestEmitSinceStartShowsElapsed(t *testing.T) {
	out := emit(t, Options{Threshold: time.Hour, SinceStart: true},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "b", HasTS: true, Time: ts(14, 0, 7, 0),
			Delta: 7 * time.Second, Elapsed: 7 * time.Second},
	)
	if !strings.Contains(out, "+7.000s │ b") {
		t.Fatalf("elapsed column missing: %q", out)
	}
}

func TestEmitBarsColumn(t *testing.T) {
	out := emit(t, Options{Threshold: time.Hour, Bars: true},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "b", HasTS: true, Time: ts(14, 0, 0, 50), Delta: 50 * time.Millisecond},
	)
	if !strings.Contains(out, "██████") || strings.Contains(out, "███████") {
		t.Fatalf("50ms should be exactly 6 blocks: %q", out)
	}
}

func TestEmitColorGradesTheDeltaCell(t *testing.T) {
	out := emit(t, Options{Threshold: time.Second, Color: true, Markers: true},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "b", HasTS: true, Time: ts(14, 0, 2, 0), Delta: 2 * time.Second},
	)
	if !strings.Contains(out, ansiRedBold) || !strings.Contains(out, ansiReset) {
		t.Fatalf("red grading missing: %q", out)
	}
	plain := emit(t, Options{Threshold: time.Second, Markers: true},
		scan.Entry{Line: 1, Text: "a", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
	)
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("ANSI leaked with Color=false: %q", plain)
	}
}

func TestEmitJSONRowShapes(t *testing.T) {
	out := emit(t, Options{Threshold: time.Second, JSON: true},
		scan.Entry{Line: 1, Text: "boot", HasTS: true, First: true, Time: ts(14, 0, 0, 0)},
		scan.Entry{Line: 2, Text: "  continuation"},
		scan.Entry{Line: 3, Text: "gap", HasTS: true, Time: ts(14, 0, 4, 20),
			Delta: 4020 * time.Millisecond, Elapsed: 4020 * time.Millisecond},
	)
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 3 {
		t.Fatalf("got %d rows: %q", len(rows), out)
	}
	var first, cont, gap map[string]any
	for i, dst := range []*map[string]any{&first, &cont, &gap} {
		if err := json.Unmarshal([]byte(rows[i]), dst); err != nil {
			t.Fatalf("row %d is not JSON: %v", i+1, err)
		}
	}
	if first["delta_ms"] != nil || first["ts"] == nil || first["elapsed_ms"] != 0.0 {
		t.Fatalf("first row wrong: %v", first)
	}
	if cont["ts"] != nil || cont["delta_ms"] != nil || cont["gap"] != false {
		t.Fatalf("continuation row wrong: %v", cont)
	}
	if gap["delta_ms"] != 4020.0 || gap["gap"] != true || gap["line"] != 3.0 {
		t.Fatalf("gap row wrong: %v", gap)
	}
}

func TestWriteStatsRendersEveryField(t *testing.T) {
	var buf bytes.Buffer
	err := WriteStats(&buf, stats.Summary{
		Lines: 10, Timestamped: 9, Deltas: 8, Format: "rfc3339",
		Span: 90 * time.Second, P50: 5 * time.Millisecond,
		P90: 40 * time.Millisecond, P99: 2 * time.Second,
		Max: 4020 * time.Millisecond, MaxLine: 7,
		GapCount: 2, GapTotal: 6 * time.Second,
	}, time.Second)
	if err != nil {
		t.Fatalf("WriteStats: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"gapline stats", "10 (9 timestamped, 1 without)", "rfc3339",
		"1m30s", "5.00ms / 40.0ms / 2.000s", "+4.020s (line 7)",
		"gaps ≥ 1.000s", "2 (total 6.000s)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats block missing %q:\n%s", want, out)
		}
	}
}

func TestWriteStatsWithNoDeltasShowsDashes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStats(&buf, stats.Summary{Lines: 3}, time.Second); err != nil {
		t.Fatalf("WriteStats: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "- / - / -") || !strings.Contains(out, "format           -") {
		t.Fatalf("empty stats block wrong:\n%s", out)
	}
}

func TestWriteTopListsGapsAndHandlesEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := WriteTop(&buf, []stats.Gap{
		{Line: 7, Delta: 4020 * time.Millisecond, Text: "slow query"},
		{Line: 2, Delta: 300 * time.Millisecond, Text: "warm cache"},
	}, false)
	if err != nil {
		t.Fatalf("WriteTop: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "top 2 gaps") ||
		!strings.Contains(out, "+4.020s  line 7") ||
		!strings.Contains(out, "slow query") {
		t.Fatalf("top block wrong:\n%s", out)
	}
	// A single gap gets a singular header — never "top 1 gaps".
	buf.Reset()
	if err := WriteTop(&buf, []stats.Gap{{Line: 7, Delta: time.Second, Text: "x"}}, false); err != nil {
		t.Fatalf("WriteTop(one): %v", err)
	}
	if !strings.Contains(buf.String(), "top 1 gap ") {
		t.Fatalf("singular top header wrong:\n%s", buf.String())
	}
	buf.Reset()
	if err := WriteTop(&buf, nil, false); err != nil {
		t.Fatalf("WriteTop(empty): %v", err)
	}
	if !strings.Contains(buf.String(), "no deltas measured") {
		t.Fatalf("empty top block wrong:\n%s", buf.String())
	}
	// The text column truncates on runes with an ellipsis, never mid-rune.
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate short = %q", got)
	}
	if got := truncate("exactly-ten", 10); got != "exactly-t…" || len([]rune(got)) != 10 {
		t.Fatalf("truncate cut = %q", got)
	}
}
