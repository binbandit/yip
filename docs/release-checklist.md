# Release checklist (A01–A44)

Status as of 26 September 2026. **Verified** means an automated test or a
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

1. **Real providers (A28, Slices B/C).** Sign-in detection has run against
   the real installations (Claude Code: signed in on a subscription; Codex:
   installed, not signed in), but no prompt has been sent through any real
   provider. Cursor isn't installed on the build machine. Run the gated tests
   in `docs/compatibility.md` (see `docs/scenario.md`) and record results.
2. **Two physical machines (Slice F).** `TestTwoMachinesAndCheckpointMove`
   runs two independent runners (separate state, replicas, journals, and
   certificates) against one hub: concurrent jobs land on different machines,
   and after a machine is revoked, work resumes elsewhere only from the
   verified bundle of its published revision, as an explicit retry. All of
   this ran on a single host; a run across two physical machines has not been
   recorded.
3. **Browser verification (A26, A27).** No automatable browser is installed on
   the build machine; keyboard, zoom, and 390/1024/1440 layouts have not been
   inspected in a real browser. Playwright specs exist in `web/tests/e2e`.
4. **Runner reboot (A30)** under the intended service account has not been
   performed.

## Backend review (25 September)

An adversarial review of the hub and runner found 14 defects (six high, eight
medium) and several minor ones: unrestricted command execution through
`work_run_check`, jobs that could wedge a machine or lose a wakeup, removed
members still being woken, offers stuck after cancellation, ambiguous
terminal acknowledgements, tool calls that could run twice after a
reconnect, a completion policy that let one approval outvote requested
changes, runner artifact endpoints that ignored revocation, unbounded
sign-in cost, and incomplete redaction. All are fixed; the rules are recorded
in ADRs 0005, 0006, and 0011, and each is pinned by a
`TestRegression…` test in `test/integration/regression_test.go` (plus
classifier unit tests in `internal/hub/policy_test.go`). `go test -race` over
`internal/...` and `test/integration` reports no data races (the latency
benchmark is skipped under the race detector).

## Spec review (26 September)

A second pass over the spec against the running product found these gaps,
now closed (each with a test):

- First-run journey on Overview (§7A), derived from live state and ending
  with finished work, not a connection dot.
- Provider accounts: sign-in re-check on demand, per-account concurrency,
  pinning an engineer to one account, no silent API billing, and an
  exhausted allowance pausing the whole account until it resets.
- Search by short work ID and by project, filtered before ranking.
- Overview status covering every project (quiet ones named) and what waits
  on you, with active work never crowded out.
- Owner review in place of peer review when nobody else is in the
  conversation; a present colleague without access still gets asked (A42).
- Interrupt and restart for updates a provider can only queue (§8D).
- Follow-up work linked to the finished work it follows (§8), in both
  drawers and in the engineer's context.
- Correcting an accepted decision from its drawer (§6), keeping its sources.
- Review findings open the reviewed revision's diff at the file and line.
- Project machine requirements (tools or OS) with the missing piece named.
- Bounded automatic retry of provider crashes with backoff and jitter, only
  without external side effects.
- Room muting (notifications only), failure notifications, pairing that
  watches for the machine, an opt-in previewed diagnostic bundle, project
  chips from typed names, a DM draft marker, and schema backups before
  migration with a refusal to open a newer database.

Still open (not blocking the scenario, recorded here so they aren't lost):

- Idempotency keys cover messages, job input, review requests, and forge
  publications; other mutations rely on optimistic versions.
- Engineer memory beyond accepted decisions (promoted notes with provenance)
  isn't implemented.
- The GitHub contract test needs a real repository and token; browser e2e
  specs need a browser on the build machine.

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
| A08 | Verified | `TestSteeringReceipts` (pending → immediate; queued when no active attempt); explicit interrupt and restart for queued updates (`TestRegressionInterruptAndRestart`, composer unit test) |
| A09 | Verified | `TestCancelJobTree`; unconfirmed termination is recorded as `unknown` (`applyTerminal`) |
| A10 | Verified (API) | `TestEventReplayAfterDisconnect` (gap-free replay from `Last-Event-ID`; `reset` for an unknown cursor). Runs are owned by hub and runner, never by the browser. Browser reconnect UX not yet exercised. |
| A11 | Verified | `TestOutboxRedeliveryExecutesOnce` (outbox rows forced back to pending across a hub restart) |
| A12 | Verified | Same test: the runner journal returns the original acknowledgement for a repeated command. A re-sent tool call returns its recorded result (`TestRegressionToolCallRetryRunsOnce`); terminal reports are settled only by an explicit, epoch-matched ack (`TestRegressionTerminalAckIsExplicit`). |
| A13 | Verified | `TestPartitionProducesUnknownThenReconciles` |
| A14 | Partial | Forge publications record a pending delivery before the network call and reconcile by marker before any retry. A push executed by a provider during a partition surfaces as an `unknown` run outcome; nothing replays it automatically. No end-to-end test. |
| A15 | Verified | `TestProviderAllowanceWaits` (the account pauses until its reset; other work on that account is held with the reason and resumes by itself) |
| A16 | Verified | `TestExactActionApprovals` (stale version, expiry, single use); checks go through the same policy (`TestRegressionRunCheckIsPolicedAndIsolated`) |
| A17 | Verified | `TestPrivateCanaryIsolation` (context manifests, knowledge search, room search, decisions, replies; positive control); removed members are neither woken nor given new messages (`TestRegressionRemovedMemberNotRoutedOrLeaked`); deleted messages leave no copy in events, jobs, run context, or search (`TestRegressionRedactionIsComplete`) |
| A18 | Verified | `TestAccessRevokedMidJob`; grant/membership changes invalidate provider sessions |
| A19 | Partial | Separate worktree and branch per job; reviews on fixed revisions. A project-level integration lock for merges is not implemented (yip performs no merges itself). |
| A20 | Verified | `TestDoneWithoutEvidence`; `work_respond` can't complete the caller's own job (`TestRegressionWorkRespondCannotSelfComplete`) |
| A21 | Verified | Overview conversation answers from the ledger with timestamps and no engineer run, covering every project and what waits on you (`TestRegressionOverviewStatusCoversAllProjects`) |
| A22 | Verified | `TestDecisionCorrection`; correction from the decision drawer keeps the sources (unit test) |
| A23 | Verified | `TestIncompatibleMachineExplains`, `TestRegressionBillingGateAndAccountPin`, `TestRegressionProjectToolchainRequirement` |
| A24 | Verified | Workspaces are never deleted automatically. Machines lists them with their work and what deleting loses; removal needs explicit selection, a named confirmation, and `force` for uncommitted/unpublished work, and is refused for open or in-use work (`TestRegressionRemoveWorkspaceFromMachines`, Machines unit test). CLI: `yip runner workspaces` / `cleanup`. |
| A25 | Verified | `yip backup` against a running hub, `yip restore` into a new directory: integrity, 12 artifact hashes, and record counts matched (recorded 25 Sep). Encrypted backups round-trip, carry no readable key material or database, and refuse a wrong passphrase leaving nothing behind (`TestEncryptedBackupRoundTrip`, `internal/backupcrypt` tests for tampering and truncation). |
| A26 | Incomplete | Keyboard/zoom not verified in a browser. |
| A27 | Incomplete | Layouts not visually inspected in a browser. |
| A28 | **Incomplete** | Real providers not exercised (see blocking items). |
| A29 | Verified (partial) | Agent-authored mentions never wake anyone; tool arguments cannot set identity or scope (bridge binds the run token; hub derives authority from the lease). |
| A30 | Incomplete | Service install exists; reboot behaviour not yet tested. Restarted runners report interrupted attempts as unknown (ADR 0009). |
| A31 | Implemented | Each adapter excludes unapproved startup configuration (see `docs/compatibility.md`); verified against fakes only. |
| A32 | Verified | `TestAtlasFixReviewLoop` |
| A33 | Verified | `TestQuestionFlowAndLateReplies` |
| A34 | Verified | `TestQuestionFlowAndLateReplies`, `TestReplyAfterCancelDoesNotRestart` |
| A35 | Verified | `TestExactActionApprovals` (granted push proceeds without asking; exceptional request inline; engineers have no approval tool); commands are parsed, so `git -C . push` and similar spellings reach the same decision (`internal/hub/policy_test.go`) |
| A36 | Verified | `TestHumanReviewPolicy` |
| A37 | Verified | `TestAtlasFixReviewLoop` (author selects the security reviewer) |
| A38 | Verified | `TestAtlasFixReviewLoop` (finding with file/line evidence; approval only of the revised head) |
| A39 | Verified | `TestReviewDedupeAndRevisionBinding` (an old approval can't satisfy a new head); `TestWebhookSupersedesReviewOnNewCommits` (new PR commits supersede the open round and schedule exactly one new round). Every active reviewer must be satisfied on the current head (`TestRegressionCompletionNeedsEveryReviewer`). Not yet exercised against real GitHub. |
| A40 | Verified | `TestSharedCredentialCannotFabricateApproval` |
| A41 | Verified | Duplicate review requests map to one round (`TestReviewDedupeAndRevisionBinding`); a replayed webhook delivery changes nothing (`TestWebhookSupersedesReviewOnNewCommits`); publications reconcile by marker before retry. |
| A42 | Verified | `TestNoPermittedReviewer`; with no colleague in the conversation at all, the owner reviews instead (`TestRegressionSoloEngineerOwnerReviews`) |
| A43 | Verified | `TestDocumentReviewWithoutForge` (changes requested on one document version, approval of the revised version, no forge). |
| A44 | Verified | `TestSharedCredentialCannotFabricateApproval` (failing checks, blocked merge, and internal approval shown as separate facts; nothing merged) |
