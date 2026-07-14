# gapline examples

Two sample logs and one runnable script, all offline and deterministic.

## api-server.log

A realistic RFC 3339 API-server log with two stalls hidden in it: a 1.3s
connection-pool wait and the 4.02s upstream timeout the tagline promises.
It also contains a continuation line (no timestamp) to show pass-through.

```bash
gapline -t 1s examples/api-server.log
gapline -t 1s --stats --top 3 examples/api-server.log
```

## worker-restart.log

A syslog-format (yearless!) service restart that crosses midnight on New
Year's Eve. gapline infers both the midnight and the year rollover from
stream order, so the 33s checkpoint stall shows up as +33s — not as a
negative year.

```bash
gapline -t 10s examples/worker-restart.log
```

## ci-gate.sh

Uses `--json` output as a CI gate: exits 1 when the log contains stalls
at or above a limit, and prints the top offenders to stderr.

```bash
go build -o gapline ./cmd/gapline
GAPLINE=./gapline bash examples/ci-gate.sh examples/api-server.log; echo "exit: $?"
```

Every file here is checked by `scripts/smoke.sh`, so the examples can
never drift from what the tool actually prints.
