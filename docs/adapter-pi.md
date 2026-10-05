# Pi Agent Harness adapter

Pi can run yip engineers. It is a local agent harness, **not a separate model
subscription**. The adapter uses Pi's supported Node SDK and your locally
configured underlying provider authentication.

## Install and connect

On the runner machine, install Node.js 22.19 or later and this verified Pi version:

```sh
npm install -g @earendil-works/pi-coding-agent@0.87.1
pi
```

Inside Pi, run `/login` and select the underlying provider. Refresh the runner's
provider status in yip and select a reported `provider/model` in the engineer's
model selector. Pi's interactive `/model` setting is not reused: yip deliberately
does not load Pi settings. An omitted yip model selects the reported default
(the first available model).

The current upstream package is `@earendil-works/pi-coding-agent`. Existing
`@mariozechner/pi-coding-agent@0.73.1` installations are also supported through
their legacy SDK boundary. Other versions can run through the same SDK and tool
restrictions. Wrappers that obscure the npm installation and standalone Pi
binaries are refused.
`Options.Executable`/`StartSpec.Executable` select the installed Pi CLI entry
point; `Options.NodeExecutable` selects Node. A normal npm symlink is supported.

Current account discovery uses `ModelRuntime.create()` with `modelsPath: null`,
`refreshOnCreate: false` and `allowModelNetwork: false`, then the supported
`listCredentials()` non-secret metadata and static `getModels()` APIs. It never
calls current `getAvailable()` or `checkAuth()`: those can resolve configured
API-key shell helpers. Environment-only credentials and custom models are not
enumerated; configure the underlying account through Pi `/login`.
The legacy SDK uses `AuthStorage.create()`,
`ModelRegistry.inMemory(authStorage).getAvailable()` and `isUsingOAuth(model)`;
that version's availability check is local and does not resolve keys.
These are supported SDK APIs. The adapter never opens credential files itself,
extracts keys/tokens, calls `getApiKey`, prints credentials, or sends them to yip.
Pi itself owns credential storage and refresh during execution. Probing does
not refresh OAuth or call any model. “Ready” means authentication is configured
locally, not that credentials are still valid or a particular subscription plan
is active. The next real run validates access.

Current installations report subscription billing only when every configured
model has OAuth metadata and Pi's provider declares subscription access. Any
other configured model makes the installation report API billing conservatively, including mixed
OAuth/API installations. This preserves yip's API-billing consent check. OAuth
classification is not a guarantee of a particular plan or entitlement.
Ambient API keys are not automatically forwarded; configure any intended API
environment explicitly in the runner profile (current discovery still requires
stored Pi authentication). `PI_CODING_AGENT_DIR` can select
the local Pi account directory. Never put credentials in repository files.

## Execution and safety

- A bundled, dependency-free Node host imports the installed official SDK.
  Go and the host exchange LF-delimited JSON over private pipes.
- Sessions use in-memory settings and session storage, a built-in-only model
  registry, and an empty `ResourceLoader`. No project/user extensions, package
  sources, skills, prompt templates, themes, automatic `AGENTS.md` files,
  `models.json` commands, or saved Pi settings are discovered. Yip-approved
  instructions are appended explicitly.
- The SDK receives an explicit allowlist of tool **names**. In both tested versions this
  differs from older documentation showing tool objects. SDK custom tool
  definitions override the corresponding built-ins.
- Read-only and conversation modes expose only `read`, `grep`, `find`, `ls`,
  plus the yip bridge's server-authorized tools. Bash, edit, and write are not
  available. This is real tool restriction, not a prompt instruction, and is not
  an OS filesystem sandbox: reads can access paths available to the process.
- Edit mode adds bash/edit/write wrappers. Every invocation requests runner
  approval for the exact arguments before execution; denial prevents execution.
  There is no blanket permission switch. User authentication configuration is
  trusted local Pi configuration; Pi may use its own configured auth helper
  when obtaining a key for a real run.
- The bash tool uses Pi's supported `BashOperations` override, not its default
  detached-shell backend. `/bin/bash --noprofile --norc` runs in the SDK host's
  supervised process group, including ordinary background jobs after the shell
  exits. `BASH_ENV`/`ENV` startup hooks are removed. A bash timeout ends the whole
  attempt so command descendants cannot continue after a timed-out tool.
- Pi has no native MCP integration. The bundled host starts only the supplied
  yip stdio bridge, negotiates MCP, lists its tools and schemas, and registers
  them as SDK custom tools named `yip_<tool>`. Tool calls use `tools/call`;
  bridge authorization and run-mode policy stay authoritative. No shell-based
  tool invocation or prompt workaround is used.
- Launch environments are allowlisted. `NODE_OPTIONS` and `NODE_PATH` are
  removed so injected preload code cannot bypass the host.
- Assistant deltas, tool start/end events, final text, token totals and Pi's
  estimated cost are normalized. Cached input tokens count toward input totals.
  Estimated cost is not a claim that a subscription incurred that charge.
- Steering is acknowledged by `session.steer()` and reported as **queued**.
  Resume and synchronous provider questions are unsupported; agents can use
  yip's conversation/question tools. No vendor session ID is advertised.
- Cancellation requests SDK abort, then terminates the entire subprocess group,
  including the bridge. Completion also cleans up the group. Exit confirmation
  uses the shared process supervisor and reports uncertainty if descendants
  remain. Temporary host files are removed.

## Verification

```sh
go test -race ./internal/providers/pi
node --test internal/providers/pi/host.test.mjs
```

Go tests launch the actual bundled host against a fake SDK and a fake stdio MCP
process. They cover auth discovery (including mixed billing), execution, bridge
calls, usage/events, approvals/denials, failures, crashes, steering, cancellation,
environment isolation and unsupported boundaries. Node tests verify the actual
tool allowlists, exact-argument approval wrappers and empty resource loader.
No test needs real credentials or a paid model call.

The published 0.87.1 and 0.73.1 SDKs were additionally loaded with in-memory
authentication, settings and session storage to verify actual read-only tool
registration and approval overrides without reading local accounts or making
model calls. To repeat against an independently installed SDK, set
`YIP_PI_TEST_SDK` to its absolute `dist/index.js` path when running the Node tests.
The same variable enables Go lifecycle tests against the actual SDK bash tool:

```sh
YIP_PI_TEST_SDK=/absolute/path/to/pi/dist/index.js \
  go test -race -run TestBashDescendantsStaySupervised ./internal/providers/pi
```

These execute bounded local `sleep` commands, verify the descendant process
group, and check cleanup after normal completion with a background child,
forced host crash, cancellation, and tool timeout. They construct no account
storage or model session. Without the variable, the scenarios still exercise
the production operations override and Go supervisor through a small tool stub.

## Upstream interfaces checked

- [Current SDK guide, v0.87.1](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/docs/sdk.md)
- [Current ModelRuntime](https://github.com/earendil-works/pi/blob/v0.87.1/packages/coding-agent/src/core/model-runtime.ts)
- [Current non-secret credential metadata contract](https://github.com/earendil-works/pi/blob/v0.87.1/packages/ai/src/auth/types.ts)
- [SDK guide, v0.73.1](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/docs/sdk.md)
- [SDK factory and tool-name contract](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/src/core/sdk.ts)
- [Agent session and custom tool override semantics](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/src/core/agent-session.ts)
- [ResourceLoader contract](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/src/core/resource-loader.ts)
- [Authentication API](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/src/core/auth-storage.ts)
- [Model registry and OAuth classification](https://github.com/badlogic/pi-mono/blob/v0.73.1/packages/coding-agent/src/core/model-registry.ts)
- [Current RPC documentation](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md)

The SDK was chosen over stock RPC because yip needs explicit resource ownership,
secure tool wrappers, and a real MCP bridge without relying on discovered
extensions. The host's private JSON protocol is not presented as Pi's RPC API.
