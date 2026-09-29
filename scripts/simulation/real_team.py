#!/usr/bin/env python3
"""Bounded, real Codex author + Claude reviewer through a disposable yip hub.

Runs only with --run-real-providers. Uses existing provider sign-ins, consumes
their allowance, and leaves logs and workspaces under the requested temporary
directory. It never configures forge credentials or publishes to GitHub.
"""
import argparse
import json
from pathlib import Path
import socket
import sqlite3
import sys
import time

from hubtools import Hub, Procs, read_credentials, start_hub, wait

REMOTE = "https://github.com/binbandit/yip-simulation-public.git"
CHECK = "python3 -m unittest discover -s tests -v"


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


CAMPAIGN = (" This campaign uses a small Python session service. Keep changes focused. "
            "Use the recorded checks and independent review process. Do not push, "
            "publish remote reviews, create a pull request, merge, or deploy anything.")


def configure(hub):
    """Creates the team: Mira on Codex and Oren on Claude Code, the Session
    service project, and the Security and Engineering rooms."""
    mira = hub.req("POST", "/v1/engineers", {
        "name": "Mira", "role": "Platform engineer", "capabilityTags": ["python", "backend", "sessions"],
        "provider": {"provider": "codex"},
        "instructions": "Preserve established contracts. Prefer small, well-tested changes. "
            "Get an independent review for security-sensitive code." + CAMPAIGN,
    })
    oren = hub.req("POST", "/v1/engineers", {
        "name": "Oren", "role": "Security engineer", "capabilityTags": ["security", "review", "auth"],
        "provider": {"provider": "claude"},
        "instructions": "Review the actual revision and surrounding code. Distinguish blocking defects "
            "from suggestions. Never approve what you could not check." + CAMPAIGN,
    })
    project = hub.req("POST", "/v1/projects", {
        "name": "Session service",
        "instructions": "A session is valid only while now is strictly before expires. "
            "At the exact expiry time it is expired. This project uses Python's standard "
            f"library only. Record `{CHECK}` on the published revision. "
            "An independent peer must review that exact revision before completion. "
            "Keep all resulting commits local to yip; no external writes are authorized.",
        "policy": {"requirePeerReview": True, "requireHumanReview": False,
                   "autoPublish": False, "checks": [CHECK],
                   "executionProfile": "native", "requires": ["python3"]},
        "repos": [{"name": "session-service", "remoteUrl": REMOTE, "defaultBranch": "main",
                   "forge": "github", "forgeRepo": "binbandit/yip-simulation-public"}],
    })
    for engineer, access in ((mira, "write"), (oren, "read")):
        hub.req("PUT", f"/v1/projects/{project['id']}/grants/{engineer['id']}", {"access": access, "actions": []})
    hub.req("POST", "/v1/rooms", {"name": "Security", "replyMode": "quiet",
        "engineerIds": [mira["id"], oren["id"]], "projectIds": [project["id"]]})
    hub.req("POST", "/v1/rooms", {"name": "Engineering", "replyMode": "steward", "stewardId": mira["id"],
        "engineerIds": [mira["id"]], "projectIds": [project["id"]]})
    hub.boot = hub.req("GET", "/v1/bootstrap")


def capture(hub, directory):
    jobs = hub.jobs()
    messages = hub.messages("Security")
    ids = {job["id"] for job in jobs}
    ids.update(run["jobId"] for run in hub.req("GET", "/v1/runs"))
    ids.update(message["jobId"] for message in messages if message.get("jobId"))
    for message in messages:
        ids.update(ref["id"] for ref in message.get("refs") or [] if ref["kind"] == "job")
    details = [hub.req("GET", "/v1/jobs/" + job_id) for job_id in sorted(ids)]
    save(directory / "jobs.json", details)
    save(directory / "messages.json", messages)
    save(directory / "nodes.json", hub.nodes())
    activity = {}
    for detail in details:
        for run in detail.get("runs") or []:
            activity[run["id"]] = hub.req(
                "GET", f"/v1/jobs/{detail['job']['id']}/runs/{run['id']}/activity")
    save(directory / "activity.json", activity)
    return details


def completed_evidence(details):
    runs = {run["id"]: run for detail in details for run in detail.get("runs") or []}
    for detail in details:
        job = detail["job"]
        if job["kind"] != "code" or job["state"] != "completed":
            continue
        head = (job.get("revision") or {}).get("head")
        checked = any(c["passed"] and c["command"] == CHECK and c["revision"] == head
                      for c in detail.get("checks") or [])
        reviewed = any(r["reviewerId"] != job["ownerId"] and any(
            rd["state"] == "approved" and rd["target"].get("head") == head
            and runs.get(rd.get("reviewerRunId"), {}).get("provider") == "claude"
            for rd in r.get("rounds") or []) for r in detail.get("reviews") or [])
        authored = any(run["provider"] == "codex" and run["mode"] == "edit"
                       for run in detail.get("runs") or [])
        if head and checked and reviewed and authored:
            return {"jobId": job["id"], "head": head, "summary": job.get("summary", "")}
    return None


def recall(hub, directory, evidence, deadline):
    engineer = hub.engineer("Oren")
    room = hub.room("Engineering")
    hub.req("PUT", f"/v1/rooms/{room['id']}/members/{engineer['id']}", {})
    request = hub.post("Engineering", "@Oren where did we land on the session expiry work? "
        "Summarize the actual completed change, its regression coverage, and the approved "
        "revision from our saved work record. This is a recall question only; do not create "
        "or modify work.", ["Oren"])
    result = {"passed": False, "reason": "recall reached its time bound"}
    while time.monotonic() < deadline:
        messages = hub.messages("Engineering")
        save(directory / "recall-messages.json", messages)
        answers = [m for m in messages if m["seq"] > request["message"]["seq"]
                   and m["author"]["id"] == engineer["id"] and m.get("jobId")]
        if answers:
            detail = hub.req("GET", "/v1/jobs/" + answers[-1]["jobId"])
            save(directory / "recall-job.json", detail)
            if detail["job"]["state"] == "completed" and all(r["state"] == "succeeded" for r in detail["runs"]):
                body = "\n".join(m["body"] for m in answers)
                run = detail["runs"][-1]
                activity = hub.req("GET", f"/v1/jobs/{detail['job']['id']}/runs/{run['id']}/activity")
                save(directory / "recall-activity.json", activity)
                with sqlite3.connect(f"file:{directory / 'hub' / 'hub.db'}?mode=ro", uri=True) as db:
                    manifest = json.loads(db.execute("SELECT manifest FROM runs WHERE id = ?", (run["id"],)).fetchone()[0])
                save(directory / "recall-manifest.json", manifest)
                sourced = any(n.get("kind") == "record" and evidence["head"][:8] in json.dumps(n)
                              for n in manifest.get("notes") or [])
                text = body.lower()
                accurate = (evidence["head"][:7] in text and "expir" in text
                            and ("test" in text or "regression" in text))
                result = {"passed": run["provider"] == "claude" and sourced and accurate,
                          "reason": "checked recall against completed work record and approved revision",
                          "body": body, "provider": run["provider"], "sourcedWorkRecord": sourced}
                break
        time.sleep(2)
    save(directory / "recall-result.json", result)
    print(json.dumps({"recall": result}), flush=True)
    return result


def run(args):
    directory = Path(args.dir).resolve()
    if not str(directory).startswith("/private/tmp/yip-real-team-"):
        raise SystemExit("Use a new /private/tmp/yip-real-team-* directory")
    if directory.exists():
        raise SystemExit("Refusing to reuse existing campaign data; choose a new directory")
    for port in (7961, 7984):
        with socket.socket() as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            sock.bind(("127.0.0.1", port))
    directory.mkdir(parents=True)
    log = directory / "hub.log"
    procs = Procs(str(directory))
    start = time.monotonic()
    result = {"passed": False, "reason": "campaign did not finish", "remote": REMOTE}
    hub = None
    try:
        base = start_hub(procs, directory / "hub", log, 7961, 7984, "codex,claude", directory / "credentials.txt")
        creds = read_credentials(directory / "credentials.txt")
        hub = Hub(base, creds["handle"], creds["password"])
        configure(hub)
        def providers_ready():
            hub.boot = hub.req("GET", "/v1/bootstrap")
            ready = {p["provider"] for p in hub.boot["providers"] if p.get("readyNodes")}
            return {"codex", "claude"}.issubset(ready)
        wait("the Codex and Claude readiness reports", providers_ready, timeout=60)
        save(directory / "configuration.json", {
            "engineers": [e for e in hub.boot["engineers"] if e["name"] in ("Mira", "Oren")],
            "projects": hub.boot["projects"], "providers": hub.boot["providers"],
        })
        recipient = "Oren" if args.delegate_through_reviewer else "Mira"
        request = ("@Oren please delegate this coding assignment to Mira, who will own the "
                   "implementation, and independently review her resulting revision when asked. "
                   if args.delegate_through_reviewer else "@Mira please ")
        hub.post("Security", request + "fix the expiry-boundary bug in Session service's "
            "session-service repository. A session must be rejected when now equals expires; "
            "before expiry it remains valid, and after expiry it is invalid. Add a regression "
            "test for the exact boundary, preserve the existing cases, and finish the work with "
            "recorded passing checks and independent peer review. The intended behavior is "
            "fully specified here; no release decision is needed. Keep the work local to yip "
            "and do not push, open a PR, publish externally, merge, or deploy.", [recipient])
        previous = None
        while time.monotonic() - start < args.seconds:
            details = capture(hub, directory)
            state = [(d["job"]["title"], d["job"]["state"], d["job"].get("waitingReason", ""))
                     for d in details]
            if state != previous:
                print(json.dumps({"elapsed": round(time.monotonic() - start), "jobs": state}), flush=True)
                previous = state
            evidence = completed_evidence(details)
            if evidence and not hub.req("GET", "/v1/runs"):
                result = {"passed": True, "reason": "autonomous code, checks and independent review completed",
                          "remote": REMOTE, **evidence}
                save(directory / "primary-result.json", result)
                if args.recall:
                    result["recall"] = recall(hub, directory, evidence,
                        min(start + args.seconds, time.monotonic() + 120))
                    if not result["recall"]["passed"]:
                        result["passed"] = False
                        result["reason"] = "primary work passed; cross-room recall failed"
                break
            pending = [a for d in details for a in d.get("approvals") or [] if a["status"] == "pending"]
            questions = [q for d in details for q in d.get("questions") or [] if q["status"] == "open"]
            if pending or questions:
                result["reason"] = "owner intervention requested"
                result["approvals"] = pending
                result["questions"] = questions
                break
            if any(d["job"]["state"] == "failed" for d in details):
                result["reason"] = "a job failed; preserved activity contains the cause"
                break
            time.sleep(2)
        else:
            result["reason"] = "campaign reached its wall-time bound"
    except BaseException as exc:
        result["passed"] = False
        result["reason"] = str(exc)
        if isinstance(exc, KeyboardInterrupt):
            result["reason"] = "campaign interrupted"
    finally:
        if hub is not None:
            try:
                capture(hub, directory)
            except BaseException as exc:
                result["captureError"] = str(exc)
        procs.stop("hub")
        result["elapsedSeconds"] = round(time.monotonic() - start, 1)
        save(directory / "result.json", result)
        print(json.dumps(result), flush=True)
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-real-providers", action="store_true")
    parser.add_argument("--dir", default="/private/tmp/yip-real-team-20260928")
    parser.add_argument("--seconds", type=int, default=600)
    parser.add_argument("--delegate-through-reviewer", action="store_true",
                        help="Claude receives the request and delegates implementation to Codex")
    parser.add_argument("--recall", action="store_true",
                        help="after settled completion, ask Claude to recall it in another room")
    args = parser.parse_args()
    if not args.run_real_providers:
        parser.error("real model use must be explicitly enabled with --run-real-providers")
    if not 1 <= args.seconds <= 600:
        parser.error("--seconds must be between 1 and 600")
    sys.exit(run(args))
