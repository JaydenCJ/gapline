// Package stats aggregates deltas across a run for the --stats summary
// and the --top largest-gaps report.
package stats

import (
	"sort"
	"time"
	"unicode/utf8"

	"github.com/JaydenCJ/gapline/internal/scan"
)

// textKeep bounds how much of a line the top-gaps report retains, so a
// log with megabyte lines cannot balloon memory through the collector.
const textKeep = 160

// Gap is one measured delta, kept for the --top report.
type Gap struct {
	Line  int
	Delta time.Duration
	Text  string
}

// Summary is the aggregate view printed by --stats.
type Summary struct {
	Lines       int
	Timestamped int
	Deltas      int
	Format      string
	// Span sums, per input file, the distance from its first to its
	// last timestamp — a total of covered time, not wall time between
	// unrelated files.
	Span     time.Duration
	P50      time.Duration
	P90      time.Duration
	P99      time.Duration
	Max      time.Duration
	MaxLine  int
	GapCount int
	GapTotal time.Duration
}

// Collector accumulates entries. Deltas are always kept (8 bytes each,
// needed for percentiles); per-line text is kept only when the top-gaps
// report was requested, and truncated even then.
type Collector struct {
	threshold time.Duration
	keepTop   bool

	lines       int
	timestamped int
	format      string
	deltas      []time.Duration
	records     []Gap
	maxDelta    time.Duration
	maxLine     int
	gapCount    int
	gapTotal    time.Duration

	span     time.Duration
	segFirst time.Time
	segLast  time.Time
	segSeen  bool
}

// New returns a collector. keepTop enables retaining per-line records
// for Top; without it only scalar aggregates are stored.
func New(threshold time.Duration, keepTop bool) *Collector {
	return &Collector{threshold: threshold, keepTop: keepTop}
}

// Add consumes one scanned entry. A First entry closes the previous
// file's span segment and opens a new one, so feeding several files
// through one collector keeps Span honest.
func (c *Collector) Add(e scan.Entry) {
	c.lines++
	if !e.HasTS {
		return
	}
	c.timestamped++
	if c.format == "" {
		c.format = e.FormatName
	}
	if e.First {
		c.closeSegment()
		c.segFirst = e.Time
		c.segSeen = true
	}
	c.segLast = e.Time
	if e.First {
		return
	}
	c.deltas = append(c.deltas, e.Delta)
	if e.Delta > c.maxDelta || c.maxLine == 0 {
		c.maxDelta = e.Delta
		c.maxLine = e.Line
	}
	if e.Delta >= c.threshold {
		c.gapCount++
		c.gapTotal += e.Delta
	}
	if c.keepTop {
		c.records = append(c.records, Gap{Line: e.Line, Delta: e.Delta, Text: clip(e.Text)})
	}
}

// Timestamped reports how many lines carried a timestamp.
func (c *Collector) Timestamped() int { return c.timestamped }

func (c *Collector) closeSegment() {
	if c.segSeen {
		if d := c.segLast.Sub(c.segFirst); d > 0 {
			c.span += d
		}
		c.segSeen = false
	}
}

// Top returns the n largest deltas, descending, ties broken by line
// number so output order is fully deterministic. It requires keepTop.
func (c *Collector) Top(n int) []Gap {
	out := make([]Gap, len(c.records))
	copy(out, c.records)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Delta != out[j].Delta {
			return out[i].Delta > out[j].Delta
		}
		return out[i].Line < out[j].Line
	})
	if n < len(out) {
		out = out[:n]
	}
	return out
}

// Summary closes the running span segment and returns the aggregates.
func (c *Collector) Summary() Summary {
	c.closeSegment()
	s := Summary{
		Lines:       c.lines,
		Timestamped: c.timestamped,
		Deltas:      len(c.deltas),
		Format:      c.format,
		Span:        c.span,
		Max:         c.maxDelta,
		MaxLine:     c.maxLine,
		GapCount:    c.gapCount,
		GapTotal:    c.gapTotal,
	}
	if len(c.deltas) > 0 {
		sorted := make([]time.Duration, len(c.deltas))
		copy(sorted, c.deltas)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		s.P50 = percentile(sorted, 50)
		s.P90 = percentile(sorted, 90)
		s.P99 = percentile(sorted, 99)
	}
	return s
}

// clip truncates retained text to textKeep bytes without splitting a
// UTF-8 sequence.
func clip(s string) string {
	if len(s) <= textKeep {
		return s
	}
	cut := textKeep
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// percentile is the nearest-rank percentile of a sorted slice.
func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
