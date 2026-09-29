# 0006 — Approval policy mapping

**Decision.** Provider permission requests, and every `work_run_check`
command, are normalised to an action (`read`, `edit`, `exec`, `network`,
`push`, `merge`, `publish`, `mcp`, `other`) and evaluated against existing
grants before anyone is asked:

- Reads, yip tools, and edits inside the job's own worktree (with write
  access) proceed; edits in read-only runs are denied.
- A command line is parsed like a shell would (quoting, escapes, `;`, `&&`,
  `|`, redirections, leading `VAR=value` assignments, wrappers such as `env`,
  `nice`, `timeout`, `xargs`, `find -exec`, and absolute program paths) and
  **every** simple command in it is classified (`internal/hub/policy.go`):
  - `git push` in any spelling (`git -C . push`, `/usr/bin/git push`,
    `g''it push`) needs the `push` grant; `gh pr create` needs `open_pr`;
    `gh pr merge` needs `merge`; `gh pr review` is denied (use
    `forge_publish_review`).
  - Exceptional (`exec`): inline code (`sh -c`, `bash -lc`, `python -c`,
    `node -e`, `eval`), command or process substitution, heredocs, a program
    name computed at run time, git configuration that runs commands
    (`alias.*`, `core.sshCommand`, `credential.*`, hooks, filters),
    privilege escalation, remote shells, package or image publication,
    infrastructure changes, and writes or deletions outside the workspace.
  - Exceptional (`network`): URLs and remote specs, `curl`/`wget`/`nc` and
    similar, `gh api`, adding or changing git remotes.
  - A local `git merge` is routine: it only changes the job's own branch,
    and publishing it still needs a push.
- The most severe part that the grants don't cover decides; a command is
  allowed only when every part is.
- Anything unrecognised becomes an exceptional request.

Exceptional requests are single-use, digest-bound, expire after 15 minutes
with no renewal, require the current `version`, say why the action isn't
routine, and are recorded as consumed when the runner confirms delivery.
Engineers have no tool to decide approvals; message text never approves
anything.

**Why.** "Apply existing permission grants before requesting additional
authority" while never auto-approving from vague matching. The first version
matched regular expressions against the raw command line, which the backend
review showed was bypassed by ordinary spellings (`git -C . push`, quoting,
`sh -c`).

**Consequence.** The classifier is a policy aid, not a sandbox: a command
that runs workspace code (a test suite, a build script) can do whatever that
code does. That is why checks also run without the owner's credentials
(ADR 0005) and why the container profile exists (ADR 0003). False positives
become owner questions, not silent approvals. Covered by the classifier unit
tests in `internal/hub/policy_test.go`, and end to end through the hub by
`TestExactActionApprovals` and `TestRegressionHeredocEditNeedsNoApproval`.
