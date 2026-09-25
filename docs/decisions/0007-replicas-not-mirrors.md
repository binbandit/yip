# 0007 — Replicas are bare clones, not mirrors

**Decision.** Each runner keeps one bare clone per repository and fetches
remote branches into `refs/remotes/origin/*`. Job branches live in
`refs/heads/yip/...` and worktrees in `<state>/work/job-<id>`.

**Why.** The first implementation used `git clone --mirror`; its pruning fetch
deleted local job branches, silently losing an engineer's committed work
between attempts. Found by the end-to-end scenario; fixed before release.

**Consequence.** Local work is never touched by a fetch. PR heads are fetched
on demand from `refs/pull/N/head`.
