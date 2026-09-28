# How yip enforces the product invariants

A map from the twelve invariants in
[`spec/01-solution-architecture.md` §2](spec/01-solution-architecture.md) to
the code that enforces them and the tests that prove it.

| # | Invariant | Enforcement | Evidence |
|---|---|---|---|
| 1 | The hub owns canonical state; no browser tab owns a job | Jobs and runs live in the hub database; runners hold leases; the browser only issues commands and reads events (`internal/hub`, `internal/store`) | `TestEventReplayAfterDisconnect`, `TestOutboxRedeliveryExecutesOnce` |
| 2 | Every dispatched unit has a persisted run ID, immutable destination, engineer, scope, cause | `runs` rows carry `dest_room_id/dest_thread_id/dest_message_id`, `cause_id`, `root_request_id`, engineer version, and scope fingerprint, written before any offer (`enqueueRun`, `offer`) | `TestEachMessageKeepsItsDestination` |
| 3 | A chat claim isn't job state | State changes go through validated hub operations and tools; completion is evaluated from recorded revisions, checks, and reviews (`completionMissing`) | `TestDoneWithoutEvidence` |
| 4 | One active owner holds a job lease; contributors get child jobs or reviews | One non-terminal attempt per job (`enqueueRun`), one active run per engineer, help and review as child jobs | `TestCancelJobTree`, `TestWakeupBudgetAndCycles` |
| 5 | Membership grants conversation access, not repository access | Room membership and `project_grants` are separate; tools re-check both on every call (`checkRunAccess`, `resolveScope`) | `TestAccessRevokedMidJob`, `TestNoPermittedReviewer` |
| 6 | Agent messages don't indiscriminately wake others | Only structured requests wake engineers; wakeups carry root, cause, and a unique delivery key and are bounded (`ClaimWakeup`, ADR 0008) | `TestIdempotentSendQuietRoomsAndAgentMentions`, `TestWakeupBudgetAndCycles` |
| 7 | A provider change preserves identity and approved knowledge, not a vendor session | Engineers are stable IDs with versioned config; provider sessions are reused only when engineer, provider account, context, node, and scope fingerprint all match (`FindProviderSession`); provider changes invalidate sessions | `TestFingerprintChangesWithScope` |
| 8 | An interrupted external write isn't replayed | Lost attempts become `unknown`; retries are explicit; forge publications journal a pending delivery before the network call and reconcile by marker | `TestPartitionProducesUnknownThenReconciles`, `TestTwoMachinesAndCheckpointMove` |
| 9 | Provider secrets stay under the provider's management on the runner | Adapters never read credential files; the runner passes an allowlisted environment; backups exclude provider logins; forge tokens are sealed on the hub | adapter tests; `docs/compatibility.md` |
| 10 | No merge, deploy, push, or outbound communication is implied by success | Push, merge, PR creation, and review publication need grants or an exact-action approval (`evaluatePolicy`, ADR 0006) | `TestExactActionApprovals`, `TestSharedCredentialCannotFabricateApproval` |
| 11 | Engineers work autonomously; genuine questions go in the room; only dependent work waits | `human_ask` posts an ordinary message; a natural reply resolves it once; no inbox; human acceptance only by policy | `TestAtlasFixReviewLoop`, `TestQuestionFlowAndLateReplies`, `TestHumanReviewPolicy` |
| 12 | Peer reviews bind to immutable revisions; internal approval ≠ forge merge eligibility | Rounds bind to base/head or artifact hash; new heads supersede; PR records keep remote reviews, checks, and merge state separately | `TestReviewDedupeAndRevisionBinding`, `TestWebhookSupersedesReviewOnNewCommits`, `TestSharedCredentialCannotFabricateApproval` |

## Request → result, end to end

1. **Message.** `POST /v1/rooms/{id}/messages` persists the message and its
   structured mentions in one transaction (`Hub.PostMessage`). It then
   resolves a correlated question, attaches steering input to a selected job,
   or routes: explicit mentions, then the thread owner, then the room steward.
2. **Reply run.** Routing creates a `reply` job and a `created` run. The
   scheduler picks an eligible machine (provider signed in, profile,
   capacity, account slot, workspace affinity), builds the context manifest
   from the destination room only, and commits an `offer_run` outbox row with
   a new lease epoch.
3. **Runner.** The runner journals the offer and acknowledges it. On
   `start_run` it prepares a workspace and launches the provider with only the
   `yip bridge` MCP server, passing a per-run token.
4. **Tools.** Tool calls go bridge → runner (unix socket) → hub (WebSocket,
   with run ID and epoch). The hub re-checks lease and access, executes, and
   records the result by call ID.
5. **Work.** `work_create` starts a job with an explicit repository. The
   author edits in a per-job worktree, publishes a revision (diff and bundle
   uploaded and hash-verified), runs checks through the runner, and picks a
   reviewer.
6. **Review.** Code rounds bind to the exact Git head; document and
   investigation rounds bind to the latest published document's content hash,
   even when the author also publishes a Git revision. The reviewer gets a
   read-only snapshot: code from the verified bundle, or the exact stored
   document as `review-artifact`. Only the leased reviewer with current room
   and project access can download that document through the runner endpoint.
   Code bundles and checkpoints must match the manifest's explicit
   `restoreArtifactId`; mentioning an artifact in chat grants no access.
   Verdicts must echo the full
   `expectedHead` or `expectedHash`. A corrected result needs a new round.
   On `changes_requested`, the author resumes, addresses the findings and
   requests re-review. Missing room membership or project read access is
   named in the source conversation; access is never widened automatically.
7. **Completion.** When the author has asked to complete and the current
   result has its required evidence (code head and checks, or document hash),
   the hub completes the job, posts the
   result, and auto-accepts the author's sourced decisions. There is no Accept
   click unless policy requires one.

   When nobody else is in the conversation to review (a direct message, or a
   workspace with one engineer), `work_create` records owner review in place
   of peer review, so the work waits for your acceptance rather than for a
   colleague who doesn't exist. A colleague who is present but lacks access is
   not waived: the author asks in the room (A42).

**Answering in the room.** An owner's reply resolves an engineer's question
when it targets it: in the question's thread or with an explicit reply target.
Mentioning the asker alone is a clarification, not an answer. In the web client, when exactly one question in the
room's main timeline is waiting on you, the composer targets it by default
and says so ("Answering Pip's question", with *Not an answer* one click
away for the next message only), so a plain answer in the room works as in any group chat without
yip guessing from text. With several open questions nothing is assumed. The
answer keeps a small receipt ("Answers Pip's question — the work waiting on
it resumed").


**Conversation and catch-up.** Tool results and context instructions keep
acknowledgments short and completion outcome-first. Internal work IDs,
absolute ledger timestamps and scheduling explanations stay out of ordinary
chat. One logical assignment produces one work-strip row; child review and
help jobs remain available in its details. Review labels come from the
current round on the current immutable result, never from job state alone.
`review_ready` means In review, not approved.

Overview derives catch-up from persisted event changes and current ledger
facts. Each changed root assignment appears once with its current summary,
open questions or permission requests, and the applicable review verdict.
Links open the work, conversation, question, review or decision. Unconfirmed
attempts remain unconfirmed. Older waits and old-version approvals are not
reported as current; the live ledger below also includes unchanged open
work. There is no new persisted status stream, and opening Overview does
not mark rooms read.

Routine shell edits and checks in the assigned checkout can use heredocs.
The policy parser separates a literal body from executable shell syntax,
checks trailing commands and retains approval for substitutions and
consequential actions. A decided approval folds into a short outcome with
a disclosure of its exact request; pending requests keep their full detail.

Permission requests and later owner decisions recheck the run's current room
and project access. A read-only attempt cannot push, merge or publish through
provider commands even when its engineer holds broader project grants.
Authorized yip review publication has its own checked tool path. An allowed
shell command with potential external effects prevents automatic crash retry,
just as a direct push or publication action does; rejected and routine local
commands retain safe retry behavior.

Conversation and review attempts require a provider that can enforce read-only
execution. An installation that reports otherwise remains queued with a named
limitation until its capability changes, rather than being dispatched to fail.

**Clarifying work in conversation.** A message to an engineer who is
working on something in the room right now goes into that work as the
owner's input (a reply would otherwise wait for the engineer to be free),
framed so the engineer decides whether it's about this work, another of
their assignments (they ask one short question), or a separate request
(they start separate work). When the engineer isn't working, their reply
sees their open assignments in the room and adds a clarification to the
right one with `work_add_input`, or asks which one if several fit. Either
way the owner's message shows "Added to Mira's …" with its delivery state
(delivering, queued for the next step, received), and nobody posts a second
acknowledgment. "Add to this work" stays as an explicit shortcut.

**Engineer notes and work records.** Context layer 5 (spec §8) is each
engineer's memory of earlier work (`engineer_notes`), visible only where
its sources are (a private room's note stays in that room).

- *Work records* are written by yip itself when work finishes, for the owner
  and for every colleague who reviewed it: the outcome, the final revision
  and who approved it, checks that passed on it, and how each review finding
  ended (with the author's reply). They restate the ledger, so they're kept
  without anyone's click; completing the same work again replaces its
  record. This is what lets an engineer answer "where did we land?" in
  another room without the owner curating memory.
- *Notes* are what an engineer (`note_record`) or the owner chooses to keep.
  They're kept automatically when every source is finished work the engineer
  did or reviewed, its result, or the owner's own words; a colleague's claim
  or open work leaves a quiet suggestion on the profile (no notification or
  queue).
- A run gets up to 6 records and 12 notes usable in its conversation, in
  projects the engineer can still access, and not due for review (records
  after a year, notes after 90 days, renewable). A corrected note carries
  what it replaced, who changed it and when, so the engineer can say "that
  was updated" instead of rewriting history.

**Late output.** A machine that reports for an attempt under an old lease
epoch is refused, and what it sent (run events, its final report, tool
calls) is kept, redacted and once, in `quarantined_output`. The work's
drawer shows it under that attempt as diagnostic evidence; it never posts,
completes, or publishes anything.

**Automatic retries.** A provider crash is retried automatically at most
twice (`MaxAutoRetries`), after 30 seconds and then 2 minutes, each with up
to 25% jitter, and only when the crashed attempt was not allowed any push,
publish, merge, or network action. Otherwise, and after the last automatic
retry, the work fails with its reason and waits for an explicit Retry. A
machine that goes quiet is different: its run becomes *not confirmed* and is
never retried blindly (invariant 8).

**Provider allowance.** An exhausted allowance pauses the whole provider
account (`provider_profiles.paused_until`), not just the run that hit it: the
scheduler holds other work for that account with the reason and resumes it
at the reset time the provider reported (or after a short wait). Work on
other accounts carries on. The pause shows on Machines.

Every step emits an event in the same transaction. Clients stream events from
a cursor and can always rebuild from `GET /v1/bootstrap`.
