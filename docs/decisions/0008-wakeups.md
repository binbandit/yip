# 0008 — What wakes an engineer

**Decision.** Only these wake an engineer: a human's structured mention; an
unaddressed follow-up in a thread (to the thread's owner) or in a steward-mode
room (to the steward); and structured requests from colleagues
(`work_create` with another owner, `work_request_help`,
`work_request_review`). Mentions inside an engineer's own messages are
rendered but wake no one. Every automatic wakeup counts against its root
request (default 12) and delegation depth (default 3). A trigger that arrives
while the target job has an active attempt is stored and replayed when that
attempt ends. Status questions in the owner's Overview conversation are
answered from the ledger by the hub without starting any run.

**Consequence.** A04, A06, A07, and A21 hold by construction and are tested.
