#!/usr/bin/env bash
# ci-gate.sh — fail a build when a log contains stalls at or above LIMIT.
#
# gapline's JSON mode marks every gap row with "gap":true, so a shell
# one-liner is all a CI gate needs. No jq, no network.
#
#   bash examples/ci-gate.sh app.log        # default limit: 2s
#   LIMIT=500ms bash examples/ci-gate.sh app.log
set -euo pipefail

LOG="${1:?usage: ci-gate.sh <logfile>}"
LIMIT="${LIMIT:-2s}"
GAPLINE="${GAPLINE:-gapline}"

# Run gapline first so a missing binary or a parse failure fails the gate
# loudly (set -e) instead of counting zero gaps and passing green.
ROWS="$("$GAPLINE" -t "$LIMIT" --json "$LOG")"
GAPS="$(grep -c '"gap":true' <<<"$ROWS" || true)"
if [ "$GAPS" -gt 0 ]; then
  echo "ci-gate: $GAPS stall(s) of $LIMIT or more in $LOG" >&2
  "$GAPLINE" -t "$LIMIT" --top 3 --color never "$LOG" >&2
  exit 1
fi
echo "ci-gate: no stalls of $LIMIT or more in $LOG"
