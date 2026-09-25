# yip · MVP implementation brief

Version 1.2 · 25 September 2026 · Build contract

Working name: **yip**. The possible domain is `getyip.dev`; see the dated naming checks in the research appendix.

## 1. Release objective

Deliver a usable, self-hosted team workspace in which one human can collaborate with persistent AI engineers across rooms and projects, run work on multiple machines, interrupt it, obtain evidence, and return after closing the laptop. The release is complete when the acceptance criteria below pass. This document gives dependency order and outcome gates, not effort estimates or a calendar.

The product succeeds when the user stops carrying context and results between disconnected agent sessions. A beautiful chat around a collection of fragile terminal processes does not meet this objective.

Read the [architecture](01-solution-architecture.md) and [design document](02-product-and-design.md) first. Preserve their invariants. Use the [research appendix](04-research-and-evidence.md) to verify dated provider assumptions before implementation.

## 2. Scope

### Required for the first complete release

- One organisation and one human owner with authenticated browser access.
- macOS Apple Silicon and Linux runners; browser access from the laptop and phone.
- One hub, multiple registered machines, one-machine operation, and at least two simultaneous remote jobs on eligible resources.
- Persistent engineers, editable roles, room membership independent of provider sessions, and an engineer belonging to several rooms.
- Group rooms, DMs, single-level threads, mentions, reactions, search, draft preservation, and unread/mention state.
- Multiple projects and repositories; a room may link more than one project; each editing run has one explicit repository.
- Codex, unmodified Claude Code, and Cursor adapters using the documented surfaces. All three need a real smoke-test record; provider-specific features may vary visibly.
- Natural-language work requests, durable jobs, explicit ownership, dependencies, review requests, structured agent-to-agent communication, bounded wakeups, and autonomous routine decisions within granted scope.
- Peer reviews of patches, documents, and pull requests, including findings, author replies, revisions, and re-review. GitHub is the first forge connector; internal reviews also work without a forge connection.
- Context manifests, scoped decisions with sources, cross-room recall where allowed, and strict private-context isolation.
- Streaming progress, useful catch-up, factual cross-project status, evidence drawer, evidence-backed completion, optional explicitly required human review, and conversational requests for changes.
- Native execution and a supported Linux-container profile; never silently substitute an unrestricted profile.
- Stop/cancel, queued follow-ups, active steering where supported, approval and question handling, quota waiting, and visible recovery states.
- Per-job workspaces, result diffs, checks, checkpoints, and exportable artifacts.
- Backups, restoration test, version compatibility checks, basic diagnostics, and opt-in log export.
- Complete day/night design, keyboard use, responsive layouts, and accessibility verification.

### Explicitly outside this release

Multiple human seats/enterprise SSO, hosted multi-tenancy, hub high availability, public federation, blockchain or Nostr, native mobile apps, voice/video calls, an agent marketplace, a visual workflow builder, an integrated Git forge/CI service, autonomous payment/business systems, arbitrary plugin execution, offline agent execution, and automatic migration of live vendor sessions between machines.

The schema should support later human memberships, but do not advertise enterprise readiness or implement speculative org-chart mechanics. No feature is excluded merely because of an assumed human development timeline.

## 3. Required demonstration scenario

Ship deterministic fixtures and a repeatable real-provider walkthrough:

1. Create engineers **Mira** (platform), **Oren** (security review), and **Pip** (reverse engineering). These are configurable examples, not built-in personalities the product depends on.
2. Put Mira and Oren in Security; Mira and Pip in Engineering. Link Atlas and Beacon projects where permitted.
3. In Security, ask Mira to fix Atlas's session-expiry bug without assigning a reviewer. A fixture repository contains a real, seeded regression test. Mira independently selects Oren for security review.
4. Mira creates a job and an isolated patch, requests review, and records evidence. Oren reviews a fixed commit and finds an intentionally seeded defect in the refresh path. Mira fixes it, adds the regression, and requests re-review. Oren verifies the new revision and approves. Exercise the same flow on an authorized test PR. Neither participant needs the owner to dispatch, relay feedback, or click Accept.
5. In Engineering, ask Pip to document Beacon's request flow. This creates different work with a separate context and destination.
6. Add a constraint to Mira's current job. Verify the receipt distinguishes immediate steering from queued input.
7. Close the browser/laptop client while execution stays on the minis. Reconnect and inspect a sourced catch-up.
8. Ask for overall status in Overview. It accurately reports both projects, including any blocker, with last-confirmed timestamps and source links.
9. Mira preserves the documented strict-expiry behaviour and completes the code job after checks and Oren’s review, without asking the owner to choose a grace period or click Accept. The owner can inspect the diff and evidence afterward. Make a later request referencing the established Atlas decision; verify correct recall. Separately test an explicitly configured human-review requirement.
10. Place a canary fact in a private room and confirm it is absent from a broader room's context, messages, search results, and summaries.
11. Pip cannot locate the separate Beacon retry-worker repository after checking linked repositories and docs. Pip asks once in Reverse engineering, mentions the owner, and continues gateway analysis. A normal reply resolves only the waiting dependency; no separate attention inbox appears.

A scripted simulation is useful for automated verification. It does not replace exercising the real adapters and the two-machine path.

## 4. Repository structure

```text
cmd/yip/                    hub, runner, doctor, backup, restore CLI entrypoints
internal/auth/              owner sessions, pairing, certificates, revocation
internal/domain/            identities, jobs, state machines, policies
internal/store/             SQLite repositories, migrations, transactions
internal/events/            outbox, replay, delivery deduplication
internal/scheduler/         eligibility, fairness, quotas, leases
internal/context/           manifests, scoped retrieval, memory provenance
internal/runner/            supervision, workspaces, journal, checkpoints
internal/providers/codex/   pinned app-server adapter
internal/providers/claude/  unmodified CLI adapter and permission integration
internal/providers/cursor/  native ACP adapter
internal/providers/fake/    deterministic faults and scripted outputs
internal/forge/             PR/review contract, synchronization, attribution, policy
internal/forge/github/      first forge adapter and real test-repository contract
internal/bridge/            scoped yip MCP tools
internal/httpapi/           API, SSE, artifact delivery, embedded web assets
protocol/                   versioned JSON schemas, OpenAPI, generated types
web/                        Svelte + TypeScript UI and design tokens
test/fixtures/              sample repositories and scenario data
test/integration/           hub/runner/provider contract and recovery tests
test/e2e/                   browser journeys
packaging/                  native services, release manifests, container profile
docs/                       operator guide, compatibility matrix, decisions
```

Use Go modules and a frontend lockfile. Pin tools and provider versions tested for release. A package manager is an implementation convenience, not an architectural dependency. Node is needed to build the UI, not to run the hub unless a selected provider itself requires it.

Use normal SQL with explicit migrations. Generate client types from versioned schemas. Never accept unvalidated model JSON directly as a DB mutation. Avoid an extensible plugin framework before the three built-in adapters work.

## 5. Implementation slices and exit conditions

### Slice A · Durable workspace

Implement owner authentication, organisation, engineers, rooms, memberships, threads, message revisions, project links, search, and the event outbox. Build the actual visual shell with the supplied design direction. Include idempotent sends, drafts, and reconnect replay.

Exit: refresh/restart preserves history; duplicate delivery does not duplicate a message; access tests pass; rooms and profiles work with a keyboard; the same engineer has one identity across rooms. The UI may use fixture data only in an explicit demo mode.

### Slice B · One job, one real remote result

Implement machine enrollment, a runner service, its journal, immutable run destinations, job/run states, workspace creation, and the Codex adapter. Add a fake provider that can pause, duplicate, fail, and disconnect at known points. Build the work strip, progress summary, activity details, and evidence drawer against real events.

Exit: a message creates an actual job on another machine, produces a diff and test result, and survives client disconnect. Hub restart between dispatch and acknowledgement does not produce a duplicate active run. If the real provider is unavailable, this slice remains incomplete rather than being represented by a fake demo.

### Slice C · Provider choice without capability fiction

Implement Cursor ACP and the Claude Code CLI adapter. Probe installed capabilities; add the provider setup flow, model selection, account/profile labels, permission and question bridges, cancellation, and session-recovery behaviour. Keep API-backed Claude SDK support optional and separately labelled.

For Claude Code, use its documented programmatic interface and supported permission-prompt tool or another documented bridge on the pinned version. Verify the entire approval round trip. Do not assume plain print mode can magically pause for custom UI approval. For details, see [programmatic Claude Code](https://code.claude.com/docs/en/headless) and the [CLI reference](https://code.claude.com/docs/en/cli-reference).

Exit: every advertised provider passes the real adapter contract suite, with any unsupported feature explicitly disabled. No blanket `--dangerously-skip-permissions`, unrestricted auto-approval, extracted OAuth token, or undocumented backend call is used to make a test pass.

### Slice D · A team that coordinates

Implement recipient routing, steward/quiet room modes, job creation through tools, help/review/delegation, waiting without holding execution capacity, dependency validation, and wakeup limits. Add the structured work ledger and factual overview. Implement autonomous decision and completion policy, ordinary room questions with correlation IDs, and step-level waiting that preserves independent progress.

Implement review rounds, findings, author responses, revision supersession, reviewer selection and queues, and the GitHub connector. Keep external publication within the existing grant and validate real remote actor eligibility.

Exit: a builder initiates a review, fixes a reviewer-found defect, receives approval on the revised work, and finishes without human coordination. A linked PR retains truthful forge review/check/merge state; sharing an author credential cannot fabricate an independent GitHub approval. Reviews return to the correct thread and do not wake unrelated engineers. A second top-level message during the first run gets its own reply destination. A cycle or empty acknowledgement chain cannot create infinite work.

### Slice E · Continuity across rooms and projects

Implement context manifests, permitted source retrieval, accepted decisions, corrections/supersession, source links, scope-aware caching, and catch-up. Add local and global status views. Exercise all confidentiality paths, including search, attachment links, provider resume, and memories derived from several sources.

Exit: approved knowledge helps a later task without re-pasting; private information never enters a wider-scoped provider session. Catch-up is traceable to persisted events and distinguishes stale facts from current ones.

### Slice F · Multiple machines and interruptions

Implement eligibility, account concurrency, fair scheduling, leases/fencing, runner disconnection, cancellation of process groups, quota waits, profile readiness, and checkpoint-based recovery. Add native and container execution profiles and Machines diagnostics.

Exit: two independent jobs run across two machines; no shared mutable worktree; laptop closure changes nothing remotely; worker loss creates truthful uncertainty and no blind duplicate side effect. Retrying on another eligible machine requires a verified checkpoint or a new explicit attempt.

### Slice G · Finish the experience and package it

Complete onboarding, conversational questions, search, failure copy, responsive layouts, dark mode, keyboard/focus behaviour, diagnostics, backup/restore, and native service packaging. Inspect actual screens rather than relying on DOM assertions alone. Validate the scenario in section 3 with the real deployment profile.

Exit: the acceptance matrix passes, artifacts are retained, the compatibility record is complete, the prototype has been replaced with production state, and the owner can operate the system without keeping a development terminal open.

## 6. Canonical interfaces

The following are **proposed yip contracts**, not existing vendor APIs. Exact JSON field names may change before schema v1 is frozen; their semantics must remain.

### HTTP surface

| Method / path | Purpose |
|---|---|
| `POST /v1/session`, `DELETE /v1/session` | Owner sign-in/sign-out |
| `GET /v1/bootstrap` | Current user, org, permitted navigation, replay cursor |
| `POST /v1/rooms`, `PATCH /v1/rooms/{id}` | Room configuration |
| `PUT /v1/rooms/{id}/members/{engineerId}` | Explicit membership change after access validation |
| `POST /v1/rooms/{id}/messages` | Message body, structured mentions, thread/project refs, client idempotency key |
| `GET /v1/rooms/{id}/messages?before=...` | Cursor history |
| `POST /v1/engineers`, `PATCH /v1/engineers/{id}` | Engineer creation/configuration version |
| `POST /v1/projects`, `PUT /v1/projects/{id}/repos/{repoId}` | Project/repository registration |
| `GET /v1/jobs`, `GET /v1/jobs/{id}` | Factual work ledger/detail |
| `POST /v1/jobs/{id}/input` | Follow-up associated with a selected job/question |
| `POST /v1/jobs/{id}/cancel` | Idempotent cancel of job and specified children |
| `POST /v1/jobs/{id}/retry` | New attempt after recovery checks |
| `POST /v1/jobs/{id}/reviews` | Request a named peer review of an immutable artifact or PR base/head revision within inherited access |
| `POST /v1/reviews/{id}/findings` | Reviewer findings with evidence and optional revision-bound file/line references |
| `POST /v1/reviews/{id}/verdict` | Reviewer-only decision with expected round/revision; validate evidence and target freshness |
| `POST /v1/reviews/{id}/revisions` | Author submits updated work and requests another round; preserves old feedback |
| `POST /v1/pull-requests/link` | Validate forge/repository access, resolve canonical PR identity and current revision, and link to the source job |
| `POST /v1/jobs/{id}/accept` | Accept a specific revision only when configured human review is required; ordinary jobs complete through validated evidence/checks |
| `POST /v1/approvals/{id}/decision` | Exact-action decision with expected version |
| `POST /v1/questions/{id}/answer` | Persist a normal attributed reply in the question’s source room/thread, linked to its ID; ordinary message creation with reply linkage uses the same resolution path |
| `POST /v1/nodes/enrollments`, `POST /v1/nodes/pair` | Issue and redeem one-time enrollment |
| `POST /v1/nodes/{id}/drain`, `DELETE /v1/nodes/{id}/credential` | Drain or revoke a machine |
| `GET /v1/search?q=...` | Scoped text search |
| `GET /v1/artifacts/{id}` | Authorized artifact access |
| `GET /v1/events` | SSE with event sequence / Last-Event-ID |

Use consistent structured errors: code, user-readable explanation, recoverability, correlation ID, and specific missing capability if relevant. POSTs with external or persistent effect accept idempotency keys. Use optimistic version checks for approvals, acceptance, and configuration. Return 409 for a stale version; never accept a stale click optimistically.

### Event envelope

```json
{
  "schemaVersion": 1,
  "eventId": "evt-example",
  "orgId": "org-example",
  "sequence": 314,
  "type": "run.waiting_for_review",
  "actor": { "kind": "engineer", "id": "mira" },
  "causeId": "msg-example",
  "rootRequestId": "msg-example",
  "roomId": "security",
  "threadId": "thread-example",
  "jobId": "job-example",
  "runId": "run-example",
  "occurredAt": "2026-09-25T01:42:00Z",
  "payload": { "reviewerId": "oren", "artifactId": "artifact-example" }
}
```

Example identifiers are illustrative, not valid UUIDv7 test values. The server stamps identity and destination from authenticated context. Worker clocks may inform diagnostics, but hub sequence controls order. Sensitive events only reach entitled subscribers.

Persist committed messages, job transitions, approvals, artifact records, and tool-action outcomes. Transient typing and token deltas may be coalesced and need not be permanent audit entries; periodically checkpoint visible text and persist the final message. A reconnect uses canonical snapshots plus replay, not assumptions about every token arriving.

If a replay cursor has expired, return a reset instruction and a snapshot cursor. Do not silently return incomplete history. Slow consumers are disconnected with a recoverable reason; bounded queues prevent them from blocking other clients.

### Runner envelope and acknowledgement

Commands: `offer_run`, `start_run`, `deliver_input`, `resolve_approval`, `cancel_run`, `drain`, `revoke`. Events: `hello`, `capabilities`, `heartbeat`, `run_ack`, `run_event`, `artifact_ready`, `run_terminal`.

Every run command carries `runId`, `leaseEpoch`, `commandId`, deadline/lease interval, and the pinned execution manifest. Runner acknowledges only after journaling acceptance. Repeated command IDs return the original acknowledgement. Runner event IDs are unique within a persisted producer identity; hub acknowledges only after commit. On reconnect, replay unacknowledged events and reconcile active leases before launching new processes.

Artifact transfer uses declared hash and size, bounded chunks, checksum verification, and atomic finalization. Do not mark an artifact available until the hub has verified it. Downloads check current access, even if a message used to contain a valid link.

### Agent-facing tools

| Tool | Semantics |
|---|---|
| `room.read` | Bounded permitted conversation slice, with source IDs |
| `room.post` | Publish a useful message to the run's permitted destination |
| `work.create` | Create a job or child job within inherited scope |
| `work.update` | Propose a valid state/progress update; hub enforces transition and completes when evidence plus configured checks/reviews satisfy policy, without default human acceptance |
| `work.request_help` | Named recipient, question, expected output, evidence references |
| `work.request_review` | Author chooses a distinct permitted colleague; persist target artifact or PR base/head, criteria, evidence, source thread, and review round |
| `work.review` | Reviewer posts findings and approved/changes_requested/comments_only/unable_to_review verdict for the exact revision; hub validates reviewer identity and freshness |
| `work.respond_to_review` | Author addresses a finding with a reply/evidence or submits a revised artifact for re-review; cannot dismiss another reviewer’s blocking finding |
| `forge.read_pr` | Read current permitted PR identity, revisions, diff, checks, and review state |
| `forge.publish_review` | Publish a revision-bound review only through an eligible authorized remote actor; record engineer attribution, external ID, and reconciliation outcome |
| `work.respond` | Resolve a specific pending request and optionally cite artifacts |
| `work.wait` | Persist dependency, yield inference slot, resume on an authorized event |
| `work.status` | Read structured permitted ledger without starting other agents |
| `human.ask` | Publish one ordinary question in the source room/thread with a permitted mention, missing fact, context checked, and dependent step; preserve independent work and correlate the eventual reply |
| `artifact.publish` | Register an output after verified transfer |
| `decision.propose` | Suggest a scoped, sourced decision/memory |
| `knowledge.search` | Retrieve only permitted indexed material |

The bridge injects caller, run, policy, and scope. These are not model-writable parameters. Reject malformed IDs, wrong-thread replies, cross-org references, arbitrary local paths, excessive payloads, and attempts to widen access. Scope all paths to runner-resolved workspace roots; defend against traversal and symlink escape.

## 7. Initial operational defaults

| Setting | Initial behaviour |
|---|---|
| Active run per engineer | 1, with an explicit queue |
| Active organisation runs | Up to 4, constrained by machine and provider-account limits |
| Active runs per shared provider account | 1 until configured otherwise |
| Delegation depth / automatic wakeups | 3 levels / 12 wakeups per root request; stop further delegation at the limit, continue bounded independent work, and ask in-room only for a genuine unresolved dependency |
| Retry | At most 2 automatic retries for known safe transient failures; backoff with jitter; honor provider retry data |
| Machine heartbeat / lease | 10-second heartbeat; suspect after 3 misses; 60-second lease with 10-second stop margin |
| Routine engineering decisions | Engineer decides from instructions, existing behaviour, evidence, and colleagues; no owner confirmation within granted scope |
| Peer review | One suitable independent engineer for code changes/PRs by default; project policy may refine this. Other work can request review when useful. No review needed for every chat reply. |
| Review rounds | Up to 3 substantive rounds, also bounded by the root wakeup budget; summarize genuine unresolved disagreement rather than looping |
| Human acceptance | Disabled by default; enabled only by explicit task/project requirements |
| Genuine questions | Normal source-room/thread message and mention; only dependent work waits, with no automatic nags |
| Approval | Single-use, action-digest-bound, expires after 15 minutes by default; no automatic renewal |
| Context | Bounded manifest; preserve instructions and user constraints; source links for omitted evidence |
| Billing fallback | Disabled until a user explicitly configures it |
| Automatic external publication | Disabled by default; explicit project/action policy can permit it |
| Result retention | Keep unreviewed/dirty work; show storage pressure; cleanup only through policy or explicit selection |
| Network exposure | Loopback by default; remote HTTPS only through an explicit setup step |
| Idle engineers | No polling model calls; event-driven wakeups |

These are tunable runtime parameters. They are not effort estimates, assurances about vendor quotas, or claims about tested performance.

## 8. Acceptance matrix

| ID | Given / action | Required observation |
|---|---|---|
| A01 | Same engineer added to two rooms | One ID/config history; separate permitted conversation sessions; profile links agree. |
| A02 | One room discusses Atlas and Beacon | Each job has explicit project/repo scope; no silent directory switching. |
| A03 | Two clients retry the same send | One canonical message and one causal dispatch. |
| A04 | New top-level question while old thread runs | Each answer returns to its immutable triggering destination. |
| A05 | Builder asks reviewer, shared account has one slot | Builder yields; reviewer runs; dependency resolves without deadlock. |
| A06 | Agent posts a finding or “thanks” | Unrelated engineers stay idle; no new recursive task. |
| A07 | Delegation cycle or budget exhausted | Hub rejects/halts further delegation and explains it; existing evidence stays accessible. |
| A08 | User changes a running task's constraint | UI reports actual delivery mode; eventual result accounts for the constraint. |
| A09 | User cancels a job tree | Active processes receive stop; children stop/leave queue; no hidden worker keeps tools running. Unconfirmed termination is shown as unknown. |
| A10 | Browser closes, laptop sleeps | Remote execution continues; reopening recovers state and output. |
| A11 | Hub crashes after DB commit and before send | Outbox replays exactly one effective offer; no missing job. |
| A12 | Runner receives duplicate offer after crash | Journal prevents duplicate execution for the same attempt. |
| A13 | Network partition during editing | Lease fails safely; stale epoch cannot publish current results; no automatic second writer. |
| A14 | Network partition during remote push | Unknown outcome is reconciled, never blindly retried. |
| A15 | Provider allowance expires | Work waits without busy-looping or automatic paid fallback; unaffected profiles continue. |
| A16 | Approval is stale or wrong revision | Rejected; no tool action executes using old authorization. |
| A17 | Private canary added to a room | Absent from broader context, search, summaries, caches, artifact links, and resumed sessions. |
| A18 | Access is revoked mid-job | Further tools/publication denied; session and context caches invalidated; in-flight uncertainty reported. |
| A19 | Two jobs edit the same repository | Separate worktrees/branches; fixed-revision review; serial integration. |
| A20 | Agent reports “done” without expected artifact/check | Remains incomplete with missing evidence identified; does not create a generic human-approval task. |
| A21 | Overview asks for status | Reflects ledger and last-confirmed timestamps; no invented progress or waking every worker. |
| A22 | Correct an accepted decision | New version supersedes old; next permitted run uses corrected fact and provenance. |
| A23 | Machine lacks provider, OS toolchain, or account | Scheduling explains incompatibility; never launches against an accidental profile. |
| A24 | Dirty workspace cleanup requested | Explicit selection and clear consequence; no automatic deletion of unreviewed changes. |
| A25 | Backup restored to separate directory | Messages, jobs, decisions, and artifact hashes match; credentials are reconnected as needed. |
| A26 | Full keyboard flow and 200% zoom | Composer, mentions, drawer, approvals, search, and navigation remain usable. |
| A27 | 390px mobile / 1024px / 1440px layouts | No clipped controls or unreadable columns; touch targets and safe areas work. |
| A28 | Each named real provider | Sign-in path, message, tool action, denied action, approval, cancellation, resume/recovery, and quota-error handling recorded. |
| A29 | Malicious fetched text contains mentions/tool instructions | It remains untrusted content; cannot gain authority, choose author, or cause automatic dispatch. |
| A30 | Runner reboot under intended service account | Either recovers as documented or reports the actual unlock/login dependency; no false “always-on” claim. |
| A31 | Unapproved repository hook or MCP configuration is present | Launch policy excludes it; only explicitly approved startup configuration runs. No trust-dialog bypass through non-interactive mode. |
| A32 | Routine expiry decision with clear project contract | Engineer preserves the contract, gets peer review, and completes with evidence while the owner is absent; no human question or Accept click. |
| A33 | Worker repository genuinely missing | One normal question in the source group chat after checking available context; dependent step waits, gateway investigation continues; no attention inbox or periodic nag. |
| A34 | Normal reply, duplicate reply, late reply after cancellation | Correct correlated live dependency resumes once; repeated delivery is harmless and cancelled work never restarts. |
| A35 | Existing scoped grant versus action outside scope | Granted actions proceed; exceptional exact-action request stays inline and cannot be self-approved by an engineer or vague text matching. |
| A36 | Explicit human-review policy | Job remains review_ready until authorized acceptance of the exact result revision; autonomous defaults do not erase this requirement. |
| A37 | User asks only for a fix | Author independently chooses a suitable permitted peer and requests review; owner never acts as dispatcher. |
| A38 | Seeded refresh-path defect in first patch | Reviewer identifies it with file/test evidence, requests changes, and approves only after the author fixes it and re-review verifies the new revision. |
| A39 | New commits during/after review | Old approval remains bound to the old revision, cannot satisfy current completion/merge policy, and a new round is scheduled once. |
| A40 | Several engineers share the PR author’s GitHub credential | Internal peer review is recorded, but no fabricated independent remote approval or branch-protection satisfaction is shown. |
| A41 | Chat mention plus structured review request, replayed webhook, or ambiguous publish timeout | One logical request and no duplicate review dispatch or remote comment; reconcile external outcome before retry. |
| A42 | Reviewer lacks access, is busy, or exhausted review budget | Queue or select a permitted qualified peer; no silent grant expansion, deadlock, rubber-stamp approval, or repetitive human escalation. |
| A43 | Document/patch review without a PR | Same request, substantive feedback, revision, and approval flow on immutable artifacts; no forge dependency. |
| A44 | Review passes but PR checks/merge policy do not | UI separates peer approval, remote checks, and merge status; no merge is claimed or performed outside authorization. |

## 9. Test strategy and quality targets

Use unit tests for state transitions, permission intersection, scope fingerprints, dependency cycles, and deduplication. Use integration tests with a real temporary SQLite database and the fake provider for crashes, retries, and event replay. Use controlled two-process/two-machine tests for leases and supervision. Use the real CLIs for provider contracts; record versions and distinguish deterministic automation from manually observed behaviour.

Use browser end-to-end tests for the demonstration scenario, reconnect, stale approvals, identity across rooms, and keyboard/focus. Visually inspect the principal screens in day/night at the specified widths. Avoid screenshot-only confidence: a green mock response is not a real remote job.

Initial performance targets, to measure and revise with evidence: ordinary local interactions should feel immediate; local optimistic message rendering under 100ms; ordinary hub reads p95 under 200ms on the nominated mini with the release fixture; an acknowledged event should reach a connected LAN client without perceptible batching delays. Provider latency is measured separately. Do not fabricate benchmarks or use fabricated percent-complete displays.

Provide a fixture containing enough history to exercise pagination and search, for example 10,000 messages across ten rooms, 100 jobs, and several artifact sizes. It is a test workload, not an advertised capacity limit.

The deeper experience tests from the design document require use with realistic multi-project work. Record where the owner still had to copy context, relay messages, hunt for results, or repeat an instruction. Those are product defects even if the individual endpoints pass.

## 10. Release evidence and operational handoff

The implementation must include:

- A compatibility table with provider binary version, OS/architecture, authentication route, supported capabilities, observed failures, and test date.
- A runnable fake-provider demonstration and the steps for the real-provider scenario.
- A migration/backup/restore guide and a tested service-installation guide for macOS and Linux.
- Clear indication of where conversations, code, model requests, and secrets live.
- Export of messages, jobs, decisions, and artifacts in documented formats; no dependence on a hosted yip service.
- A short architecture decision record for any departure from these documents, including consequences for acceptance criteria.
- A release checklist proving A01–A44, with incomplete checks explicitly marked incomplete.

Do not declare the MVP complete while the real provider path, two-machine execution, context isolation, or approval enforcement remains a placeholder. Once these gates are met, additional integrations and enterprise features can be considered based on observed use rather than feature-count competition.
