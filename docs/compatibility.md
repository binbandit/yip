# Provider compatibility record

Recorded 25 September 2026 on macOS 27.0 (Darwin 27.0.0), Apple Silicon
(arm64), Go 1.26.5. This record distinguishes **implemented**, **verified
against a scripted fake** (deterministic, automated), and **verified against
the real provider with a signed-in account** (manual smoke test). Per the
build policy, **no prompt was sent to a real provider and no account was used
while building yip**; every real-account column is therefore *not yet
verified*. The smoke tests to fill it in are written and gated (below).

| Provider | Pinned / tested version | Interface | Auth route | Real-account smoke test |
|---|---|---|---|---|
| Codex | `codex-cli 0.147.0` | `codex app-server --listen stdio://` (JSON-RPC); bindings checked against the schema generated from this binary (`internal/providers/codex/schema/`) | Codex-managed ChatGPT sign-in (`subscription`) or the user's API key (`api`), read via `account/read` | **Not run.** `YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealCodex -v ./internal/providers/codex/` |
| Claude Code | `2.1.282` | Unmodified CLI: `-p --output-format stream-json --input-format stream-json`, yip MCP server only (`--strict-mcp-config`), permission prompts via `--permission-prompt-tool mcp__yip__permission_prompt` | User's own sign-in to the unmodified binary on each runner (`claude auth login`) → `subscription`; or an explicitly configured `ANTHROPIC_API_KEY` → `api`. Never inherited implicitly. | **Not run.** `YIP_REAL_PROVIDER_TESTS=1 go test -run TestRealClaudeSmoke -v ./internal/providers/claude/` |
| Cursor | none (not installed on the build machine) | `agent acp` (ACP v1) with Cursor's `cursor/ask_question` and `cursor/create_plan` extensions | Cursor CLI login on the runner (`agent login`) or `CURSOR_API_KEY`; billing `unknown` | **Not run; no installation available.** Probe reports `Tested=false`. `YIP_REAL_PROVIDER_TESTS=1 go test -v ./internal/providers/cursor/` |
| Fake (deterministic) | built-in | Scripted MCP client of the real `yip bridge` | none | n/a — used by the demo and the integration suite |

## Sign-in detection on real installations (26 September 2026)

Probed on the build machine through the adapters' own `Probe` (the same call
the runner makes; no prompt is sent):

| Provider | Installed | Result |
|---|---|---|
| Claude Code | `2.1.282` (tested version) | **Ready** — `claude auth status --json` reports a Claude subscription (Max); billed as `subscription`, no API key involved. All required launch flags present in `--help`; model aliases read from the help text. |
| Codex | `codex-cli 0.147.0` (tested version) | **Needs sign-in** — `account/read` over `codex app-server` returns no account and `requiresOpenaiAuth`, both with yip's allowlisted environment and with the full shell environment, so this machine is simply not signed in to Codex. Model list (5) and config read succeed. |

yip never reads either CLI's credential files or keychain entries: it asks
the CLI. After signing in on a machine (`codex login`, `claude auth login`),
**Machines → Check sign-in again** re-probes immediately
(`POST /v1/nodes/{id}/probe`); otherwise the runner re-checks every five
minutes. Neither adapter forwards `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`
from the runner's environment, so a subscription sign-in is never silently
replaced by API billing.

## Capabilities

| Capability | Codex | Claude Code | Cursor | Fake |
|---|---|---|---|---|
| Structured events | fake-verified | fake-verified | fake-verified | yes |
| Tool approvals round trip | via app-server requests; fake-verified | via bridge permission tool; format read from the pinned binary; fake-verified | via `session/request_permission`; fake-verified | yes |
| Read-only (reviewer) runs | granular policy + auto-decline; **refused when user `~/.codex/rules` allow-rules exist** (they bypass the sandbox) | `dontAsk` + tools limited to Read/Glob/Grep; Bash/Edit/Write denied | `ask` mode enforced + denials + abort-on-write backstop | yes |
| User questions | `requestUserInput` → declined, redirected to `human_ask` | disabled (questions go through room tools) | `cursor/ask_question` → declined, redirected; plans never self-approved | n/a |
| Steering | `turn/steer` → **immediate**; falls back to queued | **queued** (delivery into the active turn not promised) | **queued** during a turn | immediate |
| Session resume | `thread/resume` (**machine-local**: resumes only on the machine that created the thread) | `--resume <id>` | `session/resume` or `session/load` when advertised | no |
| Cancel | `turn/interrupt` + process-group kill; fake-verified incl. SIGKILL escalation | stdin interrupt + process-group kill | `session/cancel` + process-group kill | yes |
| Usage telemetry | vendor token usage (cumulative per attempt) | vendor tokens; cost omitted for resumed sessions | only if reported | unknown |
| Rate limit → wait | vendor reset time when provided; never invented | `resetsAt` only | heuristic text match (real texts unknown) | scripted |
| Auth expiry → wait for sign-in | fake-verified | fake-verified | fake-verified | scripted |
| Model enumeration | `model/list` | no (aliases only) | `agent models` (format undocumented) | n/a |
| Startup boundary (A31) | hooks/plugins/apps/memories disabled, other MCP servers disabled, repo marked untrusted; refuses if an approved hook would still run | `--restricted`, `--strict-mcp-config`, `--disable-slash-commands`, hooks disabled, CLAUDE.md/auto-memory off | refuses to launch while `.cursor/hooks.json`, `cli.json`, or `mcp.json` exist without owner approval | n/a |
| Commits in the worktree | the Codex sandbox keeps `.git` read-only — the **runner commits** on `work_publish_revision` | the model may commit; the runner commits outstanding changes on publish | same | runner |

## Known limitations and open verification

- All real-account behaviour above is unverified until the smoke tests run.
  They use a small amount of the signed-in account's allowance.
- Codex: per-thread config overrides (trust entries, required MCP, tool
  auto-approval) and the read-only approval policy must be confirmed on the
  real binary.
- Claude Code: the permission round trip and what `--restricted` excludes
  must be confirmed on the real binary.
- Cursor: whether `agent acp` loads MCP servers passed in `session/new`,
  whether `ask` mode removes write tools, and its real error texts are
  unknown.
- The GitHub connector has been tested only against an emulated API; its
  contract test needs `YIP_GITHUB_CONTRACT_REPO` and `YIP_GITHUB_TOKEN`.

See `docs/providers/*.md` and `docs/forge/github.md` for the full adapter
records, exact launch arguments, and event mappings.
