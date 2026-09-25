# 0011 — Delivery and completion invariants

These rules came out of the backend review (25 September 2026). Each is
pinned by a `TestRegression…` test in `test/integration/regression_test.go`.

**Run outcomes.**
- A run's terminal report is settled only by an explicit acknowledgement
  (`Ack.terminal`) for that lease epoch. An event acknowledgement at the same
  sequence doesn't settle it. The hub acknowledges a report it committed, a
  duplicate, and (deliberately discarding it) one for a run it doesn't know
  or that belongs to another machine. The runner re-sends unacknowledged
  reports every few heartbeats, not only on reconnect. (`Ack.terminal` is
  part of runner protocol v1; no build with the older behaviour shipped.)
- Applying an outcome to its job happens inside a savepoint. If that fails,
  the run's own state is still recorded and the machine keeps working;
  journal reconciliation on reconnect applies each run in its own savepoint.
- An offer that was never started is cancelled outright when its job is
  cancelled, and the runner is told to drop it (also when its
  acknowledgement arrives late, or the offer expired). The runner drops an
  accepted-but-unstarted attempt on cancel, lease loss, or re-offer, so it
  never holds a slot for nothing.

**Tool calls.** A mutating tool call is recorded in the same transaction as
its mutation, after the lease and run state are re-checked under the writer
lock. The runner re-sends unanswered calls after a reconnect; a call that
already committed returns its recorded result instead of running twice.

**Wakeups.**
- Before a job is parked, the hub re-checks whether its answer or dependency
  already arrived during the run; if so it resumes instead.
- A failed or cancelled child resolves its parent's dependency, and the
  parent is told the outcome.
- Steering input is consumed when a run starts, not when it is offered.
- Work waiting for a provider sign-in resumes when a machine reports that
  provider ready.
- An engineer removed from a room is not woken there, even by replies in a
  thread they started. A queued run whose engineer lost access is cancelled
  before any context is built.

**Completion.** With peer review required, every active reviewer must be
satisfied on the exact current result (the head revision, or a document's
latest content hash): an approval doesn't outvote another reviewer's
requested changes, an open round, or unresolved blocking findings. A
reviewer who couldn't review, or whose review was cancelled, neither
approves nor blocks. `work_respond` answers only a help request addressed to
the caller and can't complete the caller's own job.

**Why.** Each rule closes a way that work could be lost, run twice,
stranded, or completed without the evidence the brief requires.
