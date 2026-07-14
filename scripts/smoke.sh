#!/usr/bin/env bash
# End-to-end smoke test for gapline: builds the binary, pipes fixed log
# streams through it, and asserts on the real CLI output — annotation,
# markers, JSON, stats, rollovers and exit codes. No network, idempotent,
# finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/gapline"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/gapline) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "gapline 0.1.0" || fail "--version mismatch"

echo "3. annotates the bundled example and flags the 4.02s gap"
OUT="$("$BIN" -t 1s --color never "$ROOT/examples/api-server.log")"
echo "$OUT" | grep -q "──── gap 4.020s " || fail "4.02s gap marker missing"
echo "$OUT" | grep -q "──── gap 1.316s " || fail "1.316s gap marker missing"
echo "$OUT" | grep -q "+25.0ms │ 2026-07-12T14:03:22.145Z" || fail "delta column wrong"
echo "$OUT" | grep -q "^         │   retrying with backoff" \
  || fail "continuation line not passed through blank"

echo "4. syslog stream crosses midnight and New Year without a glitch"
"$BIN" -t 10s --color never "$ROOT/examples/worker-restart.log" \
  | grep -q "+33.0s │ Jan  1 00:00:31" || fail "midnight/year rollover broken"

echo "5. autodetection reports the format in --stats"
"$BIN" -t 1s --stats "$ROOT/examples/api-server.log" \
  | grep -q "format           rfc3339" || fail "format not detected"

echo "6. JSON rows are machine-readable and mark the gap"
JSON="$(printf '14:00:00.000 a\n14:00:04.020 b\n' | "$BIN" -t 1s --json)"
echo "$JSON" | grep -q '"delta_ms":4020' || fail "json delta wrong"
echo "$JSON" | grep -q '"gap":true' || fail "json gap flag missing"
[ "$(echo "$JSON" | wc -l)" -eq 2 ] || fail "expected one JSON row per line"

echo "7. --top lists the largest gap first"
"$BIN" -t 1s --top 2 "$ROOT/examples/api-server.log" \
  | grep -A1 "top 2 gaps" | grep -q "+4.020s  line 9" || fail "--top order wrong"

echo "8. epoch and forced-format streams both parse"
printf '1752328402.000 start\n1752328406.020 done\n' | "$BIN" -t 1s --color never \
  | grep -q "+4.020s" || fail "epoch stream broken"
printf '2026-07-12T14:03:22Z a\n2026-07-12T14:03:25Z b\n' \
  | "$BIN" -t 1h --format time-only --stats | grep -q "format           time-only" \
  || fail "--format not forced"

echo "9. the ci-gate example trips on the stall"
if GAPLINE="$BIN" bash "$ROOT/examples/ci-gate.sh" "$ROOT/examples/api-server.log" \
  >/dev/null 2>&1; then
  fail "ci-gate should exit 1 on a 4s stall"
fi

echo "10. exit codes: 1 without timestamps, 2 on usage errors"
set +e
printf 'no stamps here\n' | "$BIN" >/dev/null 2>&1
[ $? -eq 1 ] || fail "timestamp-free input should exit 1"
"$BIN" -t soon </dev/null >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --threshold should exit 2"
"$BIN" "$WORKDIR/does-not-exist.log" >/dev/null 2>&1
[ $? -eq 2 ] || fail "missing file should exit 2"
set -e

echo "11. multiple files get headers and independent deltas"
printf '14:00:00 tail line\n' > "$WORKDIR/b.log"
OUT="$("$BIN" -t 1h --color never "$ROOT/examples/worker-restart.log" "$WORKDIR/b.log")"
echo "$OUT" | grep -q "==> $WORKDIR/b.log <==" || fail "file header missing"
echo "$OUT" | grep -q "     +0s │ 14:00:00 tail line" || fail "deltas crossed files"

echo "SMOKE OK"
