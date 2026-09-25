#!/usr/bin/env python3
"""Regenerate the README screenshots from a running `yip demo`.

    ./bin/yip demo --reset --data /tmp/yip-demo-shots --listen 127.0.0.1:7821 --runner-listen 127.0.0.1:7844
    python3 scripts/screenshots/capture.py --hub http://127.0.0.1:7821 --credentials /tmp/yip-demo-shots/demo-credentials.txt

It plays the two demo scenarios through the browser API (the Atlas fix with
peer review, and Pip's Beacon investigation with a question), asks the
Overview for status, then captures each screen with shoot.swift, which renders
pages in the system WebKit (macOS only; no browser download, nothing stored).
Images are written to docs/screenshots/.
"""
import argparse
import http.cookiejar
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


class Hub:
    def __init__(self, base, handle, password):
        self.base = base
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.csrf = ""
        self.req("POST", "/v1/session", {"handle": handle, "password": password})
        self.boot = self.req("GET", "/v1/bootstrap")
        self.csrf = self.boot["csrfToken"]

    def req(self, method, path, body=None):
        r = urllib.request.Request(self.base + path, data=json.dumps(body).encode() if body is not None else None, method=method)
        r.add_header("Content-Type", "application/json")
        r.add_header("Origin", self.base)
        if self.csrf and method != "GET":
            r.add_header("X-Yip-Csrf", self.csrf)
        try:
            with self.op.open(r) as resp:
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

    def job(self, title_part):
        return next((j for j in self.req("GET", "/v1/jobs") if title_part in j["title"]), None)


def wait(what, cond, timeout=240):
    end = time.time() + timeout
    while time.time() < end:
        v = cond()
        if v:
            return v
        time.sleep(1.5)
    raise SystemExit("timed out waiting for " + what)


def populate(h):
    if h.job("Fix Atlas session expiry") is None:
        h.post("Security", "@Mira can you fix Atlas accepting expired sessions? A customer reported an old token still worked after logout.", ["Mira"])
        h.post("Reverse engineering", "@Pip how does Beacon retry requests? I want to know before we add the new payments route.", ["Pip"])
        q = wait("Pip's question", lambda: next((m for m in h.messages("Reverse engineering") if m["kind"] == "question"), None))
        h.post("Reverse engineering", "It's in the beacon-worker repo, under worker/retry.go. It isn't linked to this project yet.", thread=q["id"])
    done = ("completed", "failed")
    wait("the Atlas fix", lambda: (h.job("Fix Atlas session expiry") or {}).get("state") in done)
    wait("the Beacon investigation", lambda: (h.job("Beacon") or {}).get("state") in done)
    if not any(m["author"]["kind"] == "user" for m in h.messages("Overview")):
        h.post("Overview", "What got done today, and is anything waiting on me?")
        time.sleep(3)
    # Open rooms at their latest messages rather than an unread divider.
    for r in h.boot["rooms"]:
        ms = h.req("GET", f"/v1/rooms/{r['id']}/messages?limit=100")["messages"]
        if ms:
            h.req("POST", f"/v1/rooms/{r['id']}/read", {"seq": max(m["seq"] for m in ms)})


BOTTOM = "const s = document.querySelector('.scroller'); if (s) s.scrollTop = s.scrollHeight;"
TO_QUESTION = ("const el = [...document.querySelectorAll('.scroller p, .scroller div')]"
               ".filter(e => e.textContent.includes('which repository')).pop();"
               "if (el) el.scrollIntoView({block: 'start'});"
               "const s = document.querySelector('.scroller'); if (s) s.scrollTop -= 60;")


def shots(h):
    sec, rev = h.room("Security")["id"], h.room("Reverse engineering")["id"]
    atlas = h.job("Fix Atlas session expiry")["id"]
    review = h.req("GET", f"/v1/jobs/{atlas}")["reviews"][0]["id"]
    question = next(m for m in h.messages("Reverse engineering") if m["kind"] == "question")["id"]
    mira = h.engineer("Mira")["id"]
    desk = {"width": 1600, "height": 1000, "scale": 1.25}
    return [
        {"name": "room-job-evidence", "path": f"/rooms/{sec}?panel=job:{atlas}&tab=evidence", "js": BOTTOM, **desk},
        {"name": "review-rounds", "path": f"/rooms/{sec}?panel=review:{review}", "js": BOTTOM, **desk},
        {"name": "question-thread", "path": f"/rooms/{rev}?panel=thread:{question}", "js": BOTTOM, **desk},
        {"name": "overview", "path": "/overview", **desk},
        {"name": "engineer", "path": f"/engineers/{mira}", **desk},
        {"name": "machines", "path": "/machines", **desk},
        {"name": "room-job-evidence-dark", "path": f"/rooms/{sec}?panel=job:{atlas}&tab=evidence", "js": BOTTOM, "dark": True, **desk},
        {"name": "mobile-room", "path": f"/rooms/{rev}", "js": TO_QUESTION, "width": 390, "height": 844, "scale": 2},
    ]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--hub", default="http://127.0.0.1:7821")
    ap.add_argument("--credentials", required=True, help="demo-credentials.txt written by yip demo")
    ap.add_argument("--out", default=os.path.join(ROOT, "docs", "screenshots"))
    a = ap.parse_args()
    creds = dict(l.strip().split(": ", 1) for l in open(a.credentials) if ": " in l)
    h = Hub(a.hub, creds["handle"], creds["password"])
    populate(h)
    with tempfile.TemporaryDirectory() as tmp:
        tool = os.path.join(tmp, "shoot")
        subprocess.run(["swiftc", "-O", "-swift-version", "5", "-o", tool, os.path.join(ROOT, "scripts", "screenshots", "shoot.swift")], check=True)
        cfg = os.path.join(tmp, "shots.json")
        with open(cfg, "w") as f:
            json.dump({"base": a.hub, "handle": creds["handle"], "password": creds["password"], "out": a.out, "shots": shots(h)}, f)
        subprocess.run([tool, cfg], check=True)


if __name__ == "__main__":
    sys.exit(main())
