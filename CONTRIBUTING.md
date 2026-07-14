# Contributing to gapline

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else.

```bash
git clone https://github.com/JaydenCJ/gapline && cd gapline
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary and pipes fixed log streams through
it, asserting on the real CLI output — annotation, gap markers, JSON
rows, midnight/year rollovers and exit codes. It must finish by printing
`SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network, no clock).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (only `internal/cli` touches argv, files and exit codes).

## Ground rules

- Keep dependencies at zero — gapline is standard library only, and the
  empty require list in `go.mod` is a feature. Adding a dependency needs
  strong justification in the PR.
- No network calls, ever, and no telemetry. gapline reads stdin or the
  files you name and writes stdout; that is its entire interface.
- Timestamp formats are data: a new format is a regex plus a validating
  builder in `internal/tstamp/tstamp.go`, a test built from a real log
  line, and a row in `docs/formats.md`.
- Determinism first: identical input must produce byte-identical output
  on any machine — never read the wall clock or the local timezone.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `gapline --version`, the full command you ran, and
five to ten raw log lines around the problem (redact payloads if needed
— the timestamps are what the detector sees). For misdetections, also
include what `--list-formats` name you expected to match.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
