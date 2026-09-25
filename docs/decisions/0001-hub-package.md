# 0001 — Orchestration in `internal/hub`

**Decision.** The MVP brief's layout lists `internal/domain` for "identities,
jobs, state machines, policies" but no home for the stateful services that
apply them. yip keeps `internal/domain` pure (state machines, limits, errors,
IDs) and puts the transactional coordinator — routing, jobs, runs, reviews,
questions, approvals, scheduling, runner connections, agent tools — in
`internal/hub`. `internal/scheduler` and `internal/events` from the brief map
to `internal/hub/scheduler.go` and `internal/events` (the live bus); the
append-only event stream and outbox are tables in `internal/store`.

**Why.** Every mutation, its event, and its outbox item must commit in one
transaction; splitting the scheduler into its own package would have forced a
second transaction owner.

**Consequence.** None for acceptance criteria; the package map in `README.md`
reflects it.
