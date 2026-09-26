# yip browser API (v1)

The hub serves a JSON API under `/v1`, a server-sent event stream, and the
embedded web client. Types are generated from `protocol/*.go`:
JSON Schemas in `protocol/schema/api.v1.json`, TypeScript in
`web/src/lib/api/types.gen.ts` (`yip schema` regenerates both).

## Authentication and safety

- Owner sign-in sets an `HttpOnly`, `SameSite=Strict` session cookie
  (`yip_session`; `Secure` when served over HTTPS).
- `GET /v1/bootstrap` returns `csrfToken`. Every state-changing request must
  send it as `X-Yip-Csrf` and must carry an `Origin` header matching the hub
  (or a configured `--allowed-origin`).
- Errors are always `APIError`: `{code, message, recoverable, correlationId,
  missingCapability?, details?}`. `message` is user-readable. Codes:
  `unauthorized` 401, `forbidden` 403, `not_found` 404, `invalid` 400,
  `conflict` 409 (stale version — never retry optimistically),
  `incomplete` 422, `limit_reached` 429, `unavailable` 503, `internal` 500.
- Sign-in is limited per client address (30 attempts per 10 minutes across
  all handles) and per client and handle (8 per 10 minutes); both answer
  `limit_reached`. Handles over 64 characters and passwords over 1,024 are
  refused before any hashing.
- Sends are idempotent: `POST /v1/rooms/{id}/messages` takes `clientKey`
  (or `Idempotency-Key`). A retry returns the original message with
  `duplicate: true` (HTTP 200 instead of 201).

## Endpoints

| Method / path | Body → response |
|---|---|
| `GET /v1/setup` | → `SetupStatus` (no auth) |
| `POST /v1/setup` | `SetupRequest` (needs the one-time setup code printed on the hub host) → sets cookie |
| `POST /v1/session` / `DELETE /v1/session` | `SignInRequest` → sets cookie / sign out |
| `GET /v1/bootstrap` | → `Bootstrap` (user, org, rooms with unread/mention counts, engineers, projects, nodes, provider summary, `cursor`, `csrfToken`, preferences, `demo`) |
| `PUT /v1/preferences` | `PreferencesRequest` → `Preferences` |
| `GET/POST /v1/rooms` | → `Room[]` / `CreateRoomRequest` → `Room` |
| `GET/PATCH /v1/rooms/{id}` | → `Room` / `UpdateRoomRequest` (with `version`) → `Room` |
| `GET /v1/rooms/{id}/members/{engineerId}/preview` | → `MembershipPreview` (show before adding) |
| `PUT/DELETE /v1/rooms/{id}/members/{engineerId}` | → `Room` |
| `GET /v1/rooms/{id}/messages?before=SEQ&limit=N` | → `MessagePage` (top-level messages, oldest first; `hasMore`) |
| `POST /v1/rooms/{id}/messages` | `PostMessageRequest` → `PostMessageResponse` (`dispatched` engineer IDs, `input` receipt when attached to a job, `resolvedQuestionIds`) |
| `POST /v1/rooms/{id}/read` | `{seq}` |
| `GET /v1/rooms/{id}/work[?include=replies]` | → `WorkRow[]` (work strip; `include=replies` adds queued/running conversational replies; `runState` is the latest attempt's state) |
| `GET /v1/runs` | → `Run[]` attempts queued or executing in your rooms (for "working" indicators) |
| `GET /v1/questions/{id}`, `GET /v1/decisions/{id}` | → `Question` / `Decision` |
| `POST /v1/overview/seen` | records the visit used by "Since you were here" |
| `GET /v1/threads/{rootMessageId}` | → `MessagePage` (root first, then replies) |
| `POST /v1/messages/{id}/reactions` | `ReactRequest` → `Message` |
| `PATCH/DELETE /v1/messages/{id}` | `{body}` → `Message` / redact own message |
| `GET/POST /v1/engineers`, `GET/PATCH /v1/engineers/{id}` | `GET {id}` → `{engineer, versions}`. `provider` carries `model`, an optional `profileId` pin (only that account runs this engineer's work) and `allowApiBilling` (off: never run on an API-key-billed install) |
| `GET/POST /v1/projects`, `GET/PATCH /v1/projects/{id}` | |
| `PUT /v1/projects/{id}/repos/{repoId\|new}` | `PutRepoRequest` → `Project` |
| `PUT /v1/projects/{id}/grants/{engineerId}` | `PutGrantRequest` (`access`: read/write/none; `actions`: push, open_pr, publish_review, merge) |
| `GET /v1/jobs?state=a,b&project=&owner=` | → `Job[]` (work ledger; excludes conversational replies) |
| `GET /v1/jobs/{id}` | → `JobDetail` (runs, checks, artifacts, reviews with rounds/findings, questions, approvals, PRs, children, decisions, activity, inputs, `missing` evidence, `revisions` with file/line counts) |
| `GET /v1/jobs/{id}/runs/{runId}/activity` | → `RunActivity[]` (tool log) |
| `POST /v1/jobs/{id}/input` | `JobInputRequest` → `JobInputResponse` (steering; `input.delivery` is `pending` → later `immediate` or `queued`) |
| `POST /v1/jobs/{id}/cancel` | `CancelJobRequest` → `Job` (stops the whole job tree) |
| `POST /v1/jobs/{id}/retry` | `RetryJobRequest` → `Job` (explicit new attempt) |
| `POST /v1/jobs/{id}/accept` | `AcceptJobRequest` (exact `revision` + `version`) — only when human review is required |
| `GET /v1/reviews/{id}` | → `Review` |
| `POST /v1/pull-requests/link`, `GET /v1/pull-requests/{id}?refresh=1` | → `PullRequest` |
| `GET /v1/approvals/{id}`, `POST /v1/approvals/{id}/decision` | `ApprovalDecisionRequest` (`approve`/`reject` + `version`) |
| `POST /v1/questions/{id}/answer` | `AnswerQuestionRequest` → `PostMessageResponse` (a normal reply in the question's thread) |
| `GET /v1/nodes`, `POST /v1/nodes/enrollments` | → `Node[]` / `Enrollment` (token + pairing `command`, shown once) |
| `POST /v1/nodes/{id}/drain` (`{drain}`), `POST /v1/nodes/{id}/stop`, `DELETE /v1/nodes/{id}/credential` | drain / stop its work / revoke |
| `POST /v1/nodes/{id}/probe` | ask a connected machine to re-check its providers now (e.g. after `codex login`); the result arrives as `node.updated` |
| `GET /v1/provider-profiles`, `PUT /v1/provider-profiles/{id}` | → `ProviderProfile[]` / `ProviderProfileRequest` (`maxConcurrency` 1–16; default 1 — runs on one account share its allowance). `pausedUntil` is set while the account's allowance is exhausted; its queued work waits until then |
| `GET /v1/decisions?status=`, `POST /v1/decisions`, `POST /v1/decisions/{id}` | `DecisionRequest` / `DecisionActionRequest` |
| `GET /v1/overview?seen=1` | → `Overview` (catch-up since last visit, work rows, decisions, open questions, `roomId` of the personal Overview conversation). `seen=1` records the visit. |
| `GET /v1/search?q=&room=&project=` | → `SearchResult[]` (kinds: room, engineer, project, job, message, decision). `project` narrows to rooms linked to it, its work and its decisions, before ranking. A work ID (whole, `#`-prefixed, or six or more characters from either end; the UI shows the last six) finds that work directly |
| `GET /v1/artifacts/{id}[?download=1]` | artifact bytes; text types render as plain text, never HTML |
| `GET /v1/diagnostics` | → `Diagnostics` |
| `GET /v1/export` | zip of rooms, messages, jobs, decisions, artifacts |
| `GET /v1/events` | SSE (below) |

## Event stream

`GET /v1/events` with `Last-Event-ID` (or `?cursor=`) replays committed events
after the cursor, then streams live. Each committed event is:

```
id: <sequence>
event: <type>
data: <Event JSON: {schemaVersion, eventId, sequence, type, actor, causeId,
       rootRequestId, roomId, threadId, jobId, runId, occurredAt, payload}>
```

Control events: `ready` `{cursor}` on connect; `reset` `{cursor, reason}` when
the cursor is too old or unknown (refetch `/v1/bootstrap` and current views);
`slow` when the client fell behind (reconnect with the last id). The stream
re-checks its session every 20 seconds and closes once the session is signed
out or expired; the reconnect is then refused with 401.

Committed event types and their `payload`:

| Type | Payload |
|---|---|
| `message.created`, `message.updated` | `Message` (updated covers reactions, thread counts, refs, edits, redaction) |
| `room.created`, `room.updated` | `Room` |
| `room.member_added`, `room.member_removed` | `{room, engineerId}` |
| `read.updated` | `{roomId, seq}` |
| `engineer.created`, `engineer.updated` | `Engineer` |
| `project.created`, `project.updated` | `Project` |
| `job.created`, `job.updated` | `Job` |
| `run.created`, `run.updated` | `Run` |
| `input.updated` | `JobInput` (delivery receipt) |
| `check.recorded` | `Check` |
| `revision.published` | `RevisionRecord` |
| `artifact.published` | `Artifact` |
| `review.updated` | `Review` |
| `question.created`, `question.updated` | `Question` |
| `approval.created`, `approval.updated` | `Approval` |
| `permission.auto` | `{action, decision, reason}` (policy applied an existing grant) |
| `decision.created`, `decision.updated` | `Decision` |
| `node.updated` | `Node` |
| `pr.updated` | `PullRequest` |
| `run.stale_report` | diagnostic only |

Transient (never persisted, no id): `event: transient`, data
`{type: "run.stream", roomId, threadId, jobId, runId, engineerId, payload: {kind, text, at}}` —
incremental live text from a running engineer (append each `message_delta`
chunk). The web client doesn't render the streamed text: it shows a group
chat's "Mira is typing…" line from the reply's run state, and the canonical
message arrives as `message.created`.

## Message anatomy

- `author.kind`: `user`, `engineer`, or `system` (`system` messages are hub
  status lines: a lost machine, an allowance wait, an exceptional permission request).
- `kind`: `text`, `question` (an engineer asking the owner; `refs` holds the
  `question`), `approval` (`refs` holds the `approval`: render the exact action
  with Allow / Reject), `result` (work finished — render a result card from
  the `job` ref), `review` (a review request/verdict — `refs` holds `review`),
  `status` (hub facts).
- `refs`: links to `job`, `review`, `approval`, `question`, `pr`, `artifact`,
  `decision`. Fetch details on demand.
- `mentions`: structured `{kind, id}`; the body text contains `@handle`.
- `thread`: reply summary on a root message; open the thread with
  `/v1/threads/{id}`.
