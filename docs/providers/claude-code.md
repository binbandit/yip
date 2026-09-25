# Claude Code provider adapter

Package: `internal/providers/claude` · constructor `claude.New(claude.Options{...})` · provider id `claude` · label "Claude Code".

The adapter runs the **unmodified Claude Code CLI** as a subprocess and talks to it only through its documented programmatic interface: print mode (`-p`) with newline-delimited JSON on stdin and stdout ("stream-json"), the documented MCP configuration flags, and the documented `--permission-prompt-tool` bridge. It does not scrape terminal output, drive a login flow, read Claude Code credential files or the keychain, or use `--dangerously-skip-permissions` / `bypassPermissions`.

## Pinned version

| | |
|---|---|
| Tested version | `2.1.282 (Claude Code)` (`TestedVersion`) |
| Verified locally against the binary | `claude --version`, `claude --help`, `claude auth --help`, `claude auth status --help` only |
| Not run during development | any prompt, `auth status`, `auth login`, or other account-touching command |

`Probe` compares the installed version with `TestedVersion` (a mismatch is reported as a limitation, `Tested=false`) and checks that every flag the adapter relies on appears in the installed binary's `--help`. If one is missing, capabilities are cleared and `Start` returns an error wrapping `providers.ErrUnsupported` instead of launching an unbounded session.

## Launch

One process per run attempt, started in `StartSpec.Workdir`, in its own process group (`providers.StartProcess`). No positional arguments: the prompt travels over stdin. Every value-taking flag uses `--flag=value`, so an instruction beginning with `-` cannot be parsed as another option.

### Arguments common to all modes

```
claude -p
  --output-format=stream-json
  --input-format=stream-json
  --verbose
  --include-partial-messages
  --restricted
  --strict-mcp-config
  --mcp-config=<tmp>/yip-claude-XXXX/mcp.json
  --disable-slash-commands
  --settings={"disableAllHooks":true}
  --permission-prompt-tool=mcp__yip__<PermissionTool>
  --permission-prompts=host
  [--model=<Model>]                       # only when StartSpec.Model is set
  [--resume=<ResumeSessionID>]            # only when resuming
  [--append-system-prompt=<Instructions>] # engineer role + org/project rules
```

`mcp.json` (mode 0600, in a private temp directory outside the worktree, removed when the session ends) defines exactly one server, so bridge secrets in `MCP.Env` never appear on the command line:

```json
{"mcpServers":{"yip":{"type":"stdio","command":"<MCP.Command>","args":["..."],"env":{"...":"..."}}}}
```

### Per-mode permission boundary

| Mode | `--permission-mode` | `--tools` (the only built-in tools that exist) | `--allowedTools` | `--disallowedTools` |
|---|---|---|---|---|
| `edit` | `acceptEdits` | `Bash,Read,Edit,Write,Glob,Grep,NotebookEdit,WebFetch,WebSearch` | `mcp__yip` | none |
| `readonly` | `dontAsk` | `Read,Glob,Grep` | `mcp__yip` | `Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch` |
| `conversation` | `dontAsk` | `Read,Glob,Grep` | `mcp__yip` | `Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch` |

- **edit**: `acceptEdits` lets Claude create and edit files inside the working directory and auto-approves the documented filesystem commands (`mkdir`, `touch`, `rm`, `rmdir`, `mv`, `cp`, `sed`) on in-scope paths. The CLI still prompts for every other Bash command outside its read-only set, for web access, for paths outside the workspace, and for protected paths. `--restricted` confines the file tools to the working directory and sends writes to settings, git, and tool-configuration files to the permission handler. Every prompt goes to the yip bridge's permission tool. yip's own MCP tools are pre-approved (`mcp__yip` matches every tool of the `yip` server) because the hub authorizes each call against the run lease.
- **readonly / conversation**: no tool that can modify files, run commands, or reach the network exists in the session. The mutating tools are also denied by name, and `dontAsk` denies anything that would otherwise prompt instead of asking. Reads inside the working directory and the pre-approved yip tools still run. Checks go through the yip `work_run_check` tool, not Bash.
- `AskUserQuestion`, subagents (`Agent`), and task tools are not in any `--tools` list.

### Environment

The launch environment is `StartSpec.Env` verbatim. If that is empty, the adapter uses `Options.Env`, or `providers.BaseEnv` plus `CLAUDE_CONFIG_DIR`, proxy variables, and `NODE_EXTRA_CA_CERTS`. It then sets:

| Variable | Why |
|---|---|
| `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` | yip owns engineer memory and confidentiality scopes. Claude Code's auto memory would carry context across scopes. |
| `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1` | No implicit `CLAUDE.md` (user or repository). yip passes approved instructions explicitly. Opt out with `Options.AllowClaudeMD`. |
| `DISABLE_AUTOUPDATER=1` | Keep the pinned binary during a run. |
| `CLAUDE_CODE_STARTUP_FAILURE_RESULTS=1` | A refused startup writes a `result` naming `startup_failure_reason`. |
| `CLAUDE_CODE_MCP_TOOL_IDLE_TIMEOUT=86400000` (default only) | Stdio MCP tool calls otherwise abort after 30 idle minutes, which would cut off a permission prompt waiting on a human. `Options.MCPToolIdleTimeout` changes this; a negative value keeps Claude Code's default. |

`ANTHROPIC_API_KEY` is **never** forwarded implicitly from the runner's environment. In print mode Claude Code always uses an API key when one is present, even if the user signed in with a subscription. Inheriting one would silently turn a subscription run into an API-billed run. Put the key in the profile's configured environment when API billing is intended.

## Startup boundary (A31)

The `-p` help text on the pinned binary says: "The workspace trust dialog is skipped when Claude is run in non-interactive mode … Only use this in directories you trust." `headless.md` and `permissions.md` ("What runs before you trust a folder") add that, without `--bare`, a `-p` session runs hooks from a project's `.claude/settings.json`, connects the servers in its `.mcp.json` without asking, and honours a project skill's hooks and `allowed-tools`. The adapter closes each path explicitly:

| Repository- or user-supplied content | Control | Evidence |
|---|---|---|
| Hooks, `env` blocks, `apiKeyHelper`/helpers, and permission rules in user, project, or local settings files | `--restricted` (loads only managed settings and `--settings`) and `--settings={"disableAllHooks":true}` | `--restricted` in `--help`; `disableAllHooks` via `--settings` in permissions.md |
| `.mcp.json`, user, or plugin MCP servers | `--strict-mcp-config` with a single yip-only `--mcp-config` | `--help` |
| Project skills (with their hooks and `allowed-tools`) and custom commands | `--disable-slash-commands` ("Disable all skills") | `--help` |
| Command-running tools | `--restricted` removes them unless `--tools` names them; `--tools` names only the mode's set | `--help` |
| `CLAUDE.md` instruction files and auto memory | `CLAUDE_CODE_DISABLE_CLAUDE_MDS=1`, `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` | env-vars.md |
| Project subagents, inline subagent MCP servers, `@skills-dir` plugins | not used by `-p` (docs); the `Agent` tool is not enabled anyway | permissions.md |

What was verified vs. assumed:

- **Verified on the pinned binary:** every flag above is in `claude --help`. `--permission-prompt-tool` is not in the option list, but it is named in the help text of `--permission-prompts`, documented in the CLI reference, and its error strings are in the binary. `TestRealHelpHasRequiredFlags` runs this check against an installed `claude`.
- **From documentation, not exercised:** the loading behaviour of `--restricted`, `--disable-slash-commands`, and the two environment variables. The A31 acceptance test, which runs a real session in a repository with a hostile hook and `.mcp.json`, is still to be run with a signed-in account.
- **Assumed:** plugins installed at user level stay off because `--restricted` ignores the user settings that enable them. `--plugin-dir` and `--plugin-url` are never passed.
- **Deliberately not used:** `--bare` never reads OAuth or keychain credentials, so a subscription sign-in could not work; it is only usable for API-key profiles. `--safe-mode` disables MCP servers, and it is undocumented whether `--mcp-config` survives it.

## Stream protocol

### stdin (adapter → CLI)

- User turn, both the initial prompt and steering. Every message carries a fresh `uuid` so the adapter can match it with `user_message_uuid(s)` on results:
  `{"type":"user","uuid":"<uuid>","session_id":"","message":{"role":"user","content":"<text>"},"parent_tool_use_id":null}`
- Interrupt, on cancel. The `control_request` envelope and the `interrupt` request with `cancel_queued` are from the Agent SDK reference. `cancel_queued` is sent only when `system/init.capabilities` includes `interrupt_cancel_queued_v1`:
  `{"type":"control_request","request_id":"yip-interrupt-<uuid>","request":{"subtype":"interrupt","cancel_queued":true}}`
- Any `control_request` that the CLI sends to the adapter gets an error `control_response` and an `EventWarning`, so nothing waits on a hidden prompt. None is expected: permissions go to the MCP tool.

### stdout (CLI → adapter) → yip events

| Claude Code message | yip event |
|---|---|
| any message with a new `session_id` | `EventVendorSession` |
| `system/init` | `EventStatus` "Claude Code session started (model …)" with model, tools, MCP status, and `apiKeySource` in `Data`. `EventWarning` if the `yip` server is missing or its status is not `connected` (`pending` gives a status line), or if `mcp_server_errors` is non-empty. |
| `stream_event` `content_block_delta` / `text_delta` (main thread) | `EventMessageDelta` |
| `assistant` text blocks | `EventMessage` (complete text). If the message has an `error` category, as Claude Code's synthetic usage-limit or sign-in messages do, the text becomes an `EventWarning` rather than an engineer message. |
| `assistant` `tool_use` | `EventToolStarted`, `Tool`=name, `Text` like "Reading refresh.go", "Running go test ./...", "Using yip room post". `Data`={id,name,input}; input over 16 KiB is omitted. |
| `user` `tool_result` | `EventToolFinished`, "(failed)" appended when `is_error`, with a ≤4000-byte output excerpt in `Data` |
| `result` | `EventUsage`, see below; also drives stdin closing |
| `system/api_retry` | `EventStatus` "Claude API request failed (rate_limit); retrying (attempt n of m) in 5s" |
| `rate_limit_event` `rejected` | `EventWarning`; records `resetsAt` (Unix seconds) |
| `rate_limit_event` `allowed_warning` | one `EventWarning` per run |
| `system/status` `compacting` | `EventStatus` |
| `auth_status` with `error` | `EventWarning`; marks an auth signal |

Fields that other message types reuse with a different JSON shape (`message`, `error`, `status`, `result`, `errors`, `event`) are decoded lazily, so an unexpected message type never breaks parsing of the rest.

### Turn lifecycle and stdin closing

The adapter keeps stdin open while any user message it sent is unanswered. On each `result` it removes the answered UUIDs from `user_message_uuids`, or from `user_message_uuid` plus everything sent before it, since merged turns name only the last message. Results with `origin.kind=="task-notification"` are ignored. A result with no echo at all clears the queue so the process cannot be held open forever. When nothing is pending it closes stdin, and Claude Code exits after its final turn. Closing stdin earlier would cancel pending permission prompts (headless.md), which is why the adapter does not close it early.

stdout is read from an `os.Pipe` the adapter owns rather than `cmd.StdoutPipe()`. `providers.StartProcess` starts `cmd.Wait()` immediately, and `Wait` closes a `StdoutPipe` as soon as the process exits, which can drop a final `result` line that has not been read yet. `TestSessionKeepsResultWrittenJustBeforeExit` covers this.

## Permission-prompt tool contract (for the bridge author)

Claude Code calls the bridge tool named by `--permission-prompt-tool=mcp__yip__<PermissionTool>` as an ordinary MCP `tools/call` whenever a tool call would otherwise prompt. The shapes below were checked against the pinned binary's bundled code.

**Request** (`tools/call` `params.arguments`):

```json
{"tool_name": "Bash", "input": {"command": "git commit -m fix", "description": "Commit the fix"}, "tool_use_id": "toolu_01…"}
```

`input` is the exact tool input Claude wants to run. Declare the tool's `inputSchema` with `tool_name` (string), `input` (object), and `tool_use_id` (string, optional).

**Response**: a normal `CallToolResult` whose **first content block is `{"type":"text"}` and whose text is a JSON string**:

```json
{"content":[{"type":"text","text":"{\"behavior\":\"allow\",\"updatedInput\":{\"command\":\"git commit -m fix\",\"description\":\"Commit the fix\"}}"}]}
{"content":[{"type":"text","text":"{\"behavior\":\"deny\",\"message\":\"The owner rejected this command.\"}"}]}
```

- `allow`: `{"behavior":"allow","updatedInput":{…}}`. Echo the received `input` unchanged, or a narrowed version of it; Claude Code runs exactly `updatedInput`. When `updatedInput` is omitted or `{}`, the pinned version falls back to the original input on the common path, but the bridge should always send it.
- `deny`: `{"behavior":"deny","message":"…"}`. `message` is required and is shown to the model. Optional `"interrupt":true` also aborts the current turn.
- **Never send `updatedPermissions`.** It can add session permission rules or switch the permission mode with `setMode`, for example to `acceptEdits`, which would widen the run's boundary outside yip's approval ledger.
- Anything else is treated as a deny with "The permission prompt tool returned an invalid permission result. Expected {behavior: 'allow', updatedInput?: object} or {behavior: 'deny', message: string}." A non-text first block raises "Permission prompt tool returned an invalid result. Expected a single text block…".
- Do **not** mark yip tools with `_meta["anthropic/requiresUserInteraction"]: true`: Claude Code converts an `allow` for such a tool into a deny under `--permission-prompt-tool`.
- Prompts do not time out inside Claude Code. The call is bounded only by MCP timeouts: `MCP_TOOL_TIMEOUT`, about 28 hours by default, and the idle timeout, which the adapter raises to 24 hours. Progress notifications also keep the call alive. When yip cancels a run, the adapter ends stdin and Claude Code cancels the pending prompt; the bridge should then expire the approval.
- Claude Code waits up to `MCP_TIMEOUT` (30 s) for the bridge to connect before the first turn. If the tool is missing when the first prompt is needed, the run exits with `Error: MCP tool mcp__yip__… (passed via --permission-prompt-tool) not found`.
- The tool is also visible to the model and pre-approved by `mcp__yip`, so the model *can* call it directly. Such a call grants nothing because Claude Code does not act on it, but the bridge should not treat it as a real approval request for anything other than the named action.
- In `readonly` and `conversation` modes (`dontAsk`) the tool is never called. Would-prompt actions are denied without asking.

## Operations

| Operation | Behaviour |
|---|---|
| `SendInput` | Writes a stream-json user message; returns **`"queued"`**. Per the Agent SDK reference, Claude Code queues it and may pick it up between tool calls of the running turn ("If Claude Code picks up a regular message of yours between tool calls, the turn answers the picked-up message from then on"), otherwise answers it as the next turn. The adapter cannot know which will happen at write time, so it reports the guaranteed behaviour. After the session stops accepting input it returns `claude.ErrInputClosed`. |
| `ResolveApproval` | `ErrUnsupported`. Approvals go to the bridge's MCP permission tool, which answers Claude Code directly; no approval request reaches the adapter. |
| `AnswerQuestion` | `ErrUnsupported`. `AskUserQuestion` is not enabled, and engineers ask through yip room tools. |
| `Cancel` | 1) writes the `interrupt` control request if stdin is open and waits up to `InterruptWait` (5 s) for the turn's `result`. Headless.md says SIGTERM alone "leaves the turn that was in progress unfinished and records no result". 2) closes stdin, which also cancels a pending permission prompt. 3) waits up to 3 s for exit. 4) `Process.Terminate(Grace)`: SIGTERM to the process group, SIGKILL after `Grace` (5 s). Outcome `cancelled`. |
| `Wait` | Returns after the leader has exited, the group has been reaped with `Terminate`, and events are closed. `ExitConfirmed` is `Terminate`'s answer: true only when no group member remains. |
| Timeout | `StartSpec.Timeout` runs the same stop sequence; the outcome is `failed` with "exceeded its … time limit". |
| Resume | `--resume=<id>`. Claude Code finds the session by ID in any project on the machine. With `--system-prompt-snapshot` at its default (`on`), a resumed conversation keeps the system prompt recorded at its first request until compaction, so **changed `Instructions` may not apply to a resumed session**. The help text says the flag has "no effect where system-prompt recording is not yet enabled", so whether this applies depends on the account; it has not been verified. |

### Outcome mapping

Checked in order:

1. `Cancel` was called → `cancelled`.
2. `StartSpec.Timeout` fired → `failed`.
3. The last `result` has `subtype:"success"` and `is_error:false` → `succeeded`, `FinalText` = `result.result`.
4. Any of these → **`auth_required`**: an assistant `error` of `authentication_failed` or `oauth_org_not_allowed`, an `auth_status.error`, `api_error_status` 401, or result/stderr text matching "Not logged in", "Please run /login", "Invalid API key", "Login expired", "OAuth token expired", "claude auth login".
5. Any of these → **`rate_limited`**: `rate_limit_event.status=="rejected"`, an assistant `error` of `rate_limit` or `billing_error`, `api_error_status` 429, or text matching "You've hit your … limit", "usage limit", "rate limit", "Request rejected (429)", "Credit balance is too low", "spend limit reached", "limiting requests". `RetryAfter` = `rate_limit_info.resetsAt` − now **only when the CLI supplied `resetsAt`**. The human text "resets 3:45pm" is not parsed, because it has no timezone, and no reset time is invented. `EventRateLimited` is emitted.
6. Otherwise → `failed`, with `Error` = the result's `result`/`errors` text, or "exited (exit status N) without a result" plus the stderr tail (last 2000 bytes). `EventError` is emitted.

"Context limit reached" is deliberately not a quota signal.

### Usage and billing

`EventUsage` is emitted on each `result`. It carries **this run's totals so far**, so the last one wins; `Result.Usage` is the final value. `Source:"vendor"`.

- Fresh session: tokens are the latest result's `modelUsage` summed over models. Input = `inputTokens + cacheReadInputTokens + cacheCreationInputTokens`, output = `outputTokens`. `CostUSD` = `total_cost_usd`, the CLI's client-side estimate, and only when the field is present.
- Resumed session: since v2.1.277, `total_cost_usd` and `modelUsage` include totals restored from earlier runs of that session. The adapter therefore reports only the sum of this run's per-turn `usage` tokens and **omits `CostUSD`** rather than double-counting.
- A crash `result` with zeroed totals does not overwrite earlier figures.
- Billing: `api` when `system/init.apiKeySource` is `ANTHROPIC_API_KEY`, `apiKeyHelper`, or `/login managed key`, or when the launch environment contains `ANTHROPIC_API_KEY`. Otherwise it is the label from the most recent `Probe` (`subscription` or `unknown`).

## Probe

Runs `claude --version`, `claude --help`, and, unless `Options.SkipAuthStatus` is set, the documented non-interactive `claude auth status --json`. The CLI reference documents that command as "Show authentication status as JSON … Exits with code 0 if logged in, 1 if not". All three run with the adapter's allowlisted environment in the system temp directory, never in a repository.

`auth status --json` fields on 2.1.282, read from the binary: `loggedIn`, `authMethod` (`claude.ai` | `oauth_token` | `api_key` | `api_key_helper` | `third_party` | `none`), `apiProvider`, `apiKeySource?`, `email?`, `orgId?`, `orgName?`, `subscriptionType?`, `configDirectory`.

| `auth status` result | `AuthState` | `Billing` |
|---|---|---|
| `loggedIn:false` | `needs_signin` ("Run `claude auth login` on this machine…") | `unknown` |
| `claude.ai` or `oauth_token` (a `setup-token` subscription token) | `ready`; `Account` = email or org name | `subscription` |
| `api_key` or `api_key_helper` | `ready` | `api` |
| `third_party` (Bedrock, Vertex, Foundry) | `ready` | `api`, with the provider named in `AuthDetail` |
| unparseable output, or check skipped | `unknown` | `unknown` |

`ANTHROPIC_API_KEY` in the configured environment always forces `api`, because print mode always uses it.

Models: the CLI has no non-interactive model listing, so `ModelEnumeration=false`. `Models` holds the aliases the installed binary names in its own `--model` help text (currently `fable`, `opus`, `sonnet`). An empty `Model` means Claude Code's default and omits `--model`.

## Capability matrix

| Capability | Advertised | Evidence |
|---|---|---|
| Structured events (init, deltas, messages, tools, results) | yes | Fake CLI tests; message shapes from the pinned docs. Not yet observed from the real binary. |
| Tool approvals (via bridge permission tool) | yes | Launch flags verified in `--help`; request/response format read from the pinned binary's code. **Real round trip unverified**: `TestRealClaudeSmoke` covers it. |
| Read-only enforcement | yes | Argument construction unit-tested (readonly never exposes or allows Edit/Write/NotebookEdit/Bash). Real enforcement unverified. |
| MCP tools (yip bridge only) | yes | Fake: config file, strict flag, and bridge-failure warning. Real: smoke test. |
| Session resume | yes | Fake: `--resume` passed; the vendor session id is reported. Real unverified. |
| Active steering | yes (`queued`) | Fake: stdin delivery, and stdin kept open until the steering turn is answered. Mid-turn pickup semantics from docs only. |
| Usage telemetry | yes | Fake: tokens, cost, and resume scoping. Real unverified. |
| Cancellation (interrupt + process-group kill) | yes | Fake: clean interrupt with `cancel_queued`, and a stuck CLI that ignores SIGTERM with a child in its group (child confirmed gone). Real interrupt unverified. |
| Rate-limit / auth mapping | yes | Fake with doc-shaped messages; classifier unit tests. Real unverified. |
| Crash reporting | yes | Fake: non-zero exit, stderr tail. |
| User questions | **no** | `AskUserQuestion` is disabled; questions go through room tools. |
| Model enumeration | **no** | Help-text aliases only. |
| Provider sandbox | **no** | Claude Code's Bash sandbox is not enabled by this adapter; use a container profile for OS-level isolation. |

Nothing has been verified against a real Claude account. To record A28 evidence, run:

```
YIP_REAL_PROVIDER_TESTS=1 go test -v -run TestRealClaudeSmoke ./internal/providers/claude/
```

The test creates a temporary git repo and starts the test binary as a stub MCP bridge that exposes `room_post` and `permission_prompt`. It asks Claude to post to the room, then to run `git commit --allow-empty -m yip-smoke` through Bash. That command is not in the read-only set, so it must go through the permission tool. The stub allows only commands containing `yip-smoke`. The test asserts success, the vendor session id, `ExitConfirmed`, the recorded `room_post` call, a recorded `permission_prompt` for `Bash`, and the resulting commit. It uses a small amount of the signed-in account's allowance.

## Authentication: subscription vs API

- **Subscription (Claude Code):** the user signs in on each runner through Anthropic's own flow, with `claude auth login` or `/login` in an interactive `claude`, or with a `claude setup-token` token they configure. yip never collects, stores, proxies, or reads Claude credentials; it only runs `claude auth status --json`. This matches Anthropic's guidance that an end user may sign in to the unmodified Claude Code binary with their own subscription, including where a platform hosts Claude Code (legal-and-compliance.md). Each machine authenticates separately, and one account used by several engineers does not create more allowance.
- **API key:** the user's own `ANTHROPIC_API_KEY`, or a cloud-provider credential, configured explicitly in the profile environment. It is labelled `api` (shown as "API billed").
- **Claude Agent SDK** is a different, optional, API-key-only adapter. It must not offer Claude.ai login, and this adapter's subscription path must not be presented as SDK authentication.
- There is no automatic paid fallback. `--fallback-model` is not used. If the account itself has paid "extra usage" enabled, Claude Code may continue past a plan limit; that is the user's account setting. The adapter surfaces `rate_limit_event` warnings either way.

## Limitations

- Steering is `queued`: the adapter cannot promise delivery into the active turn.
- Per-run cost is omitted for resumed sessions (tokens are still reported).
- Changed instructions may not reach a resumed session until it compacts (system-prompt snapshot, where enabled).
- `CLAUDE.md` files are not loaded unless `Options.AllowClaudeMD` is set.
- In `readonly` and `conversation` modes, would-prompt actions are denied, not escalated.
- After the last answered turn stdin closes. Claude Code then stops background shells about 5 s after the final result, and any later background turn cannot obtain a permission prompt.
- Tool inputs over 16 KiB are left out of event data, and tool outputs are excerpted to 4000 bytes. The adapter does not redact; the runner redacts before upload.
- Unix only, because process groups are required.
