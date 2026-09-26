# Operating yip

This guide covers installing the hub and runners, keeping them running,
remote access, backups, upgrades, and where your data lives.

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
make                                  # or download a release binary
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

## Add machines

1. In **Machines → Add machine**, name the machine. The hub shows a pairing
   command with a single-use token (15 minutes) and the hub's CA fingerprint.
2. On the machine: `yip runner pair --hub https://HUB:7443 --fingerprint sha256:… --token yipe_… --name "Build mini"`.
   The runner generates its private key locally, pins the fingerprint, and
   receives a certificate. The owner-visible machine fingerprint is shown in
   Machines.
3. Sign in to each provider you'll use **with the provider's own tool, as the
   account the runner runs under**: `codex login`, `claude auth login`,
   `agent login`. yip never asks for or stores provider tokens.
4. Run it: `yip runner --providers codex,claude,cursor --slots 2`.
5. Check it: `yip doctor` shows identity, certificate expiry, journal, git,
   and each provider's version and sign-in state.

A project can say what its work needs on a machine (Project → Policy →
*What a machine needs*, e.g. `go, docker` or `os:darwin` for Xcode work).
The runner reports git, go, node, python3, docker, cargo, swift and
xcodebuild; work only goes to machines that have what the project needs, and
otherwise waits with the missing piece named.

Revoke a machine from Machines. It can't regain authority by replaying its
queue; its in-flight runs are marked unknown until reconciled.

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

**Trying the demo on your phone.** `make lan` serves the demo workspace on
your local network over plain HTTP: the built app on port 7721 and the
live-reload development client on 5173. Open `http://<this machine's IP>:7721`
on the phone and sign in with the details in the demo's
`demo-credentials.txt`. The demo engineers are scripted; set
`YIP_LAN_PROVIDERS=codex,claude` (or run `yip demo --with-providers codex,claude`)
to also offer the Codex and Claude Code CLIs already signed in on this
machine — switch an engineer's provider preference to use them, and those
runs bill your own account. It is still plain HTTP; for a
real workspace use one of the options above, which keep the password and
session cookie off the network in clear text.

## Backups and restores

```sh
yip backup --data ~/.yip/hub --out /Volumes/Backups/yip-2026-09-25
yip restore --from /Volumes/Backups/yip-2026-09-25 --data ~/.yip/hub-restored
```

- Backups are **online** (SQLite `VACUUM INTO`, safe while the hub runs) and
  include the database, every artifact, the hub CA, and the hub key — **store
  them encrypted**. Copying only `hub.db` while WAL is active is not a backup.
- Restores go into a **new, empty directory** and verify database integrity,
  every artifact hash, and record counts before you point the hub at it.
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
| Provider credentials | Wherever each provider keeps them on each runner (its own login). yip never reads them. |
| Model requests | Sent by the provider CLI on the runner to that provider. The selected context (conversation excerpts, code) leaves your network when a cloud model is used. |

yip is not end-to-end encrypted against the hub owner; the hub must read
conversations to route work and build context.

## Forge (GitHub)

`yip forge github add` stores a token (read from stdin) sealed with the hub
key. Link a repository to `github` + `owner/name` in the project. Engineers
can read PRs; **publishing a review requires the `publish_review` grant** on
the project, and yip refuses approvals the remote account isn't eligible to
give (a PR author can't approve their own PR). Several engineers sharing one
token are one remote reviewer; internal approvals are shown separately from
remote reviews, checks, and merge state.
