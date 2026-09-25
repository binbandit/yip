# yip

**Your agents. Your machines. One room to work together.**

yip is a self-hosted workspace for your AI engineering team. You create rooms,
bring the same engineers (Mira, Oren, Pip — or whoever you configure) into
different conversations, and ask them to investigate, build, review, and
explain. Work runs on machines you choose; closing the laptop doesn't stop it.

What makes it different is that **conversation, accountable work, and
evidence are one loop**:

- A message can start a durable **job** with an owner, an explicit repository
  scope, and a completion policy.
- Engineers **own the work**: they make routine decisions, choose a colleague
  for **peer review**, address findings, and get the revised work re-reviewed —
  without you dispatching or relaying anything.
- **Done means evidence**: a published revision (diff + portable bundle),
  runner-executed checks bound to that exact revision, and an independent
  approval of the final head. A confident paragraph isn't completion.
- Genuine questions arrive as **ordinary messages in the room**; only the
  dependent step waits. There is no "needs you" inbox and no default
  acceptance click.
- Everything is **truthful about uncertainty**: a lost machine yields
  "outcome not confirmed", never a silent retry; steering says whether your
  update was delivered now or queued.

The product documents that specify it live in [`docs/spec/`](docs/spec/README.md).

## Quick start (demo)

The demo seeds a workspace whose engineers run on yip's **deterministic fake
provider** — no model is called, and the UI says so. It exercises the whole
pipeline: hub, runner over mutual TLS, git worktrees, the MCP bridge, reviews,
questions, steering, and recovery.

```sh
make            # builds the web client (npm) and the yip binary
./bin/yip demo  # prints the URL and demo credentials
```

Then, in **Security**, send `@Mira can you fix Atlas accepting expired
sessions?` and watch Mira fix it, pick Oren to review, get a real defect found
in the refresh path, fix that, and complete after re-review. In **Reverse
engineering**, ask `@Pip how does Beacon retry requests?` to see a genuine
question in the room and a resumed job after you reply in its thread.

## Real use

```sh
yip hub                          # first run prints a one-time setup code
# Machines → Add machine shows a pairing command; on each machine:
yip runner pair --hub https://HUB:7443 --fingerprint sha256:… --token yipe_…
yip runner --providers codex,claude,cursor
yip service install runner       # launchd (macOS) or systemd (Linux)
```

Sign in to each provider **with its own tool on each runner** (`codex login`,
`claude auth login`, `agent login`). yip never collects, stores, or proxies
provider credentials. See [docs/operations.md](docs/operations.md).

## Status

| Area | State |
|---|---|
| Hub (rooms, routing, jobs, runs, reviews, questions, approvals, decisions, scheduler, SSE, auth) | Implemented; covered by unit and in-process integration tests |
| Runner (pairing, mTLS, journal, leases, worktrees, snapshots, checks, revisions, bridge) | Implemented; exercised by the integration suite on one machine |
| Deterministic fake provider | Implemented (demo + failure injection) |
| Codex, Claude Code, Cursor adapters | Implemented against pinned interfaces; **verified against scripted fakes only — no real-account smoke test has been run** (see [compatibility](docs/compatibility.md)) |
| GitHub connector | Implemented; tested against an emulated API; real contract test not run |
| Web client | See [docs/design/README.md](docs/design/README.md) |
| Two physical machines | Protocol is multi-machine; tested with one hub and one runner per test on a single host |

The release gates (A01–A44) and what remains are tracked in
[docs/release-checklist.md](docs/release-checklist.md). Departures from the
spec are recorded in [docs/decisions/](docs/decisions/).

## Layout

```
cmd/yip/                 hub, runner, bridge, doctor, backup, restore, service, schema
internal/hub/            canonical state: routing, jobs, runs, reviews, approvals, scheduler, tools
internal/runner/         runner: journal, leases, workspaces, execution, local tools
internal/bridge/         agent-facing MCP tools (stdio server) and runner socket
internal/providers/      adapter contract + codex, claude, cursor, fake
internal/forge/github/   GitHub PR/review connector
internal/context/        context manifests and scope fingerprints
internal/store/          SQLite schema, migrations, repositories
internal/httpapi/        browser API, SSE, runner listener, embedded web client
protocol/                wire types + generated JSON Schemas
web/                     Svelte 5 + TypeScript client
test/integration/        hub + runner + bridge + fake provider scenarios
packaging/               launchd/systemd units, container runner profile
docs/                    spec, operations, compatibility, API, decisions, checklist
```

## Development

```sh
go test ./...            # unit + integration (≈1 min)
make schema              # regenerate JSON Schemas and web types from protocol/*.go
cd web && npm run dev    # client dev server (proxy /v1 to a running hub)
```

Requirements: Go 1.26, Node 20+ (build only), git on every runner.
