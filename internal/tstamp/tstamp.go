// Package tstamp defines the timestamp formats gapline recognizes and
// turns matched spans of a log line into absolute instants.
//
// Every format is a compiled regular expression plus a builder that
// validates calendar ranges, so "99:99:99" in a payload never becomes a
// timestamp. Formats that omit the year (syslog, klog) or the whole date
// (bare wall clocks) parse against a fixed reference — year 2000, chosen
// because it is a leap year — and are completed by the stream scanner.
// That keeps every run of gapline deterministic regardless of the wall
// clock it runs under.
package tstamp

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Match is one recognized timestamp inside a line.
type Match struct {
	// Time is the parsed instant. For Yearless formats the year is the
	// reference year 2000; for Dateless formats the date is 2000-01-01.
	Time time.Time
	// Start and End delimit the matched span in the original line (byte
	// offsets). Detection prefers the leftmost span across all formats.
	Start int
	End   int
}

// Format is one recognizable timestamp shape.
type Format struct {
	Name    string
	Example string
	Note    string
	// Yearless formats (syslog, klog) carry month+day but no year; the
	// scanner infers year rollovers from stream order.
	Yearless bool
	// Dateless formats (bare wall clocks) carry no date at all; the
	// scanner infers midnight rollovers from stream order.
	Dateless bool

	re    *regexp.Regexp
	build func(sub []string) (time.Time, bool)
}

// Find returns the leftmost calendar-valid occurrence of the format in
// line. Spans that look right but fail range validation (month 13,
// hour 25, an epoch outside 2000-2099) are skipped, and scanning resumes
// after them, so junk numbers never shadow a real timestamp further on.
func (f *Format) Find(line string) (Match, bool) {
	offset := 0
	for offset <= len(line) {
		idx := f.re.FindStringSubmatchIndex(line[offset:])
		if idx == nil {
			return Match{}, false
		}
		n := f.re.NumSubexp() + 1
		sub := make([]string, n)
		for i := 0; i < n; i++ {
			lo, hi := idx[2*i], idx[2*i+1]
			if lo >= 0 {
				sub[i] = line[offset+lo : offset+hi]
			}
		}
		if t, ok := f.build(sub); ok {
			return Match{Time: t, Start: offset + idx[2], End: offset + idx[3]}, true
		}
		offset += idx[2] + 1
	}
	return Match{}, false
}

// All returns every known format in detection-priority order: when two
// formats match a line at the same byte offset, the earlier entry wins.
func All() []*Format { return formats }

// ByName looks a format up by its --format name.
func ByName(name string) (*Format, bool) {
	for _, f := range formats {
		if f.Name == name {
			return f, true
		}
	}
	return nil, false
}

// Names returns all format names in priority order, for error messages.
func Names() []string {
	out := make([]string, len(formats))
	for i, f := range formats {
		out[i] = f.Name
	}
	return out
}

// --- builders and helpers ---------------------------------------------

const monthAlt = "Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec"

var monthNum = map[string]time.Month{
	"Jan": time.January, "Feb": time.February, "Mar": time.March,
	"Apr": time.April, "May": time.May, "Jun": time.June,
	"Jul": time.July, "Aug": time.August, "Sep": time.September,
	"Oct": time.October, "Nov": time.November, "Dec": time.December,
}

// atoi converts a digits-only string; the regexes guarantee the input.
func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// fracNanos converts a ".123" / ",123456" fraction-of-a-second capture
// into nanoseconds. An empty capture yields 0.
func fracNanos(s string) int {
	if s == "" {
		return 0
	}
	digits := s[1:] // strip the leading '.' or ','
	for len(digits) < 9 {
		digits += "0"
	}
	return atoi(digits[:9])
}

// validClock reports whether h:m:s is a plausible wall-clock reading.
// Second 60 is accepted so leap-second logs still parse; time.Date
// normalizes it into the next minute.
func validClock(h, m, s int) bool {
	return h < 24 && m < 60 && s <= 60
}

// validDate reports whether y-mo-d is a real calendar day.
func validDate(y int, mo time.Month, d int) bool {
	if mo < time.January || mo > time.December || d < 1 || d > 31 {
		return false
	}
	t := time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
	return t.Year() == y && t.Month() == mo && t.Day() == d
}

// zone converts "Z", "+09:00" or "-0700" into a fixed location. An empty
// capture means the log carried no offset; gapline treats it as UTC,
// which is harmless because only deltas within one stream matter.
func zone(s string) *time.Location {
	if s == "" || s == "Z" || s == "z" {
		return time.UTC
	}
	sign := 1
	if s[0] == '-' {
		sign = -1
	}
	rest := strings.ReplaceAll(s[1:], ":", "")
	hh := atoi(rest[:2])
	mm := atoi(rest[2:])
	return time.FixedZone(s, sign*(hh*3600+mm*60))
}

// dateTime assembles and validates a full civil timestamp.
func dateTime(y int, mo time.Month, d, h, mi, s, ns int, loc *time.Location) (time.Time, bool) {
	if !validDate(y, mo, d) || !validClock(h, mi, s) {
		return time.Time{}, false
	}
	return time.Date(y, mo, d, h, mi, s, ns, loc), true
}

// --- the format registry ----------------------------------------------

// Epoch guard: only instants in [2000-01-01, 2100-01-01) are accepted,
// so ticket numbers and phone numbers do not masquerade as timestamps.
const (
	epochMin = 946684800  // 2000-01-01T00:00:00Z
	epochMax = 4102444800 // 2100-01-01T00:00:00Z
)

var formats = []*Format{
	{
		Name:    "rfc3339",
		Example: "2026-07-12T14:03:22.123Z",
		Note:    "ISO 8601 / RFC 3339; optional offset, 1-9 fraction digits",
		re:      regexp.MustCompile(`(?:^|[^0-9])((\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})([.,]\d{1,9})?(Z|z|[+-]\d{2}:?\d{2})?)\b`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(atoi(sub[2]), time.Month(atoi(sub[3])), atoi(sub[4]),
				atoi(sub[5]), atoi(sub[6]), atoi(sub[7]), fracNanos(sub[8]), zone(sub[9]))
		},
	},
	{
		Name:    "iso-space",
		Example: "2026-07-12 14:03:22,123",
		Note:    "space-separated ISO; dot or comma fraction (Python logging)",
		re:      regexp.MustCompile(`(?:^|[^0-9])((\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})([.,]\d{1,9})?)\b`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(atoi(sub[2]), time.Month(atoi(sub[3])), atoi(sub[4]),
				atoi(sub[5]), atoi(sub[6]), atoi(sub[7]), fracNanos(sub[8]), time.UTC)
		},
	},
	{
		Name:    "slash",
		Example: "2026/07/12 14:03:22",
		Note:    "Go standard-library log default",
		re:      regexp.MustCompile(`(?:^|[^0-9])((\d{4})/(\d{2})/(\d{2}) (\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?)\b`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(atoi(sub[2]), time.Month(atoi(sub[3])), atoi(sub[4]),
				atoi(sub[5]), atoi(sub[6]), atoi(sub[7]), fracNanos(sub[8]), time.UTC)
		},
	},
	{
		Name:    "clf",
		Example: "[12/Jul/2026:14:03:22 +0900]",
		Note:    "Apache / nginx access-log clock",
		re:      regexp.MustCompile(`\[((\d{2})/(` + monthAlt + `)/(\d{4}):(\d{2}):(\d{2}):(\d{2}) ([+-]\d{4}))\]`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(atoi(sub[4]), monthNum[sub[3]], atoi(sub[2]),
				atoi(sub[5]), atoi(sub[6]), atoi(sub[7]), 0, zone(sub[8]))
		},
	},
	{
		Name:     "syslog",
		Example:  "Jul 12 14:03:22",
		Note:     "RFC 3164; yearless, year inferred from stream order",
		Yearless: true,
		re:       regexp.MustCompile(`(?:^|[^A-Za-z0-9])((` + monthAlt + `) {1,2}(\d{1,2}) (\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?)\b`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(2000, monthNum[sub[2]], atoi(sub[3]),
				atoi(sub[4]), atoi(sub[5]), atoi(sub[6]), fracNanos(sub[7]), time.UTC)
		},
	},
	{
		Name:     "klog",
		Example:  "I0712 14:03:22.123456",
		Note:     "glog / klog header; yearless",
		Yearless: true,
		re:       regexp.MustCompile(`^[IWEF]((\d{2})(\d{2}) (\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?)\b`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(2000, time.Month(atoi(sub[2])), atoi(sub[3]),
				atoi(sub[4]), atoi(sub[5]), atoi(sub[6]), fracNanos(sub[7]), time.UTC)
		},
	},
	{
		Name:    "dmesg",
		Example: "[   12.345678]",
		Note:    "kernel seconds-since-boot; line-anchored, deltas only",
		re:      regexp.MustCompile(`^(\[ *(\d{1,9})\.(\d{3,6})\])`),
		build: func(sub []string) (time.Time, bool) {
			return time.Unix(int64(atoi(sub[2])), int64(fracNanos("."+sub[3]))).UTC(), true
		},
	},
	{
		Name:    "epoch",
		Example: "1752328402.123",
		Note:    "10/13/16/19-digit s·ms·µs·ns; anchored and range-guarded",
		re:      regexp.MustCompile(`(?:^|[\s\["'(=:,>])((\d{19}|\d{16}|\d{13}|\d{10})(\.\d{1,9})?)(?:[\s\]"'),<;:}.]|$)`),
		build: func(sub []string) (time.Time, bool) {
			v, err := strconv.ParseInt(sub[2], 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			var perSec int64
			switch len(sub[2]) {
			case 10:
				perSec = 1
			case 13:
				perSec = 1e3
			case 16:
				perSec = 1e6
			default:
				perSec = 1e9
			}
			sec := v / perSec
			if sec < epochMin || sec >= epochMax {
				return time.Time{}, false
			}
			nsPerUnit := int64(1e9) / perSec
			ns := (v % perSec) * nsPerUnit
			// The fraction is a fraction of one unit (e.g. ".5" after a
			// millisecond count is 500µs), scaled with integer math.
			ns += int64(fracNanos(sub[3])) * nsPerUnit / 1e9
			return time.Unix(sec, ns).UTC(), true
		},
	},
	{
		Name:     "time-only",
		Example:  "14:03:22.123",
		Note:     "bare wall clock; dateless, midnight rollover inferred",
		Dateless: true,
		re:       regexp.MustCompile(`(?:^|[^0-9:.])((\d{2}):(\d{2}):(\d{2})([.,]\d{1,9})?)(?:[^0-9:]|$)`),
		build: func(sub []string) (time.Time, bool) {
			return dateTime(2000, time.January, 1,
				atoi(sub[2]), atoi(sub[3]), atoi(sub[4]), fracNanos(sub[5]), time.UTC)
		},
	},
}
