#!/bin/sh
# The disposable hub machines.js runs against: several machines in different
# states (see scripts/e2e/machines_fixture.py). Called by run-webkit.sh as
#   machines.setup.sh up DIR PORT     (prints BASE=, HANDLE= and PASS=)
#   machines.setup.sh down DIR
exec python3 scripts/e2e/machines_fixture.py "$@"
