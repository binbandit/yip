#!/bin/sh
# Runs the browser journeys (web/tests/webkit/*.js) in the system WebKit,
# each file against its own fresh, disposable hub. macOS only; downloads
# nothing.
#   scripts/e2e/run-webkit.sh [out-dir] [journeys.js ...]
# With no files it runs every web/tests/webkit/*.js. Screenshots of each
# journey's final state land in out-dir.
#
# A journeys file can bring its own hub: if NAME.setup.sh sits beside
# NAME.js, it is run as `NAME.setup.sh up DIR PORT` (printing BASE=, HANDLE=
# and PASS= lines) and `NAME.setup.sh down DIR` afterwards. Otherwise a fresh
# `yip demo` is started for the file.
set -eu
cd "$(dirname "$0")/../.."
OUT=${1:-${TMPDIR:-/tmp}/yip-webkit-e2e}
[ $# -gt 0 ] && shift
if [ $# -gt 0 ]; then FILES=$*; else FILES=$(ls web/tests/webkit/*.js); fi
WORK=$(mktemp -d "${TMPDIR:-/tmp}/yip-webkit.XXXXXX")
HUB=
SETUP=
SETUP_DIR=
stop() {
  if [ -n "$HUB" ]; then
    kill "$HUB" 2>/dev/null || true
    wait "$HUB" 2>/dev/null || true
    HUB=
  fi
  if [ -n "$SETUP" ]; then
    sh "$SETUP" down "$SETUP_DIR" || true
    SETUP=
  fi
}
trap 'stop; rm -rf "$WORK"' EXIT

[ -x bin/yip ] || make build
swiftc -O -swift-version 5 -o "$WORK/webkit" scripts/e2e/webkit.swift

PORT=${YIP_E2E_PORT:-7931}
FAILED=0
for FILE in $FILES; do
  NAME=$(basename "$FILE" .js)
  echo "$NAME"
  mkdir -p "$WORK/$NAME"
  if [ -f "$(dirname "$FILE")/$NAME.setup.sh" ]; then
    SETUP="$(dirname "$FILE")/$NAME.setup.sh"
    SETUP_DIR="$WORK/$NAME"
    if ! ENV=$(sh "$SETUP" up "$SETUP_DIR" "$PORT"); then
      echo "  FAIL  $NAME: its hub didn't start"
      FAILED=1
      stop
      continue
    fi
    BASE=$(printf '%s\n' "$ENV" | sed -n 's/^BASE=//p')
    HANDLE=$(printf '%s\n' "$ENV" | sed -n 's/^HANDLE=//p')
    PASS=$(printf '%s\n' "$ENV" | sed -n 's/^PASS=//p')
  else
    DATA="$WORK/$NAME/demo"
    ./bin/yip demo --reset --data "$DATA" --listen "127.0.0.1:$PORT" --runner-listen "127.0.0.1:$((PORT + 23))" >"$WORK/$NAME/hub.log" 2>&1 &
    HUB=$!
    i=0
    until grep -q "runner connected" "$WORK/$NAME/hub.log" 2>/dev/null; do
      i=$((i + 1))
      if [ "$i" -gt 120 ]; then echo "the demo hub didn't start:"; cat "$WORK/$NAME/hub.log"; exit 1; fi
      sleep 0.25
    done
    BASE="http://127.0.0.1:$PORT"
    HANDLE=$(awk '/handle:/ {print $2}' "$DATA/demo-credentials.txt")
    PASS=$(awk '/password:/ {print $2}' "$DATA/demo-credentials.txt")
  fi
  "$WORK/webkit" "$BASE" "$HANDLE" "$PASS" "$FILE" "$OUT" || FAILED=1
  stop
done
exit "$FAILED"
