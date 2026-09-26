#!/usr/bin/env python3
"""Disposable hubs that put Machines into the states worth checking.

    python3 scripts/e2e/machines_fixture.py up DIR PORT [--empty] [--bin bin/yip]
    python3 scripts/e2e/machines_fixture.py down DIR

`up` starts a throwaway demo hub whose data lives under DIR (and nowhere
else), on 127.0.0.1:PORT with runners on PORT+23, and leaves it running with:

  * this machine (the demo's own runner): connected as a foreground process,
    with a finished piece of work, an investigation still waiting on a
    question (its checkout is protected), and a reply's scratch space;
  * "Build server": a second real runner, connected as a background service
    (it reports systemd supervision), 4 slots, new work paused, and Claude
    Code "installed but not signed in": a stand-in `claude` script on its
    PATH answers only the probe (--version, --help, auth status) and never
    runs anything, so the runner's own probe reports it needing sign-in;
  * a laptop with a deliberately long name: paired for real, then stopped,
    so it is offline; its last report is edited to show low disk space,
    Claude Code signed in on an untested version without read-only support,
    an allowance pause on that account, and workspaces of every kind (a
    size that is only a lower bound, one that was never measured, and a
    long title);
  * "rack-01": a Linux machine that stopped responding (a database row; it
    never connects), with Codex billed to an API key and the container
    profile.

Everything that is not a real runner report is written into the disposable
database while its hub is stopped (rack-01 is then marked connected but
silent, and the hub's own health check finds it not responding). It prints
BASE=, HANDLE= and PASS= lines for scripts/e2e/run-webkit.sh. `down` stops
exactly the processes `up` recorded.

With --empty it starts a fresh hub with an owner and no machines at all.
"""
import argparse
import http.cookiejar
import json
import os
import signal
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from datetime import datetime, timedelta, timezone

LONG_NAME = "Brayden's travel MacBook Pro (16-inch, 2019) kept in the office drawer"

# Answers the Claude Code adapter's probe (--version, --help with the flags
# it checks for, and `auth status --json`) as an installation that isn't
# signed in. It is not Claude Code and refuses anything else.
STUB_CLAUDE = """#!/bin/sh
case "$1" in
  --version) echo "2.1.282 (Claude Code)" ;;
  --help) echo "--print --output-format --input-format --include-partial-messages --restricted --strict-mcp-config --mcp-config \\
--permission-prompts --permission-prompt-tool --permission-mode --tools --allowedTools --disallowedTools --disable-slash-commands \\
--settings --resume --append-system-prompt --model --verbose" ;;
  auth) echo '{"loggedIn": false}' ;;
  *) echo "machines fixture: a stand-in for yip's probe, not Claude Code" >&2; exit 1 ;;
esac
"""


def ts(dt):
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "Z"


def now():
    return datetime.now(timezone.utc)


def short(id_):
    s = id_.replace("-", "")
    return s[-12:]


class Hub:
    def __init__(self, base, handle, password):
        self.base = base
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.csrf = ""
        self.req("POST", "/v1/session", {"handle": handle, "password": password})
        self.boot = self.req("GET", "/v1/bootstrap")
        self.csrf = self.boot["csrfToken"]

    def req(self, method, path, body=None, ok=(200, 201, 202, 204)):
        r = urllib.request.Request(self.base + path, data=json.dumps(body).encode() if body is not None else None, method=method)
        r.add_header("Content-Type", "application/json")
        r.add_header("Origin", self.base)
        if self.csrf and method != "GET":
            r.add_header("X-Yip-Csrf", self.csrf)
        try:
            with self.op.open(r, timeout=90) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as e:
            raise SystemExit(f"{method} {path}: HTTP {e.code} {e.read()[:300]!r}")

    def room(self, name):
        return next(r for r in self.boot["rooms"] if r["name"] == name)

    def engineer(self, name):
        return next(e for e in self.boot["engineers"] if e["name"] == name)

    def post(self, room, body, mentions=(), thread=None):
        req = {"body": body, "clientKey": str(uuid.uuid4()),
               "mentions": [{"kind": "engineer", "id": self.engineer(m)["id"]} for m in mentions]}
        if thread:
            req["threadId"] = thread
        return self.req("POST", f"/v1/rooms/{self.room(room)['id']}/messages", req)

    def messages(self, room):
        return self.req("GET", f"/v1/rooms/{self.room(room)['id']}/messages?limit=100")["messages"]

    def jobs(self):
        return self.req("GET", "/v1/jobs")

    def job(self, part):
        return next((j for j in self.jobs() if part in j["title"]), None)

    def nodes(self):
        return self.req("GET", "/v1/nodes")

    def node(self, name):
        return next((n for n in self.nodes() if n["name"] == name), None)


def wait(what, cond, timeout=180, every=0.5):
    end = time.time() + timeout
    while time.time() < end:
        try:
            v = cond()
        except (urllib.error.URLError, ConnectionError, OSError):
            v = None
        if v:
            return v
        time.sleep(every)
    raise SystemExit("machines fixture: timed out waiting for " + what)


class Procs:
    """Processes started by `up`, recorded so `down` stops exactly these."""

    def __init__(self, work):
        self.file = os.path.join(work, "pids.json")
        self.pids = {}
        if os.path.exists(self.file):
            with open(self.file) as f:
                self.pids = json.load(f)

    def save(self):
        with open(self.file, "w") as f:
            json.dump(self.pids, f)

    def start(self, name, argv, log, env=None):
        out = open(log, "ab")
        p = subprocess.Popen(argv, stdout=out, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL,
                             env={**os.environ, **(env or {})}, start_new_session=True)
        self.pids[name] = p.pid
        self.save()
        return p.pid

    def stop(self, name, timeout=20):
        pid = self.pids.pop(name, None)
        self.save()
        if not pid:
            return
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            return
        end = time.time() + timeout
        while time.time() < end:
            try:
                os.kill(pid, 0)
            except ProcessLookupError:
                return
            try:
                if os.waitpid(pid, os.WNOHANG)[0] == pid:
                    return
            except ChildProcessError:
                pass
            time.sleep(0.2)
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


def log_has(path, text, after=0):
    try:
        with open(path, "rb") as f:
            f.seek(after)
            return text.encode() in f.read()
    except FileNotFoundError:
        return False


def size(path):
    try:
        return os.path.getsize(path)
    except FileNotFoundError:
        return 0


def start_demo(procs, a, data, log, reset):
    argv = [a.bin, "demo", "--data", data, "--listen", f"127.0.0.1:{a.port}", "--runner-listen", f"127.0.0.1:{a.port + 23}"]
    if reset:
        argv.insert(2, "--reset")
    mark = size(log)
    procs.start("hub", argv, log, env={"YIP_FAKE_DELAY": os.environ.get("YIP_FAKE_DELAY", "300ms")})
    wait("the demo hub", lambda: log_has(log, "runner connected", mark), timeout=90, every=0.25)


def up(a):
    work = os.path.abspath(a.dir)
    os.makedirs(work, exist_ok=True)
    procs = Procs(work)
    base = f"http://127.0.0.1:{a.port}"
    log = os.path.join(work, "hub.log")
    # The demo refuses --reset on a directory not named for the demo.
    data = os.path.join(work, "demo")

    if a.empty:
        mark = size(log)
        procs.start("hub", [a.bin, "hub", "--data", data, "--listen", f"127.0.0.1:{a.port}", "--runner-listen", f"127.0.0.1:{a.port + 23}"], log)
        wait("the setup code", lambda: log_has(log, "setup code", mark), timeout=60, every=0.25)
        with open(log) as f:
            secret = [l for l in f.read().splitlines() if "setup code" in l][-1].rsplit(": ", 1)[1].strip()
        password = "fixture-" + uuid.uuid4().hex[:12]
        r = urllib.request.Request(base + "/v1/setup", method="POST", data=json.dumps(
            {"bootstrapSecret": secret, "orgName": "Empty workspace", "name": "Brayden", "handle": "brayden", "password": password}).encode())
        r.add_header("Content-Type", "application/json")
        r.add_header("Origin", base)
        urllib.request.urlopen(r, timeout=30).read()
        print(f"BASE={base}\nHANDLE=brayden\nPASS={password}")
        return

    start_demo(procs, a, data, log, reset=True)
    creds = dict(l.strip().split(": ", 1) for l in open(os.path.join(data, "demo-credentials.txt")) if ": " in l)
    h = Hub(base, creds["handle"], creds["password"])

    # Work on this machine: a finished fix (with its review snapshot), an
    # investigation waiting on a question, and a reply's scratch space.
    h.post("Security", "@Mira can you fix Atlas accepting expired sessions? A customer reported an old token still worked after logout.", ["Mira"])
    h.post("Reverse engineering", "@Pip how does Beacon retry requests? I want to know before we add the new payments route.", ["Pip"])
    h.post("Engineering", "@Mira could you look into why the nightly Atlas integration suite keeps timing out on the payments reconciliation step since the upgrade?", ["Mira"])
    wait("Pip's question", lambda: next((m for m in h.messages("Reverse engineering") if m["kind"] == "question"), None))
    wait("the Atlas fix", lambda: (h.job("Fix Atlas session expiry") or {}).get("state") in ("completed", "failed"), timeout=240)
    local = h.nodes()[0]
    wait("this machine's workspaces", lambda: (h.req("POST", f"/v1/nodes/{local['id']}/probe", {}) or True) and
         any("nightly Atlas" in (w.get("jobTitle") or "") for w in h.node(local["name"])["workspaces"]), timeout=90, every=2)

    # Two more real runners on this machine, each with its own state. The
    # Build server finds a Claude Code that isn't signed in: a stand-in
    # script that answers only yip's probe and runs nothing.
    rport = a.port + 23
    stub = os.path.join(work, "stub-bin")
    os.makedirs(stub, exist_ok=True)
    with open(os.path.join(stub, "claude"), "w") as f:
        f.write(STUB_CLAUDE)
    os.chmod(os.path.join(stub, "claude"), 0o755)
    runners = {
        "build": ("Build server", "4", {"INVOCATION_ID": "yip-machines-fixture", "PATH": stub + os.pathsep + os.environ.get("PATH", "")}, "fake,claude"),
        "laptop": (LONG_NAME, "2", {}, "fake"),
    }
    for key, (name, slots, env, provs) in runners.items():
        state = os.path.join(work, "runner-" + key)
        if not os.path.exists(os.path.join(state, "identity.json")):
            en = h.req("POST", "/v1/nodes/enrollments", {"name": name})
            subprocess.run([a.bin, "runner", "pair", "--state", state, "--hub", f"https://127.0.0.1:{rport}",
                            "--fingerprint", en["hubFingerprint"], "--token", en["token"], "--name", name],
                           check=True, stdout=subprocess.DEVNULL)
        procs.start("runner-" + key, [a.bin, "runner", "--state", state, "--providers", provs, "--slots", slots],
                    os.path.join(work, f"runner-{key}.log"), env={**env, "YIP_FAKE_DELAY": "300ms"})
    for name, _, _, _ in runners.values():
        wait(name + " to report", lambda: (h.node(name) or {}).get("status") == "online" and (h.node(name) or {}).get("providers"), timeout=90)

    # The laptop goes away for real: it is offline from here on.
    procs.stop("runner-laptop")
    wait("the laptop to go offline", lambda: h.node(LONG_NAME)["status"] == "offline", timeout=60)

    atlas = h.job("Fix Atlas session expiry")
    detail = h.req("GET", f"/v1/jobs/{atlas['id']}")
    review_job = next((j for j in h.jobs() if j["kind"] == "review"), None)
    review_run = None
    if review_job:
        review_run = next((r for r in h.req("GET", f"/v1/jobs/{review_job['id']}")["runs"]), None)
    # Reply jobs aren't listed; their scratch spaces name the run.
    reply_ref = next((w["ref"] for w in h.node(local["name"])["workspaces"] if w["kind"] == "scratch" and "nightly Atlas" in (w.get("jobTitle") or "")), "")
    pip = h.job("Beacon")

    # Stop the hub and edit its disposable database.
    procs.stop("hub")
    db = sqlite3.connect(os.path.join(data, "hub.db"))
    db.execute("PRAGMA busy_timeout = 10000")
    org = db.execute("SELECT id FROM orgs LIMIT 1").fetchone()[0]
    laptop_id = db.execute("SELECT id FROM nodes WHERE name = ?", (LONG_NAME,)).fetchone()[0]
    t = now()

    def ws(name, kind, ref, mb, changes=0, head="", branch="", approx=False, known=True, age=timedelta(hours=5), in_use=False):
        out = {"name": name, "kind": kind, "ref": ref, "changes": changes, "sizeMb": mb, "modifiedAt": ts(t - age), "inUse": in_use,
               "sizeBytes": mb * 1048576 + (123456 if known else 0), "sizeKnown": known}
        if approx:
            out["sizeApprox"] = True
        if head:
            out["head"] = head
        if branch:
            out["branch"] = branch
        return out

    laptop_ws = []
    if atlas and detail["job"].get("revision"):
        laptop_ws.append(ws("job-" + short(atlas["id"]), "job", short(atlas["id"]), 38, head=detail["job"]["revision"]["head"],
                            branch="yip/" + short(atlas["id"]), age=timedelta(hours=4)))
    if review_run:
        laptop_ws.append(ws("review-" + short(review_run["id"]), "review", short(review_run["id"]), 36, age=timedelta(hours=4)))
    if reply_ref:
        laptop_ws.append(ws("scratch-" + reply_ref, "scratch", reply_ref, 0, age=timedelta(hours=3)))
    if pip:
        laptop_ws.append(ws("job-" + short(pip["id"]), "job", short(pip["id"]), 212, changes=3, branch="yip/" + short(pip["id"]),
                            age=timedelta(hours=3, minutes=10)))
    laptop_ws += [
        ws("job-0c7e5a91d2f4", "job", "0c7e5a91d2f4", 2412, changes=2,
           branch="yip/brayden/an-unusually-long-branch-name-for-the-payments-reconciliation-rewrite-and-its-follow-ups", approx=True,
           age=timedelta(days=9)),
        ws("scratch-5b1d0e77a0c3", "scratch", "5b1d0e77a0c3", 0, known=False, age=timedelta(days=12)),
    ]
    db.execute("""UPDATE nodes SET last_seen_at = ?, capacity = ?, service_state = 'launchd service', workspaces = ?, toolchains = ?,
                  last_activity = '' WHERE id = ?""",
               (ts(t - timedelta(hours=3, minutes=12)),
                json.dumps({"slots": 2, "used": 0, "cpus": 16, "memMb": 32768, "diskFreeMb": 1400, "diskPressure": True}),
                json.dumps(laptop_ws),
                json.dumps({"git": "git version 2.39.5 (Apple Git-154)", "go": "go version go1.26.5 darwin/amd64", "node": "v22.3.0",
                            "python3": "Python 3.9.6", "xcodebuild": "Xcode 16.4 Build version 16F6 with an unusually long diagnostic suffix for layout checks"}),
                laptop_id))
    db.execute("DELETE FROM provider_installations WHERE node_id = ?", (laptop_id,))
    claude_caps = {"structuredEvents": True, "toolApprovals": True, "userQuestions": True, "sessionResume": True, "activeSteering": True,
                   "usageTelemetry": True, "sandbox": False, "modelEnumeration": False, "readOnly": False, "mcpTools": True}
    db.execute("""INSERT INTO provider_installations(node_id, provider, version, path, auth_state, auth_detail, account, billing, profile_id,
                  capabilities, models, tested, tested_version, limitations, updated_at) VALUES (?, 'claude', '2.1.3', '/usr/local/bin/claude', 'ready', '',
                  'brayden@example.com', 'subscription', 'claude:brayden@example.com', ?, '[]', 0, '2.0.14', ?, ?)""",
               (laptop_id, json.dumps(claude_caps), json.dumps([
                   "Read-only runs are unavailable: this Claude Code settings file allows Bash(*) without asking, so a review can't be kept read-only.",
                   "Installed Claude Code 2.1.3 differs from the tested version 2.0.14; protocol changes may break the adapter.",
                   "Token usage is reported by Claude Code; dollar cost is not available.",
               ]), ts(t - timedelta(hours=3, minutes=12))))
    db.execute("""INSERT INTO provider_installations(node_id, provider, version, path, auth_state, auth_detail, account, billing, profile_id,
                  capabilities, models, tested, tested_version, limitations, updated_at) VALUES (?, 'codex', '0.125.0', '/opt/homebrew/bin/codex', 'needs_signin',
                  'Sign in with `codex login` on this machine.', '', 'unknown', 'codex:default', '{}', '[]', 1, '0.125.0', '[]', ?)""",
               (laptop_id, ts(t - timedelta(hours=3, minutes=12))))
    db.execute("""INSERT OR REPLACE INTO provider_profiles(id, org_id, provider, label, billing, max_concurrency, created_at, paused_until)
                  VALUES ('claude:brayden@example.com', ?, 'claude', 'brayden@example.com', 'subscription', 2, ?, ?)""",
               (org, ts(t - timedelta(days=3)), ts(t + timedelta(hours=2, minutes=40))))

    rack = str(uuid.uuid4())
    db.execute("""INSERT INTO nodes(id, org_id, name, hostname, os, arch, fingerprint, cert_serial, status, draining, last_seen_at, capacity, profiles,
                  toolchains, runner_version, service_state, last_activity, created_at, workspaces)
                  VALUES (?, ?, 'rack-01', 'rack-01.build.internal', 'linux', 'amd64', '7C2E-91AB-44D0-E613', 'fixture', 'offline', 0, ?, ?, ?, ?, '0.1.0-dev',
                  'systemd service', 'Running go test ./...', ?, '[]')""",
               (rack, org, ts(t - timedelta(minutes=26)),
                json.dumps({"slots": 6, "used": 0, "cpus": 32, "memMb": 131072, "diskFreeMb": 912384, "diskPressure": False}),
                json.dumps([{"name": "container", "available": True, "summary": "Runner inside a restricted Linux container: non-root, workspace volume only, no host Docker socket or home directory."},
                            {"name": "readonly", "available": True, "summary": "Read-only snapshot of an exact revision inside the container."},
                            {"name": "native", "available": False, "reason": "This runner is containerized; native host tooling is not exposed.", "summary": ""}]),
                json.dumps({"git": "git version 2.45.2", "go": "go version go1.26.5 linux/amd64", "docker": "Docker version 27.1.1, build 6312585"}),
                ts(t - timedelta(days=20))))
    codex_caps = {"structuredEvents": True, "toolApprovals": True, "userQuestions": True, "sessionResume": True, "activeSteering": True,
                  "usageTelemetry": True, "sandbox": True, "modelEnumeration": True, "readOnly": True, "mcpTools": True}
    db.execute("""INSERT INTO provider_installations(node_id, provider, version, path, auth_state, auth_detail, account, billing, profile_id,
                  capabilities, models, tested, tested_version, limitations, updated_at) VALUES (?, 'codex', '0.125.0', '/usr/local/bin/codex', 'ready', '',
                  'ci@example.com', 'api', 'codex:ci@example.com', ?, '[]', 1, '0.125.0', '[]', ?)""",
               (rack, json.dumps(codex_caps), ts(t - timedelta(minutes=26))))
    db.commit()
    db.close()

    # Back up: this machine and the Build server reconnect by themselves.
    start_demo(procs, a, data, log, reset=False)
    h = Hub(base, creds["handle"], creds["password"])
    wait("the Build server to reconnect", lambda: (h.node("Build server") or {}).get("status") == "online", timeout=90)
    build = h.node("Build server")
    db = sqlite3.connect(os.path.join(data, "hub.db"))
    db.execute("PRAGMA busy_timeout = 10000")
    # A restart marks every machine offline until it reconnects, so rack-01
    # is made "connected, last heard 26 minutes ago" now; the hub's own
    # health check then finds it not responding.
    db.execute("UPDATE nodes SET status = 'online', last_seen_at = ? WHERE id = ?", (ts(now() - timedelta(minutes=26)), rack))
    db.commit()
    db.close()
    wait("rack-01 to be found not responding", lambda: (h.node("rack-01") or {}).get("status") == "suspect", timeout=60)
    h.req("POST", f"/v1/nodes/{build['id']}/drain", {"drain": True})
    print(f"BASE={base}\nHANDLE={creds['handle']}\nPASS={creds['password']}")


def down(a):
    procs = Procs(os.path.abspath(a.dir))
    for name in list(procs.pids):
        procs.stop(name)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    u = sub.add_parser("up")
    u.add_argument("dir")
    u.add_argument("port", type=int)
    u.add_argument("--empty", action="store_true", help="a fresh hub with an owner and no machines")
    u.add_argument("--bin", default="bin/yip")
    d = sub.add_parser("down")
    d.add_argument("dir")
    a = ap.parse_args()
    if a.cmd == "up":
        if not (7900 <= a.port <= 7999):
            raise SystemExit("choose a port in 7900-7999 for a disposable hub")
        a.bin = os.path.abspath(a.bin)
        try:
            up(a)
        except BaseException:
            down(a)
            raise
    else:
        down(a)


if __name__ == "__main__":
    sys.exit(main())
