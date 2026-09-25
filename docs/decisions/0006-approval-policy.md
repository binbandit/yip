# 0006 — Approval policy mapping

**Decision.** Provider permission requests are normalised to an action
(`read`, `edit`, `exec`, `network`, `push`, `merge`, `publish`, `mcp`,
`other`) and evaluated against existing grants before anyone is asked:

- Reads, yip tools, and edits inside the job's own worktree (with write
  access) proceed; edits in read-only runs are denied.
- Commands in the worktree proceed, except: `git push` (needs the `push`
  grant), merges into the default branch (`merge`), `gh pr create`
  (`open_pr`), `gh pr review` (denied — use `forge_publish_review`), and a
  conservative list of dangerous or networked commands, which become an
  **exceptional, exact-action request** in the source conversation.
- Anything unrecognised becomes an exceptional request.

Exceptional requests are single-use, digest-bound, expire after 15 minutes
with no renewal, require the current `version`, and are recorded as consumed
when the runner confirms delivery. Engineers have no tool to decide approvals;
message text never approves anything.

**Why.** "Apply existing permission grants before requesting additional
authority" while never auto-approving from vague matching.

**Consequence.** Command classification is pattern-based and deliberately
conservative; false positives become owner questions, not silent approvals.
A16 and A35 are covered by `TestExactActionApprovals`.
