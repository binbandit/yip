# Machines redesign screenshots

`before/` is the Machines screen and the Security room as they were at `fd56c0b`; `after/` is the rebuilt Machines screen. Both were taken in the system WebKit against the disposable hub from `scripts/e2e/machines_fixture.py` (four machines: this one as a temporary session, a paused build server, an offline laptop with every limitation, and a machine that stopped responding).

Retake the after set with:

    scripts/e2e/run-webkit.sh OUT scripts/e2e/shots/machines.js

Names are `machines-<width>-<theme>` for the list and `machines-details-<machine>-<width>-<theme>` (or `…-<tab>-…`) for the details panel. Widths are 390, 900, 1280 and 1440px, in the day (light) and night (dark) themes.
