"""Helpers for the real-provider campaigns: a disposable `yip hub` with a local
runner, its one-time owner setup, and a small browser-API client."""
import http.cookiejar
import json
import os
from pathlib import Path
import re
import secrets
import signal
import subprocess
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
SETUP_CODE = re.compile(rb"One-time setup code \(expires [^)]*\): (\S+)")


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
    raise SystemExit("timed out waiting for " + what)


class Procs:
    """Processes a campaign started, recorded so it stops exactly these."""

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


def start_hub(procs, data, log, port, runner_port, providers, credentials):
    """Starts `yip hub` on 127.0.0.1 with a local runner that uses the given
    providers' existing sign-ins. A fresh hub is set up with an owner whose
    sign-in is saved to the credentials file; the local runner pairs once
    that owner exists."""
    base = f"http://127.0.0.1:{port}"
    mark = size(str(log))
    procs.start("hub", [str(ROOT / "bin" / "yip"), "hub", "--data", str(data),
        "--listen", f"127.0.0.1:{port}", "--runner-listen", f"127.0.0.1:{runner_port}",
        "--local-runner", "--local-providers", providers], str(log),
        env={"PATH": str(ROOT / "bin") + os.pathsep + os.environ["PATH"]})
    if not Path(credentials).exists():
        setup_owner(base, log, mark, credentials)
    wait("the hub's local runner", lambda: log_has(str(log), "runner connected", mark), timeout=90)
    return base


def setup_owner(base, log, after, credentials, name="Brayden", handle="brayden"):
    def code():
        with open(log, "rb") as f:
            f.seek(after)
            m = SETUP_CODE.search(f.read())
        return m and m.group(1).decode()
    password = "campaign-" + secrets.token_urlsafe(12)
    r = urllib.request.Request(base + "/v1/setup", method="POST", data=json.dumps({
        "bootstrapSecret": wait("the one-time setup code", code, timeout=30),
        "orgName": name + "'s workspace", "name": name, "handle": handle, "password": password,
    }).encode())
    r.add_header("Content-Type", "application/json")
    r.add_header("Origin", base)
    with urllib.request.urlopen(r, timeout=30):
        pass
    Path(credentials).write_text(f"handle: {handle}\npassword: {password}\n")
    os.chmod(credentials, 0o600)


def read_credentials(path):
    return dict(line.strip().split(": ", 1) for line in Path(path).read_text().splitlines() if ": " in line)


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

    def nodes(self):
        return self.req("GET", "/v1/nodes")
