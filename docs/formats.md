# Timestamp detection reference

This is the exact contract of gapline's autodetection: which formats it
recognizes, how it picks between them, and how it completes timestamps
that carry no year or no date. Everything here is enforced by tests in
`internal/tstamp` and `internal/scan`.

## Recognized formats

Priority order — when two formats match a line at the same byte offset,
the earlier row wins. `gapline --list-formats` prints the same table.

| Name | Example | Notes |
|---|---|---|
| `rfc3339` | `2026-07-12T14:03:22.123Z` | ISO 8601 / RFC 3339; `T`/`t`, `.` or `,` fraction (1-9 digits), optional `Z`/`±hh:mm`/`±hhmm` offset |
| `iso-space` | `2026-07-12 14:03:22,123` | space-separated ISO; Python `logging` default |
| `slash` | `2026/07/12 14:03:22` | Go standard-library `log` default |
| `clf` | `[12/Jul/2026:14:03:22 +0900]` | Apache / nginx access-log clock, brackets required |
| `syslog` | `Jul 12 14:03:22` | RFC 3164; yearless, `Jul  2` day padding accepted |
| `klog` | `I0712 14:03:22.123456` | glog / klog header (`I`/`W`/`E`/`F`), line-anchored; yearless |
| `dmesg` | `[   12.345678]` | kernel seconds-since-boot, line-anchored |
| `epoch` | `1752328402.123` | 10/13/16/19 digits = s/ms/µs/ns; anchored on both sides and range-guarded to 2000-2099 |
| `time-only` | `14:03:22.123` | bare wall clock; dateless |

Timestamps carrying an offset are converted to UTC before subtraction;
formats without an offset are read as UTC. Both choices are invisible in
the output because only deltas within one stream matter.

## How a line is matched

1. Every candidate format searches for its **leftmost** occurrence.
   Log stamps lead the line; quoted timestamps in payloads sit further
   right, so the earliest byte offset wins (ties go to table order).
2. Matches are **calendar-validated**: month 13, February 30th, hour 25,
   or an epoch outside 2000-2099 are rejected, and the search resumes
   after the rejected span — junk numbers never shadow a real timestamp
   later in the line.
3. The first format that matches is **locked**: it is preferred on every
   later line even if another format would match earlier in that line.
   If the locked format stops matching entirely, full detection runs
   again and re-locks, so streams that genuinely switch formats work.
4. `--format <name>` skips all of the above and forces one format.
   Lines the forced format cannot parse pass through with a blank column.

## Completing partial timestamps

gapline never reads the wall clock, so the completion is deterministic:

- **Yearless** formats (`syslog`, `klog`) parse against reference year
  2000 (a leap year, so `Feb 29` stays valid). When the stream jumps
  backwards by more than ~10 months, that is January after December: one
  year is added, permanently, for the rest of the stream.
- **Dateless** wall clocks parse against a reference date. A backwards
  jump of more than 12 hours means the clock crossed midnight: one day
  is added, permanently.
- Smaller backwards jumps are **kept as negative deltas** and colored
  cyan — interleaved writers and clock skew are real signals, not noise.

The reference instant is visible only in `--json` output (`ts` fields of
yearless streams start in year 2000); every `delta_ms` and `elapsed_ms`
is exact regardless.

## Known limits (0.1.0)

- One timestamp per line: the leftmost valid one wins, the rest is text.
- `hh:mm:ss` durations in payloads are indistinguishable from bare wall
  clocks; if a stream has no leading timestamp but mentions durations,
  force a format (or accept the lock, which usually lands correctly on
  the first line).
- 12-hour clocks with `AM`/`PM` and locale month names are not parsed.
- Lines longer than 1 MiB abort with a clear error rather than guessing.
