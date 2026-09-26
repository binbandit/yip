#!/bin/sh
# Runs the browser journeys (web/tests/webkit/journeys.js) in the system
# WebKit against a fresh demo hub. macOS only; downloads nothing.
#   scripts/e2e/run-webkit.sh [out-dir]
# Screenshots of each journey's final state land in out-dir.
set -eu
cd "$(dirname "$0")/../.."
OUT=${1:-${TMPDIR:-/tmp}/yip-webkit-e2e}
WORK=$(mktemp -d "${TMPDIR:-/tmp}/yip-webkit.XXXXXX")
trap 'kill "$HUB" 2>/dev/null || true; wait "$HUB" 2>/dev/null || true; rm -rf "$WORK"' EXIT

[ -x bin/yip ] || make build
swiftc -O -swift-version 5 -o "$WORK/webkit" scripts/e2e/webkit.swift

PORT=${YIP_E2E_PORT:-7931}
./bin/yip demo --reset --data "$WORK/demo" --listen "127.0.0.1:$PORT" --runner-listen "127.0.0.1:$((PORT + 23))" >"$WORK/hub.log" 2>&1 &
HUB=$!
i=0
until grep -q "runner connected" "$WORK/hub.log" 2>/dev/null; do
  i=$((i + 1))
  if [ "$i" -gt 120 ]; then echo "the demo hub didn't start:"; cat "$WORK/hub.log"; exit 1; fi
  sleep 0.25
done
HANDLE=$(awk '/handle:/ {print $2}' "$WORK/demo/demo-credentials.txt")
PASS=$(awk '/password:/ {print $2}' "$WORK/demo/demo-credentials.txt")
"$WORK/webkit" "http://127.0.0.1:$PORT" "$HANDLE" "$PASS" web/tests/webkit/journeys.js "$OUT"
