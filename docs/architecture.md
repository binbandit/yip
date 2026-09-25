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
6. **Review.** A review round binds to the exact head. The reviewer gets a
   read-only snapshot and records a verdict with evidence. On
   `changes_requested`, the author is woken — or the wakeup is held until the
   current attempt ends.
7. **Completion.** When the author has asked to complete and the final head
   is approved with passing checks, the hub completes the job, posts the
   result, and auto-accepts the author's sourced decisions. There is no Accept
   click unless policy requires one.

Every step emits an event in the same transaction. Clients stream events from
a cursor and can always rebuild from `GET /v1/bootstrap`.
