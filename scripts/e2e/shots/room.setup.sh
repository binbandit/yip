#!/bin/sh
# Reuse only the disposable fixture. An old binary can supply the baseline.
if [ "$1" = up ]; then
  exec python3 scripts/e2e/machines_fixture.py "$@" --bin "${YIP_ROOM_SHOT_BIN:-bin/yip}"
fi
exec python3 scripts/e2e/machines_fixture.py "$@"
