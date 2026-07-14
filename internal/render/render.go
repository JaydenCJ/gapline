// Package render formats annotated log entries for humans and machines:
// the fixed-width delta column, gap marker lines, log-scale bars, ANSI
// color, JSON rows, and the --stats / --top summaries.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JaydenCJ/gapline/internal/scan"
	"github.com/JaydenCJ/gapline/internal/stats"
)

// Column and marker geometry. The delta column is right-aligned so the
// separator bar lines up down the whole stream; 8 runes fits everything
// the duration formatter can produce, up to "+365d23h".
const (
	deltaWidth  = 8
	barWidth    = 12
	markerWidth = 46
)

// ANSI sequences; only emitted when color is on.
const (
	ansiYellow  = "\x1b[33m"
	ansiRedBold = "\x1b[1;31m"
	ansiCyan    = "\x1b[36m"
	ansiReset   = "\x1b[0m"
)

// Options controls how entries are rendered.
type Options struct {
	// Threshold is the gap threshold: deltas at or above it are
	// highlighted and, when Markers is set, preceded by a marker line.
	Threshold time.Duration
	// Color enables ANSI coloring of the delta cell and markers.
	Color bool
	// Bars draws a log-scale bar column next to the delta.
	Bars bool
	// Markers inserts a "gap" ruler line before each gap.
	Markers bool
	// SinceStart shows elapsed-since-first instead of per-line deltas.
	SinceStart bool
	// JSON emits one JSON object per line instead of the text layout.
	JSON bool
}

// Renderer writes annotated lines to a single output.
type Renderer struct {
	w   io.Writer
	opt Options
}

// New returns a renderer over w.
func New(w io.Writer, opt Options) *Renderer {
	return &Renderer{w: w, opt: opt}
}

// Emit renders one entry.
func (r *Renderer) Emit(e scan.Entry) error {
	if r.opt.JSON {
		return r.emitJSON(e)
	}
	gap := e.HasTS && !e.First && e.Delta >= r.opt.Threshold
	if gap && r.opt.Markers {
		if err := r.marker(e.Delta); err != nil {
			return err
		}
	}
	cell := ""
	if e.HasTS {
		if r.opt.SinceStart {
			cell = Signed(e.Elapsed)
		} else {
			cell = Signed(e.Delta)
		}
	}
	col := pad(cell, deltaWidth)
	color := ""
	if r.opt.Color && e.HasTS && !e.First {
		color = colorFor(e.Delta, r.opt.Threshold)
	}
	var line string
	if r.opt.Bars {
		bar := ""
		if e.HasTS && !e.First {
			bar = Bar(e.Delta)
		}
		line = paint(color, col) + " " + paint(color, padRight(bar, barWidth)) + "│ " + e.Text
	} else {
		line = paint(color, col) + " │ " + e.Text
	}
	_, err := fmt.Fprintln(r.w, line)
	return err
}

// marker writes the gap ruler line that precedes a gap.
func (r *Renderer) marker(d time.Duration) error {
	head := "──── gap " + Duration(d) + " "
	fill := markerWidth - utf8.RuneCountInString(head)
	if fill < 4 {
		fill = 4
	}
	line := head + strings.Repeat("─", fill)
	if r.opt.Color {
		line = ansiRedBold + line + ansiReset
	}
	_, err := fmt.Fprintln(r.w, line)
	return err
}

// jsonRow is the machine-readable shape of one input line. Nullable
// fields are pointers: a continuation line has ts, delta_ms and
// elapsed_ms all null; the first timestamped line has delta_ms null
// because there is nothing to subtract yet.
type jsonRow struct {
	Line      int      `json:"line"`
	TS        *string  `json:"ts"`
	DeltaMS   *float64 `json:"delta_ms"`
	ElapsedMS *float64 `json:"elapsed_ms"`
	Gap       bool     `json:"gap"`
	Text      string   `json:"text"`
}

func (r *Renderer) emitJSON(e scan.Entry) error {
	row := jsonRow{Line: e.Line, Text: e.Text}
	if e.HasTS {
		ts := e.Time.Format(time.RFC3339Nano)
		row.TS = &ts
		el := ms3(e.Elapsed)
		row.ElapsedMS = &el
		if !e.First {
			d := ms3(e.Delta)
			row.DeltaMS = &d
			row.Gap = e.Delta >= r.opt.Threshold
		}
	}
	buf, err := json.Marshal(row)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(r.w, string(buf))
	return err
}

// ms3 converts a duration to milliseconds with microsecond precision.
func ms3(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000
}

// WriteTop prints the top-gaps summary that --top requests.
func WriteTop(w io.Writer, gaps []stats.Gap, color bool) error {
	title := fmt.Sprintf("top %d gaps", len(gaps))
	if len(gaps) == 1 {
		title = "top 1 gap"
	}
	if _, err := fmt.Fprintln(w, section(title)); err != nil {
		return err
	}
	if len(gaps) == 0 {
		_, err := fmt.Fprintln(w, "no deltas measured")
		return err
	}
	for _, g := range gaps {
		cell := pad(Signed(g.Delta), deltaWidth)
		if color {
			cell = ansiRedBold + cell + ansiReset
		}
		if _, err := fmt.Fprintf(w, "%s  line %-6d %s\n", cell, g.Line, truncate(g.Text, 60)); err != nil {
			return err
		}
	}
	return nil
}

// WriteStats prints the --stats summary block.
func WriteStats(w io.Writer, s stats.Summary, threshold time.Duration) error {
	lines := []string{
		section("gapline stats"),
		fmt.Sprintf("lines            %d (%d timestamped, %d without)",
			s.Lines, s.Timestamped, s.Lines-s.Timestamped),
		fmt.Sprintf("format           %s", orDash(s.Format)),
		fmt.Sprintf("span             %s", Duration(s.Span)),
	}
	if s.Deltas > 0 {
		lines = append(lines,
			fmt.Sprintf("p50 / p90 / p99  %s / %s / %s",
				Duration(s.P50), Duration(s.P90), Duration(s.P99)),
			fmt.Sprintf("max delta        %s (line %d)", Signed(s.Max), s.MaxLine))
	} else {
		lines = append(lines,
			"p50 / p90 / p99  - / - / -",
			"max delta        -")
	}
	lines = append(lines, fmt.Sprintf("gaps ≥ %-9s %d (total %s)",
		Duration(threshold), s.GapCount, Duration(s.GapTotal)))
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

// --- pure formatting helpers ------------------------------------------

// Duration renders a non-negative duration in 3-4 significant figures,
// picking the unit a human would: 842µs, 2.31ms, 23.1ms, 231ms, 4.113s,
// 23.1s, 4m07s, 2h30m, 3d02h. Values that round past a tier boundary are
// promoted ("999.7ms" becomes "1.000s"), so widths stay bounded.
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d == 0 {
		return "0s"
	}
	if d < time.Microsecond {
		return "<1µs"
	}
	if d < time.Millisecond {
		us := math.Round(float64(d) / float64(time.Microsecond))
		if us < 1000 {
			return fmt.Sprintf("%dµs", int(us))
		}
		d = time.Millisecond
	}
	if d < time.Second {
		ms := float64(d) / float64(time.Millisecond)
		switch {
		case ms < 9.995:
			return fmt.Sprintf("%.2fms", ms)
		case ms < 99.95:
			return fmt.Sprintf("%.1fms", ms)
		default:
			if v := math.Round(ms); v < 1000 {
				return fmt.Sprintf("%.0fms", v)
			}
			d = time.Second
		}
	}
	if d < 10*time.Second {
		s := float64(d) / float64(time.Second)
		if s < 9.9995 {
			return fmt.Sprintf("%.3fs", s)
		}
		d = 10 * time.Second
	}
	if d < time.Minute {
		s := float64(d) / float64(time.Second)
		if s < 59.95 {
			return fmt.Sprintf("%.1fs", s)
		}
		d = time.Minute
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", d/time.Minute, (d%time.Minute)/time.Second)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", d/time.Hour, (d%time.Hour)/time.Minute)
	}
	return fmt.Sprintf("%dd%02dh", d/(24*time.Hour), (d%(24*time.Hour))/time.Hour)
}

// Signed is Duration with an explicit sign, as shown in the delta column.
func Signed(d time.Duration) string {
	if d < 0 {
		return "-" + Duration(-d)
	}
	return "+" + Duration(d)
}

// barSteps is a 1-2-5 geometric ladder: each threshold a delta crosses
// adds one block, so bar length is effectively log-scale and two lines
// whose deltas differ by 10x differ by exactly three blocks.
var barSteps = []time.Duration{
	time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond,
	10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond,
	100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second,
}

// Bar renders the log-scale bar for a delta (negatives get no bar).
func Bar(d time.Duration) string {
	n := 0
	for _, step := range barSteps {
		if d >= step {
			n++
		}
	}
	return strings.Repeat("█", n)
}

// pad right-aligns s to w columns, counting runes rather than bytes so
// "µ" does not skew the column.
func pad(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// padRight left-aligns s to w columns.
func padRight(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// paint wraps s in an ANSI color when color is non-empty.
func paint(color, s string) string {
	if color == "" {
		return s
	}
	return color + s + ansiReset
}

// colorFor grades a delta against the threshold: red at or above it,
// yellow within a factor of two, cyan for negative (clock skew), and
// uncolored for the quiet majority.
func colorFor(d, threshold time.Duration) string {
	switch {
	case d < 0:
		return ansiCyan
	case d >= threshold:
		return ansiRedBold
	case d >= threshold/2:
		return ansiYellow
	default:
		return ""
	}
}

// section renders a "── title ──…──" header at marker width.
func section(title string) string {
	head := "── " + title + " "
	fill := markerWidth - utf8.RuneCountInString(head)
	if fill < 4 {
		fill = 4
	}
	return head + strings.Repeat("─", fill)
}

// truncate limits s to n runes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

// orDash substitutes "-" for an empty value in the stats block.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
