// Package scan turns a stream of raw log lines into timestamped entries
// with inter-line deltas.
//
// The scanner autodetects the timestamp format on the first line that
// parses, then locks onto it: locked-format matches are preferred even if
// another format would match earlier in a line, which keeps mixed content
// (a stray ISO date inside a syslog message, say) from flapping the
// detection. If the locked format stops matching, full detection runs
// again, so streams that genuinely switch formats still work.
//
// The scanner also completes partial timestamps deterministically:
// yearless formats (syslog, klog) get a year rollover when the stream
// jumps backwards by more than ~10 months, and dateless wall clocks get a
// midnight rollover when the stream jumps backwards by more than 12
// hours. Smaller backwards jumps are preserved as negative deltas — they
// are real (clock skew, interleaved writers) and worth seeing.
package scan

import (
	"time"

	"github.com/JaydenCJ/gapline/internal/tstamp"
)

const (
	// yearTolerance decides yearless rollover: a backwards jump larger
	// than this means "January after December", not clock skew.
	yearTolerance = 300 * 24 * time.Hour
	// dayTolerance decides dateless rollover: a backwards jump larger
	// than this means the clock crossed midnight.
	dayTolerance = 12 * time.Hour
	// maxBumps caps the rollover loops so a pathological stream can
	// never spin; 400 covers more than a year of midnight crossings.
	maxBumps = 400
)

// Entry is one input line, annotated.
type Entry struct {
	// Line is the 1-based input line number.
	Line int
	// Text is the raw line without its trailing newline.
	Text string
	// HasTS reports whether a timestamp was found in the line. Lines
	// without one (stack traces, wrapped output) belong to the previous
	// entry and carry no delta.
	HasTS bool
	// First marks the first timestamped line of the stream; its Delta
	// is zero by definition.
	First bool
	// Time is the parsed instant, with yearless/dateless completion
	// applied. Only valid when HasTS.
	Time time.Time
	// Delta is Time minus the previous timestamped line's Time. It can
	// be negative. Only meaningful when HasTS and not First.
	Delta time.Duration
	// Elapsed is Time minus the first timestamped line's Time.
	Elapsed time.Duration
	// FormatName names the format that matched, e.g. "rfc3339".
	FormatName string
}

// Scanner carries the per-stream detection and delta state. One Scanner
// serves one stream; deltas never cross streams.
type Scanner struct {
	forced   *tstamp.Format
	locked   *tstamp.Format
	line     int
	seen     bool
	prev     time.Time
	first    time.Time
	yearBump int
	dayBump  int
}

// New returns a scanner. A non-nil forced format disables autodetection.
func New(forced *tstamp.Format) *Scanner {
	return &Scanner{forced: forced}
}

// FormatName reports the format currently in use ("" until one matches).
func (s *Scanner) FormatName() string {
	if s.forced != nil {
		return s.forced.Name
	}
	if s.locked != nil {
		return s.locked.Name
	}
	return ""
}

// Next consumes one line (without its newline) and returns its entry.
func (s *Scanner) Next(text string) Entry {
	s.line++
	e := Entry{Line: s.line, Text: text}
	f, m, ok := s.find(text)
	if !ok {
		return e
	}
	t := s.complete(f, m.Time)
	e.HasTS = true
	e.Time = t
	e.FormatName = f.Name
	if s.seen {
		e.Delta = t.Sub(s.prev)
	} else {
		e.First = true
		s.first = t
	}
	e.Elapsed = t.Sub(s.first)
	s.prev = t
	s.seen = true
	return e
}

// find picks the timestamp for a line: forced format only, else locked
// format if it still matches, else full leftmost detection (re-locking).
func (s *Scanner) find(text string) (*tstamp.Format, tstamp.Match, bool) {
	if s.forced != nil {
		m, ok := s.forced.Find(text)
		return s.forced, m, ok
	}
	if s.locked != nil {
		if m, ok := s.locked.Find(text); ok {
			return s.locked, m, true
		}
	}
	f, m, ok := Detect(text)
	if ok {
		s.locked = f
	}
	return f, m, ok
}

// complete applies year/midnight rollovers to partial timestamps. The
// accumulated bumps persist for the rest of the stream, so every later
// line is completed against the same, monotonically growing base.
func (s *Scanner) complete(f *tstamp.Format, t time.Time) time.Time {
	if f.Yearless {
		t = t.AddDate(s.yearBump, 0, 0)
		for i := 0; s.seen && s.prev.Sub(t) > yearTolerance && i < maxBumps; i++ {
			t = t.AddDate(1, 0, 0)
			s.yearBump++
		}
	}
	if f.Dateless {
		t = t.Add(time.Duration(s.dayBump) * 24 * time.Hour)
		for i := 0; s.seen && s.prev.Sub(t) > dayTolerance && i < maxBumps; i++ {
			t = t.Add(24 * time.Hour)
			s.dayBump++
		}
	}
	return t
}

// Detect runs full autodetection on one line: every format searches for
// its leftmost valid match, the earliest byte offset wins, and ties go to
// the higher-priority format (the order of tstamp.All). Leftmost-wins
// matters because the log's own stamp almost always leads the line, while
// payload text may quote other timestamps further right.
func Detect(text string) (*tstamp.Format, tstamp.Match, bool) {
	var (
		bestF *tstamp.Format
		bestM tstamp.Match
		found bool
	)
	for _, f := range tstamp.All() {
		m, ok := f.Find(text)
		if !ok {
			continue
		}
		if !found || m.Start < bestM.Start {
			bestF, bestM, found = f, m, true
		}
	}
	return bestF, bestM, found
}
