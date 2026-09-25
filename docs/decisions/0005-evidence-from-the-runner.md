# 0005 — Evidence comes from the runner

**Decision.** A check counts as evidence only when the runner executed it via
`work_run_check` and recorded the command, exit code, exact revision, and log
artifact. A check run on uncommitted changes is recorded against
`<head>+uncommitted` and never satisfies a published revision. Revisions count
only when published through `work_publish_revision`, which commits outstanding
changes as the engineer, then uploads the diff and a git bundle whose hashes
the hub verifies before recording.

Because the runner executes checks, it treats them like any other command an
agent wants to run:

- `work_run_check` is offered only to runs with a repository workspace (edit
  and read-only runs), never to conversation replies.
- Before running, the runner asks the hub's permission policy (ADR 0006).
  Routine checks are allowed by the run's grants; anything else becomes an
  exceptional approval in the conversation.
- The command runs with a scratch `HOME` and `TMPDIR` (removed when the run
  ends), without `SSH_AUTH_SOCK`, per-user XDG config, user or system git
  configuration, or a git credential helper. Toolchain caches (Go, npm, Cargo)
  stay shared so builds stay fast.
- The runner's own git commands never run repository hooks or fsmonitor
  (`-c core.hooksPath=/dev/null -c core.fsmonitor=false`), because they run
  with the machine owner's credentials in a repository an agent can edit.
- Agents can publish only `document` or `file` artifacts; bundles,
  checkpoints, diffs, and logs are produced by the runner.

**Why.** "A confident paragraph is not completion." Model-reported test
results are claims; runner-executed results are evidence. The runner (not the
model) commits because some provider sandboxes (Codex) keep `.git` read-only.
Executing on the agent's behalf must not widen what the agent can reach.

**Consequence.** Completion policy (A20, A32, A38, A39) is enforced by the
hub from these records; reviewers on another machine reproduce the exact
revision from the bundle. A test suite that needs the owner's credentials
(for example a private module proxy reached over SSH) fails under
`work_run_check`; run it in the container profile with explicit
configuration instead.
