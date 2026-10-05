# OpenCode engineer harness

yip runs the official **OpenCode 1.18.33** CLI as an ACP subprocess. This is
a working engineer adapter, including conversation and review modes, not a
terminal scraper or an API-token extractor.

## Setup on the runner machine

1. Install the official [1.18.33 release](https://github.com/anomalyco/opencode/releases/tag/v1.18.33)
   for your platform and put `opencode` on the runner's `PATH`. With the official
   installer, download and review the script before running it:

   ```sh
   curl -fsSL https://opencode.ai/install -o /tmp/opencode-install.sh
   # Review /tmp/opencode-install.sh, then:
   bash /tmp/opencode-install.sh --version 1.18.33
   export PATH="$HOME/.opencode/bin:$PATH"
   ```
2. As the same OS user and with the same `HOME` and `XDG_DATA_HOME` used by the
   runner, run:

   ```sh
   opencode --version
   opencode auth login
   opencode auth list
   ```

   Choose your model provider and its supported browser/subscription sign-in
   where available. Enter credentials only in OpenCode's local login flow,
   never in yip configuration or chat. An OpenCode installation by itself does
   not include access to every model or subscription.
3. Choose a provider-qualified model ID, such as `provider/model`, in yip.
   `opencode models` is the official local model listing command. yip verifies
   an explicitly requested model against the ACP session's advertised choices
   and confirms model selection before sending the engineer prompt. If omitted,
   OpenCode selects its default from available providers.
4. Use a dedicated runner profile if your usual OpenCode setup includes home
   extensions or organization configuration. The adapter permits the official
   `~/.opencode/bin` installation and inert dependency scaffolding, but refuses
   home extension/configuration files (including scripted dependency manifests),
   system-managed configuration, remote `wellknown` sign-ins, and console
   organizations. It ignores normal user and project configuration by launching
   with a temporary `XDG_CONFIG_HOME` and `OPENCODE_DISABLE_PROJECT_CONFIG=true`.
   It does not remove or rewrite your existing configuration.

The installed version is reported for diagnostics. Other releases can run when
they satisfy the same configuration isolation, ACP protocol, and controlled-agent
requirements.

## Authentication and billing

Probe runs only supported CLI commands: `--version`, `auth list`, and (when
credentials are present) `console orgs`. It never opens credential files.
`ready` means **local provider credentials are configured**, not that a live
model request or subscription entitlement was validated. Zero credentials means
`needs_signin`; unrecognized output and unsafe configuration are not ready. ACP `authenticate` is not used as evidence of sign-in:
the audited implementation returns success without validating credentials.

An API credential anywhere in the reported profile classifies the installation
as API billing, including mixed API/OAuth profiles. OAuth-only profiles classify
as subscription; the CLI reports credential type, not plan entitlements or
remaining quota. Unrecognized credential types retain unknown billing.
No paid model call is made by Probe.

No ambient provider API variables are added to the child environment. The
adapter uses the runner's explicit launch environment, or the common base
allowlist when none is supplied. Configuration/runtime injection environment
variables are rejected rather than allowed to override the controlled agent.
The existing local OpenCode sign-in data is left in place for OpenCode itself.

## Permissions and modes

Each attempt creates a new ACP session using a dedicated `yip` primary agent.
The adapter selects and verifies that agent using `session/set_config_option`.
It never uses the built-in `plan` agent as a read-only guarantee: that agent
can write plan files.

| Capability | Behavior |
| --- | --- |
| Editing | Default deny with native read/search allowed and edits/network requests explicitly requiring approval. Requests carry exact tool metadata and raw audit data; only one-time grants are selected. |
| Conversation / review | Default deny; only native read/glob/grep and the canonical mode-scoped yip bridge tools are allowed. |
| yip MCP | Passed as the sole stdio server in `session/new`, with runner-supplied command, arguments and environment. |
| Read-only bridge calls | Allowed names come from `bridge.NamesForMode`, not a second hardcoded tool catalog. The bridge independently enforces mode, run authority and scope. |
| Shell / native code execution | Denied in every mode. Use the mode-scoped yip `work_run_check` tool for runner-supervised tests/build/lint and yip publication tools for Git publication. Native shell permission requests cannot be approved. |
| Edits and arbitrary tools in read-only | Denied by OpenCode permissions, not merely by instructions. Unexpected permission requests are rejected. |
| Mode changes / subagents | Tasks, plan transitions and interactive questions are disabled. An unexpected agent change fails the run. |
| Messages and tools | ACP text deltas, final text, merged tool starts/finishes, status and vendor session ID are emitted. |
| Usage | Reported turn input/output tokens and USD cost when available; absent usage stays absent. |
| Cancellation | Best-effort `session/cancel` plus independent transport closure and process-group termination; blocked ACP writes cannot prevent shutdown. |
| Resume / active steering | Not implemented; return `ErrUnsupported`. Existing sessions may carry different permissions or previously granted authority. |
| Sandbox | Not claimed. These are native tool permissions, not an OS sandbox or filesystem read boundary. |
| Model enumeration | Not claimed by Probe. Explicit model selection is validated during session setup. |

The filesystem remains read-only for conversation/review tools, while appropriate
room/work/review actions remain available through the mode-scoped bridge.
No blanket permission bypass is passed. Client-side filesystem and terminal
capabilities are not advertised, and unknown ACP client methods are refused.
Language servers and formatters are explicitly disabled in every mode: native
reads can warm repository-controlled language servers, and formatting after an
approved edit could otherwise execute a separate command without runner approval.
Automatic OpenCode Git snapshots are disabled as well; the runner owns revisions.
Native shell, batch/code-execution wrappers and task subagents are unavailable
because OpenCode's detached shell processes can escape the ACP host's process
group. Engineers can still implement code with approved native edits and run
checks through the yip bridge. This is not general OS containment: trusted CLI
internals still perform finite helper operations, and an unexpected report of
native unmanaged execution fails the run without claiming confirmed exit.

Global/project custom providers, custom agents, custom MCP servers and user
plugins are intentionally not loaded. Server-side organization/managed overrides
cannot be safely combined with this policy and are rejected. A local process
with the same OS account remains trusted; this is not protection against hostile
concurrent changes by that account.

## Verification

```sh
go test ./internal/providers/opencode
go test -race ./internal/providers/opencode
```

The fake executable tests cover CLI sign-in summaries and billing, capability
claims, actual ACP subprocess lifecycle, MCP parameters, mode/model selection,
messages/tool events/usage, approvals, one-time-only grants, read-only denial,
unsafe startup configuration, unsupported operations, cancellation, disconnect,
authentication failure and unexpected mode changes.
They also verify that native shells cannot reach approval, bridge checks remain
enabled for edit runs, snapshots/background executors are off, and cancellation
or timeout terminates a peer that stops reading an oversized prompt.

An optional **no-credentials, no-model-call** smoke test uses a supplied official
binary. It creates fresh home/config/data/cache/state directories; it does not
load the operator's credentials or send `session/prompt`:

```sh
YIP_TEST_REAL_OPENCODE=/absolute/path/to/opencode \
  go test ./internal/providers/opencode -run '^TestRealHandshake$' -v
```

This passed with the official 1.18.33 macOS arm64 binary for edit, conversation
and review session creation and controlled-agent selection. The smoke uses a
no-op MCP executable; real bridge protocol behavior is covered by fake-process
parameter assertions, not this smoke. Paid end-to-end inference remains
**unverified**, so Probe does not claim a fully live-tested installation.

## Official references audited

- [CLI](https://opencode.ai/docs/cli/)
- [ACP](https://opencode.ai/docs/acp/)
- [Permissions](https://opencode.ai/docs/permissions/)
- [Configuration](https://opencode.ai/docs/config/)
- [Pinned ACP launch](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/cli/cmd/acp.ts)
- [Pinned ACP lifecycle/config/model/usage](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/acp/service.ts)
- [Pinned permission protocol](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/acp/permission.ts)
- [Pinned provider sign-in/listing](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/cli/cmd/providers.ts)
- [Pinned configuration merging](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/config/config.ts)
- [Pinned configuration discovery](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/config/paths.ts)
- [Pinned agent permission rules](https://github.com/anomalyco/opencode/blob/v1.18.33/packages/opencode/src/agent/agent.ts)
