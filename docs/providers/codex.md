# Codex provider adapter

Package: `internal/providers/codex` (`codex.New()`, or `codex.NewAdapter(opts...)` with `WithExecutable`, `WithTimeouts`, `WithProbeEnv`).

The adapter drives OpenAI Codex through its documented **`codex app-server`** JSON-RPC interface over **stdio** (newline-delimited JSON-RPC 2.0, with the `"jsonrpc"` field omitted on the wire). Reference: [app-server documentation](https://learn.chatgpt.com/docs/app-server). It does not use the experimental WebSocket transport, it does not opt into experimental API fields (`capabilities.experimentalApi = false`), and it never reads Codex credential files.

## Pinned version

| | |
|---|---|
| Tested binary | `codex-cli 0.147.0` (`/opt/homebrew/bin/codex` on the development machine) |
| `TestedVersion` constant | `"0.147.0"`. `Probe` sets `Tested` only when `codex --version` reports exactly this version. |
| Protocol schema | `internal/providers/codex/schema/`, generated from that binary (stable surface only) |
| Go types | `protocol.go`, hand-written for the subset the adapter uses |

### Schema generation (exact command)

```sh
cd internal/providers/codex
codex app-server generate-json-schema --out schema
```

`--experimental` was deliberately not passed, so the committed schema is the stable API surface. The generated files were byte-identical across two runs of 0.147.0. `schema_test.go` checks every method name, every JSON field, and every enum value the adapter uses against the committed schema. It also fails if the schema defines a server→client request the adapter does not explicitly handle.

To upgrade Codex:

1. Regenerate the schema.
2. Run `go test ./internal/providers/codex/`.
3. Fix any drift.
4. Bump `TestedVersion`.
5. Run the real smoke test below.

The only real-binary commands run during development were `codex --version`, `codex app-server --help` (and the subcommand `--help` pages), and the schema generation above.

## Launch

```
codex app-server --listen stdio:// \
  --disable hooks --disable plugins --disable apps --disable memories \
  -c notify=[]
```

- **Working directory:** the run's `Workdir`. `thread/start` inherits it (see Startup boundary).
- **Environment:** exactly `StartSpec.Env`, the runner's allowlist. When `Env` is nil, the adapter starts with `providers.BaseEnv(["CODEX_HOME"])`, then asks an isolated app-server for its effective configuration. Only the selected provider's `env_key` and `env_http_headers` references are added from the runner process environment. Configuration roots and process-loading variables cannot be imported through provider references. Explicit `Env` values are never augmented. `HOME` and `CODEX_HOME` are honoured and never relocated.
- **Process group:** the process runs in its own group, via `providers.StartProcess`, so cancellation reaches every descendant, including MCP servers and shells.
- **Secrets:** nothing secret is placed on argv. The yip MCP server definition and its environment (bridge token) travel over stdin inside `thread/start`'s `config`.
- **Client identity:** `initialize` sends `clientInfo = {name: "yip", title: "yip", version: "0.1.0"}`. Per the Codex docs, `clientInfo.name` appears in OpenAI's compliance logs. After that the adapter sends the `initialized` notification.

## Model gateways

Custom providers use the harness's effective `config/read` result, including
its system and user configuration precedence. YIP does not parse or merge TOML,
open credential files, run shell profiles or create a credential store. Initial
environment discovery runs in an empty directory so a repository cannot request
additional runner secrets. Probe and execution use the same environment resolver.

For example, a runner whose `CODEX_HOME/config.toml` contains:

```toml
model_provider = "work"
model = "work-model"

[model_providers.work]
name = "Work gateway"
base_url = "https://gateway.example.invalid/v1"
env_key = "WORK_GATEWAY_KEY"
```

can use `WORK_GATEWAY_KEY` already exported to the runner process. An interactive
shell export does not automatically reach a running service. A Docker worker
still requires the existing explicit `--docker-env WORK_GATEWAY_KEY` import;
its supplied environment is authoritative. An isolated home uses only the
configuration visible inside that home.

An installed executable is not proof of configuration. A selected provider whose
required `env_key` is absent or blank is reported as needing configuration, and
a run stops before starting a thread. Locally configured gateways are available
for selection, with unknown billing and an explicit unverified status: neither
credential validity, model access nor gateway reachability is established by a
probe or a model catalogue. The engineer's API billing permission remains in force.
Configuration values, endpoint URLs, headers and keys are not exposed as
installation metadata.

Named profiles need care with the pinned release: Codex 0.147.0 app-server starts
with default loader overrides, so its CLI `--profile` does not select a named
configuration layer. Legacy top-level `profile` is rejected by that release.
Use a dedicated `CODEX_HOME` with the desired `config.toml` for this adapter;
YIP does not guess a profile or enumerate every profile as available.

This support is specific to this adapter. OpenCode's isolated launch currently
replaces user configuration and checks stored sign-in, so an external gateway
configuration or environment-only provider is not proof of availability there.
Pi's current controlled SDK host excludes `models.json` and enumerates stored
credential metadata, so custom providers and environment-only configuration are
not discovered. A terminal harness working outside YIP does not demonstrate
that its configuration reaches YIP's controlled execution environment.

Sources checked: [configuration documentation](https://learn.chatgpt.com/docs/config-file/config-advanced),
[0.147.0 configuration loader](https://github.com/openai/codex/blob/rust-v0.147.0/codex-rs/config/src/loader/mod.rs),
[app-server launch](https://github.com/openai/codex/blob/rust-v0.147.0/codex-rs/cli/src/main.rs),
and [provider account state](https://github.com/openai/codex/blob/rust-v0.147.0/codex-rs/model-provider/src/provider.rs).
Tests use synthetic configurations and a fake app-server process boundary;
no account-backed gateway or model request was made.

## Session lifecycle

1. **`Start`**, synchronous:
   - Validate the spec: absolute workdir, known mode, and a bridge command.
   - Launch app-server, then run `initialize` and send `initialized`.
   - Call `config/read {cwd: Workdir, includeLayers: true}`.
   - Refuse read-only and conversation modes when exec-policy rules exist (see Startup boundary). This returns an error wrapping `providers.ErrUnsupported`.
2. **Session**, asynchronous. Failures here become a `Result` with a typed outcome:
   1. `account/read {refreshToken: false}`.
      - If there is no account and `requiresOpenaiAuth` is true, the result is `auth_required` and no thread is started.
      - The account also sets `Usage.Billing`.
   2. `hooks/list {cwds: [Workdir]}`. Start is refused if any enabled, trusted, non-managed hook remains.
   3. `thread/start`, or `thread/resume {threadId: ResumeSessionID, cwd: Workdir}`. Both send:
      - `model` (omitted when empty, so Codex uses its default);
      - `developerInstructions = Instructions`;
      - `sandbox`, `approvalPolicy`, and `approvalsReviewer: "user"`;
      - `config` overrides (below).
   4. Emit `vendor_session` with the thread id.
   5. Verify the thread response:
      - the cwd equals the workdir;
      - the approvals reviewer is `user`;
      - the sandbox type matches the mode;
      - network is off.

      Any mismatch refuses the run. Writable roots outside the workdir and temp dirs do not refuse the run; they are reported as a `warning`. The adapter then emits status lines with the sandbox summary and the loaded instruction files (`instructionSources`).
   6. `turn/start {threadId, input: [{type: "text", text: Prompt}]}`. The prompt is the user turn; instructions are never sent as the user turn.
   7. Stream notifications and answer server requests until the last turn completes.
   8. Shutdown: close stdin (stdio app-server exits on EOF), then `Process.Terminate`, which sends SIGTERM to the group and escalates to SIGKILL. `ExitConfirmed` is `Terminate`'s answer.

### Mode mapping

| yip mode | `sandbox` | `approvalPolicy` |
|---|---|---|
| `edit` | `workspace-write` | `on-request` |
| `readonly` | `read-only` | `{"granular": {"sandbox_approval": false, "rules": false, "request_permissions": false, "skill_approval": false, "mcp_elicitations": true}}` |
| `conversation` | `read-only` | same as `readonly` |

- **Edit mode.** Commands run inside Codex's workspace-write sandbox. The sandbox allows writes to the workdir, `/tmp` and `$TMPDIR`. `.git`, `.codex` and `.agents` stay read-only, and network is off. Anything that must leave the sandbox becomes an approval request.
- **Read-only modes.** The granular policy makes Codex reject sandbox escalation, rule prompts and permission grants outright; the model sees them as forbidden. As defense in depth, the adapter also auto-declines, with a `warning` event, any command, file-change or write-permission approval that still arrives in these modes.
- **Never used:** "never ask" combined with `danger-full-access`, `--dangerously-bypass-approvals-and-sandbox`, `bypass_hook_trust`, or `acceptForSession`/execpolicy amendments.

### Per-thread `config` overrides

These go in the `thread/start` and `thread/resume` `config` map, which Codex applies in the same session-flags layer as `-c`.

```jsonc
{
  "mcp_servers": {
    "yip": {"command": "<bridge>", "args": [...], "env": {...}, "enabled": true, "required": true,
            "default_tools_approval_mode": "approve", "startup_timeout_sec": 30, "tool_timeout_sec": 3600},
    "<every other server named in any config layer>": {"enabled": false}
  },
  "projects": {"<workdir>": {"trust_level": "untrusted"}, "<canonical workdir>": {...}, "<main checkout root>": {...}},
  "sandbox_workspace_write": {"network_access": false, "writable_roots": [], "exclude_tmpdir_env_var": false, "exclude_slash_tmp": false}
}
```

- **Nested keys.** Every override uses a nested key rather than a dotted one. Server names and paths can contain dots, and Codex applies the map in unspecified order, so keys must not overlap.
- **Auto-approved yip tools.** yip tools are auto-approved at the Codex level (`default_tools_approval_mode = "approve"`). The hub authorizes every bridge call against the run lease, and yip's own approval flow covers push, publish and merge. Without this setting, Codex 0.147 would prompt through MCP elicitation for every non-read-only yip tool.
- **Required yip server.** `required = true` makes Codex fail thread startup ("required MCP servers failed to initialize") if the bridge cannot start. The yip tools therefore can never be silently missing.

## Capability matrix

- **Fake only:** verified against the scripted fake app-server in `codex_test.go` and `fake_server_test.go`, and checked against the pinned schema.
- **Real account:** exercised against the installed binary with a signed-in account. None of this was done during development, per policy. `TestRealCodex` covers the marked rows; it was written but not run.

| Capability | Implemented | Fake only | Real account |
|---|---|---|---|
| Handshake (`initialize` / `initialized`, client name `yip`) | yes | yes | unverified (smoke) |
| Streaming (`item/agentMessage/delta` → `message_delta`, `agentMessage` → `message`) | yes | yes | unverified (smoke) |
| Tool events: commands, file changes, MCP, dynamic tools, web search | yes | yes (commands, edits, MCP) | unverified (smoke) |
| Command approval, allowed | yes | yes | unverified |
| Command approval, denied | yes | yes | unverified (smoke) |
| File-change approval | yes | yes (denied) | unverified |
| Network approval (`networkApprovalContext`) and permission requests | yes | yes (permissions) | unverified |
| MCP tool-approval elicitation | yes | yes | unverified |
| Read-only enforcement (granular policy plus auto-decline) | yes | yes | unverified |
| User questions (`item/tool/requestUserInput`; single-field MCP forms) | yes | yes (requestUserInput) | unverified |
| Steering (`turn/steer` → `immediate`) | yes | yes | unverified |
| Steering fallback (queued next turn → `queued`) | yes | yes | unverified |
| Cancel (`turn/interrupt`, then process-group kill) | yes | yes, including SIGKILL escalation and child reaping | unverified |
| Resume (`thread/resume` by thread id) | yes | yes | unverified (smoke) |
| Usage (`thread/tokenUsage/updated` → `usage`, `Source: "vendor"`) | yes | yes | unverified (smoke) |
| Rate limit → `rate_limited` plus vendor `RetryAfter` | yes | yes (with and without reset data) | unverified; cannot be triggered safely |
| Auth failure → `auth_required` (at start, and mid-turn 401) | yes | yes | unverified |
| Crash → `failed` with stderr tail | yes | yes | unverified |
| Probe: version, `Tested`, auth state, billing, models | yes | yes | unverified (smoke calls `Probe`) |
| Startup boundary checks (MCP disable, hook refusal, rules, trust, sandbox verification) | yes | yes | unverified |
| Model enumeration (`model/list`, hidden models skipped, paginated) | yes | yes | unverified |

`Capabilities()` advertises all ten `ProviderCapabilities` flags. `Probe` clears `ModelEnumeration` when `model/list` fails, and clears `ReadOnly` when exec-policy rule files exist.

## Event mapping

| Codex notification | yip event |
|---|---|
| `item/agentMessage/delta` | `message_delta` (text = delta) |
| `item/completed` `agentMessage` | `message`. The last one becomes `Result.FinalText`. |
| `item/started` / `item/completed` `commandExecution` | `tool_started` "Running \`cmd\`" / `tool_finished` "Ran \`cmd\` (exit N)", "Declined \`cmd\`", or "Failed \`cmd\`". Tool `shell`. |
| `item/started` / `item/completed` `fileChange` | `tool_started` "Editing a.go, b.go" / `tool_finished` "Edited …", "Edit declined: …", or "Edit failed: …". Tool `edit`. Paths are relative to the workdir. |
| `item/*` `mcpToolCall` | `tool_started` "Calling yip.room_post" / `tool_finished` "yip.room_post finished" or "… failed: msg". Tool `<server>.<tool>`. |
| `item/*` `dynamicToolCall`, `webSearch` | `tool_started` / `tool_finished` |
| `thread/tokenUsage/updated` | `usage` (see Usage) |
| `turn/plan/updated` | `status` with the in-progress step |
| `error` with `willRetry: true` | `warning` "Codex is retrying: …" |
| `warning`, `guardianWarning`, `deprecationNotice`, `configWarning`, `model/rerouted`, `hook/started`, `thread/status/changed` (`systemError`) | `warning` |
| `mcpServer/startupStatus/updated` | `warning` when the yip server fails, or when any other server starts (which should never happen) |
| `serverRequest/resolved` for a still-pending approval or question | `status` "Codex withdrew request …". A later `ResolveApproval` for it returns an error. |
| `turn/completed` | ends the attempt, or starts the queued next turn |

Every `tool_*` event carries `Data = {"itemId", "type", "status", "exitCode"?}`.

### Usage

`Usage.InputTokens` and `Usage.OutputTokens` are Codex's `total.inputTokens` and `total.outputTokens`. They are **cumulative for this attempt**; each `usage` event carries the running total. On resume, Codex replays the stored thread's usage right after `thread/resume`. The adapter records that replay as a baseline and subtracts it, so only the attempt's own tokens are reported. `CostUSD` is never set. `Billing` comes from `account/read`:

| `account/read` result | `Billing` |
|---|---|
| ChatGPT sign-in | `subscription` |
| API key | `api` |
| Amazon Bedrock | `api` |
| No account needed (custom model provider) | `unknown` |

## Approvals

Every approval request is emitted as `approval_request` with ID `codex-<json-rpc-id>`. `Raw` holds the vendor params for audit. The request stays pending until `ResolveApproval` is called, with no adapter timeout, because a human may take minutes. A decision is answered exactly once. Resolving a request twice, or resolving one that Codex already withdrew, returns an error.

| Codex request | `ApprovalAction` | Allow reply | Deny reply |
|---|---|---|---|
| `item/commandExecution/requestApproval` (command) | `exec`, `Command` = command, `Target` = cwd, `Summary` "Run \`cmd\`", `Detail` = reason | `{"decision":"accept"}` | `{"decision":"decline"}` |
| same, with `networkApprovalContext` | `network`, `Target` = host, `Summary` "Allow network access to host (proto)" | accept | decline |
| `item/fileChange/requestApproval` | `edit`, `Target` = changed paths (taken from the preceding `fileChange` item; else `grantRoot`), `Detail` = reason and diffs (truncated to 4 KB) | accept | decline |
| `item/permissions/requestApproval` | `edit` (write access), `network`, or `other`; `Target` = paths | `{"permissions": <requested>, "scope": "turn"}` | `{"permissions": {}, "scope": "turn"}` |
| `mcpServer/elicitation/request` with `_meta.codex_approval_kind = "mcp_tool_call"` | `mcp`, `Target` = server, `Summary` = Codex's message | `{"action":"accept","content":{}}` | `{"action":"decline"}` |
| `execCommandApproval` / `applyPatchApproval` (legacy v1) | `exec` / `edit` | `{"decision":"approved"}` | `{"decision":{"denied":{"rejection":reason}}}` |

The adapter never grants "for session" or persistent execpolicy amendments. Each yip decision covers exactly one action.

## Questions and other blocking requests

| Codex request | Handling |
|---|---|
| `item/tool/requestUserInput` | Emitted as `question` (`Kind: "ask"`, text = the questions, `Options` = the first question's option labels). `AnswerQuestion` replies with `{answers: {<qid>: {answers: [text or selected label]}}}`. A declined question replies with empty answers. |
| `mcpServer/elicitation/request`, form mode with exactly one string field | Emitted as `question`. The answer becomes `{"action":"accept","content":{field: text}}`; a declined question becomes `decline`. |
| Other elicitations (URL mode, multi-field forms) | Declined immediately, with a `warning`. |
| `item/tool/call` (dynamic tools; yip registers none) | Answered with `{success: false, contentItems: [text]}` and a `warning`. |
| `account/chatgptAuthTokens/refresh` | Answered with JSON-RPC error `-32601`: yip never handles tokens. A `warning` is emitted. |
| `attestation/generate` (not opted in) and any request unknown to the pinned schema | Answered with JSON-RPC error `-32601` and a `warning`. |

No server request is ever left unanswered. `schema_test.go` fails if a new request type appears in a regenerated schema without a mapping.

## Steering

The pinned schema supports adding input to an active turn (`turn/steer` with `expectedTurnId`).

- **Active turn:** `SendInput` calls `turn/steer` and returns **`"immediate"`** on success.
- **Queued:** in these cases the text is queued and `SendInput` returns **`"queued"`**. When the current turn completes, all queued texts are joined into the next `turn/start`, and a `status` event records the delivery.
  - There is no active turn yet (still setting up).
  - Codex rejects the steer, for example when the turn just ended or is a review/compaction turn that cannot be steered.
- **Session ended:** once the session has decided to end, `SendInput` returns an error rather than a false "queued". The end decision is taken under the same lock as the queue.

## Cancel

1. `Cancel` marks the attempt cancelled.
2. With an active turn, it sends `turn/interrupt` and waits up to `Timeouts.Interrupt` (10 s) for `turn/completed` with status `interrupted`.
3. It then terminates the process group: SIGTERM, then after `Timeouts.Grace` (5 s) SIGKILL, then any stragglers are reaped.
4. The outcome is `cancelled`. `ExitConfirmed` is true only when `Process.Terminate` confirms that the leader exited and no group member is left.

The fake tests cover both a cooperative server and one that ignores both `turn/interrupt` and SIGTERM, and check that a child process of the provider is gone. Background terminals started by Codex are in the same group and die with it.

`StartSpec.Timeout`, if set, triggers the same path with outcome `failed` ("timed out").

## Resume

- **Resuming:** with `ResumeSessionID` set, the adapter calls `thread/resume {threadId, cwd: Workdir, ...same overrides}` instead of `thread/start`.
- **Session id:** `vendor_session` is emitted with the thread id as soon as it is known, for new and resumed threads alike, and is returned in `Result.VendorSessionID`.
- **Failure:** if the thread cannot be resumed, the result is `failed` with Codex's message. Examples: an unknown id, or a paginated thread held open by another Codex process (JSON-RPC `-32600`).
- **Machine-local:** threads are stored under the runner's `CODEX_HOME`, so a vendor session id can only be resumed on the machine that created it.

## Errors, quota and auth

| Condition | Outcome |
|---|---|
| `account/read`: no account and `requiresOpenaiAuth` | `auth_required` before any thread; `auth_required` event |
| Turn failed, `codexErrorInfo = "unauthorized"` or any `{…: {httpStatusCode: 401}}` | `auth_required` |
| Turn failed, `codexErrorInfo = "usageLimitExceeded"` or HTTP 429 | `rate_limited`; `rate_limited` event with `RetryAfter` |
| Other turn failure | `failed` with Codex's message and `additionalDetails` |
| app-server exits or crashes unexpectedly | `failed` with the exit code and the last lines of stderr |
| JSON-RPC error during setup | `failed` with the method and message (and stderr tail if the process died) |

**`RetryAfter` is never invented.** It is derived only from vendor reset data:

1. Take the latest `account/rateLimits/updated` snapshot seen during the run.
2. If that gives nothing, call `account/rateLimits/read`.
3. Use the reset time (`resetsAt`, unix seconds) of the window at 100% or above; when both windows are exhausted, use the later reset.
4. If Codex reports `rateLimitReachedType` without an exhausted window, use the latest reset.
5. Otherwise `RetryAfter` is 0.

The human-readable "Try again at 3:15 PM" text in Codex's message is not parsed.

## Probe

1. Resolve the executable (`WithExecutable`, or `codex` on `PATH`). If it is not found, `AuthState` is `not_installed` and no capabilities are reported.
2. Run `codex --version` to get `Version`; `Tested` is true when it equals `TestedVersion`.
3. Run app-server briefly from an empty temp directory with the base allowlisted environment. Call:
   - `account/read {refreshToken: false}` for `AuthState` (`ready`, `needs_signin`, or `error`), `AuthDetail`, `Account` (the ChatGPT email when present) and `Billing`;
   - `config/read` for exec-policy rule detection;
   - `model/list` (paginated, hidden models skipped) for the models: `ID` = the model slug, `Label` = display name, `Default` = `isDefault`.
4. Limitations are listed in `ProviderInstallation.Limitations`.

## Startup boundary (A31)

Codex loads configuration in layers, lowest to highest precedence:

1. `/etc/codex/config.toml`
2. The enterprise cloud bundle
3. `$CODEX_HOME/config.toml` (default `~/.codex`) and any profile
4. Project `.codex/config.toml` files between the repository root and the cwd
5. Session flags: `-c` and the `thread/start` `config` map
6. Legacy managed config and MDM

Tables deep-merge. Project layers, including their `hooks.json`/`[hooks]` and `rules/`, are enabled only when the project's trust entry resolves to `trusted`. Sources: the codex-rs source at tag `rust-v0.147.0` (`config/src/loader/mod.rs`, `app-server/src/request_processors/thread_processor.rs`, `core/src/exec_policy.rs`, `hooks/src/registry.rs`) and the app-server docs.

### Controlled

| Startup input | Control | Verified by |
|---|---|---|
| Repository `.codex/` config (MCP servers, hooks, rules, settings) | `projects.<workdir, canonical workdir, project root, main checkout root>.trust_level = "untrusted"` in the thread config. The trust lookup reads session flags, so the layer stays disabled even if the user trusted the repo interactively. The workdir's layers are reported in a `status` event. | source; fake |
| app-server's automatic project trust write | `thread/start` omits `cwd`; the process cwd is the workdir. An explicit trust entry also suppresses the write. app-server only writes `trust_level = "trusted"` into the user's `config.toml` when `thread/start` includes a `cwd` and no trust entry exists. Resume passes `cwd`, but the explicit entry still prevents the write. | source; fake (asserts no `cwd`) |
| MCP servers | Every server named in any layer returned by `config/read` (disabled layers included) is set `enabled = false`; only `yip` is added. Any other server that still reports `starting` or `ready` produces a `warning`. | fake |
| Hooks | `--disable hooks` turns off the hooks engine. `hooks/list` is checked before the thread; if a runnable (enabled, trusted, non-managed) hook remains, for example because requirements pin the feature on, the start is refused. Managed hooks are reported as `status`. | source; fake |
| Plugins (which can carry MCP servers, skills and hooks) | `--disable plugins` | source |
| ChatGPT apps/connectors (extra MCP tools) | `--disable apps` | source |
| Codex memories (cross-session context that would cross yip scopes) | `--disable memories` | source |
| `notify` program | `-c notify=[]` | source |
| AI approval reviewer (`approvals_reviewer = "auto_review"` in user config) | `approvalsReviewer: "user"`, verified in the thread response | fake |
| Sandbox widening from user config (`sandbox_workspace_write.network_access`, `writable_roots`) | Pinned in the thread config. The response is verified: network on refuses the run, and any other writable root outside the workdir or temp is reported as a `warning`. | fake |
| Approval and sandbox defaults derived from trust | Always passed explicitly | fake |
| Environment | The runner allowlist only; secrets never go on argv | fake (asserts env and argv) |

### Not controllable through documented app-server switches

The adapter handles each of these as follows:

- **Exec-policy rules in the user and system config folders** (`~/.codex/rules/*.rules`, `/etc/codex/rules/*.rules`).
  - **Why it matters:** a rule with decision `allow` makes Codex run the matching command **outside the sandbox without asking** (`ExecApprovalRequirement::Skip { bypass_sandbox: true }`), whatever the approval policy. Only `codex exec --ignore-rules` skips rules; app-server has no equivalent.
  - **Detection:** the adapter lists (never reads) the rule files of the enabled user and system layers reported by `config/read`.
  - **Read-only and conversation runs** are refused with `ErrUnsupported`.
  - **Edit runs** proceed with a `warning` naming the files.
  - **Probe** reports `ReadOnly = false` and a limitation.
  - Clicking "always allow" in Codex creates such rules. yip never sends `acceptWithExecpolicyAmendment`.
- **Instruction files:**
  - `$CODEX_HOME/AGENTS.md` (or `AGENTS.override.md`) always loads.
  - Repository `AGENTS.md` loads even in untrusted projects.
  - They are instructions, not execution authority, so the adapter lists them in a `status` event (from `instructionSources`) rather than suppressing them. `-c project_doc_max_bytes=0` would disable repository AGENTS.md if yip decides to deliver project rules only through `Instructions`.
- **User settings stay in effect:** model provider, `shell_environment_policy`, skills, and the built-in web search tool (Codex default `cached`). The shell inherits the app-server environment, which is exactly the runner's allowlist.
- **Managed requirements and MDM:** these are organisation policy and may pin features or add managed hooks. The adapter reports managed hooks, but it cannot and does not override requirements.
- **Codex's own state:** the adapter does not relocate `CODEX_HOME` and copies no credential files. Codex therefore uses the user's own sign-in and writes session rollouts and its state DB under `CODEX_HOME` as usual.

## Known limitations

- **Unverified against a real account.** Every behaviour above is verified only against the fake server and the pinned schema until the smoke test is run. Rate-limit handling cannot be exercised on demand.
- **`.git` is read-only in workspace-write.** The engineer cannot `git commit` from inside Codex; the runner owns commits and checkpoints. Network is off unless a human approves an escalation.
- **Resume is local.** A vendor session is only resumable on the runner that created it.
- **Usage totals on resume** assume Codex replays the stored usage after `thread/resume`, which is documented behaviour. If it did not, the first attempt after a resume would over-report by the stored amount.
- **Questions.** Answers to multi-question `requestUserInput` prompts apply the one free-text answer to every question, and option selection applies only to the first question.
- **Platform.** Process-group handling is Unix-only (macOS, Linux). Windows is not supported.
- **Transport.** No experimental API is used. The WebSocket and unix-socket transports are not used.

## Real smoke test

The smoke test consumes account allowance and is not run in CI:

```sh
YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealCodex -v ./internal/providers/codex/
```

It performs these steps, with the test binary standing in as a one-tool MCP bridge (`ping`):

1. `Probe`.
2. An edit run in a temporary git repository that:
   - calls the yip MCP tool;
   - creates a file inside the sandbox;
   - asks for one escalated command, which the test **denies**.
3. A `thread/resume` of the same thread.

Record the output as the A28 compatibility evidence.
