<!--
Title: a conventional commit, as in the history: `feat(web): …`, `fix: …`, `docs: …`.

Open with why: what was wrong or missing, and what this does about it. Link
the issue, ADR (docs/decisions/) or release gate (A01–A44) it concerns.
Delete any section or checklist line that doesn't apply.
-->



## What changed

-

## Worth knowing when reviewing

<!--
Trade-offs, deliberate behaviour changes, upstream bugs worked around, bundle
size, anything left undone or now out of date.
-->

-

## Testing

| Check | Result |
|---|---|
| `just lint` | |
| `go test ./...` | |
| `go test -race ./internal/... ./test/integration/` | |
| `cd web && npm run check` | |
| `cd web && npm test` | |

<!--
Keep the rows you ran and add others you ran: `cd web && node scripts/contrast.mjs`
for theme changes, a real-provider or live GitHub scenario. Name the provider
a run used. For UI changes, say which screens you checked, in which themes,
and at phone width.
-->

## Checklist

- [ ] `just schema` re-run and its output committed, if `protocol/*.go` changed
- [ ] `docs/api.md` updated, if the browser API or its events changed
- [ ] An ADR added to `docs/decisions/` and its index, if this departs from or reinterprets `docs/spec/`
- [ ] `docs/release-checklist.md` and the README status table updated, if a gate's status changed
- [ ] `docs/screenshots/` refreshed, or noted above as out of date, if the UI changed
- [ ] Provider credentials are still never collected, stored, logged or proxied
