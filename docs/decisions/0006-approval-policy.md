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
  - Exceptional (`exec`): a program or git/gh action computed at run time,
    git configuration that runs commands
    (`alias.*`, `core.sshCommand`, `credential.*`, hooks, filters),
    privilege escalation, remote shells, package or image publication,
    infrastructure changes, and writes or deletions outside the workspace.
  - Exceptional (`network`): URLs and remote specs, `curl`/`wget`/`nc` and
    similar, `gh api`, adding or changing git remotes.
  - A local `git merge` is routine: it only changes the job's own branch,
    and publishing it still needs a push.
  - Inline code (`sh -c`, `python -c`, `eval`, a script on stdin) is
    exceptional in the engineer's own shell, which runs with the owner's
    credentials. In `work_run_check`, which runs in the isolated check
    environment (ADR 0005), it is routine: it is no riskier there than the
    project's own test suite. Its explicit actions are still classified, so
    `sh -c 'git push'` still needs the push grant.
  - Command and process substitutions are parsed and their commands
    classified like any other; their output is an unknown value, like a
    variable, so it can't pick the program or the git/gh action.
  - `gh pr create/merge --repo owner/name` is not "another repository" when
    it names the work's own GitHub repository.
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
become owner questions, not silent approvals. Covered by
`TestExactActionApprovals`, `TestRegressionRunCheckIsPolicedAndIsolated`, and
the classifier unit tests in `internal/hub/policy_test.go`.
