---
name: file-pr
description: File a concise pull request. Use when the user asks to file, open, or create a PR or pull request.
license: MIT
metadata:
  author: binbandit
  harness: cursor,claude,codex,pi
---

# File PR

Review the diff against the repo's default branch, or the base the user named, and make sure its contents match the goal. Rebase onto that base. Commit only the work that belongs in the PR.

If an open PR already tracks this branch, push and update it. Otherwise push and open a PR. Return the URL either way.

PR titles should follow the repository's title conventions.
Look at recently merged PRs and Git history for examples.
Prefer a concise, human-readable title that explains why the changes matter:

BAD
> ❌ perf(server): negotiate permessage-deflate on the websocket

GOOD
> ✅ perf(server): cut websocket frame size by 70%+ with gzipping

Open the description with a simple explanation of the problem based on the
user's original prompt, then briefly explain the solution. Do not lead with
an implementation inventory:

BAD
> ❌ Removed implicit workspace carry-over from every "new thread" entry
point (cmd+n / cmd+shift+o, sidebar v1/v2 buttons, command palette). New
threads inherit only the project from context; branch, worktree, and env
mode always come from the configured defaults. Deleted buildContextualThreadOptions,
startNewThreadInProjectFromContext, and the v1 sidebar's seed-context machinery.

GOOD
> ✅ My "new worktree" default was ignored when starting new threads on existing
worktrees. Super unintuitive. Now your preferences always apply: new threads
keep the project from context and use your configured defaults for branch,
worktree, and env mode.

If the change is something a reviewer can see, attach a screenshot or short video where it explains the change. Reference it in the body as `![what it shows](./shot.png)` and pass that same path to `--attach` on `gh pr create` or `gh pr edit`. Alt text goes after `#`: `--attach './shot.png#What it shows'`. `gh` rewrites a matching body reference to the uploaded file. An attachment the body never mentions is appended at the end, so put the reference where it belongs. Skip images when the change has nothing to show.

Open a real PR so review bots run. Open a draft only if the user asked for one. If the user also asked to babysit it, continue with the `babysit-pr` skill.
