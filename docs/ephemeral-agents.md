# Ephemeral Docker agents

`--profile docker` keeps the paired runner on the host but runs each agent
attempt in a fresh Linux container. It also runs `work_run_check` in a separate
container with **no imported harness credentials**. There is no fallback to
native execution if Docker, the image, authentication, or an import fails.

## Setup

Build the image locally, then start a paired runner:

```sh
docker build -f packaging/container/Dockerfile.agent -t yip-agent:local .
yip runner --profile docker --docker-image yip-agent:local
```

For the hub's built-in local runner:

```sh
yip hub --local-runner --local-profile docker --local-docker-image yip-agent:local
```

Choose the **container** execution profile in project policy. This runner
advertises `container` and `readonly`, not `native`. Provider installation,
authentication and toolchain probes run **inside the image**, not against host
executables. An unavailable Docker engine stops startup; it is never treated
as permission to execute on the host.

The supplied image includes pinned `codex` and `claude` CLIs, Git, Go, Node,
and Python. Extend it deliberately for other toolchains, a pinned Cursor CLI,
or local MCP dependencies. Images are never pulled or rebuilt automatically.
Run the runner as a non-root account with access to a local Linux Docker
engine (including Docker Desktop). Remote Docker endpoints are rejected.

## What is shared

- A standalone job clone, not the original checkout, shared replica, or
  another job's Git metadata. The clone is retained between attempts so edits
  and published checkpoints survive.
- Its `.git` directory is a separate **read-only mount**. Agents can inspect
  history but must publish commits with `work_publish_revision`, rather than
  changing Git metadata directly. This prevents agent-controlled Git filters
  or hooks from executing later under the host runner.
- Existing submodule/gitlink entries are not staged or updated by host
  publication/checkpointing, and host Git does not inspect their dirty state.
  Nested-repository changes need a separate job/repository; they are not
  evidence for the parent repository.
- Host Git inspects and stages a private file snapshot, never agent-writable
  nested Git metadata. New embedded repository contents are treated as ordinary
  files, with all `.git` entries removed. Snapshot limits are 100,000 entries,
  128 directory levels, 256 MiB per file and 1 GiB total, including ignored
  content copied before Git evaluates ignores. Exceeding a limit fails closed;
  it never falls back to inspecting the live tree.
- A private, allowlisted snapshot of the selected provider's file-backed
  authentication, skills and MCP configuration. It is mounted read-only,
  copied into a disposable tmpfs home, then removed from the host after worker
  startup. No sessions, histories, caches, hooks, policy rules, SSH keys,
  keychain databases, whole home directories, or runner identity files are
  imported.
- Explicit environment variables selected by name. For example:

  ```sh
  yip runner --profile docker --docker-env CURSOR_API_KEY,MY_MCP_TOKEN
  ```

  Supply their values in the runner's environment, **not in command arguments,
  source files, image layers, or committed configuration**. The corresponding
  hub flag is `--local-docker-env`. Names that override container paths or
  execution controls are rejected. Selecting an API key can change billing
  from a subscription to API usage; no host API keys are silently forwarded.

Skills and imported MCP servers are enabled for editing attempts only.
Conversation/review attempts retain the providers' stricter read-only
configuration and a read-only workspace mount. Native runners retain their
existing configuration restrictions.

## Authentication and configuration limits

File-backed credentials are copied, not moved, and never written back. A
refresh inside the container does not update the host login. Expired
credentials must be refreshed through the provider's supported host login or
explicit token flow, followed by **Machines → Check sign-in again**.

Codex supports copying `CODEX_HOME/auth.json`; its container configuration uses
the file credential store. Existing file-backed credentials for other
harnesses are supported only where the harness understands that file format
on Linux. A macOS Keychain or other OS-credential-store login is **not
automatically portable**. yip does not extract keychain secrets. Use a
provider-supported token/API-key environment variable when no portable login
exists. Cursor requires an explicitly supplied supported authentication
variable; no undocumented credential file is guessed.

Imports respect supported provider config-home overrides, but do not copy
arbitrary settings, hooks, plugins or permission grants. Symlinked imported
files/directories are rejected with an actionable error rather than following
them into unrelated host files. Materialize the intended files in the
selected config directory if needed.

MCP **configuration is portable; arbitrary host processes are not**:

- For `.claude.json`, user-scope servers are imported along with the selected
  host job workspace's local-scope `mcpServers` entry, remapped to `/workspace`. Other
  projects' entries and trust/permission grants are not copied. A local entry
  for a different checkout is not implicitly assigned to a runner job: make
  the intended server user-scoped, or configure it in the job's workspace.
  Provider probes have no selected project and import user scope only.
- A stdio MCP executable, its dependencies and referenced files must exist
  inside the image/workspace. Host absolute paths are not mounted or silently
  rewritten.
- `localhost` addresses refer to the container, not the host. Run local
  servers inside the image or configure an explicitly reachable network
  endpoint. No host-network mode or socket forwarding is enabled.
- MCP authentication embedded in an imported server definition is part of
  the selected provider's configuration. Additional environment-based
  credentials require explicit `--docker-env` names.
- Native provider session IDs are not resumed: session files disappear with
  the container. Retries use yip's conversation context and saved workspace.

## Isolation and lifecycle

Each container runs as the runner's non-root UID, with a read-only root
filesystem, all Linux capabilities dropped, `no-new-privileges`, a private
PID namespace, an init process, and limits of 2 CPUs, 4 GiB memory and 512
processes. Writable temporary storage is bounded. No host home, Docker socket,
SSH agent, runner state directory or host toolchain cache is mounted.

Normal completion, cancellation, failed startup and timeout remove the
container. The runner checks removal at the Docker daemon rather than merely
observing the Docker CLI exit. If removal cannot be confirmed, it reports
uncertain exit, stops scheduling new work, and does not checkpoint or release
the possibly active workspace. Restore Docker connectivity and restart the
runner; startup reaps containers labelled with that paired runner's node ID.

After an abrupt runner crash, a container may remain until the worker sees
stdin close or the runner restarts. Private imports can remain under the
runner state's `container-imports` directory if the process is killed during
startup. Startup removes these abandoned snapshots after reaping the old
containers. Treat runner state as sensitive. `docker ps -a --filter label=dev.yip.runner`
lists managed containers for inspection.

Host-side worker event buffering is capped at 1,024 events and 32 MiB,
including a blocked delivery; inbound worker requests are bounded as well.
Overflow fails and stops the attempt rather than consuming unbounded host
memory or blocking permission replies behind event delivery.

Containers reduce host filesystem/process exposure; they are not a VM or an
absolute security boundary. Default Docker networking permits outbound
traffic, including reachable host/LAN services. Imported credentials are
usable by the agent and its MCP servers, so this mode does **not** prevent
credential exfiltration or harmful remote API actions. Use least-privilege
accounts and an operator-managed firewall/proxy when egress restrictions are
required. Workspace disk usage can also consume host storage.

## Validation

Unit tests cover hardened command construction, cleanup uncertainty,
standalone workspace safety, bounded imports, provider configuration opt-in,
and the bidirectional worker/bridge protocol. To exercise the actual image:

```sh
docker build -f packaging/container/Dockerfile.agent -t yip-agent:local .
YIP_DOCKER_TESTS=1 go test ./internal/runner -run TestDockerImage
```

This opt-in smoke test uses synthetic configuration and no real provider
account. Live authentication and MCP compatibility still need validation with
the exact harness versions and server dependencies you choose.
