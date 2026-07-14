// In-process integration tests for the gapline CLI: flags, exit codes,
// stdin and file inputs, and the exact text and JSON output shapes. No
// subprocesses, no network, no wall-clock dependence.
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes gapline in-process and captures both streams.
func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb strings.Builder
	code = Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// demo is a small rfc3339 stream with one obvious 4.02s gap.
const demo = `2026-07-12T14:03:22.120Z GET /api/users 200 12ms
2026-07-12T14:03:22.145Z GET /api/orders 200 9ms
  retrying upstream connection
2026-07-12T14:03:26.165Z GET /api/users 200 11ms
2026-07-12T14:03:26.170Z POST /api/checkout 500
`

func TestVersionFlagPrintsToolAndVersion(t *testing.T) {
	code, out, _ := run(t, "", "--version")
	if code != 0 || out != "gapline 0.1.0\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestHelpGoesToStdoutAndExitsZero(t *testing.T) {
	code, out, _ := run(t, "", "--help")
	if code != 0 {
		t.Fatalf("help exit = %d", code)
	}
	for _, want := range []string{"usage: gapline", "--threshold", "--list-formats", "exit codes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
}

func TestListFormatsShowsEveryFormatWithExamples(t *testing.T) {
	code, out, _ := run(t, "", "--list-formats")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"rfc3339", "iso-space", "slash", "clf", "syslog", "klog", "dmesg", "epoch", "time-only"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--list-formats missing %q:\n%s", want, out)
		}
	}
}

func TestUnknownFlagIsUsageError(t *testing.T) {
	code, _, errb := run(t, "", "--frobnicate")
	if code != 2 || !strings.Contains(errb, "gapline:") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestInvalidThresholdIsUsageError(t *testing.T) {
	for _, bad := range []string{"soon", "0s", "-3s"} {
		code, _, errb := run(t, demo, "-t", bad)
		if code != 2 || !strings.Contains(errb, "threshold") {
			t.Fatalf("-t %s: code=%d stderr=%q", bad, code, errb)
		}
	}
}

func TestUnknownForcedFormatListsKnownNames(t *testing.T) {
	code, _, errb := run(t, demo, "--format", "sundial")
	if code != 2 || !strings.Contains(errb, "rfc3339") || !strings.Contains(errb, "epoch") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestInvalidColorAndTopValuesAreUsageErrors(t *testing.T) {
	code, _, errb := run(t, demo, "--color", "rainbow")
	if code != 2 || !strings.Contains(errb, "color") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
	code, _, errb = run(t, demo, "--top", "-1")
	if code != 2 || !strings.Contains(errb, "top") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestAnnotatesStdinWithDeltasAndMarker(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1s")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"     +0s │ 2026-07-12T14:03:22.120Z",
		" +25.0ms │ 2026-07-12T14:03:22.145Z",
		"         │   retrying upstream connection",
		"──── gap 4.020s ",
		" +4.020s │ 2026-07-12T14:03:26.165Z",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestGapExactlyAtThresholdIsFlagged(t *testing.T) {
	in := "14:00:00.000 a\n14:00:01.000 b\n"
	code, out, _ := run(t, in, "-t", "1s")
	if code != 0 || !strings.Contains(out, "──── gap 1.000s ") {
		t.Fatalf("threshold must be inclusive; code=%d out:\n%s", code, out)
	}
}

func TestNoMarkersSuppressesRulerLines(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1s", "--no-markers")
	if code != 0 || strings.Contains(out, "gap 4.020s") {
		t.Fatalf("marker leaked: code=%d out:\n%s", code, out)
	}
	if !strings.Contains(out, "+4.020s") {
		t.Fatalf("delta column missing:\n%s", out)
	}
}

func TestSinceStartShowsCumulativeElapsed(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1h", "--since-start")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "+4.050s │ 2026-07-12T14:03:26.170Z") {
		t.Fatalf("elapsed column wrong:\n%s", out)
	}
}

func TestBarsAddLogScaleColumn(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1h", "--bars")
	if code != 0 || !strings.Contains(out, "█") {
		t.Fatalf("bars missing: code=%d out:\n%s", code, out)
	}
}

func TestJSONEmitsOneParsableRowPerLine(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1s", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	rows := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5:\n%s", len(rows), out)
	}
	var gapRows int
	for i, raw := range rows {
		var row map[string]any
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatalf("row %d not JSON: %v", i+1, err)
		}
		if row["gap"] == true {
			gapRows++
			if row["delta_ms"] != 4020.0 {
				t.Fatalf("gap delta = %v, want 4020", row["delta_ms"])
			}
		}
	}
	if gapRows != 1 {
		t.Fatalf("gap rows = %d, want 1", gapRows)
	}
	// Text gap markers are suppressed automatically in JSON mode.
	if strings.Contains(out, "────") {
		t.Fatalf("text marker inside JSON stream:\n%s", out)
	}
}

func TestStatsBlockAfterTheStream(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1s", "--stats")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{
		"gapline stats",
		"lines            5 (4 timestamped, 1 without)",
		"format           rfc3339",
		"max delta        +4.020s (line 4)",
		"gaps ≥ 1.000s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stats missing %q:\n%s", want, out)
		}
	}
}

func TestTopListsLargestGapsDescending(t *testing.T) {
	code, out, _ := run(t, demo, "-t", "1s", "--top", "2")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	iBig := strings.Index(out, "+4.020s  line 4")
	iSmall := strings.Index(out, "+25.0ms  line 2")
	if iBig == -1 || iSmall == -1 || iBig > iSmall {
		t.Fatalf("top ordering wrong:\n%s", out)
	}
}

func TestColorAlwaysAndNever(t *testing.T) {
	_, colored, _ := run(t, demo, "-t", "1s", "--color", "always")
	if !strings.Contains(colored, "\x1b[") {
		t.Fatalf("--color always produced no ANSI:\n%q", colored)
	}
	_, plain, _ := run(t, demo, "-t", "1s", "--color", "never")
	if strings.Contains(plain, "\x1b[") {
		t.Fatalf("--color never leaked ANSI:\n%q", plain)
	}
	// auto on a non-terminal buffer must stay plain, so pipes are clean.
	_, auto, _ := run(t, demo, "-t", "1s")
	if strings.Contains(auto, "\x1b[") {
		t.Fatalf("--color auto colored a pipe:\n%q", auto)
	}
}

func TestForcedFormatOverridesDetection(t *testing.T) {
	// Forcing time-only reads the wall clock inside the rfc3339 stamps.
	code, out, _ := run(t, demo, "-t", "1s", "--format", "time-only", "--stats")
	if code != 0 || !strings.Contains(out, "format           time-only") {
		t.Fatalf("forced format ignored: code=%d\n%s", code, out)
	}
}

func TestFileArgumentIsRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte(demo), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run(t, "", "-t", "1s", path)
	if code != 0 || !strings.Contains(out, "+4.020s") {
		t.Fatalf("file input failed: code=%d\n%s", code, out)
	}
	if strings.Contains(out, "==>") {
		t.Fatalf("single file must not print a header:\n%s", out)
	}
}

func TestMultipleFilesGetHeadersAndFreshDeltas(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	os.WriteFile(a, []byte("2026-07-12T14:00:00Z a1\n2026-07-12T14:00:01Z a2\n"), 0o644)
	os.WriteFile(b, []byte("2020-01-01T00:00:00Z b1\n2020-01-01T00:00:02Z b2\n"), 0o644)
	code, out, _ := run(t, "", "-t", "1h", a, b)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "==> "+a+" <==") || !strings.Contains(out, "==> "+b+" <==") {
		t.Fatalf("file headers missing:\n%s", out)
	}
	// b1 is the First of its own stream: +0s, not six years negative.
	if !strings.Contains(out, "+0s │ 2020-01-01T00:00:00Z b1") {
		t.Fatalf("deltas leaked across file boundary:\n%s", out)
	}
}

func TestMissingFileIsRuntimeError(t *testing.T) {
	code, _, errb := run(t, "", filepath.Join(t.TempDir(), "nope.log"))
	if code != 2 || !strings.Contains(errb, "nope.log") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestNoTimestampsExitsOne(t *testing.T) {
	code, out, errb := run(t, "hello\nworld\n")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errb, "no timestamps") {
		t.Fatalf("stderr = %q", errb)
	}
	// The lines themselves still pass through, filter-style.
	if !strings.Contains(out, "│ hello") {
		t.Fatalf("lines not passed through:\n%s", out)
	}
	// Empty input exits 1 too, with nothing on stdout.
	code, out, _ = run(t, "")
	if code != 1 || out != "" {
		t.Fatalf("empty input: code=%d out=%q", code, out)
	}
}

func TestCRLFInputIsStripped(t *testing.T) {
	code, out, _ := run(t, "14:00:00 a\r\n14:00:01 b\r\n", "-t", "1h")
	if code != 0 || strings.Contains(out, "\r") {
		t.Fatalf("CR survived: code=%d out=%q", code, out)
	}
	if !strings.Contains(out, "+1.000s │ 14:00:01 b") {
		t.Fatalf("delta wrong under CRLF:\n%s", out)
	}
}

func TestOverlongLineIsReportedNotMangled(t *testing.T) {
	huge := "14:00:00 " + strings.Repeat("x", maxLine+1) + "\n"
	code, _, errb := run(t, huge)
	if code != 2 || !strings.Contains(errb, "line longer than") {
		t.Fatalf("code=%d stderr=%q", code, errb)
	}
}

func TestDefaultThresholdIsOneSecond(t *testing.T) {
	// 999ms stays quiet, 1.001s draws a marker — without any -t flag.
	quiet, out, _ := run(t, "14:00:00.000 a\n14:00:00.999 b\n")
	if quiet != 0 || strings.Contains(out, "────") {
		t.Fatalf("999ms flagged by default: %q", out)
	}
	_, out2, _ := run(t, "14:00:00.000 a\n14:00:01.001 b\n")
	if !strings.Contains(out2, "──── gap 1.001s ") {
		t.Fatalf("1.001s not flagged by default:\n%s", out2)
	}
}

func TestEpochStreamAnnotates(t *testing.T) {
	in := "1752328402.000 job start\n1752328406.020 job done\n"
	code, out, _ := run(t, in, "-t", "1s")
	if code != 0 || !strings.Contains(out, "+4.020s") {
		t.Fatalf("epoch stream: code=%d\n%s", code, out)
	}
}

func TestStdinDashMixesWithFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tail.log")
	os.WriteFile(path, []byte("14:00:00 from file\n"), 0o644)
	code, out, _ := run(t, "14:00:00 from stdin\n", "-t", "1h", path, "-")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "==> stdin <==") || !strings.Contains(out, "from stdin") {
		t.Fatalf("stdin as '-' not handled:\n%s", out)
	}
}
