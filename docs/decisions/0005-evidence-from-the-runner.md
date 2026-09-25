# 0005 — Evidence comes from the runner

**Decision.** A check counts as evidence only when the runner executed it via
`work_run_check` and recorded the command, exit code, exact revision, and log
artifact. A check run on uncommitted changes is recorded against
`<head>+uncommitted` and never satisfies a published revision. Revisions count
only when published through `work_publish_revision`, which commits outstanding
changes as the engineer, then uploads the diff and a git bundle whose hashes
the hub verifies before recording.

**Why.** "A confident paragraph is not completion." Model-reported test
results are claims; runner-executed results are evidence. The runner (not the
model) commits because some provider sandboxes (Codex) keep `.git` read-only.

**Consequence.** Completion policy (A20, A32, A38, A39) is enforced by the
hub from these records; reviewers on another machine reproduce the exact
revision from the bundle.
