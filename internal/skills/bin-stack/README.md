# Bundled bin-stack

Source: https://github.com/binbandit/bin-stack

Pinned revision: `e71d506b83b9197fb6ec37dca1624d9ce51b05e4`.

The four `skills/*/SKILL.md` files and `LICENSE` are verbatim upstream files.
Yip embeds them in its binary and serves skill bodies through the read-only
`skill_read` tool. It does not fetch updates or execute an installer at runtime.

To update, select and review an upstream commit, copy those exact files, then
update `skills.Revision` and the integrity hashes in `skills_test.go`. Preserve
the upstream license. Review any new script or reference dependencies before
expanding this Markdown-only bundle. The revision participates in provider
session fingerprints so a changed bundle starts a fresh session.
