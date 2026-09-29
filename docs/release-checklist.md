# Release checklist (A01–A44)

Status as of 29 September 2026. **Verified** means an automated test or a
recorded manual check exercised it end to end on this build. **Implemented**
means the behaviour exists but lacks a dedicated automated check. **Incomplete**
means it is not done or not verified; it blocks declaring the MVP complete.

Performance (28 September, §9 fixture, `TestLargeHistoryReadLatency`: 10,100
messages across ten rooms and 100 jobs, this build machine): p95 bootstrap
37.354 ms, room page 1.422 ms, older page 1.339 ms, search 23.545 ms,
jobs 0.392 ms,
within the 200 ms target. Measured, not an advertised capacity.

Automated evidence: `go test ./...` (unit tests; `test/integration` runs the
hub in process against a temporary database, with the test playing the runner
over the runner protocol). Removing the fake provider (ADR 0004) also removed
the integration tests that drove scripted provider runs, so gates they
verified are now **Implemented** until they have an automated check again.
The security, recovery and publication gates have been rewritten with the
test making the runner's tool calls and permission requests itself
(`test/integration/gates_test.go`, `publication_test.go`) and are
**Verified** again; the conversation and review-loop gates are not yet.

## Blocking items

These must be done before the MVP can be called complete (MVP brief §10):

1. **Remaining provider coverage (A28, Slices B/C).** Real Codex and Claude
   smoke tests, team collaboration, cross-room recall and a second feature
   with cancellation/retry and two restarts passed on 28 September. This
   machine's Codex allow-rules prevent conversation/review mode; the tested
   configuration uses Claude for those roles and Codex for edits. Versions
   are newer than the schema pins. Cursor is not installed or real-tested.
2. **Two physical machines (Slice F).** The two-runner integration test
   (concurrent jobs on different machines, and resumption elsewhere from the
   verified bundle after a revocation) ran on the removed fake provider
   (ADR 0004). A run across two physical machines has not been recorded.
3. **Remaining browser verification (A26, A27).** On 28 September system
   WebKit passed 53 distinct journeys and installed Chrome 153 passed six
   Playwright journeys. They ran against the removed demo (ADR 0004), so no
   browser journeys remain. Firefox remains untested.
4. **Runner reboot (A30)** under the intended service account has not been
   performed.

Cross-machine recovery currently uses the last published revision's verified
bundle. Unpublished edits depend on the retained workspace on the original
machine; checkpoint records do not provide general cross-machine restoration.

Older hubs may also contain an empty-ID PR record from the now-fixed link
collision. New PRs are independent, but the legacy record is retained to
preserve review references. Its browser/context access and any previously
overwritten remote facts require repair before relying on that historical PR.
No live hub data was changed during the campaign.

## Daily-use campaign (28 September)

The owner authorized dedicated private/public GitHub playgrounds, three
signed-in GitHub actors, and bounded real subscription-provider runs. The
[campaign record](simulations/2026-09-28.md) separates actual GitHub/provider
results from scripted simulations and links the retained fixtures.

- 32 live GitHub scenarios, three account-specific connector contracts and
  three actual hub/runner/bridge publication scenarios passed. These include
  fork contributions, requested changes, stale approvals, branch protection,
  failing/repaired CI, revoked access and publication identity.
- Real Codex and Claude smoke tests passed. The team completed an actual fix
  and independent exact-revision review, followed by recall from another room.
  A later feature survived cancellation, explicit retry and two restarts.
- Persistent-workspace and concurrency campaigns exercised repeated work,
  queued input, duplicate delivery, correction/re-review, account-slot limits,
  restart durability and artifact integrity. These are bounded runs, not a
  multi-day unattended reliability claim.
- All 53 WebKit journeys, six Chrome journeys and 137 web tests passed.
  Type checking reports zero errors and warnings.
- Final complete Go suite: 342 passed, zero failed, nine external gates
  skipped. Matching complete race coverage: 341 passed, zero failed, ten
  skipped (the same gates plus the latency fixture). Counts include named
  subtests and parent outcomes. All 90 top-level integration tests ran across
  two nonoverlapping race groups. Build, formatting and Go vet passed.

The daily-use hub and development server were not changed. This provides
evidence for the tested Claude-intake/review plus Codex-edit configuration;
it does not certify the outstanding deployment and provider combinations.

## UX campaign (28 September)

The [UX report](simulations/2026-09-28-ux.md) compares the working product with
the owner's original discussion, read in their signed-in browser. It records
new first-use, multi-project, interrupted-message, approval-race, live
catch-up, and responsive checks separately from the earlier reliability
campaign above. A new bounded Claude conversation recalled both saved real
results and their publication state; its chronological and brevity limits
are recorded rather than treated as a complete memory guarantee.

## Backend review (25 September)

An adversarial review of the hub and runner found 14 defects (six high, eight
medium) and several minor ones: unrestricted command execution through
`work_run_check`, jobs that could wedge a machine or lose a wakeup, removed
members still being woken, offers stuck after cancellation, ambiguous
terminal acknowledgements, tool calls that could run twice after a
reconnect, a completion policy that let one approval outvote requested
changes, runner artifact endpoints that ignored revocation, unbounded
sign-in cost, and incomplete redaction. All are fixed; the rules are recorded
in ADRs 0005, 0006, and 0011. Each was pinned by a `TestRegression…` test in
`test/integration/regression_test.go`, plus classifier unit tests in
`internal/hub/policy_test.go`. The regression tests that drove scripted
provider runs were removed with the fake provider (ADR 0004); those for
removed members, redaction, self-completion and heredoc edits have been
rewritten with the test playing the runner. `go test -race` over
`internal/...` and `test/integration` reports no data races (the latency
benchmark is skipped under the race detector).

## Spec review (26 September)

A second pass over the spec against the running product found these gaps,
now closed (each with a test):

- First-run journey (§7A), derived from live state and ending with finished
  work, not a connection dot. It was on the Overview and is now Getting
  started (ADR 0016).
- Provider accounts: sign-in re-check on demand, per-account concurrency,
  pinning an engineer to one account, no silent API billing, and an
  exhausted allowance pausing the whole account until it resets.
- Search by short work ID and by project, filtered before ranking.
- Overview status covering every project (quiet ones named) and what waits
  on you, with active work never crowded out. Withdrawn with the Overview
  (ADR 0016).
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

At this point GitHub and Chromium still needed real runs. Both were exercised
in the 28 September campaign above; Firefox remains untested.

## Team conversation verification (26 September)

The scripted acceptance path now covers one assignment, a clarification,
a genuine question, an explicit answer, peer requested changes, an author
correction and re-review, completion, and recall from another room. The
owner sends only the assignment, clarification and answer. The final
reviewed revision includes the clarified rollout scope. Overview links the
completed work to its review evidence, which can be opened with Enter.

Code verdicts bind to the full Git head; document verdicts and completion
bind to the same published content hash. Review labels distinguish queued,
active, requested changes, approved and older-version evidence. Catch-up
uses the current ledger, preserves unconfirmed outcomes and links open
questions and permission requests without duplicating assignments.

The final visual review also caught overlapping Overview side panels. Evidence
now replaces the optional conversation, narrow columns wrap their controls,
and setup starts compact once recorded work exists.

The verification pass also fixed a late-input race in the scripted provider
and synchronized the idle-engineer test with the end of its actual attempt.
The race detector passed three repetitions of the provider regression and
both clarification paths; the idle path then passed ten repetitions.

Room captures covered 390, 900, 1280 and 1440px in both themes, and Overview
captures covered the summary and review evidence at each width and theme.
The capture scripts ran against the removed demo (ADR 0004); the captures
are available in Git history, not the current checkout. Architecture and
design docs describe the resulting behaviour.

Final verification results:

| Check | Passed | Failed | Intentionally skipped |
|---|---:|---:|---:|
| Go tests, including named subtests | 262 | 0 | 7 |
| Web unit and mounted-app tests | 133 | 0 | 0 |
| System WebKit journeys (all four files together) | 34 | 0 | 0 |
| Room captures, four widths in both themes | 8 | 0 | 0 |
| Overview summary and review, four widths in both themes | 16 | 0 | 0 |

The combined journey and room capture run preceded the final Overview layout
correction. All 133 web tests and the 16 targeted Overview checks passed
afterward, including expanded setup controls and Enter/Escape review access.

Formatting and Go vet pass. Svelte check reports zero errors and warnings;
the production build succeeds. Generated schemas are current. The WebKit
run uses the scripted provider with `YIP_FAKE_DELAY=300ms`; its Machines
fixture already uses the same pace. Keyboard events come from the system
WebKit runner, including focus containment and return, mention selection,
search shortcuts, tab navigation, and opening review evidence with Enter.

Real-provider prompts and account/auth checks were **not run in this pass**.
At that time the gated provider tests and GitHub contract test remained
owner-run checks; later authorization and results are recorded above.
No dependencies or browsers were downloaded, and the LAN hub and
other chats' test hubs were not used. Builds reused installed web dependencies
(`make -o web-deps all`) and the existing Go module cache offline.

## Matrix

| ID | Status | Evidence / note |
|---|---|---|
| A01 | Verified (backend) | One engineer ID and version history across rooms; sessions keyed by a scope fingerprint that includes the room (`manifest.Fingerprint`, `TestFingerprintChangesWithScope`). Profile view: UI. |
| A02 | Implemented | Ambiguous repositories are rejected with the options (`resolveScope`); runs never switch directories. Previously verified by `TestScopeIsExplicitInMultiProjectRoom`, removed with the fake provider (ADR 0004). |
| A03 | Implemented | Previously verified by `TestIdempotentSendQuietRoomsAndAgentMentions`, removed with the fake provider (ADR 0004). |
| A04 | Implemented | Previously verified by `TestEachMessageKeepsItsDestination`, removed with the fake provider (ADR 0004). |
| A05 | Implemented | Previously verified by `TestSharedAccountSingleSlot`, removed with the fake provider (ADR 0004). |
| A06 | Implemented | Previously verified by `TestIdempotentSendQuietRoomsAndAgentMentions`, removed with the fake provider (ADR 0004). |
| A07 | Implemented | Previously verified by `TestWakeupBudgetAndCycles`, removed with the fake provider (ADR 0004). |
| A08 | Implemented | Explicit interrupt and restart for queued updates (composer unit test). Steering receipts (pending → immediate; queued when no active attempt) were previously verified by `TestSteeringReceipts` and `TestRegressionInterruptAndRestart`, removed with the fake provider (ADR 0004). |
| A09 | Implemented | Unconfirmed termination is recorded as `unknown` (`applyTerminal`). Previously verified by `TestCancelJobTree`, removed with the fake provider (ADR 0004). |
| A10 | Verified | `TestEventReplayAfterDisconnect` (gap-free replay from `Last-Event-ID`; `reset` for an unknown cursor). Runs are owned by hub and runner. The browser simulation of a lost HTTP response ran against the removed demo (ADR 0004). |
| A11 | Verified | After a hub restart the outbox resends the offer and start, and a duplicate acknowledgement settles them as one run with one reply (`TestOutboxRedeliveryExecutesOnce`, with a test node as the runner). The real runner starts its provider once when the start is redelivered, re-sent under a new command, or redelivered with the offer after the runner restarts (`TestRedeliveredStartRunsTheProviderOnce`, counting adapter). The journal returns the original acknowledgement after a restart and keeps unacknowledged output (`TestJournalAnswersRepeatedCommandAfterRestart`, `TestJournalKeepsUnacknowledgedOutput`). |
| A12 | Verified | A re-sent tool call returns its recorded result (`TestRegressionToolCallRetryRunsOnce`); terminal reports are settled only by an explicit, epoch-matched ack (`TestRegressionTerminalAckIsExplicit`); a repeated command gets the journal's original acknowledgement and a duplicate offer never starts a second provider session (`TestJournalAnswersRepeatedCommandAfterRestart`, `TestRedeliveredStartRunsTheProviderOnce`). |
| A13 | Implemented | Previously verified by `TestPartitionProducesUnknownThenReconciles`, removed with the fake provider (ADR 0004). |
| A14 | Partial | Forge publications record a pending delivery before the network call and reconcile by marker before any retry (`TestPublication…`: failed then successful retry, ambiguous response, concurrent calls, a failed save, a late error, and pre-fix retry records). A push executed by a provider during a partition surfaces as an `unknown` run outcome; nothing replays it automatically, and that path has no end-to-end test. |
| A15 | Implemented | The account pauses until its reset; other work on that account is held with the reason and resumes by itself. Previously verified by `TestProviderAllowanceWaits`, removed with the fake provider (ADR 0004). |
| A16 | Verified | Stale versions conflict, a decision is single use, and an undecided request expires and denies (`TestExactActionApprovals`). The runner's check tool asks the hub about the exact command and revision, runs nothing when denied or when the run is stopping, and runs and records an allowed check (`TestRunCheckAsksBeforeRunning`); the hub decides that request with the same policy as provider commands, so a push needs the owner (`internal/hub/policy_test.go`, `TestExactActionApprovals`). Checks run with a scratch home and no agent socket or credential helper (`TestCheckEnvIsIsolated`); reply mode doesn't offer them (bridge mode test). |
| A17 | Verified | Context manifests, knowledge search, room search, decisions and replies stay private (`TestPrivateCanaryIsolation`); removed members are neither woken nor given new messages (`TestRegressionRemovedMemberNotRoutedOrLeaked`); deleted messages leave no copy in events, jobs, run context, or search (`TestRegressionRedactionIsComplete`). |
| A18 | Verified | Grant/membership changes invalidate provider sessions. After write access is removed mid-job, the attempt's tool calls are refused and its permission requests denied (`TestAccessRevokedMidJob`). |
| A19 | Partial | Separate worktree and branch per job; reviews on fixed revisions. A project-level integration lock for merges is not implemented (yip performs no merges itself). |
| A20 | Verified | "Done" without evidence names what's missing and leaves the work stalled, with no human task (`TestDoneWithoutEvidence`); `work_respond` can't complete the caller's own job (`TestRegressionWorkRespondCannotSelfComplete`). |
| A21 | Withdrawn | The Overview conversation was removed with journey F ([0016](decisions/0016-no-overview.md)); status is still read from the ledger, never by waking an engineer |
| A22 | Implemented | Correction from the decision drawer keeps the sources (unit test). Previously verified by `TestDecisionCorrection`, removed with the fake provider (ADR 0004). |
| A23 | Verified (partial) | `TestRegressionBillingGateAndAccountPin`; incompatible machines wait with the reason and are never offered the work (`TestIncompatibleMachineExplains`), as does work for a provider the hub doesn't support (`TestUnsupportedProviderExplainsAndFollowsTheNewChoice`). Project toolchain requirements were verified by `TestRegressionProjectToolchainRequirement`, removed with the fake provider (ADR 0004). |
| A24 | Implemented | Workspaces are never deleted automatically. Machines lists them with their work and what deleting loses; removal needs explicit selection, a named confirmation, and `force` for uncommitted/unpublished work, and is refused for open or in-use work (Machines unit test). CLI: `yip runner workspaces` / `cleanup`. Previously verified by `TestRegressionRemoveWorkspaceFromMachines`, removed with the fake provider (ADR 0004). |
| A25 | Verified | `yip backup` / `yip restore` verified integrity, artifact hashes and record counts (25 Sep). On 28 Sep a new restored hub accepted the owner login and retained both real-provider approved results, reviewer records and 11 unique artifact blobs. Encrypted backups round-trip and refuse a wrong passphrase leaving nothing behind (`TestEncryptedBackupRoundTrip`, `internal/backupcrypt` tampering/truncation tests). |
| A26 | Implemented | On 28 September, real key presses in system WebKit covered the skip link, mentions, Enter to send, drawer focus trapping/return, search, and 200% zoom, and six installed-Chrome journeys covered keyboard, drawers and search. Those journeys ran against the removed demo (ADR 0004). Firefox untested. |
| A27 | Implemented | On 28 September, system WebKit layouts at 390, 1024 and 1440 px, mobile controls, crowded selectors and a Chrome mobile journey passed. Those journeys ran against the removed demo (ADR 0004). Firefox untested. |
| A28 | Verified in tested configuration | Real Codex edit/permission/resume and Claude MCP/permission smoke tests, independent team review, recall, cancellation/retry and restart durability passed. Cursor and other combinations remain unverified. |
| A29 | Verified (partial) | Agent-authored mentions never wake anyone; tool arguments cannot set identity or scope (bridge binds the run token; hub derives authority from the lease). |
| A30 | Incomplete | Service install exists; reboot behaviour not yet tested. Restarted runners report interrupted attempts as unknown (ADR 0009). |
| A31 | Implemented | Each adapter excludes unapproved startup configuration (see `docs/compatibility.md`); verified against fakes only. |
| A32 | Implemented | Previously verified by `TestAtlasFixReviewLoop`, removed with the fake provider (ADR 0004). |
| A33 | Implemented | Previously verified by `TestQuestionFlowAndLateReplies`, removed with the fake provider (ADR 0004). |
| A34 | Implemented | Previously verified by `TestQuestionFlowAndLateReplies` and `TestReplyAfterCancelDoesNotRestart`, removed with the fake provider (ADR 0004). |
| A35 | Verified | Engineers have no approval tool. Commands are parsed, so `git -C . push` and similar spellings reach the same decision (`internal/hub/policy_test.go`). Decided cards collapse and reopen the exact action (web action tests). The exceptional request appears inline and a granted push proceeds without asking (`TestExactActionApprovals`); checkout heredoc edits need no approval (`TestRegressionHeredocEditNeedsNoApproval`). |
| A36 | Implemented | Previously verified by `TestHumanReviewPolicy`, removed with the fake provider (ADR 0004). |
| A37 | Implemented | The author selects the security reviewer. Previously verified by `TestAtlasFixReviewLoop`, removed with the fake provider (ADR 0004). |
| A38 | Implemented | Findings carry file/line evidence; only the revised head can be approved. Previously verified by `TestAtlasFixReviewLoop`, removed with the fake provider (ADR 0004). |
| A39 | Implemented | An old approval can't satisfy a new head; new PR commits schedule one new round; every active reviewer must be satisfied on the current head. Real protected GitHub fixtures verified that new commits dismiss stale approvals. Previously verified by `TestReviewDedupeAndRevisionBinding`, `TestWebhookSupersedesReviewOnNewCommits` and `TestRegressionCompletionNeedsEveryReviewer`, removed with the fake provider (ADR 0004). |
| A40 | Verified | An engineer sharing the PR author's credential records an internal approval, but nothing is posted to the forge (`TestSharedCredentialCannotFabricateApproval`). |
| A41 | Implemented | Duplicate review requests map to one round; a replayed webhook delivery changes nothing; publications reconcile by marker before retry (`TestPublication…`). Review dedupe and webhook replay were verified by `TestReviewDedupeAndRevisionBinding` and `TestWebhookSupersedesReviewOnNewCommits`, removed with the fake provider (ADR 0004). |
| A42 | Implemented | Missing project read access is named; initial and subsequent rounds also check room membership. With no colleague in the conversation at all, the owner reviews instead. Previously verified by `TestNoPermittedReviewer` and `TestRegressionSoloEngineerOwnerReviews`, removed with the fake provider (ADR 0004). |
| A43 | Implemented | Document review and completion use the same content hash, with or without a Git revision; corrected content requires re-review; reviewers inspect the exact stored artifact and reject a mismatched hash. Previously verified by `TestDocumentReviewWithoutForge`, `TestRegressionDocumentCorrectionAndRereview` and `TestRegressionCorrectingApprovedDocumentRequiresNewApproval`, removed with the fake provider (ADR 0004). |
| A44 | Verified | Failing checks, blocked merge, and internal approval are shown as separate facts; nothing is merged (`TestSharedCredentialCannotFabricateApproval`). |
