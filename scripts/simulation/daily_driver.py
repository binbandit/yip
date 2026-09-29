#!/usr/bin/env python3
"""Opt-in real-provider daily cycle on the preserved isolated team workspace.

Restarts the hub, checks previous evidence, cancels one real Codex edit attempt,
explicitly retries with clarification, requires independent Claude review, and
restarts again to check durability. No remote publication or account changes.
"""
import argparse
import json
from pathlib import Path
import subprocess
import sys
import time
import uuid

import hubtools
from hubtools import Hub, Procs, read_credentials
from real_team import capture, completed_evidence, save


def until(label, condition, deadline):
    while time.monotonic() < deadline:
        result = condition()
        if result:
            return result
        time.sleep(1)
    raise RuntimeError("Timed out: " + label)


def start_hub(directory, output, procs, deadline):
    credentials = directory / "credentials.txt"
    if not credentials.exists():
        raise RuntimeError("The team campaign's credentials.txt is missing")
    base = hubtools.start_hub(procs, directory / "hub", output / "hub.log", 7961, 7984, "codex,claude", credentials)
    creds = read_credentials(credentials)
    hub = Hub(base, creds["handle"], creds["password"])
    def ready():
        hub.boot = hub.req("GET", "/v1/bootstrap")
        return {"codex", "claude"}.issubset(
            {p["provider"] for p in hub.boot["providers"] if p.get("readyNodes")})
    until("provider readiness after restart", ready, deadline)
    return hub


def verify_completed(hub, expected):
    detail = hub.req("GET", "/v1/jobs/" + expected["jobId"])
    details = [detail] + [hub.req("GET", "/v1/jobs/" + child["id"]) for child in detail["children"]]
    verify_completed_details(details, expected)
    return detail


def verify_completed_details(details, expected):
    evidence = completed_evidence(details)
    if not evidence or any(evidence[key] != expected[key] for key in ("jobId", "head")):
        raise RuntimeError("Completed revision, checks or independent review did not survive restart")


def successful_edit(activities):
    return next((a for a in activities if a["kind"] == "tool_finished" and a.get("tool") == "edit"
                 and (a.get("data") or {}).get("status") == "completed"), None)


def run_workspace(activities):
    paths = {a["data"]["workspace"] for a in activities
             if a["kind"] == "started" and (a.get("data") or {}).get("workspace")}
    if len(paths) != 1:
        raise RuntimeError("Run does not identify one unambiguous workspace")
    return Path(paths.pop())


def git_output(workspace, *args):
    return subprocess.check_output(["git", *args], cwd=workspace, text=True)


def verify_retained_work(cancelled, completed, activities, run_id):
    original = next(r for r in cancelled["runs"] if r["id"] == run_id)
    if cancelled["job"]["state"] != "cancelled" or original["state"] != "cancelled":
        raise RuntimeError("The interrupted job and run were not confirmed cancelled")
    edit = successful_edit(activities[run_id])
    if not edit or edit["at"] > original["endedAt"]:
        raise RuntimeError("No successful file edit was recorded before cancellation")
    resumed = next((r for r in completed["runs"] if r.get("previousRunId") == run_id
                    and r["provider"] == "codex" and r["mode"] == "edit" and r["state"] == "succeeded"), None)
    if not resumed or not original.get("vendorSessionId") or any(
            resumed.get(key) != original.get(key) for key in ("vendorSessionId", "nodeId", "branch", "baseRev")):
        raise RuntimeError("Retry did not continue the interrupted Codex session on its original machine")
    workspace = run_workspace(activities[run_id])
    if run_workspace(activities[resumed["id"]]) != workspace:
        raise RuntimeError("Retry did not reuse the interrupted workspace")
    # This scenario resumes on the same machine. The runner's local checkpoint
    # proves the helper existed at cancellation; it is not a portable restore.
    ref = "refs/yip/checkpoints/" + run_id.rsplit("-", 1)[-1]
    checkpoint = git_output(workspace, "rev-parse", "--verify", ref).strip()
    subject = git_output(workspace, "show", "-s", "--format=%s", checkpoint).strip()
    if subject != "yip checkpoint for run " + run_id:
        raise RuntimeError("Checkpoint does not belong to the cancelled attempt")
    helper = git_output(workspace, "show", checkpoint + ":session_remaining.py")
    tests = git_output(workspace, "show", checkpoint + ":tests/test_remaining.py")
    head = completed["job"]["revision"]["head"]
    if not helper or not tests or helper != git_output(workspace, "show", head + ":session_remaining.py"):
        raise RuntimeError("The approved helper differs from the file retained at cancellation")
    return {"successfulEditSeq": edit["seq"], "resumedRunId": resumed["id"],
            "sameWorkspace": True, "sameVendorSession": True, "localCheckpoint": checkpoint,
            "helperRetainedAtApprovedHead": True}


def verify_records(before, after, expected):
    for evidence in expected:
        if not any(n.get("kind") == "record" and n.get("status") == "accepted"
                   and evidence["head"][:8] in n.get("body", "")
                   and any(s["kind"] == "job" and s["id"] == evidence["jobId"]
                           for s in n.get("sources") or []) for n in before):
            raise RuntimeError("A completed job has no accepted reviewer record for its revision")
    if {n["id"]: n for n in before} != {n["id"]: n for n in after}:
        raise RuntimeError("Reviewer work record content did not survive restart")


def inspect_feature(detail, activities):
    latest = max((r for r in detail["runs"] if r["provider"] == "codex" and r["mode"] == "edit"
                  and r["state"] == "succeeded"), key=lambda r: r["attempt"])
    workspace = run_workspace(activities[latest["id"]])
    revision = detail["job"]["revision"]
    if git_output(workspace, "rev-parse", "HEAD").strip() != revision["head"]:
        raise RuntimeError("The inspected workspace is not on the reviewed revision")
    if git_output(workspace, "status", "--porcelain=v1", "--untracked-files=all").strip():
        raise RuntimeError("The inspected workspace has changes outside the reviewed revision")
    source = git_output(workspace, "show", revision["head"] + ":session_remaining.py")
    namespace = {}
    exec(compile(source, revision["head"] + ":session_remaining.py", "exec"), namespace)
    for now, expires, want in [(9, 10, 1), (10, 10, 0), (11, 10, 0), (9.25, 10.0, 0.75)]:
        got = namespace["remaining_seconds"](now, expires)
        if got != want:
            raise RuntimeError(f"remaining_seconds({now}, {expires})={got}; wanted {want}")
    diff = git_output(workspace, "diff", revision["base"], revision["head"], "--",
                      "session.py", "tests/test_session.py")
    if diff:
        raise RuntimeError("Independent feature unexpectedly altered the separate expiry-fix files")
    return {"workspace": str(workspace), "behaviorCasesPassed": 4, "originalFilesUnchanged": True,
            "reviewedHeadChecked": True, "workspaceClean": True,
            "base": revision["base"], "head": revision["head"]}


def run(args):
    directory = Path(args.dir).resolve()
    if not str(directory).startswith("/private/tmp/yip-real-team-"):
        raise SystemExit("Only a preserved /private/tmp/yip-real-team-* workspace may be used")
    prior = json.loads((directory / "primary-result.json").read_text())
    if not prior.get("passed"):
        raise SystemExit("The primary team campaign must have passed first")
    output = directory / "daily-cycle"
    output.mkdir(exist_ok=False)
    procs = Procs(str(output))
    started = time.monotonic()
    deadline = started + args.seconds
    result = {"passed": False, "priorJobId": prior["jobId"], "priorHead": prior["head"]}
    hub = None
    try:
        hub = start_hub(directory, output, procs, deadline)
        previous = verify_completed(hub, prior)
        save(output / "prior-after-first-restart.json", previous)
        history = hub.messages("Security")
        prior_messages = [m for m in history if m.get("jobId") == prior["jobId"]
                          or any(r["kind"] == "job" and r["id"] == prior["jobId"] for r in m.get("refs") or [])]
        if not prior_messages:
            raise RuntimeError("Prior work history did not survive restart")
        result["firstRestartPassed"] = True
        source = prior_messages[-1]
        room = hub.room("Security")
        hub.req("POST", f"/v1/rooms/{room['id']}/messages", {
            "body": "@Oren please delegate a new, independent feature to Mira: add session_remaining.py "
                "with remaining_seconds(now, expires), returning the positive time remaining before "
                "expiry and zero at or after expiry. Add tests/test_remaining.py for before, exact, "
                "and after expiry. Do not edit session.py or tests/test_session.py: the earlier "
                "expiry correction is still an unpublished local result, and this new assignment "
                "starts from the repository's default branch. Keep both results separate. Finish "
                "with recorded checks and independent review. No push, PR, remote review, merge, or deploy.",
            "mentions": [{"kind": "engineer", "id": hub.engineer("Oren")["id"]}],
            "replyToId": source["id"], "clientKey": str(uuid.uuid4()),
        })
        # The code assignment is created asynchronously by Claude, not this harness.
        def new_work():
            details = capture(hub, output)
            return next((d for d in details if d["job"]["kind"] == "code"
                         and d["job"]["id"] != prior["jobId"] and "remaining" in d["job"]["title"].lower()), None)
        detail = until("Claude delegates the second feature", new_work, deadline)
        job_id = detail["job"]["id"]
        result["jobId"] = job_id
        result["followsId"] = detail["job"].get("followsId")
        if result["followsId"] != prior["jobId"]:
            raise RuntimeError("Reply-to-result follow-up link was lost")
        def after_edit():
            detail = hub.req("GET", "/v1/jobs/" + job_id)
            save(output / "before-cancel.json", detail)
            if detail["job"]["state"] == "failed":
                raise RuntimeError(detail["job"].get("stateDetail", "second job failed"))
            for run in detail["runs"]:
                if run["provider"] == "codex" and run["mode"] == "edit" and run["state"] == "running":
                    acts = hub.req("GET", f"/v1/jobs/{job_id}/runs/{run['id']}/activity")
                    save(output / "before-cancel-activity.json", acts)
                    if successful_edit(acts):
                        return run
            return None
        interrupted = until("actual Codex file edit before cancellation", after_edit, deadline)
        result["cancelledRunId"] = interrupted["id"]
        hub.req("POST", f"/v1/jobs/{job_id}/cancel", {"reason": "Daily-cycle test: stop once after an actual edit", "includeChildren": True})
        def stopped():
            detail = hub.req("GET", "/v1/jobs/" + job_id)
            run = next(r for r in detail["runs"] if r["id"] == interrupted["id"])
            return detail if detail["job"]["state"] == "cancelled" and run["state"] == "cancelled" else None
        cancelled = until("confirmed cancellation", stopped, deadline)
        save(output / "cancelled.json", cancelled)
        rejected_body = "This cancelled input must not be consumed"
        try:
            hub.req("POST", f"/v1/jobs/{job_id}/input", {"body": rejected_body, "clientKey": str(uuid.uuid4())})
        except SystemExit as exc:
            if "HTTP 409" not in str(exc):
                raise
            result["cancelledInputRejected"] = True
        else:
            raise RuntimeError("Cancelled work accepted input instead of requiring explicit retry")
        if any(m["body"] == rejected_body for m in hub.messages("Security")):
            raise RuntimeError("Rejected input left a misleading message in the conversation")
        hub.req("POST", f"/v1/jobs/{job_id}/retry", {"fromCheckpoint": True,
            "reason": "Explicitly resume the interrupted local work from its retained workspace"})
        clarification = hub.req("POST", f"/v1/jobs/{job_id}/input", {
            "body": "Clarification for the resumed feature: support fractional timestamps without rounding. "
                "remaining_seconds(9.25, 10.0) must return 0.75. Add a regression for that case. "
                "Keep session.py and its existing tests untouched, and finish the normal peer review.",
            "clientKey": str(uuid.uuid4()),
        })
        result["clarificationInputId"] = clarification["input"]["id"]
        def completion():
            details = capture(hub, output)
            relevant = [d for d in details if d["job"]["id"] == job_id or d["job"].get("parentId") == job_id]
            for d in relevant:
                if d["job"]["state"] == "failed":
                    raise RuntimeError(d["job"].get("stateDetail", "recovery failed"))
                if any(a["status"] == "pending" for a in d.get("approvals") or []):
                    raise RuntimeError("Recovery requested owner permission; evidence preserved")
                if any(q["status"] == "open" for q in d.get("questions") or []):
                    raise RuntimeError("Recovery asked an owner question; evidence preserved")
            evidence = completed_evidence(relevant)
            return evidence if evidence and not hub.req("GET", "/v1/runs") else None
        final = until("retried work completes with independent review and settled sessions", completion, deadline)
        detail = verify_completed(hub, final)
        if not any(i["id"] == result["clarificationInputId"] and i.get("deliveredAt") for i in detail["inputs"]):
            raise RuntimeError("Clarification was not recorded as delivered")
        activities = {r["id"]: hub.req("GET", f"/v1/jobs/{job_id}/runs/{r['id']}/activity")
                      for r in detail["runs"]}
        result["retainedWork"] = verify_retained_work(cancelled, detail, activities, interrupted["id"])
        result["feature"] = inspect_feature(detail, activities)
        result["head"] = final["head"]
        result["recoveryPassed"] = True
        save(output / "completed-before-restart.json", detail)
        final_history = {m["id"] for m in hub.messages("Security")}
        notes = hub.req("GET", f"/v1/engineers/{hub.engineer('Oren')['id']}/notes")
        save(output / "notes-before-restart.json", notes)
        procs.stop("hub")
        hub = start_hub(directory, output, procs, deadline)
        save(output / "prior-after-final-restart.json", verify_completed(hub, prior))
        save(output / "completed-after-restart.json", verify_completed(hub, final))
        if not final_history.issubset({m["id"] for m in hub.messages("Security")}):
            raise RuntimeError("Conversation history did not survive the final restart")
        restored = hub.req("GET", f"/v1/engineers/{hub.engineer('Oren')['id']}/notes")
        save(output / "notes-after-restart.json", restored)
        verify_records(notes, restored, [prior, final])
        result["finalRestartPassed"] = True
        result["passed"] = True
    except BaseException as exc:
        result["reason"] = str(exc)
    finally:
        if hub is not None:
            try:
                capture(hub, output)
            except BaseException as exc:
                result["captureError"] = str(exc)
        procs.stop("hub")
        result["elapsedSeconds"] = round(time.monotonic() - started, 1)
        save(output / "result.json", result)
        print(json.dumps(result), flush=True)
    return 0 if result["passed"] else 1


def verify_saved(directory):
    """Recheck preserved evidence locally without starting a hub or provider."""
    output = Path(directory).resolve() / "daily-cycle"
    def read(name):
        return json.loads((output / name).read_text())
    original = read("result.json")
    if not original.get("passed"):
        raise RuntimeError("The saved daily cycle did not pass")
    jobs = {d["job"]["id"]: d for d in read("jobs.json")}
    for filename, expected in [
            ("prior-after-first-restart.json", {"jobId": original["priorJobId"], "head": original["priorHead"]}),
            ("prior-after-final-restart.json", {"jobId": original["priorJobId"], "head": original["priorHead"]}),
            ("completed-before-restart.json", original), ("completed-after-restart.json", original)]:
        detail = read(filename)
        verify_completed_details([detail] + [jobs[c["id"]] for c in detail["children"]], expected)
    activities = read("activity.json")
    activities[original["cancelledRunId"]] = read("before-cancel-activity.json")
    detail = read("completed-after-restart.json")
    retained = verify_retained_work(read("cancelled.json"), detail, activities, original["cancelledRunId"])
    feature = inspect_feature(detail, activities)
    notes = read("notes-before-restart.json")
    verify_records(notes, read("notes-after-restart.json"), [original,
        {"jobId": original["priorJobId"], "head": original["priorHead"]}])
    result = {"passed": True, "retainedWork": retained, "feature": feature,
              "reviewerRecordsPersisted": len(notes), "providersStarted": False}
    save(output / "audit-result.json", result)
    print(json.dumps(result), flush=True)
    return 0


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--run-real-providers", action="store_true")
    mode.add_argument("--verify-saved", action="store_true",
                      help="recheck preserved evidence without starting any hub or provider")
    parser.add_argument("--dir", default="/private/tmp/yip-real-team-20260928-final")
    parser.add_argument("--seconds", type=int, default=720)
    args = parser.parse_args()
    if not 1 <= args.seconds <= 720:
        parser.error("--seconds must be between 1 and 720")
    sys.exit(verify_saved(args.dir) if args.verify_saved else run(args))
