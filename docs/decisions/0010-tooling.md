# 0010 — Build tooling

**Decision.** The web client builds with npm (lockfile `web/package-lock.json`)
because pnpm was unavailable on the build machine; the brief treats the package
manager as a convenience. Browser-facing TypeScript types and JSON Schemas are
generated from the Go protocol structs (`yip schema`), so there is one source
of truth.

**Consequence.** `make web` uses `npm ci`.
