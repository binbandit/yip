# Operating yip

This guide covers installing the hub and runners, keeping them running,
remote access, backups, upgrades, and where your data lives.

For provider setup, start with [Connections](#connections): choose a tool,
connect a machine, and check its local sign-in before assigning an engineer.

## Topology

- **Hub** — one always-on machine. It serves the web client and API, owns the
  SQLite database and artifacts, schedules work, and runs the runner listener.
  It never executes provider CLIs itself.
- **Runners** — each machine that does work (it can be the hub machine too).
  A runner connects **outbound** to the hub over mutual TLS, runs provider
  CLIs in per-job git worktrees, and reports events and evidence.
- **Clients** — any browser. Closing it has no effect on running work.

If the hub machine sleeps or powers off, scheduling and approvals pause.
There is no hidden cloud failover.

## Install the hub

```sh
just                                  # or download a release binary
yip hub --data ~/.yip/hub             # defaults: web on 127.0.0.1:7420, runners on :7443
```

On first start the hub prints a **one-time setup code** (valid 30 minutes).
Open the web address, enter the code, and create the owner. If the code
expires, run `yip hub setup-code`. There is no unauthenticated setup route.

Useful flags:

| Flag | Purpose |
|---|---|
| `--listen` | Browser/API address. Loopback by default. |
| `--runner-listen` | Runner listener (always TLS with the hub's own CA). |
| `--runner-url` | The `https://host:port` runners should use (shown in pairing commands). |
| `--runner-hosts` | Extra hostnames/IPs for the runner listener certificate. |
| `--tls-cert/--tls-key` | Serve the browser API over HTTPS directly. |
| `--secure-cookies` | Mark cookies Secure (use behind an HTTPS proxy). |
| `--allowed-origin` | Extra origin for state-changing requests (e.g. a Tailscale name). |
| `--local-runner` | Also run a runner on the hub machine, paired through the normal path. |

### In Docker

Build once on your machine, then reuse the image:

```sh
just docker-build             # build web + Linux binary locally; package yip-hub:local
just docker-run               # run the existing image; no compilation or image pull
just docker-run -d            # or in the background
just docker down              # stop it; the volume keeps your workspace
```

The build needs local Go (the version in `go.mod` or newer), Node 20.19+ or
22.12+, npm, just and Docker. It uses your normal local Go configuration,
including exported `GOPROXY`, `GOPRIVATE`, `GONOPROXY`, `GONOSUMDB`, `GOSUMDB`
and settings saved with `go env -w`. Nothing needs exporting again if your
local Go build already works. Go settings, module caches and credentials
are not copied into the image or passed as Docker build arguments.

`docker-build` embeds the freshly built web assets and cross-compiles with
`GOOS=linux CGO_ENABLED=0`; it never copies your native Mac executable. It
selects amd64 or arm64 from the active Docker daemon (including a remote
daemon). Set `DOCKER_DEFAULT_PLATFORM=linux/amd64` or `linux/arm64` to choose
explicitly; running a different architecture requires Docker's emulation
support. Docker still needs to fetch the Debian runtime image and packages
on its first build. A failed local build stops; it does not fall back to a
container build or replace the previous image.

Repeat `just docker-build` after source updates, then `just docker-run -d`
to use the new image. Ordinary starts need neither Go nor Node. If the local
image is missing, `docker-run` fails until you build it.

Alternatively, build entirely inside Docker with only Docker and just:

```sh
just docker                   # build and run it; prints the one-time setup code
just docker up --build -d     # or in the background, then: just docker logs hub
just docker down              # stop it; the volume keeps your workspace
```

Without just, run `docker compose -f packaging/container/compose.hub.yml up --build`.
This uses `Dockerfile.hub` and the separate `yip-hub` image; its Go compiler
runs inside Docker and does not inherit the host's Go configuration. Both
workflows use the same Compose service and persistent `hub-data` volume,
running as a non-root user with no capabilities.

- The web client is at `http://localhost:7420`, published on this machine's
  loopback only. A container's own loopback can't be published, so inside it
  the hub listens on every interface with `--insecure-http` and logs that
  warning; the loopback-only publish keeps the password and session cookie on
  this machine. To reach it from elsewhere, use Tailscale Serve on the host
  (below) or mount a certificate and add `--tls-cert`/`--tls-key` to the
  compose `command`.
- Runners pair over port 7443, published on every interface (mutual TLS).
  `just docker` puts `https://<this machine's hostname>:7443` in pairing
  commands, as a native hub would; set `YIP_RUNNER_URL` to override it (plain
  `docker compose` defaults to `https://localhost:7443`, which only runners on
  this machine can reach). Runners, including the container runner below, run
  outside the hub's container.
- Operator commands run inside it: `just docker exec hub yip hub setup-code`,
  `just docker exec hub yip owner reset-password --handle <you>`, or
  `just docker exec hub yip backup --out /var/lib/yip/backup.yipenc --encrypt`
  followed by `just docker cp hub:/var/lib/yip/backup.yipenc .` to copy it out.
- To upgrade, take a backup, update the checkout, and run
  `just docker-build && just docker-run -d` (or `just docker up --build -d`
  for the container build); migrations run on start.

## Add machines

1. In **Machines → Add machine**, name the machine. The hub shows a pairing
   command with a single-use token (15 minutes) and the hub's CA fingerprint.
2. On the machine: `yip runner pair --hub https://HUB:7443 --fingerprint sha256:… --token yipe_… --name "Build mini"`.
   The runner generates its private key locally, pins the fingerprint, and
   receives a certificate. The owner-visible machine fingerprint is shown in
   Machines.
3. Sign in to each provider you'll use **with the provider's own tool, as the
   account the runner runs under**: `codex login`, `claude auth login`,
   `agent login`. yip never asks for or stores provider tokens. For private
   GitHub repositories, also `gh auth login` as that account
   ([Adding a repository](#adding-a-repository)).
4. Run it: `yip runner --slots 2`. All supported real tools are enabled by
   default; use `--providers` only to restrict which ones this machine may use.
5. Check it: `yip doctor` shows identity, certificate expiry, journal, git,
   and each provider's version and sign-in state.

A project can say what its work needs on a machine (Project → Policy →
*What a machine needs*, e.g. `go, docker` or `os:darwin` for Xcode work).
The runner reports git, go, node, python3, docker, cargo, swift, xcodebuild
and gh; work only goes to machines that have what the project needs, and
otherwise waits with the missing piece named.

Workspaces (a git worktree per piece of work, plus review snapshots) are
never deleted automatically. **Machines → Workspaces** lists each with its
work, size, and whether deleting it loses anything; *Delete…* states the
consequence first, needs a second confirmation when uncommitted or
unpublished work would be lost, and is refused while the work is open or an
attempt is using it. The machine removes the files itself. The same is
available on the machine as `yip runner workspaces` / `yip runner cleanup`.

Revoke a machine from Machines. It can't regain authority by replaying its
queue; its in-flight runs are marked unknown until reconciled.

### Reconnect a revoked machine

Revocation intentionally leaves the runner's local files intact. To reconnect:

1. Stop the runner process or its service. For the standard macOS LaunchAgent,
   use `launchctl bootout gui/$(id -u)/dev.getyip.runner`.
2. In **Machines → Add machine**, create a fresh enrollment token and copy its
   pairing command. Add `--replace`, retaining the same `--state` directory if
   you use one: `yip runner pair --replace --hub … --fingerprint … --token …`.
3. Start the runner again with its usual command. For the standard macOS
   LaunchAgent, use `launchctl bootstrap gui/$(id -u) "$HOME/Library/LaunchAgents/dev.getyip.runner.plist"`.

This enrolls a new machine identity; it does not re-enable the revoked one.
The fresh identity uses its own `identity-*` directory beneath the existing
state directory. Previous credentials, journal, replicas and workspaces stay
at their original paths, preserving unpublished work and Git worktree links.
They are not replayed or exposed through the new identity. The previous
identity metadata is saved as `previous-node.json` in the new directory.
Invalid tokens, a wrong hub fingerprint or invalid returned credentials leave
the active pairing unchanged. Replacement is refused while the runner is
active; keep the service stopped until pairing finishes.

Keep using the original state directory when starting the runner; generated
`identity-*` directories are storage, not independent runner roots. If the old
identity used Docker agents, or Docker is discoverable during replacement or
startup, startup first removes containers carrying its node label, including
across repeated replacements. Keep the local Docker
engine available for this cleanup even when switching to native execution.
If inspection or removal cannot be confirmed, startup stops before accepting
work. Other runners' containers and unpublished workspace files stay intact.
Disposable `container-imports` copies from previous identities are removed
only after their containers are confirmed gone.

For pre-upgrade Docker runners, keep `docker` on the pairing or service PATH:
older toolchain-only probes did not leave a local usage marker, so their
containers cannot be detected if Docker is absent from both environments.
Preserved historical work remains inspectable with `yip runner workspaces
--state <old identity directory>` and removable with `yip runner cleanup
--state <old identity directory> --workspace <name> --confirm <name>`. These
commands do not start that identity or replay its journal.

## Connections

Open **Connections** (`/connections`) and choose a tool. Its guide (for
example `/connections/codex`) walks through:

1. Select a connected machine or pair one with **Add machine**.
2. Install the official CLI on that machine, following the guide's link.
3. Sign in locally with that CLI **as the OS user running the runner**.
   Provider credentials stay on the machine; they are never uploaded to yip.
4. Choose **Check connection** and wait for a fresh, asynchronous machine
   report. Sending the check is not confirmation of a successful sign-in.
5. Choose the provider on an engineer's profile once the report is ready.

The stock adapters include the tools listed above, **OpenCode**, and
**Pi Agent Harness**. A harness is not itself a model subscription: connect
an eligible model account inside it, then choose that harness and its model
on your engineer's profile. Cursor remains experimental.

| Tool | Sign in on the runner machine | Choose in yip |
|---|---|---|
| OpenCode | `opencode auth login`, then select the model provider | OpenCode and a `provider/model` |
| Pi Agent Harness | Start `pi`, run `/login`, and select the model provider | Pi Agent Harness and a `provider/model` |

The runner enables both harnesses by default. If you set `--providers`
explicitly, add `opencode,pi` to your existing list and restart it. For an
embedded runner, update the hub's `--local-providers` list instead. The
demo keeps real accounts disabled unless you opt in with
`yip demo --with-providers opencode,pi`.

Billing is reported separately from sign-in: **unknown is not a confirmed
subscription**. API billing requires the engineer's explicit
**Allow runs billed to an API key** opt-in.
When a harness has both subscription and API accounts configured, yip
conservatively requires that opt-in for the whole harness connection.

Runners probe every five minutes or on a manual check. Signing in or
installing a CLI on the runner's existing `PATH` needs a new probe, not a
restart. Changing `PATH` or the enabled provider list requires restarting
the runner with the updated environment or `--providers` option. For the
embedded local runner, use `yip hub --local-runner --local-providers …` and
restart the hub instead.

## Adding a repository

**Projects → New project** (or **Add repository** on a project) takes just a
GitHub `owner/name`, such as `acme/atlas`. The hub turns it into
`https://github.com/acme/atlas.git`, links it to GitHub as `acme/atlas`,
names it `atlas`, and asks GitHub for its default branch. A full URL works
too: `https://github.com/acme/atlas`, `git@github.com:acme/atlas.git`, or any
other git remote your machines can clone (name and branch then default to the
URL's last segment and `main`).

**Private GitHub repositories** need no token in yip, only the
[GitHub CLI](https://cli.github.com) signed in (`gh auth login`) as an account
that can see them:

- **On the hub's machine**, to look the repository up. Without `gh` the hub
  asks the GitHub API anonymously (or with the stored forge credential),
  which can't see a private repository; enter its default branch to add it
  anyway.
- **On each machine that runs its work**, to clone it. For a github.com HTTPS
  remote the runner uses `gh auth git-credential` as the git credential
  helper, as `gh auth setup-git` would, but only for that repository's copy:
  its fetches, and pushes an engineer is granted or you approve, use the same
  sign-in. Credential helpers you already configured still run first. Checks
  get no credential helper at all (see below). If a clone is refused, the
  error says to sign in or check `gh auth status` on that machine. Add `gh` to
  *What a machine needs* to keep the work on machines that have it.

An SSH remote (`git@github.com:…`) uses the machine's SSH keys instead.

## Code with no remote

A repository your machines can't reach (a folder on your laptop, a remote
behind a VPN) can be imported instead: in the folder run
`git bundle create repo.bundle --all`, then **Project → Import from a
folder**. Only committed work is included. Machines build their copy from
the bundle and engineers publish revisions to yip as usual; there is no
remote to push to. **Import a newer bundle** refreshes it; **Add a remote**
switches the repository to a remote.

## Keep it running

```sh
yip service install hub       # or: runner
```

This writes a launchd agent (macOS) or systemd user unit (Linux) and prints
the command that activates it; review with `--print`. Examples of
system-level units for a dedicated account are in `packaging/systemd/`.

After installing, **test restart after a process failure and after a reboot
under the actual account**. On macOS with FileVault, a LaunchAgent only starts
after the disk is unlocked and the user logs in; `yip doctor` and the Machines
page report the real service state instead of promising uptime.

### What checks can reach

Engineers' checks (`work_run_check`) run on the runner's machine, in the
job's worktree, after the permission policy allows them (routine tests and
builds proceed; pushes, publication, network access, and anything the policy
can't inspect ask you first). They run with a scratch home directory and no
SSH agent, git credential helper, or per-user tool configuration, so a test
suite can't use your keys or tokens. Go, npm, and Cargo caches are shared.
A suite that genuinely needs credentials (for example a private module proxy
over SSH) will fail there; give it explicit configuration in the container
profile instead. The runner's own git operations never run repository hooks.

This is a boundary for credentials, not a sandbox: a check runs code from the
workspace with your account's file access. Use the container profile for
projects you don't trust.

### Container execution profile

For a fresh agent container per attempt, build
`packaging/container/Dockerfile.agent` and start the host runner with
`--profile docker`. It imports allowlisted file-backed harness authentication,
skills and MCP configuration into a disposable home, mounts only the job
workspace, and runs checks in credential-free containers. See
[Ephemeral Docker agents](ephemeral-agents.md) for setup, supported imports,
keychain limitations, cancellation and the security boundary.

`packaging/container/` builds a runner image and a compose file that runs it
non-root with a read-only root filesystem, one workspace volume, no host
Docker socket or home directory, no new privileges, and resource limits. A
runner started with `--profile container` advertises the `container` profile;
projects whose policy requires it are only scheduled there. Provider CLIs are
not baked in — extend the image with a pinned, tested version. Restrict egress
with a firewall or proxy; compose cannot express an allowlist.

## Remote access

Keep the browser API on loopback and reach it through a private network:

- **Tailscale Serve** (optional): `tailscale serve --bg 7420`, then start the
  hub with `--secure-cookies --allowed-origin https://hub.your-tailnet.ts.net`.
- **Direct TLS**: `--listen 0.0.0.0:7420 --tls-cert … --tls-key …`.

A non-loopback `--listen` without TLS is refused unless you pass
`--insecure-http`, which is only for a trusted local network (the password
and session cookie cross it unencrypted) and is logged as a warning.

yip never opens ports or sets up port forwarding by itself. Runners need to
reach the runner listener (default 7443) on the hub.

## Backups and restores

```sh
yip backup --data ~/.yip/hub --out /Volumes/Backups/yip-2026-09-25.yipenc --encrypt
yip restore --from /Volumes/Backups/yip-2026-09-25.yipenc --data ~/.yip/hub-restored
```

- Backups are **online** (SQLite `VACUUM INTO`, safe while the hub runs) and
  include the root and every committed workspace's database, artifacts and hub
  key, plus one copy of the shared root CA. Use the installation root for
  `--data`; hidden workspace staging directories are skipped. Each database
  has a consistent snapshot, not a single transaction across all workspaces.
  Copying only `hub.db` while WAL is active is not a backup.
- `--encrypt` writes one file encrypted with your passphrase (Argon2id key,
  AES-256-GCM in authenticated chunks, so a wrong passphrase, an altered
  file, or a truncated one is refused). The passphrase comes from
  `--passphrase-file`, `YIP_BACKUP_PASSPHRASE`, or is asked for twice; it is
  at least 12 characters, and without it the backup can't be restored. The
  backup is staged unencrypted in a private temporary directory while it's
  verified and packed, then removed. Without `--encrypt` you get a plain
  directory: keep it on an encrypted volume.
- Restores go into a **new, empty directory** and verify database integrity,
  every artifact hash, and record counts for the root and each workspace before
  you point the hub at it. Older root-only backups remain supported.
- Provider sign-ins are never in backups; re-establish them on runners.

## Upgrades

1. `yip backup` first.
2. Drain runners (Machines → Drain) so no new work starts; let active work
   finish or stop it deliberately.
3. Replace the binaries and restart. Migrations run on start. Runners with an
   incompatible protocol version are refused with a clear message — upgrade
   hub and runners together.
4. Never upgrade a provider CLI under an active session. Pin the versions
   listed in [compatibility.md](compatibility.md).

Upgrading from a build that had `yip demo`: the demo and its scripted
provider are gone (ADR 0004). Demo data in `~/.yip/demo` is no longer used and
can be deleted. On a real hub, engineers still set to the scripted provider
keep that setting, and their work waits with "… is set to a provider this hub
doesn't support" until you choose Codex, Claude Code or Cursor on their
profile. Queued work then runs on the new choice.

## Diagnostics

- `yip doctor` on any machine.
- Settings → Diagnostics shows queue depth, active runs, pending outbox
  commands, pending approvals, recent failures, storage, and machine health.
- **Prepare a diagnostic bundle** (Settings → Diagnostics) shows, in full,
  a JSON file of counts, health, versions, machines and their providers'
  sign-in and billing state, and the last 20 failure reasons (redacted). It
  leaves out messages, prompts, code, account names, hostnames, fingerprints
  and credentials. It is saved only when you choose to, and never sent
  anywhere (`GET /v1/diagnostics/bundle`).
- `YIP_DEBUG=1` enables debug logging. Nothing is sent to third-party
  telemetry. `GET /v1/export` is the full data export (it does include your
  conversations).

## Owner recovery

On the hub host: `yip owner reset-password --handle <you>`. This revokes every
existing session.

## Where your data lives

| Data | Location |
|---|---|
| Conversations, jobs, reviews, decisions, audit log | Hub: `<data>/hub.db` (SQLite, readable by the hub owner; use disk encryption) |
| Artifacts (diffs, bundles, logs, documents) | Hub: `<data>/artifacts/<sha256>` |
| Hub CA and runner-listener identity | Hub: `<data>/pki/` |
| Hub key (seals forge credentials) | Hub: `<data>/hub.key` |
| Forge credentials (e.g. a GitHub token) | Hub database, sealed with the hub key |
| Code and worktrees | Each runner: `<state>/replicas/`, `<state>/work/` — never deleted automatically; `yip runner workspaces` / `yip runner cleanup` |
| Runner journal | Each runner: `<state>/journal.db` |
| Provider credentials | Managed by the provider on each runner. Opt-in `--profile docker` copies allowlisted file-backed credentials into a disposable container home; it never extracts OS-keychain credentials or writes refreshed credentials back. |
| Model requests | Sent by the provider CLI on the runner to that provider. The selected context (conversation excerpts, code) leaves your network when a cloud model is used. |

yip is not end-to-end encrypted against the hub owner; the hub must read
conversations to route work and build context.

## Forge (GitHub)

`yip forge github add` stores a token (read from stdin) sealed with the hub
key; `yip forge github add --from-gh` stores the token the GitHub CLI is
signed in with on the hub's machine (`gh auth token`), which reaches the same
private repositories that account can. A repository added by `owner/name` or
a GitHub URL is linked to `github` + `owner/name` already. Engineers
can read PRs; **publishing a review requires the `publish_review` grant** on
the project, and yip refuses approvals the remote account isn't eligible to
give (a PR author can't approve their own PR). Several engineers sharing one
token are one remote reviewer; internal approvals are shown separately from
remote reviews, checks, and merge state.
