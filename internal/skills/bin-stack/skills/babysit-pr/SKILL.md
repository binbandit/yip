---
name: babysit-pr
description: Monitor a pull request through review and CI. Use when the user asks to monitor, watch or babysit a PR.
license: MIT
metadata:
  author: binbandit
  harness: cursor,claude,codex,pi
---

# Babysit PR

All the repos we work in have various AI review bots. They're helpful, even if they are not always right.

If your harness offers tools to monitor a PR, use them so you can respond when comments arrive. Otherwise poll the PR for new comments and checks.

Only act on checks and comments newer than the latest push. Ignore your own "AI reply on behalf of Brayden" comments. After you push a fix, wait for bots on that new commit before answering again.

Verify every bot finding against the source before changing code. Fix real findings, then commit and push. Rerun an infrastructure flake and leave the code alone. Reply with a written reason when dismissing a false positive.

A human comment gets reported. The reply format below is for bots.

Keep an eye on the PR's base branch and rebase onto it when it moves.
If an overlapping PR makes this one obsolete, stop monitoring, report it to the user, and ask before closing the PR unless closure was explicitly authorized.

If a review bot leaves feedback you believe is not worth addressing, reply and resolve the comment. A bot question that needs nothing can just be resolved, with no reply. Format comments left on Brayden's behalf as:

```
###### AI reply on behalf of Brayden - [dismissed | fixed]

[actual reply]
```

Use `dismissed` for a false positive and `fixed` when the change is in. The reply is the reason a reviewer can check. For a fix, say what changed and cite the commit. Six hashes keep the byline smaller than the reply.

When a reply is about something a reviewer can see, attach a screenshot or short video with `gh pr comment --attach './shot.png#What it shows'`. Reference it in the comment where it belongs, as `![what it shows](./shot.png)`, so `gh` rewrites that spot instead of appending the file at the end. If a new image replaces an out-of-date one, edit that comment or the PR body in the same update, drop the old image reference, and put the new image where the old one was. An attachment the text never mentions is appended, so both stay up unless the old reference is removed. Skip images when there is nothing to show.

Do not let review feedback expand the PR beyond the user's original goal.
Address real shortcomings, but avoid scope creep.

If nothing has changed, stay quiet rather than posting filler comments.
Stop when the review bots and required checks are green on the latest commit.
Merge only when the user explicitly requested it; otherwise report that the PR is ready.
