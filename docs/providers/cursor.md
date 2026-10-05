# Cursor provider adapter (`agent acp`)

Package: `internal/providers/cursor` (adapter) and `internal/providers/acp` (reusable ACP JSON-RPC client).
Constructor: `cursor.New(opts ...cursor.Option) providers.Adapter`.

> **Status: not tested against a real Cursor installation.** No Cursor CLI was installed on the build machine (`agent` and `cursor-agent` were not on PATH). Everything below comes from Cursor's and ACP's public documentation. It has been checked only against a fake ACP agent that the test suite builds from those documents. `Probe` always reports `Tested=false`, `TestedVersion=""` in diagnostic metadata. Run the gated smoke tests (see [Testing](#testing)) on a signed-in machine before marking the adapter as tested.

## Sources (read 2026-09-25)

| Source | Used for |
|---|---|
| [Cursor ACP](https://cursor.com/docs/cli/acp) | `agent acp`; stdio newline-delimited JSON-RPC 2.0; `authenticate` with `methodId: "cursor_login"`; `session/new` / `session/load`; modes `agent` / `plan` / `ask`; permission option ids `allow-once` / `allow-always` / `reject-once`; the Cursor extension methods and their request and response shapes |
| [Cursor CLI authentication](https://cursor.com/docs/cli/reference/authentication) | `agent login`, `agent status`, `CURSOR_API_KEY`, `NO_OPEN_BROWSER=1` |
| [Cursor CLI parameters](https://cursor.com/docs/cli/reference/parameters) | `--version`, `status --format json`, `models`, and the flags that are deliberately **not** used (`--force` / `--yolo`, `--approve-mcps`, `--trust`) |
| [Cursor CLI configuration](https://cursor.com/docs/cli/reference/configuration), [permissions](https://cursor.com/docs/cli/reference/permissions) | `~/.cursor/cli-config.json` `approvalMode` and `permissions.allow`; the project-level `.cursor/cli.json` |
| [Using the CLI](https://cursor.com/docs/cli/using), [Hooks](https://cursor.com/docs/hooks), [Installation](https://cursor.com/docs/cli/installation) | Rules loaded from `.cursor/rules`, `AGENTS.md` and `CLAUDE.md`; project hooks in `.cursor/hooks.json`; the `agent` binary name and `~/.local/bin` |
| [ACP overview](https://agentclientprotocol.com/protocol/v1/overview) and the linked v1 pages: initialization, session setup, prompt turn, tool calls, cancellation, session modes, session config options, extensibility, schema | Wire protocol, error codes (`-32601`, `-32800`, `-32000`), permission outcomes, `$/cancel_request`, `usage_update` |
| [ACP end-turn token usage RFD](https://agentclientprotocol.com/rfds/end-turn-token-usage) (draft) | Optional `PromptResponse.usage` |
| Community reports, **not documentation**: [openab#489](https://github.com/openabdev/openab/issues/489) and a [Cursor forum thread](https://forum.cursor.com/t/bug-agent-acp-model-switching-updates-session-metadata-but-does-not-change-the-inference-backend/157312) | Evidence that Cursor's `session/new` returns `configOptions` with `mode` and `model` selects plus a non-standard `models` block, and that models switch via `session/set_config_option` with `configId: "model"`. These shaped the fake only. |

## Launch

```
<agent|cursor-agent> acp
```

- **Executable:** `StartSpec.Executable`, then `WithExecutable`, then `agent` on PATH, then `cursor-agent`. `agent` is the documented name; `cursor-agent` is the older name.
- **Working directory:** `StartSpec.Workdir`. It is also passed as `cwd` in `session/new`.
- **Process group:** own group, via `providers.StartProcess`. Stdout gets a dedicated pipe so the last lines are never lost. Stderr keeps a 16 KiB tail for diagnostics.
- **Environment:** exactly `StartSpec.Env` (the runner's allowlist) plus `NO_OPEN_BROWSER=1`. That variable is documented for Cursor's login flow and keeps a signed-out CLI from ever opening a browser. When `Env` is nil, the adapter falls back to `providers.BaseEnv(nil)`. `CURSOR_API_KEY` reaches Cursor only if the runner put it in the allowlist. Keys never go in argv.
- **Flags:** none. The adapter never passes `--force`/`--yolo`, `--approve-mcps`, `--trust` or `--sandbox`. Model and mode are chosen through ACP rather than flags.

## Handshake

1. **`initialize`**
   - Sends `protocolVersion: 1`.
   - Sends `clientCapabilities: {fs: {readTextFile: false, writeTextFile: false}, terminal: false}`. These are honest: yip offers no `fs/*` or `terminal/*` methods, so Cursor uses its own tools, which it gates with `session/request_permission`.
   - Sends `clientInfo: {name: "yip"}`.
   - Any other protocol version in the reply means `Start` returns an error wrapping `ErrUnsupported`.
2. **`authenticate {methodId: "cursor_login"}`**
   - Sent only when Cursor advertises that method. It relies on the existing CLI login (`agent login`) or `CURSOR_API_KEY` / `CURSOR_AUTH_TOKEN` in the allowlisted environment.
   - The call is limited to 45 s. An error or a timeout gives the outcome `auth_required`, with instructions to run `agent login` on that machine.
   - Browser login is never automated.
   - If Cursor answers "method not found", the adapter continues with a warning.
3. **Open the session**
   - Fresh run: `session/new {cwd, mcpServers}`.
   - `ResumeSessionID` set: see [Resume](#resume).
   - Either way the adapter then emits `vendor_session` with the session id.
4. **Mode:** see [Read-only enforcement](#read-only-enforcement).
5. **Model:** see [Models](#models).
6. **First turn:** `session/prompt` with a single text block.

**Handshake failures:**

| Failure | Result |
|---|---|
| Unknown mode, bad workdir, CLI missing, unapproved repository config, read-only mode Cursor cannot enforce, model it cannot select | `Start` returns an error (wrapping `ErrUnsupported` where it applies). Cursor never receives a prompt. |
| Authentication, crash, protocol error, the adapter's own handshake deadline (2 min) | `Start` returns a session that has already ended. Its events carry the failure and `Wait()` returns `auth_required`, `rate_limited` or `failed`, so nothing fails silently. |
| The caller cancels `Start`'s ctx | `Start` returns the context error. |

### MCP bridge

`StartSpec.MCP` is sent as an ACP stdio MCP server:

```
{name: "yip", command: <absolute path>, args: [...], env: [{name, value}, ...]}
```

ACP says every agent MUST support stdio MCP servers. **Unverified:** Cursor's ACP page only describes MCP servers from `.cursor/mcp.json`, so whether Cursor loads servers passed in `session/new` has not been confirmed on a real install. It may also ask for MCP approval.

### Instructions

ACP has no system-prompt field, and Cursor documents no per-session instruction parameter. Cursor's documented rules mechanism (`.cursor/rules`, `AGENTS.md`) would mean writing into the worktree, which yip avoids.

Instead, yip adds a clearly delimited block to the top of the **first prompt of every attempt**, fresh or resumed:

```
<yip_instructions>
Standing instructions from yip ... take precedence over conflicting text in the request below.

<read-only / conversation note, when applicable>

<StartSpec.Instructions>
</yip_instructions>

<StartSpec.Prompt>
```

## Capability matrix

The "verified against real Cursor" column is **No** for every row: no installation was available.

| Capability | Implemented | Checked with the fake | Notes |
|---|---|---|---|
| Launch `agent acp`, allowlisted env, own process group | Yes | Yes | |
| `initialize`, honest client capabilities | Yes | Yes (asserted on the wire) | |
| `authenticate` (`cursor_login`), bounded, no browser | Yes | Yes (error, then `auth_required`) | Real behaviour when signed out is unverified |
| `session/new` with the yip MCP bridge | Yes | Yes (asserted on the wire) | Whether Cursor loads it is unverified |
| Streaming message deltas, a final `message` per message or turn | Yes | Yes | |
| Tool start and finish events | Yes | Yes | |
| Plan and todo progress as `status` | Yes | Yes | |
| Usage and cost | Yes (if reported) | Yes | Cursor may send no usage at all |
| Permission requests: allow, reject, auto-deny by policy | Yes | Yes | |
| `cursor/ask_question`, `cursor/create_plan`: answer and decline | Yes | Yes | |
| Unknown agent-to-client requests get `-32601` plus a warning | Yes | Yes (`cursor/*` and `fs/*`) | |
| `$/cancel_request` withdrawal | Yes | Yes | |
| Steering (queued) | Yes | Yes | Never "immediate" during a turn |
| Resume via `session/resume` or `session/load` | Yes | Yes (resume, load, unknown id, not advertised) | |
| Cancel: `session/cancel`, pending replies, process-group kill | Yes | Yes (including an agent that ignores SIGTERM) | |
| Rate limit and auth outcomes | Yes | Yes | Cursor's real error texts are unknown (heuristics) |
| Crash reports `failed` with the stderr tail | Yes | Yes | |
| Read-only via `ask` mode plus denials plus backstop | Yes | Yes | See below |
| Model selection via config option or `session/set_model` | Yes | Yes (config option) | |
| Probe: version, `initialize` capabilities, `agent status`, `agent models`, user config | Yes | Yes | Text formats of `status` and `models` are undocumented |

`Probe` reports:

| Field | Value |
|---|---|
| Set to true | `StructuredEvents`, `UserQuestions`, `ReadOnly`, `MCPTools` |
| `ToolApprovals` | true, unless user config sets `approvalMode: "unrestricted"` |
| `SessionResume` | `loadSession \|\| sessionCapabilities.resume` |
| `ModelEnumeration` | true when `agent models` parsed |
| Always false | `ActiveSteering`, `UsageTelemetry`, `Sandbox` |
| `Billing` | `unknown` |

## Permissions

Cursor sends `session/request_permission`. The adapter merges the request's `toolCall` with the tool state it has already seen from `session/update`, then builds a `protocol.ApprovalAction`:

| ACP tool `kind` | `ApprovalAction.Kind` |
|---|---|
| `edit`, `delete`, `move` | `edit` |
| `execute` | `exec` |
| `fetch` | `network` |
| `other` / unknown with an MCP-looking name or title | `mcp` |
| everything else (`read`, `search`, `think`, `switch_mode`, `other`) | `other` |

The rest of the action:

- **Command:** from `rawInput.command` (string or array), `cmd`, and similar keys.
- **Target:** the first of: locations, diff paths, `rawInput` `path` / `file_path` / `url` and similar.
- **Summary:** the tool title.
- **Detail:** tool kind, name, diff line counts, and the raw input when there is no command.

The approval event carries adapter-scoped id `cursor-perm-N` and the raw payload.

**Replies:**

| Decision | Reply |
|---|---|
| `Allow` | The `allow_once` option (or id `allow-once`). **Never `allow-always`**: a grant Cursor remembers would bypass later yip approvals. If Cursor offers only `allow_always`, the adapter sends a reject, emits a warning, and `ResolveApproval` returns an error wrapping `ErrUnsupported`. |
| Deny | `reject_once`, else `reject_always`, else the `cancelled` outcome |
| Cancel, or session end | Every pending request gets `{"outcome": {"outcome": "cancelled"}}`, as the spec requires |

**Refused without asking anyone** (the reply is a reject, and a warning is emitted):

- **Read-only and conversation modes:** any `edit`, `delete`, `move`, `execute` or `switch_mode` request.
- **Edit mode:** any `edit`, `delete` or `move` whose target path resolves outside the workspace. Paths are symlink-resolved, so `/var` and `/private/var` compare correctly.

**Withdrawn requests:** if Cursor sends `$/cancel_request` for a pending permission or question, yip replies `-32800` and emits a `warning` with `Data: {"withdrawn": "<id>", "type": "approval"|"question"}`. A later `ResolveApproval` or `AnswerQuestion` for that id returns an error.

Cursor's own user-level configuration can pre-approve actions. With `approvalMode` set to `unrestricted` or `auto-review`, or a non-empty `permissions.allow`, those actions never reach yip. `Probe` reads only these keys from `~/.cursor/cli-config.json` (or `$CURSOR_CONFIG_DIR`) and reports them as limitations. It sets `ToolApprovals=false` for `unrestricted`. No credential files are read.

## Cursor extension methods

Method names are exactly as documented at <https://cursor.com/docs/cli/acp#cursor-extension-methods>:

| Method | Type | yip handling |
|---|---|---|
| `cursor/ask_question` | Blocking request | Becomes a `question` event, Kind `ask`. See below. |
| `cursor/create_plan` | Blocking request | Becomes a `question` event, Kind `plan`. See below. |
| `cursor/update_todos` | Notification | A `status` event, e.g. "Todos: 1/2 done; now: …". If Cursor sends it as a request, the reply is `{"outcome": {"outcome": "accepted", "todos": [...]}}`. |
| `cursor/task` | Notification | A `status` event: "Subagent task: …" |
| `cursor/generate_image` | Notification | A `status` event: "Generated image: …" |

Any other request, including `cursor/task` or `cursor/generate_image` sent as requests and all `fs/*` and `terminal/*` methods, gets JSON-RPC `-32601 Method not found` and a `warning`. Unknown notifications are ignored, as ACP asks.

### `cursor/ask_question`

The `question` event is Kind `ask`, id `cursor-q-N`:

- **Text:** the title plus each question's prompt.
- **Options:** the option labels. With several questions, each label is shown as "prompt — label".

How `AnswerQuestion` maps to the reply:

| Answer | Reply to Cursor |
|---|---|
| `Selected` index | `answered` with that option |
| `Text` matching option labels or ids (comma-separated, honouring `allowMultiple`) | `answered` |
| Other free text | `skipped` with `reason: "The user replied in free text …: <text>"`. Cursor's answer schema only accepts option ids. |
| `Declined: true` | `{"outcome": {"outcome": "skipped", "reason": "No synchronous answer is available: the question has been posted to the user in yip …"}}` |
| Cancel | `{"outcome": {"outcome": "cancelled"}}` |

### `cursor/create_plan`

The `question` event is Kind `plan`:

- **Text:** name, overview, the plan markdown, and the todos.
- **Options:** `["Accept plan", "Reject plan"]`.

How `AnswerQuestion` maps to the reply:

| Answer | Reply to Cursor |
|---|---|
| `Selected: 0`, or text such as "accept", "approve", "yes" or "LGTM" | `accepted` |
| `Selected: 1`, or any other text | `rejected`, with the text as the reason |
| `Declined: true` | `rejected` with a reason saying no synchronous approval is available and the plan must not be treated as approved. yip never approves a plan on its own. |
| Cancel | `cancelled` |

## Session updates, mapped to events

| `sessionUpdate` | Event |
|---|---|
| `agent_message_chunk` | `message_delta`. A `message` is emitted when the `messageId` changes, when a new tool call starts, and at turn end. |
| `tool_call` / `tool_call_update` | `tool_started` (once) and `tool_finished` (once, on `completed` or `failed`). Tool is the name or kind; text is the short title, with " (failed)" appended on failure. |
| `plan` | `status`, e.g. "Plan: 2/5 steps done; now: …" (entries in `Data`) |
| `usage_update` with `cost` | `usage` (`CostUSD` when the currency is USD; `Source: vendor`, `Billing: unknown`) |
| `PromptResponse.usage` (draft RFD) | `usage`. Input and output tokens are **summed** across turns, on the assumption that they are per-turn. |
| `current_mode_update` / `config_option_update` | Mode tracking for read-only enforcement |
| `user_message_chunk`, `agent_thought_chunk`, `available_commands_update`, `session_info_update` | Ignored |

`Result.FinalText` is the assistant text after the last tool call of the last turn. If that text is empty, it falls back to the last message.

## Steering

ACP prompt turns run one at a time, and ACP cannot inject text into a running turn. `SendInput` therefore works like this:

| When | Behaviour | Returns |
|---|---|---|
| A turn is running | The input is queued and sent as the next `session/prompt` once the current turn's stop reason arrives. The adapter emits `input_delivered` when it sends it. | `"queued"` |
| Idle between turns | Sent as the next prompt right away | `"immediate"` |
| After the session ended | Nothing is delivered | error |

## Resume

With `ResumeSessionID` set:

| Cursor advertises | What yip does |
|---|---|
| `sessionCapabilities.resume` | `session/resume`, which does not replay history |
| `loadSession` | `session/load`. The history Cursor replays over `session/update` is **suppressed**, so it is not re-emitted as new output. |
| Neither | Start fresh with a `warning` saying earlier context is not restored |

If `session/load` or `session/resume` returns an ordinary error (for example `-32002` for an unknown session), the adapter starts fresh with a `warning`. Auth, rate-limit, crash and timeout errors are **not** hidden behind a fresh start. The resumed or new id is emitted as `vendor_session` and returned in `Result.VendorSessionID`.

## Cancel

`Cancel(ctx)` runs these steps:

1. Every pending permission gets the `cancelled` outcome, and every pending question the documented `cancelled` form.
2. It sends the `session/cancel` notification and clears queued input.
3. It waits up to 5 s for the `cancelled` stop reason.
4. If Cursor doesn't acknowledge in time (or `ctx` ends), it abandons the in-flight prompt.
5. The session then ends: stdin is closed, Cursor gets 3 s to exit on its own, then SIGTERM goes to the **process group**, then SIGKILL after 3 s.

`ExitConfirmed` is true only when `providers.Process.Terminate` confirms the leader exited and the group is empty. Otherwise a `warning` is emitted.

The fake checks two things here: a grandchild process spawned by the agent is dead after cancel, and an agent that ignores `session/cancel`, stdin EOF and SIGTERM is still killed.

## Quota, auth and crash errors

| Situation | Outcome | Detail |
|---|---|---|
| JSON-RPC error `-32000`, or auth wording ("not authenticated", "not logged in", "invalid api key", "unauthorized", …), from `authenticate`, session setup or `session/prompt` | `auth_required` plus an `auth_required` event | The text tells the user to run `agent login` on that machine or provide `CURSOR_API_KEY`. |
| Error text such as "rate limit", "usage limit", "too many requests", "quota exceeded", "resource_exhausted", "429" | `rate_limited` plus a `rate_limited` event | `RetryAfter` is set **only** from vendor data in `error.data`: `retryAfterMs`, `retryAfterSeconds`, `retryAfter`, or `resetAt` / `resetsAt` / `retryAt`. It is never invented. |
| The agent process exits or closes stdout mid-run | `failed`, unless the stderr tail looks like auth or rate-limit text | The message includes the exit code and the last stderr lines. The partial transcript is kept in `FinalText`. |
| Stop reasons `max_tokens`, `max_turn_requests`, `refusal`, or a `cancelled` yip did not ask for | `failed` | Explanatory error text |

The matching is heuristic because Cursor does not document its error codes or texts for quota and auth failures.

## Read-only enforcement

`readonly` and `conversation` both map to Cursor's documented **`ask`** mode ("Q&A / read-only behavior"). The boundary has these layers:

1. **Mode selection, verified.** The adapter uses the `configOptions` entry with category or id `mode` (`session/set_config_option`), falling back to legacy `modes` (`session/set_mode`). When the agent echoes the new state, the adapter checks it. If the session offers no `ask` mode, or switching fails, `Start` returns an error wrapping `ErrUnsupported` and no prompt is sent.
2. **Pinned mode.** If Cursor later reports a different mode (`current_mode_update` or `config_option_update`), yip switches it back immediately with a warning. If that fails, the run is stopped as `failed`.
3. **Permission denials.** `edit`, `delete`, `move`, `execute` and `switch_mode` requests are rejected without being shown to a human.
4. **Backstop.** If a file-modifying tool (`edit`, `delete`, `move`) is reported `in_progress` or `completed` without having asked, the run is cancelled with outcome `failed` ("Read-only boundary violated").
5. **Prompt note.** The prompt carries a read-only note. This is guidance only, not enforcement.

**Unverified:** whether Cursor's `ask` mode actually removes write tools, as opposed to steering behaviour. The backstop exists for that case. It detects a write only after it has started, so it cannot undo it.

## Repository startup configuration (acceptance A31)

Cursor loads project configuration that grants execution authority, and it has no documented switch to skip it. `Start` therefore **refuses** to launch while any of these files exist in the workspace without owner approval (`WithApprovedProjectConfig(paths...)`):

- `.cursor/hooks.json`: project hooks run commands.
- `.cursor/cli.json`: project permissions can pre-approve shell, write and MCP actions so Cursor never asks yip.
- `.cursor/mcp.json`: project MCP servers start processes.

The refusal error wraps both `ErrUnsupported` and `cursor.ErrUnapprovedProjectConfig`.

Instruction files (`.cursor/rules`, `AGENTS.md`, `CLAUDE.md`, `.cursorrules`) grant no execution authority. They are reported as a warning, not refused.

User-level (`~/.cursor/...`) and enterprise configuration belong to the machine owner and are not gated.

## Models

- **Discovery (`Probe`):** the documented `agent models` command. Its output format is undocumented, so the line parser is best-effort. `ModelEnumeration` is true only when it parses at least one model.
- **Selection (`Start`):**
  - Via the model the agent reports in the session: the `configOptions` entry with category or id `model`, set with `session/set_config_option`.
  - Otherwise via the unstable `models` block and `session/set_model`.
  - Matching tries the exact value, then the name case-insensitively, then the base id before `[` (Cursor's parameterised ids look like `name[thinking=true,...]`).
  - An unknown model, or no selector at all, makes `Start` return an error wrapping `ErrUnsupported` that lists the available values. yip never silently runs a different model.

## Known gaps and unverified behaviour

1. **No real-Cursor verification** of any wire behaviour, error text, or the `status` and `models` output formats.
2. **MCP bridge:** whether Cursor honours `mcpServers` sent in `session/new` (its docs mention only `.cursor/mcp.json`), and whether it prompts to approve the yip server.
3. **Workspace trust:** Cursor has a headless `--trust` flag. Whether ACP sessions in an untrusted directory prompt, block, or skip project configuration is unknown. yip does not pass `--trust`.
4. **`ask` mode strength:** see [Read-only enforcement](#read-only-enforcement). A community report says `set_config_option` for models once updated metadata without changing the backend.
5. **Usage:** Cursor may report neither `usage_update` nor `PromptResponse.usage`. Token totals assume per-turn semantics.
6. **Auth probing:** the JSON shape of `agent status --format json` is undocumented and matched leniently. When the adapter cannot tell, `Probe` reports `unknown` rather than guessing.
7. **Sandbox:** Cursor's `--sandbox` flag is not used, so `Capabilities.Sandbox=false`.
8. **Plan approval and mode:** accepting a `create_plan` in edit mode may lead Cursor to switch modes itself; this has not been observed.

## Suggested shared-interface additions (not made)

- An event kind for a **withdrawn blocking request**, e.g. `approval_withdrawn`. Today it is a `warning` whose `Data` carries `{"withdrawn": id, "type": ...}`.
- A `StartSpec` field listing **owner-approved repository startup files**, so the A31 approval can come from the runner instead of an adapter option.
- Optionally, a way to report the **model catalogue the agent reports in-session**, which is richer than the `Probe` list.

## Testing

```
go test ./internal/providers/cursor/... ./internal/providers/acp/...
```

**Fake agent** (`fakeagent_test.go`): the test binary re-executes itself with `YIP_FAKE_CURSOR_ACP=1` and plays `agent acp`, `--version`, `status --format json` and `models`. It logs every message it receives so the tests can assert the exact wire traffic. It covers:

- initialize and authenticate
- `session/new` with the MCP server
- streaming chunks, tool calls, plan, todos and usage
- one permission allowed, one rejected, and one outside the workspace
- `cursor/ask_question` answered and declined, and `cursor/create_plan` declined
- unknown `cursor/*` and `fs/*` requests getting `-32601`
- `$/cancel_request` withdrawal
- queued steering
- `session/resume`, `session/load` resume, an unknown session, and `loadSession` not advertised
- cancel mid-turn (a grandchild process is killed) and cancel with an agent that ignores SIGTERM
- rate limit with vendor retry, auth failure at authenticate and at prompt, and a crash with stderr
- read-only `ask` mode with denials and mode re-assertion, read-only refused without `ask`, and a read-only violation
- model selection and an unknown model
- unapproved repository config
- handshake timeout and caller cancellation
- probe: signed in, signed out, not installed, user config limitations

**Real smoke tests** (`real_test.go`) spend real allowance. They skip unless enabled, and skip when the CLI is missing or signed out:

```
YIP_REAL_PROVIDER_TESTS=1 [YIP_CURSOR_MODEL=<id>] go test -run TestReal -v ./internal/providers/cursor/
```

They cover: a conversation-mode answer; an edit run with one approval and a denied `rm`, then resume of the same session; and cancel mid-stream.
