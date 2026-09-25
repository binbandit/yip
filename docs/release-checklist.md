# Release checklist (A01–A44)

Status as of 25 September 2026. **Verified** means an automated test or a
recorded manual check exercised it end to end on this build. **Implemented**
means the behaviour exists but lacks a dedicated automated check. **Incomplete**
means it is not done or not verified; it blocks declaring the MVP complete.

Performance (§9 fixture, `TestLargeHistoryReadLatency`: 10,100 messages across
ten rooms and 100 jobs, this build machine): p95 bootstrap 20 ms, room page
0.9 ms, older page 0.9 ms, search 21 ms, jobs 0.25 ms, overview 5.5 ms —
within the 200 ms target. Measured, not an advertised capacity.

Automated evidence: `go test ./...` (unit tests; `test/integration` runs the
hub, a paired runner over mutual TLS, the bridge, and the deterministic fake
provider against a temporary database and git fixtures).

## Blocking items

These must be done before the MVP can be called complete (MVP brief §10):

1. **Real providers (A28, Slices B/C).** No real-account smoke test has been
   run for Codex, Claude Code, or Cursor. Cursor isn't installed on the build
   machine. Run the gated tests in `docs/compatibility.md` and record results.
2. **Two physical machines (Slice F).** Multi-machine behaviour is exercised
   with one hub and one runner per test on a single host; a real two-machine
   run (hub on one Mac mini, runner on another) has not been recorded.
3. **Browser verification (A26, A27).** No automatable browser is installed on
   the build machine; keyboard, zoom, and 390/1024/1440 layouts have not been
   inspected in a real browser. Playwright specs exist in `web/tests/e2e`.
4. **Runner reboot (A30)** under the intended service account has not been
   performed.

## Matrix

| ID | Status | Evidence / note |
|---|---|---|
| A01 | Verified (backend) | One engineer ID and version history across rooms; sessions keyed by a scope fingerprint that includes the room (`manifest.Fingerprint`, `TestFingerprintChangesWithScope`). Profile view: UI. |
| A02 | Verified | `TestScopeIsExplicitInMultiProjectRoom`; ambiguous repositories are rejected with the options (`resolveScope`); runs never switch directories. |
| A03 | Verified | `TestIdempotentSendQuietRoomsAndAgentMentions` |
| A04 | Verified | `TestEachMessageKeepsItsDestination` |
| A05 | Verified | `TestSharedAccountSingleSlot` |
| A06 | Verified | `TestIdempotentSendQuietRoomsAndAgentMentions` |
| A07 | Verified | `TestWakeupBudgetAndCycles` |
| A08 | Verified | `TestSteeringReceipts` (pending → immediate; queued when no active attempt) |
| A09 | Verified | `TestCancelJobTree`; unconfirmed termination is recorded as `unknown` (`applyTerminal`) |
| A10 | Verified (API) | `TestEventReplayAfterDisconnect` (gap-free replay from `Last-Event-ID`; `reset` for an unknown cursor). Runs are owned by hub and runner, never by the browser. Browser reconnect UX not yet exercised. |
| A11 | Verified | `TestOutboxRedeliveryExecutesOnce` (outbox rows forced back to pending across a hub restart) |
| A12 | Verified | Same test: the runner journal returns the original acknowledgement for a repeated command |
| A13 | Verified | `TestPartitionProducesUnknownThenReconciles` |
| A14 | Partial | Forge publications record a pending delivery before the network call and reconcile by marker before any retry. A push executed by a provider during a partition surfaces as an `unknown` run outcome; nothing replays it automatically. No end-to-end test. |
| A15 | Verified | `TestProviderAllowanceWaits` |
| A16 | Verified | `TestExactActionApprovals` (stale version, expiry, single use) |
| A17 | Verified | `TestPrivateCanaryIsolation` (context manifests, knowledge search, room search, decisions, replies; positive control) |
| A18 | Verified | `TestAccessRevokedMidJob`; grant/membership changes invalidate provider sessions |
| A19 | Partial | Separate worktree and branch per job; reviews on fixed revisions. A project-level integration lock for merges is not implemented (yip performs no merges itself). |
| A20 | Verified | `TestDoneWithoutEvidence` |
| A21 | Verified (manual) | Overview conversation answers from the ledger with timestamps and no engineer run (recorded run, 25 Sep). |
| A22 | Verified | `TestDecisionCorrection` |
| A23 | Verified | `TestIncompatibleMachineExplains` |
| A24 | Implemented | Workspaces are never deleted automatically; `yip runner workspaces` / `yip runner cleanup --workspace X --confirm X`. |
| A25 | Verified (manual) | `yip backup` against a running hub, `yip restore` into a new directory: integrity, 12 artifact hashes, and record counts matched (recorded 25 Sep). |
| A26 | Incomplete | Keyboard/zoom not verified in a browser. |
| A27 | Incomplete | Layouts not visually inspected in a browser. |
| A28 | **Incomplete** | Real providers not exercised (see blocking items). |
| A29 | Verified (partial) | Agent-authored mentions never wake anyone; tool arguments cannot set identity or scope (bridge binds the run token; hub derives authority from the lease). |
| A30 | Incomplete | Service install exists; reboot behaviour not yet tested. Restarted runners report interrupted attempts as unknown (ADR 0009). |
| A31 | Implemented | Each adapter excludes unapproved startup configuration (see `docs/compatibility.md`); verified against fakes only. |
| A32 | Verified | `TestAtlasFixReviewLoop` |
| A33 | Verified | `TestQuestionFlowAndLateReplies` |
| A34 | Verified | `TestQuestionFlowAndLateReplies`, `TestReplyAfterCancelDoesNotRestart` |
| A35 | Verified | `TestExactActionApprovals` (granted push proceeds without asking; exceptional request inline; engineers have no approval tool) |
| A36 | Verified | `TestHumanReviewPolicy` |
| A37 | Verified | `TestAtlasFixReviewLoop` (author selects the security reviewer) |
| A38 | Verified | `TestAtlasFixReviewLoop` (finding with file/line evidence; approval only of the revised head) |
| A39 | Verified | `TestReviewDedupeAndRevisionBinding` (an old approval can't satisfy a new head); `TestWebhookSupersedesReviewOnNewCommits` (new PR commits supersede the open round and schedule exactly one new round). Not yet exercised against real GitHub. |
| A40 | Verified | `TestSharedCredentialCannotFabricateApproval` |
| A41 | Verified | Duplicate review requests map to one round (`TestReviewDedupeAndRevisionBinding`); a replayed webhook delivery changes nothing (`TestWebhookSupersedesReviewOnNewCommits`); publications reconcile by marker before retry. |
| A42 | Verified | `TestNoPermittedReviewer` |
| A43 | Verified | `TestDocumentReviewWithoutForge` (changes requested on one document version, approval of the revised version, no forge). |
| A44 | Verified | `TestSharedCredentialCannotFabricateApproval` (failing checks, blocked merge, and internal approval shown as separate facts; nothing merged) |
