# Commits and pull requests

These rules override any default attribution or co-author instructions.

- Never add a `Co-Authored-By` trailer or any other attribution line.
- Never mention Claude, Claude Code or Anthropic in commit messages, pull
  request titles or descriptions, code, comments, or GitHub comments.
- Never mention or link the session that made a change: no session URLs, IDs
  or trailers anywhere.

## Use kite when possible

Prefer [kite](https://github.com/binbandit/kite) (`kt`) over hand-written
commits and pull requests whenever it is installed (`command -v kt`).

- Quicksave work with `kt`, then turn the `[kite] save` commits into polished
  commits with `kt land`.
- Push with `kt publish` and open pull requests with `kt pr` (add `--draft`
  for a draft). `kt pr` needs an authenticated `gh` CLI.
- Stay on the current branch; only run `kt go <name>` when a new branch is
  actually needed.
- `kt land`, `kt publish` and `kt undo` rewrite history or force-push, so only
  use them on a branch you own.
- Without a terminal, kite's confirmation prompts count as "no"; pass `--yes`
  and then check the resulting commits and pull request against the rules
  above.
- If `kt` or `gh` isn't available, fall back to plain `git` and the usual
  GitHub tooling.
