# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Timestamp-format autodetection across nine formats: RFC 3339 / ISO 8601
  (dot or comma fractions, any offset, lowercase variants), space-separated
  ISO (Python logging), Go log default, Apache/nginx CLF, RFC 3164 syslog,
  glog/klog headers, dmesg seconds-since-boot, 10/13/16/19-digit epochs
  (range-guarded to 2000-2099), and bare wall clocks.
- Leftmost-match detection with calendar validation — junk numbers and
  impossible dates never become timestamps — plus per-stream format
  locking so payload text cannot flap the detection mid-stream.
- Deterministic completion of partial timestamps: midnight rollover for
  bare wall clocks, New Year rollover for yearless syslog/klog, and
  preservation of small backwards jumps as visible negative deltas.
- Relative-delta annotation as a pipe filter: an aligned `+4.020s`-style
  column, blank pass-through for continuation lines, gap ruler lines at
  or above the threshold (`-t`, default 1s), and threshold-graded ANSI
  color with `--color auto|always|never` (NO_COLOR honored).
- `--since-start` elapsed mode, `--bars` log-scale bar column on a 1-2-5
  ladder, `--stats` percentile summary (p50/p90/p99, span, max, gap
  totals), and `--top n` largest-gaps report with quoted line text.
- `--json` mode emitting one row per input line with nullable `ts`,
  `delta_ms`, `elapsed_ms` and a `gap` flag, for CI gates and scripting.
- Multi-file input with per-file headers and per-file delta isolation,
  `-` for stdin, CRLF stripping, a 1 MiB line-length guard, and distinct
  exit codes (0 ok, 1 no timestamps, 2 usage or I/O error).
- Runnable examples (`examples/api-server.log`, `examples/worker-restart.log`,
  `examples/ci-gate.sh`) and a format reference (`docs/formats.md`).
- 90 deterministic offline tests (unit + in-process CLI integration) and
  `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/gapline/releases/tag/v0.1.0
