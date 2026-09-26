#!/bin/sh
# The disposable hub machines-empty.js runs against: a fresh hub with an
# owner and no machines. Called by run-webkit.sh like machines.setup.sh.
if [ "${1:-}" = up ]; then
  exec python3 scripts/e2e/machines_fixture.py up "$2" "$3" --empty
fi
exec python3 scripts/e2e/machines_fixture.py "$@"
