// Package cli wires flags, input files and the annotation pipeline
// together. All logic that can be pure lives in the other packages; this
// one owns argv, streams and exit codes.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/JaydenCJ/gapline/internal/render"
	"github.com/JaydenCJ/gapline/internal/scan"
	"github.com/JaydenCJ/gapline/internal/stats"
	"github.com/JaydenCJ/gapline/internal/tstamp"
	"github.com/JaydenCJ/gapline/internal/version"
)

// maxLine bounds a single input line. Log lines beyond 1 MiB are almost
// certainly not line-oriented logs; gapline reports them instead of
// silently mangling the stream.
const maxLine = 1 << 20

// Exit codes: 0 = ok, 1 = the input contained no recognizable
// timestamps, 2 = usage or I/O error.
const (
	exitOK      = 0
	exitNoStamp = 1
	exitUsage   = 2
)

type options struct {
	threshold  string
	format     string
	color      string
	top        int
	sinceStart bool
	bars       bool
	statsOut   bool
	noMarkers  bool
	jsonOut    bool
	listFmts   bool
	version    bool
}

// Run executes gapline and returns its exit code.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gapline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o options
	fs.StringVar(&o.threshold, "t", "1s", "")
	fs.StringVar(&o.threshold, "threshold", "1s", "")
	fs.StringVar(&o.format, "f", "", "")
	fs.StringVar(&o.format, "format", "", "")
	fs.StringVar(&o.color, "color", "auto", "")
	fs.IntVar(&o.top, "top", 0, "")
	fs.BoolVar(&o.sinceStart, "s", false, "")
	fs.BoolVar(&o.sinceStart, "since-start", false, "")
	fs.BoolVar(&o.bars, "b", false, "")
	fs.BoolVar(&o.bars, "bars", false, "")
	fs.BoolVar(&o.statsOut, "stats", false, "")
	fs.BoolVar(&o.noMarkers, "no-markers", false, "")
	fs.BoolVar(&o.jsonOut, "json", false, "")
	fs.BoolVar(&o.listFmts, "list-formats", false, "")
	fs.BoolVar(&o.version, "version", false, "")

	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return exitOK
		}
		return errf(stderr, "%v (see 'gapline --help')", err)
	}
	if o.version {
		fmt.Fprintf(stdout, "gapline %s\n", version.Version)
		return exitOK
	}
	if o.listFmts {
		printFormats(stdout)
		return exitOK
	}

	threshold, err := time.ParseDuration(o.threshold)
	if err != nil {
		return errf(stderr, "invalid --threshold %q: %v", o.threshold, err)
	}
	if threshold <= 0 {
		return errf(stderr, "--threshold must be positive, got %q", o.threshold)
	}
	if o.top < 0 {
		return errf(stderr, "--top must be zero or positive, got %d", o.top)
	}
	var forced *tstamp.Format
	if o.format != "" {
		f, ok := tstamp.ByName(o.format)
		if !ok {
			return errf(stderr, "unknown --format %q (known: %s)",
				o.format, strings.Join(tstamp.Names(), ", "))
		}
		forced = f
	}
	color := false
	switch o.color {
	case "always":
		color = true
	case "never":
		color = false
	case "auto":
		color = isTTY(stdout) && os.Getenv("NO_COLOR") == ""
	default:
		return errf(stderr, "invalid --color %q (auto, always or never)", o.color)
	}

	out := bufio.NewWriter(stdout)
	r := render.New(out, render.Options{
		Threshold:  threshold,
		Color:      color,
		Bars:       o.bars,
		Markers:    !o.noMarkers && !o.jsonOut,
		SinceStart: o.sinceStart,
		JSON:       o.jsonOut,
	})
	coll := stats.New(threshold, o.top > 0)

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}
	for _, name := range files {
		if len(files) > 1 && !o.jsonOut {
			fmt.Fprintf(out, "==> %s <==\n", displayName(name))
		}
		if err := processFile(name, stdin, forced, r, coll); err != nil {
			out.Flush()
			return errf(stderr, "%v", err)
		}
	}

	if o.top > 0 {
		if err := render.WriteTop(out, coll.Top(o.top), color); err != nil {
			return errf(stderr, "writing output: %v", err)
		}
	}
	if o.statsOut {
		if err := render.WriteStats(out, coll.Summary(), threshold); err != nil {
			return errf(stderr, "writing output: %v", err)
		}
	}
	if err := out.Flush(); err != nil {
		return errf(stderr, "writing output: %v", err)
	}
	if coll.Timestamped() == 0 {
		fmt.Fprintln(stderr, "gapline: no timestamps detected in the input")
		return exitNoStamp
	}
	return exitOK
}

// processFile streams one input through a fresh scanner: deltas never
// cross file boundaries.
func processFile(name string, stdin io.Reader, forced *tstamp.Format, r *render.Renderer, coll *stats.Collector) error {
	var src io.Reader
	if name == "-" {
		src = stdin
	} else {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	sc := scan.New(forced)
	lines := bufio.NewScanner(src)
	lines.Buffer(make([]byte, 64*1024), maxLine)
	for lines.Scan() {
		text := strings.TrimSuffix(lines.Text(), "\r")
		e := sc.Next(text)
		coll.Add(e)
		if err := r.Emit(e); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
	}
	if err := lines.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return fmt.Errorf("%s: line longer than %d bytes; not a line-oriented log?",
				displayName(name), maxLine)
		}
		return fmt.Errorf("%s: %w", displayName(name), err)
	}
	return nil
}

func displayName(name string) string {
	if name == "-" {
		return "stdin"
	}
	return name
}

// errf prints a gapline-prefixed error and returns the usage exit code.
func errf(stderr io.Writer, format string, args ...any) int {
	fmt.Fprintf(stderr, "gapline: "+format+"\n", args...)
	return exitUsage
}

// isTTY reports whether w is an interactive terminal; --color auto only
// switches colors on when it is, so pipes stay clean.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

func printFormats(w io.Writer) {
	fmt.Fprintf(w, "%-10s  %-28s  %s\n", "NAME", "EXAMPLE", "NOTES")
	for _, f := range tstamp.All() {
		fmt.Fprintf(w, "%-10s  %-28s  %s\n", f.Name, f.Example, f.Note)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `gapline — highlight time gaps in any log stream

usage: gapline [flags] [file ...]

Reads log lines from files or stdin, autodetects the timestamp format,
prepends the delta since the previous timestamped line, and flags gaps.
With no file (or with "-") gapline reads stdin, so it drops into any
pipeline: kubectl logs api | gapline -t 500ms

flags:
  -t, --threshold <dur>  gap threshold: deltas at or above it are
                         highlighted and get a marker line (default 1s)
  -f, --format <name>    force a timestamp format instead of autodetecting
                         (see --list-formats)
  -s, --since-start      show elapsed time since the first timestamp
                         instead of per-line deltas
  -b, --bars             draw a log-scale bar next to each delta
      --top <n>          after the stream, print the n largest gaps
      --stats            after the stream, print summary statistics
      --no-markers       suppress the gap marker lines
      --json             emit one JSON object per input line
      --color <mode>     auto, always or never (default auto)
      --list-formats     list supported timestamp formats and exit
      --version          print version and exit

exit codes: 0 ok, 1 no timestamps detected, 2 usage or I/O error
`)
}
